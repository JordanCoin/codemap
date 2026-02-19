package watch

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

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

			// Allow directory creates through (to add new dirs to watcher)
			// but skip non-source files otherwise
			isCreate := event.Op&fsnotify.Create != 0
			if !d.isSourceFile(event.Name) {
				// Check if it's a directory create - let those through
				if isCreate {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						// Directory create - let it through to handleEvent
					} else {
						continue
					}
				} else {
					continue
				}
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
		info, err := os.Stat(fsEvent.Name)
		if err != nil {
			d.graph.mu.Unlock()
			return
		}

		// If a new directory was created, add it to the watcher
		if info.IsDir() {
			name := filepath.Base(fsEvent.Name)
			// Skip hidden directories and common ignores
			if !strings.HasPrefix(name, ".") && name != "node_modules" && name != "vendor" {
				d.watcher.Add(fsEvent.Name)
			}
			d.graph.mu.Unlock()
			return
		}

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

// writeState persists current state for hooks to read
func (d *Daemon) writeState() {
	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	// Get last 50 events for timeline
	events := d.graph.Events
	if len(events) > 50 {
		events = events[len(events)-50:]
	}

	state := State{
		UpdatedAt:    time.Now(),
		FileCount:    len(d.graph.Files),
		Hubs:         []string{},
		Importers:    map[string][]string{},
		Imports:      map[string][]string{},
		RecentEvents: events,
	}
	if d.graph.FileGraph != nil {
		state.Hubs = d.graph.FileGraph.HubFiles()
		state.Importers = d.graph.FileGraph.Importers
		state.Imports = d.graph.FileGraph.Imports
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}

	stateFile := filepath.Join(d.root, ".codemap", "state.json")
	os.WriteFile(stateFile, data, 0644)
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

// isFileDirty checks if a file has uncommitted changes (fast git check)
func isFileDirty(root, relPath string) bool {
	cmd := exec.Command("git", "diff", "--quiet", "--", relPath)
	cmd.Dir = root
	err := cmd.Run()
	return err != nil // non-zero exit = dirty
}
