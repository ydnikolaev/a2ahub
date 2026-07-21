package fold

import "sort"

// Kind is one of the eight §3.1 object types this package folds.
type Kind string

const (
	// KindContract denotes an artifact representing a work contract.
	KindContract Kind = "contract"
	// KindRequirement denotes an artifact representing a work requirement.
	KindRequirement Kind = "requirement"
	// KindQuestion denotes an artifact representing a question.
	KindQuestion Kind = "question"
	// KindWorkRequest denotes an artifact representing a work request.
	KindWorkRequest Kind = "work_request"
	// KindDecision denotes an artifact representing a decision.
	KindDecision Kind = "decision"
	// KindHandoff denotes an artifact representing a handoff.
	KindHandoff Kind = "handoff"
	// KindResponse denotes an artifact representing a response.
	KindResponse Kind = "response"
	// KindAnnouncement denotes an artifact representing an announcement.
	KindAnnouncement Kind = "announcement"
)

// State is a §3.4.x lifecycle state name. StateNone is the sentinel
// "artifact does not exist yet" state used only as a table fromState for
// the (never actually committed, per 3.4 "drafts are local-only") create
// transition and as table row documentation.
type State string

const (
	// StateNone is the sentinel "artifact does not exist yet" state, used only as a table fromState for the create transition.
	StateNone State = ""
	// StateDraft is the initial draft state of an artifact.
	StateDraft State = "draft"
	// StatePublished indicates an artifact has been published.
	StatePublished State = "published"
	// StateDeprecated indicates an artifact is deprecated.
	StateDeprecated State = "deprecated"
	// StateRetired indicates an artifact is retired.
	StateRetired State = "retired"

	// StateAcknowledged indicates a requirement has been acknowledged.
	StateAcknowledged State = "acknowledged"
	// StateSatisfied indicates a requirement has been satisfied.
	StateSatisfied State = "satisfied"
	// StateDeclined indicates a requirement has been declined.
	StateDeclined State = "declined"
	// StateWithdrawn indicates a requirement has been withdrawn.
	StateWithdrawn State = "withdrawn"
	// StateSuperseded indicates a requirement has been superseded.
	StateSuperseded State = "superseded"

	// StateSubmitted indicates a work request has been submitted.
	StateSubmitted State = "submitted"
	// StateAccepted indicates a work request has been accepted.
	StateAccepted State = "accepted"
	// StateInProgress indicates a work request is in progress.
	StateInProgress State = "in_progress"
	// StateBlocked indicates a work request is blocked.
	StateBlocked State = "blocked"
	// StateResponded indicates a work request has been responded to.
	StateResponded State = "responded"
	// StateClosed indicates a work request is closed.
	StateClosed State = "closed"
	// StateCancelled indicates a work request is cancelled.
	StateCancelled State = "cancelled"

	// StateProposed indicates a decision is in the proposed state.
	StateProposed State = "proposed"
	// StateApproved indicates a decision has been approved.
	StateApproved State = "approved"
	// StateRejected indicates a decision has been rejected.
	StateRejected State = "rejected"

	// StateVerified indicates a response has been verified.
	StateVerified State = "verified"
	// StateDisputed indicates a response is disputed.
	StateDisputed State = "disputed"

	// StateDynamic is a table-row sentinel: the row's real toState is
	// resolved at apply-time by dedicated logic (unblock's pre-block
	// recovery, decision approve's quorum arithmetic), never returned
	// directly from Apply/Fold.
	StateDynamic State = "__dynamic__"
)

// Role is how a candidate/committed event's actor is authorized against
// an artifact's own envelope facts (spec §T1 "Manifest-membership view"
// row: role checks need no extra manifest data, only the artifact's own
// envelope). Membership validity (member/left/unknown) is checked
// separately and always, on top of the role match.
type Role string

const (
	// RoleOwner resolves to the artifact's own From (creator / requester
	// / author / producing system).
	RoleOwner Role = "owner"
	// RoleTarget resolves to the artifact's To[0] (D-027: exchanges
	// address exactly one system).
	RoleTarget Role = "target"
	// RoleApprover resolves to membership in the artifact's
	// RequiredApprovers (decision only).
	RoleApprover Role = "approver"
	// RoleEitherParty resolves to From OR To[0] (note: "authorized for
	// either party", 3.5).
	RoleEitherParty Role = "either_party"
	// RoleAny waives the system match; membership validity still applies.
	RoleAny Role = "any"
)

// FlagKind is the shared non-fatal protocol-violation enum (3.5 rules
// 2/3/5) — one type for every flag class fold ever raises on an
// already-committed event; it never errors or panics on these.
type FlagKind string

const (
	// FlagIllegalTransition indicates a non-fatal protocol violation for an illegal state transition.
	FlagIllegalTransition FlagKind = "illegal-transition"
	// FlagUnauthorizedActor indicates a non-fatal protocol violation for an unauthorized actor.
	FlagUnauthorizedActor FlagKind = "unauthorized-actor"
	// FlagStateClaimMismatch indicates a non-fatal protocol violation for a state claim mismatch.
	FlagStateClaimMismatch FlagKind = "state-claim-mismatch"
)

// Flag records one non-fatal protocol violation. The triggering event is
// retained by reference (EventULID) — fold never drops or mutates input
// events (CC-020: "event stays in history").
type Flag struct {
	Kind      FlagKind
	EventULID string
	Subject   string
}

// Verdict is the pre-write legality check's result set (§T1 "Legality
// check" row) — a strict subset of FlagKind: no state-claim-mismatch,
// since a not-yet-committed candidate event carries no committed claim
// to compare against.
type Verdict string

const (
	// VerdictLegal indicates the candidate transition is legal for the given state and actor.
	VerdictLegal Verdict = "legal"
	// VerdictIllegalTransition indicates the candidate transition is not legal from the subject's current state.
	VerdictIllegalTransition Verdict = "illegal-transition"
	// VerdictUnauthorizedActor indicates the acting actor is not authorized for this transition.
	VerdictUnauthorizedActor Verdict = "unauthorized-actor"
)

// MembershipStatus is the caller-resolved manifest-membership fact for
// one system as of one commit (§T1 "Manifest-membership view" row). Fold
// never parses space.yaml; the caller supplies this.
type MembershipStatus int

const (
	// MembershipUnknown indicates the membership status is not yet determined.
	MembershipUnknown MembershipStatus = iota
	// MembershipMember indicates the system is a member of the space.
	MembershipMember
	// MembershipLeft indicates the system has left the space.
	MembershipLeft
)

// MembershipView resolves a system's membership status as of the commit
// the fold is currently processing. Caller-supplied (D-017: authorization
// is evaluated against the manifest as of the event's commit); fold never
// reads git/space.yaml itself.
type MembershipView func(system string) MembershipStatus

// Actor is the §5.2.2 event actor block's fold-relevant projection.
type Actor struct {
	Kind   string // "human" | "agent" — carried for fidelity, unused by fold logic
	Name   string
	System string
}

// Envelope carries the create/envelope facts fold needs to resolve role
// checks and expand-only-transition tables — the caller's own translation
// of a validated event/v1 document's referenced artifact (spec §7: fold
// defines its own minimal input types rather than depending on
// internal/schema's parsed types).
type Envelope struct {
	ID                string
	Kind              Kind
	From              string   // owner / requester / author / producing system
	To                []string // exchange target(s); D-027: to[0] is authoritative for exchanges
	RequiredApprovers []string // decision only
}

// To0 returns the exchange's single target system (D-027), or "" if none
// is set.
func (e Envelope) To0() string {
	if len(e.To) == 0 {
		return ""
	}
	return e.To[0]
}

// Event is fold's own minimal projection of one committed lifecycle event
// (§5.2.2). CommitSeq + ULID together are the caller-supplied ordering key
// (§T1 "Ordering key" row; D-017: first-parent commit order, ULID
// intra-commit tiebreak only) — fold never reads git to derive it.
type Event struct {
	ULID         string
	CommitSeq    int64
	Subject      string // artifact this event acts on: the primary/parent ID, or (verify/dispute only) a response ID
	Transition   string
	ClaimedState State // event's "state" field (informational; 3.5 rule 5)
	Actor        Actor
	// ResponseID is set only on a "respond" event: the newly attached XS
	// id (D-024). Fold uses it to open that response's own closure
	// sub-state at StateSubmitted.
	ResponseID string
}

// Result is the full carrier a caller stores and re-supplies as
// Apply's "prior folded state" input — not just the top-level State
// string. It MUST carry everything a correct incremental continuation
// needs to recompute purely from itself + the next event: the pre-block
// state (unblock's dynamic target), decision quorum bookkeeping, the
// per-response closure sub-states (D-024), the per-recipient broadcast
// ack set (D-025), the accumulated non-fatal flags, and the set of
// already-applied event ULIDs (idempotent replay, T2 AC5).
type Result struct {
	Kind          Kind
	State         State
	PreBlockState State // recovered by `unblock`; recomputed from the event sequence, carried in the pure result (never an external side-channel)

	Responses map[string]State // response XS id -> closure sub-state (submitted/verified/disputed)
	Acks      map[string]bool  // broadcast per-recipient ack set (D-025), keyed by acting system
	Approvals map[string]bool  // decision quorum bookkeeping, keyed by approving system

	Flags []Flag // append-only, in event-application order (deterministic; never re-sorted from a map)

	Applied map[string]bool // applied event ULIDs, for idempotent replay (T2 AC5)
}

// clone deep-copies every mutable field so Apply never mutates a shared
// "prior" value out from under a concurrent caller (pure function of its
// arguments; t.Parallel()-safe under -race).
func (r Result) clone() Result {
	out := r
	out.Responses = copyStateMap(r.Responses)
	out.Acks = copyBoolMap(r.Acks)
	out.Approvals = copyBoolMap(r.Approvals)
	out.Applied = copyBoolMap(r.Applied)
	out.Flags = append([]Flag(nil), r.Flags...)
	return out
}

func copyBoolMap(m map[string]bool) map[string]bool {
	if m == nil {
		return nil
	}
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyStateMap(m map[string]State) map[string]State {
	if m == nil {
		return nil
	}
	out := make(map[string]State, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// AckedRecipients returns the broadcast ack set as a deterministically
// sorted slice — maps have no iteration order, so any output rendering
// sorts (constraint: "no maps with nondeterministic iteration leaking
// into output ordering").
func (r Result) AckedRecipients() []string {
	out := make([]string, 0, len(r.Acks))
	for k := range r.Acks {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResponseIDs returns the tracked response ids as a deterministically
// sorted slice.
func (r Result) ResponseIDs() []string {
	out := make([]string, 0, len(r.Responses))
	for k := range r.Responses {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
