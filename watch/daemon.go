// Package watch provides a file system watcher daemon for live code graph updates
package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codemap/analysis"
	"codemap/config"
	"codemap/internal/projectpath"
	"codemap/limits"
	"codemap/scanner"
	"codemap/topology"

	"github.com/fsnotify/fsnotify"
)

var (
	buildTopologyGraph = topology.BuildGraph
	writeTopologyCache = topology.WriteCacheAt
	buildFileGraph     = scanner.BuildFileGraph
)

// Daemon is the watch daemon that keeps the graph updated
type Daemon struct {
	root       string
	configDir  string
	runtimeDir string
	graph      *Graph
	watcher    *fsnotify.Watcher
	gitCache   *scanner.GitIgnoreCache
	eventLog   string
	verbose    bool
	done       chan struct{}

	eventLoopWG  sync.WaitGroup
	publisher    *statePublisher
	closeWatcher func() error
}

func (d *Daemon) runtimeStateDir() (string, error) {
	if d.runtimeDir != "" {
		return d.runtimeDir, nil
	}
	return projectpath.CheckedRuntimeCodemapDir(d.root)
}

func (d *Daemon) ensurePublisher() error {
	if d.publisher != nil {
		return nil
	}
	runtimeDir, err := d.runtimeStateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return err
	}
	d.publisher = newStatePublisher(d, filepath.Join(runtimeDir, "state.json"), "legacy-test-instance")
	return nil
}

// NewDaemon creates a new watch daemon for the given root
func NewDaemon(root string, verbose bool) (*Daemon, error) {
	// Canonicalize so d.root, the runtime dir, and fsnotify paths agree.
	absRoot := projectpath.CanonicalPath(root)
	selection, err := projectpath.SelectRuntime(absRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime state: %w", err)
	}
	runtimeDir := filepath.Join(selection.RuntimeDir, "projects", projectpath.ProjectKey(selection.ProjectRoot))

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	gitCache := scanner.NewGitIgnoreCache(absRoot)

	isGitRepo := false
	if _, err := os.Stat(filepath.Join(absRoot, ".git")); err == nil {
		isGitRepo = true
	}

	d := &Daemon{
		root:         absRoot,
		configDir:    selection.PolicyDir,
		runtimeDir:   runtimeDir,
		watcher:      watcher,
		gitCache:     gitCache,
		verbose:      verbose,
		done:         make(chan struct{}),
		closeWatcher: watcher.Close,
		eventLog:     filepath.Join(runtimeDir, "events.log"),
		graph: &Graph{
			Root:            absRoot,
			Files:           make(map[string]*scanner.FileInfo),
			ConfiguredFiles: make(map[string]struct{}),
			DepCtx:          make(map[string]*DepContext),
			State:           make(map[string]*FileState),
			Events:          make([]Event, 0),
			WorkingSet:      NewWorkingSet(),
			IsGitRepo:       isGitRepo,
		},
	}
	instance, err := newDaemonInstance()
	if err != nil {
		watcher.Close()
		return nil, fmt.Errorf("create daemon identity: %w", err)
	}
	d.publisher = newStatePublisher(d, filepath.Join(runtimeDir, "state.json"), instance)

	return d, nil
}

// Start begins watching and returns immediately
func (d *Daemon) Start() error {
	if err := d.ensurePublisher(); err != nil {
		return fmt.Errorf("resolve runtime state: %w", err)
	}
	runtimeDir, err := d.runtimeStateDir()
	if err != nil {
		return fmt.Errorf("resolve runtime state: %w", err)
	}
	codemapDir := runtimeDir
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		return fmt.Errorf("failed to create .codemap dir: %w", err)
	}
	// Ensure the config directory exists; it is watched so config edits can
	// refresh the configured-file inventory.
	configDir := d.configDir
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create .codemap dir: %w", err)
	}

	// Initial full scan
	if err := d.fullScan(); err != nil {
		return fmt.Errorf("initial scan failed: %w", err)
	}

	// Compute dependency and topology graphs (best effort). Skip on very large
	// repos to avoid expensive startup memory/CPU spikes in background hook flows.
	fileCount := d.ConfiguredFileCount()
	d.computeInitialGraphs(fileCount)

	// Add directories to watcher
	if err := d.addWatchDirs(); err != nil {
		return fmt.Errorf("failed to add watch dirs: %w", err)
	}
	if err := d.watcher.Add(configDir); err != nil {
		return fmt.Errorf("failed to watch .codemap dir: %w", err)
	}
	if err := ensureControlDirectory(d.publisher.flushDir); err != nil {
		return fmt.Errorf("create flush directory: %w", err)
	}
	if err := d.watcher.Add(d.publisher.flushDir); err != nil {
		return fmt.Errorf("watch flush directory: %w", err)
	}

	// Write initial state for hooks to read immediately
	if err := d.publisher.publish(); err != nil {
		return fmt.Errorf("publish initial state: %w", err)
	}

	// Start event loop
	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()

	return nil
}

func (d *Daemon) computeInitialGraphs(fileCount int) {
	if fileCount > limits.LargeRepoFileCount {
		d.markGraphLifecycle(graphLifecycleSkippedSize)
		if d.verbose {
			fmt.Printf("[watch] Skipping dependency and topology graphs for large repo (%d files)\n", fileCount)
		}
		return
	}
	d.computeDeps()
	d.computeTopology()
}

func (d *Daemon) computeTopology() {
	start := time.Now()
	graph, identity, err := buildTopologyGraph(context.Background(), d.root)
	if err != nil {
		if d.verbose {
			fmt.Printf("[watch] Topology unavailable: %v\n", err)
		}
		return
	}

	d.graph.mu.Lock()
	d.graph.Topology = graph
	d.graph.TopologyIdentity = identity
	d.graph.mu.Unlock()

	if !topology.IsCacheable(graph) {
		return
	}
	envelope := topology.CacheEnvelope{
		Schema:      topology.CacheSchemaVersion,
		GeneratedAt: time.Now().UTC(),
		Identity:    identity,
		Graph:       graph,
	}
	if err := writeTopologyCache(d.runtimeDir, envelope); err != nil {
		if d.verbose {
			fmt.Printf("[watch] Topology cache unavailable: %v\n", err)
		}
		return
	}
	if d.verbose {
		fmt.Printf("[watch] Topology: %d modules in %v\n", len(graph.Nodes), time.Since(start))
	}
}

// Stop gracefully shuts down the daemon
func (d *Daemon) Stop() {
	close(d.done)
	d.eventLoopWG.Wait()
	_ = d.closeWatcher()
}

// GetGraph returns the current graph (thread-safe)
func (d *Daemon) GetGraph() *Graph {
	return d.graph
}

// GetEvents returns recent events (thread-safe)
func (d *Daemon) GetEvents(limit int) []Event {
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	events := d.graph.Events
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}

	// Return a copy
	result := make([]Event, len(events))
	copy(result, events)
	return result
}

// FileCount returns current tracked file count
func (d *Daemon) FileCount() int {
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	return len(d.graph.Files)
}

// ConfiguredFileCount returns the number of files included by the active
// project filters. FileCount intentionally continues to report all tracked
// files for watch/activity consumers.
func (d *Daemon) ConfiguredFileCount() int {
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	return len(d.graph.ConfiguredFiles)
}

func shouldComputeDependencyGraph(fileCount int) bool {
	return fileCount <= limits.LargeRepoFileCount
}

// WriteInitialState writes state after initial scan (for hooks)
func (d *Daemon) WriteInitialState() {
	if err := d.ensurePublisher(); err != nil {
		d.reportPublicationError(err)
		return
	}
	d.reportPublicationError(d.publisher.publish())
}

// fullScan does a complete scan of the project
func (d *Daemon) fullScan() error {
	start := time.Now()

	files, err := scanner.ScanFiles(context.Background(), d.root, d.gitCache, nil, nil)
	if err != nil {
		return err
	}
	configuredFiles, err := scanner.ScanConfiguredFiles(context.Background(), d.root, d.gitCache)
	if err != nil {
		return err
	}

	d.graph.mu.Lock()
	d.graph.Files = make(map[string]*scanner.FileInfo)
	d.graph.ConfiguredFiles = make(map[string]struct{}, len(configuredFiles))
	d.graph.State = make(map[string]*FileState)
	for i := range files {
		f := &files[i]
		d.graph.Files[f.Path] = f
		// Cache line count for delta calculations (fast: ~1ms per file)
		if lines := countLines(filepath.Join(d.root, f.Path)); lines > 0 {
			d.graph.State[f.Path] = &FileState{Lines: lines, Size: f.Size}
		}
	}
	for _, file := range configuredFiles {
		d.graph.ConfiguredFiles[file.Path] = struct{}{}
	}
	d.graph.LastScan = time.Now()
	d.graph.mu.Unlock()

	if d.verbose {
		fmt.Printf("[watch] Full scan: %d files in %v\n", len(files), time.Since(start))
	}

	return nil
}

func (d *Daemon) isConfiguredFile(path string) bool {
	cfg := config.Load(d.root)
	return scanner.MatchesFilters(path, filepath.Ext(path), cfg.Only, cfg.Exclude)
}

var daemonRefreshConfiguredFiles = (*Daemon).refreshConfiguredFiles

func (d *Daemon) refreshConfiguredFiles(resetIgnoreCache bool) error {
	gitCache := d.gitCache
	if resetIgnoreCache {
		gitCache = scanner.NewGitIgnoreCache(d.root)
		d.gitCache = gitCache
	}
	files, err := scanner.ScanConfiguredFiles(context.Background(), d.root, gitCache)
	if err != nil {
		return err
	}
	configured := make(map[string]struct{}, len(files))
	for _, file := range files {
		configured[file.Path] = struct{}{}
	}
	d.graph.mu.Lock()
	d.graph.ConfiguredFiles = configured
	// Filters define dependency membership too, so the previous graph must not
	// be published under a new configured-file count.
	d.graph.FileGraph = nil
	d.graph.DepCtx = make(map[string]*DepContext)
	d.graph.HasDeps = false
	d.graph.mu.Unlock()

	// Invalidation alone would leave the daemon serving no hub or importer
	// intelligence until it restarts, so every hook reading daemon state would
	// silently degrade after one config edit. Rebuild under the same size guard
	// Start uses. computeDeps takes the lock itself, so call it unlocked.
	if shouldComputeDependencyGraph(len(configured)) {
		d.computeDeps()
	}
	d.computeTopology()
	return nil
}

var daemonRefreshDependencies = (*Daemon).refreshDependencies

func (d *Daemon) refreshDependencies() {
	d.graph.mu.RLock()
	stale := d.graph.GraphState.Status == graphLifecycleStale
	configuredCount := len(d.graph.ConfiguredFiles)
	d.graph.mu.RUnlock()
	if !stale || !shouldComputeDependencyGraph(configuredCount) {
		return
	}
	d.computeDeps()
	if d.publisher != nil {
		d.reportPublicationError(d.writeState())
	}
}

// computeDeps builds the file-to-file dependency graph
func (d *Daemon) computeDeps() {
	d.computeDepsWith(func(ctx context.Context, root string) (*scanner.FileGraph, error) {
		return buildFileGraph(ctx, root, scanner.ConfiguredFilters(root))
	})
}

func (d *Daemon) computeDepsWith(build func(context.Context, string) (*scanner.FileGraph, error)) {
	d.computeDepsWithBeforePublish(build, nil)
}

func (d *Daemon) computeDepsWithBeforePublish(build func(context.Context, string) (*scanner.FileGraph, error), beforePublish func()) {
	start := time.Now()
	buildConfig := config.Load(d.root)
	d.graph.mu.RLock()
	configuredBefore := make([]string, 0, len(d.graph.ConfiguredFiles))
	for file := range d.graph.ConfiguredFiles {
		configuredBefore = append(configuredBefore, file)
	}
	generationBefore := d.graph.graphGeneration
	d.graph.mu.RUnlock()

	// Build the file graph. Unavailable coverage provides no usable dependency
	// evidence, so do not publish an authoritative empty graph.
	fg, err := build(context.Background(), d.root)
	if err != nil || fg == nil || (len(configuredBefore) > 0 && fg.Coverage.Status == analysis.CoverageUnavailable) {
		d.markGraphLifecycle(graphLifecycleFailed)
		if d.verbose {
			fmt.Printf("[watch] File graph unavailable: %v\n", err)
		}
		return
	}
	if beforePublish != nil {
		beforePublish()
	}

	d.graph.mu.Lock()
	defer d.graph.mu.Unlock()
	configuredAfter := make([]string, 0, len(d.graph.ConfiguredFiles))
	for file := range d.graph.ConfiguredFiles {
		configuredAfter = append(configuredAfter, file)
	}
	currentConfig := config.Load(d.root)
	if d.graph.graphGeneration != generationBefore ||
		ConfiguredInventoryFingerprint(configuredBefore) != ConfiguredInventoryFingerprint(configuredAfter) ||
		graphFilterFingerprint(buildConfig) != graphFilterFingerprint(currentConfig) {
		d.markGraphLifecycleLocked(newGraphState(d.root, currentConfig, graphLifecycleStale, time.Time{}, nil))
		return
	}
	state := newGraphState(d.root, buildConfig, graphLifecycleAvailable, time.Now(), configuredBefore)

	// Convert FileGraph to DepContext map
	d.graph.DepCtx = make(map[string]*DepContext)
	d.graph.FileGraph = fg

	for path := range d.graph.Files {
		ctx := &DepContext{
			Imports:   fg.Imports[path],
			Importers: fg.Importers[path],
		}
		d.graph.DepCtx[path] = ctx
	}

	d.graph.HasDeps = true
	d.graph.GraphState = state

	hubCount := len(fg.HubFiles())
	if d.verbose {
		fmt.Printf("[watch] File graph: %d files, %d hubs in %v\n", len(d.graph.Files), hubCount, time.Since(start))
	}
}

func (d *Daemon) markGraphLifecycle(status GraphLifecycle) {
	state := newGraphState(d.root, config.Load(d.root), status, time.Time{}, nil)
	d.graph.mu.Lock()
	defer d.graph.mu.Unlock()
	d.markGraphLifecycleLocked(state)
}

func (d *Daemon) markGraphLifecycleLocked(state GraphState) {
	d.graph.graphGeneration++
	d.graph.GraphState = state
	d.graph.HasDeps = false
	d.graph.FileGraph = nil
	d.graph.DepCtx = make(map[string]*DepContext)
}

// addWatchDirs recursively adds directories to the watcher
func (d *Daemon) addWatchDirs() error {
	return filepath.Walk(d.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		absPath, _ := filepath.Abs(path)

		// Skip hidden directories and common ignores
		name := info.Name()
		if info.IsDir() {
			if d.gitCache != nil {
				d.gitCache.EnsureDir(absPath)
				// Honor nested .gitignore rules so ignored subtrees are never watched.
				if path != d.root && d.gitCache.ShouldIgnore(absPath) {
					return filepath.SkipDir
				}
			}
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return d.watcher.Add(path)
		}
		return nil
	})
}
