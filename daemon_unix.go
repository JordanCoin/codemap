//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr sets Unix-specific process attributes for daemon detachment
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
