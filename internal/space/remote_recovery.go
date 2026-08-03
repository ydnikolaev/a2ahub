package space

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

var ErrOperationConflict = errors.New("space: operation-conflict")

// remoteRecoveryReader is the narrow read capability required to repair a
// lost acknowledgement after a deterministic branch push. Implementations
// return the exact commit message and the complete changed-path set against
// the commit's first parent. Each change includes bounded before/after digest
// and regular-file mode evidence; absence on one side denotes add/delete.
// Returning the full set, rather than only requested paths, prevents an
// otherwise matching recovery commit from hiding an extra remote change.
//
// The primitive-only signature deliberately lets a host adapter implement the
// consumer-side interface without importing space (which would form a cycle).
// RepoDir and RemoteURL are carried so the eventual adapter can perform one
// authenticated exact-ref fetch, read %B, and compute a first-parent
// diff-tree in the disposable mirror; current host.RemoteBranchReader alone
// cannot provide this evidence.
type remoteRecoveryReader interface {
	ReadRemoteRecoveryCommit(
		ctx context.Context,
		repoDir string,
		remoteURL string,
		repository host.Repo,
		branch string,
		credential host.Credential,
	) (
		observedRepository host.Repo,
		observedBranch string,
		headSHA string,
		parentSHA string,
		commitMessage string,
		commitAuthorName string,
		commitAuthorEmail string,
		changedFiles map[string]host.RemoteRecoveryChange,
		exists bool,
		err error,
	)
}

type remoteRecoveryRuntime struct {
	RepoDir    string
	RemoteURL  string
	Credential host.Credential
}

type remoteRecoveryRepair struct {
	HeadSHA string
	OpenPR  host.OpenPRRequest
}

// probePreparedRemoteRecovery checks the deterministic remote head after the
// caller has established that no PR owns it. A matching head yields only the
// frozen OpenPR request. It never materializes the tree, reapplies mutations,
// or mints a delete permit.
func probePreparedRemoteRecovery(
	ctx context.Context,
	reader remoteRecoveryReader,
	prepared PreparedSubmission,
	runtime remoteRecoveryRuntime,
) (remoteRecoveryRepair, bool, error) {
	if reader == nil {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("reader-unavailable")
	}
	if err := prepared.verify(); err != nil {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("prepared-invalid")
	}
	if len(prepared.data.recoveryJSON) == 0 || prepared.data.recoveryDigest == "" {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("recovery-missing")
	}
	if runtime.RepoDir == "" {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("repository-directory-missing")
	}
	remoteDigest, err := canonicalRemoteIdentityDigest(runtime.RemoteURL, prepared.data.repository)
	if err != nil || remoteDigest != prepared.data.remoteIdentityDigest {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("remote-identity-mismatch")
	}

	if err := validateRemoteRecoveryMutations(prepared.data.mutations); err != nil {
		return remoteRecoveryRepair{}, false, err
	}
	request := prepared.HostRequest()
	observedRepository, observedBranch, headSHA, parentSHA, message, _, _, fileDigests, exists, err :=
		reader.ReadRemoteRecoveryCommit(
			ctx, runtime.RepoDir, runtime.RemoteURL, request.Repository, request.HeadBranch, runtime.Credential,
		)
	if err != nil {
		return remoteRecoveryRepair{}, false, fmt.Errorf("space: read remote recovery commit: %w", err)
	}
	if !exists {
		return remoteRecoveryRepair{}, false, nil
	}
	if observedRepository != request.Repository {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("repository-mismatch")
	}
	if observedBranch != request.HeadBranch {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("branch-mismatch")
	}
	if !validRemoteRecoveryOID(headSHA) {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("head-sha-invalid")
	}
	if parentSHA != prepared.data.baseCommitSHA {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("base-commit-mismatch")
	}

	trailers, canonicalMessage, err := parseRemoteRecoveryTrailers(message)
	if err != nil {
		return remoteRecoveryRepair{}, false, err
	}
	if canonicalMessage != prepared.data.commitMessage {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("commit-message-mismatch")
	}
	record, err := DecodeRecoveryV1Trailer(trailers.recoveryV1, trailers.recoveryDigest)
	if err != nil {
		return remoteRecoveryRepair{}, false, remoteRecoveryConflict("recovery-record-invalid")
	}
	if err := verifyRemoteRecoveryMetadata(prepared, record, trailers); err != nil {
		return remoteRecoveryRepair{}, false, err
	}
	if err := verifyRemoteRecoveryTree(prepared.data.mutations, fileDigests); err != nil {
		return remoteRecoveryRepair{}, false, err
	}

	return remoteRecoveryRepair{
		HeadSHA: headSHA,
		OpenPR: host.OpenPRRequest{
			Repo: request.Repository, Head: request.HeadBranch, Base: request.BaseBranch,
			Title: request.PRTitle, Body: request.PRBody, ExpectedHeadSHA: headSHA,
			Credential: runtime.Credential,
		},
	}, true, nil
}

type remoteRecoveryTrailers struct {
	operationKey   string
	planDigest     string
	target         string
	preparedDigest string
	recoveryDigest string
	recoveryV1     string
}

func parseRemoteRecoveryTrailers(message string) (remoteRecoveryTrailers, string, error) {
	canonical := message
	if strings.ContainsRune(canonical, '\r') {
		return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("commit-message-noncanonical")
	}
	// `git show --format=%B` appends one presentation newline to the commit
	// message, whose canonical object form itself ends in one newline. Test
	// adapters may return the canonical text directly. Accept either transport
	// shape, normalize both to the prepared message, and reject a third suffix.
	if strings.HasSuffix(canonical, "\n") {
		canonical = strings.TrimSuffix(canonical, "\n")
	}
	if strings.HasSuffix(canonical, "\n") {
		canonical = strings.TrimSuffix(canonical, "\n")
	}
	if strings.HasSuffix(canonical, "\n") {
		return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("commit-message-noncanonical")
	}
	lines := strings.Split(canonical, "\n")
	names := []string{
		"A2A-Operation-Key", "A2A-Plan-Digest", "A2A-Target",
		"A2A-Prepared-Digest", "A2A-Recovery-Version",
		"A2A-Recovery-Digest", "A2A-Recovery-v1",
	}
	if len(lines) < len(names) {
		return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("recovery-trailers-missing")
	}
	values := make([]string, len(names))
	start := len(lines) - len(names)
	for i, name := range names {
		prefix := name + ": "
		if !strings.HasPrefix(lines[start+i], prefix) {
			return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("recovery-trailer-order-or-shape")
		}
		values[i] = strings.TrimPrefix(lines[start+i], prefix)
		if values[i] == "" || strings.TrimSpace(values[i]) != values[i] {
			return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("recovery-trailer-empty-or-spaced")
		}
	}
	for _, line := range lines[:start] {
		trimmed := strings.TrimSpace(line)
		for _, name := range names {
			if strings.HasPrefix(trimmed, name+":") {
				return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("recovery-trailer-duplicate")
			}
		}
	}
	if values[4] != "1" {
		return remoteRecoveryTrailers{}, "", remoteRecoveryConflict("recovery-version-invalid")
	}
	return remoteRecoveryTrailers{
		operationKey: values[0], planDigest: values[1], target: values[2],
		preparedDigest: values[3], recoveryDigest: values[5], recoveryV1: values[6],
	}, canonical, nil
}

func recoveryCommitMessageBase(canonical string) (string, bool) {
	lines := strings.Split(canonical, "\n")
	const trailerCount = 7
	if len(lines) < trailerCount {
		return "", false
	}
	start := len(lines) - trailerCount
	if start == 0 {
		return "", true
	}
	if lines[start-1] != "" {
		return "", false
	}
	return strings.Join(lines[:start-1], "\n"), true
}

func verifyRemoteRecoveryMetadata(
	prepared PreparedSubmission,
	record RecoveryV1,
	trailers remoteRecoveryTrailers,
) error {
	if _, err := DecodeRecoveryV1(prepared.data.recoveryJSON); err != nil {
		return remoteRecoveryConflict("prepared-recovery-invalid")
	}
	if trailers.operationKey != record.OperationKey ||
		trailers.planDigest != record.PlanDigest ||
		trailers.target != record.Target ||
		trailers.preparedDigest != record.PreparedDigest ||
		trailers.recoveryDigest != prepared.data.recoveryDigest {
		return remoteRecoveryConflict("typed-trailer-mismatch")
	}
	canonical, err := EncodeRecoveryV1(record)
	if err != nil || !bytes.Equal(canonical, prepared.data.recoveryJSON) {
		return remoteRecoveryConflict("recovery-record-mismatch")
	}
	request := prepared.HostRequest()
	if record.Repository != repositoryIdentity(request.Repository) ||
		record.HeadBranch != request.HeadBranch || record.BaseBranch != request.BaseBranch ||
		record.PRTitle != request.PRTitle || record.PRBody != request.PRBody ||
		record.OperationKey != prepared.data.operationKey ||
		record.PreparedDigest != prepared.data.preparedDigest {
		return remoteRecoveryConflict("prepared-binding-mismatch")
	}
	return nil
}

func validateRemoteRecoveryMutations(mutations []Mutation) error {
	seen := make(map[string]struct{}, len(mutations))
	for _, mutation := range mutations {
		if !isCleanRelativePath(mutation.Path) {
			return remoteRecoveryConflict("mutation-path-invalid")
		}
		if _, duplicate := seen[mutation.Path]; duplicate {
			return remoteRecoveryConflict("mutation-path-duplicate")
		}
		seen[mutation.Path] = struct{}{}
		switch mutation.Operation {
		case MutationWrite:
			if mutation.Bytes == nil {
				return remoteRecoveryConflict("write-bytes-missing")
			}
		case MutationDelete:
			if mutation.Bytes != nil {
				return remoteRecoveryConflict("delete-bytes-present")
			}
		default:
			return remoteRecoveryConflict("mutation-operation-invalid")
		}
	}
	return nil
}

func verifyRemoteRecoveryTree(mutations []Mutation, observed map[string]host.RemoteRecoveryChange) error {
	if len(observed) != len(mutations) {
		return remoteRecoveryConflict("tree-path-set-mismatch")
	}
	for _, mutation := range mutations {
		change, exists := observed[mutation.Path]
		switch mutation.Operation {
		case MutationWrite:
			wantMode := mutationResultMode(mutation)
			if !exists || change.AfterDigest != remoteRecoveryFileDigest(mutation.Bytes) || change.AfterMode != wantMode {
				return remoteRecoveryConflict("tree-write-mismatch")
			}
			if mutation.ExpectedBefore != nil &&
				(change.BeforeDigest != mutation.ExpectedBefore.Digest || change.BeforeMode != mutation.ExpectedBefore.Mode) {
				return remoteRecoveryConflict("tree-write-precondition-mismatch")
			}
		case MutationDelete:
			if !exists || change.AfterDigest != "" || change.AfterMode != "" || mutation.ExpectedBefore == nil ||
				change.BeforeDigest != mutation.ExpectedBefore.Digest || change.BeforeMode != mutation.ExpectedBefore.Mode {
				return remoteRecoveryConflict("tree-delete-mismatch")
			}
		default:
			return remoteRecoveryConflict("mutation-operation-invalid")
		}
	}
	return nil
}

func remoteRecoveryFileDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validRemoteRecoveryOID(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	for _, ch := range oid {
		if !('0' <= ch && ch <= '9') && !('a' <= ch && ch <= 'f') {
			return false
		}
	}
	return true
}

func remoteRecoveryConflict(reason string) error {
	return fmt.Errorf("%w: remote recovery %s", ErrOperationConflict, reason)
}
