//go:build windows

package fsutil

import (
	"os"
	"syscall"
	"unsafe"
)

func moveFileNoReplace(oldPath, newPath string) error {
	oldPtr, err := syscall.UTF16PtrFromString(oldPath)
	if err != nil {
		return err
	}
	newPtr, err := syscall.UTF16PtrFromString(newPath)
	if err != nil {
		return err
	}
	r1, _, callErr := moveFileExW.Call(
		uintptr(unsafe.Pointer(oldPtr)),
		uintptr(unsafe.Pointer(newPtr)),
		moveFileWriteThrough,
	)
	if r1 != 0 {
		return nil
	}
	if errno, ok := callErr.(syscall.Errno); ok && (errno == 80 || errno == 183) {
		return os.ErrExist
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return syscall.EINVAL
}
