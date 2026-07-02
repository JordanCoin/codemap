//go:build windows

package watch

import (
	"errors"
	"os"
	"syscall"
)

func processCommandLine(pid int) (string, error) {
	_ = pid
	return "", errors.New("process command line lookup not supported on windows")
}

// processAlive reports whether a process with the given PID is currently
// running. os.Process.Signal(0) is unsupported on Windows — Signal returns an
// error for any signal other than Kill — so IsRunning cannot use it there.
// Instead we open the process and check its exit code: a live process reports
// STILL_ACTIVE.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// terminateProcess stops the daemon. Windows has no SIGTERM, so Kill (which
// maps to TerminateProcess) is the correct mechanism.
func terminateProcess(proc *os.Process) error {
	return proc.Kill()
}
