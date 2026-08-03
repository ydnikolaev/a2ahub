//go:build !darwin && !linux

package space

import "os"

const contractMaterializeNoReplaceSupported = false

func renameContractMaterializeNoReplace(*os.Root, string, string) (bool, error) {
	return false, ErrContractAtomicNoReplaceUnsupported
}
