//go:build windows

package watch

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// processCommandLine returns the full command line of the given PID via CIM
// (the Windows analog of `ps`; wmic is deprecated/removed on newer Windows).
// IsOwnedDaemon uses this to confirm a PID belongs to this repo's watch daemon.
func processCommandLine(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid pid %d", pid)
	}
	psCmd := fmt.Sprintf(`(Get-CimInstance Win32_Process -Filter "ProcessId=%d").CommandLine`, pid)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", psCmd).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
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

// terminateDaemon stops the daemon on Windows. Windows has no SIGTERM, so it
// terminates with Kill — but only after confirming proc is actually this repo's
// watch daemon. Ownership is checked against proc.Pid (not by re-reading
// watch.pid) so we validate the exact process we are about to kill. A stale
// watch.pid may point to a PID the OS reused for an unrelated process; killing
// that would be destructive, so we refuse when ownership is foreign or unknown.
func terminateDaemon(root string, proc *os.Process) error {
	switch daemonOwnershipForPID(root, proc.Pid) {
	case ownershipOwned:
		return proc.Kill()
	case ownershipForeign:
		return ErrForeignDaemonPID
	default:
		return ErrDaemonOwnershipUnknown
	}
}
