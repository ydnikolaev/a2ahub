package space

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
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

const maxContractMaterializeDifferences = 64

var (
	// ErrContractDestinationParentMissing is part of the public package API.
	ErrContractDestinationParentMissing = errors.New("space: contract destination parent is missing")
	// ErrContractDestinationDiverged is part of the public package API.
	ErrContractDestinationDiverged = errors.New("space: contract destination diverged")
	// ErrContractDestinationDurabilityUnknown is part of the public package API.
	ErrContractDestinationDurabilityUnknown = errors.New("space: contract destination is visible but durability is unknown")
	// ErrContractMaterializationInvalid is part of the public package API.
	ErrContractMaterializationInvalid = errors.New("space: contract historical snapshot is not materializable")
	// ErrContractAtomicNoReplaceUnsupported is part of the public package API.
	ErrContractAtomicNoReplaceUnsupported = errors.New("space: atomic no-replace directory install is unsupported")

	contractMaterializeMu sync.Mutex
)

// ContractMaterializeOutcome is part of the public package API.
type ContractMaterializeOutcome string

const (
	// ContractMaterialized is part of the public package API.
	ContractMaterialized ContractMaterializeOutcome = "materialized"
	// ContractAlreadyPresent is part of the public package API.
	ContractAlreadyPresent ContractMaterializeOutcome = "already-present"
)

// ContractMaterializeResult keeps the two historical verification facts
// distinct: a legacy descriptor is a Git object at the publish commit, not an
// event-digest-covered byte stream.
// ContractMaterializeResult is part of the public package API.
type ContractMaterializeResult struct {
	ContractID             string                     `json:"contract"`
	Version                string                     `json:"version"`
	SourceCommit           string                     `json:"commit"`
	DigestProfile          contract.DigestProfile     `json:"digest_profile"`
	AggregateDigest        string                     `json:"aggregate_digest"`
	AggregateVerification  string                     `json:"aggregate_verification"`
	DescriptorVerification string                     `json:"descriptor_verification"`
	Destination            string                     `json:"destination"`
	Outcome                ContractMaterializeOutcome `json:"outcome"`
	Durable                bool                       `json:"durable"`
}

// ContractMaterializer owns one held project-root capability. Materialize
// never reopens the configured pathname and every descendant traversal keeps
// using os.Root capabilities plus no-follow identity checks.
// ContractMaterializer is part of the public package API.
type ContractMaterializer struct {
	root  *os.Root
	hooks contractMaterializeHooks
}

type contractMaterializeHooks struct {
	afterParentComponentLstat func(string)
	afterCompareLeafLstat     func(string)
	beforeWrite               func(string) error
	syncFile                  func(string, *os.File) error
	syncDirectory             func(string, *os.File) error
	beforeRename              func() error
	afterDestinationRecheck   func()
	syncParent                func(*os.File) error
}

// NewContractMaterializer is part of the public package API.
func NewContractMaterializer(projectRoot string) (*ContractMaterializer, error) {
	root, _, err := openHeldContractRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	return &ContractMaterializer{root: root}, nil
}

// Close is part of the public package API.
func (m *ContractMaterializer) Close() error {
	if m == nil || m.root == nil {
		return nil
	}
	err := m.root.Close()
	m.root = nil
	return err
}

// Materialize writes exactly contract.md plus the verified carried set. The
// complete snapshot is revalidated before the destination is inspected.
// Materialize is part of the public package API.
func (m *ContractMaterializer) Materialize(
	ctx context.Context,
	snapshot HistoricalSnapshot,
	destination string,
) (ContractMaterializeResult, error) {
	if m == nil || m.root == nil {
		return ContractMaterializeResult{}, fmt.Errorf("%w: materializer is closed", ErrContractUnsafePath)
	}
	if err := ctx.Err(); err != nil {
		return ContractMaterializeResult{}, err
	}
	files, err := validateMaterializationSnapshot(snapshot)
	if err != nil {
		return ContractMaterializeResult{}, err
	}
	if !cleanContractRelativePath(destination) {
		return ContractMaterializeResult{}, fmt.Errorf("%w: invalid materialization destination", ErrContractUnsafePath)
	}

	// The held-root operations prevent escape. Serializing the final
	// compare/create decision also gives concurrent in-process callers the
	// specified winner/re-read behavior without creating a lock file that
	// would violate zero-write identical reruns.
	contractMaterializeMu.Lock()
	defer contractMaterializeMu.Unlock()

	parent, leaf, closeParent, err := m.openMaterializeParent(destination)
	if err != nil {
		return ContractMaterializeResult{}, err
	}
	if closeParent {
		defer func() {
			_ = parent.Close() // reason: the operation result remains primary; the held project root still owns no data
		}()
	}

	baseResult := materializeResult(snapshot, destination)
	info, err := parent.Lstat(leaf)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return ContractMaterializeResult{}, contractDestinationDivergence([]string{"unsafe:" + destination})
		}
		equal, differences, compareErr := m.compareMaterializedDirectory(ctx, parent, leaf, files)
		if compareErr != nil {
			return ContractMaterializeResult{}, compareErr
		}
		if !equal {
			return ContractMaterializeResult{}, contractDestinationDivergence(differences)
		}
		baseResult.Outcome = ContractAlreadyPresent
		baseResult.Durable = true
		return baseResult, nil
	case errors.Is(err, os.ErrNotExist):
		return m.materializeAbsent(ctx, parent, leaf, destination, snapshot, files)
	default:
		return ContractMaterializeResult{}, fmt.Errorf("%w: inspect destination: %w", ErrContractUnsafePath, err)
	}
}

func validateMaterializationSnapshot(snapshot HistoricalSnapshot) ([]ContractSnapshotFile, error) {
	parsedID, idErr := artifact.ParseID(snapshot.ContractID)
	canonicalVersion, versionErr := version.Canonical(snapshot.Version)
	if idErr != nil || parsedID.Prefix != "XC" || parsedID.Class != artifact.ClassStanding ||
		versionErr != nil || canonicalVersion != snapshot.Version ||
		!contractGitObjectID(snapshot.CommitSHA) || snapshot.CommitSHA != strings.ToLower(snapshot.CommitSHA) {
		return nil, ErrContractMaterializationInvalid
	}
	if snapshot.AggregateVerification != ContractVerificationEventDigest ||
		(snapshot.DigestProfile != contract.ProfileContractSetV2 && snapshot.DigestProfile != contract.ProfileContractTreeV1) {
		return nil, ErrContractMaterializationInvalid
	}
	wantDescriptorVerification := ContractVerificationEventDigest
	if snapshot.DigestProfile == contract.ProfileContractTreeV1 {
		wantDescriptorVerification = ContractVerificationGitObjectAtPublishCommit
	}
	if snapshot.DescriptorVerification != wantDescriptorVerification || len(snapshot.Files) == 0 ||
		len(snapshot.Files) > contract.MaxExplicitFiles+1 {
		return nil, ErrContractMaterializationInvalid
	}

	files := make([]ContractSnapshotFile, len(snapshot.Files))
	seen := make(map[string]struct{}, len(files))
	var core contract.CandidateSnapshot
	aggregate := 0
	for index, file := range snapshot.Files {
		if !cleanContractRelativePath(file.Path) || file.Kind != contract.CandidateRegular ||
			(file.Mode != "100644" && file.Mode != "100755") || len(file.Raw) > contract.MaxFileBytes {
			return nil, ErrContractMaterializationInvalid
		}
		if _, exists := seen[file.Path]; exists {
			return nil, ErrContractMaterializationInvalid
		}
		seen[file.Path] = struct{}{}
		aggregate += len(file.Raw)
		if aggregate > contract.MaxAggregateBytes+contract.MaxFileBytes {
			return nil, ErrContractMaterializationInvalid
		}
		files[index] = file
		files[index].Raw = bytes.Clone(file.Raw)
		candidate := contract.CandidateFile{Path: file.Path, Kind: file.Kind, Raw: bytes.Clone(file.Raw)}
		if file.Path == contract.DescriptorPath {
			if len(core.Descriptor.Raw) != 0 {
				return nil, ErrContractMaterializationInvalid
			}
			core.Descriptor = candidate
		} else {
			core.Files = append(core.Files, candidate)
		}
	}
	if len(core.Descriptor.Raw) == 0 {
		return nil, ErrContractMaterializationInvalid
	}
	parsedDescriptor, err := contract.ParseDescriptor(core.Descriptor.Raw)
	if err != nil || !reflect.DeepEqual(parsedDescriptor, snapshot.Descriptor) {
		return nil, ErrContractMaterializationInvalid
	}
	descriptorIdentity, err := parseHistoricalDescriptor(core.Descriptor.Raw)
	if err != nil || descriptorIdentity.ID != snapshot.ContractID || descriptorIdentity.Version != snapshot.Version ||
		(snapshot.DigestProfile == contract.ProfileContractSetV2 && descriptorIdentity.Schema != "envelope/v2") ||
		(snapshot.DigestProfile == contract.ProfileContractTreeV1 && descriptorIdentity.Schema != "envelope/v1") {
		return nil, ErrContractMaterializationInvalid
	}
	var event contractHistoricalEvent
	if len(snapshot.PublishEventRaw) == 0 || yaml.Unmarshal(snapshot.PublishEventRaw, &event) != nil ||
		event.Schema != snapshot.EventSchema || event.Subject != snapshot.ContractID || event.Transition != "publish" ||
		event.Version != snapshot.Version || event.Digest != snapshot.PublishedDigest {
		return nil, ErrContractMaterializationInvalid
	}
	resolvedProfile, issues := contract.ResolveDigestProfile(event.Schema, event.DigestProfile)
	if len(issues) != 0 || resolvedProfile != snapshot.DigestProfile || !isContractEventPath(snapshot.PublishEventPath) ||
		strings.TrimSuffix(path.Base(snapshot.PublishEventPath), ".yaml") != event.Event {
		return nil, ErrContractMaterializationInvalid
	}
	layout, layoutErr := NewLayout(parsedID.System)
	if layoutErr != nil || snapshot.DescriptorPath != layout.ProvidesContract(parsedID.Slug) ||
		!strings.HasPrefix(snapshot.PublishEventPath, parsedID.System+"/events/") || event.Actor.System != parsedID.System {
		return nil, ErrContractMaterializationInvalid
	}
	set, issues := contract.ValidateCarriedSet(snapshot.DigestProfile, parsedDescriptor, core)
	if len(issues) != 0 || len(set.VerifyDigest(snapshot.PublishedDigest)) != 0 ||
		snapshot.CarriedSet.Profile != set.Profile || snapshot.CarriedSet.AggregateDigest != set.AggregateDigest {
		return nil, ErrContractMaterializationInvalid
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func materializeResult(snapshot HistoricalSnapshot, destination string) ContractMaterializeResult {
	return ContractMaterializeResult{
		ContractID: snapshot.ContractID, Version: snapshot.Version, SourceCommit: snapshot.CommitSHA,
		DigestProfile: snapshot.DigestProfile, AggregateDigest: snapshot.PublishedDigest,
		AggregateVerification: snapshot.AggregateVerification, DescriptorVerification: snapshot.DescriptorVerification,
		Destination: destination,
	}
}

func (m *ContractMaterializer) openMaterializeParent(destination string) (*os.Root, string, bool, error) {
	components := strings.Split(destination, "/")
	leaf := components[len(components)-1]
	current := m.root
	closeCurrent := false
	for index, component := range components[:len(components)-1] {
		display := path.Join(components[:index+1]...)
		if _, err := current.Lstat(component); errors.Is(err, os.ErrNotExist) {
			if closeCurrent {
				_ = current.Close() // reason: preserve the typed missing-parent refusal
			}
			return nil, "", false, fmt.Errorf("%w: %s", ErrContractDestinationParentMissing, display)
		} else if err != nil {
			if closeCurrent {
				_ = current.Close() // reason: preserve the unsafe-parent refusal
			}
			return nil, "", false, fmt.Errorf("%w: inspect %s: %w", ErrContractUnsafePath, display, err)
		}
		child, _, err := openContractDirectory(current, component, display, func() {
			if m.hooks.afterParentComponentLstat != nil {
				m.hooks.afterParentComponentLstat(display)
			}
		})
		if err != nil {
			if closeCurrent {
				_ = current.Close() // reason: preserve the no-follow component refusal
			}
			return nil, "", false, err
		}
		if closeCurrent {
			_ = current.Close() // reason: the independently held child capability replaces this parent
		}
		current, closeCurrent = child, true
	}
	return current, leaf, closeCurrent, nil
}

func (m *ContractMaterializer) compareMaterializedDirectory(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	expected []ContractSnapshotFile,
) (bool, []string, error) {
	root, _, err := openContractDirectory(parent, leaf, leaf, nil)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = root.Close() }() // reason: compare result remains primary
	wantFiles := make(map[string][]byte, len(expected))
	wantDirectories := map[string]struct{}{".": {}}
	for _, file := range expected {
		wantFiles[file.Path] = file.Raw
		for directory := path.Dir(file.Path); directory != "."; directory = path.Dir(directory) {
			wantDirectories[directory] = struct{}{}
		}
	}
	state := &contractMaterializeCompare{
		materializer: m, wantFiles: wantFiles, wantDirectories: wantDirectories,
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

type contractMaterializeCompare struct {
	materializer         *ContractMaterializer
	wantFiles            map[string][]byte
	wantDirectories      map[string]struct{}
	seenFiles            map[string]struct{}
	seenDirectories      map[string]struct{}
	differences          []string
	differencesTruncated bool
	nodes                int
}

func (s *contractMaterializeCompare) walk(ctx context.Context, root *os.Root, directory string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	handle, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("%w: open destination directory %s: %w", ErrContractUnsafePath, directory, err)
	}
	entries, readErr := handle.ReadDir(contract.MaxExplicitFiles*4 + 2)
	closeErr := handle.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("%w: enumerate destination directory %s: %w", ErrContractUnsafePath, directory, readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("space: close destination directory %s: %w", directory, closeErr)
	}
	if len(entries) > contract.MaxExplicitFiles*4+1 {
		return fmt.Errorf("%w: materialized destination enumeration exceeded bound", ErrContractBoundedResult)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.nodes++
		if s.nodes > contract.MaxExplicitFiles*4+1 {
			return fmt.Errorf("%w: materialized destination nodes exceeded bound", ErrContractBoundedResult)
		}
		name := entry.Name()
		relative := name
		if directory != "." {
			relative = path.Join(directory, name)
		}
		before, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("%w: inspect destination entry %s: %w", ErrContractUnsafePath, relative, err)
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
			child, _, err := openContractDirectory(root, name, relative, nil)
			if err != nil {
				return err
			}
			walkErr := s.walk(ctx, child, relative)
			closeErr := child.Close()
			if walkErr != nil {
				return walkErr
			}
			if closeErr != nil {
				return fmt.Errorf("space: close destination capability %s: %w", relative, closeErr)
			}
			continue
		}
		if !before.Mode().IsRegular() {
			s.addDifference("unsafe:" + relative)
			continue
		}
		if s.materializer.hooks.afterCompareLeafLstat != nil {
			s.materializer.hooks.afterCompareLeafLstat(relative)
		}
		file, err := root.OpenFile(name, os.O_RDONLY, 0)
		if err != nil {
			return fmt.Errorf("%w: open destination entry %s: %w", ErrContractUnsafePath, relative, err)
		}
		opened, statErr := file.Stat()
		raw, readErr := io.ReadAll(io.LimitReader(file, contract.MaxFileBytes+1))
		closeErr = file.Close()
		after, afterErr := root.Lstat(name)
		if statErr != nil || readErr != nil || closeErr != nil || afterErr != nil ||
			!opened.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 ||
			!os.SameFile(before, opened) || !os.SameFile(opened, after) {
			return fmt.Errorf("%w: destination entry %s changed while reading", ErrContractUnsafePath, relative)
		}
		if len(raw) > contract.MaxFileBytes {
			return fmt.Errorf("%w: destination entry %s exceeds file bound", ErrContractBoundedResult, relative)
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

func (s *contractMaterializeCompare) addDifference(value string) {
	if len(s.differences) < maxContractMaterializeDifferences {
		s.differences = append(s.differences, value)
		return
	}
	s.differencesTruncated = true
}

func (s *contractMaterializeCompare) finalizedDifferences() []string {
	differences := append([]string(nil), s.differences...)
	if s.differencesTruncated {
		marker := "truncated:additional-differences"
		if len(differences) == maxContractMaterializeDifferences {
			differences[len(differences)-1] = marker
		} else {
			differences = append(differences, marker)
		}
	}
	sort.Strings(differences)
	return differences
}

func (m *ContractMaterializer) materializeAbsent(
	ctx context.Context,
	parent *os.Root,
	leaf string,
	destination string,
	snapshot HistoricalSnapshot,
	files []ContractSnapshotFile,
) (result ContractMaterializeResult, retErr error) {
	temporary, err := newContractMaterializeTemporaryName()
	if err != nil {
		return ContractMaterializeResult{}, err
	}
	if err := parent.Mkdir(temporary, 0o700); err != nil {
		return ContractMaterializeResult{}, fmt.Errorf("space: create materialization temporary sibling: %w", err)
	}
	temporaryPresent := true
	var temporaryIdentity os.FileInfo
	defer func() {
		if !temporaryPresent {
			return
		}
		if temporaryIdentity == nil {
			if cleanupErr := parent.Remove(temporary); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("space: clean unverified materialization temporary sibling: %w", cleanupErr))
			}
			return
		}
		current, inspectErr := parent.Lstat(temporary)
		if errors.Is(inspectErr, os.ErrNotExist) {
			return
		}
		if inspectErr != nil || current.Mode()&os.ModeSymlink != 0 || !current.IsDir() || !os.SameFile(temporaryIdentity, current) {
			retErr = errors.Join(retErr, fmt.Errorf("%w: temporary sibling changed before cleanup", ErrContractUnsafePath))
			return
		}
		if cleanupErr := parent.RemoveAll(temporary); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("space: clean materialization temporary sibling: %w", cleanupErr))
		}
	}()
	temporaryIdentity, err = parent.Lstat(temporary)
	if err != nil || !temporaryIdentity.IsDir() || temporaryIdentity.Mode()&os.ModeSymlink != 0 {
		return ContractMaterializeResult{}, fmt.Errorf("%w: inspect materialization temporary sibling", ErrContractUnsafePath)
	}

	temporaryRoot, _, err := openContractDirectory(parent, temporary, temporary, nil)
	if err != nil {
		return ContractMaterializeResult{}, err
	}
	if err := m.writeMaterializedTree(ctx, temporaryRoot, files); err != nil {
		_ = temporaryRoot.Close() // reason: the write/refusal error remains primary and cleanup still runs
		return ContractMaterializeResult{}, err
	}
	if err := temporaryRoot.Close(); err != nil {
		return ContractMaterializeResult{}, fmt.Errorf("space: close completed materialization tree: %w", err)
	}

	if m.hooks.beforeRename != nil {
		if err := m.hooks.beforeRename(); err != nil {
			return ContractMaterializeResult{}, err
		}
	}
	if winner, err := parent.Lstat(leaf); err == nil {
		if winner.Mode()&os.ModeSymlink != 0 || !winner.IsDir() {
			return ContractMaterializeResult{}, contractDestinationDivergence([]string{"unsafe:" + destination})
		}
		equal, differences, compareErr := m.compareMaterializedDirectory(ctx, parent, leaf, files)
		if compareErr != nil {
			return ContractMaterializeResult{}, compareErr
		}
		if !equal {
			return ContractMaterializeResult{}, contractDestinationDivergence(differences)
		}
		result = materializeResult(snapshot, destination)
		result.Outcome, result.Durable = ContractAlreadyPresent, true
		return result, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ContractMaterializeResult{}, fmt.Errorf("%w: re-inspect destination before rename: %w", ErrContractUnsafePath, err)
	}

	if m.hooks.afterDestinationRecheck != nil {
		m.hooks.afterDestinationRecheck()
	}
	installed, installErr := renameContractMaterializeNoReplace(parent, temporary, leaf)
	if !installed {
		if winner, winnerErr := parent.Lstat(leaf); winnerErr == nil {
			if winner.Mode()&os.ModeSymlink != 0 || !winner.IsDir() {
				return ContractMaterializeResult{}, contractDestinationDivergence([]string{"unsafe:" + destination})
			}
			equal, differences, compareErr := m.compareMaterializedDirectory(ctx, parent, leaf, files)
			if compareErr != nil {
				return ContractMaterializeResult{}, compareErr
			}
			if !equal {
				return ContractMaterializeResult{}, contractDestinationDivergence(differences)
			}
			result = materializeResult(snapshot, destination)
			result.Outcome, result.Durable = ContractAlreadyPresent, true
			return result, nil
		}
		return ContractMaterializeResult{}, fmt.Errorf("space: atomically install materialized contract: %w", installErr)
	}
	temporaryPresent = false
	result = materializeResult(snapshot, destination)
	result.Outcome = ContractMaterialized
	parentHandle, err := parent.Open(".")
	if err != nil {
		return result, fmt.Errorf("%w: %w", ErrContractDestinationDurabilityUnknown, errors.Join(installErr, err))
	}
	if m.hooks.syncParent != nil {
		err = m.hooks.syncParent(parentHandle)
	} else {
		err = parentHandle.Sync()
	}
	closeErr := parentHandle.Close()
	if installErr != nil || err != nil || closeErr != nil {
		return result, fmt.Errorf("%w: %w", ErrContractDestinationDurabilityUnknown, errors.Join(installErr, err, closeErr))
	}
	result.Durable = true
	return result, nil
}

func (m *ContractMaterializer) writeMaterializedTree(ctx context.Context, root *os.Root, files []ContractSnapshotFile) error {
	directories := materializeDirectories(files)
	roots := map[string]*os.Root{".": root}
	closeRoots := func() {
		for _, directory := range reverseMaterializeDirectories(directories) {
			if child := roots[directory]; child != nil {
				_ = child.Close() // reason: the originating write/sync error remains primary
			}
		}
	}
	defer closeRoots()

	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		parentName := path.Dir(directory)
		parent := roots[parentName]
		if err := parent.Mkdir(path.Base(directory), 0o700); err != nil {
			return fmt.Errorf("space: create materialization directory %s: %w", directory, err)
		}
		child, _, err := openContractDirectory(parent, path.Base(directory), directory, nil)
		if err != nil {
			return err
		}
		roots[directory] = child
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.hooks.beforeWrite != nil {
			if err := m.hooks.beforeWrite(file.Path); err != nil {
				return err
			}
		}
		parent := roots[path.Dir(file.Path)]
		if err := m.writeMaterializedFile(ctx, parent, file); err != nil {
			return err
		}
	}
	equal, differences, err := m.compareRootedMaterializedTree(ctx, root, files)
	if err != nil {
		return err
	}
	if !equal {
		return fmt.Errorf("%w: temporary tree changed: %s", ErrContractUnsafePath, strings.Join(differences, ", "))
	}
	for _, directory := range append(reverseMaterializeDirectories(directories), ".") {
		handle, err := roots[directory].Open(".")
		if err != nil {
			return fmt.Errorf("space: open materialization directory %s for fsync: %w", directory, err)
		}
		if m.hooks.syncDirectory != nil {
			err = m.hooks.syncDirectory(directory, handle)
		} else {
			err = handle.Sync()
		}
		closeErr := handle.Close()
		if err != nil || closeErr != nil {
			return fmt.Errorf("space: fsync materialization directory %s: %w", directory, errors.Join(err, closeErr))
		}
	}
	return nil
}

func (m *ContractMaterializer) writeMaterializedFile(ctx context.Context, root *os.Root, file ContractSnapshotFile) error {
	mode := os.FileMode(0o644)
	if file.Mode == "100755" {
		mode = 0o755
	}
	handle, err := root.OpenFile(path.Base(file.Path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("space: create materialization file %s: %w", file.Path, err)
	}
	opened, statErr := handle.Stat()
	writeErr := writeMaterializedBytes(ctx, handle, file.Raw)
	if writeErr == nil {
		if m.hooks.syncFile != nil {
			writeErr = m.hooks.syncFile(file.Path, handle)
		} else {
			writeErr = handle.Sync()
		}
	}
	closeErr := handle.Close()
	after, afterErr := root.Lstat(path.Base(file.Path))
	if statErr != nil || writeErr != nil || closeErr != nil {
		return fmt.Errorf("space: write materialization file %s: %w", file.Path, errors.Join(statErr, writeErr, closeErr))
	}
	if afterErr != nil || !opened.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return fmt.Errorf("%w: materialization file %s changed while writing", ErrContractUnsafePath, file.Path)
	}
	return nil
}

func writeMaterializedBytes(ctx context.Context, writer io.Writer, raw []byte) error {
	for len(raw) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
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

func (m *ContractMaterializer) compareRootedMaterializedTree(ctx context.Context, root *os.Root, expected []ContractSnapshotFile) (bool, []string, error) {
	wantFiles := make(map[string][]byte, len(expected))
	wantDirectories := map[string]struct{}{".": {}}
	for _, file := range expected {
		wantFiles[file.Path] = file.Raw
		for directory := path.Dir(file.Path); directory != "."; directory = path.Dir(directory) {
			wantDirectories[directory] = struct{}{}
		}
	}
	state := &contractMaterializeCompare{
		materializer: m, wantFiles: wantFiles, wantDirectories: wantDirectories,
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

func materializeDirectories(files []ContractSnapshotFile) []string {
	set := make(map[string]struct{})
	for _, file := range files {
		for directory := path.Dir(file.Path); directory != "."; directory = path.Dir(directory) {
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

func reverseMaterializeDirectories(directories []string) []string {
	reversed := append([]string(nil), directories...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func newContractMaterializeTemporaryName() (string, error) {
	var token [16]byte
	if _, err := io.ReadFull(rand.Reader, token[:]); err != nil {
		return "", fmt.Errorf("space: create materialization token: %w", err)
	}
	return ".a2a-materialize-" + hex.EncodeToString(token[:]), nil
}

func contractDestinationDivergence(differences []string) error {
	sorted := append([]string(nil), differences...)
	sort.Strings(sorted)
	if len(sorted) == 0 {
		return ErrContractDestinationDiverged
	}
	return fmt.Errorf("%w: %s", ErrContractDestinationDiverged, strings.Join(sorted, ", "))
}
