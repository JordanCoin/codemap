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
	configPath string
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

	dependencyRequests chan dependencyGraphSnapshot
	dependencyResults  chan dependencyGraphResult
	dependencyOnce     sync.Once
	dependencyCancel   context.CancelFunc
	dependencyWorkerWG sync.WaitGroup
	// These flags are owned by eventLoop; the worker only exchanges snapshots
	// and results through the channels above.
	dependencyBusy    bool
	dependencyPending bool
}

type dependencyGraphSnapshot struct {
	configured []string
	config     config.ProjectConfig
	generation uint64
}

type dependencyGraphResult struct {
	snapshot dependencyGraphSnapshot
	graph    *scanner.FileGraph
	err      error
	started  time.Time
}

func (d *Daemon) runtimeStateDir() (string, error) {
	if d.runtimeDir != "" {
		return d.runtimeDir, nil
	}
	return projectpath.CheckedRuntimeCodemapDir(d.root)
}

func (d *Daemon) loadConfig() config.ProjectConfig {
	if d.configPath != "" {
		return config.LoadFile(d.configPath)
	}
	if d.configDir != "" {
		return config.LoadFile(filepath.Join(d.configDir, "config.json"))
	}
	return config.Load(d.root)
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
		configPath:   filepath.Join(selection.PolicyDir, "config.json"),
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
	d.startDependencyWorker()

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
	if d.dependencyCancel != nil {
		d.dependencyCancel()
	}
	d.eventLoopWG.Wait()
	d.dependencyWorkerWG.Wait()
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
	cfg := d.loadConfig()

	files, err := scanner.ScanFiles(context.Background(), d.root, d.gitCache, nil, nil)
	if err != nil {
		return err
	}
	configuredPaths := make([]string, 0)
	for i := range files {
		file := &files[i]
		path := filepath.ToSlash(file.Path)
		if path != ".codemap" && !strings.HasPrefix(path, ".codemap/") && scanner.MatchesFilters(file.Path, file.Ext, cfg.Only, cfg.Exclude) {
			configuredPaths = append(configuredPaths, file.Path)
		}
	}

	d.graph.mu.Lock()
	d.graph.Files = make(map[string]*scanner.FileInfo)
	d.graph.ConfiguredFiles = make(map[string]struct{}, len(configuredPaths))
	d.graph.State = make(map[string]*FileState)
	for i := range files {
		f := &files[i]
		d.graph.Files[f.Path] = f
		// Cache line count for delta calculations (fast: ~1ms per file)
		if lines := countLines(filepath.Join(d.root, f.Path)); lines > 0 {
			d.graph.State[f.Path] = &FileState{Lines: lines, Size: f.Size}
		}
	}
	for _, path := range configuredPaths {
		d.graph.ConfiguredFiles[path] = struct{}{}
	}
	d.graph.LastScan = time.Now()
	d.graph.mu.Unlock()

	if d.verbose {
		fmt.Printf("[watch] Full scan: %d files in %v\n", len(files), time.Since(start))
	}

	return nil
}

func matchesConfiguredFile(path string, cfg config.ProjectConfig) bool {
	return scanner.MatchesFilters(path, filepath.Ext(path), cfg.Only, cfg.Exclude)
}

var daemonRefreshConfiguredFiles = (*Daemon).refreshConfiguredFiles

func (d *Daemon) refreshConfiguredFiles(resetIgnoreCache bool) error {
	gitCache := d.gitCache
	if resetIgnoreCache {
		gitCache = scanner.NewGitIgnoreCache(d.root)
		d.gitCache = gitCache
	}
	cfg := d.loadConfig()
	files, err := scanner.ScanConfiguredFilesWithFilters(context.Background(), d.root, gitCache, scanner.Filters{Only: cfg.Only, Exclude: cfg.Exclude})
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
	d.markGraphLifecycleLocked(newGraphState(d.root, cfg, graphLifecycleStale, time.Time{}, nil))
	d.graph.mu.Unlock()

	// Invalidation alone would leave the daemon serving no hub or importer
	// intelligence until it restarts, so every hook reading daemon state would
	// silently degrade after one config edit. Queue the rebuild under the same
	// size guard Start uses.
	if shouldComputeDependencyGraph(len(configured)) {
		d.refreshDependencies()
	}
	d.computeTopology()
	return nil
}

var daemonRefreshDependencies = (*Daemon).refreshDependencies

// refreshDependencies is called by eventLoop and owns the worker state flags.
func (d *Daemon) refreshDependencies() {
	cfg := d.loadConfig()
	d.graph.mu.RLock()
	stale := d.graph.GraphState.Status == graphLifecycleStale
	configuredCount := len(d.graph.ConfiguredFiles)
	configured := make([]string, 0, len(d.graph.ConfiguredFiles))
	for file := range d.graph.ConfiguredFiles {
		configured = append(configured, file)
	}
	snapshot := dependencyGraphSnapshot{
		configured: configured,
		config:     cfg,
		generation: d.graph.graphGeneration,
	}
	d.graph.mu.RUnlock()
	if !stale || !shouldComputeDependencyGraph(configuredCount) {
		return
	}
	d.startDependencyWorker()
	if d.dependencyBusy {
		d.dependencyPending = true
		return
	}
	d.dependencyBusy = true
	select {
	case d.dependencyRequests <- snapshot:
	case <-d.done:
		d.dependencyBusy = false
	}
}

// buildDependencyGraph converts a worker panic into the existing failed-build
// path so a background scan cannot terminate the daemon process.
func buildDependencyGraph(ctx context.Context, root string, filters scanner.Filters, build func(context.Context, string, scanner.Filters) (*scanner.FileGraph, error)) (graph *scanner.FileGraph, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("dependency graph build panicked: %v", recovered)
		}
	}()
	return build(ctx, root, filters)
}

func (d *Daemon) startDependencyWorker() {
	d.dependencyOnce.Do(func() {
		if d.dependencyRequests == nil {
			d.dependencyRequests = make(chan dependencyGraphSnapshot, 1)
		}
		if d.dependencyResults == nil {
			d.dependencyResults = make(chan dependencyGraphResult, 1)
		}
		ctx, cancel := context.WithCancel(context.Background())
		d.dependencyCancel = cancel
		d.dependencyWorkerWG.Add(1)
		go func() {
			defer d.dependencyWorkerWG.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case snapshot := <-d.dependencyRequests:
					started := time.Now()
					filters := scanner.Filters{Only: snapshot.config.Only, Exclude: snapshot.config.Exclude}
					graph, err := buildDependencyGraph(ctx, d.root, filters, buildFileGraph)
					result := dependencyGraphResult{snapshot: snapshot, graph: graph, err: err, started: started}
					select {
					case d.dependencyResults <- result:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	})
}

// handleDependencyGraphResult is called by eventLoop and owns the worker state flags.
func (d *Daemon) handleDependencyGraphResult(result dependencyGraphResult) {
	d.dependencyBusy = false
	d.applyDependencyGraph(result.snapshot, result.graph, result.err, result.started)
	retry := d.dependencyPending
	d.dependencyPending = false
	if retry {
		cfg := d.loadConfig()
		d.graph.mu.Lock()
		if d.graph.GraphState.Status != graphLifecycleStale {
			d.markGraphLifecycleLocked(newGraphState(d.root, cfg, graphLifecycleStale, time.Time{}, nil))
		}
		d.graph.mu.Unlock()
	}
	if d.publisher != nil {
		d.reportPublicationError(d.writeState())
	}
	if retry {
		d.refreshDependencies()
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
	snapshot := d.dependencyGraphSnapshot()

	// Build the file graph. Unavailable coverage provides no usable dependency
	// evidence, so do not publish an authoritative empty graph.
	fg, err := build(context.Background(), d.root)
	if beforePublish != nil {
		beforePublish()
	}
	d.applyDependencyGraph(snapshot, fg, err, start)
}

func (d *Daemon) dependencyGraphSnapshot() dependencyGraphSnapshot {
	cfg := d.loadConfig()
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	configured := make([]string, 0, len(d.graph.ConfiguredFiles))
	for file := range d.graph.ConfiguredFiles {
		configured = append(configured, file)
	}
	return dependencyGraphSnapshot{
		configured: configured,
		config:     cfg,
		generation: d.graph.graphGeneration,
	}
}

func (d *Daemon) applyDependencyGraph(snapshot dependencyGraphSnapshot, fg *scanner.FileGraph, err error, start time.Time) {
	if err != nil || fg == nil || (len(snapshot.configured) > 0 && fg.Coverage.Status == analysis.CoverageUnavailable) {
		d.markGraphLifecycle(graphLifecycleFailed)
		if d.verbose {
			fmt.Printf("[watch] File graph unavailable: %v\n", err)
		}
		return
	}

	currentConfig := d.loadConfig()
	d.graph.mu.Lock()
	defer d.graph.mu.Unlock()
	configuredAfter := make([]string, 0, len(d.graph.ConfiguredFiles))
	for file := range d.graph.ConfiguredFiles {
		configuredAfter = append(configuredAfter, file)
	}
	if d.graph.graphGeneration != snapshot.generation ||
		ConfiguredInventoryFingerprint(snapshot.configured) != ConfiguredInventoryFingerprint(configuredAfter) ||
		graphFilterFingerprint(snapshot.config) != graphFilterFingerprint(currentConfig) {
		d.markGraphLifecycleLocked(newGraphState(d.root, currentConfig, graphLifecycleStale, time.Time{}, nil))
		return
	}
	state := newGraphState(d.root, snapshot.config, graphLifecycleAvailable, time.Now(), snapshot.configured)

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
	state := newGraphState(d.root, d.loadConfig(), status, time.Time{}, nil)
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
