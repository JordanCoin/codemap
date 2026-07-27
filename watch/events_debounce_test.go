package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestDaemonDebounceActionUsesSizeAndModTime(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	initialModTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(path, initialModTime, initialModTime); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root: root,
		graph: &Graph{State: map[string]*FileState{
			"main.go": {Size: info.Size(), ModTime: info.ModTime().UnixNano()},
		}},
	}
	debouncer := newEventDebouncer(100 * time.Millisecond)
	event := fsnotify.Event{Name: path, Op: fsnotify.Write}
	base := time.Unix(0, 0)

	if got := d.debounceAction(debouncer, event, base); got != debounceProcess {
		t.Fatalf("first write action = %v, want process", got)
	}

	if err := os.WriteFile(path, []byte("package bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changedModTime := initialModTime.Add(time.Second)
	if err := os.Chtimes(path, changedModTime, changedModTime); err != nil {
		t.Fatal(err)
	}
	if got := d.debounceAction(debouncer, event, base.Add(10*time.Millisecond)); got != debounceDefer {
		t.Fatalf("same-size changed write action = %v, want defer", got)
	}

	info, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d.graph.State["main.go"].Size = info.Size()
	d.graph.State["main.go"].ModTime = info.ModTime().UnixNano()
	if got := d.debounceAction(debouncer, event, base.Add(20*time.Millisecond)); got != debounceSkip {
		t.Fatalf("duplicate write action = %v, want skip", got)
	}
}

func TestDaemonDebounceActionProcessesMissingCache(t *testing.T) {
	root := t.TempDir()
	d := &Daemon{root: root, graph: &Graph{State: map[string]*FileState{}}}
	debouncer := newEventDebouncer(100 * time.Millisecond)
	event := fsnotify.Event{Name: filepath.Join(root, "missing.go"), Op: fsnotify.Write}
	base := time.Unix(0, 0)

	if got := d.debounceAction(debouncer, event, base); got != debounceProcess {
		t.Fatalf("first missing-cache action = %v, want process", got)
	}
	if got := d.debounceAction(debouncer, event, base.Add(10*time.Millisecond)); got != debounceProcess {
		t.Fatalf("rapid missing-cache action = %v, want process", got)
	}
}

func TestDaemonDebounceActionProcessesMissingTrackedFile(t *testing.T) {
	root := t.TempDir()
	d := &Daemon{root: root, graph: &Graph{State: map[string]*FileState{
		"missing.go": {},
	}}}
	debouncer := newEventDebouncer(100 * time.Millisecond)
	event := fsnotify.Event{Name: filepath.Join(root, "missing.go"), Op: fsnotify.Write}
	base := time.Unix(0, 0)

	if got := d.debounceAction(debouncer, event, base); got != debounceProcess {
		t.Fatalf("first missing-file action = %v, want process", got)
	}
	if got := d.debounceAction(debouncer, event, base.Add(10*time.Millisecond)); got != debounceProcess {
		t.Fatalf("rapid missing-file action = %v, want process", got)
	}
}

func TestEventDebouncerSkipsRapidWrites(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)
	event := fsnotify.Event{Name: "src/file.go", Op: fsnotify.Write}

	if debouncer.shouldSkip(event, base) {
		t.Fatal("first write should not be skipped")
	}
	if !debouncer.shouldSkip(event, base.Add(50*time.Millisecond)) {
		t.Fatal("rapid write should be skipped")
	}
	if !debouncer.shouldSkip(event, base.Add(120*time.Millisecond)) {
		t.Fatal("rapid write should extend the quiet window")
	}
	if debouncer.shouldSkip(event, base.Add(220*time.Millisecond)) {
		t.Fatal("write outside debounce window should not be skipped")
	}
}

func TestEventDebouncerDoesNotSkipNonWriteOps(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)
	path := "src/tmp.go"

	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Create}, base) {
		t.Fatal("create should not be skipped")
	}
	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Remove}, base.Add(5*time.Millisecond)) {
		t.Fatal("remove should not be skipped even after rapid create")
	}
	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Write}, base.Add(10*time.Millisecond)) {
		t.Fatal("first write should not be skipped")
	}
	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Rename}, base.Add(15*time.Millisecond)) {
		t.Fatal("rename should not be skipped even after rapid write")
	}
}

func TestEventDebouncerDoesNotSkipCombinedLifecycleOps(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)
	path := "src/tmp.go"

	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Create | fsnotify.Write}, base) {
		t.Fatal("create+write should not be skipped")
	}
	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Rename | fsnotify.Write}, base.Add(10*time.Millisecond)) {
		t.Fatal("rename+write should not be skipped")
	}
	if debouncer.shouldSkip(fsnotify.Event{Name: path, Op: fsnotify.Remove | fsnotify.Write}, base.Add(20*time.Millisecond)) {
		t.Fatal("remove+write should not be skipped")
	}
}

func TestEventDebouncerPrunesStaleEntries(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)

	debouncer.shouldSkip(fsnotify.Event{Name: "src/old.go", Op: fsnotify.Write}, base)
	if len(debouncer.lastSeen) != 1 {
		t.Fatalf("expected 1 tracked path, got %d", len(debouncer.lastSeen))
	}

	debouncer.shouldSkip(fsnotify.Event{Name: "src/new.go", Op: fsnotify.Write}, base.Add(2*time.Second))

	if _, exists := debouncer.lastSeen["src/old.go"]; exists {
		t.Fatal("expected stale path entry to be pruned")
	}
	if _, exists := debouncer.lastSeen["src/new.go"]; !exists {
		t.Fatal("expected recent path entry to be retained")
	}
}

func TestEventDebouncerPendingWritesKeepLatestUntilDue(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)
	first := fsnotify.Event{Name: "src/file.go", Op: fsnotify.Write}
	latest := fsnotify.Event{Name: "src/file.go", Op: fsnotify.Write | fsnotify.Chmod}

	debouncer.deferEvent(first, base)
	debouncer.deferEvent(latest, base.Add(50*time.Millisecond))

	if got := debouncer.takeDue(base.Add(149 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("takeDue before quiet window returned %v", got)
	}
	if delay, ok := debouncer.nextDelay(base.Add(100 * time.Millisecond)); !ok || delay != 50*time.Millisecond {
		t.Fatalf("nextDelay = (%v, %v), want (50ms, true)", delay, ok)
	}
	if delay, ok := debouncer.nextDelay(base.Add(151 * time.Millisecond)); !ok || delay != 0 {
		t.Fatalf("overdue nextDelay = (%v, %v), want (0, true)", delay, ok)
	}

	got := debouncer.takeDue(base.Add(150 * time.Millisecond))
	if len(got) != 1 || got[0] != latest {
		t.Fatalf("takeDue after quiet window = %v, want latest event %v", got, latest)
	}
	if _, ok := debouncer.nextDelay(base.Add(150 * time.Millisecond)); ok {
		t.Fatal("expected no timer deadline after pending event drained")
	}
}

func TestEventLoopDrainsLatestPendingWriteOnShutdown(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.watcher.Close(); err != nil {
		t.Fatal(err)
	}
	d.watcher.Events = make(chan fsnotify.Event)
	d.watcher.Errors = make(chan error)
	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			d.Stop()
		}
	})

	event := fsnotify.Event{Name: path, Op: fsnotify.Write}
	d.watcher.Events <- event
	waitForWatchCondition(t, time.Second, func() bool {
		return len(d.GetEvents(10)) == 1
	})
	d.watcher.Events <- event
	time.Sleep(20 * time.Millisecond)
	if got := len(d.GetEvents(10)); got != 1 {
		t.Fatalf("duplicate write produced %d events, want 1", got)
	}

	if err := os.WriteFile(path, []byte("package bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	changedModTime := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, changedModTime, changedModTime); err != nil {
		t.Fatal(err)
	}
	d.watcher.Events <- event
	d.Stop()
	stopped = true
	got := d.GetEvents(10)
	if len(got) != 2 || got[1].Lines != 1 {
		t.Fatalf("events after shutdown drain = %#v, want latest pending write", got)
	}
}

func TestEventDebouncerCancelsAndDrainsPendingWrites(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)
	canceled := fsnotify.Event{Name: "src/canceled.go", Op: fsnotify.Write}
	remaining := fsnotify.Event{Name: "src/remaining.go", Op: fsnotify.Write}

	debouncer.deferEvent(canceled, base)
	debouncer.deferEvent(remaining, base.Add(10*time.Millisecond))
	debouncer.cancelPending(canceled.Name)

	got := debouncer.takeAll()
	if len(got) != 1 || got[0] != remaining {
		t.Fatalf("takeAll = %v, want remaining event %v", got, remaining)
	}
	if got := debouncer.takeAll(); len(got) != 0 {
		t.Fatalf("second takeAll returned already-drained events: %v", got)
	}
}

func TestEventDebouncerLifecycleCancelsSamePathBeforeDueFlush(t *testing.T) {
	debouncer := newEventDebouncer(100 * time.Millisecond)
	base := time.Unix(0, 0)
	canceled := fsnotify.Event{Name: "src/canceled.go", Op: fsnotify.Write}
	remaining := fsnotify.Event{Name: "src/remaining.go", Op: fsnotify.Write}

	debouncer.deferEvent(canceled, base)
	debouncer.deferEvent(remaining, base)
	got := debouncer.takeDueBeforeEvent(
		fsnotify.Event{Name: canceled.Name, Op: fsnotify.Remove},
		base.Add(100*time.Millisecond),
	)

	if len(got) != 1 || got[0] != remaining {
		t.Fatalf("due writes before lifecycle event = %v, want only %v", got, remaining)
	}
}
