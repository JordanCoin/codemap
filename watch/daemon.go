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

	"codemap/config"
	"codemap/internal/projectpath"
	"codemap/limits"
	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

// Daemon is the watch daemon that keeps the graph updated
type Daemon struct {
	root     string
	graph    *Graph
	watcher  *fsnotify.Watcher
	gitCache *scanner.GitIgnoreCache
	eventLog string // path to event log file
	verbose  bool
	done     chan struct{}

	eventLoopWG sync.WaitGroup
}

// NewDaemon creates a new watch daemon for the given root
func NewDaemon(root string, verbose bool) (*Daemon, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("invalid root path: %w", err)
	}
	// Canonicalize so d.root, the runtime dir, and fsnotify paths agree.
	if canonical, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = canonical
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	gitCache := scanner.NewGitIgnoreCache(root)

	// Check if git repo (fast, one-time)
	isGitRepo := false
	if _, err := os.Stat(filepath.Join(absRoot, ".git")); err == nil {
		isGitRepo = true
	}

	d := &Daemon{
		root:     absRoot,
		watcher:  watcher,
		gitCache: gitCache,
		verbose:  verbose,
		done:     make(chan struct{}),
		eventLog: filepath.Join(projectpath.RuntimeCodemapDir(absRoot), "events.log"),
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

	return d, nil
}

// Start begins watching and returns immediately
func (d *Daemon) Start() error {
	// Ensure .codemap directory exists
	codemapDir := projectpath.RuntimeCodemapDir(d.root)
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		return fmt.Errorf("failed to create .codemap dir: %w", err)
	}

	// Initial full scan
	if err := d.fullScan(); err != nil {
		return fmt.Errorf("initial scan failed: %w", err)
	}

	// Compute dependency graph (best effort). Skip on very large repos to avoid
	// expensive startup memory/CPU spikes in background hook flows.
	fileCount := d.ConfiguredFileCount()
	if shouldComputeDependencyGraph(fileCount) {
		d.computeDeps()
	} else if d.verbose {
		fmt.Printf("[watch] Skipping dependency graph for large repo (%d files)\n", fileCount)
	}

	// Add directories to watcher
	if err := d.addWatchDirs(); err != nil {
		return fmt.Errorf("failed to add watch dirs: %w", err)
	}
	// The hidden state directory is otherwise skipped. Watch it so config edits
	// can refresh the configured-file inventory; other state files stay ignored.
	if err := d.watcher.Add(codemapDir); err != nil {
		return fmt.Errorf("failed to watch .codemap dir: %w", err)
	}

	// Write initial state for hooks to read immediately
	d.writeState()

	// Start event loop
	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()

	return nil
}

// Stop gracefully shuts down the daemon
func (d *Daemon) Stop() {
	close(d.done)
	d.watcher.Close()
	d.eventLoopWG.Wait()
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
	d.writeState()
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

// daemonRefreshConfiguredFiles is the seam the event loop calls, so tests can
// observe how often control events trigger a refresh.
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
	return nil
}

// computeDeps builds the file-to-file dependency graph
func (d *Daemon) computeDeps() {
	start := time.Now()

	// Build file graph (internal file-to-file dependencies)
	fg, err := scanner.BuildFileGraph(context.Background(), d.root, scanner.ConfiguredFilters(d.root))
	if err != nil {
		if d.verbose {
			fmt.Printf("[watch] File graph unavailable: %v\n", err)
		}
		return
	}

	d.graph.mu.Lock()
	defer d.graph.mu.Unlock()

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

	hubCount := len(fg.HubFiles())
	if d.verbose {
		fmt.Printf("[watch] File graph: %d files, %d hubs in %v\n", len(d.graph.Files), hubCount, time.Since(start))
	}
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
