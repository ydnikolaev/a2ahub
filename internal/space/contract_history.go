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
	CommitSHA  string
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
		if !contractGitObjectID(field) {
			return nil, fmt.Errorf("%w: descriptor history contains a non-object id", ErrContractHistoryInvalid)
		}
		verified, err := contractGitResolveCommit(ctx, repoDir, field)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[verified]; duplicate {
			return nil, fmt.Errorf("%w: descriptor history repeats commit %s", ErrContractHistoryInvalid, verified)
		}
		seen[verified] = struct{}{}
	}
	return seen, nil
}

// contractPublishEventHistory discovers every first-parent commit that adds a
// canonical event path. Discovery is based on paths rather than raw YAML text:
// escaped but semantically equal subjects therefore cannot evade resolution.
// The one bounded Git listing is the supported-history ceiling; event decode
// and target filtering happen only for the returned commits.
func contractPublishEventHistory(ctx context.Context, repoDir, authoritativeCommit string) ([]string, error) {
	raw, err := contractGitBytes(ctx, repoDir, maxContractGitListingBytes,
		"log", "--first-parent", "--reverse", "--diff-merges=first-parent", "--format=%x00%H%x00",
		"--name-only", "--diff-filter=A", "-z", authoritativeCommit)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: cannot walk contract publish events; run a2a sync: %w", ErrContractObjectUnavailable, err)
	}
	records := bytes.Split(raw, []byte{0})
	commits := make([]string, 0)
	seen := make(map[string]struct{})
	current := ""
	for index := 0; index < len(records); index++ {
		if len(records[index]) == 0 && index+2 < len(records) && len(records[index+2]) == 0 && contractGitObjectID(string(records[index+1])) {
			current = strings.ToLower(string(records[index+1]))
			index += 2
			continue
		}
		name := strings.TrimPrefix(string(records[index]), "\n")
		if name == "" {
			continue
		}
		if current == "" {
			return nil, fmt.Errorf("%w: publish event history contains a path without a commit", ErrContractHistoryInvalid)
		}
		if !isContractEventPath(name) {
			continue
		}
		if _, duplicate := seen[current]; duplicate {
			continue
		}
		seen[current] = struct{}{}
		commits = append(commits, current)
	}
	return commits, nil
}

func validateContractPublicationPair(descriptor contractHistoricalDescriptor, event contractHistoricalEvent, profile contract.DigestProfile) error {
	valid := descriptor.Schema == "envelope/v1" && event.Schema == "event/v1" && profile == contract.ProfileContractTreeV1 ||
		descriptor.Schema == "envelope/v2" && event.Schema == "event/v2" && profile == contract.ProfileContractSetV2
	if !valid {
		return fmt.Errorf("%w: unsupported descriptor/event/profile pairing %q/%q/%q", ErrContractHistoryInvalid, descriptor.Schema, event.Schema, profile)
	}
	return nil
}

func contractGitFirstParent(ctx context.Context, repoDir, commitSHA string) (string, error) {
	raw, err := contractGitBytes(ctx, repoDir, maxTreeCommitMetadataBytes, "rev-list", "--parents", "-n", "1", commitSHA)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("%w: cannot inspect commit parents; run a2a sync: %w", ErrContractObjectUnavailable, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 || strings.ToLower(fields[0]) != commitSHA {
		return "", fmt.Errorf("%w: malformed commit parent record", ErrContractHistoryInvalid)
	}
	if len(fields) == 1 {
		return "", nil
	}
	return contractGitResolveCommit(ctx, repoDir, fields[1])
}

func contractPublishEventAtCommit(ctx context.Context, repoDir, commitSHA, firstParent, contractID, version string) (string, []byte, contractHistoricalEvent, contract.DigestProfile, bool, error) {
	added, err := contractGitAddedPaths(ctx, repoDir, commitSHA, firstParent)
	if err != nil {
		return "", nil, contractHistoricalEvent{}, "", false, err
	}
	eventPaths := make([]string, 0)
	for _, addedPath := range added {
		if isContractEventPath(addedPath) {
			eventPaths = append(eventPaths, addedPath)
		}
	}
	sort.Strings(eventPaths)
	type match struct {
		path    string
		raw     []byte
		event   contractHistoricalEvent
		profile contract.DigestProfile
	}
	matches := make([]match, 0, 1)
	for _, eventPath := range eventPaths {
		file, err := contractGitReadPath(ctx, repoDir, commitSHA, eventPath, contract.MaxFileBytes)
		if err != nil {
			return "", nil, contractHistoricalEvent{}, "", false, err
		}
		var event contractHistoricalEvent
		if err := yaml.Unmarshal(file.Raw, &event); err != nil {
			// Invalid append-only events stay visible to validators, but cannot
			// establish or globally block an unrelated contract publication.
			continue
		}
		if event.Subject != contractID || event.Version != version || event.Transition != "publish" {
			continue
		}
		parts := strings.Split(eventPath, "/")
		owner := parts[0]
		if parsedID, parseErr := artifact.ParseID(contractID); parseErr != nil || parsedID.System != owner || event.Actor.System != owner || strings.TrimSuffix(path.Base(eventPath), ".yaml") != event.Event {
			return "", nil, contractHistoricalEvent{}, "", false, fmt.Errorf("%w: publish event %q has mismatched owner, actor, or event id", ErrContractHistoryInvalid, eventPath)
		}
		profile, issues := contract.ResolveDigestProfile(event.Schema, event.DigestProfile)
		if len(issues) != 0 {
			return "", nil, contractHistoricalEvent{}, "", false, fmt.Errorf("%w: publish event %q has invalid profile: %v", ErrContractHistoryInvalid, eventPath, issues)
		}
		if event.Schema == "event/v2" {
			if issues := contract.ValidatePublicationIntent(event.Publication); len(issues) != 0 {
				return "", nil, contractHistoricalEvent{}, "", false, fmt.Errorf("%w: publish event %q has invalid publication identity: %v", ErrContractHistoryInvalid, eventPath, issues)
			}
		}
		matches = append(matches, match{path: eventPath, raw: file.Raw, event: event, profile: profile})
	}
	if len(matches) == 0 {
		return "", nil, contractHistoricalEvent{}, "", false, nil
	}
	if len(matches) != 1 {
		return "", nil, contractHistoricalEvent{}, "", false, fmt.Errorf("%w: commit %s contains %d matching publish events", ErrContractHistoryInvalid, commitSHA, len(matches))
	}
	got := matches[0]
	return got.path, append([]byte(nil), got.raw...), got.event, got.profile, true, nil
}

func contractGitAddedPaths(ctx context.Context, repoDir, commitSHA, firstParent string) ([]string, error) {
	args := []string{"diff-tree", "--no-commit-id", "--diff-filter=A", "--name-only", "-r", "-z"}
	if firstParent == "" {
		args = append(args, "--root", commitSHA, "--")
	} else {
		args = append(args, firstParent, commitSHA, "--")
	}
	raw, err := contractGitBytes(ctx, repoDir, maxContractGitListingBytes, args...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: cannot inspect establishment commit tree: %w", ErrContractObjectUnavailable, err)
	}
	return parseContractGitNames(raw)
}

func parseContractGitNames(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, fmt.Errorf("%w: Git returned a non-NUL-terminated path list", ErrContractHistoryInvalid)
	}
	records := bytes.Split(raw[:len(raw)-1], []byte{0})
	paths := make([]string, 0, len(records))
	for _, record := range records {
		if len(record) == 0 || bytes.ContainsRune(record, '\x00') {
			return nil, fmt.Errorf("%w: Git returned an invalid empty path", ErrContractHistoryInvalid)
		}
		paths = append(paths, string(record))
	}
	return paths, nil
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

func readHistoricalContractSnapshot(ctx context.Context, repoDir, descriptorPath string, establishment contractEstablishment) (HistoricalSnapshot, error) {
	contractRoot := path.Dir(descriptorPath)
	treeObjectID, err := contractGitTreeObjectID(ctx, repoDir, establishment.CommitSHA, contractRoot)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
	descriptorFile, err := contractGitReadPath(ctx, repoDir, establishment.CommitSHA, descriptorPath, contract.MaxFileBytes)
	if err != nil {
		return HistoricalSnapshot{}, err
	}
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
		raw, err := contractGitReadBlob(ctx, repoDir, entry, contract.MaxFileBytes)
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
		PublishEventRaw: append([]byte(nil), establishment.EventRaw...), EventSchema: establishment.Event.Schema,
		DigestProfile: establishment.Profile, PublishedDigest: establishment.Event.Digest,
		AggregateVerification: ContractVerificationEventDigest, DescriptorVerification: descriptorVerification,
		Descriptor: descriptor, CarriedSet: set, Files: files,
	}, nil
}
