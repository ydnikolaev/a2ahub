package release

import (
	"fmt"
	"os"
)

// Swap atomically replaces targetPath with newBinaryPath (T1 step 4):
// chmod 0755 the new binary, then rename(2) it over targetPath. Both paths
// MUST be explicit — Swap never discovers "the current process's own
// executable"; the caller resolves that (os.Executable +
// filepath.EvalSymlinks) and decides. rename(2) is atomic only on the same
// filesystem, which is why Download writes into targetPath's own
// directory: the running process keeps its old inode across the rename
// (no self-truncation). An unwritable target (system-owned install dir)
// returns a clear error naming targetPath; no privilege-escalation attempt
// is ever made.
func Swap(targetPath, newBinaryPath string) error {
	const op = "Swap"
	if err := os.Chmod(newBinaryPath, 0o755); err != nil {
		return &Error{Op: op, Input: newBinaryPath, Err: fmt.Errorf("%w: %v", ErrSwapFailed, err)}
	}
	if err := os.Rename(newBinaryPath, targetPath); err != nil {
		return &Error{Op: op, Input: targetPath, Err: fmt.Errorf("%w: %v", ErrSwapFailed, err)}
	}
	return nil
}
