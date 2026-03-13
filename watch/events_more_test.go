package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codemap/scanner"

	"github.com/fsnotify/fsnotify"
)

func TestNewEventDebouncerSetsPruneWindow(t *testing.T) {
	tests := []struct {
		name      string
		window    time.Duration
		wantPrune time.Duration
	}{
		{name: "minimum one second prune window", window: 50 * time.Millisecond, wantPrune: time.Second},
		{name: "ten times window when larger", window: 250 * time.Millisecond, wantPrune: 2500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newEventDebouncer(tt.window)
			if d.window != tt.window {
				t.Fatalf("window = %v, want %v", d.window, tt.window)
			}
			if d.pruneAfter != tt.wantPrune {
				t.Fatalf("pruneAfter = %v, want %v", d.pruneAfter, tt.wantPrune)
			}
			if d.lastSeen == nil {
				t.Fatal("expected lastSeen map to be initialized")
			}
		})
	}
}

func TestTrimEventLogToBytesNoOpCases(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.log")
	original := []byte("line one\nline two\n")
	if err := os.WriteFile(logPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		path     string
		maxBytes int64
		keep     int64
	}{
		{name: "invalid max bytes", path: logPath, maxBytes: 0, keep: 10},
		{name: "invalid keep bytes", path: logPath, maxBytes: 10, keep: 0},
		{name: "keep larger than max", path: logPath, maxBytes: 10, keep: 11},
		{name: "file missing returns nil", path: filepath.Join(t.TempDir(), "missing.log"), maxBytes: 10, keep: 5},
		{name: "size below max no trim", path: logPath, maxBytes: 1024, keep: 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := trimEventLogToBytes(tt.path, tt.maxBytes, tt.keep); err != nil {
				t.Fatalf("trimEventLogToBytes error: %v", err)
			}
		})
	}

	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("expected no-op trim to leave file unchanged, got %q", string(got))
	}
}

func TestHandleEventSkipsGitignoredPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignoredFile := filepath.Join(root, "ignored.go")
	if err := os.WriteFile(ignoredFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := scanner.NewGitIgnoreCache(root)
	cache.EnsureDir(root)

	d := &Daemon{
		root:     root,
		gitCache: cache,
		graph: &Graph{
			Files:  map[string]*scanner.FileInfo{},
			State:  map[string]*FileState{},
			Events: []Event{},
		},
	}

	d.handleEvent(fsnotify.Event{Name: ignoredFile, Op: fsnotify.Write})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	if len(d.graph.Events) != 0 {
		t.Fatalf("expected no events for gitignored path, got %d", len(d.graph.Events))
	}
}

func TestHandleEventIgnoresUnknownOperation(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "file.go")
	if err := os.WriteFile(target, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root: root,
		graph: &Graph{
			Files:  map[string]*scanner.FileInfo{},
			State:  map[string]*FileState{},
			Events: []Event{},
		},
	}

	d.handleEvent(fsnotify.Event{Name: target, Op: fsnotify.Chmod})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()
	if len(d.graph.Events) != 0 {
		t.Fatalf("expected no events for chmod-only operation, got %d", len(d.graph.Events))
	}
}

func TestHandleEventRemoveDeletesTrackedState(t *testing.T) {
	root := t.TempDir()
	rel := "old.go"
	abs := filepath.Join(root, rel)

	d := &Daemon{
		root: root,
		graph: &Graph{
			Files: map[string]*scanner.FileInfo{
				rel: {Path: rel, Size: 20, Ext: ".go"},
			},
			State: map[string]*FileState{
				rel: {Lines: 5, Size: 20},
			},
			Events: []Event{},
		},
	}

	d.handleEvent(fsnotify.Event{Name: abs, Op: fsnotify.Remove})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	if _, ok := d.graph.Files[rel]; ok {
		t.Fatalf("expected removed file %q to be dropped from graph", rel)
	}
	if _, ok := d.graph.State[rel]; ok {
		t.Fatalf("expected removed state %q to be dropped from cache", rel)
	}
	if len(d.graph.Events) != 1 {
		t.Fatalf("expected one remove event, got %d", len(d.graph.Events))
	}

	ev := d.graph.Events[0]
	if ev.Op != "REMOVE" {
		t.Fatalf("event op = %q, want REMOVE", ev.Op)
	}
	if ev.Delta != -5 {
		t.Fatalf("event delta = %d, want -5", ev.Delta)
	}
	if ev.SizeDelta != -20 {
		t.Fatalf("event size delta = %d, want -20", ev.SizeDelta)
	}
}

func TestHandleEventWriteAddsNewFileState(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	rel := "main.go"
	abs := filepath.Join(root, rel)
	content := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root:     root,
		eventLog: filepath.Join(root, ".codemap", "events.log"),
		graph: &Graph{
			Files:  map[string]*scanner.FileInfo{},
			State:  map[string]*FileState{},
			Events: []Event{},
		},
	}

	d.handleEvent(fsnotify.Event{Name: abs, Op: fsnotify.Write})

	d.graph.mu.RLock()
	defer d.graph.mu.RUnlock()

	state, ok := d.graph.State[rel]
	if !ok {
		t.Fatalf("expected %q to be tracked in file state", rel)
	}
	if state.Lines != 3 {
		t.Fatalf("state lines = %d, want 3", state.Lines)
	}
	if len(d.graph.Events) != 1 {
		t.Fatalf("expected one write event, got %d", len(d.graph.Events))
	}

	ev := d.graph.Events[0]
	if ev.Op != "WRITE" {
		t.Fatalf("event op = %q, want WRITE", ev.Op)
	}
	if ev.Path != rel {
		t.Fatalf("event path = %q, want %q", ev.Path, rel)
	}
	if ev.Delta != 3 {
		t.Fatalf("event delta = %d, want 3", ev.Delta)
	}
}
