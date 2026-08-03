package space

import (
	"context"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

const (
	workPublishErrorPrepared = "prepared-journal-invalid"
	workPublishErrorResult   = "write-result-invalid"
	workPublishErrorRuntime  = "submission-runtime-unavailable"
	workPublishErrorShared   = "shared-write"
	workPublishErrorPending  = "shared-write-incomplete"
)

var ErrWorkPublishIncomplete = errors.New("space: shared work checkpoint did not converge")

// PreparedWorkSubmitter is the consumer-side seam used by WorkPublisher.
// WriteFunnel satisfies it without exposing any of its preparation rules to
// the workreport state machine.
type PreparedWorkSubmitter interface {
	SubmitPrepared(context.Context, PreparedSubmission, SubmissionRuntime) (WriteResult, error)
}

// WorkSubmissionRuntimeProvider refreshes invocation-local facts that may not
// be persisted in a work lease: mirror path, remote URL and credentials plus
// the current target and authoritative floor.
type WorkSubmissionRuntimeProvider interface {
	SubmissionRuntime(context.Context) (SubmissionRuntime, error)
}

// WorkPublisher is the strict adapter between workreport's opaque journals
// and the P4 prepared-submission funnel. It never re-prepares a candidate.
type WorkPublisher struct {
	submitter PreparedWorkSubmitter
	runtime   WorkSubmissionRuntimeProvider
}

func NewWorkPublisher(submitter PreparedWorkSubmitter, runtime WorkSubmissionRuntimeProvider) (*WorkPublisher, error) {
	if submitter == nil || runtime == nil {
		return nil, fmt.Errorf("space: work publisher submitter and runtime provider are required")
	}
	return &WorkPublisher{submitter: submitter, runtime: runtime}, nil
}

func (p *WorkPublisher) SubmitPrepared(
	ctx context.Context,
	preparedJournal workreport.PreparedJournal,
	priorJournal workreport.WriteResultJournal,
) (workreport.PublishAttempt, error) {
	prepared, err := DecodePreparedSubmissionJournal(preparedJournal.Bytes())
	if err != nil {
		return failedWorkPublishAttempt(baseWriteResult("", nil), workPublishErrorPrepared, err)
	}

	prior := baseWriteResult(prepared.Branch(), prepared.ArtifactIDs())
	if priorJournal.Len() != 0 {
		prior, err = DecodeWriteResultJournal(priorJournal.Bytes())
		if err != nil {
			return failedWorkPublishAttempt(baseWriteResult(prepared.Branch(), prepared.ArtifactIDs()), workPublishErrorResult, err)
		}
	}

	runtime, err := p.runtime.SubmissionRuntime(ctx)
	if err != nil {
		return failedWorkPublishAttempt(prior, workPublishErrorRuntime, err)
	}
	runtime.PriorResult = prior

	result, submitErr := p.submitter.SubmitPrepared(ctx, prepared, runtime)
	journal, encodeErr := EncodeWriteResultJournal(result)
	if encodeErr != nil {
		attempt, fallbackErr := failedWorkPublishAttempt(prior, workPublishErrorResult, encodeErr)
		return attempt, errors.Join(submitErr, fallbackErr)
	}
	resultJournal, journalErr := workreport.NewWriteResultJournal(journal)
	if journalErr != nil {
		attempt, fallbackErr := failedWorkPublishAttempt(prior, workPublishErrorResult, journalErr)
		return attempt, errors.Join(submitErr, fallbackErr)
	}

	convergence := convergenceForWorkWrite(result, submitErr)
	if submitErr == nil && convergence == workreport.ConvergenceResumable {
		submitErr = ErrWorkPublishIncomplete
	}
	attempt := workreport.PublishAttempt{
		Attempted:   true,
		WriteResult: resultJournal,
		Convergence: convergence,
	}
	if submitErr != nil {
		attempt.ErrorCode = workPublishErrorShared
		if errors.Is(submitErr, ErrWorkPublishIncomplete) {
			attempt.ErrorCode = workPublishErrorPending
		}
	}
	return attempt, submitErr
}

func convergenceForWorkWrite(result WriteResult, err error) workreport.Convergence {
	if err != nil {
		return workreport.ConvergenceResumable
	}
	switch result.State {
	case WriteStateMerged, WriteStateAlreadyMerged:
		if result.Stage == WriteStageMerged {
			return workreport.ConvergenceTerminal
		}
	case WriteStatePendingMerge:
		if result.Stage == WriteStageAutoMergeArmed {
			return workreport.ConvergenceAccepted
		}
	case WriteStateAlreadyOpen:
		if result.Stage == WriteStageAutoMergeArmed {
			return workreport.ConvergenceAccepted
		}
	}
	return workreport.ConvergenceResumable
}

func failedWorkPublishAttempt(result WriteResult, code string, cause error) (workreport.PublishAttempt, error) {
	raw, err := EncodeWriteResultJournal(result)
	if err != nil {
		return workreport.PublishAttempt{}, errors.Join(cause, err)
	}
	journal, err := workreport.NewWriteResultJournal(raw)
	if err != nil {
		return workreport.PublishAttempt{}, errors.Join(cause, err)
	}
	return workreport.PublishAttempt{
		Attempted:   true,
		WriteResult: journal,
		Convergence: workreport.ConvergenceResumable,
		ErrorCode:   code,
	}, cause
}
