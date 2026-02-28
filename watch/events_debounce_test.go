package watch

import (
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

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
