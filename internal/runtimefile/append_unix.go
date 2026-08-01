//go:build !windows

package runtimefile

import (
	"fmt"
	"os"
	"syscall"
)

// OpenAppend opens without following a final symlink.
func OpenAppend(path string, mode os.FileMode) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CREAT|syscall.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("unsafe runtime file %q", path)
	}
	return file, nil
}
