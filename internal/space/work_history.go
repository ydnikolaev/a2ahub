package space

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

// WorkLeaseLister is the bounded local-store seam needed to recover the exact
// accepted checkpoint that has not landed on main yet.
// WorkLeaseLister is part of the public package API.
type WorkLeaseLister interface {
	ListWork(context.Context, string, string, string, bool) ([]workreport.Lease, error)
}

// WorkLeaseContinuationResolver is part of the public package API.
type WorkLeaseContinuationResolver struct {
	store     WorkLeaseLister
	projectID string
	spaceID   string
	repoDir   string
}

// NewWorkLeaseContinuationResolver is part of the public package API.
func NewWorkLeaseContinuationResolver(store WorkLeaseLister, projectID, spaceID, repoDir string) (*WorkLeaseContinuationResolver, error) {
	if store == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(spaceID) == "" || strings.TrimSpace(repoDir) == "" {
		return nil, fmt.Errorf("space: work continuation resolver requires store, project, space and mirror")
	}
	return &WorkLeaseContinuationResolver{store: store, projectID: projectID, spaceID: spaceID, repoDir: repoDir}, nil
}

// ResolveWorkContinuation is part of the public package API.
func (r *WorkLeaseContinuationResolver) ResolveWorkContinuation(ctx context.Context, workID string, sequence uint64) (WorkCheckpointContinuation, bool, error) {
	leases, err := r.store.ListWork(ctx, r.projectID, r.spaceID, workID, true)
	if err != nil {
		return WorkCheckpointContinuation{}, false, err
	}
	var found *WorkCheckpointContinuation
	for _, lease := range leases {
		if lease.LastResult == nil || lease.LastResult.SemanticSequence != sequence {
			continue
		}
		result, err := DecodeWriteResultJournal(lease.LastResult.WriteResult.Bytes())
		if err != nil {
			return WorkCheckpointContinuation{}, false, fmt.Errorf("space: decode accepted work continuation: %w", err)
		}
		if !validRemoteRecoveryOID(strings.ToLower(result.CommitSHA)) {
			return WorkCheckpointContinuation{}, false, fmt.Errorf("space: accepted work continuation has invalid commit")
		}
		parsedID, err := artifact.ParseID(lease.LastResult.ArtifactID)
		if err != nil || parsedID.Prefix != "XA" || parsedID.System != lease.Identity.Actor.System {
			return WorkCheckpointContinuation{}, false, fmt.Errorf("space: accepted work continuation has invalid artifact identity")
		}
		artifactRecorded := false
		for _, id := range result.ArtifactIDs {
			artifactRecorded = artifactRecorded || id == lease.LastResult.ArtifactID
		}
		if !artifactRecorded {
			return WorkCheckpointContinuation{}, false, fmt.Errorf("space: accepted work continuation artifact is absent from write result")
		}
		layout, err := NewLayout(lease.Identity.Actor.System)
		if err != nil {
			return WorkCheckpointContinuation{}, false, err
		}
		file, err := contractGitReadPath(ctx, r.repoDir, result.CommitSHA, layout.Exchange(lease.LastResult.ArtifactID), maxConfigBytes)
		if err != nil {
			return WorkCheckpointContinuation{}, false, fmt.Errorf("space: read accepted work continuation: %w", err)
		}
		candidate := WorkCheckpointContinuation{ArtifactID: lease.LastResult.ArtifactID, Artifact: append([]byte(nil), file.Raw...)}
		if found != nil {
			return WorkCheckpointContinuation{}, false, fmt.Errorf("space: ambiguous accepted work continuation for %s", workID)
		}
		found = &candidate
	}
	if found == nil {
		return WorkCheckpointContinuation{}, false, nil
	}
	return *found, true, nil
}

// WorkHistoryPublisher short-circuits an exact terminal checkpoint already
// present in the local origin/main first-parent tree before the delegate asks
// for credentials or contacts the host. All prepared write bytes must match.
// WorkHistoryPublisher is part of the public package API.
type WorkHistoryPublisher struct {
	repoDir  string
	delegate workreport.Publisher
}

// NewWorkHistoryPublisher is part of the public package API.
func NewWorkHistoryPublisher(repoDir string, delegate workreport.Publisher) (*WorkHistoryPublisher, error) {
	if strings.TrimSpace(repoDir) == "" || delegate == nil {
		return nil, fmt.Errorf("space: work history publisher requires mirror and delegate")
	}
	return &WorkHistoryPublisher{repoDir: repoDir, delegate: delegate}, nil
}

// SubmitPrepared is part of the public package API.
func (p *WorkHistoryPublisher) SubmitPrepared(ctx context.Context, preparedJournal workreport.PreparedJournal, priorJournal workreport.WriteResultJournal) (workreport.PublishAttempt, error) {
	prepared, err := DecodePreparedSubmissionJournal(preparedJournal.Bytes())
	if err != nil {
		return p.delegate.SubmitPrepared(ctx, preparedJournal, priorJournal)
	}
	if prepared.OperationKey() == "" {
		return p.delegate.SubmitPrepared(ctx, preparedJournal, priorJournal)
	}
	mainCommit, err := contractGitResolveCommit(ctx, p.repoDir, "origin/main")
	if err == nil {
		matched := true
		introducedBy := ""
		for _, mutation := range prepared.Mutations() {
			if mutation.Operation != MutationWrite {
				matched = false
				break
			}
			file, readErr := contractGitReadPath(ctx, p.repoDir, mainCommit, mutation.Path, len(mutation.Bytes))
			if readErr != nil || !bytes.Equal(file.Raw, mutation.Bytes) {
				matched = false
				break
			}
			commit, commitErr := contractGitBytes(ctx, p.repoDir, 128,
				"log", "--first-parent", "-1", "--format=%H", "origin/main", "--", mutation.Path)
			commitID := strings.ToLower(strings.TrimSpace(string(commit)))
			if commitErr != nil || !validRemoteRecoveryOID(commitID) || (introducedBy != "" && introducedBy != commitID) {
				matched = false
				break
			}
			introducedBy = commitID
		}
		if matched && introducedBy != "" {
			parentRaw, parentErr := contractGitBytes(ctx, p.repoDir, 128,
				"rev-parse", "--verify", introducedBy+"^1")
			if parentErr == nil {
				parent := strings.ToLower(strings.TrimSpace(string(parentRaw)))
				for _, mutation := range prepared.Mutations() {
					entry, found, entryErr := contractGitExactTreeEntry(ctx, p.repoDir, parent, mutation.Path)
					if entryErr != nil {
						matched = false
						break
					}
					if !found {
						continue
					}
					prior, priorErr := contractGitReadBlob(ctx, p.repoDir, entry, len(mutation.Bytes))
					if priorErr != nil || bytes.Equal(prior, mutation.Bytes) {
						matched = false
						break
					}
				}
			}
		}
		if matched && introducedBy != "" {
			message, messageErr := contractGitBytes(ctx, p.repoDir, maxTreeCommitMetadataBytes,
				"show", "-s", "--format=%B", introducedBy)
			matched = messageErr == nil && strings.Contains("\n"+strings.TrimSpace(string(message))+"\n", "\nA2A-Operation-Key: "+prepared.OperationKey()+"\n")
		}
		if matched {
			result := WriteResult{ArtifactIDs: prepared.ArtifactIDs(), Branch: prepared.Branch(), CommitSHA: introducedBy, Stage: WriteStageMerged, State: WriteStateAlreadyMerged}
			if priorJournal.Len() != 0 {
				if prior, decodeErr := DecodeWriteResultJournal(priorJournal.Bytes()); decodeErr == nil {
					result.PRNumber, result.PRURL, result.MergeMethod = prior.PRNumber, prior.PRURL, prior.MergeMethod
				}
			}
			result, err = finalizeWriteResult(result)
			if err == nil {
				raw, encodeErr := EncodeWriteResultJournal(result)
				if encodeErr == nil {
					journal, journalErr := workreport.NewWriteResultJournal(raw)
					if journalErr == nil {
						return workreport.PublishAttempt{Attempted: true, WriteResult: journal, Convergence: workreport.ConvergenceTerminal}, nil
					}
				}
			}
		}
	}
	return p.delegate.SubmitPrepared(ctx, preparedJournal, priorJournal)
}

var _ WorkCheckpointContinuationResolver = (*WorkLeaseContinuationResolver)(nil)
var _ workreport.Publisher = (*WorkHistoryPublisher)(nil)
