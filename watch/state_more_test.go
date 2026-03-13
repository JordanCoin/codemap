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
			t.Skip("process inspection is blocked in this sandbox")
		}
		t.Fatalf("processCommandLine error: %v", err)
	}
	if cmdline == "" {
		t.Fatal("expected current process command line to be non-empty")
	}
}

func TestIsOwnedDaemonMatchesCommandLine(t *testing.T) {
	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(root, "watch-daemon.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(script, "watch", "daemon", root)
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
		if cmdline, err := processCommandLine(cmd.Process.Pid); err != nil && strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("process inspection is blocked in this sandbox")
		} else {
			_ = cmdline
		}
		if time.Now().After(deadline) {
			t.Fatal("expected process command line to match owned daemon")
		}
		time.Sleep(25 * time.Millisecond)
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

func TestIsOwnedDaemonRejectsInvalidPIDFile(t *testing.T) {
	tests := []struct {
		name     string
		pidValue string
	}{
		{name: "missing pid file", pidValue: ""},
		{name: "nonnumeric pid", pidValue: "abc"},
		{name: "zero pid", pidValue: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			codemapDir := filepath.Join(root, ".codemap")
			if err := os.MkdirAll(codemapDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.pidValue != "" {
				if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte(tt.pidValue), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if IsOwnedDaemon(root) {
				t.Fatal("expected invalid pid state to not be treated as owned daemon")
			}
		})
	}
}

func TestStopWithoutPIDReturnsError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".codemap"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Stop(root); err == nil {
		t.Fatal("expected Stop to fail when no pid file exists")
	}
}

func TestIsOwnedDaemonWithMissingProcessReturnsFalse(t *testing.T) {
	root := t.TempDir()
	codemapDir := filepath.Join(root, ".codemap")
	if err := os.MkdirAll(codemapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codemapDir, "watch.pid"), []byte("999999"), 0o644); err != nil {
		t.Fatal(err)
	}

	if IsOwnedDaemon(root) {
		t.Fatal("expected false for non-existent process pid")
	}
}
