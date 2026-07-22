//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package cmd

import "os/exec"

func configureDoctorProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return killDoctorProcess(cmd) }
}

func killDoctorProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
