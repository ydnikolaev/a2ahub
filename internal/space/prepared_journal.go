package space

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

const preparedJournalMaxBytes = 24 * 1024

var (
	ErrPreparedNotJournalable  = errors.New("space: delete-bearing prepared submission is not journalable")
	ErrPreparedJournalInvalid  = errors.New("space: invalid prepared submission journal")
	ErrPreparedJournalTooLarge = errors.New("space: prepared submission journal exceeds 24 KiB")
)

type preparedMutationWire struct {
	Path                 string            `json:"path"`
	Operation            MutationOperation `json:"operation"`
	Bytes                []byte            `json:"bytes"`
	Mode                 string            `json:"mode,omitempty"`
	ExpectedBeforeDigest string            `json:"expected_before_digest"`
	ExpectedBeforeMode   string            `json:"expected_before_mode"`
	ContractRoot         string            `json:"contract_root"`
	PlanDigest           string            `json:"plan_digest"`
}

type preparedJournalV1 struct {
	Version        int                   `json:"version"`
	Payload        preparedDigestPayload `json:"payload"`
	PreparedDigest string                `json:"prepared_digest"`
	RecoveryJSON   string                `json:"recovery_json"`
	RecoveryDigest string                `json:"recovery_digest"`
}

// EncodePreparedSubmissionJournal emits the closed v1 no-delete checkpoint.
func EncodePreparedSubmissionJournal(prepared PreparedSubmission) ([]byte, error) {
	for _, mutation := range prepared.data.mutations {
		if mutation.Operation == MutationDelete || mutation.deletePermit != nil {
			return nil, ErrPreparedNotJournalable
		}
	}
	if err := prepared.verify(); err != nil {
		return nil, err
	}
	wire := preparedJournalV1{
		Version: preparedVersion, Payload: prepared.payload(), PreparedDigest: prepared.data.preparedDigest,
		RecoveryJSON: string(prepared.data.recoveryJSON), RecoveryDigest: prepared.data.recoveryDigest,
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire); err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrPreparedJournalInvalid, err)
	}
	raw := bytes.TrimSuffix(out.Bytes(), []byte("\n"))
	if len(raw) > preparedJournalMaxBytes {
		return nil, ErrPreparedJournalTooLarge
	}
	return append([]byte(nil), raw...), nil
}

// DecodePreparedSubmissionJournal accepts only the closed canonical v1 form
// and reconstructs no authority: any delete operation is refused.
func DecodePreparedSubmissionJournal(raw []byte) (PreparedSubmission, error) {
	if len(raw) > preparedJournalMaxBytes {
		return PreparedSubmission{}, ErrPreparedJournalTooLarge
	}
	if len(raw) == 0 {
		return PreparedSubmission{}, ErrPreparedJournalInvalid
	}
	if err := rejectDuplicateJSONKeys(raw); err != nil {
		return PreparedSubmission{}, fmt.Errorf("%w: %v", ErrPreparedJournalInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire preparedJournalV1
	if err := decoder.Decode(&wire); err != nil {
		return PreparedSubmission{}, fmt.Errorf("%w: %v", ErrPreparedJournalInvalid, err)
	}
	if wire.Version != preparedVersion || wire.PreparedDigest == "" || wire.Payload.Version != preparedVersion {
		return PreparedSubmission{}, ErrPreparedJournalInvalid
	}
	mutations, err := mutationsFromWire(wire.Payload.Mutations)
	if err != nil {
		return PreparedSubmission{}, err
	}
	for _, mutation := range mutations {
		if mutation.Operation == MutationDelete {
			return PreparedSubmission{}, ErrPreparedNotJournalable
		}
	}
	repositoryOwner, repositoryName, ok := strings.Cut(wire.Payload.Repository, "/")
	if !ok {
		return PreparedSubmission{}, ErrPreparedJournalInvalid
	}
	data := preparedSubmissionData{
		version: wire.Payload.Version, targetIdentity: wire.Payload.TargetIdentity,
		observedSpaceFloor: wire.Payload.ObservedSpaceFloor, featureFloor: wire.Payload.FeatureFloor,
		schemaFloor: wire.Payload.SchemaFloor, profileFloor: wire.Payload.ProfileFloor,
		producerCompatibility: wire.Payload.ProducerCompatibility,
		remoteIdentityDigest:  wire.Payload.RemoteIdentityDigest,
		system:                wire.Payload.System, verb: wire.Payload.Verb, artifactID: wire.Payload.ArtifactID,
		artifactIDs: append([]string(nil), wire.Payload.ArtifactIDs...), operationKey: wire.Payload.OperationKey,
		mutations: mutations, commitMessageBase: wire.Payload.CommitMessageBase,
		commitMessage: wire.Payload.CommitMessageBase, commitAuthorName: wire.Payload.CommitAuthorName,
		commitAuthorEmail: wire.Payload.CommitAuthorEmail,
		repository:        host.Repo{Owner: repositoryOwner, Name: repositoryName}, headBranch: wire.Payload.HeadBranch,
		baseBranch: wire.Payload.BaseBranch, baseCommitSHA: wire.Payload.BaseCommitSHA,
		prTitle: wire.Payload.PRTitle, prBody: wire.Payload.PRBody,
		allowForkFallback:        wire.Payload.AllowForkFallback,
		allowSpaceInfrastructure: wire.Payload.AllowSpaceInfrastructure,
		replaceOrphanBranch:      wire.Payload.ReplaceOrphanBranch,
		preparedDigest:           wire.PreparedDigest, recoveryJSON: []byte(wire.RecoveryJSON),
		recoveryDigest: wire.RecoveryDigest,
	}
	if len(data.recoveryJSON) > 0 {
		record, err := DecodeRecoveryV1(data.recoveryJSON)
		if err != nil {
			return PreparedSubmission{}, err
		}
		encoded, digest, err := EncodeRecoveryV1Trailer(record)
		if err != nil || digest != data.recoveryDigest {
			return PreparedSubmission{}, ErrRecoveryDigestMismatch
		}
		data.commitMessage = appendRecoveryTrailers(data.commitMessageBase, record, encoded, digest)
	} else if data.recoveryDigest != "" {
		return PreparedSubmission{}, ErrPreparedJournalInvalid
	}
	prepared := PreparedSubmission{data: data}
	if err := prepared.verify(); err != nil {
		return PreparedSubmission{}, err
	}
	canonical, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		return PreparedSubmission{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return PreparedSubmission{}, fmt.Errorf("%w: JSON is not canonical", ErrPreparedJournalInvalid)
	}
	return PreparedSubmission{data: clonePreparedData(data)}, nil
}

func (p PreparedSubmission) MarshalJSON() ([]byte, error) {
	return EncodePreparedSubmissionJournal(p)
}

func (p *PreparedSubmission) UnmarshalJSON(raw []byte) error {
	decoded, err := DecodePreparedSubmissionJournal(raw)
	if err != nil {
		return err
	}
	*p = decoded
	return nil
}

func (p PreparedSubmission) payload() preparedDigestPayload {
	data := p.data
	return preparedDigestPayload{
		Version: data.version, TargetIdentity: data.targetIdentity, ObservedSpaceFloor: data.observedSpaceFloor,
		FeatureFloor: data.featureFloor, SchemaFloor: data.schemaFloor, ProfileFloor: data.profileFloor,
		ProducerCompatibility: data.producerCompatibility, RemoteIdentityDigest: data.remoteIdentityDigest,
		System: data.system, Verb: data.verb,
		ArtifactID: data.artifactID, ArtifactIDs: append([]string(nil), data.artifactIDs...),
		OperationKey: data.operationKey, Mutations: mutationsToWire(data.mutations),
		CommitMessageBase: data.commitMessageBase, CommitAuthorName: data.commitAuthorName,
		CommitAuthorEmail: data.commitAuthorEmail, Repository: repositoryIdentity(data.repository),
		HeadBranch: data.headBranch, BaseBranch: data.baseBranch, BaseCommitSHA: data.baseCommitSHA,
		PRTitle: data.prTitle, PRBody: data.prBody,
		AllowForkFallback: data.allowForkFallback, AllowSpaceInfrastructure: data.allowSpaceInfrastructure,
		ReplaceOrphanBranch: data.replaceOrphanBranch,
	}
}

func mutationsToWire(mutations []Mutation) []preparedMutationWire {
	out := make([]preparedMutationWire, len(mutations))
	for i, mutation := range mutations {
		out[i] = preparedMutationWire{
			Path: mutation.Path, Operation: mutation.Operation, Bytes: append([]byte{}, mutation.Bytes...),
			Mode: mutation.Mode, ContractRoot: mutation.ContractRoot, PlanDigest: mutation.PlanDigest,
		}
		if mutation.ExpectedBefore != nil {
			out[i].ExpectedBeforeDigest = mutation.ExpectedBefore.Digest
			out[i].ExpectedBeforeMode = mutation.ExpectedBefore.Mode
		}
	}
	return out
}

func mutationsFromWire(wire []preparedMutationWire) ([]Mutation, error) {
	if wire == nil || len(wire) == 0 || len(wire) > maxMutationCount {
		return nil, ErrPreparedJournalInvalid
	}
	out := make([]Mutation, len(wire))
	for i, encoded := range wire {
		mutation := Mutation{
			Path: encoded.Path, Operation: encoded.Operation, Bytes: append([]byte{}, encoded.Bytes...),
			Mode: encoded.Mode, ContractRoot: encoded.ContractRoot, PlanDigest: encoded.PlanDigest,
		}
		if encoded.ExpectedBeforeDigest != "" || encoded.ExpectedBeforeMode != "" {
			mutation.ExpectedBefore = &MutationPrecondition{
				Digest: encoded.ExpectedBeforeDigest, Mode: encoded.ExpectedBeforeMode,
			}
		}
		if mutation.Operation == MutationDelete {
			return nil, ErrPreparedNotJournalable
		}
		if mutation.Operation != MutationWrite || encoded.Bytes == nil || mutation.ContractRoot != "" || mutation.PlanDigest != "" ||
			(mutation.Mode != "" && mutation.Mode != "100644" && mutation.Mode != "100755") ||
			(mutation.ExpectedBefore != nil && !validMutationPrecondition(*mutation.ExpectedBefore)) || !isCleanRelativePath(mutation.Path) {
			return nil, ErrPreparedJournalInvalid
		}
		out[i] = mutation
	}
	return out, nil
}
