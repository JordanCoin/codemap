package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"codemap/internal/projectpath"
)

// startDaemonLookalike starts an inert process whose command line reads
// `... watch daemon <root>`, so ownership checks classify it as this root's
// daemon. The returned stop kills it and waits for the reap; t.Cleanup also
// stops it.
func startDaemonLookalike(t *testing.T, root string) (pid int, stop func()) {
	t.Helper()
	process := exec.Command(os.Args[0], "-test.run=TestHelperWatchDaemonProcess", "--", "watch", "daemon", root)
	process.Env = append(os.Environ(), "CODEMAP_TEST_WATCH_HELPER=1")
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = process.Wait(); close(waited) }()
	stop = func() { _ = process.Process.Kill(); <-waited }
	t.Cleanup(stop)
	return process.Process.Pid, stop
}

// TestClaimDaemonPIDAdmitsExactlyOneConcurrentClaimant is #155 defect 1: the
// check and the publish happen under one lock, so N simultaneous daemons
// produce one owner, and a dead owner's pid is reclaimed with evidence kept.
func TestClaimDaemonPIDAdmitsExactlyOneConcurrentClaimant(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	const claimants = 6
	pids := make([]int, claimants)
	stops := make([]func(), claimants)
	for i := range pids {
		pids[i], stops[i] = startDaemonLookalike(t, root)
	}

	won := make([]bool, claimants)
	errs := make([]error, claimants)
	var wg sync.WaitGroup
	for i := range pids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			won[i], errs[i] = ClaimDaemonPID(root, pids[i])
		}(i)
	}
	wg.Wait()

	winner := -1
	for i := range pids {
		if errs[i] != nil {
			t.Fatalf("claimant %d: %v", pids[i], errs[i])
		}
		if won[i] {
			if winner >= 0 {
				t.Fatalf("two claimants won: %d and %d", pids[winner], pids[i])
			}
			winner = i
		}
	}
	if winner < 0 {
		t.Fatal("no claimant won")
	}
	if got, err := ReadPID(root); err != nil || got != pids[winner] {
		t.Fatalf("ReadPID() = %d, %v; want winner %d", got, err, pids[winner])
	}

	// Dead owner: the next claimant reclaims, and the stale pid is kept aside.
	stops[winner]()
	loser := (winner + 1) % claimants
	if claimed, err := ClaimDaemonPID(root, pids[loser]); err != nil || !claimed {
		t.Fatalf("reclaim after owner death = %v, %v; want true", claimed, err)
	}
	if got, err := ReadPID(root); err != nil || got != pids[loser] {
		t.Fatalf("ReadPID() after reclaim = %d, %v; want %d", got, err, pids[loser])
	}
	if stale, _ := filepath.Glob(filepath.Join(projectpath.ProjectRuntimeDir(root), "watch.pid.stale*")); len(stale) == 0 {
		t.Fatal("dead owner's pid file was not preserved as evidence")
	}
}
