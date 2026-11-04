//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setSysProcAttr is a no-op on Windows (Setpgid not available)
func setSysProcAttr(cmd *exec.Cmd) {
	// Windows doesn't support Setpgid
}

// inheritReadinessPipe passes w's handle to the child (ExtraFiles is
// unsupported on Windows) and returns the handle value.
func inheritReadinessPipe(cmd *exec.Cmd, w *os.File) (string, error) {
	h := syscall.Handle(w.Fd())
	if err := syscall.SetHandleInformation(h, syscall.HANDLE_FLAG_INHERIT, syscall.HANDLE_FLAG_INHERIT); err != nil {
		return "", err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, h)
	return strconv.FormatUint(uint64(h), 10), nil
}

// adoptReadinessPipe wraps the inherited handle. Go starts children with an
// explicit handle list, so the daemon's own subprocesses do not inherit it.
func adoptReadinessPipe(fd uintptr) *os.File {
	return os.NewFile(fd, "codemap-watch-readiness")
}
