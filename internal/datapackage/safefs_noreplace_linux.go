//go:build linux

package datapackage

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const atomicNoReplaceSupported = true

// renameNoReplace installs from as to, or fails leaving both exactly as they
// were. RENAME_NOREPLACE is what makes "or" true: the kernel refuses rather
// than clobbering an existing destination, so a fetch racing another writer
// can never half-replace a directory a reader is already using.
//
// Mirrors internal/space/contract_materialize_noreplace_linux.go. The two are
// deliberately separate files rather than a shared helper: this is a syscall
// wrapper, not a policy, and the packages that need it must not acquire a
// dependency on each other to get it.
func renameNoReplace(parent *os.Root, from, to string) (bool, error) {
	directory, err := parent.Open(".")
	if err != nil {
		return false, err
	}
	err = unix.Renameat2(int(directory.Fd()), from, int(directory.Fd()), to, unix.RENAME_NOREPLACE)
	installed := err == nil
	return installed, errors.Join(err, directory.Close())
}
