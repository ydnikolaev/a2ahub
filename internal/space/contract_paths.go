package space

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrContractUnsafePath means a configured candidate location was not a
	// clean project-root-relative path backed by the same real directory for
	// the lifetime of its held capability.
	// ErrContractUnsafePath is part of the public package API.
	ErrContractUnsafePath = errors.New("space: contract path is not safely contained")

	// ErrContractUnsafeEntry means a candidate or historical carried leaf is
	// a symlink, submodule, device, socket, pipe, or otherwise non-regular.
	// ErrContractUnsafeEntry is part of the public package API.
	ErrContractUnsafeEntry = errors.New("space: contract entry is not a regular file")

	// ErrContractBoundedResult means a file, tree, history, subprocess output,
	// explicit inventory, or aggregate exceeded its protocol ceiling.
	// ErrContractBoundedResult is part of the public package API.
	ErrContractBoundedResult = errors.New("space: contract result exceeds bound")
)

func cleanContractRelativePath(name string) bool {
	if name == "" || name == "." || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || filepath.VolumeName(name) != "" {
		return false
	}
	if path.Clean(name) != name {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func openHeldContractRoot(name string) (*os.Root, os.FileInfo, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect configured root: %w", ErrContractUnsafePath, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, nil, fmt.Errorf("%w: configured root must be a real directory", ErrContractUnsafePath)
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open configured root: %w", ErrContractUnsafePath, err)
	}
	valid := false
	defer func() {
		if !valid {
			_ = root.Close() // reason: preserve the constructor error while releasing the incomplete capability
		}
	}()
	after, err := os.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: re-inspect configured root: %w", ErrContractUnsafePath, err)
	}
	opened, err := root.Stat(".")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect configured root capability: %w", ErrContractUnsafePath, err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !opened.IsDir() ||
		!os.SameFile(before, after) || !os.SameFile(before, opened) {
		return nil, nil, fmt.Errorf("%w: configured root changed while opening", ErrContractUnsafePath)
	}
	valid = true
	return root, opened, nil
}

// openContractDirectory opens name below parent without ever replacing the
// held directory capability with an unrooted pathname. Identity checks on
// both sides of OpenRoot turn a component swap into a refusal.
func openContractDirectory(parent *os.Root, name, displayPath string, afterLstat func()) (*os.Root, os.FileInfo, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect %q: %w", ErrContractUnsafePath, displayPath, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return nil, nil, fmt.Errorf("%w: %q is not a real directory", ErrContractUnsafePath, displayPath)
	}
	if afterLstat != nil {
		afterLstat()
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open %q: %w", ErrContractUnsafePath, displayPath, err)
	}
	valid := false
	defer func() {
		if !valid {
			_ = child.Close() // reason: preserve the unsafe-component error
		}
	}()
	opened, err := child.Stat(".")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect opened %q: %w", ErrContractUnsafePath, displayPath, err)
	}
	after, err := parent.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: re-inspect %q: %w", ErrContractUnsafePath, displayPath, err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !opened.IsDir() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, nil, fmt.Errorf("%w: %q changed while opening", ErrContractUnsafePath, displayPath)
	}
	valid = true
	return child, opened, nil
}
