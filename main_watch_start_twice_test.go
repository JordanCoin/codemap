package main

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codemap/internal/projectpath"
)

// Four daemons were once observed for one checkout (#185). A second
// `watch start` must find the live daemon through its PID file and ownership
// check, and spawn nothing.
func TestRunWatchStartTwiceSpawnsOneDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh to stand in for the daemon process")
	}
	root := t.TempDir()
	projectpath.ResetSetupRoot()
	t.Cleanup(projectpath.ResetSetupRoot)
	absRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	spawns := 0
	var child *exec.Cmd
	withMainRuntimeStubs(
		t,
		nil,
		nil,
		func(name string, args ...string) *exec.Cmd {
			spawns++
			// A stand-in daemon: publishes readiness, then stays alive with
			// "watch daemon <root>" at the end of its command line, which is
			// what the ownership check reads from ps.
			child = exec.Command("sh", "-c", `printf '{}' > "$CODEMAP_WATCH_READINESS_FILE"; sleep 30`, "watch", "daemon", absRoot)
			return child
		},
		func() (string, error) { return "/tmp/codemap-test", nil },
		nil,
		nil,
		nil,
	)
	t.Cleanup(func() {
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}
	})

	first, stderr := captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil {
		t.Fatalf("first watch start failed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(first, "Watch daemon started") {
		t.Fatalf("first start output = %q, want a started line", first)
	}

	second, stderr := captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil {
		t.Fatalf("second watch start failed: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(second, "Watch daemon already running") {
		t.Fatalf("second start output = %q, want already running", second)
	}
	if spawns != 1 {
		t.Fatalf("daemon spawned %d times, want 1", spawns)
	}
}
