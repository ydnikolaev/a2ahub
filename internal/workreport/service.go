package workreport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

const maxCASAttempts = 16

// Service is part of the public package API.
type Service struct {
	repository LeaseRepository
	publisher  Publisher
	clock      Clock
}

// NewService is part of the public package API.
func NewService(repository LeaseRepository, publisher Publisher, clock Clock) (*Service, error) {
	if repository == nil || publisher == nil || clock == nil {
		return nil, fmt.Errorf("workreport: repository, publisher and clock are required")
	}
	return &Service{repository: repository, publisher: publisher, clock: clock}, nil
}

// Start is part of the public package API.
func (s *Service) Start(ctx context.Context, command StartCommand) (OperationResult, error) {
	now := s.clock.Now().UTC()
	if err := validateTTL(command.TTL); err != nil {
		return emptyResult(command.Identity, command.Prepared), err
	}
	if command.Prepared.Action != ActionStart || command.Prepared.SemanticSequence != 1 || command.Prepared.LocalTarget != TargetActive {
		return emptyResult(command.Identity, command.Prepared), fmt.Errorf("%w: start prepared operation mismatch", ErrSequenceConflict)
	}
	if command.Mode != ModePlanning && command.Mode != ModeImplementing && command.Mode != ModeTesting && command.Mode != ModeReviewing {
		return emptyResult(command.Identity, command.Prepared), fmt.Errorf("%w: start requires a nonterminal active mode", ErrInvalidLease)
	}
	recipients, classification, err := normalizeStartAudience(command.Recipients, command.Classification)
	if err != nil {
		return emptyResult(command.Identity, command.Prepared), err
	}
	lease := Lease{
		SchemaVersion:     SchemaVersion,
		Identity:          command.Identity,
		SubjectRef:        command.SubjectRef,
		Mode:              command.Mode,
		Summary:           command.Summary,
		WaitingOn:         append([]WaitingOn(nil), command.WaitingOn...),
		Recipients:        recipients,
		Classification:    classification,
		StartedAt:         now,
		RenewedAt:         now,
		ExpiresAt:         now.Add(command.TTL),
		HeartbeatSequence: 1,
		SemanticSequence:  1,
		Pending:           newPending(command.Prepared),
	}
	if err := ValidateLease(lease); err != nil {
		return emptyResult(command.Identity, command.Prepared), err
	}
	if _, err := s.repository.CompareAndSwap(ctx, command.Identity.LeaseKey, "", &lease); err != nil {
		if errors.Is(err, ErrSequenceConflict) {
			existing, _, loadErr := s.repository.Load(ctx, command.Identity.LeaseKey)
			if loadErr == nil && existing.OwnedBy(command.Identity) && existing.Pending != nil &&
				samePrepared(*existing.Pending, command.Prepared) {
				return s.submit(ctx, existing.Identity, command.Prepared.OperationKey, resultFor(existing, localState(existing)))
			}
			if loadErr == nil && existing.OwnedBy(command.Identity) && existing.LastResult != nil &&
				receiptMatches(*existing.LastResult, command.Prepared) {
				return replayResult(existing, command.Prepared, *existing.LastResult), nil
			}
			return emptyResult(command.Identity, command.Prepared), ErrLeaseExists
		}
		return emptyResult(command.Identity, command.Prepared), err
	}
	result := resultFor(lease, LocalActive)
	return s.submit(ctx, lease.Identity, command.Prepared.OperationKey, result)
}

// Apply is part of the public package API.
func (s *Service) Apply(ctx context.Context, command SemanticCommand) (OperationResult, error) {
	if command.Prepared.Action == ActionStart || !command.Prepared.Action.Valid() {
		return emptyResult(command.Identity, command.Prepared), fmt.Errorf("%w: invalid continuation action", ErrInvalidLease)
	}
	if err := validateTTL(command.TTL); err != nil {
		return emptyResult(command.Identity, command.Prepared), err
	}
	if err := validateActionMode(command.Prepared.Action, command.Mode); err != nil {
		return emptyResult(command.Identity, command.Prepared), err
	}
	lease, revision, err := s.repository.Load(ctx, command.Identity.LeaseKey)
	if err != nil {
		return emptyResult(command.Identity, command.Prepared), err
	}
	now := s.clock.Now().UTC()
	state, err := preflightSemanticContinuation(lease, command.Identity, now, &command.Prepared)
	if err != nil {
		if errors.Is(err, ErrLeaseNotOwned) {
			return emptyResult(command.Identity, command.Prepared), err
		}
		return resultForError(lease, state, err), err
	}
	if lease.Pending != nil {
		return s.submit(ctx, lease.Identity, command.Prepared.OperationKey, resultFor(lease, state))
	}
	if command.Prepared.SemanticSequence == lease.SemanticSequence {
		if lease.LastResult != nil && receiptMatches(*lease.LastResult, command.Prepared) {
			return replayResult(lease, command.Prepared, *lease.LastResult), nil
		}
		return resultFor(lease, LocalUnchanged), ErrSequenceConflict
	}
	if command.Prepared.SemanticSequence != lease.SemanticSequence+1 {
		return resultFor(lease, LocalUnchanged), ErrSequenceConflict
	}
	if command.Prepared.Action == ActionStop {
		if command.Prepared.LocalTarget != TargetClosing {
			return resultFor(lease, LocalUnchanged), fmt.Errorf("%w: stop must target closing", ErrInvalidLease)
		}
		lease.Closing = true
	} else if command.Prepared.LocalTarget != TargetActive {
		return resultFor(lease, LocalUnchanged), fmt.Errorf("%w: continuation must target active", ErrInvalidLease)
	}
	lease.SubjectRef = command.SubjectRef
	lease.Mode = command.Mode
	lease.Summary = command.Summary
	lease.WaitingOn = append([]WaitingOn(nil), command.WaitingOn...)
	lease.RenewedAt = now
	lease.ExpiresAt = now.Add(command.TTL)
	lease.SemanticSequence = command.Prepared.SemanticSequence
	lease.Pending = newPending(command.Prepared)
	lease.LastResult = nil
	if err := ValidateLease(lease); err != nil {
		return resultFor(lease, LocalUnchanged), err
	}
	if _, err := s.repository.CompareAndSwap(ctx, lease.Identity.LeaseKey, revision, &lease); err != nil {
		return resultFor(lease, LocalUnchanged), err
	}
	return s.submit(ctx, lease.Identity, command.Prepared.OperationKey, resultFor(lease, localState(lease)))
}

// preflightSemanticContinuation centralizes the stable pre-CAS refusal order
// used by both Coordinator and Service. A nil prepared value is a fresh
// coordinator request and therefore cannot replay a pending operation. Service
// supplies the final prepared value so an exact pending retry remains legal;
// its later CAS is still authoritative over races after this observation.
func preflightSemanticContinuation(
	lease Lease,
	identity Identity,
	now time.Time,
	prepared *PreparedOperation,
) (LocalState, error) {
	if !lease.OwnedBy(identity) {
		return LocalUnchanged, ErrLeaseNotOwned
	}
	if lease.Expired(now) {
		return LocalUnchanged, ErrLeaseExpired
	}
	if now.Before(lease.RenewedAt) {
		return LocalUnchanged, ErrClockBackward
	}
	if lease.Closing {
		return LocalClosing, ErrLeaseClosing
	}
	if lease.Pending != nil {
		if prepared != nil && samePrepared(*lease.Pending, *prepared) {
			return localState(lease), nil
		}
		return localState(lease), &PendingOperationError{OperationKey: lease.Pending.OperationKey, Action: lease.Pending.Action}
	}
	if !leaseAudienceKnown(lease) {
		return localState(lease), ErrLegacyAudience
	}
	return localState(lease), nil
}

// Heartbeat is part of the public package API.
func (s *Service) Heartbeat(ctx context.Context, identity Identity, ttl time.Duration) (OperationResult, error) {
	if err := validateTTL(ttl); err != nil {
		return OperationResult{WorkID: identity.WorkID, Session: identity.Actor.Session, LocalState: LocalUnchanged}, err
	}
	lease, revision, err := s.repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		return OperationResult{WorkID: identity.WorkID, Session: identity.Actor.Session, LocalState: LocalUnchanged}, err
	}
	if !lease.OwnedBy(identity) {
		return OperationResult{WorkID: identity.WorkID, Session: identity.Actor.Session, LocalState: LocalUnchanged}, ErrLeaseNotOwned
	}
	now := s.clock.Now().UTC()
	if lease.Closing {
		return resultFor(lease, LocalClosing), ErrLeaseClosing
	}
	if lease.Expired(now) {
		return resultFor(lease, LocalUnchanged), ErrLeaseExpired
	}
	if now.Before(lease.RenewedAt) {
		return resultFor(lease, LocalUnchanged), ErrClockBackward
	}
	nextExpiry := now.Add(ttl)
	if nextExpiry.Before(lease.ExpiresAt) {
		return resultFor(lease, LocalUnchanged), ErrSequenceConflict
	}
	lease.RenewedAt = now
	lease.ExpiresAt = nextExpiry
	lease.HeartbeatSequence++
	if err := ValidateLease(lease); err != nil {
		return resultFor(lease, LocalUnchanged), err
	}
	if _, err := s.repository.CompareAndSwap(ctx, identity.LeaseKey, revision, &lease); err != nil {
		return resultFor(lease, LocalUnchanged), err
	}
	return resultFor(lease, LocalActive), nil
}

// Resume is part of the public package API.
func (s *Service) Resume(ctx context.Context, identity Identity) (OperationResult, error) {
	lease, _, err := s.repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		return OperationResult{WorkID: identity.WorkID, Session: identity.Actor.Session, LocalState: LocalUnchanged}, err
	}
	if !lease.OwnedBy(identity) {
		return OperationResult{WorkID: identity.WorkID, Session: identity.Actor.Session, LocalState: LocalUnchanged}, ErrLeaseNotOwned
	}
	if lease.Pending == nil {
		return resultFor(lease, localState(lease)), ErrNoPendingOperation
	}
	if lease.Pending.Shared.Attempted &&
		((lease.Pending.Action != ActionStop && (lease.Pending.Shared.Convergence == ConvergenceAccepted || lease.Pending.Shared.Convergence == ConvergenceTerminal)) ||
			(lease.Pending.Action == ActionStop && lease.Pending.Shared.Convergence == ConvergenceTerminal)) {
		state, finalizeErr := s.finalizeStored(ctx, identity, lease.Pending.OperationKey)
		result := resultFor(lease, state)
		return result, finalizeErr
	}
	return s.submit(ctx, lease.Identity, lease.Pending.OperationKey, resultFor(lease, localState(lease)))
}

func (s *Service) submit(ctx context.Context, identity Identity, operationKey string, result OperationResult) (OperationResult, error) {
	lease, _, err := s.repository.Load(ctx, identity.LeaseKey)
	if err != nil {
		return result, err
	}
	if !lease.OwnedBy(identity) {
		return result, ErrLeaseNotOwned
	}
	if lease.Pending == nil || lease.Pending.OperationKey != operationKey {
		return result, ErrNoPendingOperation
	}
	prepared := PreparedJournal{raw: lease.Pending.Prepared.Bytes()}
	priorResult := WriteResultJournal{raw: lease.Pending.Shared.WriteResult.Bytes()}
	attempt, publishErr := s.publisher.SubmitPrepared(ctx, prepared, priorResult)
	if publishErr != nil {
		attempt.Convergence = ConvergenceResumable
	}
	if err := validateAttempt(attempt); err != nil {
		return result, errors.Join(publishErr, err)
	}
	stored, storeErr := s.persistAttempt(ctx, identity, operationKey, attempt)
	result.Shared = attempt.clone()
	result.LocalState = stored
	if storeErr != nil {
		return result, errors.Join(publishErr, ErrResultPersistence, storeErr)
	}
	return result, publishErr
}

func (s *Service) persistAttempt(ctx context.Context, identity Identity, operationKey string, attempt PublishAttempt) (LocalState, error) {
	for range maxCASAttempts {
		lease, revision, err := s.repository.Load(ctx, identity.LeaseKey)
		if err != nil {
			return LocalUnchanged, err
		}
		if !lease.OwnedBy(identity) {
			return LocalUnchanged, ErrLeaseNotOwned
		}
		if lease.Pending == nil || lease.Pending.OperationKey != operationKey {
			return localState(lease), ErrNoPendingOperation
		}
		lease.Pending.Shared = attempt.clone()
		lease.Pending.LastErrorCode = attempt.ErrorCode
		if err := ValidateLease(lease); err != nil {
			return localState(lease), err
		}
		if _, err := s.repository.CompareAndSwap(ctx, identity.LeaseKey, revision, &lease); err != nil {
			if errors.Is(err, ErrSequenceConflict) {
				continue
			}
			return localState(lease), err
		}
		if (lease.Pending.Action != ActionStop && (attempt.Convergence == ConvergenceAccepted || attempt.Convergence == ConvergenceTerminal)) ||
			(lease.Pending.Action == ActionStop && attempt.Convergence == ConvergenceTerminal) {
			return s.finalizeStored(ctx, identity, operationKey)
		}
		return localState(lease), nil
	}
	return LocalUnchanged, ErrSequenceConflict
}

func (s *Service) finalizeStored(ctx context.Context, identity Identity, operationKey string) (LocalState, error) {
	for range maxCASAttempts {
		lease, revision, err := s.repository.Load(ctx, identity.LeaseKey)
		if err != nil {
			return LocalUnchanged, err
		}
		if !lease.OwnedBy(identity) {
			return LocalUnchanged, ErrLeaseNotOwned
		}
		if lease.Pending == nil || lease.Pending.OperationKey != operationKey || !lease.Pending.Shared.Attempted {
			return localState(lease), ErrNoPendingOperation
		}
		if lease.Pending.Action == ActionStop {
			if lease.Pending.Shared.Convergence != ConvergenceTerminal {
				return LocalClosing, nil
			}
			if _, err := s.repository.CompareAndSwap(ctx, identity.LeaseKey, revision, nil); err != nil {
				if errors.Is(err, ErrSequenceConflict) {
					continue
				}
				return LocalClosing, err
			}
			return LocalCleared, nil
		}
		if lease.Pending.Shared.Convergence != ConvergenceAccepted && lease.Pending.Shared.Convergence != ConvergenceTerminal {
			return LocalActive, nil
		}
		completed := lease.Pending
		lease.LastResult = &PublicationReceipt{
			OperationKey: completed.OperationKey, Action: completed.Action, ArtifactID: completed.ArtifactID,
			EventID: completed.EventID, SemanticSequence: completed.SemanticSequence,
			PreparedDigest: digestPrepared(completed.Prepared), WriteResult: WriteResultJournal{raw: completed.Shared.WriteResult.Bytes()},
			Convergence: completed.Shared.Convergence,
		}
		lease.Pending = nil
		if err := ValidateLease(lease); err != nil {
			return LocalActive, err
		}
		if _, err := s.repository.CompareAndSwap(ctx, identity.LeaseKey, revision, &lease); err != nil {
			if errors.Is(err, ErrSequenceConflict) {
				continue
			}
			return LocalActive, err
		}
		return LocalActive, nil
	}
	return LocalUnchanged, ErrSequenceConflict
}

func newPending(prepared PreparedOperation) *PendingOperation {
	return &PendingOperation{
		OperationKey:     prepared.OperationKey,
		Action:           prepared.Action,
		ArtifactID:       prepared.ArtifactID,
		EventID:          prepared.EventID,
		SemanticSequence: prepared.SemanticSequence,
		Prepared:         PreparedJournal{raw: prepared.Prepared.Bytes()},
		LocalTarget:      prepared.LocalTarget,
	}
}

func samePrepared(pending PendingOperation, prepared PreparedOperation) bool {
	return pending.OperationKey == prepared.OperationKey && pending.Action == prepared.Action &&
		pending.ArtifactID == prepared.ArtifactID && pending.EventID == prepared.EventID &&
		pending.SemanticSequence == prepared.SemanticSequence && pending.LocalTarget == prepared.LocalTarget &&
		bytes.Equal(pending.Prepared.raw, prepared.Prepared.raw)
}

func receiptMatches(receipt PublicationReceipt, prepared PreparedOperation) bool {
	return receipt.OperationKey == prepared.OperationKey && receipt.Action == prepared.Action &&
		receipt.ArtifactID == prepared.ArtifactID && receipt.EventID == prepared.EventID &&
		receipt.SemanticSequence == prepared.SemanticSequence && receipt.PreparedDigest == digestPrepared(prepared.Prepared)
}

func replayResult(lease Lease, prepared PreparedOperation, receipt PublicationReceipt) OperationResult {
	result := resultFor(lease, localState(lease))
	result.OperationKey = prepared.OperationKey
	result.Shared = receipt.attempt()
	return result
}

func validateAttempt(attempt PublishAttempt) error {
	if !attempt.Attempted || attempt.WriteResult.Len() == 0 || !attempt.Convergence.Valid() || !validErrorCode(attempt.ErrorCode) {
		return ErrInvalidAttempt
	}
	return nil
}

func validateTTL(ttl time.Duration) error {
	if ttl < MinimumTTL || ttl > MaximumTTL {
		return ErrInvalidTTL
	}
	return nil
}

func resultFor(lease Lease, state LocalState) OperationResult {
	result := OperationResult{
		WorkID:           lease.Identity.WorkID,
		Session:          lease.Identity.Actor.Session,
		SemanticSequence: lease.SemanticSequence,
		LocalState:       state,
		ExpiresAt:        lease.ExpiresAt,
	}
	if lease.Pending != nil {
		result.OperationKey = lease.Pending.OperationKey
		result.Action = lease.Pending.Action
		result.Shared = lease.Pending.Shared.clone()
	}
	return result
}

func resultForError(lease Lease, state LocalState, err error) OperationResult {
	result := resultFor(lease, state)
	var pending *PendingOperationError
	if errors.As(err, &pending) {
		result.OperationKey = pending.OperationKey
		result.Action = pending.Action
		result.LocalErrorCode = LocalErrorPendingOperation
	}
	return result
}

func emptyResult(identity Identity, prepared PreparedOperation) OperationResult {
	return OperationResult{
		WorkID:           identity.WorkID,
		Session:          identity.Actor.Session,
		OperationKey:     prepared.OperationKey,
		SemanticSequence: prepared.SemanticSequence,
		LocalState:       LocalUnchanged,
	}
}

func localState(lease Lease) LocalState {
	if lease.Closing {
		return LocalClosing
	}
	return LocalActive
}
