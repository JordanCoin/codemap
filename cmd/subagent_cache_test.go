package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// subagentCacheRepo is a committed git repo with the renderer and the
// background refresh replaced by counters.
func subagentCacheRepo(t *testing.T) (root string, renders, refreshes *int) {
	t.Helper()
	root = makeRepoOnBranch(t, "main")
	renders, refreshes = new(int), new(int)
	origRender, origRefresh := renderSubagentBlock, startSubagentRefresh
	t.Cleanup(func() { renderSubagentBlock, startSubagentRefresh = origRender, origRefresh })
	renderSubagentBlock = func(ctx context.Context, root string) (string, error) {
		*renders++
		return "block " + subagentCacheKey(root) + "\n", nil
	}
	startSubagentRefresh = func(string) { *refreshes++ }
	return root, renders, refreshes
}

func runSubagentStartHook(t *testing.T, root string) string {
	t.Helper()
	var out string
	withStdinInput(t, `{"cwd":"`+filepath.ToSlash(root)+`"}`, func() {
		out = captureOutput(func() {
			if err := hookSubagentStart(root, subagentStartTimeout); err != nil {
				t.Fatalf("hookSubagentStart: %v", err)
			}
		})
	})
	return out
}

func TestSubagentStartCachesPerHead(t *testing.T) {
	root, renders, _ := subagentCacheRepo(t)

	first := runSubagentStartHook(t, root)
	if *renders != 1 || !strings.HasPrefix(first, "block ") {
		t.Fatalf("miss: renders=%d out=%q", *renders, first)
	}
	dir, _ := subagentCacheDir(root)
	if _, err := os.Stat(filepath.Join(dir, subagentCacheFile)); err != nil {
		t.Fatalf("miss did not store the block: %v", err)
	}
	if second := runSubagentStartHook(t, root); second != first || *renders != 1 {
		t.Fatalf("hit rendered again: renders=%d out=%q", *renders, second)
	}

	oldKey := subagentCacheKey(root)
	runGitTestCmd(t, root, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "next")
	if subagentCacheKey(root) == oldKey {
		t.Fatal("cache key did not change with HEAD")
	}
	if third := runSubagentStartHook(t, root); *renders != 2 || third == first {
		t.Fatalf("new HEAD served the old block: renders=%d out=%q", *renders, third)
	}
}

func TestSubagentStartCorruptCacheIsAMiss(t *testing.T) {
	root, renders, _ := subagentCacheRepo(t)
	dir, _ := subagentCacheDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, subagentCacheFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out := runSubagentStartHook(t, root); *renders != 1 || !strings.HasPrefix(out, "block ") {
		t.Fatalf("corrupt cache: renders=%d out=%q", *renders, out)
	}
}

func TestSubagentStartTimeoutPrintsNothingAndRefreshes(t *testing.T) {
	root, _, refreshes := subagentCacheRepo(t)
	renderSubagentBlock = func(ctx context.Context, root string) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}
	start := time.Now()
	withStdinInput(t, `{"cwd":"`+filepath.ToSlash(root)+`"}`, func() {
		out := captureOutput(func() { _ = hookSubagentStart(root, subagentStartTimeout) })
		if out != "" {
			t.Fatalf("timed-out render printed %q", out)
		}
	})
	if *refreshes != 1 {
		t.Fatalf("background refreshes = %d, want 1", *refreshes)
	}
	if elapsed := time.Since(start); elapsed > subagentStartTimeout+2*time.Second {
		t.Fatalf("hook took %v past its deadline", elapsed)
	}
}

// The background render starts before the foreground one, so a hook killed by
// its timeout still leaves one running; a fresh lock means one already is.
func TestSubagentStartLaunchesRefreshUpFrontUnlessLocked(t *testing.T) {
	root, renders, refreshes := subagentCacheRepo(t)
	renderSubagentBlock = func(context.Context, string) (string, error) {
		*renders++
		if *refreshes != 1 {
			t.Errorf("foreground render began before the background refresh (refreshes=%d)", *refreshes)
		}
		return "block\n", nil
	}
	runSubagentStartHook(t, root)

	dir, _ := subagentCacheDir(root)
	if err := os.Remove(filepath.Join(dir, subagentCacheFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, subagentRefreshLock), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	*refreshes = 0
	renderSubagentBlock = func(context.Context, string) (string, error) { return "block\n", nil }
	runSubagentStartHook(t, root)
	if *refreshes != 0 {
		t.Fatalf("launched a refresh while a fresh lock exists (refreshes=%d)", *refreshes)
	}
}

func TestRefreshSubagentCacheHonoursLock(t *testing.T) {
	root, renders, _ := subagentCacheRepo(t)
	dir, _ := subagentCacheDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, subagentRefreshLock)
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	refreshSubagentCache(root)
	if *renders != 0 {
		t.Fatal("refresh ran while another held the lock")
	}
	stale := time.Now().Add(-2 * subagentRefreshBound)
	if err := os.Chtimes(lock, stale, stale); err != nil {
		t.Fatal(err)
	}
	refreshSubagentCache(root)
	if *renders != 1 {
		t.Fatalf("stale lock not taken over: renders=%d", *renders)
	}
	if _, ok := readSubagentCache(root, subagentCacheKey(root)); !ok {
		t.Fatal("refresh did not store the block")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Fatalf("lock left behind: %v", err)
	}
}
