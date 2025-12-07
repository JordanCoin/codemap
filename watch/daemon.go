// Package watch provides a file system watcher daemon for live code graph updates
package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
}

// NewDaemon creates a new watch daemon for the given root
func NewDaemon(root string, verbose bool) (*Daemon, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("invalid root path: %w", err)
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
		eventLog: filepath.Join(absRoot, ".codemap", "events.log"),
		graph: &Graph{
			Root:      absRoot,
			Files:     make(map[string]*scanner.FileInfo),
			DepCtx:    make(map[string]*DepContext),
			State:     make(map[string]*FileState),
			Events:    make([]Event, 0),
			IsGitRepo: isGitRepo,
		},
	}

	return d, nil
}

// Start begins watching and returns immediately
func (d *Daemon) Start() error {
	// Ensure .codemap directory exists
	codemapDir := filepath.Join(d.root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0755); err != nil {
		return fmt.Errorf("failed to create .codemap dir: %w", err)
	}

	// Initial full scan
	if err := d.fullScan(); err != nil {
		return fmt.Errorf("initial scan failed: %w", err)
	}

	// Compute dependency graph (best effort - don't fail if deps unavailable)
	d.computeDeps()

	// Add directories to watcher
	if err := d.addWatchDirs(); err != nil {
		return fmt.Errorf("failed to add watch dirs: %w", err)
	}

	// Write initial state for hooks to read immediately
	d.writeState()

	// Start event loop
	go d.eventLoop()

	return nil
}

// Stop gracefully shuts down the daemon
func (d *Daemon) Stop() {
	close(d.done)
	d.watcher.Close()
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

// WriteInitialState writes state after initial scan (for hooks)
func (d *Daemon) WriteInitialState() {
	d.writeState()
}

// fullScan does a complete scan of the project
func (d *Daemon) fullScan() error {
	start := time.Now()

	files, err := scanner.ScanFiles(d.root, d.gitCache)
	if err != nil {
		return err
	}

	d.graph.mu.Lock()
	d.graph.Files = make(map[string]*scanner.FileInfo)
	d.graph.State = make(map[string]*FileState)
	for i := range files {
		f := &files[i]
		d.graph.Files[f.Path] = f
		// Cache line count for delta calculations (fast: ~1ms per file)
		if lines := countLines(filepath.Join(d.root, f.Path)); lines > 0 {
			d.graph.State[f.Path] = &FileState{Lines: lines, Size: f.Size}
		}
	}
	d.graph.LastScan = time.Now()
	d.graph.mu.Unlock()

	if d.verbose {
		fmt.Printf("[watch] Full scan: %d files in %v\n", len(files), time.Since(start))
	}

	return nil
}

// computeDeps builds the file-to-file dependency graph
func (d *Daemon) computeDeps() {
	start := time.Now()

	// Build file graph (internal file-to-file dependencies)
	fg, err := scanner.BuildFileGraph(d.root)
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

		// Skip hidden directories and common ignores
		name := info.Name()
		if info.IsDir() {
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return d.watcher.Add(path)
		}
		return nil
	})
}
