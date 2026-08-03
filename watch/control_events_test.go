package watch

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// startControlEventDaemon boots a daemon over a minimal Go project and returns
// it with the path to its config file.
func startControlEventDaemon(t *testing.T) (*Daemon, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, ".codemap", "config.json")
	if err := os.WriteFile(configPath, []byte(`{"only":["go"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewDaemon(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Stop() })
	return d, configPath
}

// TestControlEventBurstTriggersOneRefresh pins that a burst of control events
// coalesces into a single refresh. Each refresh rebuilds the dependency graph,
// which runs ast-grep over the whole repo, and fsnotify routinely emits several
// events per save — so refreshing per event stalls the event loop and repeats
// the most expensive work the daemon does.
func TestControlEventBurstTriggersOneRefresh(t *testing.T) {
	var refreshes atomic.Int32
	original := daemonRefreshConfiguredFiles
	t.Cleanup(func() { daemonRefreshConfiguredFiles = original })
	daemonRefreshConfiguredFiles = func(d *Daemon, resetIgnoreCache bool) error {
		refreshes.Add(1)
		return original(d, resetIgnoreCache)
	}

	_, configPath := startControlEventDaemon(t)

	// A tight burst, well inside the coalescing window.
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(configPath, []byte(`{"only":["go","md"]}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	waitForWatchCondition(t, 5*time.Second, func() bool { return refreshes.Load() >= 1 })
	// Let any further coalesced refreshes land before counting.
	time.Sleep(500 * time.Millisecond)

	if got := refreshes.Load(); got != 1 {
		t.Fatalf("control event burst caused %d refreshes, want 1", got)
	}
}

// TestControlEventBurstPreservesIgnoreCacheReset guards the coalescing: when a
// burst mixes a .gitignore change with a config change, the single refresh must
// still reset the ignore cache, or gitignore edits are silently dropped.
func TestControlEventBurstPreservesIgnoreCacheReset(t *testing.T) {
	var sawReset atomic.Bool
	var refreshes atomic.Int32
	original := daemonRefreshConfiguredFiles
	t.Cleanup(func() { daemonRefreshConfiguredFiles = original })
	daemonRefreshConfiguredFiles = func(d *Daemon, resetIgnoreCache bool) error {
		refreshes.Add(1)
		if resetIgnoreCache {
			sawReset.Store(true)
		}
		return original(d, resetIgnoreCache)
	}

	d, configPath := startControlEventDaemon(t)

	if err := os.WriteFile(configPath, []byte(`{"only":["go","md"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.root, ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitForWatchCondition(t, 5*time.Second, func() bool { return sawReset.Load() })
}
