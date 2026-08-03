package space

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
)

const (
	maxContractFilesystemListingBytes = 1 << 20
	maxContractCandidateFiles         = contract.MaxExplicitFiles * 2
	maxContractCandidateBytes         = contract.MaxAggregateBytes*2 + contract.MaxFileBytes
	maxContractCandidateDepth         = 64
	maxContractCandidateNodes         = 1024
	maxContractCandidateDirectories   = 768
)

// ContractCandidateSource identifies where a frozen candidate was observed.
// It is presentation metadata; the fingerprint itself depends only on exact
// relative paths, kinds, modes, and bytes so equal mirror/staging trees agree.
// ContractCandidateSource is part of the public package API.
type ContractCandidateSource string

const (
	// ContractCandidateSourceMirror is part of the public package API.
	ContractCandidateSourceMirror ContractCandidateSource = "mirror"
	// ContractCandidateSourceStaging is part of the public package API.
	ContractCandidateSourceStaging ContractCandidateSource = "staging"
)

// ContractCandidateLocation selects one already-existing directory beneath a
// configured root. Path is always relative to the configured root; callers do
// not get an absolute-path escape hatch.
// ContractCandidateLocation is part of the public package API.
type ContractCandidateLocation struct {
	Path   string
	Source ContractCandidateSource
}

// ContractSnapshotFile is one exact leaf frozen by a filesystem or Git
// collector. Mode uses Git's canonical 100644/100755 spelling for regular
// files. ObjectID is populated for Git objects and empty for filesystem input.
// ContractSnapshotFile is part of the public package API.
type ContractSnapshotFile struct {
	Path     string
	Kind     contract.CandidateKind
	Mode     string
	Source   ContractCandidateSource
	ObjectID string
	Raw      []byte
}

// ContractCandidateSnapshot is detached from every open file/path. Planning
// consumes this exact value and never reopens the candidate source.
// ContractCandidateSnapshot is part of the public package API.
type ContractCandidateSnapshot struct {
	Source      ContractCandidateSource
	Fingerprint string
	Files       []ContractSnapshotFile
}

// CoreSnapshot projects the frozen I/O result into the pure contract core.
// It clones every byte slice so neither side can mutate the other's snapshot.
// CoreSnapshot is part of the public package API.
func (s ContractCandidateSnapshot) CoreSnapshot() contract.CandidateSnapshot {
	var out contract.CandidateSnapshot
	for _, file := range s.Files {
		candidate := contract.CandidateFile{
			Path: file.Path,
			Kind: file.Kind,
			Raw:  append([]byte(nil), file.Raw...),
		}
		if file.Path == contract.DescriptorPath {
			out.Descriptor = candidate
			continue
		}
		out.Files = append(out.Files, candidate)
	}
	return out
}

func (s ContractCandidateSnapshot) file(name string) ContractSnapshotFile {
	for _, file := range s.Files {
		if file.Path == name {
			return file
		}
	}
	return ContractSnapshotFile{}
}

// ContractCandidateReader holds both the configured-root and selected-root
// capabilities. Keeping the anchor lets Read prove the configured pathname
// still names the selected directory before and after collection; keeping the
// child capability prevents a concurrent rename from redirecting any read.
// ContractCandidateReader is part of the public package API.
type ContractCandidateReader struct {
	configured *os.Root
	root       *os.Root
	location   ContractCandidateLocation
	identity   os.FileInfo
	hooks      contractCandidateHooks
}

// contractCandidateHooks makes component/leaf replacement races
// deterministic in package tests. Production constructors leave both nil.
type contractCandidateHooks struct {
	afterDirectoryLstat func(string)
	afterLeafLstat      func(string)
}

// OpenContractCandidateReader resolves every location component through
// os.Root and retains the resulting capability until Close.
// OpenContractCandidateReader is part of the public package API.
func OpenContractCandidateReader(configuredRoot string, location ContractCandidateLocation) (*ContractCandidateReader, error) {
	if !cleanContractRelativePath(location.Path) ||
		(location.Source != ContractCandidateSourceMirror && location.Source != ContractCandidateSourceStaging) {
		return nil, fmt.Errorf("%w: invalid candidate location", ErrContractUnsafePath)
	}
	configured, _, err := openHeldContractRoot(configuredRoot)
	if err != nil {
		return nil, err
	}
	closeConfigured := true
	defer func() {
		if closeConfigured {
			_ = configured.Close() // reason: preserve the location-opening error
		}
	}()

	parent := configured
	var openedIntermediates []*os.Root
	components := strings.Split(location.Path, "/")
	var selected *os.Root
	var identity os.FileInfo
	for index, component := range components {
		display := path.Join(components[:index+1]...)
		child, info, openErr := openContractDirectory(parent, component, display, nil)
		if openErr != nil {
			if parent != configured {
				_ = parent.Close() // reason: preserve the unsafe-component error
			}
			for _, intermediate := range openedIntermediates {
				if intermediate != parent {
					_ = intermediate.Close() // reason: preserve the unsafe-component error
				}
			}
			return nil, openErr
		}
		if parent != configured {
			openedIntermediates = append(openedIntermediates, parent)
		}
		parent, selected, identity = child, child, info
	}
	for _, intermediate := range openedIntermediates {
		if intermediate != selected {
			_ = intermediate.Close() // reason: the final child owns its own held capability
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("%w: empty candidate location", ErrContractUnsafePath)
	}
	closeConfigured = false
	return &ContractCandidateReader{
		configured: configured,
		root:       selected,
		location:   location,
		identity:   identity,
	}, nil
}

// Close releases both held capabilities. It never removes candidate data.
func (r *ContractCandidateReader) Close() error {
	if r == nil {
		return nil
	}
	var rootErr, configuredErr error
	if r.root != nil {
		rootErr = r.root.Close()
		r.root = nil
	}
	if r.configured != nil {
		configuredErr = r.configured.Close()
		r.configured = nil
	}
	if rootErr != nil || configuredErr != nil {
		return fmt.Errorf("space: close contract candidate capabilities: %w", errors.Join(rootErr, configuredErr))
	}
	return nil
}

// ReadContractCandidate is the one-shot rooted candidate collector.
func ReadContractCandidate(ctx context.Context, configuredRoot string, location ContractCandidateLocation) (snapshot ContractCandidateSnapshot, retErr error) {
	reader, err := OpenContractCandidateReader(configuredRoot, location)
	if err != nil {
		return ContractCandidateSnapshot{}, err
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			retErr = errors.Join(retErr, closeErr)
		}
	}()
	return reader.Read(ctx)
}

// Read freezes descriptor, schema/**, fixtures/**, and artifacts/** from the
// held root. It checks cancellation between directory and file operations.
// Read is part of the public package API.
func (r *ContractCandidateReader) Read(ctx context.Context) (ContractCandidateSnapshot, error) {
	if r == nil || r.root == nil || r.configured == nil {
		return ContractCandidateSnapshot{}, fmt.Errorf("%w: candidate reader is closed", ErrContractUnsafePath)
	}
	if err := ctx.Err(); err != nil {
		return ContractCandidateSnapshot{}, err
	}
	if err := r.verifySelectedPath(); err != nil {
		return ContractCandidateSnapshot{}, err
	}
	state := &contractWalkState{source: r.location.Source}
	descriptor, err := readRootContractFile(ctx, r.root, contract.DescriptorPath, contract.DescriptorPath, r.location.Source, r.hooks.afterLeafLstat)
	if err != nil {
		return ContractCandidateSnapshot{}, err
	}
	state.files = append(state.files, descriptor)
	state.aggregate = len(descriptor.Raw)
	if state.aggregate > maxContractCandidateBytes {
		return ContractCandidateSnapshot{}, fmt.Errorf("%w: raw candidate snapshot exceeds %d bytes", ErrContractBoundedResult, maxContractCandidateBytes)
	}
	for _, rootName := range []string{"schema", "fixtures", "artifacts"} {
		if err := walkOptionalContractDirectory(ctx, r.root, rootName, state, r.hooks); err != nil {
			return ContractCandidateSnapshot{}, err
		}
	}
	if err := r.verifySelectedPath(); err != nil {
		return ContractCandidateSnapshot{}, err
	}
	sort.Slice(state.files, func(i, j int) bool { return state.files[i].Path < state.files[j].Path })
	return ContractCandidateSnapshot{
		Source:      r.location.Source,
		Fingerprint: fingerprintContractFiles(state.files),
		Files:       state.files,
	}, nil
}

func (r *ContractCandidateReader) verifySelectedPath() error {
	current, err := r.configured.Lstat(r.location.Path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(r.identity, current) {
		return fmt.Errorf("%w: configured candidate directory changed", ErrContractUnsafePath)
	}
	return nil
}

type contractWalkState struct {
	source       ContractCandidateSource
	files        []ContractSnapshotFile
	explicit     int
	aggregate    int
	listingBytes int
	nodes        int
	directories  int
}

func walkOptionalContractDirectory(ctx context.Context, parent *os.Root, name string, state *contractWalkState, hooks contractCandidateHooks) error {
	info, err := parent.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("space: inspect contract root %q: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %q must be a real directory", ErrContractUnsafeEntry, name)
	}
	state.directories++
	if state.directories > maxContractCandidateDirectories {
		return fmt.Errorf("%w: raw candidate has more than %d directories", ErrContractBoundedResult, maxContractCandidateDirectories)
	}
	dir, _, err := openContractDirectory(parent, name, name, func() {
		if hooks.afterDirectoryLstat != nil {
			hooks.afterDirectoryLstat(name)
		}
	})
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }() // reason: preserve the traversal result
	return walkContractDirectory(ctx, dir, name, 1, state, hooks)
}

func walkContractDirectory(ctx context.Context, dir *os.Root, relative string, depth int, state *contractWalkState, hooks contractCandidateHooks) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > maxContractCandidateDepth {
		return fmt.Errorf("%w: raw candidate exceeds directory depth %d", ErrContractBoundedResult, maxContractCandidateDepth)
	}
	directoryBefore, err := dir.Stat(".")
	if err != nil {
		return fmt.Errorf("space: inspect contract directory %q: %w", relative, err)
	}
	listing, err := dir.Open(".")
	if err != nil {
		return fmt.Errorf("space: list contract directory %q: %w", relative, err)
	}
	defer func() { _ = listing.Close() }() // reason: preserve the traversal error
	for {
		entries, readErr := listing.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			state.nodes++
			if state.nodes > maxContractCandidateNodes {
				return fmt.Errorf("%w: raw candidate has more than %d filesystem nodes", ErrContractBoundedResult, maxContractCandidateNodes)
			}
			state.listingBytes += len(entry.Name()) + 1
			if state.listingBytes > maxContractFilesystemListingBytes {
				return fmt.Errorf("%w: filesystem contract tree listing is too large", ErrContractBoundedResult)
			}
			entryPath := path.Join(relative, entry.Name())
			info, statErr := dir.Lstat(entry.Name())
			if statErr != nil {
				return fmt.Errorf("space: inspect contract entry %q: %w", entryPath, statErr)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: %q is a symlink", ErrContractUnsafeEntry, entryPath)
			}
			if info.IsDir() {
				state.directories++
				if state.directories > maxContractCandidateDirectories {
					return fmt.Errorf("%w: raw candidate has more than %d directories", ErrContractBoundedResult, maxContractCandidateDirectories)
				}
				child, _, openErr := openContractDirectory(dir, entry.Name(), entryPath, func() {
					if hooks.afterDirectoryLstat != nil {
						hooks.afterDirectoryLstat(entryPath)
					}
				})
				if openErr != nil {
					return openErr
				}
				walkErr := walkContractDirectory(ctx, child, entryPath, depth+1, state, hooks)
				closeErr := child.Close()
				if walkErr != nil {
					return walkErr
				}
				if closeErr != nil {
					return fmt.Errorf("space: close contract directory %q: %w", entryPath, closeErr)
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%w: %q has mode %s", ErrContractUnsafeEntry, entryPath, info.Mode())
			}
			state.explicit++
			if state.explicit > maxContractCandidateFiles {
				return fmt.Errorf("%w: raw candidate has more than %d files", ErrContractBoundedResult, maxContractCandidateFiles)
			}
			file, fileErr := readRootContractFile(ctx, dir, entry.Name(), entryPath, state.source, hooks.afterLeafLstat)
			if fileErr != nil {
				return fileErr
			}
			state.aggregate += len(file.Raw)
			if state.aggregate > maxContractCandidateBytes {
				return fmt.Errorf("%w: raw candidate snapshot exceeds %d bytes", ErrContractBoundedResult, maxContractCandidateBytes)
			}
			state.files = append(state.files, file)
		}
		if readErr == io.EOF {
			directoryAfter, statErr := dir.Stat(".")
			if statErr != nil || !directoryAfter.IsDir() || !os.SameFile(directoryBefore, directoryAfter) ||
				directoryBefore.Size() != directoryAfter.Size() || !directoryBefore.ModTime().Equal(directoryAfter.ModTime()) {
				return fmt.Errorf("%w: directory %q changed while collecting", ErrContractUnsafeEntry, relative)
			}
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("space: list contract directory %q: %w", relative, readErr)
		}
	}
}

func readRootContractFile(ctx context.Context, root *os.Root, name, relative string, source ContractCandidateSource, afterLstat func(string)) (ContractSnapshotFile, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return ContractSnapshotFile{}, fmt.Errorf("space: inspect contract file %q: %w", relative, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return ContractSnapshotFile{}, fmt.Errorf("%w: %q has mode %s", ErrContractUnsafeEntry, relative, before.Mode())
	}
	if before.Size() < 0 || before.Size() > contract.MaxFileBytes {
		return ContractSnapshotFile{}, fmt.Errorf("%w: %q exceeds %d bytes", ErrContractBoundedResult, relative, contract.MaxFileBytes)
	}
	if afterLstat != nil {
		afterLstat(relative)
	}
	file, err := root.Open(name)
	if err != nil {
		return ContractSnapshotFile{}, fmt.Errorf("%w: open %q after inspection: %w", ErrContractUnsafeEntry, relative, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close() // reason: preserve the read/identity error
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return ContractSnapshotFile{}, fmt.Errorf("space: inspect opened contract file %q: %w", relative, err)
	}
	afterOpen, err := root.Lstat(name)
	if err != nil || !opened.Mode().IsRegular() || afterOpen.Mode()&os.ModeSymlink != 0 ||
		!afterOpen.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(opened, afterOpen) {
		return ContractSnapshotFile{}, fmt.Errorf("%w: %q changed while opening", ErrContractUnsafeEntry, relative)
	}
	raw, err := readContractFileContext(ctx, file, contract.MaxFileBytes)
	if err != nil {
		return ContractSnapshotFile{}, fmt.Errorf("space: read contract file %q: %w", relative, err)
	}
	afterRead, err := file.Stat()
	if err != nil {
		return ContractSnapshotFile{}, fmt.Errorf("space: re-inspect opened contract file %q: %w", relative, err)
	}
	afterPath, err := root.Lstat(name)
	if err != nil || !afterRead.Mode().IsRegular() || afterPath.Mode()&os.ModeSymlink != 0 ||
		!afterPath.Mode().IsRegular() || !os.SameFile(opened, afterRead) || !os.SameFile(afterRead, afterPath) ||
		opened.Size() != afterRead.Size() || int64(len(raw)) != afterRead.Size() || !opened.ModTime().Equal(afterRead.ModTime()) {
		return ContractSnapshotFile{}, fmt.Errorf("%w: %q changed while reading", ErrContractUnsafeEntry, relative)
	}
	if err := file.Close(); err != nil {
		return ContractSnapshotFile{}, fmt.Errorf("space: close contract file %q: %w", relative, err)
	}
	closed = true
	return ContractSnapshotFile{
		Path: relative, Kind: contract.CandidateRegular, Mode: contractFilesystemMode(opened),
		Source: source, Raw: raw,
	}, nil
}

func readContractFileContext(ctx context.Context, reader io.Reader, maximum int) ([]byte, error) {
	buffer := make([]byte, 32*1024)
	raw := make([]byte, 0, min(maximum, len(buffer)))
	for len(raw) <= maximum {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := maximum + 1 - len(raw)
		chunk := buffer
		if remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		n, err := reader.Read(chunk)
		if n > 0 {
			raw = append(raw, chunk[:n]...)
		}
		if len(raw) > maximum {
			return nil, ErrContractBoundedResult
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
	return nil, ErrContractBoundedResult
}

func contractFilesystemMode(info os.FileInfo) string {
	if info.Mode().Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

func fingerprintContractFiles(files []ContractSnapshotFile) string {
	hash := sha256.New()
	writeContractFingerprintField(hash, "contract-candidate-tree-v1")
	for _, file := range files {
		writeContractFingerprintField(hash, file.Path)
		writeContractFingerprintField(hash, string(file.Kind))
		writeContractFingerprintField(hash, file.Mode)
		writeContractFingerprintField(hash, artifact.Digest(file.Raw))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func writeContractFingerprintField(writer io.Writer, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])       // hash.Hash writes never fail
	_, _ = io.WriteString(writer, value) // hash.Hash writes never fail
}
