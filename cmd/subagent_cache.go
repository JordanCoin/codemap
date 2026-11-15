package cmd

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"codemap/internal/projectpath"
	"codemap/internal/runtimefile"
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
)

type subagentCache struct {
	Key   string `json:"key"`
	Block string `json:"block"`
}

// subagentCacheKey is HEAD plus the format version; "" (no caching) when the
// repo has no commit yet.
func subagentCacheKey(root string) string {
	cmd := exec.Command("git", "rev-parse", "--verify", "-q", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	head := strings.TrimSpace(string(out))
	if err != nil || head == "" {
		return ""
	}
	return head + "/v" + subagentCacheVersion
}

func subagentCacheDir(root string) (string, bool) {
	dir, err := projectpath.CheckedRuntimeCodemapDir(root)
	return dir, err == nil
}

// readSubagentCache returns the cached block for key; a missing, corrupt or
// stale cache is a miss.
func readSubagentCache(root, key string) (string, bool) {
	dir, ok := subagentCacheDir(root)
	if !ok || key == "" {
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

// subagentRefreshRunning reports a background render holding a fresh lock.
func subagentRefreshRunning(root string) bool {
	dir, ok := subagentCacheDir(root)
	if !ok {
		return true // nowhere to write: do not launch one
	}
	info, err := os.Stat(filepath.Join(dir, subagentRefreshLock))
	return err == nil && time.Since(info.ModTime()) < subagentRefreshBound
}

// startSubagentRefresh launches the background render, detached, so the next
// subagent gets a block; tests replace it.
var startSubagentRefresh = func(root string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, append(projectpath.PrependSetupRootArgs("hook", subagentRefreshHook), root)...)
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

// refreshSubagentCache renders and stores the block for the current HEAD,
// at most one per project at a time and never past subagentRefreshBound.
func refreshSubagentCache(root string) {
	resolved, found, err := ResolveNearestGitRoot(root)
	if err != nil || !found {
		return
	}
	if root, err = ValidateProjectPath(resolved); err != nil {
		return
	}
	dir, ok := subagentCacheDir(root)
	key := subagentCacheKey(root)
	if !ok || key == "" {
		return
	}
	release, ok := acquireRefreshLock(filepath.Join(dir, subagentRefreshLock))
	if !ok {
		return
	}
	defer release()
	if _, hit := readSubagentCache(root, key); hit {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), subagentRefreshBound)
	defer cancel()
	if block, err := renderSubagentBlock(ctx, root); err == nil {
		writeSubagentCache(root, key, block)
	}
}

// acquireRefreshLock creates lock exclusively. A lock older than the refresh
// bound belongs to a render that died, so it is taken over.
func acquireRefreshLock(lock string) (func(), bool) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, true
		}
		info, statErr := os.Stat(lock)
		if statErr != nil || time.Since(info.ModTime()) < subagentRefreshBound {
			return nil, false
		}
		_ = os.Remove(lock)
	}
	return nil, false
}
