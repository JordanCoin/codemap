package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

// os.WriteFile truncates before it writes, and on Linux inotify reports the
// empty file as its own WRITE. Processing that observation recorded a 0-line
// snapshot, so the content WRITE a moment later was measured against it: a
// 3-line file edited to 5 lines reported +5 (PR #205 CI, "Expected delta of
// +2, got 5"). The sequence is driven directly so the test fails on macOS
// too, where kqueue coalesces the two events.
func TestTruncationWriteIsHeldUntilContentLands(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "counter.go")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		root: root,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{},
			State: map[string]*FileState{"counter.go": {Lines: 3, Size: info.Size(), ModTime: info.ModTime().UnixNano()}},
		},
	}
	debouncer := newEventDebouncer(100 * time.Millisecond)
	event := fsnotify.Event{Name: path, Op: fsnotify.Write}
	apply := func(now time.Time) {
		switch d.debounceAction(debouncer, event, now) {
		case debounceSkip:
			debouncer.cancelPending(event.Name)
		case debounceDefer:
			debouncer.deferEvent(event, now)
		case debounceProcess:
			d.handleEvent(event)
		}
	}
	base := time.Unix(0, 0)

	// 1. O_TRUNC: the file is empty when the first WRITE is observed.
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	apply(base)
	if n := len(d.graph.Events); n != 0 {
		t.Fatalf("truncation observation recorded %d event(s): %+v", n, d.graph.Events)
	}
	if got := d.graph.State["counter.go"].Lines; got != 3 {
		t.Fatalf("truncation observation replaced the 3-line baseline with %d", got)
	}

	// 2. The content lands and its WRITE arrives inside the window.
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\nline4\nline5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	apply(base.Add(10 * time.Millisecond))

	// 3. The window elapses; the trailing event is measured against the baseline.
	for _, due := range debouncer.takeDue(base.Add(200 * time.Millisecond)) {
		d.handleEvent(due)
	}
	if n := len(d.graph.Events); n != 1 {
		t.Fatalf("got %d events, want exactly one trailing WRITE: %+v", n, d.graph.Events)
	}
	if e := d.graph.Events[0]; e.Lines != 5 || e.Delta != 2 {
		t.Fatalf("trailing event = lines %d delta %d, want lines 5 delta +2", e.Lines, e.Delta)
	}
}

// A file that genuinely became empty still reports it, once the window
// shows no content followed.
func TestTruncationToEmptyStillReported(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "gone.go")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		root:  root,
		graph: &Graph{Files: map[string]*scanner.FileInfo{}, State: map[string]*FileState{"gone.go": {Lines: 3, Size: info.Size(), ModTime: info.ModTime().UnixNano()}}},
	}
	debouncer := newEventDebouncer(100 * time.Millisecond)
	event := fsnotify.Event{Name: path, Op: fsnotify.Write}
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(0, 0)
	if got := d.debounceAction(debouncer, event, base); got != debounceDefer {
		t.Fatalf("empty observation action = %v, want defer", got)
	}
	debouncer.deferEvent(event, base)
	for _, due := range debouncer.takeDue(base.Add(200 * time.Millisecond)) {
		d.handleEvent(due)
	}
	if n := len(d.graph.Events); n != 1 {
		t.Fatalf("got %d events, want one: %+v", n, d.graph.Events)
	}
	if e := d.graph.Events[0]; e.Lines != 0 || e.Delta != -3 {
		t.Fatalf("event = lines %d delta %d, want lines 0 delta -3", e.Lines, e.Delta)
	}
}
