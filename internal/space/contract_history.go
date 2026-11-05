package space

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

const (
	contractAuthoritativeMainRef = "refs/remotes/origin/main"

	// ContractVerificationEventDigest is part of the public package API.
	ContractVerificationEventDigest = "event-digest"
	// ContractVerificationGitObjectAtPublishCommit is part of the public package API.
	ContractVerificationGitObjectAtPublishCommit = "git-object-at-publish-commit"
)

var (
	// ErrContractInvalidReference is part of the public package API.
	ErrContractInvalidReference = errors.New("space: contract reference must be an exact XC id and canonical version")
	// ErrContractNotPublished is part of the public package API.
	ErrContractNotPublished = errors.New("space: contract version is not published")
	// ErrContractHistoryInvalid is part of the public package API.
	ErrContractHistoryInvalid = errors.New("space: contract publication history is invalid")
	// ErrContractDigestMismatch is part of the public package API.
	ErrContractDigestMismatch = errors.New("space: contract publication digest does not match historical bytes")
	// ErrContractObjectUnavailable is part of the public package API.
	ErrContractObjectUnavailable = errors.New("space: required Git object is unavailable")
)

// HistoricalSnapshot contains only bytes read from one verified Git commit.
// Files includes contract.md plus every profile-carried leaf, sorted by path.
// HistoricalSnapshot is part of the public package API.
type HistoricalSnapshot struct {
	ContractID             string
	Version                string
	CommitSHA              string
	TreeObjectID           string
	DescriptorPath         string
	PublishEventPath       string
	PublishEventRaw        []byte
	EventSchema            string
	DigestProfile          contract.DigestProfile
	PublishedDigest        string
	AggregateVerification  string
	DescriptorVerification string
	Descriptor             contract.Descriptor
	CarriedSet             contract.CarriedSet
	Files                  []ContractSnapshotFile
}

// HistoricalResolution is one borrowed exact-version result delivered by
// VisitContractVersions. Err is version-scoped: one missing or invalid
// publication must not make a caller borrow facts from another version.
// Snapshot bytes are valid only for the duration of the visitor call; callers
// must project the facts they need before returning.
// HistoricalResolution is part of the public package API.
type HistoricalResolution struct {
	Version  string
	Snapshot HistoricalSnapshot
	Err      error
}

func (s HistoricalSnapshot) file(name string) ContractSnapshotFile {
	for _, file := range s.Files {
		if file.Path == name {
			return file
		}
	}
	return ContractSnapshotFile{}
}

type contractHistoricalDescriptor struct {
	Schema  string `yaml:"schema"`
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
}

type contractHistoricalEvent struct {
	Schema        string                     `yaml:"schema"`
	Event         string                     `yaml:"event"`
	Subject       string                     `yaml:"subject"`
	Transition    string                     `yaml:"transition"`
	Version       string                     `yaml:"version"`
	Digest        string                     `yaml:"digest"`
	DigestProfile string                     `yaml:"digest_profile"`
	Publication   contract.PublicationIntent `yaml:"publication"`
	Actor         struct {
		System string `yaml:"system"`
	} `yaml:"actor"`
}

// ContractHistoryDocument is one exact historical document presented to the
// canonical validation consumer. Raw is detached from the Git read buffer.
// ContractHistoryDocument is part of the public package API.
type ContractHistoryDocument struct {
	Path   string
	Schema string
	Raw    []byte
}

// ContractHistoryDocuments is the descriptor/event pair whose canonical
// schema validity is required before it may establish publication.
// ContractHistoryDocuments is part of the public package API.
type ContractHistoryDocuments struct {
	Descriptor   ContractHistoryDocument
	PublishEvent ContractHistoryDocument
}

// ContractHistoryDocumentValidator is the consumer-side seam that keeps
// schema mechanics in internal/validate while making validity mandatory here.
// ContractHistoryDocumentValidator is part of the public package API.
type ContractHistoryDocumentValidator interface {
	ValidateHistoricalContractDocuments(context.Context, ContractHistoryDocuments) error
}

type contractEstablishment struct {
	CommitSHA string
	// Parent is the establishing commit's first parent ("" for a root commit),
	// carried from the one event walk so no later step asks git again.
	Parent string
	// TreeObjectID and DescriptorFile are the contract root's tree object and
	// the descriptor at CommitSHA, resolved once by the establishment check.
	TreeObjectID   string
	DescriptorFile ContractSnapshotFile
	// EventMode and EventOID are the event file as the walk saw it added.
	EventMode  string
	EventOID   string
	Descriptor contractHistoricalDescriptor
	EventPath  string
	EventRaw   []byte
	Event      contractHistoricalEvent
	Profile    contract.DigestProfile
}

// ResolveContractVersion walks only origin/main's first-parent line. A merge
// publication is therefore established by the merge commit itself, while
// second-parent-only commits never become authority.
// ResolveContractVersion is part of the public package API.
func ResolveContractVersion(ctx context.Context, repoDir, contractID, requestedVersion string, validator ContractHistoryDocumentValidator) (HistoricalSnapshot, error) {
	parsedID, err := artifact.ParseID(contractID)
	if err != nil || parsedID.Prefix != "XC" || parsedID.Class != artifact.ClassStanding {
		return HistoricalSnapshot{}, fmt.Errorf("%w: %q", ErrContractInvalidReference, contractID)
	}
	canonical, err := version.Canonical(requestedVersion)
	if err != nil || canonical != requestedVersion {
		return HistoricalSnapshot{}, fmt.Errorf("%w: %q", ErrContractInvalidReference, requestedVersion)
	}
	if validator == nil {
		return HistoricalSnapshot{}, fmt.Errorf("%w: canonical document validator is required", ErrContractHistoryInvalid)
	}
	authoritativeCommit, err := contractGitResolveCommit(ctx, repoDir, contractAuthoritativeMainRef)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	return resolveContractVersionAt(ctx, repoDir, authoritativeCommit, contractID, requestedVersion, validator)
}

// VisitContractVersions resolves a bounded set of exact versions against one
// pinned origin/main revision. Publication history is discovered once for the
// whole request, then each immutable package is read, visited and released
// before the next package is materialized. Results are delivered in requested
// order and version-scoped errors do not block other exact versions.
//
// The resolution pointer and all Snapshot byte views are borrowed. The
// visitor must project any durable metadata before it returns; the resolver
// invalidates raw byte views at that boundary so a batch cannot retain every
// historical package accidentally.
// VisitContractVersions is part of the public package API.
func VisitContractVersions(ctx context.Context, repoDir, contractID string, requestedVersions []string, validator ContractHistoryDocumentValidator, visit func(*HistoricalResolution) error) error {
	parsedID, err := artifact.ParseID(contractID)
	if err != nil || parsedID.Prefix != "XC" || parsedID.Class != artifact.ClassStanding || validator == nil || visit == nil || len(requestedVersions) > maxContractPublicationEstablishments {
		return fmt.Errorf("%w: %q", ErrContractInvalidReference, contractID)
	}
	seen := make(map[string]struct{}, len(requestedVersions))
	for _, requestedVersion := range requestedVersions {
		if _, duplicate := seen[requestedVersion]; duplicate {
			return fmt.Errorf("%w: duplicate requested version %q", ErrContractInvalidReference, requestedVersion)
		}
		seen[requestedVersion] = struct{}{}
	}
	if len(requestedVersions) == 0 {
		return nil
	}
	versionErrors := make([]error, len(requestedVersions))
	validVersions := make([]string, 0, len(requestedVersions))
	for index, requestedVersion := range requestedVersions {
		canonical, versionErr := version.Canonical(requestedVersion)
		if versionErr != nil || canonical != requestedVersion {
			versionErrors[index] = fmt.Errorf("%w: %q", ErrContractInvalidReference, requestedVersion)
			continue
		}
		validVersions = append(validVersions, requestedVersion)
	}
	if len(validVersions) > 0 {
		authoritativeCommit, resolveErr := contractGitResolveCommit(ctx, repoDir, contractAuthoritativeMainRef)
		if resolveErr != nil {
			return resolveErr
		}
		validIndex := 0
		if err := visitContractVersionsAt(ctx, repoDir, authoritativeCommit, contractID, validVersions, validator, func(resolution *HistoricalResolution) error {
			for validIndex < len(requestedVersions) && versionErrors[validIndex] != nil {
				invalid := HistoricalResolution{Version: requestedVersions[validIndex], Err: versionErrors[validIndex]}
				if err := visit(&invalid); err != nil {
					return err
				}
				validIndex++
			}
			if validIndex >= len(requestedVersions) || requestedVersions[validIndex] != resolution.Version {
				return fmt.Errorf("%w: historical visitor returned version %q out of requested order", ErrContractHistoryInvalid, resolution.Version)
			}
			if err := visit(resolution); err != nil {
				return err
			}
			validIndex++
			return nil
		}); err != nil {
			return err
		}
		for validIndex < len(requestedVersions) {
			if versionErrors[validIndex] == nil {
				return fmt.Errorf("%w: historical visitor omitted requested version %q", ErrContractHistoryInvalid, requestedVersions[validIndex])
			}
			invalid := HistoricalResolution{Version: requestedVersions[validIndex], Err: versionErrors[validIndex]}
			if err := visit(&invalid); err != nil {
				return err
			}
			validIndex++
		}
		return nil
	}
	for index, requestedVersion := range requestedVersions {
		resolution := HistoricalResolution{Version: requestedVersion, Err: versionErrors[index]}
		if err := visit(&resolution); err != nil {
			return err
		}
	}
	return nil
}

func parseHistoricalDescriptor(raw []byte) (contractHistoricalDescriptor, error) {
	frontmatter, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return contractHistoricalDescriptor{}, err
	}
	var descriptor contractHistoricalDescriptor
	if err := yaml.Unmarshal(frontmatter.YAML, &descriptor); err != nil {
		return contractHistoricalDescriptor{}, err
	}
	return descriptor, nil
}

func contractDescriptorHistory(ctx context.Context, repoDir, authoritativeCommit, descriptorPath string) (map[string]struct{}, error) {
	raw, err := contractGitBytes(ctx, repoDir, maxContractGitListingBytes,
		"rev-list", "--first-parent", "--reverse", authoritativeCommit, "--", descriptorPath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: cannot walk first-parent history; run a2a sync: %w", ErrContractObjectUnavailable, err)
	}
	fields := strings.Fields(string(raw))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		// rev-list prints full object ids of commits it just walked, so the id
		// needs no second round trip to be believed; only its shape is checked.
		if !contractGitObjectID(field) {
			return nil, fmt.Errorf("%w: descriptor history contains a non-object id", ErrContractHistoryInvalid)
		}
		verified := strings.ToLower(field)
		if _, duplicate := seen[verified]; duplicate {
			return nil, fmt.Errorf("%w: descriptor history repeats commit %s", ErrContractHistoryInvalid, verified)
		}
		seen[verified] = struct{}{}
	}
	return seen, nil
}

// maxContractEventWalkBytes is the supported-history ceiling of the ONE git log
// that discovers every publish event. It was 1 MiB (maxContractGitListingBytes)
// when the listing printed paths only, about 110 bytes per added event, which
// supported roughly 9,500 event commits. Carrying each added file's mode and
// blob id (`--raw --no-abbrev`) is what lets the walk stop asking git again per
// commit, and it costs about 345 bytes per commit that adds an event and one
// more file (measured: 3.45 MB for 10,000 such commits). 64 MiB therefore
// supports roughly 190,000 of them, and a refusal past it is still the bounded
// ErrContractBoundedResult, never a silently shorter history.
const maxContractEventWalkBytes = 64 << 20

// contractEventCommit is one first-parent commit that added at least one
// canonical event path, with everything the later steps need about it.
type contractEventCommit struct {
	Commit string
	// Parent is the first parent, "" for a root commit.
	Parent string
	// Events are the event paths this commit added, sorted by path.
	Events []contractGitTreeEntry
}

// contractPublishEventWalk discovers every first-parent commit that adds a
// canonical event path, from ONE git invocation that also prints each commit's
// first parent and every added file's mode and blob id. Discovery is based on
// paths rather than raw YAML text: escaped but semantically equal subjects
// therefore cannot evade resolution.
//
// Renames are not detected: an event file that merely resembles a deleted one
// is still an added event. (With detection on, `--diff-filter=A` drops such a
// file, and the publication it establishes disappears from history.)
func contractPublishEventWalk(ctx context.Context, repoDir, authoritativeCommit string) ([]contractEventCommit, error) {
	raw, err := contractGitBytes(ctx, repoDir, maxContractEventWalkBytes,
		"log", "--first-parent", "--reverse", "--diff-merges=first-parent", "--no-renames",
		"--raw", "-z", "--no-abbrev", "--diff-filter=A", "--format=%x00%H %P%x00", authoritativeCommit)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: cannot walk contract publish events; run a2a sync: %w", ErrContractObjectUnavailable, err)
	}
	return parseContractEventWalk(raw)
}

// parseContractEventWalk reads the record stream of the log above, split on
// NUL. Per commit it is
//
//	""  "<oid> [<parent>...]"  ""  then, per added file,  ":<meta> A"  "<path>"
//
// where the first meta record is additionally preceded by a newline. Anything
// else is an error: a record that cannot be read is never skipped, because a
// skipped commit is a publication that silently never happened.
func parseContractEventWalk(raw []byte) ([]contractEventCommit, error) {
	if len(raw) == 0 {
		return []contractEventCommit{}, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("%w: publish event history is not NUL-terminated", ErrContractHistoryInvalid)
	}
	records := bytes.Split(raw[:len(raw)-1], []byte{0})
	commits := make([]contractEventCommit, 0)
	seen := make(map[string]struct{})
	for index := 0; index < len(records); {
		if len(records[index]) != 0 || index+1 >= len(records) {
			return nil, fmt.Errorf("%w: publish event history has a malformed commit record", ErrContractHistoryInvalid)
		}
		commit, parent, err := parseContractEventWalkHeader(string(records[index+1]))
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[commit]; duplicate {
			return nil, fmt.Errorf("%w: publish event history repeats commit %s", ErrContractHistoryInvalid, commit)
		}
		seen[commit] = struct{}{}
		index += 2
		if index < len(records) && len(records[index]) == 0 {
			index++ // git's own record terminator after the --format text under -z
		}
		entry := contractEventCommit{Commit: commit, Parent: parent}
		for index < len(records) && len(records[index]) != 0 {
			if index+1 >= len(records) {
				return nil, fmt.Errorf("%w: publish event history has a diff record without a path", ErrContractHistoryInvalid)
			}
			added, err := parseContractEventWalkEntry(string(records[index]), string(records[index+1]))
			if err != nil {
				return nil, err
			}
			index += 2
			if isContractEventPath(added.Path) {
				entry.Events = append(entry.Events, added)
			}
		}
		if len(entry.Events) == 0 {
			continue
		}
		// Git emits tree order, which is not byte order; the pipeline has
		// always visited a commit's events in sorted order.
		sort.Slice(entry.Events, func(i, j int) bool { return entry.Events[i].Path < entry.Events[j].Path })
		commits = append(commits, entry)
	}
	return commits, nil
}

func parseContractEventWalkHeader(record string) (commit, parent string, err error) {
	fields := strings.Split(record, " ")
	if len(fields) < 2 || !contractGitObjectID(fields[0]) {
		return "", "", fmt.Errorf("%w: publish event history has a malformed commit header", ErrContractHistoryInvalid)
	}
	parents := fields[1:]
	if len(parents) == 1 && parents[0] == "" {
		parents = nil // a root commit prints no parent
	}
	for _, candidate := range parents {
		if !contractGitObjectID(candidate) {
			return "", "", fmt.Errorf("%w: publish event history has a malformed parent id", ErrContractHistoryInvalid)
		}
	}
	if len(parents) > 0 {
		parent = strings.ToLower(parents[0])
	}
	return strings.ToLower(fields[0]), parent, nil
}

// parseContractEventWalkEntry reads one `:<src mode> <dst mode> <src oid> <dst
// oid> A` record and its path. Only additions can appear (--diff-filter=A), so
// the source side must be the null mode and object.
func parseContractEventWalkEntry(meta, name string) (contractGitTreeEntry, error) {
	fields := strings.Split(strings.TrimPrefix(meta, "\n"), " ")
	if len(fields) != 5 || !strings.HasPrefix(fields[0], ":") || name == "" ||
		fields[0] != ":000000" || fields[4] != "A" ||
		!contractGitObjectID(fields[2]) || strings.Trim(fields[2], "0") != "" || !contractGitObjectID(fields[3]) {
		return contractGitTreeEntry{}, fmt.Errorf("%w: publish event history has a malformed diff record", ErrContractHistoryInvalid)
	}
	mode := fields[1]
	var kind string
	switch mode {
	case "100644", "100755", "120000":
		kind = "blob"
	case "160000":
		kind = "commit"
	case "040000":
		kind = "tree"
	default:
		return contractGitTreeEntry{}, fmt.Errorf("%w: publish event history has an unknown file mode %q", ErrContractHistoryInvalid, mode)
	}
	return contractGitTreeEntry{Mode: mode, Kind: kind, OID: strings.ToLower(fields[3]), Path: name}, nil
}

func validateContractPublicationPair(descriptor contractHistoricalDescriptor, event contractHistoricalEvent, profile contract.DigestProfile) error {
	valid := descriptor.Schema == "envelope/v1" && event.Schema == "event/v1" && profile == contract.ProfileContractTreeV1 ||
		descriptor.Schema == "envelope/v2" && event.Schema == "event/v2" && profile == contract.ProfileContractSetV2
	if !valid {
		return fmt.Errorf("%w: unsupported descriptor/event/profile pairing %q/%q/%q", ErrContractHistoryInvalid, descriptor.Schema, event.Schema, profile)
	}
	return nil
}

func isContractEventPath(name string) bool {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[1] != "events" || len(parts[2]) != 4 || !strings.HasSuffix(parts[3], ".yaml") {
		return false
	}
	if _, err := NewLayout(parts[0]); err != nil {
		return false
	}
	for _, char := range parts[2] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func readHistoricalContractSnapshot(ctx context.Context, repoDir string, reader *contractGitBatchReader, descriptorPath string, establishment contractEstablishment) (HistoricalSnapshot, error) {
	contractRoot := path.Dir(descriptorPath)
	eventRaw := establishment.EventRaw
	if len(eventRaw) == 0 {
		eventBytes, err := reader.readBlob(ctx, contractGitTreeEntry{
			Mode: establishment.EventMode, Kind: "blob", OID: establishment.EventOID, Path: establishment.EventPath,
		}, contract.MaxFileBytes)
		if err != nil {
			return HistoricalSnapshot{}, err
		}
		eventRaw = eventBytes
	}
	treeObjectID := establishment.TreeObjectID
	descriptorFile := establishment.DescriptorFile
	descriptorFile.Path = contract.DescriptorPath
	descriptor, err := contract.ParseDescriptor(descriptorFile.Raw)
	if err != nil {
		return HistoricalSnapshot{}, fmt.Errorf("%w: descriptor at %s cannot be decoded: %w", ErrContractHistoryInvalid, establishment.CommitSHA, err)
	}
	if err := validateContractPublicationPair(establishment.Descriptor, establishment.Event, establishment.Profile); err != nil {
		return HistoricalSnapshot{}, err
	}
	roots := []string{path.Join(contractRoot, "schema"), path.Join(contractRoot, "fixtures")}
	if establishment.Profile == contract.ProfileContractSetV2 {
		roots = append(roots, path.Join(contractRoot, "artifacts"))
	}
	entries, err := contractGitListTree(ctx, repoDir, establishment.CommitSHA, true, roots...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return HistoricalSnapshot{}, ctxErr
		}
		return HistoricalSnapshot{}, fmt.Errorf("%w: cannot list historical contract tree: %w", ErrContractObjectUnavailable, err)
	}
	if len(entries) > contract.MaxExplicitFiles {
		return HistoricalSnapshot{}, fmt.Errorf("%w: historical carried tree has %d files", ErrContractBoundedResult, len(entries))
	}
	files := make([]ContractSnapshotFile, 0, len(entries)+1)
	files = append(files, descriptorFile)
	coreFiles := make([]contract.CandidateFile, 0, len(entries))
	aggregateBytes := 0
	if establishment.Profile == contract.ProfileContractSetV2 {
		aggregateBytes = len(descriptorFile.Raw)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return HistoricalSnapshot{}, err
		}
		prefix := contractRoot + "/"
		if !strings.HasPrefix(entry.Path, prefix) {
			return HistoricalSnapshot{}, fmt.Errorf("%w: Git listed a contract path outside its root", ErrContractHistoryInvalid)
		}
		relative := strings.TrimPrefix(entry.Path, prefix)
		kind := contractGitCandidateKind(entry)
		if kind != contract.CandidateRegular {
			return HistoricalSnapshot{}, fmt.Errorf("%w: %q has git mode/type %s/%s", ErrContractUnsafeEntry, relative, entry.Mode, entry.Kind)
		}
		raw, err := reader.readBlob(ctx, entry, contract.MaxFileBytes)
		if err != nil {
			return HistoricalSnapshot{}, err
		}
		aggregateBytes += len(raw)
		if aggregateBytes > contract.MaxAggregateBytes {
			return HistoricalSnapshot{}, fmt.Errorf("%w: historical carried aggregate exceeds %d bytes", ErrContractBoundedResult, contract.MaxAggregateBytes)
		}
		files = append(files, ContractSnapshotFile{
			Path: relative, Kind: kind, Mode: entry.Mode, Source: ContractCandidateSourceMirror,
			ObjectID: entry.OID, Raw: raw,
		})
		coreFiles = append(coreFiles, contract.CandidateFile{Path: relative, Kind: kind, Raw: raw})
	}
	set, issues := contract.ValidateCarriedSet(establishment.Profile, descriptor, contract.CandidateSnapshot{
		Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: descriptorFile.Raw},
		Files:      coreFiles,
	})
	if len(issues) != 0 {
		return HistoricalSnapshot{}, fmt.Errorf("%w: historical carried set is invalid: %v", ErrContractHistoryInvalid, issues)
	}
	if issues := set.VerifyDigest(establishment.Event.Digest); len(issues) != 0 {
		return HistoricalSnapshot{}, fmt.Errorf("%w: %v", ErrContractDigestMismatch, issues)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	descriptorVerification := ContractVerificationGitObjectAtPublishCommit
	if establishment.Profile == contract.ProfileContractSetV2 {
		descriptorVerification = ContractVerificationEventDigest
	}
	return HistoricalSnapshot{
		ContractID: establishment.Descriptor.ID, Version: establishment.Descriptor.Version,
		CommitSHA: establishment.CommitSHA, TreeObjectID: treeObjectID,
		DescriptorPath: descriptorPath, PublishEventPath: establishment.EventPath,
		PublishEventRaw: append([]byte(nil), eventRaw...), EventSchema: establishment.Event.Schema,
		DigestProfile: establishment.Profile, PublishedDigest: establishment.Event.Digest,
		AggregateVerification: ContractVerificationEventDigest, DescriptorVerification: descriptorVerification,
		Descriptor: descriptor, CarriedSet: set, Files: files,
	}, nil
}
