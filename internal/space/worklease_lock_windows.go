//go:build windows

package space

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const workLeaseLockAllBytes = ^uint32(0)

func tryLockWorkLeaseFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		workLeaseLockAllBytes,
		workLeaseLockAllBytes,
		overlapped,
	)
}

func unlockWorkLeaseFile(file *os.File) error {
	overlapped := new(windows.Overlapped)
	return windows.UnlockFileEx(
		windows.Handle(file.Fd()),
		0,
		workLeaseLockAllBytes,
		workLeaseLockAllBytes,
		overlapped,
	)
}

func isWorkLeaseLockContended(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
