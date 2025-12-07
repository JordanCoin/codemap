//go:build windows

package main

import "os/exec"

// setSysProcAttr is a no-op on Windows (Setpgid not available)
func setSysProcAttr(cmd *exec.Cmd) {
	// Windows doesn't support Setpgid
}
