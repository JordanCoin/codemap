package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codemap/internal/projectpath"
)

// A readiness file caught mid-write reads as zero or partial bytes. Treating
// that as fatal reported a startup failure for a daemon that was starting
// fine, and made TestRunWatchStartWaitsForChildReadinessFailure fail whenever
// the machine was loaded enough to land in the window.
func TestWaitWatchReadinessWaitsThroughPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{"error":"claim rejected"}`), 0o644)
	}()

	err := waitWatchReadiness(path, 5*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "claim rejected") {
		t.Fatalf("waitWatchReadiness() = %v, want the daemon's own error once the file is complete", err)
	}
}

func TestWaitWatchReadinessSucceedsAfterPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, []byte(`{"err`), 0o644); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{}`), 0o644)
	}()

	if err := waitWatchReadiness(path, 5*time.Second, nil); err != nil {
		t.Fatalf("waitWatchReadiness() = %v, want success once the file is complete", err)
	}
}

// A file that never becomes valid still has to say why, rather than reporting
// only that it timed out.
func TestWaitWatchReadinessReportsPersistentGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := waitWatchReadiness(path, 200*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "reading daemon readiness") {
		t.Fatalf("waitWatchReadiness() = %v, want the parse failure surfaced", err)
	}
}

// A missing file must still time out rather than hang.
func TestWaitWatchReadinessTimesOutWhenAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-written.json")
	err := waitWatchReadiness(path, 100*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitWatchReadiness() = %v, want a timeout", err)
	}
}

// stubStandInDaemon makes `watch start` spawn newChild(canonical root) and
// returns the root plus the spawned command once start has run.
func stubStandInDaemon(t *testing.T, newChild func(absRoot string) *exec.Cmd) (string, func() *exec.Cmd) {
	t.Helper()
	root := t.TempDir()
	projectpath.ResetSetupRoot()
	t.Cleanup(projectpath.ResetSetupRoot)
	absRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	var child *exec.Cmd
	withMainRuntimeStubs(t, nil, nil, func(string, ...string) *exec.Cmd {
		child = newChild(absRoot)
		return child
	}, func() (string, error) { return "/tmp/codemap-test", nil }, nil, nil, nil)
	t.Cleanup(func() {
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}
	})
	previous := watchReadinessTimeout
	t.Cleanup(func() { watchReadinessTimeout = previous })
	return root, func() *exec.Cmd { return child }
}

// #270: a daemon still building past the readiness deadline is alive, so
// `watch start` keeps it and a second start finds it running. Its late
// readiness publish must not leave a file in $TMPDIR.
func TestRunWatchStartKeepsDaemonStillBuilding(t *testing.T) {
	const publishAfter = 400 * time.Millisecond
	// The test binary itself is the stand-in: one process whose argv ends in
	// "watch daemon <root>", so killing it leaves nothing behind.
	root, child := stubStandInDaemon(t, func(absRoot string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestMainWatchHelperProcess", "--", "watch", "daemon", absRoot)
		cmd.Env = append(os.Environ(), "CODEMAP_MAIN_WATCH_HELPER=1", "CODEMAP_MAIN_WATCH_HELPER_PUBLISH_AFTER="+publishAfter.String())
		return cmd
	})
	watchReadinessTimeout = 100 * time.Millisecond

	var err error
	out, stderr := captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil {
		t.Fatalf("watch start = %v, want success for a daemon still building\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(out, "still building") {
		t.Fatalf("watch start output = %q, want a still-building line", out)
	}
	out, _ = captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil || !strings.Contains(out, "already running") {
		t.Fatalf("second watch start = %v %q, want the kept daemon already running", err, out)
	}

	var readyPath string
	for _, kv := range child().Env {
		if v, ok := strings.CutPrefix(kv, watchReadinessEnv+"="); ok {
			readyPath = v
		}
	}
	if readyPath == "" {
		t.Fatal("daemon was spawned without a readiness path")
	}
	time.Sleep(publishAfter + 300*time.Millisecond)
	if _, statErr := os.Stat(filepath.Dir(readyPath)); !os.IsNotExist(statErr) {
		t.Fatalf("slow start left its readiness directory behind: %v", statErr)
	}
}

// A daemon that dies without publishing readiness is a failed start, reported
// when it exits rather than at the deadline.
func TestRunWatchStartReportsDaemonThatDies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh to stand in for the daemon process")
	}
	root, _ := stubStandInDaemon(t, func(absRoot string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 3", "watch", "daemon", absRoot)
	})
	watchReadinessTimeout = time.Minute

	started := time.Now()
	var err error
	captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err == nil || !strings.Contains(err.Error(), "exited before reporting readiness") {
		t.Fatalf("watch start = %v, want a dead-daemon failure", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("dead daemon reported after %s, want before the deadline", elapsed)
	}
	if _, statErr := os.Stat(filepath.Join(projectpath.ProjectRuntimeDir(root), "watch.pid")); !os.IsNotExist(statErr) {
		t.Fatalf("dead daemon left its PID file behind: %v", statErr)
	}
}
