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

// stormEvents is a 300-file checkout the way git writes one: every file
// CREATEd, the first 200 also WRITTEN, 500 events in all.
func stormEvents(t *testing.T, root string) []fsnotify.Event {
	t.Helper()
	const files = 300
	events := make([]fsnotify.Event, 0, 500)
	for i := 0; i < files; i++ {
		path := filepath.Join(root, fmt.Sprintf("f%03d.go", i))
		if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		events = append(events, fsnotify.Event{Name: path, Op: fsnotify.Create})
		if i < 200 {
			events = append(events, fsnotify.Event{Name: path, Op: fsnotify.Write})
		}
	}
	return events
}

// The detector takes its clock from the caller, so the same 500 arrivals
// 0.4 ms apart make the same decisions every run: the first burstThreshold
// are handled one by one, everything after is queued, and one settle
// closes the burst with one event per path.
func TestBurstDetectorOpensOnceForAStorm(t *testing.T) {
	events := stormEvents(t, t.TempDir())
	var b burstDetector
	clock := time.Unix(1_700_000_000, 0)
	const gap = 400 * time.Microsecond // 500 events in 200 ms

	perFile := 0
	for _, event := range events {
		if !b.observe(clock) {
			perFile++
		} else {
			if delay := b.enqueue(event, clock); delay != burstSettle {
				t.Fatalf("enqueue delay = %v, want burstSettle %v", delay, burstSettle)
			}
		}
		clock = clock.Add(gap)
	}
	if perFile != burstThreshold {
		t.Fatalf("events handled before the burst opened = %d, want burstThreshold %d", perFile, burstThreshold)
	}
	batch := b.take()
	// The first 16 events are f000..f007 (CREATE+WRITE each); the batch holds
	// the other 292 files once each, last op wins.
	if len(batch) != 300-burstThreshold/2 {
		t.Fatalf("batch = %d events, want %d (one per remaining file)", len(batch), 300-burstThreshold/2)
	}
	if got := batch[0]; filepath.Base(got.Name) != "f008.go" || got.Op != fsnotify.Write {
		t.Fatalf("batch[0] = %s %v, want f008.go WRITE (last op for the path)", filepath.Base(got.Name), got.Op)
	}
	if b.pending() {
		t.Fatal("take must close the burst")
	}
	// After the burst, a lone event starts a fresh window and is not batched.
	if b.observe(clock.Add(time.Second)) {
		t.Fatal("a single event after a flush must not open a burst")
	}
}

// The ceiling clips the settle delay so a storm that never goes quiet still
// flushes after burstCeiling.
func TestBurstDetectorCeilingClipsSettle(t *testing.T) {
	var b burstDetector
	clock := time.Unix(1_700_000_000, 0)
	for i := 0; i <= burstThreshold; i++ {
		b.observe(clock)
	}
	event := fsnotify.Event{Name: "a.go", Op: fsnotify.Create}
	if delay := b.enqueue(event, clock); delay != burstSettle {
		t.Fatalf("first enqueue delay = %v, want %v", delay, burstSettle)
	}
	late := clock.Add(burstCeiling - 100*time.Millisecond)
	if delay := b.enqueue(event, late); delay != 100*time.Millisecond {
		t.Fatalf("delay near the ceiling = %v, want 100ms", delay)
	}
	if delay := b.enqueue(event, clock.Add(burstCeiling+time.Second)); delay != 0 {
		t.Fatalf("delay past the ceiling = %v, want 0", delay)
	}
}

func newBurstTestDaemon(t *testing.T, root string) *Daemon {
	t.Helper()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
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
	return d
}

type burstCounters struct {
	git, topology, refreshes atomic.Int32
}

// stubBurstSeams counts the shared costs. The daemon must be stopped before
// the seams are restored, so callers register Stop after this.
func stubBurstSeams(t *testing.T) *burstCounters {
	t.Helper()
	c := &burstCounters{}
	origDirty, origDirtyPaths := isFileDirtyFn, dirtyPathsFn
	origTopology, origRefresh := daemonComputeTopology, daemonRefreshDependencies
	t.Cleanup(func() {
		isFileDirtyFn, dirtyPathsFn = origDirty, origDirtyPaths
		daemonComputeTopology, daemonRefreshDependencies = origTopology, origRefresh
	})
	isFileDirtyFn = func(string, string) bool { c.git.Add(1); return false }
	dirtyPathsFn = func(_ string, rel []string) map[string]bool {
		c.git.Add(1)
		if len(rel) == 0 {
			return nil
		}
		return map[string]bool{rel[0]: true}
	}
	daemonComputeTopology = func(*Daemon) { c.topology.Add(1) }
	daemonRefreshDependencies = func(*Daemon) { c.refreshes.Add(1) }
	return c
}

// A flushed batch pays each shared cost exactly once, however many files it
// holds, and still records one event per file with the batched dirty flag.
func TestHandleBurstPaysEachSharedCostOnce(t *testing.T) {
	root := t.TempDir()
	events := stormEvents(t, root)
	d := newBurstTestDaemon(t, root)
	c := stubBurstSeams(t)
	t.Cleanup(func() { d.Stop() })

	var b burstDetector
	clock := time.Unix(1_700_000_000, 0)
	for _, event := range events {
		b.observe(clock)
		b.enqueue(event, clock)
		clock = clock.Add(400 * time.Microsecond)
	}
	d.handleBurst(b.take())

	if got := c.git.Load(); got != 1 {
		t.Fatalf("git subprocesses = %d, want exactly 1", got)
	}
	if got := c.topology.Load(); got != 1 {
		t.Fatalf("topology recomputes = %d, want exactly 1", got)
	}
	if got := c.refreshes.Load(); got != 1 {
		t.Fatalf("dependency refreshes = %d, want exactly 1", got)
	}
	recorded := d.GetEvents(1000)
	if len(recorded) != 300 {
		t.Fatalf("events recorded = %d, want 300 (one per file)", len(recorded))
	}
	dirty := 0
	for _, event := range recorded {
		if event.Dirty {
			dirty++
		}
	}
	if dirty != 1 {
		t.Fatalf("dirty events = %d, want 1 (the one path the batched git call reported)", dirty)
	}
}

// End to end through the real event loop on the wall clock. CI load can
// split one storm into several bursts, so this asserts only that batching
// happened: per-file costs stay far below one per file. Exact counts live in
// the synthetic-clock tests above.
func TestCheckoutStormIsBatchedThroughTheEventLoop(t *testing.T) {
	root := t.TempDir()
	events := stormEvents(t, root)
	d := newBurstTestDaemon(t, root)
	c := stubBurstSeams(t)
	t.Cleanup(func() { d.Stop() })

	d.eventLoopWG.Add(1)
	go func() {
		defer d.eventLoopWG.Done()
		d.eventLoop()
	}()
	for _, event := range events {
		d.watcher.Events <- event
	}
	waitForWatchCondition(t, 10*time.Second, func() bool {
		return len(d.GetEvents(1000)) >= 300
	})
	time.Sleep(burstSettle + 50*time.Millisecond)

	// Each burst costs at most burstThreshold per-file calls before it is
	// recognised plus one batched call; allow a slow runner to split the
	// storm four ways. Unbatched, the count would be 500.
	const bound = 4 * (burstThreshold + 1)
	if got := c.git.Load(); got > bound {
		t.Fatalf("git subprocesses = %d, want at most %d", got, bound)
	}
	if got := c.topology.Load(); got > bound {
		t.Fatalf("topology recomputes = %d, want at most %d", got, bound)
	}
	if got := c.refreshes.Load(); got > bound {
		t.Fatalf("dependency refreshes = %d, want at most %d", got, bound)
	}
}

// Below the threshold nothing changes: a lone event is handled at once with
// its own git call.
func TestFewEventsAreNotBatched(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	if err := os.WriteFile(path, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := newBurstTestDaemon(t, root)
	var perFile, batched atomic.Int32
	origDirty, origDirtyPaths := isFileDirtyFn, dirtyPathsFn
	origTopology, origRefresh := daemonComputeTopology, daemonRefreshDependencies
	t.Cleanup(func() {
		isFileDirtyFn, dirtyPathsFn = origDirty, origDirtyPaths
		daemonComputeTopology, daemonRefreshDependencies = origTopology, origRefresh
	})
	isFileDirtyFn = func(string, string) bool { perFile.Add(1); return false }
	dirtyPathsFn = func(string, []string) map[string]bool { batched.Add(1); return nil }
	daemonComputeTopology = func(*Daemon) {}
	daemonRefreshDependencies = func(*Daemon) {}
	t.Cleanup(func() { d.Stop() })

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
