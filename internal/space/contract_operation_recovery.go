package space

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"gopkg.in/yaml.v3"
)

type contractPublicationRecoveryHost interface {
	ListContractPublishHeads(context.Context, string, string, string, host.Credential) (host.RemoteHeadListing, error)
	ReadRemoteRecoveryCommit(context.Context, string, string, host.Repo, string, host.Credential) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error)
	ReadRemoteRecoveryTreeFiles(context.Context, string, string, []string) (map[string]host.RemoteTreeFile, error)
	ListRemoteRecoveryTreeFiles(context.Context, string, string, []string) (map[string]host.RemoteTreeFile, error)
	FindPRByHeadBranch(context.Context, host.FindPRRequest) (*host.PRInfo, error)
	OpenPR(context.Context, host.OpenPRRequest) (host.PRInfo, error)
	EnableAutoMerge(context.Context, host.EnableAutoMergeRequest) (host.MergeMethod, error)
}

type verifiedContractPublicationHead struct {
	proof  ContractPublicationHeadProof
	record RecoveryV1
}

// ContractPublicationRecovery is the production remote-head proof and PR-only
// repair adapter. A repair is accepted only for a proof minted by the same
// adapter after reading the remote commit and its complete resulting tree.
// ContractPublicationRecovery is part of the public package API.
type ContractPublicationRecovery struct {
	host              contractPublicationRecoveryHost
	repoDir           string
	remoteURL         string
	repository        host.Repo
	credential        host.Credential
	manifestValidator ManifestValidator
	historyValidator  ContractHistoryDocumentValidator
	checker           contract.CompatibilityChecker
	// binaryVersion is the RUNNING binary's own version — the same value
	// space.NewWriteFunnel receives at the cmd/a2a seam (funnelBinaryVersion
	// there, threaded here via internal/contractwiring's PublicationDependencies.
	// Binary). It is what a recomputed publication plan is compared against a
	// recovered head's own RecoveryV1.MintedByVersion with, so a verifier can
	// tell "this head was minted by a different binary" apart from "this head
	// is tampered".
	binaryVersion string

	mu       sync.Mutex
	verified map[string]verifiedContractPublicationHead
}

// ContractPublicationRecoveryValidation is part of the public package API.
type ContractPublicationRecoveryValidation struct {
	ManifestValidator ManifestValidator
	HistoryValidator  ContractHistoryDocumentValidator
	Compatibility     contract.CompatibilityChecker
}

// NewContractPublicationRecovery is part of the public package API.
//
// binaryVersion is the RUNNING binary's own version, mirroring
// NewWriteFunnel(host, validator, binaryVersion)'s trailing positional
// parameter — a plain non-empty value is required, but (unlike a manifest's
// min_binary_version) it is NOT required to be canonical semver, because a
// non-release binary can carry a non-semver stamp such as "dev" (see
// cmd/a2a's own `version` default) and that is a legitimate running binary,
// not an invalid one.
func NewContractPublicationRecovery(h contractPublicationRecoveryHost, repoDir, remoteURL string, repository host.Repo, credential host.Credential, binaryVersion string, validation ContractPublicationRecoveryValidation) (*ContractPublicationRecovery, error) {
	if h == nil || strings.TrimSpace(repoDir) == "" || strings.TrimSpace(remoteURL) == "" || repository.Owner == "" || repository.Name == "" ||
		strings.TrimSpace(binaryVersion) == "" ||
		validation.ManifestValidator == nil || validation.HistoryValidator == nil || validation.Compatibility == nil {
		return nil, fmt.Errorf("%w: recovery host, repository, remote and binary version are required", ErrContractPublicationInvalid)
	}
	return &ContractPublicationRecovery{
		host: h, repoDir: repoDir, remoteURL: remoteURL, repository: repository, credential: credential,
		manifestValidator: validation.ManifestValidator, historyValidator: validation.HistoryValidator, checker: validation.Compatibility,
		binaryVersion: binaryVersion,
		verified:      make(map[string]verifiedContractPublicationHead),
	}, nil
}

// ProbeContractPublicationHeads is part of the public package API.
func (r *ContractPublicationRecovery) ProbeContractPublicationHeads(ctx context.Context, request ContractPublicationHeadProbeRequest) (ContractPublicationHeadListing, error) {
	if r == nil || request.MaximumHeads != hostContractPublishHeadLimit || request.NamespacePrefix == "" {
		return ContractPublicationHeadListing{}, ErrContractPublicationInvalid
	}
	listing, err := r.host.ListContractPublishHeads(ctx, r.repoDir, r.remoteURL, request.NamespacePrefix, r.credential)
	if err != nil {
		return ContractPublicationHeadListing{}, err
	}
	if !listing.Exhaustive {
		return ContractPublicationHeadListing{Heads: []ContractPublicationHeadProof{}, Observed: listing.Observed, Exhaustive: false}, nil
	}
	proofs := make([]ContractPublicationHeadProof, 0, len(listing.Heads))
	verified := make(map[string]verifiedContractPublicationHead, len(listing.Heads))
	for _, remoteHead := range listing.Heads {
		proof, record, relevant, err := r.proveHead(ctx, request, remoteHead)
		if err != nil {
			return ContractPublicationHeadListing{}, err
		}
		if !relevant {
			continue
		}
		proofs = append(proofs, proof)
		verified[contractPublicationProofKey(proof)] = verifiedContractPublicationHead{proof: proof, record: record}
	}
	r.mu.Lock()
	r.verified = verified
	r.mu.Unlock()
	return ContractPublicationHeadListing{Heads: proofs, Observed: len(proofs), Exhaustive: true}, nil
}

func (r *ContractPublicationRecovery) proveHead(ctx context.Context, request ContractPublicationHeadProbeRequest, remoteHead host.RemoteHead) (ContractPublicationHeadProof, RecoveryV1, bool, error) {
	repository, branch, head, parent, message, authorName, authorEmail, changes, exists, err := r.host.ReadRemoteRecoveryCommit(
		ctx, r.repoDir, r.remoteURL, r.repository, remoteHead.Branch, r.credential,
	)
	if err != nil {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, err
	}
	if !exists || repository != r.repository || branch != remoteHead.Branch || head != remoteHead.SHA {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, remoteRecoveryConflict("publication-head-identity")
	}
	trailers, _, err := parseRemoteRecoveryTrailers(message)
	if err != nil {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, err
	}
	record, err := DecodeRecoveryV1Trailer(trailers.recoveryV1, trailers.recoveryDigest)
	if err != nil || trailers.operationKey != record.OperationKey || trailers.planDigest != record.PlanDigest ||
		trailers.target != record.Target || trailers.preparedDigest != record.PreparedDigest ||
		record.HeadBranch != branch || record.Repository != repositoryIdentity(r.repository) || !validRemoteRecoveryOID(parent) {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, remoteRecoveryConflict("publication-recovery-metadata")
	}
	contractID, target, _ := strings.Cut(record.Target, "@")
	if record.System != request.System {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, remoteRecoveryConflict("publication-recovery-system")
	}
	if contractID != request.ContractID {
		return ContractPublicationHeadProof{}, record, false, nil
	}
	// A head recording a target that already resolves in authoritative
	// main's own published history is a prior publish's own now-merged,
	// not-yet-pruned branch: main has moved past it, so recomputing its plan
	// against CURRENT history is guaranteed to disagree with what the
	// branch's own trailer recorded, not because the branch is forged but
	// because history advanced. Skip it — leave it out of the listing
	// entirely — rather than let that guaranteed disagreement abort the
	// WHOLE probe before every other head's own relevance/contract-ID filter
	// is even consulted. request.ResolvedInMain is main's OWN answer
	// (ContractPublicationRepository.ResolveContractPublicationTarget,
	// supplied by Publish), never a host-reported "merged" state this
	// package cannot verify offline. A head that does NOT resolve in main —
	// including a genuinely conflicting or tampered one — still falls
	// through to verifyPublicationTree exactly as before.
	if request.ResolvedInMain != nil {
		resolved, err := request.ResolvedInMain(ctx, contractID, target)
		if err != nil {
			return ContractPublicationHeadProof{}, RecoveryV1{}, false, err
		}
		if resolved {
			return ContractPublicationHeadProof{}, record, false, nil
		}
	}
	var recoveredPlan contract.PublicationPlan
	if err := r.verifyPublicationTree(ctx, record, parent, head, message, authorName, authorEmail, changes, &recoveredPlan); err != nil {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, err
	}
	raw, err := EncodeRecoveryV1(record)
	if err != nil {
		return ContractPublicationHeadProof{}, RecoveryV1{}, false, err
	}
	return ContractPublicationHeadProof{
		Branch: branch, HeadSHA: head, RecoveryJSON: raw, TreeVerified: true, recoveredPlan: recoveredPlan,
	}, record, true, nil
}

func (r *ContractPublicationRecovery) verifyPublicationTree(ctx context.Context, record RecoveryV1, parent, head, message, authorName, authorEmail string, changes map[string]host.RemoteRecoveryChange, recoveredPlan *contract.PublicationPlan) error {
	contractID, target, ok := strings.Cut(record.Target, "@")
	if !ok || operation.ContractPublish(record.System, contractID, target) != record.OperationKey {
		return remoteRecoveryConflict("publication-target-operation")
	}
	root, err := canonicalContractPublicationRoot(record.System, contractID)
	if err != nil {
		return remoteRecoveryConflict("publication-contract-root")
	}
	descriptorPath := path.Join(root, contract.DescriptorPath)
	afterPaths := make([]string, 0, len(changes))
	for name, change := range changes {
		if name != descriptorPath && !strings.HasPrefix(name, root+"/") && !isContractEventPath(name) {
			return remoteRecoveryConflict("publication-extra-change")
		}
		if change.AfterDigest != "" {
			afterPaths = append(afterPaths, name)
		}
	}
	if _, ok := changes[descriptorPath]; !ok || len(afterPaths) == 0 {
		return remoteRecoveryConflict("publication-descriptor-change-missing")
	}
	sort.Strings(afterPaths)
	after, err := r.host.ReadRemoteRecoveryTreeFiles(ctx, r.repoDir, head, afterPaths)
	if err != nil {
		return err
	}
	complete, err := r.host.ListRemoteRecoveryTreeFiles(ctx, r.repoDir, head, []string{root})
	if err != nil {
		return err
	}
	descriptorFile, ok := complete[descriptorPath]
	if !ok || !bytes.Equal(descriptorFile.Raw, after[descriptorPath].Raw) {
		return remoteRecoveryConflict("publication-descriptor-tree-mismatch")
	}
	identity, err := parseHistoricalDescriptor(descriptorFile.Raw)
	if err != nil || identity.ID != contractID || identity.Version != target {
		return remoteRecoveryConflict("publication-descriptor-identity")
	}
	descriptor, err := contract.ParseDescriptor(descriptorFile.Raw)
	if err != nil {
		return remoteRecoveryConflict("publication-descriptor-invalid")
	}
	profile := contract.ProfileContractTreeV1
	eventSchema := "event/v1"
	if identity.Schema == "envelope/v2" {
		profile, eventSchema = contract.ProfileContractSetV2, "event/v2"
	} else if identity.Schema != "envelope/v1" {
		return remoteRecoveryConflict("publication-descriptor-schema")
	}
	candidateFiles := make([]contract.CandidateFile, 0, len(complete)-1)
	for name, file := range complete {
		if name == descriptorPath {
			continue
		}
		relative := strings.TrimPrefix(name, root+"/")
		if relative == name || profile == contract.ProfileContractTreeV1 &&
			!strings.HasPrefix(relative, "schema/") && !strings.HasPrefix(relative, "fixtures/") {
			return remoteRecoveryConflict("publication-contract-tree-extra")
		}
		candidateFiles = append(candidateFiles, contract.CandidateFile{Path: relative, Kind: contract.CandidateRegular, Raw: append([]byte(nil), file.Raw...)})
	}
	candidateSnapshot := contract.CandidateSnapshot{
		Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: append([]byte(nil), descriptorFile.Raw...)},
		Files:      candidateFiles,
	}
	candidate, issues := contract.BuildCandidateIntent(candidateSnapshot)
	if len(issues) != 0 || candidate.Digest() != record.CandidateIntentDigest {
		return remoteRecoveryConflict("publication-candidate-intent")
	}
	set, issues := contract.BuildCarriedSet(profile, descriptorFile.Raw, descriptor, candidateFiles)
	if profile == contract.ProfileContractTreeV1 {
		set, issues = contract.BuildCarriedSet(profile, nil, contract.Descriptor{}, candidateFiles)
	}
	if len(issues) != 0 {
		return remoteRecoveryConflict("publication-carried-set")
	}
	var matches int
	eventPath := ""
	for name, file := range after {
		if !isContractEventPath(name) {
			continue
		}
		var event publicationEventWire
		if yaml.Unmarshal(file.Raw, &event) != nil || event.Subject != contractID || event.Transition != "publish" || event.Version != target {
			continue
		}
		if event.Actor.System != record.System || strings.TrimSuffix(path.Base(name), ".yaml") != event.Event {
			return remoteRecoveryConflict("publication-event-owner-or-id")
		}
		matches++
		eventPath = name
		if event.Schema != eventSchema || event.Digest != set.AggregateDigest {
			return remoteRecoveryConflict("publication-event-integrity")
		}
		if eventSchema == "event/v2" {
			want := contract.PublicationIntent{
				IntentKey: record.IntentKey, CandidateIntentDigest: record.CandidateIntentDigest,
				VersionSelector: record.VersionSelector, OperationKey: record.OperationKey,
			}
			if event.DigestProfile != string(profile) || event.Publication != want {
				return remoteRecoveryConflict("publication-event-intent")
			}
		} else if event.DigestProfile != "" || event.Publication != (contract.PublicationIntent{}) {
			return remoteRecoveryConflict("publication-legacy-event-shape")
		}
	}
	if matches != 1 {
		return remoteRecoveryConflict("publication-event-count")
	}
	source, ok := contractPublicationCandidateSourceFromPRBody(record.PRBody)
	if !ok {
		return remoteRecoveryConflict("publication-candidate-source-binding")
	}
	modes := map[string]string{contract.DescriptorPath: descriptorFile.Mode}
	for name, file := range complete {
		relative := strings.TrimPrefix(name, root+"/")
		if relative != name {
			modes[relative] = file.Mode
		}
	}
	return r.verifyRecomputedPublication(ctx, record, parent, eventPath, after[eventPath].Raw, candidate, modes, source, complete, changes, message, authorName, authorEmail, recoveredPlan)
}

func (r *ContractPublicationRecovery) verifyRecomputedPublication(
	ctx context.Context,
	record RecoveryV1,
	parent string,
	eventPath string,
	eventRaw []byte,
	candidate contract.CandidateIntentSnapshot,
	candidateModes map[string]string,
	candidateSource contract.CandidateSource,
	complete map[string]host.RemoteTreeFile,
	changes map[string]host.RemoteRecoveryChange,
	message string,
	authorName string,
	authorEmail string,
	recoveredPlan *contract.PublicationPlan,
) error {
	contractID, target, _ := strings.Cut(record.Target, "@")
	manifestFile, err := contractGitReadPath(ctx, r.repoDir, parent, "space.yaml", maxConfigBytes)
	if err != nil {
		return err
	}
	manifest, err := LoadManifest(ctx, manifestFile.Raw, r.manifestValidator)
	if err != nil {
		return remoteRecoveryConflict("publication-authoring-floor")
	}
	history, err := publicationHistoryAt(ctx, r.repoDir, parent, contractID, r.historyValidator)
	if err != nil {
		return err
	}
	published := make([]contract.PublishedContract, 0, len(history))
	for _, snapshot := range history {
		published = append(published, historicalPublishedContract(snapshot))
	}
	root, err := canonicalContractPublicationRoot(record.System, contractID)
	if err != nil {
		return remoteRecoveryConflict("publication-contract-root")
	}
	assertion := ""
	if generated := candidate.Descriptor().GeneratedFrom; generated != nil {
		assertion = generated.SourceDigest
	}
	plan, issues := contract.PlanPublication(contract.PublicationInput{
		System: record.System, ContractID: contractID, Selector: record.VersionSelector,
		AuthoringFloor: manifest.MinBinaryVersion, Candidate: candidate, CandidateModes: candidateModes, Published: published,
		CandidateSource: candidateSource, ContractRoot: root, SourceDigestAssertion: assertion,
		Warnings: []contract.Finding{}, Violations: []contract.Finding{},
	}, r.checker)
	if len(issues) != 0 || plan.PlanDigest != record.PlanDigest || plan.TargetVersion != target ||
		plan.OperationKey != record.OperationKey || plan.IntentKey != record.IntentKey ||
		plan.CandidateIntentDigest != record.CandidateIntentDigest || plan.HeadBranch != record.HeadBranch {
		return remoteRecoveryConflict(publicationRecomputeConflictReason(record.MintedByVersion, r.binaryVersion))
	}
	wantTree := plan.PlannedBytes()
	if len(wantTree) != len(complete) {
		return remoteRecoveryConflict("publication-plan-tree-size")
	}
	for relative, raw := range wantTree {
		observed, ok := complete[path.Join(root, relative)]
		if !ok || !bytes.Equal(raw, observed.Raw) || observed.Mode != candidateModes[relative] {
			return remoteRecoveryConflict("publication-plan-tree-bytes")
		}
	}
	mutations := make([]Mutation, 0, len(plan.Mutations)+1)
	for _, planned := range plan.Mutations {
		fullPath := path.Join(root, planned.Path)
		switch planned.Action {
		case contract.MutationWrite:
			raw, ok := wantTree[planned.Path]
			if !ok {
				return remoteRecoveryConflict("publication-plan-write")
			}
			mutation := Mutation{Path: fullPath, Operation: MutationWrite, Bytes: bytes.Clone(raw)}
			if planned.AfterMode == "100755" || planned.BeforeMode != "" && planned.AfterMode != planned.BeforeMode {
				mutation.Mode = planned.AfterMode
			}
			if planned.BeforeDigest != "" {
				mutation.ExpectedBefore = &MutationPrecondition{Digest: planned.BeforeDigest, Mode: planned.BeforeMode}
			}
			mutations = append(mutations, mutation)
		case contract.MutationDelete:
			mutations = append(mutations, Mutation{
				Path: fullPath, Operation: MutationDelete,
				ExpectedBefore: &MutationPrecondition{Digest: planned.BeforeDigest, Mode: planned.BeforeMode},
				ContractRoot:   root, PlanDigest: plan.PlanDigest,
			})
		default:
			return remoteRecoveryConflict("publication-plan-mutation")
		}
	}
	mutations = append(mutations, Mutation{Path: eventPath, Operation: MutationWrite, Bytes: bytes.Clone(eventRaw)})
	if err := verifyRemoteRecoveryTree(mutations, changes); err != nil {
		return err
	}
	remoteDigest, err := canonicalRemoteIdentityDigest(r.remoteURL, r.repository)
	if err != nil {
		return err
	}
	_, canonicalMessage, err := parseRemoteRecoveryTrailers(message)
	if err != nil {
		return err
	}
	messageBase, ok := recoveryCommitMessageBase(canonicalMessage)
	if !ok {
		return remoteRecoveryConflict("publication-commit-message-base")
	}
	producerVersion, ok := publicationEventProducerVersion(eventRaw)
	if !ok {
		return remoteRecoveryConflict("publication-producer-stamp")
	}
	data := preparedSubmissionData{
		version: preparedVersion, targetIdentity: record.Repository,
		observedSpaceFloor: manifest.MinBinaryVersion, featureFloor: manifest.MinBinaryVersion,
		schemaFloor: manifest.MinBinaryVersion, profileFloor: manifest.MinBinaryVersion,
		producerCompatibility: producerVersion, remoteIdentityDigest: remoteDigest,
		system: record.System, verb: record.Verb, artifactID: contractID,
		artifactIDs: append([]string(nil), record.ArtifactIDs...), operationKey: record.OperationKey,
		mutations: mutations, commitMessageBase: messageBase,
		commitAuthorName: authorName, commitAuthorEmail: authorEmail,
		repository: r.repository, headBranch: record.HeadBranch, baseBranch: record.BaseBranch,
		baseCommitSHA: parent, prTitle: record.PRTitle, prBody: record.PRBody,
		allowForkFallback:        record.Flags.AllowForkFallback,
		allowSpaceInfrastructure: record.Flags.AllowSpaceInfrastructure,
		replaceOrphanBranch:      record.Flags.ReplaceOrphanBranch,
	}
	preparedDigest, err := preparedDataDigest(data)
	if err != nil || preparedDigest != record.PreparedDigest {
		return remoteRecoveryConflict("publication-prepared-digest-recompute")
	}
	if recoveredPlan == nil {
		return remoteRecoveryConflict("publication-plan-output")
	}
	*recoveredPlan = plan
	return nil
}

// publicationRecomputeConflictReason classifies WHY a freshly recomputed
// publication plan disagrees with a recovered head's own RecoveryV1 record,
// once that disagreement is already established. A disagreement recomputed
// by the SAME binary that minted the head is presumptively tampering; one
// recomputed by a DIFFERENT binary — or by any binary against a record that
// predates version recording entirely — cannot be told apart from a benign
// plan-shape change across a binary upgrade, so it must not be reported with
// the tampering-shaped reason.
//
//   - mintedByVersion == runningVersion: unchanged legacy reason — this is
//     the exact "compare exactly as today" path.
//   - mintedByVersion == "": the record predates version recording; that
//     compatibility state is reported by name, not folded into tampering.
//   - mintedByVersion != runningVersion (both non-empty): an unprovable
//     cross-version recompute; the reason names BOTH versions so an operator
//     can tell which upgrade crossed the boundary.
//
// It is still a refusal in every case — an unprovable head is not a safe
// head — but only the first case may read as tampering.
func publicationRecomputeConflictReason(mintedByVersion, runningVersion string) string {
	switch {
	case mintedByVersion == "":
		return "publication-plan-recompute-unversioned-head"
	case mintedByVersion == runningVersion:
		return "publication-plan-recompute"
	default:
		return fmt.Sprintf("publication-plan-recompute-version-boundary minted-by=%s running=%s", mintedByVersion, runningVersion)
	}
}

// RepairContractPublicationHead is part of the public package API.
func (r *ContractPublicationRecovery) RepairContractPublicationHead(ctx context.Context, proof ContractPublicationHeadProof, runtime SubmissionRuntime) (WriteResult, error) {
	if r == nil || runtime.RepoDir != r.repoDir || runtime.RemoteURL != r.remoteURL || runtime.TargetIdentity != repositoryIdentity(r.repository) {
		return WriteResult{}, ErrPreparedTargetStale
	}
	r.mu.Lock()
	verified, ok := r.verified[contractPublicationProofKey(proof)]
	r.mu.Unlock()
	if !ok || verified.proof.Branch != proof.Branch || verified.proof.HeadSHA != proof.HeadSHA ||
		!bytes.Equal(verified.proof.RecoveryJSON, proof.RecoveryJSON) {
		return WriteResult{}, remoteRecoveryConflict("publication-proof-not-minted")
	}
	record := verified.record
	credential := runtime.Credential
	existing, err := r.host.FindPRByHeadBranch(ctx, host.FindPRRequest{Repo: r.repository, Branch: record.HeadBranch, Credential: credential})
	if err != nil {
		return baseWriteResult(record.HeadBranch, record.ArtifactIDs), err
	}
	if existing != nil {
		result, resultErr := contractPublicationPRResult(record, proof.HeadSHA, *existing)
		if resultErr != nil || existing.State == "merged" || existing.AutoMergeArmed {
			return result, resultErr
		}
		method, armErr := r.host.EnableAutoMerge(ctx, host.EnableAutoMergeRequest{
			Repo: r.repository, PRNumber: existing.Number, ExpectedHeadSHA: proof.HeadSHA, Credential: credential,
		})
		if armErr != nil {
			result.MergeMethod = method
			result.State = WriteStateNeedsAttention
			result.Note = "matching publication PR exists, but auto-merge could not be re-armed"
			result, _ = completeWriteResult("ContractPublicationRepair", record.HeadBranch, result)
			return result, armErr
		}
		existing.AutoMergeArmed, existing.AutoMergeNote, existing.MergeMethod = true, "", method
		return contractPublicationPRResult(record, proof.HeadSHA, *existing)
	}
	opened, err := r.host.OpenPR(ctx, host.OpenPRRequest{
		Repo: r.repository, Head: record.HeadBranch, Base: record.BaseBranch,
		Title: record.PRTitle, Body: record.PRBody, ExpectedHeadSHA: proof.HeadSHA, Credential: credential,
	})
	result, resultErr := contractPublicationPRResult(record, proof.HeadSHA, opened)
	if err != nil {
		if resultErr != nil {
			return result, fmt.Errorf("repair publication PR: %w", errors.Join(err, resultErr))
		}
		return result, err
	}
	return result, resultErr
}

func contractPublicationPRResult(record RecoveryV1, head string, pr host.PRInfo) (WriteResult, error) {
	result := baseWriteResult(record.HeadBranch, record.ArtifactIDs)
	result.CommitSHA, result.PRNumber, result.PRURL, result.MergeMethod = head, pr.Number, pr.URL, pr.MergeMethod
	if pr.Number <= 0 || pr.URL == "" || pr.HeadSHA != head || pr.BaseBranch != record.BaseBranch ||
		pr.Title != record.PRTitle || pr.Body != record.PRBody {
		return result, remoteRecoveryConflict("publication-pr-identity")
	}
	switch pr.State {
	case "merged":
		result.Stage, result.State = WriteStageMerged, WriteStateAlreadyMerged
	case "open", "":
		if pr.AutoMergeArmed {
			result.Stage, result.State = WriteStageAutoMergeArmed, WriteStatePendingMerge
		} else {
			result.Stage, result.State = WriteStagePRCreated, WriteStateAlreadyOpen
			result.Note = pr.AutoMergeNote
		}
	default:
		return result, remoteRecoveryConflict("publication-pr-state")
	}
	return completeWriteResult("ContractPublicationRepair", record.HeadBranch, result)
}

func contractPublicationProofKey(proof ContractPublicationHeadProof) string {
	return proof.Branch + "\x00" + proof.HeadSHA + "\x00" + artifact.Digest(proof.RecoveryJSON)
}

var _ ContractPublicationRemoteProof = (*ContractPublicationRecovery)(nil)
