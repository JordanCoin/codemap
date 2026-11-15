package runtimefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrUnsafe marks a state path that is a symlink or of the wrong type.
var ErrUnsafe = errors.New("unsafe codemap state")

// Unsafe is the one error every refused state path gets: it names the path
// and says what to do. codemap never deletes it itself.
func Unsafe(path string) error {
	return fmt.Errorf("%w: %s is a symlink or not a plain file or directory; remove it and rerun", ErrUnsafe, path)
}

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
		return Unsafe(path)
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

// EnsureDir makes path a private directory, creating it when missing, and
// refuses a symlink or anything else that is not a real directory, so a
// listing or cleanup inside it never reaches outside the state dir.
func EnsureDir(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	return CheckDir(path)
}

// CheckDir is EnsureDir without creating: a missing path is os.ErrNotExist.
func CheckDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return Unsafe(path)
	}
	return nil
}

// Read rejects symlink and non-regular endpoints.
func Read(path string) ([]byte, error) {
	f, err := OpenRead(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// OpenRead opens a regular file for reading and refuses a symlink, even one
// swapped in between the check and the open: the opened file must be the one
// Lstat saw.
func OpenRead(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, Unsafe(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if after, err := f.Stat(); err != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, Unsafe(path)
	}
	return f, nil
}
