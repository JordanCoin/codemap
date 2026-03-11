//go:build !windows

package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReadStateMissingAndInvalid(t *testing.T) {
	root := t.TempDir()
	if got := ReadState(root); got != nil {
		t.Fatal("expected nil for missing state file")
	}

	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codemapDir, "state.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadState(root); got != nil {
		t.Fatal("expected nil for invalid state JSON")
	}
}

func TestPIDRoundTripAndRemovePID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WritePID(root); err != nil {
		t.Fatalf("WritePID error: %v", err)
	}
	pid, err := ReadPID(root)
	if err != nil {
		t.Fatalf("ReadPID error: %v", err)
	}
	if pid != os.Getpid() {
		t.Fatalf("pid = %d, want %d", pid, os.Getpid())
	}

	RemovePID(root)
	if _, err := ReadPID(root); err == nil {
		t.Fatal("expected ReadPID to fail after RemovePID")
	}
}

func TestProcessCommandLineCurrentProcess(t *testing.T) {
	cmdline, err := processCommandLine(os.Getpid())
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("process inspection unavailable in sandbox: %v", err)
		}
		t.Fatalf("processCommandLine error: %v", err)
	}
	if cmdline == "" {
		t.Fatal("expected current process command line to be non-empty")
	}
}

func TestIsOwnedDaemonMatchesCommandLine(t *testing.T) {
	if _, err := processCommandLine(os.Getpid()); err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("process inspection unavailable in sandbox: %v", err)
		}
	}

	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Keep process alive and embed watch/daemon/root in command line text.
	cmdline := "tail -f /dev/null # watch daemon " + root
	cmd := exec.Command("sh", "-c", cmdline)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper daemon: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	pidPath := filepath.Join(codemapDir, "watch.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for !IsOwnedDaemon(root) {
		if time.Now().After(deadline) {
			t.Fatal("expected process command line to match owned daemon")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestReadPIDInvalidContent(t *testing.T) {
	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte("not-a-number"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadPID(root); err == nil {
		t.Fatal("expected parse error for invalid pid file content")
	}
}

func TestIsOwnedDaemonFalseCases(t *testing.T) {
	tests := []struct {
		name       string
		pidContent string
	}{
		{name: "missing pid file", pidContent: ""},
		{name: "invalid pid file", pidContent: "bad"},
		{name: "nonexistent pid", pidContent: "999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			codemapDir := filepath.Join(root, ".codemap")
			if err := os.MkdirAll(codemapDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.pidContent != "" {
				if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte(tt.pidContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if IsOwnedDaemon(root) {
				t.Fatal("expected IsOwnedDaemon to be false")
			}
		})
	}
}

func TestStopNoDaemonRunning(t *testing.T) {
	root := t.TempDir()
	if err := Stop(root); err == nil {
		t.Fatal("expected error when no daemon pid file exists")
	} else if !strings.Contains(err.Error(), "no daemon running") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopTerminatesProcessAndRemovesPID(t *testing.T) {
	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	pidPath := filepath.Join(codemapDir, "watch.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Stop(root); err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("expected pid file to be removed, stat err=%v", err)
	}
}
