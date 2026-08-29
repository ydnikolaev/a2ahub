package datapackage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

// This file is the security-critical half of the loop: a filesystem walker
// that packs a local directory into []LocalFile, and an installer that
// writes a verified set of LocalFile back to a destination directory. Spec
// 05a §5 forbids building either on artifact.DigestTreeFS — it os.Opens
// every non-directory entry and follows symlinks, so it fails the symlink
// case outright — and instructs mirroring the Lstat-before-and-after-read,
// os.Root-capability-based discipline of internal/space's
// contract_candidate.go and contract_materialize.go instead. That mirroring
// is deliberate and literal in a few places: neither package may import the
// other (internal/artifact must not depend on internal/datapackage or
// internal/contract, and internal/datapackage is not internal/space), so the
// same care is re-earned here rather than reused.
//
// Spec: docs/features/archive/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md §5, §6.4
// Plan: docs/features/archive/agent-ops-2026-07/plans/05a-contract-data-exchange-loop.plan.md D-4, D-5

const (
	// Internal filesystem-shape safety caps, distinct from the configured
	// Bounds a caller supplies. These protect the walker and installer's own
	// bookkeeping (directory depth, node count, raw listing bytes) against a
	// filesystem shaped to exhaust memory or recurse forever; they are not
	// part of the manifest's declared bounds and are not meant to be tuned.
	maxDataPackageListingBytes = 1 << 20
	maxDataPackageDepth        = 64
	maxDataPackageDirectories  = 4096
	maxDataPackageDifferences  = 64
)

var dataPackageInstallMu sync.Mutex

// dataPackageHooks makes component/leaf replacement races deterministic in
// package tests — the same mechanism internal/space's
// contractCandidateHooks uses for its own TOCTOU tests. Production callers
// (WalkLocalDirectory, InstallLocalFiles) always pass the zero value.
type dataPackageHooks struct {
	afterDirectoryLstat   func(string)
	afterLeafLstat        func(string)
	afterCompareLeafLstat func(string)
}

// WalkLocalDirectory walks source — a local directory that must already
// exist — into a flat, path-sorted []LocalFile. Every entry is proven
// contained within source and free of symlinks before its bytes are
// returned; bounds.CheckEntryBytes, bounds.CheckEntryCount and
// bounds.CheckPackageBytes are enforced as the walk proceeds, so a directory
// that would exceed any of them is refused before the whole tree is read.
func WalkLocalDirectory(ctx context.Context, source string, bounds Bounds) ([]LocalFile, error) {
	return walkLocalDirectory(ctx, source, bounds, dataPackageHooks{})
}

func walkLocalDirectory(ctx context.Context, source string, bounds Bounds, hooks dataPackageHooks) ([]LocalFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, _, err := openHeldDataPackageRoot(source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // reason: the walk result remains primary

	state := &dataPackageWalkState{bounds: bounds}
	if err := walkDataPackageDirectory(ctx, root, ".", 0, state, hooks); err != nil {
		return nil, err
	}
	sort.Slice(state.files, func(i, j int) bool { return state.files[i].RelPath < state.files[j].RelPath })
	return state.files, nil
}

type dataPackageWalkState struct {
	bounds       Bounds
	files        []LocalFile
	entries      int
	aggregate    int64
	listingBytes int
	nodes        int
	directories  int
}

func walkDataPackageDirectory(ctx context.Context, dir *os.Root, relative string, depth int, state *dataPackageWalkState, hooks dataPackageHooks) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxDataPackageDepth {
		return fmt.Errorf("%w: directory depth exceeds %d", ErrBoundExceeded, maxDataPackageDepth)
	}
	directoryBefore, err := dir.Stat(".")
	if err != nil {
		return fmt.Errorf("datapackage: inspect directory %q: %w", relative, err)
	}
	listing, err := dir.Open(".")
	if err != nil {
		return fmt.Errorf("datapackage: list directory %q: %w", relative, err)
	}
	defer func() { _ = listing.Close() }() // reason: preserve the traversal result

	for {
		entries, readErr := listing.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			state.nodes++
			if state.nodes > maxDataPackageDirectories*4 {
				return fmt.Errorf("%w: too many filesystem nodes", ErrBoundExceeded)
			}
			state.listingBytes += len(entry.Name()) + 1
			if state.listingBytes > maxDataPackageListingBytes {
				return fmt.Errorf("%w: filesystem listing is too large", ErrBoundExceeded)
			}
			entryPath := path.Join(relative, entry.Name())
			if !artifact.CleanRelativePath(entryPath) {
				return fmt.Errorf("%w: %q is not a clean relative path", ErrPathEscapes, entryPath)
			}
			info, statErr := dir.Lstat(entry.Name())
			if statErr != nil {
				return fmt.Errorf("datapackage: inspect entry %q: %w", entryPath, statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %q", ErrSymlink, entryPath)
			}
			if info.IsDir() {
				state.directories++
				if state.directories > maxDataPackageDirectories {
					return fmt.Errorf("%w: too many directories", ErrBoundExceeded)
				}
				child, _, openErr := openDataPackageDirectory(dir, entry.Name(), entryPath, func() {
					if hooks.afterDirectoryLstat != nil {
						hooks.afterDirectoryLstat(entryPath)
					}
				})
				if openErr != nil {
					return openErr
				}
				walkErr := walkDataPackageDirectory(ctx, child, entryPath, depth+1, state, hooks)
				closeErr := child.Close()
				if walkErr != nil {
					return walkErr
				}
				if closeErr != nil {
					return fmt.Errorf("datapackage: close directory %q: %w", entryPath, closeErr)
				}
				continue
			}
			if !info.Mode().IsRegular() {
				// Neither a symlink nor a directory nor a regular file: a
				// socket, device or named pipe. datapackage/errors.go has
				// no sentinel distinct from ErrSymlink for "not a regular
				// filesystem entry" (see the deviation note in this
				// implementation's report) — ErrSymlink is the closest
				// "the walker refuses this filesystem shape" sentinel, and
				// the wrapped message names the real mode so a reader is
				// not misled into thinking a literal symlink was found.
				return fmt.Errorf("%w: %q has mode %s, not a regular file", ErrSymlink, entryPath, info.Mode())
			}
			state.entries++
			if err := state.bounds.CheckEntryCount(state.entries); err != nil {
				return err
			}
			file, fileErr := readDataPackageFile(ctx, dir, entry.Name(), entryPath, state.bounds, hooks.afterLeafLstat)
			if fileErr != nil {
				return fileErr
			}
			state.aggregate += int64(len(file.Bytes))
			if err := state.bounds.CheckPackageBytes(state.aggregate); err != nil {
				return err
			}
			state.files = append(state.files, file)
		}
		if readErr == io.EOF {
			directoryAfter, statErr := dir.Stat(".")
			if statErr != nil || !directoryAfter.IsDir() || !os.SameFile(directoryBefore, directoryAfter) ||
				directoryBefore.Size() != directoryAfter.Size() || !directoryBefore.ModTime().Equal(directoryAfter.ModTime()) {
				return fmt.Errorf("%w: directory %q changed while walking", ErrPathEscapes, relative)
			}
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("datapackage: list directory %q: %w", relative, readErr)
		}
	}
}

// readDataPackageFile is the walker's Lstat-before-and-after-read leaf.
// Every identity check that fails between the first Lstat and the final
// read refuses via ErrSymlink: a symlink swapped in at any point along the
// way is exactly the case §6.4 names, and this is the mechanism, not a
// comment, that catches it.
func readDataPackageFile(ctx context.Context, dir *os.Root, name, relative string, bounds Bounds, afterLstat func(string)) (LocalFile, error) {
	before, err := dir.Lstat(name)
	if err != nil {
		return LocalFile{}, fmt.Errorf("datapackage: inspect %q: %w", relative, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return LocalFile{}, fmt.Errorf("%w: %q", ErrSymlink, relative)
	}
	if !before.Mode().IsRegular() {
		return LocalFile{}, fmt.Errorf("%w: %q has mode %s, not a regular file", ErrSymlink, relative, before.Mode())
	}
	if err := bounds.CheckEntryBytes(before.Size()); err != nil {
		return LocalFile{}, err
	}

	if afterLstat != nil {
		afterLstat(relative)
	}

	file, err := dir.Open(name)
	if err != nil {
		return LocalFile{}, fmt.Errorf("%w: open %q after inspection: %w", ErrSymlink, relative, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close() // reason: preserve the read/identity error
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return LocalFile{}, fmt.Errorf("datapackage: inspect opened %q: %w", relative, err)
	}
	afterOpen, err := dir.Lstat(name)
	if err != nil || !opened.Mode().IsRegular() || afterOpen.Mode()&os.ModeSymlink != 0 ||
		!afterOpen.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, afterOpen) {
		return LocalFile{}, fmt.Errorf("%w: %q changed while opening", ErrSymlink, relative)
	}

	raw, err := readDataPackageFileContext(ctx, file, bounds.MaxEntryBytes)
	if err != nil {
		if errors.Is(err, ErrBoundExceeded) {
			return LocalFile{}, fmt.Errorf("%w: %q exceeds the %d byte per-entry bound", ErrBoundExceeded, relative, bounds.MaxEntryBytes)
		}
		return LocalFile{}, fmt.Errorf("datapackage: read %q: %w", relative, err)
	}
	afterRead, err := file.Stat()
	if err != nil {
		return LocalFile{}, fmt.Errorf("datapackage: re-inspect opened %q: %w", relative, err)
	}
	afterPath, err := dir.Lstat(name)
	if err != nil || !afterRead.Mode().IsRegular() || afterPath.Mode()&os.ModeSymlink != 0 || !afterPath.Mode().IsRegular() ||
		!os.SameFile(opened, afterRead) || !os.SameFile(afterRead, afterPath) ||
		opened.Size() != afterRead.Size() || int64(len(raw)) != afterRead.Size() || !opened.ModTime().Equal(afterRead.ModTime()) {
		return LocalFile{}, fmt.Errorf("%w: %q changed while reading", ErrSymlink, relative)
	}
	if err := file.Close(); err != nil {
		return LocalFile{}, fmt.Errorf("datapackage: close %q: %w", relative, err)
	}
	closed = true
	return LocalFile{RelPath: relative, Bytes: raw}, nil
}

func readDataPackageFileContext(ctx context.Context, reader io.Reader, maximum int64) ([]byte, error) {
	buffer := make([]byte, 32*1024)
	raw := make([]byte, 0, min(maximum, int64(len(buffer))))
	for int64(len(raw)) <= maximum {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := maximum + 1 - int64(len(raw))
		chunk := buffer
		if remaining < int64(len(chunk)) {
			chunk = chunk[:remaining]
		}
		n, err := reader.Read(chunk)
		if n > 0 {
			raw = append(raw, chunk[:n]...)
		}
		if int64(len(raw)) > maximum {
			return nil, ErrBoundExceeded
		}
		if err == io.EOF {
			return raw, nil
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrNoProgress
		}
	}
	return nil, ErrBoundExceeded
}

func openHeldDataPackageRoot(name string) (*os.Root, os.FileInfo, error) {
	before, err := os.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect root: %w", ErrPathEscapes, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%w: root must not be a symlink", ErrSymlink)
	}
	if !before.IsDir() {
		return nil, nil, fmt.Errorf("%w: root must be a real directory", ErrPathEscapes)
	}
	root, err := os.OpenRoot(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open root: %w", ErrPathEscapes, err)
	}
	valid := false
	defer func() {
		if !valid {
			_ = root.Close() // reason: preserve the constructor error while releasing the incomplete capability
		}
	}()
	after, err := os.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: re-inspect root: %w", ErrPathEscapes, err)
	}
	opened, err := root.Stat(".")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect root capability: %w", ErrPathEscapes, err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !opened.IsDir() ||
		!os.SameFile(before, after) || !os.SameFile(before, opened) {
		return nil, nil, fmt.Errorf("%w: root changed while opening", ErrPathEscapes)
	}
	valid = true
	return root, opened, nil
}

// openDataPackageDirectory opens name below parent without ever replacing
// the held directory capability with an unrooted pathname. Identity checks
// on both sides of OpenRoot turn a component swap into a refusal.
func openDataPackageDirectory(parent *os.Root, name, displayPath string, afterLstat func()) (*os.Root, os.FileInfo, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("datapackage: inspect %q: %w", displayPath, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, nil, fmt.Errorf("%w: %q", ErrSymlink, displayPath)
	}
	if !before.IsDir() {
		return nil, nil, fmt.Errorf("%w: %q is not a real directory", ErrPathEscapes, displayPath)
	}
	if afterLstat != nil {
		afterLstat()
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: open %q: %w", ErrPathEscapes, displayPath, err)
	}
	valid := false
	defer func() {
		if !valid {
			_ = child.Close() // reason: preserve the unsafe-component error
		}
	}()
	opened, err := child.Stat(".")
	if err != nil {
		return nil, nil, fmt.Errorf("%w: inspect opened %q: %w", ErrPathEscapes, displayPath, err)
	}
	after, err := parent.Lstat(name)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: re-inspect %q: %w", ErrPathEscapes, displayPath, err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !opened.IsDir() ||
		!os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return nil, nil, fmt.Errorf("%w: %q changed while opening", ErrPathEscapes, displayPath)
	}
	valid = true
	return child, opened, nil
}

// InstallOutcome is part of the public package API.
type InstallOutcome string

const (
	// Installed is part of the public package API.
	Installed InstallOutcome = "installed"
	// AlreadyPresent is part of the public package API.
	AlreadyPresent InstallOutcome = "already-present"
)

// InstallLocalFiles verifies bounds and containment for files, then installs
// them under destination — a filesystem path whose parent must already
// exist — atomically: either the whole set lands or none of it does. A
// destination that does not yet exist is written via a temporary sibling
// directory, then renamed into place with no-replace semantics (D-4, D-5):
// if the destination already holds byte-identical content, that is accepted
// as AlreadyPresent rather than refused (a re-fetch that finds what it would
// have written is a success, not a collision — the retry this loop depends
// on would be unsafe under the opposite reading); if it holds anything else,
// including a symlink where a file or directory belongs, it is refused with
// ErrDestinationDiverged and left untouched.
func InstallLocalFiles(ctx context.Context, destination string, files []LocalFile, bounds Bounds) (InstallOutcome, error) {
	return installLocalFiles(ctx, destination, files, bounds, dataPackageHooks{})
}

func installLocalFiles(ctx context.Context, destination string, files []LocalFile, bounds Bounds, hooks dataPackageHooks) (InstallOutcome, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := bounds.CheckEntryCount(len(files)); err != nil {
		return "", err
	}
	var aggregate int64
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if !artifact.CleanRelativePath(file.RelPath) {
			return "", fmt.Errorf("%w: %q is not a clean relative path", ErrPathEscapes, file.RelPath)
		}
		if _, exists := seen[file.RelPath]; exists {
			return "", fmt.Errorf("%w: %q is declared more than once", ErrPathEscapes, file.RelPath)
		}
		seen[file.RelPath] = struct{}{}
		if err := bounds.CheckEntryBytes(int64(len(file.Bytes))); err != nil {
			return "", err
		}
		aggregate += int64(len(file.Bytes))
		if err := bounds.CheckPackageBytes(aggregate); err != nil {
			return "", err
		}
	}

	parentDir := filepath.Dir(destination)
	leaf := filepath.Base(destination)
	if leaf == "." || leaf == string(filepath.Separator) || leaf == "" || leaf == ".." {
		return "", fmt.Errorf("%w: %q is not a valid installation destination", ErrPathEscapes, destination)
	}

	root, _, err := openHeldDataPackageRoot(parentDir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // reason: the install result remains primary

	// Serializing the final compare/create decision gives concurrent
	// in-process callers the specified winner/re-read behavior without a
	// lock file that would violate a zero-write identical rerun.
	dataPackageInstallMu.Lock()
	defer dataPackageInstallMu.Unlock()

	info, err := root.Lstat(leaf)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", dataPackageDestinationDivergence([]string{"unsafe:" + destination})
		}
		equal, differences, compareErr := compareInstalledDirectory(ctx, root, leaf, files, hooks)
		if compareErr != nil {
			return "", compareErr
		}
		if !equal {
			return "", dataPackageDestinationDivergence(differences)
		}
		return AlreadyPresent, nil
	case errors.Is(err, os.ErrNotExist):
		return installAbsent(ctx, root, leaf, destination, files, hooks)
	default:
		return "", fmt.Errorf("datapackage: inspect destination: %w", err)
	}
}

func installAbsent(ctx context.Context, parent *os.Root, leaf, destination string, files []LocalFile, hooks dataPackageHooks) (result InstallOutcome, retErr error) {
	temporary, err := newDataPackageTemporaryName()
	if err != nil {
		return "", err
	}
	if err := parent.Mkdir(temporary, 0o700); err != nil {
		return "", fmt.Errorf("datapackage: create install temporary sibling: %w", err)
	}
	temporaryPresent := true
	var temporaryIdentity os.FileInfo
	defer func() {
		if !temporaryPresent {
			return
		}
		if temporaryIdentity == nil {
			if cleanupErr := parent.Remove(temporary); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("datapackage: clean unverified install temporary sibling: %w", cleanupErr))
			}
			return
		}
		current, inspectErr := parent.Lstat(temporary)
		if errors.Is(inspectErr, os.ErrNotExist) {
			return
		}
		if inspectErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(temporaryIdentity, current) {
			retErr = errors.Join(retErr, fmt.Errorf("%w: temporary sibling changed before cleanup", ErrPathEscapes))
			return
		}
		if cleanupErr := parent.RemoveAll(temporary); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("datapackage: clean install temporary sibling: %w", cleanupErr))
		}
	}()
	temporaryIdentity, err = parent.Lstat(temporary)
	if err != nil || !temporaryIdentity.IsDir() || temporaryIdentity.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: inspect install temporary sibling", ErrPathEscapes)
	}

	temporaryRoot, _, err := openDataPackageDirectory(parent, temporary, temporary, nil)
	if err != nil {
		return "", err
	}
	if err := writeInstalledTree(ctx, temporaryRoot, files); err != nil {
		_ = temporaryRoot.Close() // reason: the write/refusal error remains primary and cleanup still runs
		return "", err
	}
	if err := temporaryRoot.Close(); err != nil {
		return "", fmt.Errorf("datapackage: close completed install tree: %w", err)
	}

	if winner, err := parent.Lstat(leaf); err == nil {
		if winner.Mode()&os.ModeSymlink != 0 || !winner.IsDir() {
			return "", dataPackageDestinationDivergence([]string{"unsafe:" + destination})
		}
		equal, differences, compareErr := compareInstalledDirectory(ctx, parent, leaf, files, hooks)
		if compareErr != nil {
			return "", compareErr
		}
		if !equal {
			return "", dataPackageDestinationDivergence(differences)
		}
		return AlreadyPresent, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("datapackage: re-inspect destination before rename: %w", err)
	}

	installed, installErr := renameNoReplace(parent, temporary, leaf)
	if !installed {
		if winner, winnerErr := parent.Lstat(leaf); winnerErr == nil {
			if winner.Mode()&os.ModeSymlink != 0 || !winner.IsDir() {
				return "", dataPackageDestinationDivergence([]string{"unsafe:" + destination})
			}
			equal, differences, compareErr := compareInstalledDirectory(ctx, parent, leaf, files, hooks)
			if compareErr != nil {
				return "", compareErr
			}
			if !equal {
				return "", dataPackageDestinationDivergence(differences)
			}
			return AlreadyPresent, nil
		}
		return "", fmt.Errorf("datapackage: atomically install package: %w", installErr)
	}
	temporaryPresent = false
	return Installed, nil
}

func writeInstalledTree(ctx context.Context, root *os.Root, files []LocalFile) error {
	directories := dataPackageDirectories(files)
	roots := map[string]*os.Root{".": root}
	defer func() {
		for _, directory := range reverseDataPackageDirectories(directories) {
			if child := roots[directory]; child != nil {
				_ = child.Close() // reason: the originating write/sync error remains primary
			}
		}
	}()

	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := roots[path.Dir(directory)]
		if err := parent.Mkdir(path.Base(directory), 0o700); err != nil {
			return fmt.Errorf("datapackage: create install directory %s: %w", directory, err)
		}
		child, _, err := openDataPackageDirectory(parent, path.Base(directory), directory, nil)
		if err != nil {
			return err
		}
		roots[directory] = child
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent := roots[path.Dir(file.RelPath)]
		if err := writeInstalledFile(parent, file); err != nil {
			return err
		}
	}
	for _, directory := range append(reverseDataPackageDirectories(directories), ".") {
		handle, err := roots[directory].Open(".")
		if err != nil {
			return fmt.Errorf("datapackage: open install directory %s for fsync: %w", directory, err)
		}
		err = handle.Sync()
		closeErr := handle.Close()
		if err != nil || closeErr != nil {
			return fmt.Errorf("datapackage: fsync install directory %s: %w", directory, errors.Join(err, closeErr))
		}
	}
	return nil
}

func writeInstalledFile(root *os.Root, file LocalFile) error {
	handle, err := root.OpenFile(path.Base(file.RelPath), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("datapackage: create install file %s: %w", file.RelPath, err)
	}
	opened, statErr := handle.Stat()
	writeErr := writeDataPackageBytes(handle, file.Bytes)
	if writeErr == nil {
		writeErr = handle.Sync()
	}
	closeErr := handle.Close()
	after, afterErr := root.Lstat(path.Base(file.RelPath))
	if statErr != nil || writeErr != nil || closeErr != nil {
		return fmt.Errorf("datapackage: write install file %s: %w", file.RelPath, errors.Join(statErr, writeErr, closeErr))
	}
	if afterErr != nil || !opened.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return fmt.Errorf("%w: install file %s changed while writing", ErrSymlink, file.RelPath)
	}
	return nil
}

func writeDataPackageBytes(writer io.Writer, raw []byte) error {
	for len(raw) != 0 {
		written, err := writer.Write(raw)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		raw = raw[written:]
	}
	return nil
}

// compareInstalledDirectory walks an existing destination and reports
// whether it holds exactly the bytes files declares — nothing missing,
// nothing extra, nothing unsafe (a symlink where a file or directory
// belongs), nothing divergent. It is used both when the destination already
// existed before install started, and to re-check after a losing rename
// race against a concurrent installer.
func compareInstalledDirectory(ctx context.Context, parent *os.Root, leaf string, expected []LocalFile, hooks dataPackageHooks) (bool, []string, error) {
	root, _, err := openDataPackageDirectory(parent, leaf, leaf, nil)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = root.Close() }() // reason: compare result remains primary

	wantFiles := make(map[string][]byte, len(expected))
	wantDirectories := map[string]struct{}{".": {}}
	for _, file := range expected {
		wantFiles[file.RelPath] = file.Bytes
		for directory := path.Dir(file.RelPath); directory != "."; directory = path.Dir(directory) {
			wantDirectories[directory] = struct{}{}
		}
	}
	state := &dataPackageCompareState{
		hooks: hooks, wantFiles: wantFiles, wantDirectories: wantDirectories,
		seenFiles: make(map[string]struct{}, len(wantFiles)), seenDirectories: map[string]struct{}{".": {}},
	}
	if err := state.walk(ctx, root, "."); err != nil {
		return false, nil, err
	}
	for name := range wantFiles {
		if _, exists := state.seenFiles[name]; !exists {
			state.addDifference("missing:" + name)
		}
	}
	for name := range wantDirectories {
		if _, exists := state.seenDirectories[name]; !exists {
			state.addDifference("missing-dir:" + name)
		}
	}
	differences := state.finalizedDifferences()
	return len(differences) == 0, differences, nil
}

type dataPackageCompareState struct {
	hooks                dataPackageHooks
	wantFiles            map[string][]byte
	wantDirectories      map[string]struct{}
	seenFiles            map[string]struct{}
	seenDirectories      map[string]struct{}
	differences          []string
	differencesTruncated bool
	nodes                int
}

func (s *dataPackageCompareState) walk(ctx context.Context, root *os.Root, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("datapackage: open destination directory %s: %w", directory, err)
	}
	entries, readErr := handle.ReadDir(-1)
	closeErr := handle.Close()
	if readErr != nil {
		return fmt.Errorf("datapackage: enumerate destination directory %s: %w", directory, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("datapackage: close destination directory %s: %w", directory, closeErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.nodes++
		if s.nodes > maxDataPackageDirectories*4 {
			return fmt.Errorf("%w: destination has too many filesystem nodes", ErrBoundExceeded)
		}
		name := entry.Name()
		relative := name
		if directory != "." {
			relative = path.Join(directory, name)
		}
		before, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("datapackage: inspect destination entry %s: %w", relative, err)
		}
		if before.Mode()&os.ModeSymlink != 0 {
			s.addDifference("unsafe:" + relative)
			continue
		}
		if before.IsDir() {
			s.seenDirectories[relative] = struct{}{}
			if _, expected := s.wantDirectories[relative]; !expected {
				s.addDifference("extra-dir:" + relative)
			}
			child, _, err := openDataPackageDirectory(root, name, relative, nil)
			if err != nil {
				return err
			}
			walkErr := s.walk(ctx, child, relative)
			closeErr := child.Close()
			if walkErr != nil {
				return walkErr
			}
			if closeErr != nil {
				return fmt.Errorf("datapackage: close destination capability %s: %w", relative, closeErr)
			}
			continue
		}
		if !before.Mode().IsRegular() {
			s.addDifference("unsafe:" + relative)
			continue
		}
		if s.hooks.afterCompareLeafLstat != nil {
			s.hooks.afterCompareLeafLstat(relative)
		}
		file, err := root.OpenFile(name, os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("datapackage: open destination entry %s: %w", relative, err)
		}
		opened, statErr := file.Stat()
		raw, readErr := io.ReadAll(file)
		closeErr = file.Close()
		after, afterErr := root.Lstat(name)
		if statErr != nil || readErr != nil || closeErr != nil || afterErr != nil ||
			!opened.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 ||
			!os.SameFile(before, opened) || !os.SameFile(opened, after) {
			return fmt.Errorf("%w: destination entry %s changed while reading", ErrSymlink, relative)
		}
		s.seenFiles[relative] = struct{}{}
		want, exists := s.wantFiles[relative]
		if !exists {
			s.addDifference("extra:" + relative)
		} else if !bytes.Equal(raw, want) {
			s.addDifference("divergent:" + relative)
		}
	}
	return nil
}

func (s *dataPackageCompareState) addDifference(value string) {
	if len(s.differences) < maxDataPackageDifferences {
		s.differences = append(s.differences, value)
		return
	}
	s.differencesTruncated = true
}

func (s *dataPackageCompareState) finalizedDifferences() []string {
	differences := append([]string(nil), s.differences...)
	if s.differencesTruncated {
		marker := "truncated:additional-differences"
		if len(differences) == maxDataPackageDifferences {
			differences[len(differences)-1] = marker
		} else {
			differences = append(differences, marker)
		}
	}
	sort.Strings(differences)
	return differences
}

func dataPackageDirectories(files []LocalFile) []string {
	set := make(map[string]struct{})
	for _, file := range files {
		for directory := path.Dir(file.RelPath); directory != "."; directory = path.Dir(directory) {
			set[directory] = struct{}{}
		}
	}
	directories := make([]string, 0, len(set))
	for directory := range set {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(i, j int) bool {
		leftDepth, rightDepth := strings.Count(directories[i], "/"), strings.Count(directories[j], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return directories[i] < directories[j]
	})
	return directories
}

func reverseDataPackageDirectories(directories []string) []string {
	reversed := append([]string(nil), directories...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func newDataPackageTemporaryName() (string, error) {
	var token [16]byte
	if _, err := io.ReadFull(rand.Reader, token[:]); err != nil {
		return "", fmt.Errorf("datapackage: create install token: %w", err)
	}
	return ".a2a-datapackage-install-" + hex.EncodeToString(token[:]), nil
}

func dataPackageDestinationDivergence(differences []string) error {
	sorted := append([]string(nil), differences...)
	sort.Strings(sorted)
	if len(sorted) == 0 {
		return ErrDestinationDiverged
	}
	return fmt.Errorf("%w: %s", ErrDestinationDiverged, strings.Join(sorted, ", "))
}
