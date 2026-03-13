//go:build !windows

package watch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func shouldSkipProcessCommandError(err error) bool {
	if err == nil {
		return false
	}

	lowerErr := strings.ToLower(err.Error())
	if strings.Contains(lowerErr, "operation not permitted") || strings.Contains(lowerErr, "permission denied") {
		return true
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		lowerStderr := strings.ToLower(string(exitErr.Stderr))
		if strings.Contains(lowerStderr, "operation not permitted") || strings.Contains(lowerStderr, "permission denied") {
			return true
		}
	}

	return false
}

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
		if shouldSkipProcessCommandError(err) {
			t.Skipf("process command introspection not permitted in this environment: %v", err)
		}
		t.Fatalf("processCommandLine error: %v", err)
	}
	if cmdline == "" {
		t.Fatal("expected current process command line to be non-empty")
	}
}

func TestOwnedDaemonHelperProcess(t *testing.T) {
	if os.Getenv("CODEMAP_WATCH_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func TestIsOwnedDaemonMatchesCommandLine(t *testing.T) {
	if _, err := processCommandLine(os.Getpid()); shouldSkipProcessCommandError(err) {
		t.Skipf("process command introspection not permitted in this environment: %v", err)
	}

	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestOwnedDaemonHelperProcess", "watch", "daemon", root)
	cmd.Env = append(os.Environ(), "CODEMAP_WATCH_HELPER=1")
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

func TestIsOwnedDaemonInvalidPIDInputs(t *testing.T) {
	tests := []struct {
		name       string
		pidContent string
		writePID   bool
	}{
		{name: "missing pid file", writePID: false},
		{name: "non numeric pid", pidContent: "not-a-pid", writePID: true},
		{name: "zero pid", pidContent: "0", writePID: true},
		{name: "negative pid", pidContent: "-42", writePID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			codemapDir := filepath.Join(root, ".codemap")
			if err := os.MkdirAll(codemapDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.writePID {
				if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte(tt.pidContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if IsOwnedDaemon(root) {
				t.Fatalf("IsOwnedDaemon(root=%q, pidContent=%q) = true, want false", root, tt.pidContent)
			}
		})
	}
}

func TestIsRunningInvalidPIDInputs(t *testing.T) {
	tests := []struct {
		name       string
		pidContent string
		writePID   bool
	}{
		{name: "missing pid file", writePID: false},
		{name: "invalid pid format", pidContent: "NaN", writePID: true},
		{name: "nonexistent pid", pidContent: "999999", writePID: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			codemapDir := filepath.Join(root, ".codemap")
			if err := os.MkdirAll(codemapDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.writePID {
				if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte(tt.pidContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if IsRunning(root) {
				t.Fatalf("IsRunning(root=%q, pidContent=%q) = true, want false", root, tt.pidContent)
			}
		})
	}
}

func TestStopWithoutPIDFileReturnsNoDaemonError(t *testing.T) {
	root := t.TempDir()
	if err := Stop(root); err == nil || !strings.Contains(err.Error(), "no daemon running") {
		t.Fatalf("Stop() error = %v, want no daemon running error", err)
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
