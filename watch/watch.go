// Package watch provides a file system watcher daemon for live code graph updates
package watch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

// Event represents a file change event with timestamp and structural context
type Event struct {
	Time      time.Time `json:"time"`
	Op        string    `json:"op"`               // CREATE, WRITE, REMOVE, RENAME
	Path      string    `json:"path"`             // relative path
	Language  string    `json:"lang,omitempty"`   // go, py, js, etc.
	Lines     int       `json:"lines,omitempty"`
	Delta     int       `json:"delta,omitempty"`  // line count change (+/-)
	SizeDelta int64     `json:"size_delta,omitempty"`
	Dirty     bool      `json:"dirty,omitempty"`  // uncommitted changes
	// Structural context from deps
	Importers   int      `json:"importers,omitempty"`   // how many files import this
	Imports     int      `json:"imports,omitempty"`     // how many files this imports
	IsHub       bool     `json:"is_hub,omitempty"`      // importers >= 3
	RelatedHot  []string `json:"related_hot,omitempty"` // connected files also edited recently
}

// FileState tracks lightweight per-file state for delta calculations
type FileState struct {
	Lines int
	Size  int64
}

// DepContext holds pre-computed dependency context for a file
type DepContext struct {
	Imports   []string // files this file imports
	Importers []string // files that import this file
}

// Graph holds the live code graph state
type Graph struct {
	mu        sync.RWMutex
	Root      string
	Files     map[string]*scanner.FileInfo   // path -> file info
	FileGraph *scanner.FileGraph             // internal file-to-file dependencies
	DepCtx    map[string]*DepContext         // path -> dependency context (precomputed)
	State     map[string]*FileState          // path -> line/size cache for deltas
	Events    []Event
	LastScan  time.Time
	IsGitRepo bool
	HasDeps   bool // whether deps were successfully computed
}

// Daemon is the watch daemon that keeps the graph updated
type Daemon struct {
	root      string
	graph     *Graph
	watcher   *fsnotify.Watcher
	gitCache  *scanner.GitIgnoreCache
	eventLog  string // path to event log file
	verbose   bool
	done      chan struct{}
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

// Graph returns the current graph (thread-safe)
func (d *Daemon) GetGraph() *Graph {
	return d.graph
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

// countLines counts lines in a file efficiently (no full read into memory)
func countLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	return count
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

// eventLoop processes file system events
func (d *Daemon) eventLoop() {
	// Debounce rapid changes (e.g., save + format)
	debounce := make(map[string]time.Time)
	debounceWindow := 100 * time.Millisecond

	for {
		select {
		case <-d.done:
			return

		case event, ok := <-d.watcher.Events:
			if !ok {
				return
			}

			// Skip non-source files
			if !d.isSourceFile(event.Name) {
				continue
			}

			// Debounce rapid events on same file
			if last, exists := debounce[event.Name]; exists {
				if time.Since(last) < debounceWindow {
					continue
				}
			}
			debounce[event.Name] = time.Now()

			// Process the event
			d.handleEvent(event)

		case err, ok := <-d.watcher.Errors:
			if !ok {
				return
			}
			if d.verbose {
				fmt.Printf("[watch] Error: %v\n", err)
			}
		}
	}
}

// isSourceFile checks if a file should be tracked
func (d *Daemon) isSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java", ".swift", ".kt", ".c", ".cpp", ".h":
		return true
	}
	return false
}

// handleEvent processes a single file event
func (d *Daemon) handleEvent(fsEvent fsnotify.Event) {
	relPath, err := filepath.Rel(d.root, fsEvent.Name)
	if err != nil {
		relPath = fsEvent.Name
	}

	// Determine operation
	var op string
	switch {
	case fsEvent.Op&fsnotify.Create != 0:
		op = "CREATE"
	case fsEvent.Op&fsnotify.Write != 0:
		op = "WRITE"
	case fsEvent.Op&fsnotify.Remove != 0:
		op = "REMOVE"
	case fsEvent.Op&fsnotify.Rename != 0:
		op = "RENAME"
	default:
		return
	}

	event := Event{
		Time:     time.Now(),
		Op:       op,
		Path:     relPath,
		Language: scanner.DetectLanguage(relPath),
	}

	// Update graph and calculate deltas
	d.graph.mu.Lock()
	switch op {
	case "CREATE", "WRITE":
		if info, err := os.Stat(fsEvent.Name); err == nil && !info.IsDir() {
			// Count new lines
			newLines := countLines(fsEvent.Name)
			event.Lines = newLines

			// Calculate deltas from cached state
			if prev, exists := d.graph.State[relPath]; exists {
				event.Delta = newLines - prev.Lines
				event.SizeDelta = info.Size() - prev.Size
			} else {
				event.Delta = newLines // new file, all lines are added
				event.SizeDelta = info.Size()
			}

			// Update cached state
			d.graph.State[relPath] = &FileState{Lines: newLines, Size: info.Size()}

			// Update file info
			d.graph.Files[relPath] = &scanner.FileInfo{
				Path: relPath,
				Size: info.Size(),
				Ext:  filepath.Ext(relPath),
			}
		}

	case "REMOVE", "RENAME":
		// Record what was lost
		if prev, exists := d.graph.State[relPath]; exists {
			event.Lines = 0
			event.Delta = -prev.Lines
			event.SizeDelta = -prev.Size
		}
		delete(d.graph.Files, relPath)
		delete(d.graph.State, relPath)
	}

	// Check if file is dirty (uncommitted) - only if git repo
	if d.graph.IsGitRepo && (op == "CREATE" || op == "WRITE") {
		event.Dirty = isFileDirty(d.root, relPath)
	}

	// Enrich with structural context from file graph (if available)
	if d.graph.HasDeps && d.graph.FileGraph != nil {
		fg := d.graph.FileGraph
		event.Imports = len(fg.Imports[relPath])
		event.Importers = len(fg.Importers[relPath])
		event.IsHub = fg.IsHub(relPath)

		// Find related hot files - connected files also edited recently (last 5 min)
		event.RelatedHot = d.findRelatedHot(relPath, 5*time.Minute)
	}

	d.graph.Events = append(d.graph.Events, event)
	d.graph.mu.Unlock()

	// Log event
	d.logEvent(event)

	if d.verbose {
		deltaStr := ""
		if event.Delta != 0 {
			deltaStr = fmt.Sprintf(" (%+d lines)", event.Delta)
		}
		dirtyStr := ""
		if event.Dirty {
			dirtyStr = " [dirty]"
		}
		hubStr := ""
		if event.IsHub {
			hubStr = fmt.Sprintf(" [HUB:%d importers]", event.Importers)
		}
		hotStr := ""
		if len(event.RelatedHot) > 0 {
			hotStr = fmt.Sprintf(" [related:%d]", len(event.RelatedHot))
		}
		fmt.Printf("[watch] %s %s %s%s%s%s%s\n", event.Time.Format("15:04:05"), op, relPath, deltaStr, dirtyStr, hubStr, hotStr)
	}
}

// findRelatedHot finds connected files that were also recently edited
// Must be called while holding d.graph.mu lock
func (d *Daemon) findRelatedHot(path string, window time.Duration) []string {
	if d.graph.FileGraph == nil {
		return nil
	}

	// Get connected files from the file graph
	connected := d.graph.FileGraph.ConnectedFiles(path)
	if len(connected) == 0 {
		return nil
	}

	connectedSet := make(map[string]bool)
	for _, f := range connected {
		connectedSet[f] = true
	}

	// Look at recent events and find matches
	cutoff := time.Now().Add(-window)
	recentlyEdited := make(map[string]bool)
	for i := len(d.graph.Events) - 1; i >= 0; i-- {
		e := d.graph.Events[i]
		if e.Time.Before(cutoff) {
			break
		}
		if e.Path != path && (e.Op == "CREATE" || e.Op == "WRITE") {
			recentlyEdited[e.Path] = true
		}
	}

	// Find intersection
	var hot []string
	for file := range connectedSet {
		if recentlyEdited[file] {
			hot = append(hot, file)
		}
	}

	return hot
}

// isFileDirty checks if a file has uncommitted changes (fast git check)
func isFileDirty(root, relPath string) bool {
	cmd := exec.Command("git", "diff", "--quiet", "--", relPath)
	cmd.Dir = root
	err := cmd.Run()
	return err != nil // non-zero exit = dirty
}

// logEvent appends an event to the log file
func (d *Daemon) logEvent(e Event) {
	f, err := os.OpenFile(d.eventLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// Format: timestamp | OP | path | lines | delta | dirty
	deltaStr := ""
	if e.Delta > 0 {
		deltaStr = fmt.Sprintf("+%d", e.Delta)
	} else if e.Delta < 0 {
		deltaStr = fmt.Sprintf("%d", e.Delta)
	}

	dirtyStr := ""
	if e.Dirty {
		dirtyStr = "dirty"
	}

	line := fmt.Sprintf("%s | %-6s | %-40s | %4d | %6s | %s\n",
		e.Time.Format("2006-01-02 15:04:05"),
		e.Op,
		e.Path,
		e.Lines,
		deltaStr,
		dirtyStr,
	)
	f.WriteString(line)

	// Update state file for hooks to read
	d.writeState()
}

// State represents the daemon state that hooks can read
type State struct {
	UpdatedAt    time.Time           `json:"updated_at"`
	FileCount    int                 `json:"file_count"`
	Hubs         []string            `json:"hubs"`
	Importers    map[string][]string `json:"importers"`     // file -> files that import it
	Imports      map[string][]string `json:"imports"`       // file -> files it imports
	RecentEvents []Event             `json:"recent_events"` // last 50 events for timeline
}

// writeState persists current state for hooks to read
func (d *Daemon) writeState() {
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if d.graph.FileGraph == nil {
		return
	}

	// Get last 50 events for timeline
	events := d.graph.Events
	if len(events) > 50 {
		events = events[len(events)-50:]
	}

	state := State{
		UpdatedAt:    time.Now(),
		FileCount:    len(d.graph.Files),
		Hubs:         d.graph.FileGraph.HubFiles(),
		Importers:    d.graph.FileGraph.Importers,
		Imports:      d.graph.FileGraph.Imports,
		RecentEvents: events,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}

	stateFile := filepath.Join(d.root, ".codemap", "state.json")
	os.WriteFile(stateFile, data, 0644)
}

// WriteInitialState writes state after initial scan (for hooks)
func (d *Daemon) WriteInitialState() {
	d.writeState()
}

// ReadState reads the daemon state from disk (for hooks to use)
// Returns nil if state doesn't exist or is stale (> 30 seconds old)
func ReadState(root string) *State {
	stateFile := filepath.Join(root, ".codemap", "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}

	// Check if state is fresh (daemon still running)
	if time.Since(state.UpdatedAt) > 30*time.Second {
		return nil // stale, daemon probably not running
	}

	return &state
}

// WritePID writes the daemon PID to .codemap/watch.pid
func WritePID(root string) error {
	pidFile := filepath.Join(root, ".codemap", "watch.pid")
	return os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

// ReadPID reads the daemon PID from .codemap/watch.pid
func ReadPID(root string) (int, error) {
	pidFile := filepath.Join(root, ".codemap", "watch.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

// RemovePID removes the PID file
func RemovePID(root string) {
	pidFile := filepath.Join(root, ".codemap", "watch.pid")
	os.Remove(pidFile)
}

// IsRunning checks if the daemon is running
func IsRunning(root string) bool {
	pid, err := ReadPID(root)
	if err != nil {
		return false
	}
	// Check if process exists by sending signal 0
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so send signal 0 to check
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// Stop sends SIGTERM to the daemon process
func Stop(root string) error {
	pid, err := ReadPID(root)
	if err != nil {
		return fmt.Errorf("no daemon running: %w", err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	// Clean up PID file
	RemovePID(root)
	return nil
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
