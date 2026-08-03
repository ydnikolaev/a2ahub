//go:build windows

package space

import (
	"os"
	"syscall"
)

func validWorkLeaseDirectory(info os.FileInfo) bool {
	return !workLeaseWindowsReparsePoint(info) && info.Mode()&os.ModeSymlink == 0 && info.IsDir()
}

func validWorkLeaseRegularFile(info os.FileInfo) bool {
	return !workLeaseWindowsReparsePoint(info) && info.Mode()&os.ModeSymlink == 0 && info.Mode().IsRegular()
}

func workLeaseWindowsReparsePoint(info os.FileInfo) bool {
	attributes, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return !ok || attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
