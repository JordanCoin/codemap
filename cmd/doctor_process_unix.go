//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cmd

import (
	"errors"
	"os/exec"
	"syscall"
)

func configureDoctorProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return killDoctorProcess(cmd) }
}

func killDoctorProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
