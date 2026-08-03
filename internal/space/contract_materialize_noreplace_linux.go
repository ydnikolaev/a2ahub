//go:build linux

package space

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

const contractMaterializeNoReplaceSupported = true

func renameContractMaterializeNoReplace(parent *os.Root, from, to string) (bool, error) {
	directory, err := parent.Open(".")
	if err != nil {
		return false, err
	}
	err = unix.Renameat2(int(directory.Fd()), from, int(directory.Fd()), to, unix.RENAME_NOREPLACE)
	installed := err == nil
	return installed, errors.Join(err, directory.Close())
}
