//go:build !darwin && !linux

package datapackage

import "os"

const atomicNoReplaceSupported = false

// renameNoReplace refuses everywhere the kernel offers no no-replace rename.
// This is an accepted platform constraint inherited from the contract
// materialize sibling, not a gap to paper over: a non-atomic fallback would
// silently trade the guarantee the caller is relying on for the appearance of
// portability, and a fetch that half-replaced a directory would be worse than
// one that refused to run at all.
func renameNoReplace(*os.Root, string, string) (bool, error) {
	return false, ErrAtomicNoReplaceUnsupported
}
