//go:build darwin || linux

package space

import (
	"errors"
	"syscall"
)

// mirrorLockHolderState reports whether pid is a process on THIS host. Signal
// 0 performs the existence and permission checks without delivering anything:
// ESRCH means no such process, EPERM means it exists but belongs to someone
// else (so it is alive). Any other answer is not evidence either way.
func mirrorLockHolderState(pid int) mirrorLockHolder {
	if pid <= 0 {
		return mirrorLockHolderUnknown
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return mirrorLockHolderAlive
	case errors.Is(err, syscall.ESRCH):
		return mirrorLockHolderDead
	default:
		return mirrorLockHolderUnknown
	}
}
