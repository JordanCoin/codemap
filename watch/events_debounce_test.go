package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestDaemonDoesNotDebounceWriteWhenCachedSizeIsStale(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc changed() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{
		root: root,
		graph: &Graph{State: map[string]*FileState{
			"main.go": {Size: 0},
		}},
	}
	debouncer := newEventDebouncer(100 * time.Millisecond)
	event := fsnotify.Event{Name: path, Op: fsnotify.Write}
	base := time.Unix(0, 0)

	if d.shouldSkipEvent(debouncer, event, base) {
		t.Fatal("first write should not be skipped")
	}
	if d.shouldSkipEvent(debouncer, event, base.Add(10*time.Millisecond)) {
		t.Fatal("final write should refresh stale cached state")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	d.graph.State["main.go"].Size = info.Size()
	if !d.shouldSkipEvent(debouncer, event, base.Add(20*time.Millisecond)) {
		t.Fatal("duplicate write should be skipped once cached state is current")
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
	if debouncer.shouldSkip(event, base.Add(150*time.Millisecond)) {
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
