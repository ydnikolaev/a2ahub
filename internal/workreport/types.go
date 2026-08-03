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
	SchemaVersion       = 1
	DefaultTTL          = 15 * time.Minute
	MinimumTTL          = time.Minute
	MaximumTTL          = 24 * time.Hour
	MaximumPrepared     = 24 * 1024
	MaximumEncodedLease = 32 * 1024
)

var (
	workIDPattern = regexp.MustCompile(`^work:[0-9A-HJKMNP-TV-Z]{26}$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Mode string

const (
	ModePlanning     Mode = "planning"
	ModeImplementing Mode = "implementing"
	ModeTesting      Mode = "testing"
	ModeReviewing    Mode = "reviewing"
	ModeWaiting      Mode = "waiting"
	ModePaused       Mode = "paused"
	ModeFinished     Mode = "finished"
)

func (m Mode) Valid() bool {
	switch m {
	case ModePlanning, ModeImplementing, ModeTesting, ModeReviewing, ModeWaiting, ModePaused, ModeFinished:
		return true
	default:
		return false
	}
}

type WaitKind string

const (
	WaitSystem   WaitKind = "system"
	WaitHuman    WaitKind = "human"
	WaitTool     WaitKind = "tool"
	WaitTimer    WaitKind = "timer"
	WaitExternal WaitKind = "external"
)

func (k WaitKind) Valid() bool {
	switch k {
	case WaitSystem, WaitHuman, WaitTool, WaitTimer, WaitExternal:
		return true
	default:
		return false
	}
}

type WaitingOn struct {
	Kind    WaitKind `json:"kind"`
	ID      string   `json:"id"`
	Summary string   `json:"summary,omitempty"`
}

type Actor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session"`
}

type Identity struct {
	LeaseKey  string
	ProjectID string
	Space     string
	Thread    string
	WorkID    string
	Actor     Actor
}

func (i Identity) Equal(other Identity) bool {
	return i.LeaseKey == other.LeaseKey && i.ProjectID == other.ProjectID &&
		i.Space == other.Space && i.Thread == other.Thread && i.WorkID == other.WorkID &&
		i.Actor.System == other.Actor.System && i.Actor.Name == other.Actor.Name &&
		i.Actor.Session == other.Actor.Session
}

// PreparedJournal is the opaque canonical journal emitted by the P4 adapter.
// It deliberately exposes copies only: callers cannot mutate bytes persisted
// for restart recovery.
type PreparedJournal struct{ raw []byte }

func NewPreparedJournal(raw []byte) (PreparedJournal, error) {
	if err := validateOpaqueJSON(raw, MaximumPrepared, "prepared journal"); err != nil {
		return PreparedJournal{}, err
	}
	return PreparedJournal{raw: bytes.Clone(raw)}, nil
}

func (j PreparedJournal) Bytes() []byte { return bytes.Clone(j.raw) }
func (j PreparedJournal) Len() int      { return len(j.raw) }

// WriteResultJournal is the opaque canonical P4 result. The strict adapter,
// not this package, owns its schema and maps it to a convergence class.
type WriteResultJournal struct{ raw []byte }

func NewWriteResultJournal(raw []byte) (WriteResultJournal, error) {
	if err := validateOpaqueJSON(raw, MaximumEncodedLease, "write result journal"); err != nil {
		return WriteResultJournal{}, err
	}
	return WriteResultJournal{raw: bytes.Clone(raw)}, nil
}

func (j WriteResultJournal) Bytes() []byte { return bytes.Clone(j.raw) }
func (j WriteResultJournal) Len() int      { return len(j.raw) }

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

type Action string

const (
	ActionStart      Action = "start"
	ActionCheckpoint Action = "checkpoint"
	ActionWait       Action = "wait"
	ActionStop       Action = "stop"
)

func (a Action) Valid() bool {
	switch a {
	case ActionStart, ActionCheckpoint, ActionWait, ActionStop:
		return true
	default:
		return false
	}
}

type LocalTarget string

const (
	TargetActive  LocalTarget = "active"
	TargetClosing LocalTarget = "closing"
)

type Convergence string

const (
	ConvergenceResumable Convergence = "resumable"
	ConvergenceAccepted  Convergence = "accepted"
	ConvergenceTerminal  Convergence = "terminal"
)

func (c Convergence) Valid() bool {
	return c == ConvergenceResumable || c == ConvergenceAccepted || c == ConvergenceTerminal
}

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

type PreparedOperation struct {
	OperationKey     string
	Action           Action
	ArtifactID       string
	EventID          string
	SemanticSequence uint64
	Prepared         PreparedJournal
	LocalTarget      LocalTarget
}

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

type Lease struct {
	SchemaVersion     int
	Identity          Identity
	SubjectRef        string
	Mode              Mode
	Summary           string
	WaitingOn         []WaitingOn
	StartedAt         time.Time
	RenewedAt         time.Time
	ExpiresAt         time.Time
	HeartbeatSequence uint64
	SemanticSequence  uint64
	Closing           bool
	Pending           *PendingOperation
	LastResult        *PublicationReceipt
}

func (l Lease) Clone() Lease {
	copy := l
	copy.WaitingOn = append([]WaitingOn(nil), l.WaitingOn...)
	copy.Pending = l.Pending.clone()
	copy.LastResult = l.LastResult.clone()
	return copy
}

func (l Lease) Expired(now time.Time) bool { return now.After(l.ExpiresAt) }

func (l Lease) OwnedBy(identity Identity) bool { return l.Identity.Equal(identity) }

type Revision string

// LeaseRepository supplies process-safe compare-and-swap semantics. A zero
// revision means the key must not exist. Passing next=nil removes it.
type LeaseRepository interface {
	Load(context.Context, string) (Lease, Revision, error)
	CompareAndSwap(context.Context, string, Revision, *Lease) (Revision, error)
}

type Publisher interface {
	SubmitPrepared(context.Context, PreparedJournal, WriteResultJournal) (PublishAttempt, error)
}

type Clock interface{ Now() time.Time }

type StartCommand struct {
	Identity   Identity
	SubjectRef string
	Mode       Mode
	Summary    string
	WaitingOn  []WaitingOn
	TTL        time.Duration
	Prepared   PreparedOperation
}

type SemanticCommand struct {
	Identity   Identity
	SubjectRef string
	Mode       Mode
	Summary    string
	WaitingOn  []WaitingOn
	TTL        time.Duration
	Prepared   PreparedOperation
}

type LocalState string

const (
	LocalActive    LocalState = "active"
	LocalClosing   LocalState = "closing"
	LocalCleared   LocalState = "cleared"
	LocalUnchanged LocalState = "unchanged"
)

type OperationResult struct {
	WorkID           string
	Session          string
	OperationKey     string
	SemanticSequence uint64
	LocalState       LocalState
	ExpiresAt        time.Time
	Shared           PublishAttempt
}

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
		return "", fmt.Errorf("%w: %v", ErrInvalidLease, err)
	}
	return key, nil
}

func validErrorCode(code string) bool {
	if code == "" {
		return true
	}
	return len(code) <= 96 && !strings.ContainsAny(code, "\r\n\t ")
}
