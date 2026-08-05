package space

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

const maxContractPublicationEstablishments = 256

// ContractPublicationRepository is the production authoritative-main and
// planning adapter. Refresh pins one origin/main object; every later read in
// that publication invocation proves the ref still names that object.
// ContractPublicationRepository is part of the public package API.
type ContractPublicationRepository struct {
	repoDir           string
	remoteURL         string
	manifestValidator ManifestValidator
	historyValidator  ContractHistoryDocumentValidator

	mu     sync.Mutex
	anchor string
}

// NewContractPublicationRepository is part of the public package API.
func NewContractPublicationRepository(repoDir, remoteURL string, manifestValidator ManifestValidator, historyValidator ContractHistoryDocumentValidator) (*ContractPublicationRepository, error) {
	if strings.TrimSpace(repoDir) == "" || strings.TrimSpace(remoteURL) == "" || manifestValidator == nil || historyValidator == nil {
		return nil, fmt.Errorf("%w: repository, remote and validators are required", ErrContractPublicationInvalid)
	}
	return &ContractPublicationRepository{
		repoDir: repoDir, remoteURL: remoteURL,
		manifestValidator: manifestValidator, historyValidator: historyValidator,
	}, nil
}

// RefreshContractPublicationMain is part of the public package API.
func (r *ContractPublicationRepository) RefreshContractPublicationMain(ctx context.Context) (string, error) {
	if r == nil {
		return "", ErrContractPublicationInvalid
	}
	if err := CloneOrFetch(ctx, r.repoDir, r.remoteURL); err != nil {
		return "", err
	}
	commit, err := contractGitResolveCommit(ctx, r.repoDir, contractAuthoritativeMainRef)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.anchor = commit
	r.mu.Unlock()
	return commit, nil
}

// ResolveContractPublicationTarget is part of the public package API.
func (r *ContractPublicationRepository) ResolveContractPublicationTarget(ctx context.Context, contractID, target string) (ContractPublicationCompletion, bool, error) {
	anchor, err := r.authoritativeAnchor(ctx)
	if err != nil {
		return ContractPublicationCompletion{}, false, err
	}
	history, err := r.publicationHistory(ctx, anchor, contractID)
	if err != nil {
		return ContractPublicationCompletion{}, false, err
	}
	for index := range history {
		if history[index].Version != target {
			continue
		}
		return ContractPublicationCompletion{
			Snapshot: history[index], PublishedBefore: publishedBefore(history, index),
		}, true, nil
	}
	return ContractPublicationCompletion{}, false, nil
}

// LookupContractPublicationIntent is part of the public package API.
func (r *ContractPublicationRepository) LookupContractPublicationIntent(ctx context.Context, contractID, intentKey string) (ContractPublicationIntentLookup, error) {
	anchor, err := r.authoritativeAnchor(ctx)
	if err != nil {
		return ContractPublicationIntentLookup{}, err
	}
	history, err := r.publicationHistory(ctx, anchor, contractID)
	if err != nil {
		return ContractPublicationIntentLookup{}, err
	}
	matches := make([]ContractPublicationCompletion, 0, 1)
	for index, snapshot := range history {
		var event contractHistoricalEvent
		if err := yaml.Unmarshal(snapshot.PublishEventRaw, &event); err != nil {
			return ContractPublicationIntentLookup{}, fmt.Errorf("%w: decode historical publication event", ErrContractHistoryInvalid)
		}
		if event.Schema == "event/v2" && event.Publication.IntentKey == intentKey {
			matches = append(matches, ContractPublicationCompletion{
				Snapshot: snapshot, PublishedBefore: publishedBefore(history, index),
			})
		}
	}
	if matches == nil {
		matches = []ContractPublicationCompletion{}
	}
	return ContractPublicationIntentLookup{Matches: matches, Exhaustive: true}, nil
}

// ReadContractPublicationPlanningContext is part of the public package API.
func (r *ContractPublicationRepository) ReadContractPublicationPlanningContext(ctx context.Context, request ContractPublicationPlanningRequest) (ContractPublicationPlanningContext, error) {
	if r == nil || request.System == "" || request.ContractID == "" || request.Candidate.ContractID() != request.ContractID {
		return ContractPublicationPlanningContext{}, ErrContractPublicationInvalid
	}
	anchor := request.AuthoritativeCommit
	if anchor == "" {
		var err error
		anchor, err = contractGitResolveCommit(ctx, r.repoDir, contractAuthoritativeMainRef)
		if err != nil {
			return ContractPublicationPlanningContext{}, err
		}
	} else {
		pinned, err := r.authoritativeAnchor(ctx)
		if err != nil || pinned != anchor {
			return ContractPublicationPlanningContext{}, fmt.Errorf("%w: planning anchor changed", ErrOperationConflict)
		}
	}
	manifestFile, err := contractGitReadPath(ctx, r.repoDir, anchor, "space.yaml", maxConfigBytes)
	if err != nil {
		return ContractPublicationPlanningContext{}, err
	}
	manifest, err := LoadManifest(ctx, manifestFile.Raw, r.manifestValidator)
	if err != nil {
		return ContractPublicationPlanningContext{}, err
	}
	history, err := r.publicationHistory(ctx, anchor, request.ContractID)
	if err != nil {
		return ContractPublicationPlanningContext{}, err
	}
	published := make([]contract.PublishedContract, 0, len(history))
	for _, snapshot := range history {
		published = append(published, historicalPublishedContract(snapshot))
	}
	root, err := canonicalContractPublicationRoot(request.System, request.ContractID)
	if err != nil {
		return ContractPublicationPlanningContext{}, err
	}
	prior := selectPublicationMutationBaseline(history, request.Candidate.CurrentVersion())
	preconditions := make(map[string]MutationPrecondition)
	if prior != nil {
		descriptor := prior.file(contract.DescriptorPath)
		if descriptor.Path == "" || !validContractPublicationGitMode(descriptor.Mode) || len(descriptor.Raw) == 0 {
			return ContractPublicationPlanningContext{}, fmt.Errorf("%w: prior descriptor precondition is invalid", ErrContractHistoryInvalid)
		}
		preconditions[contract.DescriptorPath] = MutationPrecondition{Digest: artifact.Digest(descriptor.Raw), Mode: descriptor.Mode}
		for _, entry := range prior.CarriedSet.Entries {
			file := prior.file(entry.Path)
			if file.Path == "" || (file.Mode != "100644" && file.Mode != "100755") {
				return ContractPublicationPlanningContext{}, fmt.Errorf("%w: prior sidecar mode is invalid", ErrContractHistoryInvalid)
			}
			preconditions[entry.Path] = MutationPrecondition{Digest: prior.CarriedSet.PerFileDigest[entry.Path], Mode: file.Mode}
		}
	}
	assertion := ""
	if generated := request.Candidate.Descriptor().GeneratedFrom; generated != nil {
		assertion = generated.SourceDigest
	}
	currentDescriptor, err := contractDescriptorDigestAt(ctx, r.repoDir, anchor, path.Join(root, contract.DescriptorPath))
	if err != nil {
		return ContractPublicationPlanningContext{}, err
	}
	return ContractPublicationPlanningContext{
		AuthoringFloor: manifest.MinBinaryVersion, ContractRoot: root, BaseCommitSHA: anchor,
		Published: published, SourceDigestAssertion: assertion,
		Warnings: []contract.Finding{}, Violations: []contract.Finding{}, PriorDeclaredSidecars: preconditions,
		CurrentDescriptorDigest: currentDescriptor,
	}, nil
}

// contractDescriptorDigestAt reads the descriptor as authoritative main
// carries it, returning "" when main has none. Absence is a legitimate state
// (nothing has landed at the contract root yet), so it is reported rather than
// raised — unlike contractGitReadPath, which treats a missing path as invalid
// history because every caller there is walking a commit that must have one.
func contractDescriptorDigestAt(ctx context.Context, repoDir, commit, name string) (string, error) {
	entry, found, err := contractGitExactTreeEntry(ctx, repoDir, commit, name)
	if err != nil || !found {
		return "", err
	}
	if contractGitCandidateKind(entry) != contract.CandidateRegular {
		return "", fmt.Errorf("%w: %q on main is not a regular file", ErrContractUnsafeEntry, name)
	}
	raw, err := contractGitReadBlob(ctx, repoDir, entry, contract.MaxFileBytes)
	if err != nil {
		return "", err
	}
	return artifact.Digest(raw), nil
}

func (r *ContractPublicationRepository) authoritativeAnchor(ctx context.Context) (string, error) {
	r.mu.Lock()
	anchor := r.anchor
	r.mu.Unlock()
	if !validRemoteRecoveryOID(anchor) {
		return "", fmt.Errorf("%w: authoritative main is not pinned", ErrOperationConflict)
	}
	current, err := contractGitResolveCommit(ctx, r.repoDir, contractAuthoritativeMainRef)
	if err != nil || current != anchor {
		return "", fmt.Errorf("%w: authoritative main changed during publication", ErrOperationConflict)
	}
	return anchor, nil
}

func (r *ContractPublicationRepository) publicationHistory(ctx context.Context, anchor, contractID string) ([]HistoricalSnapshot, error) {
	current, err := contractGitResolveCommit(ctx, r.repoDir, contractAuthoritativeMainRef)
	if err != nil || current != anchor {
		return nil, fmt.Errorf("%w: publication history anchor changed", ErrOperationConflict)
	}
	history, err := publicationHistoryAt(ctx, r.repoDir, anchor, contractID, r.historyValidator)
	if err != nil {
		return nil, err
	}
	current, err = contractGitResolveCommit(ctx, r.repoDir, contractAuthoritativeMainRef)
	if err != nil || current != anchor {
		return nil, fmt.Errorf("%w: publication history anchor changed", ErrOperationConflict)
	}
	return history, nil
}

func publicationHistoryAt(ctx context.Context, repoDir, anchor, contractID string, validator ContractHistoryDocumentValidator) ([]HistoricalSnapshot, error) {
	commits, err := contractPublishEventHistory(ctx, repoDir, anchor)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0)
	seen := make(map[string]struct{})
	for _, commit := range commits {
		parent, err := contractGitFirstParent(ctx, repoDir, commit)
		if err != nil {
			return nil, err
		}
		paths, err := contractGitAddedPaths(ctx, repoDir, commit, parent)
		if err != nil {
			return nil, err
		}
		sort.Strings(paths)
		for _, eventPath := range paths {
			if !isContractEventPath(eventPath) {
				continue
			}
			file, err := contractGitReadPath(ctx, repoDir, commit, eventPath, contract.MaxFileBytes)
			if err != nil {
				return nil, err
			}
			var event contractHistoricalEvent
			if yaml.Unmarshal(file.Raw, &event) != nil || event.Subject != contractID || event.Transition != "publish" {
				continue
			}
			// The standing-artifact lifecycle uses a versionless publish event
			// when a contract descriptor first enters the space. Only P6's
			// version-bearing publish events establish immutable versions.
			if event.Version == "" {
				continue
			}
			canonical, err := version.Canonical(event.Version)
			if err != nil || canonical != event.Version {
				return nil, fmt.Errorf("%w: publication event has invalid version", ErrContractHistoryInvalid)
			}
			if _, duplicate := seen[event.Version]; duplicate {
				return nil, fmt.Errorf("%w: duplicate publication version %s", ErrContractHistoryInvalid, event.Version)
			}
			seen[event.Version] = struct{}{}
			versions = append(versions, event.Version)
			if len(versions) > maxContractPublicationEstablishments {
				return nil, fmt.Errorf("%w: contract has more than %d publications", ErrContractBoundedResult, maxContractPublicationEstablishments)
			}
		}
	}
	history := make([]HistoricalSnapshot, 0, len(versions))
	for _, publishedVersion := range versions {
		snapshot, err := resolveContractVersionAt(ctx, repoDir, anchor, contractID, publishedVersion, validator)
		if err != nil {
			return nil, err
		}
		history = append(history, snapshot)
	}
	return history, nil
}

// visitContractVersionsAt is the streaming historical read used by projection
// callers. It walks the authoritative first-parent event and descriptor
// history once, then materializes at most one exact immutable package at a
// time. Raw byte views are scrubbed immediately after the visitor returns.
func visitContractVersionsAt(ctx context.Context, repoDir, anchor, contractID string, requestedVersions []string, validator ContractHistoryDocumentValidator, visit func(*HistoricalResolution) error) error {
	parsedID, err := artifact.ParseID(contractID)
	if err != nil || parsedID.Prefix != "XC" || parsedID.Class != artifact.ClassStanding ||
		!validRemoteRecoveryOID(anchor) || validator == nil || visit == nil {
		return ErrContractInvalidReference
	}
	layout, err := NewLayout(parsedID.System)
	if err != nil {
		return ErrContractInvalidReference
	}
	descriptorPath := layout.ProvidesContract(parsedID.Slug)
	descriptorHistory, err := contractDescriptorHistory(ctx, repoDir, anchor, descriptorPath)
	if err != nil {
		return err
	}
	eventHistory, err := contractPublishEventHistory(ctx, repoDir, anchor)
	if err != nil {
		return err
	}
	requested := make(map[string]struct{}, len(requestedVersions))
	for _, requestedVersion := range requestedVersions {
		requested[requestedVersion] = struct{}{}
	}
	establishments := make(map[string][]contractEstablishment, len(requestedVersions))

	for _, commitSHA := range eventHistory {
		if err := ctx.Err(); err != nil {
			return err
		}
		parent, err := contractGitFirstParent(ctx, repoDir, commitSHA)
		if err != nil {
			return err
		}
		paths, err := contractGitAddedPaths(ctx, repoDir, commitSHA, parent)
		if err != nil {
			return err
		}
		sort.Strings(paths)
		for _, eventPath := range paths {
			if !isContractEventPath(eventPath) {
				continue
			}
			file, err := contractGitReadPath(ctx, repoDir, commitSHA, eventPath, contract.MaxFileBytes)
			if err != nil {
				return err
			}
			var event contractHistoricalEvent
			if yaml.Unmarshal(file.Raw, &event) != nil || event.Subject != contractID || event.Transition != "publish" {
				continue
			}
			if _, wanted := requested[event.Version]; !wanted {
				continue
			}
			establishments[event.Version] = append(establishments[event.Version], contractEstablishment{
				CommitSHA: commitSHA, EventPath: eventPath,
			})
		}
	}

	for _, requestedVersion := range requestedVersions {
		if err := ctx.Err(); err != nil {
			return err
		}
		result := HistoricalResolution{Version: requestedVersion}
		switch candidates := establishments[requestedVersion]; {
		case len(candidates) == 0:
			result.Err = ErrContractNotPublished
		case len(candidates) != 1:
			result.Err = fmt.Errorf("%w: duplicate establishment", ErrContractHistoryInvalid)
		default:
			candidate, err := validateHistoricalContractEstablishment(ctx, repoDir, descriptorPath, contractID, requestedVersion, descriptorHistory, candidates[0], validator)
			if err != nil {
				result.Err = err
				break
			}
			result.Snapshot, result.Err = readHistoricalContractSnapshot(ctx, repoDir, descriptorPath, candidate)
		}
		if result.Err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
		}
		visitErr := func() error {
			defer releaseHistoricalResolution(&result)
			return visit(&result)
		}()
		if visitErr != nil {
			return visitErr
		}
	}
	return nil
}

func validateHistoricalContractEstablishment(
	ctx context.Context,
	repoDir, descriptorPath, contractID, requestedVersion string,
	descriptorHistory map[string]struct{},
	candidate contractEstablishment,
	validator ContractHistoryDocumentValidator,
) (contractEstablishment, error) {
	eventFile, err := contractGitReadPath(ctx, repoDir, candidate.CommitSHA, candidate.EventPath, contract.MaxFileBytes)
	if err != nil {
		return contractEstablishment{}, err
	}
	var event contractHistoricalEvent
	if err := yaml.Unmarshal(eventFile.Raw, &event); err != nil || event.Subject != contractID || event.Version != requestedVersion || event.Transition != "publish" {
		return contractEstablishment{}, fmt.Errorf("%w: publish event identity mismatch", ErrContractHistoryInvalid)
	}
	parts := strings.Split(candidate.EventPath, "/")
	owner := parts[0]
	parsedID, parseErr := artifact.ParseID(contractID)
	if parseErr != nil || event.Actor.System != owner || owner != parsedID.System || strings.TrimSuffix(path.Base(candidate.EventPath), ".yaml") != event.Event {
		return contractEstablishment{}, fmt.Errorf("%w: publish event %q has mismatched owner, actor, or event id", ErrContractHistoryInvalid, candidate.EventPath)
	}
	profile, issues := contract.ResolveDigestProfile(event.Schema, event.DigestProfile)
	if len(issues) != 0 {
		return contractEstablishment{}, fmt.Errorf("%w: publish event %q has invalid profile: %v", ErrContractHistoryInvalid, candidate.EventPath, issues)
	}
	if event.Schema == "event/v2" {
		if issues := contract.ValidatePublicationIntent(event.Publication); len(issues) != 0 {
			return contractEstablishment{}, fmt.Errorf("%w: publish event %q has invalid publication identity: %v", ErrContractHistoryInvalid, candidate.EventPath, issues)
		}
	}
	if _, changed := descriptorHistory[candidate.CommitSHA]; !changed {
		return contractEstablishment{}, fmt.Errorf("%w: matching event does not change descriptor", ErrContractHistoryInvalid)
	}
	currentFile, err := contractGitReadPath(ctx, repoDir, candidate.CommitSHA, descriptorPath, contract.MaxFileBytes)
	if err != nil {
		return contractEstablishment{}, err
	}
	current, err := parseHistoricalDescriptor(currentFile.Raw)
	if err != nil || current.ID != contractID || current.Version != requestedVersion {
		return contractEstablishment{}, fmt.Errorf("%w: descriptor identity mismatch", ErrContractHistoryInvalid)
	}
	parent, err := contractGitFirstParent(ctx, repoDir, candidate.CommitSHA)
	if err != nil {
		return contractEstablishment{}, err
	}
	if parent != "" {
		parentFile, parentErr := contractGitReadPath(ctx, repoDir, parent, descriptorPath, contract.MaxFileBytes)
		if parentErr == nil {
			prior, parseErr := parseHistoricalDescriptor(parentFile.Raw)
			if parseErr != nil || prior.Version == requestedVersion {
				return contractEstablishment{}, fmt.Errorf("%w: invalid prior descriptor", ErrContractHistoryInvalid)
			}
		} else if !errors.Is(parentErr, ErrContractHistoryInvalid) {
			return contractEstablishment{}, parentErr
		}
	}
	if err := validateContractPublicationPair(current, event, profile); err != nil {
		return contractEstablishment{}, err
	}
	if err := validator.ValidateHistoricalContractDocuments(ctx, ContractHistoryDocuments{
		Descriptor: ContractHistoryDocument{Path: descriptorPath, Schema: current.Schema, Raw: currentFile.Raw},
		PublishEvent: ContractHistoryDocument{
			Path: candidate.EventPath, Schema: event.Schema, Raw: eventFile.Raw,
		},
	}); err != nil {
		return contractEstablishment{}, fmt.Errorf("%w: canonical document validation failed: %w", ErrContractHistoryInvalid, err)
	}
	candidate.Descriptor = current
	candidate.EventRaw = eventFile.Raw
	candidate.Event = event
	candidate.Profile = profile
	return candidate, nil
}

// releaseHistoricalResolution invalidates the borrowed raw-byte views shared
// by a visitor and the resolver. Metadata remains readable, but no batch call
// can accidentally keep every historical package live after callbacks return.
func releaseHistoricalResolution(result *HistoricalResolution) {
	if result == nil {
		return
	}
	result.Snapshot.PublishEventRaw = nil
	for index := range result.Snapshot.Files {
		result.Snapshot.Files[index].Raw = nil
	}
	for name := range result.Snapshot.CarriedSet.Bytes {
		delete(result.Snapshot.CarriedSet.Bytes, name)
	}
}

func resolveContractVersionAt(ctx context.Context, repoDir, anchor, contractID, requestedVersion string, validator ContractHistoryDocumentValidator) (HistoricalSnapshot, error) {
	parsedID, err := artifact.ParseID(contractID)
	canonical, versionErr := version.Canonical(requestedVersion)
	if err != nil || parsedID.Prefix != "XC" || parsedID.Class != artifact.ClassStanding ||
		versionErr != nil || canonical != requestedVersion || !validRemoteRecoveryOID(anchor) || validator == nil {
		return HistoricalSnapshot{}, ErrContractInvalidReference
	}
	var owned HistoricalSnapshot
	err = visitContractVersionsAt(ctx, repoDir, anchor, contractID, []string{requestedVersion}, validator, func(result *HistoricalResolution) error {
		if result.Err != nil {
			return result.Err
		}
		owned = cloneHistoricalSnapshot(result.Snapshot)
		return nil
	})
	return owned, err
}

func bytesClone(raw []byte) []byte { return append([]byte(nil), raw...) }

func cloneHistoricalSnapshot(snapshot HistoricalSnapshot) HistoricalSnapshot {
	clone := snapshot
	clone.PublishEventRaw = bytesClone(snapshot.PublishEventRaw)
	clone.Files = make([]ContractSnapshotFile, len(snapshot.Files))
	for index, file := range snapshot.Files {
		clone.Files[index] = cloneContractSnapshotFile(file)
	}
	clone.Descriptor = cloneContractDescriptor(snapshot.Descriptor)
	clone.CarriedSet = snapshot.CarriedSet
	clone.CarriedSet.Descriptor = cloneContractDescriptor(snapshot.CarriedSet.Descriptor)
	clone.CarriedSet.Entries = append([]contract.Entry(nil), snapshot.CarriedSet.Entries...)
	clone.CarriedSet.PerFileDigest = make(map[string]string, len(snapshot.CarriedSet.PerFileDigest))
	for name, digest := range snapshot.CarriedSet.PerFileDigest {
		clone.CarriedSet.PerFileDigest[name] = digest
	}
	clone.CarriedSet.Bytes = make(map[string][]byte, len(snapshot.CarriedSet.Bytes))
	for name, raw := range snapshot.CarriedSet.Bytes {
		clone.CarriedSet.Bytes[name] = bytesClone(raw)
	}
	return clone
}

func cloneContractDescriptor(descriptor contract.Descriptor) contract.Descriptor {
	clone := descriptor
	clone.Artifacts = append([]contract.Entry(nil), descriptor.Artifacts...)
	if descriptor.GeneratedFrom != nil {
		generated := *descriptor.GeneratedFrom
		clone.GeneratedFrom = &generated
	}
	return clone
}

func publishedBefore(history []HistoricalSnapshot, index int) []contract.PublishedContract {
	out := make([]contract.PublishedContract, 0, index)
	for _, snapshot := range history[:index] {
		out = append(out, historicalPublishedContract(snapshot))
	}
	return out
}

func historicalPublishedContract(snapshot HistoricalSnapshot) contract.PublishedContract {
	modes := make(map[string]string, len(snapshot.Files))
	for _, file := range snapshot.Files {
		modes[file.Path] = file.Mode
	}
	return contract.PublishedContract{
		Version: snapshot.Version, DescriptorRaw: append([]byte(nil), snapshot.file(contract.DescriptorPath).Raw...),
		Set: snapshot.CarriedSet, Modes: modes,
	}
}

func selectPublicationMutationBaseline(history []HistoricalSnapshot, currentVersion string) *HistoricalSnapshot {
	var selected *HistoricalSnapshot
	for index := range history {
		if history[index].Version == currentVersion {
			copy := history[index]
			return &copy
		}
		if selected == nil {
			copy := history[index]
			selected = &copy
			continue
		}
		older, err := version.OlderThan(selected.Version, history[index].Version)
		if err == nil && older {
			copy := history[index]
			selected = &copy
		}
	}
	return selected
}

var _ ContractPublicationMain = (*ContractPublicationRepository)(nil)
var _ ContractPublicationPlanningReader = (*ContractPublicationRepository)(nil)
