package workreport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/provenance"
)

const (
	// DefaultReportValidFor is part of the public package API.
	DefaultReportValidFor = 24 * time.Hour
	// MinimumReportValidFor is part of the public package API.
	MinimumReportValidFor = 5 * time.Minute
	// MaximumReportValidFor is part of the public package API.
	MaximumReportValidFor = 7 * 24 * time.Hour
)

// SemanticWorkService is the coordinator's semantic mutation seam. Service
// satisfies it and remains the sole owner of lease CAS and publication.
// SemanticWorkService is part of the public package API.
type SemanticWorkService interface {
	Start(context.Context, StartCommand) (OperationResult, error)
	Apply(context.Context, SemanticCommand) (OperationResult, error)
}

// LocalWorkService is deliberately separate from SemanticWorkService so
// heartbeat and restart recovery cannot accidentally acquire preparation.
// LocalWorkService is part of the public package API.
type LocalWorkService interface {
	Heartbeat(context.Context, Identity, time.Duration) (OperationResult, error)
	Resume(context.Context, Identity) (OperationResult, error)
}

// LeaseReader provides the persisted owner and semantic sequence needed to
// construct a continuation. It does not grant the coordinator mutation.
// LeaseReader is part of the public package API.
type LeaseReader interface {
	Load(context.Context, string) (Lease, Revision, error)
}

// WorkAuthoringGate reads authoritative space compatibility state. Its input
// deliberately contains no producer stamp: self-asserted produced_by data can
// never activate work authoring.
// WorkAuthoringGate is part of the public package API.
type WorkAuthoringGate interface {
	AuthorizeWork(context.Context, WorkAuthoringScope) error
}

// WorkAuthoringScope is part of the public package API.
type WorkAuthoringScope struct {
	ProjectID string
	Space     string
}

// Preparer owns render, P1 evaluation, producer stamping, exact-byte
// validation, and side-effect-free P4 preparation. It must return the final
// PreparedOperation; the coordinator never rebuilds or edits its journal.
// Preparer is part of the public package API.
type Preparer interface {
	Prepare(context.Context, PreparationRequest) (PreparedOperation, error)
}

// IDSource mints the two coordinator-owned identities. Both methods return
// their complete prefixed form: work:<ULID> and session:<ULID>.
// IDSource is part of the public package API.
type IDSource interface {
	NewWorkID(time.Time) (string, error)
	NewSessionID(time.Time) (string, error)
}

// LeaseKeySource binds the complete local identity to the adapter's
// configuration-trusted canonical project root without exposing that path in
// a durable checkpoint.
// LeaseKeySource is part of the public package API.
type LeaseKeySource interface {
	LeaseKey(Identity) (string, error)
}

// WorkIdentityInput is part of the public package API.
type WorkIdentityInput struct {
	ProjectID string
	Space     string
	Thread    string
	WorkID    string
	Actor     Actor
}

// StartInput is part of the public package API.
type StartInput struct {
	ProjectID      string
	Space          string
	Thread         string
	Actor          Actor
	SubjectRef     string
	Mode           Mode
	Summary        string
	TTL            time.Duration
	ReportValidFor time.Duration
	Recipients     []string
	Classification string
}

// CheckpointInput is part of the public package API.
type CheckpointInput struct {
	Work           WorkIdentityInput
	SubjectRef     string
	Mode           Mode
	Summary        string
	TTL            time.Duration
	ReportValidFor time.Duration
}

// WaitInput is part of the public package API.
type WaitInput struct {
	Work           WorkIdentityInput
	Summary        string
	WaitingOn      []WaitingOn
	TTL            time.Duration
	ReportValidFor time.Duration
}

// StopInput is part of the public package API.
type StopInput struct {
	Work           WorkIdentityInput
	Result         Mode
	Summary        string
	ReportValidFor time.Duration
}

// HeartbeatInput is part of the public package API.
type HeartbeatInput struct {
	Work WorkIdentityInput
	TTL  time.Duration
}

// ResumeInput is part of the public package API.
type ResumeInput struct{ Work WorkIdentityInput }

// PreparationRequest is the complete semantic intent presented to the single
// validation/preparation path. OperationKey is local retry identity and must
// not be rendered into the durable announcement.
// PreparationRequest is part of the public package API.
type PreparationRequest struct {
	Identity         Identity
	Action           Action
	OperationKey     string
	SemanticSequence uint64
	LocalTarget      LocalTarget
	SubjectRef       string
	Mode             Mode
	Summary          string
	WaitingOn        []WaitingOn
	ReportedAt       time.Time
	ValidUntil       time.Time
	Recipients       []string
	Classification   string
}

// Coordinator is part of the public package API.
type Coordinator struct {
	semantic SemanticWorkService
	local    LocalWorkService
	leases   LeaseReader
	gate     WorkAuthoringGate
	preparer Preparer
	ids      IDSource
	keys     LeaseKeySource
	clock    Clock
}

// NewCoordinator is part of the public package API.
func NewCoordinator(
	semantic SemanticWorkService,
	local LocalWorkService,
	leases LeaseReader,
	gate WorkAuthoringGate,
	preparer Preparer,
	ids IDSource,
	keys LeaseKeySource,
	clock Clock,
) (*Coordinator, error) {
	if semantic == nil || local == nil || leases == nil || gate == nil || preparer == nil || ids == nil || keys == nil || clock == nil {
		return nil, fmt.Errorf("workreport: semantic service, local service, lease reader, authoring gate, preparer, id source, lease key source and clock are required")
	}
	return &Coordinator{
		semantic: semantic,
		local:    local,
		leases:   leases,
		gate:     gate,
		preparer: preparer,
		ids:      ids,
		keys:     keys,
		clock:    clock,
	}, nil
}

// Start is part of the public package API.
func (c *Coordinator) Start(ctx context.Context, input StartInput) (OperationResult, error) {
	if err := c.authorize(ctx, input.ProjectID, input.Space); err != nil {
		return OperationResult{LocalState: LocalUnchanged}, err
	}
	ttl, err := normalizedTTL(input.TTL)
	if err != nil {
		return OperationResult{LocalState: LocalUnchanged}, err
	}
	if err := validateActionMode(ActionStart, input.Mode); err != nil {
		return OperationResult{LocalState: LocalUnchanged}, err
	}
	if err := validateReport(input.SubjectRef, input.Mode, input.Summary, nil); err != nil {
		return OperationResult{LocalState: LocalUnchanged}, err
	}

	now := c.clock.Now().UTC()
	recipients, classification, err := normalizeStartAudience(input.Recipients, input.Classification)
	if err != nil {
		return OperationResult{LocalState: LocalUnchanged}, err
	}
	validUntil, err := reportValidUntil(now, input.Mode, input.ReportValidFor)
	if err != nil {
		return OperationResult{LocalState: LocalUnchanged}, err
	}
	workID, err := c.ids.NewWorkID(now)
	if err != nil {
		return OperationResult{LocalState: LocalUnchanged}, fmt.Errorf("workreport: mint work id: %w", err)
	}
	if !workIDPattern.MatchString(workID) {
		return OperationResult{WorkID: workID, LocalState: LocalUnchanged},
			fmt.Errorf("%w: generated work id must be work:<ULID>", ErrInvalidLease)
	}
	actor := input.Actor
	if actor.Session == "" {
		actor.Session, err = c.ids.NewSessionID(now)
		if err != nil {
			return OperationResult{WorkID: workID, LocalState: LocalUnchanged}, fmt.Errorf("workreport: mint session id: %w", err)
		}
		if !validGeneratedSession(actor.Session) || !provenance.IsSafeSession(actor.Session) {
			return OperationResult{WorkID: workID, LocalState: LocalUnchanged},
				fmt.Errorf("%w: generated session must be session:<ULID>", ErrInvalidLease)
		}
	} else {
		actor.Session = normalizedSuppliedSession(actor.Session)
	}
	identity, err := c.resolveIdentity(WorkIdentityInput{
		ProjectID: input.ProjectID,
		Space:     input.Space,
		Thread:    input.Thread,
		WorkID:    workID,
		Actor:     actor,
	})
	if err != nil {
		return OperationResult{WorkID: workID, Session: actor.Session, LocalState: LocalUnchanged}, err
	}
	operationKey, err := expectedOperationKey(1, ActionStart, identity.WorkID)
	if err != nil {
		return operationIdentityResult(identity, "", 1), err
	}
	request := PreparationRequest{
		Identity:         identity,
		Action:           ActionStart,
		OperationKey:     operationKey,
		SemanticSequence: 1,
		LocalTarget:      TargetActive,
		SubjectRef:       input.SubjectRef,
		Mode:             input.Mode,
		Summary:          input.Summary,
		ReportedAt:       now,
		ValidUntil:       validUntil,
		Recipients:       recipients,
		Classification:   classification,
	}
	prepared, err := c.prepare(ctx, request)
	if err != nil {
		return operationIdentityResult(identity, operationKey, 1), err
	}
	return c.semantic.Start(ctx, StartCommand{
		Identity:   identity,
		SubjectRef: input.SubjectRef,
		Mode:       input.Mode,
		Summary:    input.Summary,
		Recipients: recipients, Classification: classification,
		TTL:      ttl,
		Prepared: prepared,
	})
}

// Checkpoint is part of the public package API.
func (c *Coordinator) Checkpoint(ctx context.Context, input CheckpointInput) (OperationResult, error) {
	if err := c.authorize(ctx, input.Work.ProjectID, input.Work.Space); err != nil {
		return inputResult(input.Work), err
	}
	ttl, err := normalizedTTL(input.TTL)
	if err != nil {
		return inputResult(input.Work), err
	}
	if err := validateActionMode(ActionCheckpoint, input.Mode); err != nil {
		return inputResult(input.Work), err
	}
	return c.continueSemantic(ctx, continuationCommand{
		work:           input.Work,
		action:         ActionCheckpoint,
		subjectRef:     input.SubjectRef,
		mode:           input.Mode,
		summary:        input.Summary,
		ttl:            ttl,
		reportValidFor: input.ReportValidFor,
	})
}

// Wait is part of the public package API.
func (c *Coordinator) Wait(ctx context.Context, input WaitInput) (OperationResult, error) {
	if err := c.authorize(ctx, input.Work.ProjectID, input.Work.Space); err != nil {
		return inputResult(input.Work), err
	}
	ttl, err := normalizedTTL(input.TTL)
	if err != nil {
		return inputResult(input.Work), err
	}
	return c.continueSemantic(ctx, continuationCommand{
		work:           input.Work,
		action:         ActionWait,
		mode:           ModeWaiting,
		summary:        input.Summary,
		waitingOn:      append([]WaitingOn(nil), input.WaitingOn...),
		ttl:            ttl,
		reportValidFor: input.ReportValidFor,
	})
}

// Stop is part of the public package API.
func (c *Coordinator) Stop(ctx context.Context, input StopInput) (OperationResult, error) {
	if err := c.authorize(ctx, input.Work.ProjectID, input.Work.Space); err != nil {
		return inputResult(input.Work), err
	}
	if err := validateActionMode(ActionStop, input.Result); err != nil {
		return inputResult(input.Work), err
	}
	return c.continueSemantic(ctx, continuationCommand{
		work:           input.Work,
		action:         ActionStop,
		mode:           input.Result,
		summary:        input.Summary,
		reportValidFor: input.ReportValidFor,
	})
}

// Heartbeat is part of the public package API.
func (c *Coordinator) Heartbeat(ctx context.Context, input HeartbeatInput) (OperationResult, error) {
	ttl, err := normalizedTTL(input.TTL)
	if err != nil {
		return inputResult(input.Work), err
	}
	identity, err := c.resolveIdentity(input.Work)
	if err != nil {
		return inputResult(input.Work), err
	}
	result, err := c.local.Heartbeat(ctx, identity, ttl)
	return normalizeResultSession(result), err
}

// Resume resolves only local ownership identity and enters Service.Resume.
// It deliberately does not consult the clock, ID source, lease reader, or
// preparer; Service replays the exact persisted prepared/result journals.
// Resume is part of the public package API.
func (c *Coordinator) Resume(ctx context.Context, input ResumeInput) (OperationResult, error) {
	identity, err := c.resolveIdentity(input.Work)
	if err != nil {
		return inputResult(input.Work), err
	}
	result, err := c.local.Resume(ctx, identity)
	return normalizeResultSession(result), err
}

type continuationCommand struct {
	work           WorkIdentityInput
	action         Action
	subjectRef     string
	mode           Mode
	summary        string
	waitingOn      []WaitingOn
	ttl            time.Duration
	reportValidFor time.Duration
}

func (c *Coordinator) continueSemantic(ctx context.Context, command continuationCommand) (OperationResult, error) {
	identity, err := c.resolveIdentity(command.work)
	if err != nil {
		return inputResult(command.work), err
	}
	lease, _, err := c.leases.Load(ctx, identity.LeaseKey)
	if err != nil {
		return operationIdentityResult(identity, "", 0), err
	}
	now := c.clock.Now().UTC()
	state, err := preflightSemanticContinuation(lease, identity, now, nil)
	if err != nil {
		if errors.Is(err, ErrLeaseNotOwned) {
			return operationIdentityResult(identity, "", 0), err
		}
		return resultForError(lease, state, err), err
	}
	if lease.SemanticSequence >= math.MaxInt64 {
		return operationIdentityResult(lease.Identity, "", lease.SemanticSequence), ErrSequenceConflict
	}

	subjectRef := command.subjectRef
	if subjectRef == "" {
		subjectRef = lease.SubjectRef
	}
	if err := validateReport(subjectRef, command.mode, command.summary, command.waitingOn); err != nil {
		return operationIdentityResult(lease.Identity, "", lease.SemanticSequence), err
	}
	recipients := append([]string(nil), lease.Recipients...)
	classification := lease.Classification
	ttl := command.ttl
	if command.action == ActionStop {
		ttl = lease.ExpiresAt.Sub(lease.RenewedAt)
		if err := validateTTL(ttl); err != nil {
			return operationIdentityResult(lease.Identity, "", lease.SemanticSequence), err
		}
	}
	validUntil, err := reportValidUntil(now, command.mode, command.reportValidFor)
	if err != nil {
		return operationIdentityResult(lease.Identity, "", lease.SemanticSequence), err
	}
	sequence := lease.SemanticSequence + 1
	operationKey, err := expectedOperationKey(sequence, command.action, lease.Identity.WorkID)
	if err != nil {
		return operationIdentityResult(lease.Identity, "", sequence), err
	}
	target := TargetActive
	if command.action == ActionStop {
		target = TargetClosing
	}
	request := PreparationRequest{
		Identity:         lease.Identity,
		Action:           command.action,
		OperationKey:     operationKey,
		SemanticSequence: sequence,
		LocalTarget:      target,
		SubjectRef:       subjectRef,
		Mode:             command.mode,
		Summary:          command.summary,
		WaitingOn:        append([]WaitingOn(nil), command.waitingOn...),
		ReportedAt:       now,
		ValidUntil:       validUntil,
		Recipients:       recipients,
		Classification:   classification,
	}
	prepared, err := c.prepare(ctx, request)
	if err != nil {
		return operationIdentityResult(lease.Identity, operationKey, sequence), err
	}
	return c.semantic.Apply(ctx, SemanticCommand{
		Identity:   lease.Identity,
		SubjectRef: subjectRef,
		Mode:       command.mode,
		Summary:    command.summary,
		WaitingOn:  append([]WaitingOn(nil), command.waitingOn...),
		TTL:        ttl,
		Prepared:   prepared,
	})
}

func normalizeStartAudience(recipients []string, classification string) ([]string, string, error) {
	if len(recipients) == 0 {
		recipients = defaultAudienceRecipients()
	}
	if classification == "" {
		classification = DefaultClassification
	}
	recipients = append([]string(nil), recipients...)
	if err := validateAudience(recipients, classification); err != nil {
		return nil, "", err
	}
	return recipients, classification, nil
}

func (c *Coordinator) prepare(ctx context.Context, request PreparationRequest) (PreparedOperation, error) {
	prepared, err := c.preparer.Prepare(ctx, clonePreparationRequest(request))
	if err != nil {
		return PreparedOperation{}, err
	}
	if prepared.OperationKey != request.OperationKey || prepared.Action != request.Action ||
		prepared.SemanticSequence != request.SemanticSequence || prepared.LocalTarget != request.LocalTarget {
		return PreparedOperation{}, fmt.Errorf("%w: preparer changed coordinator-owned operation identity", ErrSequenceConflict)
	}
	pending := PendingOperation{
		OperationKey:     prepared.OperationKey,
		Action:           prepared.Action,
		ArtifactID:       prepared.ArtifactID,
		EventID:          prepared.EventID,
		SemanticSequence: prepared.SemanticSequence,
		Prepared:         prepared.Prepared,
		LocalTarget:      prepared.LocalTarget,
	}
	if err := validatePending(pending, request.SemanticSequence, request.Identity.WorkID); err != nil {
		return PreparedOperation{}, err
	}
	if err := validateOpaqueJSON(prepared.Prepared.raw, MaximumPrepared, "prepared journal"); err != nil {
		return PreparedOperation{}, err
	}
	return prepared, nil
}

func (c *Coordinator) authorize(ctx context.Context, projectID, space string) error {
	return c.gate.AuthorizeWork(ctx, WorkAuthoringScope{ProjectID: projectID, Space: space})
}

func (c *Coordinator) resolveIdentity(input WorkIdentityInput) (Identity, error) {
	actor := input.Actor
	if actor.Session != "" {
		actor.Session = normalizedSuppliedSession(actor.Session)
	}
	identity := Identity{
		ProjectID: input.ProjectID,
		Space:     input.Space,
		Thread:    input.Thread,
		WorkID:    input.WorkID,
		Actor:     actor,
	}
	key, err := c.keys.LeaseKey(identity)
	if err != nil {
		return Identity{}, fmt.Errorf("workreport: derive lease key: %w", err)
	}
	identity.LeaseKey = key
	if err := validateIdentity(identity); err != nil {
		return Identity{}, err
	}
	return identity, nil
}

func normalizedTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if err := validateTTL(ttl); err != nil {
		return 0, err
	}
	return ttl, nil
}

func reportValidUntil(now time.Time, mode Mode, validFor time.Duration) (time.Time, error) {
	if mode == ModeFinished {
		if validFor != 0 {
			return time.Time{}, fmt.Errorf("%w: finished report forbids report validity", ErrInvalidLease)
		}
		return time.Time{}, nil
	}
	if validFor == 0 {
		validFor = DefaultReportValidFor
	}
	if validFor < MinimumReportValidFor || validFor > MaximumReportValidFor {
		return time.Time{}, fmt.Errorf("%w: report validity outside accepted range", ErrInvalidLease)
	}
	return now.Add(validFor), nil
}

func validGeneratedSession(session string) bool {
	const prefix = "session:"
	return strings.HasPrefix(session, prefix) && workIDPattern.MatchString("work:"+strings.TrimPrefix(session, prefix))
}

func normalizedSuppliedSession(session string) string {
	if provenance.IsSafeSession(session) {
		return session
	}
	sum := sha256.Sum256([]byte(session))
	return "session-sha256:" + hex.EncodeToString(sum[:])
}

// NormalizeSession returns the only session representation safe to expose or
// use as lease ownership. Canonical safe sessions are preserved; provider/raw
// values are irreversibly reduced to a deterministic digest.
// NormalizeSession is part of the public package API.
func NormalizeSession(session string) string {
	if session == "" {
		return ""
	}
	return normalizedSuppliedSession(session)
}

func clonePreparationRequest(request PreparationRequest) PreparationRequest {
	request.WaitingOn = append([]WaitingOn(nil), request.WaitingOn...)
	request.Recipients = append([]string(nil), request.Recipients...)
	return request
}

func inputResult(input WorkIdentityInput) OperationResult {
	session := input.Actor.Session
	if session != "" {
		session = normalizedSuppliedSession(session)
	}
	return OperationResult{
		WorkID: input.WorkID, Session: session, LocalState: LocalUnchanged,
	}
}

func operationIdentityResult(identity Identity, operationKey string, sequence uint64) OperationResult {
	return OperationResult{
		WorkID: identity.WorkID, Session: identity.Actor.Session, OperationKey: operationKey,
		SemanticSequence: sequence, LocalState: LocalUnchanged,
	}
}

func normalizeResultSession(result OperationResult) OperationResult {
	if result.Session != "" {
		result.Session = normalizedSuppliedSession(result.Session)
	}
	return result
}
