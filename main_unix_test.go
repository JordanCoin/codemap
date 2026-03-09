//go:build !windows

package main

import (
	"os/exec"
	"testing"
)

func TestSetSysProcAttr(t *testing.T) {
	cmd := exec.Command("echo", "ok")
	setSysProcAttr(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("expected setSysProcAttr to enable Setpgid")
	}
}
