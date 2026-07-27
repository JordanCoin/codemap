//go:build !windows

package watch

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

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

// terminateDaemon stops the daemon on Unix with SIGTERM so it can shut down
// gracefully. This matches long-standing behavior and does not gate on
// ownership (unlike Windows, where the kill is destructive): the root argument
// is accepted only to share a signature with the Windows implementation.
func terminateDaemon(_ string, proc *os.Process) error {
	return proc.Signal(syscall.SIGTERM)
}
