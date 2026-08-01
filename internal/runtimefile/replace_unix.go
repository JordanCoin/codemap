//go:build !windows

package runtimefile

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
