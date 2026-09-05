package runtimefile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteAtomic replaces a regular runtime file without following its endpoint.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	return WriteAtomicWith(path, mode, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}

// WriteAtomicWith replaces a regular runtime file with streamed content.
func WriteAtomicWith(path string, mode os.FileMode, write func(io.Writer) error) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe runtime file %q", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".codemap-write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpPath, path)
}

// Read rejects symlink and non-regular endpoints.
func Read(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe runtime file %q", path)
	}
	return os.ReadFile(path)
}
