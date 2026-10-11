package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// A rebase or branch switch writes every touched file through a temp file and
// a rename, so a 300-file checkout used to cost 300 git subprocesses, 300
// state writes and 300 topology recomputes on the event loop (#201). The
// storm must coalesce into one batch.
func TestCheckoutStormCoalescesIntoOneBatch(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	const files = 300
	paths := make([]string, files)
	for i := range paths {
		paths[i] = filepath.Join(root, fmt.Sprintf("f%03d.go", i))
		if err := os.WriteFile(paths[i], []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.watcher.Close(); err != nil {
		t.Fatal(err)
	}
	d.watcher.Events = make(chan fsnotify.Event, 1024)
	d.watcher.Errors = make(chan error)
	if err := os.MkdirAll(d.runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var gitCalls, topology, refreshes atomic.Int32
	origDirty, origDirtyPaths := isFileDirtyFn, dirtyPathsFn
	origTopology, origRefresh := daemonComputeTopology, daemonRefreshDependencies
	t.Cleanup(func() {
		isFileDirtyFn, dirtyPathsFn = origDirty, origDirtyPaths
		daemonComputeTopology, daemonRefreshDependencies = origTopology, origRefresh
	})
	// Registered after the seam restore so it runs first: the loop must be
	// stopped before the seams change under it.
	t.Cleanup(func() { d.Stop() })
	isFileDirtyFn = func(string, string) bool { gitCalls.Add(1); return false }
	dirtyPathsFn = func(_ string, rel []string) map[string]bool {
		gitCalls.Add(1)
		return map[string]bool{rel[0]: true}
	}
	daemonComputeTopology = func(*Daemon) { topology.Add(1) }
	daemonRefreshDependencies = func(*Daemon) { refreshes.Add(1) }

	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()

	// 500 events for 300 files inside 200 ms: every file CREATEd, the first
	// 200 also RENAMEd into place, the way git writes a checkout.
	start := time.Now()
	for i := range paths {
		d.watcher.Events <- fsnotify.Event{Name: paths[i], Op: fsnotify.Create}
		if i < 200 {
			d.watcher.Events <- fsnotify.Event{Name: paths[i], Op: fsnotify.Write}
		}
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("feeding the storm took %v, want under 200ms for the test to mean anything", elapsed)
	}

	waitForWatchCondition(t, 5*time.Second, func() bool {
		return len(d.GetEvents(files+1)) >= files
	})
	// Let any trailing per-file work surface before counting.
	time.Sleep(burstSettle + 50*time.Millisecond)

	// The first burstThreshold events are handled one by one before the storm
	// is recognised; everything after that is one batch.
	const leading = burstThreshold
	if got := gitCalls.Load(); got > leading+1 {
		t.Fatalf("git subprocesses = %d, want at most %d (per-file before detection + one batch)", got, leading+1)
	}
	if got := topology.Load(); got > leading+1 {
		t.Fatalf("topology recomputes = %d, want at most %d", got, leading+1)
	}
	if got := refreshes.Load(); got > leading+1 {
		t.Fatalf("dependency refreshes = %d, want at most %d", got, leading+1)
	}
	// Files handled before detection may log both their CREATE and WRITE;
	// every file in the batch logs exactly once, last op wins.
	events := d.GetEvents(files + leading + 1)
	if len(events) < files || len(events) > files+leading {
		t.Fatalf("events recorded = %d, want between %d and %d", len(events), files, files+leading)
	}
	dirty := 0
	for _, event := range events {
		if event.Dirty {
			dirty++
		}
	}
	if dirty != 1 {
		t.Fatalf("dirty events = %d, want 1 (the one path the batched git call reported)", dirty)
	}
}

// Below the threshold nothing changes: a handful of events are still handled
// one at a time with their own git call each.
func TestFewEventsAreNotBatched(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.watcher.Close(); err != nil {
		t.Fatal(err)
	}
	d.watcher.Events = make(chan fsnotify.Event, 8)
	d.watcher.Errors = make(chan error)
	if err := os.MkdirAll(d.runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var perFile, batched atomic.Int32
	origDirty, origDirtyPaths := isFileDirtyFn, dirtyPathsFn
	origTopology, origRefresh := daemonComputeTopology, daemonRefreshDependencies
	t.Cleanup(func() {
		isFileDirtyFn, dirtyPathsFn = origDirty, origDirtyPaths
		daemonComputeTopology, daemonRefreshDependencies = origTopology, origRefresh
	})
	t.Cleanup(func() { d.Stop() })
	isFileDirtyFn = func(string, string) bool { perFile.Add(1); return false }
	dirtyPathsFn = func(string, []string) map[string]bool { batched.Add(1); return nil }
	daemonComputeTopology = func(*Daemon) {}
	daemonRefreshDependencies = func(*Daemon) {}

	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()
	d.watcher.Events <- fsnotify.Event{Name: path, Op: fsnotify.Create}
	waitForWatchCondition(t, time.Second, func() bool { return perFile.Load() == 1 })
	if batched.Load() != 0 {
		t.Fatalf("a single event must not open a burst")
	}
}
