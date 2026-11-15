package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// subagentCacheRepo is a committed git repo whose renderer and detached
// launcher are replaced: the launcher runs the refresh in a goroutine.
func subagentCacheRepo(t *testing.T, delay time.Duration) (root string, renders, launches *atomic.Int32) {
	t.Helper()
	root = makeRepoOnBranch(t, "main")
	renders, launches = new(atomic.Int32), new(atomic.Int32)
	origRender, origLaunch := renderSubagentBlock, startSubagentRefresh
	t.Cleanup(func() { renderSubagentBlock, startSubagentRefresh = origRender, origLaunch })
	renderSubagentBlock = func(ctx context.Context, root string) (string, error) {
		renders.Add(1)
		time.Sleep(delay)
		return "block " + subagentCacheKey(root) + "\n", nil
	}
	startSubagentRefresh = func(root string) bool {
		launches.Add(1)
		go refreshSubagentCache(root)
		return true
	}
	return root, renders, launches
}

func waitForSubagentRefresh(t *testing.T, root string) {
	t.Helper()
	dir, _ := subagentCacheDir(root)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		_, hit := readSubagentCache(root, subagentCacheKey(root))
		if _, err := os.Lstat(filepath.Join(dir, subagentRefreshLock)); hit && os.IsNotExist(err) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the refresh never filled the cache and dropped its lock")
		}
	}
}

func TestSubagentStartCachesPerHead(t *testing.T) {
	root, renders, _ := subagentCacheRepo(t, 0)
	first := subagentStartBlock(root, subagentStartTimeout)
	if renders.Load() != 1 || !strings.HasPrefix(first, "block ") {
		t.Fatalf("miss: renders=%d out=%q", renders.Load(), first)
	}
	waitForSubagentRefresh(t, root)
	if second := subagentStartBlock(root, subagentStartTimeout); second != first || renders.Load() != 1 {
		t.Fatalf("hit rendered again: renders=%d out=%q", renders.Load(), second)
	}
	runGitTestCmd(t, root, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "next")
	if third := subagentStartBlock(root, subagentStartTimeout); renders.Load() != 2 || third == first {
		t.Fatalf("new HEAD served the old block: renders=%d out=%q", renders.Load(), third)
	}
	waitForSubagentRefresh(t, root)
}

func TestSubagentStartCorruptCacheIsAMiss(t *testing.T) {
	root, renders, _ := subagentCacheRepo(t, 0)
	dir, _ := subagentCacheDir(root)
	if err := os.WriteFile(filepath.Join(dir, subagentCacheFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := subagentStartBlock(root, subagentStartTimeout); renders.Load() != 1 || !strings.HasPrefix(out, "block ") {
		t.Fatalf("corrupt cache: renders=%d out=%q", renders.Load(), out)
	}
	waitForSubagentRefresh(t, root)
}

// N subagents starting at once on a new HEAD cost one render, and every one
// of them gets the block when the render is fast.
func TestSubagentStartConcurrentMissesLaunchOneRender(t *testing.T) {
	root, renders, launches := subagentCacheRepo(t, 50*time.Millisecond)
	const n = 8
	outs := make([]string, n)
	var wg sync.WaitGroup
	for i := range outs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); outs[i] = subagentStartBlock(root, subagentStartTimeout) }(i)
	}
	wg.Wait()
	if launches.Load() != 1 || renders.Load() != 1 {
		t.Fatalf("launches=%d renders=%d, want exactly one of each", launches.Load(), renders.Load())
	}
	for i, out := range outs {
		if !strings.HasPrefix(out, "block ") {
			t.Fatalf("caller %d printed %q", i, out)
		}
	}
	waitForSubagentRefresh(t, root)
}

// A render slower than the budget prints nothing for anyone, yet still fills
// the cache afterwards and drops its lock.
func TestSubagentStartSlowRenderPrintsNothingAndFillsTheCacheLater(t *testing.T) {
	root, renders, launches := subagentCacheRepo(t, 0)
	// The render waits for every caller to give up, whatever the machine's
	// speed, so "slow" is a fact of the test, not a race.
	gate := make(chan struct{})
	fast := renderSubagentBlock
	renderSubagentBlock = func(ctx context.Context, root string) (string, error) {
		<-gate
		return fast(ctx, root)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out := subagentStartBlock(root, 20*time.Millisecond); out != "" {
				t.Errorf("slow render printed %q", out)
			}
		}()
	}
	wg.Wait()
	close(gate)
	waitForSubagentRefresh(t, root)
	if launches.Load() != 1 || renders.Load() != 1 {
		t.Fatalf("launches=%d renders=%d, want one", launches.Load(), renders.Load())
	}
}

func TestSubagentStartTakesOverAStaleLock(t *testing.T) {
	root, renders, launches := subagentCacheRepo(t, 0)
	dir, _ := subagentCacheDir(root)
	lock := filepath.Join(dir, subagentRefreshLock)
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out := subagentStartBlock(root, 50*time.Millisecond); out != "" || launches.Load() != 0 {
		t.Fatalf("a fresh lock must hold: out=%q launches=%d", out, launches.Load())
	}
	stale := time.Now().Add(-2 * subagentRefreshBound)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	if out := subagentStartBlock(root, subagentStartTimeout); !strings.HasPrefix(out, "block ") || renders.Load() != 1 {
		t.Fatalf("stale lock not taken over: out=%q renders=%d", out, renders.Load())
	}
	waitForSubagentRefresh(t, root)
}

// A fresh lock whose refresher died is taken over at once; one held by a
// live process stays held.
func TestSubagentStartTakesOverTheLockOfADeadRefresher(t *testing.T) {
	root, renders, launches := subagentCacheRepo(t, 0)
	dir, _ := subagentCacheDir(root)
	lock := filepath.Join(dir, subagentRefreshLock)
	if err := os.WriteFile(lock, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := subagentStartBlock(root, 50*time.Millisecond); out != "" || launches.Load() != 0 {
		t.Fatalf("a live refresher's lock must hold: out=%q launches=%d", out, launches.Load())
	}
	dead := exec.Command("git", "--version")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte(strconv.Itoa(dead.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := subagentStartBlock(root, subagentStartTimeout); !strings.HasPrefix(out, "block ") || renders.Load() != 1 {
		t.Fatalf("dead refresher's lock not taken over: out=%q renders=%d", out, renders.Load())
	}
	waitForSubagentRefresh(t, root)
}

// With no usable state dir the block is rendered in the foreground, not
// lost (the cache is an optimisation).
func TestSubagentStartWithoutAStateDirStillPrintsTheBlock(t *testing.T) {
	root, renders, launches := subagentCacheRepo(t, 0)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("USERPROFILE", filepath.Join(t.TempDir(), "missing"))
	if out := subagentStartBlock(root, subagentStartTimeout); !strings.HasPrefix(out, "block ") || renders.Load() != 1 || launches.Load() != 0 {
		t.Fatalf("out=%q renders=%d launches=%d", out, renders.Load(), launches.Load())
	}
}
