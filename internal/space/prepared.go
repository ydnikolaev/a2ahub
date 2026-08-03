package space

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

const preparedVersion = 1

var (
	ErrPreparedInvalid          = errors.New("space: prepared submission is invalid")
	ErrPreparedDigestMismatch   = errors.New("space: prepared submission digest mismatch")
	ErrPreparedTargetStale      = errors.New("space: prepared-target-stale")
	ErrPreparedFloorStale       = errors.New("space: prepared-floor-stale")
	ErrPreparedProducerConflict = errors.New("space: caller supplied produced_by before preparation")
)

// CandidateSubmitValidator is the optional pre-stamp candidate/evaluator
// check. SubmitValidator remains the mandatory final-byte validation seam.
type CandidateSubmitValidator interface {
	ValidateSubmitCandidate(ctx context.Context, files []FileWrite) error
}

// PreparationContext binds mutable environment facts before any external
// side effect. ProducerCompatibility defaults to the funnel binary version.
type PreparationContext struct {
	TargetIdentity string
	// BaseCommitSHA is the authoritative commit at BaseBranch that a
	// recoverable submission was prepared against. It is required whenever
	// Recovery is requested so a remote retry cannot accept an otherwise
	// matching commit built atop a different base tree.
	BaseCommitSHA         string
	ObservedSpaceFloor    string
	FeatureFloor          string
	SchemaFloor           string
	ProfileFloor          string
	ProducerCompatibility string
	Recovery              *RecoveryV1
}

// SubmissionRuntime contains only invocation-local facts that cannot be
// journaled: the local mirror, push endpoint, refreshed credential, current
// bound target/floor, and the last prior result to preserve on a refusal.
type SubmissionRuntime struct {
	RepoDir           string
	RemoteURL         string
	Credential        host.Credential
	TargetIdentity    string
	CurrentSpaceFloor string
	PriorResult       WriteResult
}

// PreparedHostRequest is the frozen non-secret host request projection.
type PreparedHostRequest struct {
	Repository               host.Repo
	HeadBranch               string
	BaseBranch               string
	PRTitle                  string
	PRBody                   string
	AllowForkFallback        bool
	AllowSpaceInfrastructure bool
	ReplaceOrphanBranch      bool
}

// PreparedSubmission is an immutable value object to callers: every field is
// private and every slice-returning accessor clones. Its journal codec lives
// in prepared_journal.go and refuses delete-bearing values.
type PreparedSubmission struct {
	data preparedSubmissionData
}

type preparedSubmissionData struct {
	version                  int
	targetIdentity           string
	observedSpaceFloor       string
	featureFloor             string
	schemaFloor              string
	profileFloor             string
	producerCompatibility    string
	remoteIdentityDigest     string
	system                   string
	verb                     string
	artifactID               string
	artifactIDs              []string
	operationKey             string
	mutations                []Mutation
	commitMessageBase        string
	commitMessage            string
	commitAuthorName         string
	commitAuthorEmail        string
	repository               host.Repo
	headBranch               string
	baseBranch               string
	baseCommitSHA            string
	prTitle                  string
	prBody                   string
	allowForkFallback        bool
	allowSpaceInfrastructure bool
	replaceOrphanBranch      bool
	preparedDigest           string
	recoveryJSON             []byte
	recoveryDigest           string
}

type preparedDigestPayload struct {
	Version                  int                    `json:"version"`
	TargetIdentity           string                 `json:"target_identity"`
	ObservedSpaceFloor       string                 `json:"observed_space_floor"`
	FeatureFloor             string                 `json:"feature_floor"`
	SchemaFloor              string                 `json:"schema_floor"`
	ProfileFloor             string                 `json:"profile_floor"`
	ProducerCompatibility    string                 `json:"producer_compatibility"`
	RemoteIdentityDigest     string                 `json:"remote_identity_digest"`
	System                   string                 `json:"system"`
	Verb                     string                 `json:"verb"`
	ArtifactID               string                 `json:"artifact_id"`
	ArtifactIDs              []string               `json:"artifact_ids"`
	OperationKey             string                 `json:"operation_key"`
	Mutations                []preparedMutationWire `json:"mutations"`
	CommitMessageBase        string                 `json:"commit_message_base"`
	CommitAuthorName         string                 `json:"commit_author_name"`
	CommitAuthorEmail        string                 `json:"commit_author_email"`
	Repository               string                 `json:"repository"`
	HeadBranch               string                 `json:"head_branch"`
	BaseBranch               string                 `json:"base_branch"`
	BaseCommitSHA            string                 `json:"base_commit_sha"`
	PRTitle                  string                 `json:"pr_title"`
	PRBody                   string                 `json:"pr_body"`
	AllowForkFallback        bool                   `json:"allow_fork_fallback"`
	AllowSpaceInfrastructure bool                   `json:"allow_space_infrastructure"`
	ReplaceOrphanBranch      bool                   `json:"replace_orphan_branch"`
}

// Submit preserves the compatibility one-shot API while routing through the
// same immutable preparation boundary used by restartable callers.
func (f *WriteFunnel) Submit(ctx context.Context, request SubmitRequest) (WriteResult, error) {
	target := repositoryIdentity(request.Repo)
	prepared, err := f.PrepareSubmission(ctx, request, PreparationContext{
		TargetIdentity: target, ObservedSpaceFloor: request.MinBinaryVersion,
		ProducerCompatibility: f.binaryVersion,
	})
	if err != nil {
		return baseWriteResult("", request.ArtifactIDs), err
	}
	return f.SubmitPrepared(ctx, prepared, SubmissionRuntime{
		RepoDir: request.RepoDir, RemoteURL: request.RemoteURL, Credential: request.Credential,
		TargetIdentity: target, CurrentSpaceFloor: request.MinBinaryVersion,
	})
}

// PrepareSubmission validates, stamps, revalidates and freezes one exact
// submission without branch/commit/push/PR or any other external write.
func (f *WriteFunnel) PrepareSubmission(ctx context.Context, candidate SubmitRequest, preparation PreparationContext) (PreparedSubmission, error) {
	const op = "PrepareSubmission"
	mutations, err := normalizeMutations(candidate.Files, candidate.Mutations)
	if err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	branchID, err := validatePreparedRequest(candidate, preparation, mutations)
	if err != nil {
		return PreparedSubmission{}, &Error{Op: op, Input: candidate.ArtifactID, Err: err}
	}
	if err := f.validateMutationSet(candidate.OperationKey, mutations); err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	if preparation.ProducerCompatibility == "" {
		preparation.ProducerCompatibility = f.binaryVersion
	}
	if preparation.ObservedSpaceFloor == "" {
		preparation.ObservedSpaceFloor = candidate.MinBinaryVersion
	}
	if err := validatePreparationFloors(f.binaryVersion, preparation); err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	if preparation.Recovery != nil && !validRemoteRecoveryOID(preparation.BaseCommitSHA) {
		return PreparedSubmission{}, &Error{Op: op, Err: ErrPreparedInvalid}
	}

	writeFiles := mutationWrites(mutations)
	if f.validator == nil && f.finalValidationRequired() {
		return PreparedSubmission{}, &Error{Op: op, Err: ErrSubmitValidatorRequired}
	}
	if validator, ok := f.validator.(CandidateSubmitValidator); ok {
		if err := validator.ValidateSubmitCandidate(ctx, cloneFiles(writeFiles)); err != nil {
			return PreparedSubmission{}, &Error{Op: op, Err: err}
		}
	}
	for _, file := range writeFiles {
		if strings.Contains(file.Path, eventPathMarker) && hasProducerStamp(file.Content) {
			return PreparedSubmission{}, &Error{Op: op, Input: file.Path, Err: ErrPreparedProducerConflict}
		}
	}
	stamped, err := StampProducer(writeFiles, f.binaryVersion, preparation.ObservedSpaceFloor)
	if err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	applyStampedWrites(mutations, stamped)
	finalFiles := mutationWrites(mutations)
	if f.validator != nil {
		if err := f.validator.ValidateSubmit(ctx, cloneFiles(finalFiles)); err != nil {
			return PreparedSubmission{}, &Error{Op: op, Err: err}
		}
	}

	branch := BranchName(candidate.System, candidate.Verb, branchID)
	baseBranch := resolvedBaseBranch(candidate)
	artifactIDs := append([]string(nil), candidate.ArtifactIDs...)
	if candidate.OperationKey != "" && len(artifactIDs) == 0 && candidate.ArtifactID != "" {
		artifactIDs = []string{candidate.ArtifactID}
	}
	prBody := candidate.PRBody
	if candidate.OperationKey != "" {
		prBody = appendOperationMetadata(prBody, candidate.OperationKey, artifactIDs)
	}
	data := preparedSubmissionData{
		version: preparedVersion, targetIdentity: preparation.TargetIdentity,
		observedSpaceFloor: preparation.ObservedSpaceFloor, featureFloor: preparation.FeatureFloor,
		schemaFloor: preparation.SchemaFloor, profileFloor: preparation.ProfileFloor,
		producerCompatibility: preparation.ProducerCompatibility,
		system:                candidate.System, verb: candidate.Verb, artifactID: candidate.ArtifactID,
		artifactIDs: artifactIDs, operationKey: candidate.OperationKey,
		mutations: cloneMutations(mutations), commitMessageBase: candidate.CommitMessage,
		commitMessage: candidate.CommitMessage, commitAuthorName: candidate.CommitAuthorName,
		commitAuthorEmail: candidate.CommitAuthorEmail, repository: candidate.Repo,
		headBranch: branch, baseBranch: baseBranch, baseCommitSHA: preparation.BaseCommitSHA,
		prTitle: candidate.PRTitle, prBody: prBody,
		allowForkFallback:        candidate.AllowForkFallback,
		allowSpaceInfrastructure: candidate.AllowSpaceInfrastructure,
		replaceOrphanBranch:      candidate.ReplaceOrphanBranch,
	}
	data.remoteIdentityDigest, err = canonicalRemoteIdentityDigest(candidate.RemoteURL, candidate.Repo)
	if err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	data.preparedDigest, err = preparedDataDigest(data)
	if err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	if preparation.Recovery != nil {
		record := *preparation.Recovery
		record.ArtifactIDs = append([]string(nil), data.artifactIDs...)
		slices.Sort(record.ArtifactIDs)
		record.BaseBranch = data.baseBranch
		record.Flags = RecoveryFlagsV1{
			AllowForkFallback: data.allowForkFallback, AllowSpaceInfrastructure: data.allowSpaceInfrastructure,
			ReplaceOrphanBranch: data.replaceOrphanBranch,
		}
		record.HeadBranch, record.OperationKey = data.headBranch, data.operationKey
		record.PRBody, record.PRTitle = data.prBody, data.prTitle
		record.PreparedDigest = data.preparedDigest
		record.Repository, record.System = repositoryIdentity(data.repository), data.system
		record.Verb, record.Version = data.verb, recoveryVersion
		encoded, digest, rerr := EncodeRecoveryV1Trailer(record)
		if rerr != nil {
			return PreparedSubmission{}, &Error{Op: op, Err: rerr}
		}
		data.recoveryJSON, rerr = EncodeRecoveryV1(record)
		if rerr != nil {
			return PreparedSubmission{}, &Error{Op: op, Err: rerr}
		}
		data.recoveryDigest = digest
		data.commitMessage = appendRecoveryTrailers(data.commitMessageBase, record, encoded, digest)
	}
	prepared := PreparedSubmission{data: clonePreparedData(data)}
	if err := prepared.verify(); err != nil {
		return PreparedSubmission{}, &Error{Op: op, Err: err}
	}
	return prepared, nil
}

// SubmitPrepared verifies every frozen layer and current target/floor before
// making a host or Git call. It refreshes only the runtime credential/path.
func (f *WriteFunnel) SubmitPrepared(ctx context.Context, prepared PreparedSubmission, runtime SubmissionRuntime) (WriteResult, error) {
	const op = "SubmitPrepared"
	prior := runtime.PriorResult
	if prior.Stage == "" {
		prior = baseWriteResult(prepared.data.headBranch, prepared.data.artifactIDs)
	}
	if err := prepared.verify(); err != nil {
		return prior, &Error{Op: op, Input: prepared.data.headBranch, Err: err}
	}
	if err := f.validateMutationSet(prepared.data.operationKey, prepared.data.mutations); err != nil {
		return prior, &Error{Op: op, Input: prepared.data.headBranch, Err: err}
	}
	if err := validatePreparedSemantics(prepared.data); err != nil {
		return prior, &Error{Op: op, Input: prepared.data.headBranch, Err: err}
	}
	if runtime.TargetIdentity == "" || runtime.TargetIdentity != prepared.data.targetIdentity {
		return prior, &Error{Op: op, Input: runtime.TargetIdentity, Err: ErrPreparedTargetStale}
	}
	if err := verifyPreparedFloor(runtime.CurrentSpaceFloor, prepared.data); err != nil {
		return prior, &Error{Op: op, Input: runtime.CurrentSpaceFloor, Err: err}
	}
	remoteDigest, err := canonicalRemoteIdentityDigest(runtime.RemoteURL, prepared.data.repository)
	if err != nil || remoteDigest != prepared.data.remoteIdentityDigest {
		return prior, &Error{Op: op, Input: runtime.TargetIdentity, Err: ErrPreparedTargetStale}
	}
	request := prepared.submitRequest(runtime)
	return f.submitPreparedRequest(ctx, request, prior, &prepared)
}

func (f *WriteFunnel) finalValidationRequired() bool {
	older, err := versionOlderThan(f.binaryVersion, version.OperationalConfidenceFloor)
	return err != nil || !older
}

func (p PreparedSubmission) Digest() string { return p.data.preparedDigest }

func (p PreparedSubmission) RecoveryDigest() string { return p.data.recoveryDigest }

func (p PreparedSubmission) Branch() string { return p.data.headBranch }

func (p PreparedSubmission) ArtifactIDs() []string {
	return append([]string(nil), p.data.artifactIDs...)
}

func (p PreparedSubmission) Mutations() []Mutation { return cloneMutations(p.data.mutations) }

func (p PreparedSubmission) HostRequest() PreparedHostRequest {
	return PreparedHostRequest{
		Repository: p.data.repository, HeadBranch: p.data.headBranch, BaseBranch: p.data.baseBranch,
		PRTitle: p.data.prTitle, PRBody: p.data.prBody, AllowForkFallback: p.data.allowForkFallback,
		AllowSpaceInfrastructure: p.data.allowSpaceInfrastructure,
		ReplaceOrphanBranch:      p.data.replaceOrphanBranch,
	}
}

func (p PreparedSubmission) verify() error {
	if p.data.version != preparedVersion || p.data.preparedDigest == "" {
		return ErrPreparedInvalid
	}
	if p.data.remoteIdentityDigest != "" && !validTypedSHA256(p.data.remoteIdentityDigest) {
		return ErrPreparedInvalid
	}
	if err := validatePreparedSemantics(p.data); err != nil {
		return err
	}
	want, err := preparedDataDigest(p.data)
	if err != nil {
		return err
	}
	if p.data.preparedDigest != want {
		return ErrPreparedDigestMismatch
	}
	if p.data.recoveryDigest == "" && len(p.data.recoveryJSON) == 0 {
		if p.data.commitMessage != p.data.commitMessageBase {
			return ErrPreparedDigestMismatch
		}
		return nil
	}
	if p.data.recoveryDigest == "" || len(p.data.recoveryJSON) == 0 || RecoveryDigest(p.data.recoveryJSON) != p.data.recoveryDigest {
		return ErrRecoveryDigestMismatch
	}
	record, err := DecodeRecoveryV1(p.data.recoveryJSON)
	if err != nil {
		return err
	}
	if record.PreparedDigest != p.data.preparedDigest || record.HeadBranch != p.data.headBranch ||
		record.OperationKey != p.data.operationKey || record.BaseBranch != p.data.baseBranch ||
		record.Repository != repositoryIdentity(p.data.repository) || record.System != p.data.system ||
		record.Verb != p.data.verb || record.PRTitle != p.data.prTitle || record.PRBody != p.data.prBody ||
		!slices.Equal(record.ArtifactIDs, p.data.artifactIDs) ||
		record.Flags != (RecoveryFlagsV1{
			AllowForkFallback:        p.data.allowForkFallback,
			AllowSpaceInfrastructure: p.data.allowSpaceInfrastructure,
			ReplaceOrphanBranch:      p.data.replaceOrphanBranch,
		}) {
		return ErrPreparedDigestMismatch
	}
	encoded, digest, err := EncodeRecoveryV1Trailer(record)
	if err != nil || digest != p.data.recoveryDigest ||
		p.data.commitMessage != appendRecoveryTrailers(p.data.commitMessageBase, record, encoded, digest) {
		return ErrPreparedDigestMismatch
	}
	return nil
}

func (p PreparedSubmission) hasDelete() bool {
	for _, mutation := range p.data.mutations {
		if mutation.Operation == MutationDelete {
			return true
		}
	}
	return false
}

func (p PreparedSubmission) submitRequest(runtime SubmissionRuntime) SubmitRequest {
	data := clonePreparedData(p.data)
	return SubmitRequest{
		RepoDir: runtime.RepoDir, System: data.system, Verb: data.verb, ArtifactID: data.artifactID,
		ArtifactIDs: data.artifactIDs, OperationKey: data.operationKey, Mutations: data.mutations,
		CommitMessage: data.commitMessage, CommitAuthorName: data.commitAuthorName,
		CommitAuthorEmail: data.commitAuthorEmail, RemoteURL: runtime.RemoteURL,
		Repo: data.repository, BaseBranch: data.baseBranch, ExpectedBaseSHA: data.baseCommitSHA,
		PRTitle: data.prTitle, PRBody: data.prBody,
		Credential: runtime.Credential, AllowForkFallback: data.allowForkFallback,
		ReplaceOrphanBranch:      data.replaceOrphanBranch,
		AllowSpaceInfrastructure: data.allowSpaceInfrastructure,
	}
}

func validatePreparedRequest(candidate SubmitRequest, preparation PreparationContext, mutations []Mutation) (string, error) {
	if candidate.Verb == "" {
		return "", ErrMissingVerb
	}
	branchID := candidate.ArtifactID
	if candidate.OperationKey != "" {
		if !operation.Valid(candidate.OperationKey) {
			return "", ErrInvalidOperationKey
		}
		branchID = candidate.OperationKey
	}
	if err := validateBranchSegments(candidate.System, candidate.Verb, branchID); err != nil {
		return "", err
	}
	if preparation.TargetIdentity == "" || preparation.TargetIdentity != repositoryIdentity(candidate.Repo) ||
		!validRecoveryRepository(preparation.TargetIdentity) {
		return "", ErrPreparedTargetStale
	}
	if candidate.MinBinaryVersion != "" && preparation.ObservedSpaceFloor != "" &&
		candidate.MinBinaryVersion != preparation.ObservedSpaceFloor {
		return "", ErrPreparedFloorStale
	}
	for _, mutation := range mutations {
		if !sectionOK(candidate.System, mutation.Path) &&
			(!candidate.AllowSpaceInfrastructure || !spaceInfraOK(mutation.Path)) {
			return "", fmt.Errorf("%w: %s", ErrWrongSection, mutation.Path)
		}
	}
	for _, line := range strings.Split(candidate.CommitMessage, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, name := range []string{
			"A2A-Operation-Key:", "A2A-Plan-Digest:", "A2A-Target:",
			"A2A-Prepared-Digest:", "A2A-Recovery-Version:",
			"A2A-Recovery-Digest:", "A2A-Recovery-v1:",
		} {
			if strings.HasPrefix(trimmed, name) {
				return "", fmt.Errorf("%w: caller supplied reserved trailer %s", ErrPreparedInvalid, name)
			}
		}
	}
	if candidate.OperationKey != "" && strings.Contains(candidate.PRBody, operationMetadataPrefix) {
		return "", fmt.Errorf("%w: caller supplied reserved PR operation metadata", ErrPreparedInvalid)
	}
	return branchID, nil
}

func validatePreparationFloors(binaryVersion string, preparation PreparationContext) error {
	for _, floor := range []string{
		preparation.ObservedSpaceFloor, preparation.FeatureFloor, preparation.SchemaFloor,
		preparation.ProfileFloor, preparation.ProducerCompatibility,
	} {
		if floor == "" {
			continue
		}
		canonical, err := version.Canonical(floor)
		if err != nil || canonical != floor {
			return ErrPreparedFloorStale
		}
	}
	if preparation.ObservedSpaceFloor != "" {
		older, err := versionOlderThan(binaryVersion, preparation.ObservedSpaceFloor)
		if err != nil {
			return err
		}
		if older {
			return ErrStaleBinaryVersion
		}
	}
	data := preparedSubmissionData{
		featureFloor: preparation.FeatureFloor, schemaFloor: preparation.SchemaFloor,
		profileFloor: preparation.ProfileFloor, producerCompatibility: preparation.ProducerCompatibility,
	}
	return verifyPreparedFloor(preparation.ObservedSpaceFloor, data)
}

func verifyPreparedFloor(current string, data preparedSubmissionData) error {
	if current == "" {
		if data.observedSpaceFloor == "" && data.featureFloor == "" && data.schemaFloor == "" && data.profileFloor == "" {
			return nil
		}
		return ErrPreparedFloorStale
	}
	canonical, err := version.Canonical(current)
	if err != nil || canonical != current {
		return ErrPreparedFloorStale
	}
	for _, required := range []string{data.featureFloor, data.schemaFloor, data.profileFloor} {
		if required == "" {
			continue
		}
		older, err := versionOlderThan(current, required)
		if err != nil || older {
			return ErrPreparedFloorStale
		}
	}
	if data.producerCompatibility != "" {
		compatibilityOlder, err := versionOlderThan(data.producerCompatibility, current)
		if err != nil || compatibilityOlder {
			return ErrPreparedFloorStale
		}
	}
	return nil
}

func preparedDataDigest(data preparedSubmissionData) (string, error) {
	payload := preparedDigestPayload{
		Version: data.version, TargetIdentity: data.targetIdentity, ObservedSpaceFloor: data.observedSpaceFloor,
		FeatureFloor: data.featureFloor, SchemaFloor: data.schemaFloor, ProfileFloor: data.profileFloor,
		ProducerCompatibility: data.producerCompatibility, System: data.system, Verb: data.verb,
		RemoteIdentityDigest: data.remoteIdentityDigest,
		ArtifactID:           data.artifactID, ArtifactIDs: append([]string(nil), data.artifactIDs...),
		OperationKey: data.operationKey, Mutations: mutationsToWire(data.mutations),
		CommitMessageBase: data.commitMessageBase, CommitAuthorName: data.commitAuthorName,
		CommitAuthorEmail: data.commitAuthorEmail, Repository: repositoryIdentity(data.repository),
		HeadBranch: data.headBranch, BaseBranch: data.baseBranch, BaseCommitSHA: data.baseCommitSHA,
		PRTitle: data.prTitle, PRBody: data.prBody,
		AllowForkFallback: data.allowForkFallback, AllowSpaceInfrastructure: data.allowSpaceInfrastructure,
		ReplaceOrphanBranch: data.replaceOrphanBranch,
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return "", fmt.Errorf("%w: digest encoding: %v", ErrPreparedInvalid, err)
	}
	digest := sha256.Sum256(bytes.TrimSuffix(encoded.Bytes(), []byte("\n")))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func clonePreparedData(data preparedSubmissionData) preparedSubmissionData {
	data.artifactIDs = append([]string(nil), data.artifactIDs...)
	data.mutations = cloneMutations(data.mutations)
	data.recoveryJSON = append([]byte(nil), data.recoveryJSON...)
	return data
}

func cloneMutations(mutations []Mutation) []Mutation {
	out := make([]Mutation, len(mutations))
	for i := range mutations {
		out[i] = cloneMutation(mutations[i])
	}
	return out
}

func cloneFiles(files []FileWrite) []FileWrite {
	out := make([]FileWrite, len(files))
	for i := range files {
		out[i] = FileWrite{Path: files[i].Path, Content: append([]byte(nil), files[i].Content...)}
	}
	return out
}

func mutationWrites(mutations []Mutation) []FileWrite {
	files := make([]FileWrite, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.Operation == MutationWrite {
			files = append(files, FileWrite{Path: mutation.Path, Content: append([]byte(nil), mutation.Bytes...)})
		}
	}
	return files
}

func applyStampedWrites(mutations []Mutation, stamped []FileWrite) {
	byPath := make(map[string][]byte, len(stamped))
	for _, file := range stamped {
		byPath[file.Path] = file.Content
	}
	for i := range mutations {
		if mutations[i].Operation == MutationWrite {
			mutations[i].Bytes = append([]byte(nil), byPath[mutations[i].Path]...)
		}
	}
}

func repositoryIdentity(repository host.Repo) string { return repository.Owner + "/" + repository.Name }

func validatePreparedSemantics(data preparedSubmissionData) error {
	candidate := SubmitRequest{
		System: data.system, Verb: data.verb, ArtifactID: data.artifactID,
		ArtifactIDs: append([]string(nil), data.artifactIDs...), OperationKey: data.operationKey,
		Mutations: cloneMutations(data.mutations), CommitMessage: data.commitMessageBase,
		CommitAuthorName: data.commitAuthorName, CommitAuthorEmail: data.commitAuthorEmail,
		Repo: data.repository, BaseBranch: data.baseBranch, PRTitle: data.prTitle,
		MinBinaryVersion: data.observedSpaceFloor, AllowForkFallback: data.allowForkFallback,
		AllowSpaceInfrastructure: data.allowSpaceInfrastructure, ReplaceOrphanBranch: data.replaceOrphanBranch,
	}
	branchID, err := validatePreparedRequest(candidate, PreparationContext{
		TargetIdentity: data.targetIdentity, ObservedSpaceFloor: data.observedSpaceFloor,
		FeatureFloor: data.featureFloor, SchemaFloor: data.schemaFloor, ProfileFloor: data.profileFloor,
		ProducerCompatibility: data.producerCompatibility,
	}, candidate.Mutations)
	if err != nil {
		return err
	}
	if data.headBranch != BranchName(data.system, data.verb, branchID) ||
		data.baseBranch != resolvedBaseBranch(candidate) || !validRecoveryBranch(data.baseBranch) {
		return ErrPreparedInvalid
	}
	if err := validatePreparationFloors(data.producerCompatibility, PreparationContext{
		ObservedSpaceFloor: data.observedSpaceFloor, FeatureFloor: data.featureFloor,
		SchemaFloor: data.schemaFloor, ProfileFloor: data.profileFloor,
		ProducerCompatibility: data.producerCompatibility,
	}); err != nil {
		return err
	}
	if data.operationKey != "" {
		key, ids, ok := ParseOperationMetadata(data.prBody)
		if !ok || key != data.operationKey || !slices.Equal(ids, data.artifactIDs) {
			return ErrPreparedInvalid
		}
	}
	if len(data.recoveryJSON) > 0 && !validRemoteRecoveryOID(data.baseCommitSHA) {
		return ErrPreparedInvalid
	}
	return nil
}

func canonicalRemoteIdentityDigest(raw string, repository host.Repo) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	identity := ""
	if scp, ok := strings.CutPrefix(trimmed, "git@github.com:"); ok {
		identity = "github.com/" + strings.TrimSuffix(scp, ".git")
	} else {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", ErrPreparedTargetStale
		}
		switch {
		case parsed.Scheme == "":
			absolute, err := filepath.Abs(trimmed)
			if err != nil {
				return "", ErrPreparedTargetStale
			}
			identity = "file:" + filepath.Clean(absolute)
		case parsed.Scheme == "file":
			if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return "", ErrPreparedTargetStale
			}
			absolute, err := filepath.Abs(parsed.Path)
			if err != nil {
				return "", ErrPreparedTargetStale
			}
			identity = "file:" + filepath.Clean(absolute)
		case strings.EqualFold(parsed.Hostname(), "github.com"):
			if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return "", ErrPreparedTargetStale
			}
			identity = "github.com/" + strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git")
		default:
			return "", ErrPreparedTargetStale
		}
	}
	want := strings.ToLower(repositoryIdentity(repository))
	if strings.HasPrefix(strings.ToLower(identity), "github.com/") &&
		strings.TrimPrefix(strings.ToLower(identity), "github.com/") != want {
		return "", ErrPreparedTargetStale
	}
	digest := sha256.Sum256([]byte(identity))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func appendRecoveryTrailers(base string, record RecoveryV1, encoded, digest string) string {
	trailers := []string{
		"A2A-Operation-Key: " + record.OperationKey,
		"A2A-Plan-Digest: " + record.PlanDigest,
		"A2A-Target: " + record.Target,
		"A2A-Prepared-Digest: " + record.PreparedDigest,
		"A2A-Recovery-Version: 1",
		"A2A-Recovery-Digest: " + digest,
		"A2A-Recovery-v1: " + encoded,
	}
	if base == "" {
		return strings.Join(trailers, "\n")
	}
	return strings.TrimRight(base, "\n") + "\n\n" + strings.Join(trailers, "\n")
}
