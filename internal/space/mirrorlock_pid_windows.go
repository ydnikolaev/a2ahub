//go:build windows

package space

// mirrorLockHolderState has no portable answer on windows without opening the
// process handle, so it reports unknown and the lock keeps the age-only rule.
func mirrorLockHolderState(int) mirrorLockHolder {
	return mirrorLockHolderUnknown
}
