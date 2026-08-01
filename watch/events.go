package watch

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codemap/internal/runtimefile"
	"codemap/limits"
	"codemap/scanner"
	"codemap/topology"

	"github.com/fsnotify/fsnotify"
)

var isTopologyManifest = topology.IsManifestPath

// eventDebouncer coalesces rapid successive WRITE events for the same path.
// Non-WRITE operations are never debounced so create/remove transitions stay accurate.
type eventDebouncer struct {
	window     time.Duration
	pruneAfter time.Duration
	lastSeen   map[string]time.Time
	lastPruned time.Time
	pending    map[string]pendingWrite
}

type pendingWrite struct {
	event fsnotify.Event
	due   time.Time
}

type debounceAction uint8

const (
	debounceProcess debounceAction = iota
	debounceSkip
	debounceDefer
)

// controlRefreshWindow is how long the event loop waits for a control-event
// burst to settle before refreshing configured files once.
const controlRefreshWindow = 150 * time.Millisecond

func newEventDebouncer(window time.Duration) *eventDebouncer {
	pruneAfter := 10 * window
	if pruneAfter < time.Second {
		pruneAfter = time.Second
	}
	return &eventDebouncer{
		window:     window,
		pruneAfter: pruneAfter,
		lastSeen:   make(map[string]time.Time),
		pending:    make(map[string]pendingWrite),
	}
}

func (d *eventDebouncer) shouldSkip(event fsnotify.Event, now time.Time) bool {
	op := event.Op
	// Never debounce transitions that include create/remove/rename bits,
	// even if they also carry WRITE, so lifecycle tracking stays accurate.
	if op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		return false
	}
	// Only debounce pure write events (allow CHMOD alongside WRITE).
	if op&fsnotify.Write == 0 {
		return false
	}
	allowedWriteMask := fsnotify.Write | fsnotify.Chmod
	if op&^allowedWriteMask != 0 {
		return false
	}

	last, exists := d.lastSeen[event.Name]
	d.lastSeen[event.Name] = now

	if d.lastPruned.IsZero() || now.Sub(d.lastPruned) >= d.pruneAfter {
		d.prune(now)
		d.lastPruned = now
	}

	return exists && now.Sub(last) < d.window
}

func (d *eventDebouncer) prune(now time.Time) {
	cutoff := now.Add(-d.pruneAfter)
	for path, ts := range d.lastSeen {
		if ts.Before(cutoff) {
			delete(d.lastSeen, path)
		}
	}
}

func (d *eventDebouncer) deferEvent(event fsnotify.Event, now time.Time) {
	d.pending[event.Name] = pendingWrite{event: event, due: now.Add(d.window)}
}

func (d *eventDebouncer) cancelPending(path string) {
	delete(d.pending, path)
}

func (d *eventDebouncer) takeDue(now time.Time) []fsnotify.Event {
	var events []fsnotify.Event
	for path, pending := range d.pending {
		if pending.due.After(now) {
			continue
		}
		events = append(events, pending.event)
		delete(d.pending, path)
	}
	return events
}

func (d *eventDebouncer) takeDueBeforeEvent(event fsnotify.Event, now time.Time) []fsnotify.Event {
	if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		d.cancelPending(event.Name)
	}
	return d.takeDue(now)
}

func (d *eventDebouncer) takeAll() []fsnotify.Event {
	events := make([]fsnotify.Event, 0, len(d.pending))
	for path, pending := range d.pending {
		events = append(events, pending.event)
		delete(d.pending, path)
	}
	return events
}

func (d *eventDebouncer) nextDelay(now time.Time) (time.Duration, bool) {
	var earliest time.Time
	for _, pending := range d.pending {
		if earliest.IsZero() || pending.due.Before(earliest) {
			earliest = pending.due
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	delay := earliest.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

// eventLoop processes file system events
func (d *Daemon) eventLoop() {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	var timerC <-chan time.Time
	armTimer := func(now time.Time) {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		delay, ok := debouncer.nextDelay(now)
		if publishDelay, publishOK := d.publisher.nextDelay(now); publishOK && (!ok || publishDelay < delay) {
			delay, ok = publishDelay, true
		}
		if !ok {
			timerC = nil
			return
		}
		timer.Reset(delay)
		timerC = timer.C
	}
	flushDue := func(now time.Time) {
		for _, event := range debouncer.takeDue(now) {
			d.handleEvent(event)
		}
		if d.publisher.due(now) {
			_ = d.publisher.publish()
		}
	}

	// Control events are coalesced separately from file events; a trailing-edge
	// debounce keeps one save to one graph rebuild.
	controlTimer := time.NewTimer(time.Hour)
	controlTimer.Stop()
	defer controlTimer.Stop()
	var controlTimerC <-chan time.Time
	controlResetIgnoreCache := false
	armControlTimer := func() {
		if !controlTimer.Stop() {
			select {
			case <-controlTimer.C:
			default:
			}
		}
		controlTimer.Reset(controlRefreshWindow)
		controlTimerC = controlTimer.C
	}
	refreshConfigured := func() {
		controlTimerC = nil
		resetIgnoreCache := controlResetIgnoreCache
		controlResetIgnoreCache = false
		if err := daemonRefreshConfiguredFiles(d, resetIgnoreCache); err == nil {
			d.writeState()
		}
	}
	defer func() {
		for _, event := range debouncer.takeAll() {
			d.handleEvent(event)
		}
		if d.publisher.dirty || len(d.publisher.pending) > 0 {
			_ = d.publisher.publish()
		}
	}()

	for {
		select {
		case <-d.done:
			d.drainQueued(debouncer, d.watcher.Events)
			return

		case <-timerC:
			now := time.Now()
			flushDue(now)
			armTimer(time.Now())

		case <-controlTimerC:
			refreshConfigured()

		case event, ok := <-d.watcher.Events:
			if !ok {
				return
			}
			now := time.Now()
			if filepath.Clean(filepath.Dir(event.Name)) == filepath.Clean(d.publisher.flushDir) {
				d.publisher.scanRequests(now)
				armTimer(now)
				continue
			}
			if resetIgnoreCache, control := d.filterControlEvent(event.Name); control {
				// OR the flag across the burst: a coalesced refresh must still
				// reset the ignore cache if any event in it was a .gitignore.
				controlResetIgnoreCache = controlResetIgnoreCache || resetIgnoreCache
				armControlTimer()
				continue
			}
			for _, pending := range debouncer.takeDueBeforeEvent(event, now) {
				d.handleEvent(pending)
			}
			if d.handleTopologyControlEvent(event) {
				continue
			}

			// Allow directory creates through (to add new dirs to watcher)
			// but skip non-source files otherwise
			isCreate := event.Op&fsnotify.Create != 0
			if !d.isSourceFile(event.Name) && !isTopologyManifest(event.Name) {
				// Check if it's a directory create - let those through
				if isCreate {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						// Directory create - let it through to handleEvent
					} else {
						d.handleConfiguredMembershipEvent(event)
						continue
					}
				} else {
					d.handleConfiguredMembershipEvent(event)
					continue
				}
			}

			// Skip build-tool temp files (e.g. vite's
			// vite.config.ts.timestamp-*.mjs) so transient churn never
			// reaches the event log or working set.
			if isTransientFile(event.Name) {
				continue
			}

			switch d.debounceAction(debouncer, event, now) {
			case debounceSkip:
				debouncer.cancelPending(event.Name)
			case debounceDefer:
				debouncer.deferEvent(event, now)
			case debounceProcess:
				d.handleEvent(event)
			}
			armTimer(time.Now())

		case err, ok := <-d.watcher.Errors:
			if !ok {
				return
			}
			if d.verbose {
				fmt.Printf("[watch] Error: %v\n", err)
			}
			d.publisher.failPending("watch_error")
		}
	}
}

func (d *Daemon) filterControlEvent(path string) (resetIgnoreCache, control bool) {
	clean := filepath.Clean(path)
	if clean == filepath.Join(d.configDir, "config.json") {
		return false, true
	}
	if filepath.Base(clean) == ".gitignore" {
		return true, true
	}
	return false, false
}

func (d *Daemon) handleConfiguredMembershipEvent(event fsnotify.Event) {
	relPath, err := filepath.Rel(d.root, event.Name)
	if err != nil {
		return
	}
	if path := filepath.ToSlash(relPath); path == ".codemap" || strings.HasPrefix(path, ".codemap/") {
		return
	}
	present := event.Op&(fsnotify.Remove|fsnotify.Rename) == 0
	if present {
		info, err := os.Stat(event.Name)
		if err != nil || info.IsDir() || (d.gitCache != nil && d.gitCache.ShouldIgnore(event.Name)) || !d.isConfiguredFile(relPath) {
			present = false
		}
	}
	d.graph.mu.Lock()
	_, existed := d.graph.ConfiguredFiles[relPath]
	if present {
		d.graph.ConfiguredFiles[relPath] = struct{}{}
	} else {
		delete(d.graph.ConfiguredFiles, relPath)
	}
	d.graph.mu.Unlock()
	if present != existed {
		d.writeState()
	}
}

func (d *Daemon) processQueuedEvent(debouncer *eventDebouncer, event fsnotify.Event, now time.Time) {
	if filepath.Clean(filepath.Dir(event.Name)) == filepath.Clean(d.publisher.flushDir) {
		d.publisher.scanRequests(now)
		return
	}
	for _, pending := range debouncer.takeDueBeforeEvent(event, now) {
		d.handleEvent(pending)
	}
	if d.handleTopologyControlEvent(event) {
		return
	}
	isCreate := event.Op&fsnotify.Create != 0
	if !d.isSourceFile(event.Name) && !isTopologyManifest(event.Name) {
		if !isCreate {
			return
		}
		if info, err := os.Stat(event.Name); err != nil || !info.IsDir() {
			return
		}
	}
	if isTransientFile(event.Name) {
		return
	}
	switch d.debounceAction(debouncer, event, now) {
	case debounceSkip:
		debouncer.cancelPending(event.Name)
	case debounceDefer:
		debouncer.deferEvent(event, now)
	case debounceProcess:
		d.handleEvent(event)
	}
}

func (d *Daemon) drainQueued(debouncer *eventDebouncer, events <-chan fsnotify.Event) {
	quiet := time.NewTimer(5 * time.Millisecond)
	deadline := time.NewTimer(50 * time.Millisecond)
	defer quiet.Stop()
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				goto drained
			}
			d.processQueuedEvent(debouncer, event, time.Now())
			if !quiet.Stop() {
				select {
				case <-quiet.C:
				default:
				}
			}
			quiet.Reset(5 * time.Millisecond)
		case <-quiet.C:
			goto drained
		case <-deadline.C:
			goto drained
		}
	}

drained:
	for _, event := range debouncer.takeAll() {
		d.handleEvent(event)
	}
	d.publisher.scanRequests(time.Now())
}
func (d *Daemon) handleTopologyControlEvent(event fsnotify.Event) bool {
	rel, err := filepath.Rel(d.configDir, event.Name)
	if err != nil || filepath.Clean(rel) != "config.json" {
		return false
	}
	if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
		d.computeTopology()
	}
	return true
}

func (d *Daemon) debounceAction(debouncer *eventDebouncer, event fsnotify.Event, now time.Time) debounceAction {
	if !debouncer.shouldSkip(event, now) {
		return debounceProcess
	}
	relPath, err := filepath.Rel(d.root, event.Name)
	if err != nil {
		return debounceProcess
	}

	d.graph.mu.RLock()
	cached := d.graph.State[relPath]
	_, configured := d.graph.ConfiguredFiles[relPath]
	var cachedSize, cachedModTime int64
	if cached != nil {
		cachedSize = cached.Size
		cachedModTime = cached.ModTime
	}
	d.graph.mu.RUnlock()
	if cached == nil {
		return debounceProcess
	}

	info, err := os.Stat(event.Name)
	if err != nil {
		return debounceProcess
	}
	// An identical write carries no new information and is skipped for every
	// file. Configured files still bypass the quiet-window defer so their
	// changes reach graph invalidation immediately.
	if cachedModTime != 0 && cachedSize == info.Size() && cachedModTime == info.ModTime().UnixNano() {
		return debounceSkip
	}
	if configured {
		return debounceProcess
	}
	return debounceDefer
}

// isSourceFile checks if a file should be tracked.
// Derives from the canonical extension registry in scanner.
func (d *Daemon) isSourceFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return scanner.IsSourceExt(ext)
}

// isTransientFile reports whether a path is a build-tool temp artifact that
// happens to carry a source extension and so slips past isSourceFile. These
// files flap CREATE/REMOVE within milliseconds and only add noise to the
// timeline and working set.
func isTransientFile(path string) bool {
	base := filepath.Base(path)
	// Vite/esbuild bundle the config to a sibling temp file named
	// e.g. vite.config.ts.timestamp-1782838234865-c8aa76ee.mjs
	if strings.Contains(base, ".timestamp-") {
		return true
	}
	return false
}

// handleEvent processes a single file event
func (d *Daemon) handleEvent(fsEvent fsnotify.Event) {
	absPath, absErr := filepath.Abs(fsEvent.Name)
	if absErr == nil && d.gitCache != nil {
		// Ignore gitignored paths entirely so watcher churn cannot come from excluded trees.
		if d.gitCache.ShouldIgnore(absPath) {
			return
		}
	}

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
	topologyManifest := isTopologyManifest(relPath)
	sourceMembershipChange := d.isSourceFile(relPath) && op != "WRITE"

	// Update graph and calculate deltas
	d.graph.mu.Lock()
	_, wasConfigured := d.graph.ConfiguredFiles[relPath]
	var isConfigured bool
	switch op {
	case "CREATE", "WRITE":
		info, err := os.Stat(fsEvent.Name)
		if err != nil {
			// Event delivery can race file deletion (e.g., atomic saves or temp
			// files); if the path disappeared, clear any stale tracked entry.
			if os.IsNotExist(err) {
				delete(d.graph.Files, relPath)
				delete(d.graph.ConfiguredFiles, relPath)
				delete(d.graph.State, relPath)
			}
			d.graph.mu.Unlock()
			return
		}

		// If a new directory was created, add it to the watcher
		if info.IsDir() {
			name := filepath.Base(fsEvent.Name)
			if d.gitCache != nil {
				dirPath := fsEvent.Name
				if absErr == nil {
					dirPath = absPath
				}
				d.gitCache.EnsureDir(dirPath)
				if d.gitCache.ShouldIgnore(dirPath) {
					d.graph.mu.Unlock()
					return
				}
			}
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
		d.graph.State[relPath] = &FileState{
			Lines:   newLines,
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		}

		// Update file info
		d.graph.Files[relPath] = &scanner.FileInfo{
			Path: relPath,
			Size: info.Size(),
			Ext:  filepath.Ext(relPath),
		}
		if d.graph.ConfiguredFiles == nil {
			d.graph.ConfiguredFiles = make(map[string]struct{})
		}
		if d.isConfiguredFile(relPath) {
			d.graph.ConfiguredFiles[relPath] = struct{}{}
			isConfigured = true
		} else {
			delete(d.graph.ConfiguredFiles, relPath)
		}

	case "REMOVE", "RENAME":
		// Record what was lost
		if prev, exists := d.graph.State[relPath]; exists {
			event.Lines = 0
			event.Delta = -prev.Lines
			event.SizeDelta = -prev.Size
		}
		delete(d.graph.Files, relPath)
		delete(d.graph.ConfiguredFiles, relPath)
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
	if d.graph.Topology != nil {
		event.ModuleIDs = d.graph.Topology.OwnersForFile(relPath)
		if len(event.ModuleIDs) == 1 {
			event.ModuleDependents = countTopologyDependents(d.graph.Topology, event.ModuleIDs[0])
		}
	}

	d.graph.Events = appendBoundedEvents(d.graph.Events, event)

	// Update working set for create/write events
	if d.graph.WorkingSet != nil && (op == "CREATE" || op == "WRITE") {
		d.graph.WorkingSet.Touch(relPath, event.Delta, event.IsHub, event.Importers)
	} else if d.graph.WorkingSet != nil && (op == "REMOVE" || op == "RENAME") {
		d.graph.WorkingSet.Remove(relPath)
	}

	d.graph.mu.Unlock()
	if d.publisher != nil {
		d.publisher.markDirty(time.Now())
	}
	// Persist the stale graph state synchronously so hooks and direct callers
	// observe the invalidation even when the coalesced publish loop is idle.
	if wasConfigured || isConfigured {
		d.writeState()
	}

	// Log event
	d.logEvent(event)

	if topologyManifest || sourceMembershipChange {
		d.computeTopology()
	}

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

func countTopologyDependents(graph *topology.Graph, id topology.ID) int {
	dependents := make(map[topology.ID]bool)
	for _, edge := range graph.Dependents[id] {
		if edge.Kind == topology.EdgeDependency {
			dependents[edge.From] = true
		}
	}
	return len(dependents)
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
	if err := requireRegularRuntimeFile(d.eventLog); err != nil && !os.IsNotExist(err) {
		return
	}
	f, err := runtimefile.OpenAppend(d.eventLog, 0o644)
	if err != nil {
		return
	}

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
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return
	}
	if err := f.Close(); err != nil {
		return
	}

	_ = trimEventLogToBytes(d.eventLog, int64(limits.MaxEventLogBytes), int64(limits.EventLogTrimToBytes))

}

// writeState persists current state for hooks to read
func (d *Daemon) writeState() {
	if d.ensurePublisher() != nil {
		return
	}
	_ = d.publisher.publish()
}

func appendBoundedEvents(events []Event, event Event) []Event {
	events = append(events, event)
	if len(events) <= limits.MaxDaemonEvents {
		return events
	}

	// Reallocate to release references to the old backing array.
	trimmed := append([]Event(nil), events[len(events)-limits.MaxDaemonEvents:]...)
	return trimmed
}

func trimEventLogToBytes(path string, maxBytes, keepBytes int64) error {
	if maxBytes <= 0 || keepBytes <= 0 || keepBytes > maxBytes {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxBytes {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if keepBytes > info.Size() {
		keepBytes = info.Size()
	}
	start := info.Size() - keepBytes
	tail := make([]byte, keepBytes)
	n, err := f.ReadAt(tail, start)
	if err != nil && err != io.EOF {
		return err
	}
	tail = tail[:n]
	if len(tail) == 0 {
		return nil
	}

	if idx := bytes.IndexByte(tail, '\n'); start > 0 && idx >= 0 && idx+1 < len(tail) {
		tail = tail[idx+1:]
	}

	return os.WriteFile(path, tail, 0644)
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
