package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codemap/internal/projectpath"
)

// stubReadinessPipe hands runDaemon the write end of a pipe, as `watch start`
// does, and returns the end start reads.
func stubReadinessPipe(t *testing.T) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := openWatchReadiness
	openWatchReadiness = func() *os.File { return w }
	t.Cleanup(func() {
		openWatchReadiness = previous
		_ = r.Close()
		_ = w.Close()
	})
	return r
}

// A readiness value split across writes is still one value, not a failure.
func TestWaitWatchReadinessWaitsThroughPartialWrite(t *testing.T) {
	r, w := io.Pipe()
	go func() {
		_, _ = w.Write([]byte(`{"error":"claim`))
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(` rejected"}`))
		_ = w.Close()
	}()

	err := waitWatchReadiness(r, 5*time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "claim rejected") {
		t.Fatalf("waitWatchReadiness() = %v, want the daemon's own error once the value is complete", err)
	}
}

func TestWaitWatchReadinessReportsGarbage(t *testing.T) {
	err := waitWatchReadiness(strings.NewReader("not json at all"), time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "reading daemon readiness") {
		t.Fatalf("waitWatchReadiness() = %v, want the parse failure surfaced", err)
	}
}

// A pipe closed without a value is a daemon that died before reporting.
func TestWaitWatchReadinessReportsSilentClose(t *testing.T) {
	err := waitWatchReadiness(strings.NewReader(""), time.Second, nil)
	if err == nil || !strings.Contains(err.Error(), "exited before reporting readiness") {
		t.Fatalf("waitWatchReadiness() = %v, want a dead-daemon failure", err)
	}
}

// Nothing written must still time out rather than hang.
func TestWaitWatchReadinessTimesOutWhenSilent(t *testing.T) {
	r, w := io.Pipe()
	t.Cleanup(func() { _ = w.Close() })
	err := waitWatchReadiness(r, 100*time.Millisecond, nil)
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

// helperDaemon is the test binary as a stand-in daemon: one process whose
// argv ends in "watch daemon <root>", so killing it leaves nothing behind.
func helperDaemon(publishAfter time.Duration) func(string) *exec.Cmd {
	return func(absRoot string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestMainWatchHelperProcess", "--", "watch", "daemon", absRoot)
		cmd.Env = append(os.Environ(), "CODEMAP_MAIN_WATCH_HELPER=1", "CODEMAP_MAIN_WATCH_HELPER_PUBLISH_AFTER="+publishAfter.String())
		return cmd
	}
}

// #270: a daemon still building past the readiness deadline is alive, so
// `watch start` keeps it. Its late publish hits a pipe nobody reads, and it
// must survive that.
func TestRunWatchStartKeepsDaemonStillBuilding(t *testing.T) {
	const publishAfter = 400 * time.Millisecond
	root, _ := stubStandInDaemon(t, helperDaemon(publishAfter))
	watchReadinessTimeout = 100 * time.Millisecond

	var err error
	out, stderr := captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil {
		t.Fatalf("watch start = %v, want success for a daemon still building\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(out, "still building") {
		t.Fatalf("watch start output = %q, want a still-building line", out)
	}
	time.Sleep(publishAfter + 300*time.Millisecond)
	out, _ = captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil || !strings.Contains(out, "already running") {
		t.Fatalf("second watch start = %v %q, want the kept daemon alive after its late publish", err, out)
	}
}

// Readiness travels over an inherited descriptor; no path the daemon writes
// to may appear in its argv or env, where another local user could read it
// and plant a symlink.
func TestRunWatchStartPassesNoReadinessPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("readiness is a handle, not a descriptor, on Windows")
	}
	root, child := stubStandInDaemon(t, helperDaemon(0))
	var err error
	captureMainStreams(t, func() { err = runWatchSubcommand("start", root) })
	if err != nil {
		t.Fatalf("watch start = %v", err)
	}
	cmd := child()
	if len(cmd.ExtraFiles) != 1 {
		t.Fatalf("daemon got %d extra files, want the readiness pipe", len(cmd.ExtraFiles))
	}
	for _, value := range append(append([]string{}, cmd.Args...), cmd.Env...) {
		if strings.Contains(value, "READINESS") && value != watchReadinessEnv+"=3" {
			t.Fatalf("daemon readiness setting %q, want only %s=3", value, watchReadinessEnv)
		}
		if strings.Contains(value, "codemap-watch-ready") {
			t.Fatalf("daemon argv/env carries a readiness path: %q", value)
		}
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
