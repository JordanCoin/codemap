package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"codemap/internal/projectpath"
	"codemap/internal/runtimefile"
	"codemap/watch"
)

// The subagent orientation block is cached per HEAD commit in the project's
// state directory, so a large repo renders it once per commit, not once per
// subagent spawn.
const (
	subagentCacheVersion = "1" // bump when the block's format changes
	subagentCacheFile    = "subagent-start.json"
	subagentRefreshLock  = "subagent-start.lock"
	subagentRefreshHook  = "subagent-refresh" // internal: the background render
	subagentRefreshBound = 60 * time.Second
	// subagentRenderDelayEnv delays the background render; only tests set it,
	// so the slow path is exercised without racing the machine's speed.
	subagentRenderDelayEnv = "CODEMAP_TEST_SUBAGENT_RENDER_DELAY"
)

type subagentCache struct {
	Key   string `json:"key"`
	Block string `json:"block"`
}

func subagentCacheDir(root string) (string, bool) {
	dir, err := projectpath.CheckedRuntimeCodemapDir(root)
	return dir, err == nil
}

// readSubagentCache returns the cached block for key; a missing, corrupt or
// stale cache is a miss. It creates nothing.
func readSubagentCache(root, key string) (string, bool) {
	dir, err := projectpath.StateDir(root)
	if err != nil || key == "" {
		return "", false
	}
	data, err := runtimefile.Read(filepath.Join(dir, subagentCacheFile))
	if err != nil {
		return "", false
	}
	var cache subagentCache
	if json.Unmarshal(data, &cache) != nil || cache.Key != key || cache.Block == "" {
		return "", false
	}
	return cache.Block, true
}

// writeSubagentCache stores block for key, best effort.
func writeSubagentCache(root, key, block string) {
	dir, ok := subagentCacheDir(root)
	if !ok || key == "" || block == "" {
		return
	}
	data, err := json.Marshal(subagentCache{Key: key, Block: block})
	if err != nil {
		return
	}
	_ = runtimefile.WriteAtomic(filepath.Join(dir, subagentCacheFile), data, 0o600)
}

// subagentCacheKey is HEAD plus the format version; a repo with no commit yet
// shares one "unborn" key until its first commit.
func subagentCacheKey(root string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "HEAD")
	cmd.Dir = root
	out, _ := cmd.Output()
	head := strings.TrimSpace(string(out))
	if head == "" {
		head = "unborn"
	}
	return head + "/v" + subagentCacheVersion
}

// subagentStartBlock returns the cached block, or "" when none appears within
// budget. Nobody renders in the foreground: on a miss the one process that
// wins the lock launches the single detached render, and every caller, the
// winner included, waits for the cache file. So N subagents starting on a
// new HEAD cost one render, and a hook killed by its timeout still leaves it
// running.
func subagentStartBlock(root string, budget time.Duration) string {
	key := subagentCacheKey(root)
	if block, ok := readSubagentCache(root, key); ok {
		return block
	}
	dir, ok := subagentCacheDir(root)
	if !ok {
		// The cache is an optimisation: with nowhere to keep it, render here
		// under the same budget and store nothing.
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		defer cancel()
		block, _ := renderSubagentBlock(ctx, root)
		return block
	}
	if release, won := acquireRefreshLock(filepath.Join(dir, subagentRefreshLock)); won && !startSubagentRefresh(root) {
		release()
	}
	for deadline := time.Now().Add(budget); ; time.Sleep(25 * time.Millisecond) {
		if block, ok := readSubagentCache(root, key); ok {
			return block
		}
		if time.Now().After(deadline) {
			return ""
		}
	}
}

// startSubagentRefresh launches the detached render, which owns the lock the
// caller took; tests replace it. It reports whether the launch happened.
var startSubagentRefresh = func(root string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command(exe, append(projectpath.PrependSetupRootArgs("hook", subagentRefreshHook), root)...)
	if cmd.Start() != nil {
		return false
	}
	_ = cmd.Process.Release()
	return true
}

// refreshSubagentCache renders and stores the block for the current HEAD,
// never past subagentRefreshBound, then drops the lock its launcher took.
func refreshSubagentCache(root string) {
	resolved, found, err := ResolveNearestGitRoot(root)
	if err != nil || !found {
		return
	}
	if root, err = ValidateProjectPath(resolved); err != nil {
		return
	}
	dir, ok := subagentCacheDir(root)
	if !ok {
		return
	}
	lock := filepath.Join(dir, subagentRefreshLock)
	defer os.Remove(lock)
	// The pid lets a waiter take the lock over at once if this process dies.
	_ = runtimefile.WriteAtomic(lock, []byte(strconv.Itoa(os.Getpid())), 0o600)
	key := subagentCacheKey(root)
	if _, hit := readSubagentCache(root, key); hit {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), subagentRefreshBound)
	defer cancel()
	if delay, err := time.ParseDuration(os.Getenv(subagentRenderDelayEnv)); err == nil {
		time.Sleep(delay)
	}
	if block, err := renderSubagentBlock(ctx, root); err == nil {
		writeSubagentCache(root, key, block)
	}
}

// acquireRefreshLock creates lock exclusively (O_EXCL never follows a link).
// A lock whose recorded refresher is no longer alive, or that is older than
// the refresh bound (a live but stuck render, or one that never recorded its
// pid), is taken over.
func acquireRefreshLock(lock string) (func(), bool) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, true
		}
		info, statErr := os.Lstat(lock)
		if statErr != nil || !info.Mode().IsRegular() {
			return nil, false
		}
		data, _ := runtimefile.Read(lock)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		dead := pid > 0 && !watch.ProcessAlive(pid)
		if !dead && time.Since(info.ModTime()) < subagentRefreshBound {
			return nil, false
		}
		_ = os.Remove(lock)
	}
	return nil, false
}
