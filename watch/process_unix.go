//go:build !windows

package watch

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const transitionLockWait = 2 * time.Second

func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// processAlive reports whether a process with the given PID is currently
// running. FindProcess always succeeds on Unix, so liveness is probed with
// signal 0.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// terminateDaemon requests graceful shutdown of an owned Unix daemon.
// Resume it before SIGTERM so a suspended daemon can run its shutdown handler.
func terminateDaemon(root string, proc *os.Process) error {
	if daemonOwnershipForPID(root, proc.Pid) != ownershipOwned {
		return ErrDaemonOwnershipUnknown
	}
	if err := proc.Signal(syscall.SIGCONT); err != nil {
		return err
	}
	err := proc.Signal(syscall.SIGTERM)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
