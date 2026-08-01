//go:build windows

package runtimefile

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	moveFileWriteThrough    = 0x8
	replaceFileWriteThrough = 0x2
)

var (
	kernel32DLL  = syscall.NewLazyDLL("kernel32.dll")
	moveFileExW  = kernel32DLL.NewProc("MoveFileExW")
	replaceFileW = kernel32DLL.NewProc("ReplaceFileW")
)

func replaceFile(source, destination string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		result, _, callErr := replaceFileW.Call(
			uintptr(unsafe.Pointer(destinationPtr)),
			uintptr(unsafe.Pointer(sourcePtr)),
			0,
			replaceFileWriteThrough,
			0,
			0,
		)
		if result == 0 {
			return fmt.Errorf("replace runtime file: %w", callErr)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	result, _, callErr := moveFileExW.Call(
		uintptr(unsafe.Pointer(sourcePtr)),
		uintptr(unsafe.Pointer(destinationPtr)),
		moveFileWriteThrough,
	)
	if result == 0 {
		return fmt.Errorf("replace runtime file: %w", callErr)
	}
	return nil
}
