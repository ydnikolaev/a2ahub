package workreport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/operation"
)

const (
	// SchemaVersion is part of the public package API.
	SchemaVersion = 1
	// DefaultTTL is part of the public package API.
	DefaultTTL = 15 * time.Minute
	// MinimumTTL is part of the public package API.
	MinimumTTL = time.Minute
	// MaximumTTL is part of the public package API.
	MaximumTTL = 24 * time.Hour
	// MaximumPrepared is part of the public package API.
	MaximumPrepared = 24 * 1024
	// MaximumEncodedLease is part of the public package API.
	MaximumEncodedLease = 32 * 1024
	// DefaultClassification is part of the public package API.
	DefaultClassification = "internal"
)

func defaultAudienceRecipients() []string { return []string{"all"} }

var (
	workIDPattern = regexp.MustCompile(`^work:[0-9A-HJKMNP-TV-Z]{26}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Mode is part of the public package API.
type Mode string

const (
	// ModePlanning is part of the public package API.
	ModePlanning Mode = "planning"
	// ModeImplementing is part of the public package API.
	ModeImplementing Mode = "implementing"
	// ModeTesting is part of the public package API.
	ModeTesting Mode = "testing"
	// ModeReviewing is part of the public package API.
	ModeReviewing Mode = "reviewing"
	// ModeWaiting is part of the public package API.
	ModeWaiting Mode = "waiting"
	// ModePaused is part of the public package API.
	ModePaused Mode = "paused"
	// ModeFinished is part of the public package API.
	ModeFinished Mode = "finished"
)

// Modes returns every work mode in stable normative order. The returned slice
// is fresh so callers cannot mutate the vocabulary.
func Modes() []Mode {
	return []Mode{
		ModePlanning,
		ModeImplementing,
		ModeTesting,
		ModeReviewing,
		ModeWaiting,
		ModePaused,
		ModeFinished,
	}
}

// Valid is part of the public package API.
func (m Mode) Valid() bool {
	for _, mode := range Modes() {
		if m == mode {
			return true
		}
	}
	return false
}

// WaitKind is part of the public package API.
type WaitKind string

const (
	// WaitSystem is part of the public package API.
	WaitSystem WaitKind = "system"
	// WaitHuman is part of the public package API.
	WaitHuman WaitKind = "human"
	// WaitTool is part of the public package API.
	WaitTool WaitKind = "tool"
	// WaitTimer is part of the public package API.
	WaitTimer WaitKind = "timer"
	// WaitExternal is part of the public package API.
	WaitExternal WaitKind = "external"
)

// Valid is part of the public package API.
func (k WaitKind) Valid() bool {
	switch k {
	case WaitSystem, WaitHuman, WaitTool, WaitTimer, WaitExternal:
		return true
	default:
		return false
	}
}

// WaitingOn is part of the public package API.
type WaitingOn struct {
	Kind    WaitKind `json:"kind"`
	ID      string   `json:"id"`
	Summary string   `json:"summary,omitempty"`
}

// Actor is part of the public package API.
type Actor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session"`
}

// Identity is part of the public package API.
type Identity struct {
	LeaseKey  string
	ProjectID string
	Space     string
	Thread    string
	WorkID    string
	Actor     Actor
}

// Equal is part of the public package API.
func (i Identity) Equal(other Identity) bool {
	return i.LeaseKey == other.LeaseKey && i.ProjectID == other.ProjectID &&
		i.Space == other.Space && i.Thread == other.Thread && i.WorkID == other.WorkID &&
		i.Actor.System == other.Actor.System && i.Actor.Name == other.Actor.Name &&
		i.Actor.Session == other.Actor.Session
}

// PreparedJournal is the opaque canonical journal emitted by the P4 adapter.
// It deliberately exposes copies only: callers cannot mutate bytes persisted
// for restart recovery.
// PreparedJournal is part of the public package API.
type PreparedJournal struct{ raw []byte }

// NewPreparedJournal is part of the public package API.
func NewPreparedJournal(raw []byte) (PreparedJournal, error) {
	if err := validateOpaqueJSON(raw, MaximumPrepared, "prepared journal"); err != nil {
		return PreparedJournal{}, err
	}
	return PreparedJournal{raw: bytes.Clone(raw)}, nil
}

// Bytes is part of the public package API.
func (j PreparedJournal) Bytes() []byte { return bytes.Clone(j.raw) }

// Len is part of the public package API.
func (j PreparedJournal) Len() int { return len(j.raw) }

// WriteResultJournal is the opaque canonical P4 result. The strict adapter,
// not this package, owns its schema and maps it to a convergence class.
// WriteResultJournal is part of the public package API.
type WriteResultJournal struct{ raw []byte }

// NewWriteResultJournal is part of the public package API.
func NewWriteResultJournal(raw []byte) (WriteResultJournal, error) {
	if err := validateOpaqueJSON(raw, MaximumEncodedLease, "write result journal"); err != nil {
		return WriteResultJournal{}, err
	}
	return WriteResultJournal{raw: bytes.Clone(raw)}, nil
}

// Bytes is part of the public package API.
func (j WriteResultJournal) Bytes() []byte { return bytes.Clone(j.raw) }

// Len is part of the public package API.
func (j WriteResultJournal) Len() int { return len(j.raw) }

func validateOpaqueJSON(raw []byte, maximum int, label string) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: %s is empty", ErrInvalidLease, label)
	}
	if len(raw) > maximum {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrLeaseTooLarge, label, maximum)
	}
	if !bytes.Equal(raw, bytes.TrimSpace(raw)) || raw[0] != '{' || raw[len(raw)-1] != '}' || !json.Valid(raw) {
		return fmt.Errorf("%w: %s must be one canonical JSON object", ErrInvalidLease, label)
	}
	return nil
}

// Action is part of the public package API.
type Action string

const (
	// ActionStart is part of the public package API.
	ActionStart Action = "start"
	// ActionCheckpoint is part of the public package API.
	ActionCheckpoint Action = "checkpoint"
	// ActionWait is part of the public package API.
	ActionWait Action = "wait"
	// ActionStop is part of the public package API.
	ActionStop Action = "stop"
)

// Valid is part of the public package API.
func (a Action) Valid() bool {
	switch a {
	case ActionStart, ActionCheckpoint, ActionWait, ActionStop:
		return true
	default:
		return false
	}
}

// LocalTarget is part of the public package API.
type LocalTarget string

const (
	// TargetActive is part of the public package API.
	TargetActive LocalTarget = "active"
	// TargetClosing is part of the public package API.
	TargetClosing LocalTarget = "closing"
)

// Convergence is part of the public package API.
type Convergence string

const (
	// ConvergenceResumable is part of the public package API.
	ConvergenceResumable Convergence = "resumable"
	// ConvergenceAccepted is part of the public package API.
	ConvergenceAccepted Convergence = "accepted"
	// ConvergenceTerminal is part of the public package API.
	ConvergenceTerminal Convergence = "terminal"
)

// Valid is part of the public package API.
func (c Convergence) Valid() bool {
	return c == ConvergenceResumable || c == ConvergenceAccepted || c == ConvergenceTerminal
}

// PublishAttempt is part of the public package API.
type PublishAttempt struct {
	Attempted   bool
	WriteResult WriteResultJournal
	Convergence Convergence
	ErrorCode   string
}

func (a PublishAttempt) clone() PublishAttempt {
	return PublishAttempt{
		Attempted:   a.Attempted,
		WriteResult: WriteResultJournal{raw: bytes.Clone(a.WriteResult.raw)},
		Convergence: a.Convergence,
		ErrorCode:   a.ErrorCode,
	}
}

// PreparedOperation is part of the public package API.
type PreparedOperation struct {
	OperationKey     string
	Action           Action
	ArtifactID       string
	EventID          string
	SemanticSequence uint64
	Prepared         PreparedJournal
	LocalTarget      LocalTarget
}

// PendingOperation is part of the public package API.
type PendingOperation struct {
	OperationKey     string
	Action           Action
	ArtifactID       string
	EventID          string
	SemanticSequence uint64
	Prepared         PreparedJournal
	LocalTarget      LocalTarget
	Shared           PublishAttempt
	LastErrorCode    string
}

// PublicationReceipt is a bounded local idempotency receipt. It proves only
// which exact prepared bytes already converged; it is never projected as work
// truth and is replaced when the next semantic operation is persisted.
// PublicationReceipt is part of the public package API.
type PublicationReceipt struct {
	OperationKey     string
	Action           Action
	ArtifactID       string
	EventID          string
	SemanticSequence uint64
	PreparedDigest   string
	WriteResult      WriteResultJournal
	Convergence      Convergence
}

func (r *PublicationReceipt) clone() *PublicationReceipt {
	if r == nil {
		return nil
	}
	copy := *r
	copy.WriteResult = WriteResultJournal{raw: bytes.Clone(r.WriteResult.raw)}
	return &copy
}

func (r PublicationReceipt) attempt() PublishAttempt {
	return PublishAttempt{Attempted: true, WriteResult: WriteResultJournal{raw: r.WriteResult.Bytes()}, Convergence: r.Convergence}
}

func (p *PendingOperation) clone() *PendingOperation {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Prepared = PreparedJournal{raw: bytes.Clone(p.Prepared.raw)}
	copy.Shared = p.Shared.clone()
	return &copy
}

// Lease is part of the public package API.
type Lease struct {
	SchemaVersion int
	Identity      Identity
	SubjectRef    string
	Mode          Mode
	Summary       string
	WaitingOn     []WaitingOn
	// Recipients and Classification are both empty only for inspectable
	// schema-v1 leases written before audience persistence. That legacy state
	// may renew/replay exact pending bytes but cannot authorize new semantics.
	Recipients        []string
	Classification    string
	StartedAt         time.Time
	RenewedAt         time.Time
	ExpiresAt         time.Time
	HeartbeatSequence uint64
	SemanticSequence  uint64
	Closing           bool
	Pending           *PendingOperation
	LastResult        *PublicationReceipt
}

// Clone is part of the public package API.
func (l Lease) Clone() Lease {
	copy := l
	copy.WaitingOn = append([]WaitingOn(nil), l.WaitingOn...)
	copy.Recipients = append([]string(nil), l.Recipients...)
	copy.Pending = l.Pending.clone()
	copy.LastResult = l.LastResult.clone()
	return copy
}

// Expired is part of the public package API.
func (l Lease) Expired(now time.Time) bool { return now.After(l.ExpiresAt) }

// OwnedBy is part of the public package API.
func (l Lease) OwnedBy(identity Identity) bool { return l.Identity.Equal(identity) }

// Revision is part of the public package API.
type Revision string

// LeaseRepository supplies process-safe compare-and-swap semantics. A zero
// revision means the key must not exist. Passing next=nil removes it.
// LeaseRepository is part of the public package API.
type LeaseRepository interface {
	Load(context.Context, string) (Lease, Revision, error)
	CompareAndSwap(context.Context, string, Revision, *Lease) (Revision, error)
}

// Publisher is part of the public package API.
type Publisher interface {
	SubmitPrepared(context.Context, PreparedJournal, WriteResultJournal) (PublishAttempt, error)
}

// Clock is part of the public package API.
type Clock interface{ Now() time.Time }

// StartCommand is part of the public package API.
type StartCommand struct {
	Identity       Identity
	SubjectRef     string
	Mode           Mode
	Summary        string
	WaitingOn      []WaitingOn
	Recipients     []string
	Classification string
	TTL            time.Duration
	Prepared       PreparedOperation
}

// SemanticCommand is part of the public package API.
type SemanticCommand struct {
	Identity   Identity
	SubjectRef string
	Mode       Mode
	Summary    string
	WaitingOn  []WaitingOn
	TTL        time.Duration
	Prepared   PreparedOperation
}

// LocalState is part of the public package API.
type LocalState string

const (
	// LocalActive is part of the public package API.
	LocalActive LocalState = "active"
	// LocalClosing is part of the public package API.
	LocalClosing LocalState = "closing"
	// LocalCleared is part of the public package API.
	LocalCleared LocalState = "cleared"
	// LocalUnchanged is part of the public package API.
	LocalUnchanged LocalState = "unchanged"
)

// OperationResult is part of the public package API.
type OperationResult struct {
	WorkID           string
	Session          string
	OperationKey     string
	Action           Action
	LocalErrorCode   string
	SemanticSequence uint64
	LocalState       LocalState
	ExpiresAt        time.Time
	Shared           PublishAttempt
}

// ValidateLease is part of the public package API.
func ValidateLease(lease Lease) error {
	if err := validateLeaseStructure(lease); err != nil {
		return err
	}
	_, err := marshalLeaseUnchecked(lease)
	return err
}

func validateLeaseStructure(lease Lease) error {
	if lease.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version %d", ErrInvalidLease, lease.SchemaVersion)
	}
	if err := validateIdentity(lease.Identity); err != nil {
		return err
	}
	if err := validateReport(lease.SubjectRef, lease.Mode, lease.Summary, lease.WaitingOn); err != nil {
		return err
	}
	if leaseAudienceKnown(lease) {
		if err := validateAudience(lease.Recipients, lease.Classification); err != nil {
			return err
		}
	} else if len(lease.Recipients) != 0 || lease.Classification != "" {
		return fmt.Errorf("%w: audience must be either fully persisted or fully absent", ErrInvalidLease)
	}
	if lease.StartedAt.IsZero() || lease.RenewedAt.IsZero() || lease.ExpiresAt.IsZero() ||
		lease.RenewedAt.Before(lease.StartedAt) || lease.ExpiresAt.Before(lease.RenewedAt) {
		return fmt.Errorf("%w: invalid lease timestamps", ErrInvalidLease)
	}
	if lease.HeartbeatSequence == 0 || lease.SemanticSequence == 0 || lease.SemanticSequence > math.MaxInt64 {
		return fmt.Errorf("%w: sequences must be positive", ErrInvalidLease)
	}
	ttl := lease.ExpiresAt.Sub(lease.RenewedAt)
	if ttl < MinimumTTL || ttl > MaximumTTL {
		return fmt.Errorf("%w: stored ttl outside accepted range", ErrInvalidTTL)
	}
	if lease.Closing && (lease.Pending == nil || lease.Pending.LocalTarget != TargetClosing) {
		return fmt.Errorf("%w: closing lease requires closing pending operation", ErrInvalidLease)
	}
	if (lease.Mode == ModePaused || lease.Mode == ModeFinished) != lease.Closing {
		return fmt.Errorf("%w: terminal or paused mode must be closing", ErrInvalidLease)
	}
	if lease.Pending != nil {
		if lease.LastResult != nil {
			return fmt.Errorf("%w: pending operation cannot retain prior result receipt", ErrInvalidLease)
		}
		if err := validatePending(*lease.Pending, lease.SemanticSequence, lease.Identity.WorkID); err != nil {
			return err
		}
		if err := validateActionMode(lease.Pending.Action, lease.Mode); err != nil {
			return err
		}
	}
	if lease.LastResult != nil {
		if err := validateReceipt(*lease.LastResult, lease.SemanticSequence, lease.Identity.WorkID); err != nil {
			return err
		}
		if err := validateActionMode(lease.LastResult.Action, lease.Mode); err != nil {
			return err
		}
	}
	return nil
}

func leaseAudienceKnown(lease Lease) bool {
	return len(lease.Recipients) != 0 && lease.Classification != ""
}

func validateAudience(recipients []string, classification string) error {
	if classification != "public" && classification != "internal" && classification != "restricted" {
		return fmt.Errorf("%w: invalid classification", ErrInvalidLease)
	}
	if len(recipients) == 0 || len(recipients) > 32 {
		return fmt.Errorf("%w: invalid recipients", ErrInvalidLease)
	}
	seen := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		if strings.TrimSpace(recipient) == "" || utf8.RuneCountInString(recipient) > 96 {
			return fmt.Errorf("%w: invalid recipient", ErrInvalidLease)
		}
		if _, ok := seen[recipient]; ok {
			return fmt.Errorf("%w: duplicate recipient", ErrInvalidLease)
		}
		seen[recipient] = struct{}{}
	}
	if len(recipients) > 1 {
		if _, ok := seen["all"]; ok {
			return fmt.Errorf("%w: all recipient must stand alone", ErrInvalidLease)
		}
	}
	return nil
}

func validateIdentity(identity Identity) error {
	if !digestPattern.MatchString(identity.LeaseKey) || !digestPattern.MatchString(identity.ProjectID) {
		return fmt.Errorf("%w: invalid lease or project digest", ErrInvalidLease)
	}
	if identity.Space == "" || identity.Thread == "" || !workIDPattern.MatchString(identity.WorkID) {
		return fmt.Errorf("%w: invalid work identity", ErrInvalidLease)
	}
	if identity.Actor.Kind == "" || identity.Actor.Name == "" || identity.Actor.System == "" || identity.Actor.Session == "" {
		return fmt.Errorf("%w: incomplete actor identity", ErrInvalidLease)
	}
	return nil
}

func validateReport(subjectRef string, mode Mode, summary string, waiting []WaitingOn) error {
	if subjectRef == "" || utf8.RuneCountInString(subjectRef) > 200 || !mode.Valid() ||
		strings.TrimSpace(summary) == "" || utf8.RuneCountInString(summary) > 240 {
		return fmt.Errorf("%w: invalid report fields", ErrInvalidLease)
	}
	if mode == ModeWaiting {
		if len(waiting) == 0 || len(waiting) > 8 {
			return fmt.Errorf("%w: waiting mode requires 1-8 reasons", ErrInvalidLease)
		}
	} else if len(waiting) != 0 {
		return fmt.Errorf("%w: waiting reasons forbidden outside waiting mode", ErrInvalidLease)
	}
	seen := make(map[string]struct{}, len(waiting))
	for _, wait := range waiting {
		if !wait.Kind.Valid() || strings.TrimSpace(wait.ID) == "" || utf8.RuneCountInString(wait.ID) > 96 || utf8.RuneCountInString(wait.Summary) > 160 {
			return fmt.Errorf("%w: invalid waiting reason", ErrInvalidLease)
		}
		key := string(wait.Kind) + "\x00" + wait.ID
		if _, ok := seen[key]; ok {
			return fmt.Errorf("%w: duplicate waiting reason", ErrInvalidLease)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validatePending(pending PendingOperation, semanticSequence uint64, workID string) error {
	if !pending.Action.Valid() ||
		pending.ArtifactID == "" || pending.EventID == "" || pending.SemanticSequence != semanticSequence ||
		pending.Prepared.Len() == 0 {
		return fmt.Errorf("%w: invalid pending operation", ErrInvalidLease)
	}
	if err := validateOperationKey(workID, pending); err != nil {
		return err
	}
	if pending.Action == ActionStop && pending.LocalTarget != TargetClosing {
		return fmt.Errorf("%w: stop must target closing", ErrInvalidLease)
	}
	if pending.Action == ActionStart && pending.SemanticSequence != 1 {
		return fmt.Errorf("%w: start sequence must be one", ErrInvalidLease)
	}
	if pending.Action != ActionStart && pending.SemanticSequence < 2 {
		return fmt.Errorf("%w: continuation sequence must exceed one", ErrInvalidLease)
	}
	if pending.Action != ActionStop && pending.LocalTarget != TargetActive {
		return fmt.Errorf("%w: non-stop operation must target active", ErrInvalidLease)
	}
	if pending.Shared.Attempted {
		if pending.Shared.WriteResult.Len() == 0 || !pending.Shared.Convergence.Valid() {
			return fmt.Errorf("%w: attempted publication requires result and convergence", ErrInvalidLease)
		}
	} else if pending.Shared.WriteResult.Len() != 0 || pending.Shared.Convergence != "" || pending.Shared.ErrorCode != "" {
		return fmt.Errorf("%w: unattempted publication contains outcome", ErrInvalidLease)
	}
	if pending.Shared.ErrorCode != pending.LastErrorCode {
		return fmt.Errorf("%w: shared and pending error code differ", ErrInvalidLease)
	}
	if !validErrorCode(pending.LastErrorCode) || !validErrorCode(pending.Shared.ErrorCode) {
		return fmt.Errorf("%w: invalid stable error code", ErrInvalidLease)
	}
	return nil
}

func validateActionMode(action Action, mode Mode) error {
	switch action {
	case ActionStart, ActionCheckpoint:
		if mode != ModePlanning && mode != ModeImplementing && mode != ModeTesting && mode != ModeReviewing {
			return fmt.Errorf("%w: %s requires an active mode", ErrInvalidLease, action)
		}
	case ActionWait:
		if mode != ModeWaiting {
			return fmt.Errorf("%w: wait requires waiting mode", ErrInvalidLease)
		}
	case ActionStop:
		if mode != ModePaused && mode != ModeFinished {
			return fmt.Errorf("%w: stop requires paused or finished mode", ErrInvalidLease)
		}
	default:
		return fmt.Errorf("%w: unknown action", ErrInvalidLease)
	}
	return nil
}

func validateOperationKey(workID string, pending PendingOperation) error {
	expected, err := expectedOperationKey(pending.SemanticSequence, pending.Action, workID)
	if err != nil {
		return err
	}
	if pending.OperationKey != expected {
		return fmt.Errorf("%w: operation key does not bind work identity", ErrInvalidLease)
	}
	return nil
}

func validateReceipt(receipt PublicationReceipt, semanticSequence uint64, workID string) error {
	pendingShape := PendingOperation{
		OperationKey: receipt.OperationKey, Action: receipt.Action, ArtifactID: receipt.ArtifactID,
		EventID: receipt.EventID, SemanticSequence: receipt.SemanticSequence,
	}
	if receipt.SemanticSequence != semanticSequence || receipt.Action == ActionStop || receipt.ArtifactID == "" || receipt.EventID == "" {
		return fmt.Errorf("%w: invalid completed operation receipt", ErrInvalidLease)
	}
	if err := validateOperationKey(workID, pendingShape); err != nil {
		return err
	}
	if !digestPattern.MatchString(receipt.PreparedDigest) || receipt.WriteResult.Len() == 0 ||
		(receipt.Convergence != ConvergenceAccepted && receipt.Convergence != ConvergenceTerminal) {
		return fmt.Errorf("%w: incomplete completed operation receipt", ErrInvalidLease)
	}
	return nil
}

func digestPrepared(prepared PreparedJournal) string {
	digest := sha256.Sum256(prepared.raw)
	return fmt.Sprintf("sha256:%x", digest)
}

func expectedOperationKey(sequence uint64, action Action, workID string) (string, error) {
	if sequence == 0 || sequence > math.MaxInt64 {
		return "", fmt.Errorf("%w: semantic sequence outside canonical operation range", ErrInvalidLease)
	}
	key, err := operation.Work(workID, int64(sequence), string(action))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidLease, err)
	}
	return key, nil
}

func validErrorCode(code string) bool {
	if code == "" {
		return true
	}
	return len(code) <= 96 && !strings.ContainsAny(code, "\r\n\t ")
}
