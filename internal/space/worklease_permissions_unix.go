//go:build darwin || linux

package space

import "os"

func validWorkLeaseDirectory(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink == 0 && info.IsDir() && info.Mode().Perm() == 0o700
}

func validWorkLeaseRegularFile(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular() && info.Mode().Perm() == 0o600
}
