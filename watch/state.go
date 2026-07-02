package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrForeignDaemonPID is returned by Stop when the PID in watch.pid is alive and
// its command line was read but does NOT match this repo's watch daemon — i.e. a
// stale PID the OS reused for an unrelated process. Callers can treat it as
// "nothing of ours to stop" and safely discard the pid file.
var ErrForeignDaemonPID = errors.New("watch.pid points to a live process that is not this repo's codemap watch daemon (stale or reused PID)")

// ErrDaemonOwnershipUnknown is returned by Stop when the PID is alive but its
// ownership could not be determined (the process command line was unavailable,
// e.g. introspection was denied). We refuse to kill it AND keep the pid file, so
// a real daemon is never orphaned or an unrelated process killed.
var ErrDaemonOwnershipUnknown = errors.New("could not verify that watch.pid belongs to this repo's codemap watch daemon; refusing to stop it")

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

// daemonOwnership is the result of checking whether a specific PID is this
// repo's watch daemon. It distinguishes "confirmed not ours" from "could not
// determine" so callers never treat an unverifiable process as safe to discard.
type daemonOwnership int

const (
	ownershipUnknown daemonOwnership = iota // could not read the process command line
	ownershipOwned                          // command line matches this repo's watch daemon
	ownershipForeign                        // command line retrieved and does NOT match
)

// daemonOwnershipForPID classifies whether pid is this repo's watch daemon.
// It takes the PID explicitly (rather than re-reading watch.pid) so callers can
// validate the exact process they are about to act on, avoiding a TOCTOU race
// with a concurrent start/stop rewriting the pid file.
func daemonOwnershipForPID(root string, pid int) daemonOwnership {
	if pid <= 0 {
		return ownershipForeign
	}
	cmdline, err := processCommandLine(pid)
	if err != nil {
		return ownershipUnknown
	}
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return ownershipUnknown
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	if absRoot != "" &&
		strings.Contains(cmdline, "watch") &&
		strings.Contains(cmdline, "daemon") &&
		strings.Contains(cmdline, absRoot) {
		return ownershipOwned
	}
	return ownershipForeign
}

// IsOwnedDaemon reports whether the PID file points to a codemap watch daemon
// for this repository root. It is true only when ownership is positively
// confirmed.
func IsOwnedDaemon(root string) bool {
	pid, err := ReadPID(root)
	if err != nil || pid <= 0 {
		return false
	}
	return daemonOwnershipForPID(root, pid) == ownershipOwned
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
	// terminateDaemon is platform-specific: SIGTERM on Unix; on Windows it
	// verifies the PID belongs to this repo's daemon (guarding against a reused
	// stale PID) before killing, returning ErrForeignDaemonPID otherwise.
	if err := terminateDaemon(root, proc); err != nil {
		if errors.Is(err, ErrForeignDaemonPID) {
			// The recorded PID isn't our daemon (stale or reused). Clear the
			// bogus pid file so status stops reporting it, but never kill a
			// process we can't confirm is ours.
			RemovePID(root)
		}
		return err
	}
	// Clean up PID file
	RemovePID(root)
	return nil
}
