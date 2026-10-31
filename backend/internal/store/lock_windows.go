//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockDir opens path with no sharing allowed, so a second process cannot
// open it until this handle is closed. Windows closes it when the process
// exits, so a crash never leaves a stale lock.
func lockDir(path string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, // no sharing
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		if errors.Is(err, errSharingViolation) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock data directory: %w", err)
	}
	return os.NewFile(uintptr(h), path), nil
}

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall does
// not name.
const errSharingViolation syscall.Errno = 32
