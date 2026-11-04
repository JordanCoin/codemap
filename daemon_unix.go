//go:build !windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setSysProcAttr sets Unix-specific process attributes for daemon detachment
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// inheritReadinessPipe passes w to the child and returns its descriptor there.
func inheritReadinessPipe(cmd *exec.Cmd, w *os.File) (string, error) {
	cmd.ExtraFiles = append(cmd.ExtraFiles, w)
	return strconv.Itoa(2 + len(cmd.ExtraFiles)), nil
}

// adoptReadinessPipe keeps the inherited descriptor out of the daemon's own
// children, so a scanner subprocess cannot hold the pipe open past an exit.
func adoptReadinessPipe(fd uintptr) *os.File {
	syscall.CloseOnExec(int(fd))
	return os.NewFile(fd, "codemap-watch-readiness")
}
