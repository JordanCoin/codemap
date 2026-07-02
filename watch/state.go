package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReadState reads the daemon state from disk (for hooks to use).
// Returns nil if state doesn't exist or if it's stale and daemon is not running.
func ReadState(root string) *State {
	stateFile := filepath.Join(root, ".codemap", "state.json")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}

	// If state is stale, still allow it when daemon is alive.
	// This avoids expensive fallback scans during idle periods.
	if time.Since(state.UpdatedAt) > 30*time.Second && !IsRunning(root) {
		return nil
	}

	return &state
}

// WritePID writes the daemon PID to .codemap/watch.pid
func WritePID(root string) error {
	pidFile := filepath.Join(root, ".codemap", "watch.pid")
	return os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
}

// ReadPID reads the daemon PID from .codemap/watch.pid
func ReadPID(root string) (int, error) {
	pidFile := filepath.Join(root, ".codemap", "watch.pid")
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscanf(string(data), "%d", &pid)
	return pid, err
}

// RemovePID removes the PID file
func RemovePID(root string) {
	pidFile := filepath.Join(root, ".codemap", "watch.pid")
	os.Remove(pidFile)
}

// IsOwnedDaemon checks whether the PID file points to a codemap watch daemon
// for this repository root.
func IsOwnedDaemon(root string) bool {
	pid, err := ReadPID(root)
	if err != nil || pid <= 0 {
		return false
	}

	cmdline, err := processCommandLine(pid)
	if err != nil {
		return false
	}
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return false
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	if absRoot == "" {
		return false
	}

	return strings.Contains(cmdline, "watch") &&
		strings.Contains(cmdline, "daemon") &&
		strings.Contains(cmdline, absRoot)
}

// IsRunning checks if the daemon is running
func IsRunning(root string) bool {
	pid, err := ReadPID(root)
	if err != nil {
		return false
	}
	// Liveness is checked in a platform-specific way: Signal(0) on Unix is
	// unsupported on Windows, so processAlive queries the OS directly there.
	return processAlive(pid)
}

// Stop sends SIGTERM to the daemon process
func Stop(root string) error {
	pid, err := ReadPID(root)
	if err != nil {
		return fmt.Errorf("no daemon running: %w", err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	// terminateProcess is platform-specific: SIGTERM on Unix, Kill on Windows.
	if err := terminateProcess(proc); err != nil {
		return err
	}
	// Clean up PID file
	RemovePID(root)
	return nil
}
