package validate

// Verdict is the pre-write lifecycle-legality verdict, per spec 03 §7's
// fold seam table (3-valued). The fold-time flag set internal/fold owns
// is a superset (it also has `state-claim-mismatch`, which requires
// post-fold state comparison a pre-write V2 call cannot perform) — this
// enum stays exactly 3-valued, matching the pre-write contract.
type Verdict int

const (
	// VerdictLegal indicates the candidate transition is legal for the given
	// state/actor — no violation.
	VerdictLegal Verdict = iota
	// VerdictIllegalTransition indicates the candidate transition is not legal
	// from the subject's current folded state (§3.5 rule 2).
	VerdictIllegalTransition
	// VerdictUnauthorizedActor indicates the acting actor/system is not
	// authorized for this transition (§3.5 rule 3, G3).
	VerdictUnauthorizedActor
)

// Actor is validate's own minimal view of a lifecycle event's actor block
// (§3.5, §5.2.2) — deliberately not internal/fold's own actor type
// (consumer-side ISP, ADR-001 plan Amendment 2026-07-21).
type Actor struct {
	Kind   string // "human" | "agent"
	Name   string
	System string
}

// CandidateEvent is validate's own minimal view of an about-to-be-
// submitted lifecycle event, passed to LegalityChecker once per event
// accompanying a submit batch (§7 "V2 usage" row). It is NOT internal/
// fold's event type: P4 builds internal/fold in the same wave, and
// importing it here would compile against a half-written sibling package
// (this epic's Off-limits rule) — cmd/a2a (P6) wires the concrete
// implementation, adapting fold's own richer event type into this shape.
type CandidateEvent struct {
	// Subject is the artifact ID this event acts on.
	Subject string
	// Transition is the §3.4 transition name (or "note").
	Transition string
	// Actor is the acting actor block.
	Actor Actor
	// Version is the event's own §5.2.2 "version" field, non-empty only on
	// a contract publish/deprecate/retire event that names one (P4,
	// agent-ops-2026-07/plans/04-per-version-lifecycle.plan.md). A
	// concrete LegalityChecker threads this through to internal/fold's
	// per-version legality primitive; validate itself never inspects it.
	Version string
	// Envelope carries the SUBJECT artifact's own create/envelope facts
	// (§5.2.2) a LegalityChecker needs to resolve role checks — the same
	// facts fold.Envelope carries, in this package's own vocabulary (plain
	// strings, no internal/fold import: this type's own doc comment states
	// the same rule fold's richer event type is kept out for).
	//
	// no-silent-yes-2026-08/P6, US-3: this REPLACES the RegisterEnvelope
	// side-channel both internal/mcp's and internal/cli's own
	// LegalityAdapter used to carry (a `map[string]fold.Envelope` plus a
	// mutex, populated by a SEPARATE method call before ValidateForSubmit)
	// — a call site could omit that call entirely, and CheckLegality then
	// errored at RUNTIME ("no envelope registered for subject"). Carrying
	// the envelope ON the CandidateEvent itself, at the SAME construction
	// site that builds every other field, makes a forgotten envelope a
	// STRUCTURAL fact about the literal a reviewer/vet sees immediately,
	// not a separate statement two lines away that is easy to omit.
	Envelope Envelope
	// SuccessorEnvelope carries the caller-resolved facts about a
	// decision-supersede candidate's own SUCCESSOR artifact — the two
	// facts internal/fold's own declared row preconditions check (D7/D9,
	// no-silent-yes-2026-08/P6). nil means UNRESOLVED, never "resolved and
	// blank" (the same distinction internal/fold's own SuccessorFacts doc
	// comment states one layer down) — checkLifecycle (lifecycle.go) reads
	// this field directly to decide whether an LFC-005 refusal also pairs
	// with LFC-006, and a concrete LegalityChecker (internal/mcp's and
	// internal/cli's own LegalityAdapter) converts it into internal/fold's
	// own SuccessorFacts shape before calling
	// fold.CheckCandidateWithSuccessor. Meaningful only for a decision
	// `supersede` transition; every other transition leaves it nil, and
	// checkLifecycle never inspects it for any other transition.
	SuccessorEnvelope *SuccessorEnvelope
}

// Envelope is validate's own minimal, caller-resolved projection of an
// artifact's create/envelope facts (§5.2.2) — the same shape fold.Envelope
// carries, in this package's own plain-string vocabulary (no internal/fold
// import, CandidateEvent's own doc comment). A concrete LegalityChecker
// converts this into fold's own Envelope type before calling
// fold.CheckCandidateWithSuccessor/fold.CheckLegality.
type Envelope struct {
	// ID is the artifact's own id.
	ID string
	// Kind is the artifact's §3.1 object type, as fold.Kind's own string
	// vocabulary (e.g. "decision") — no fold.Kind import to name it with.
	Kind string
	// From is the owner / requester / author / producing system.
	From string
	// To is the exchange target(s); §D-027 — to[0] is authoritative for
	// exchanges.
	To []string
	// RequiredApprovers is the decision-only required-approver set.
	RequiredApprovers []string
}

// SuccessorEnvelope is validate's own minimal, caller-resolved projection
// of a decision-supersede candidate event's successor artifact — plain
// strings only, this package's own vocabulary, deliberately NOT internal/
// fold's SuccessorFacts type: validate must not import internal/fold
// (CandidateEvent's own doc comment states the same rule for fold's richer
// event type). A concrete LegalityChecker is the one place that converts
// this shape into fold's own.
type SuccessorEnvelope struct {
	// Author is the successor artifact's own envelope `from` (§5.2.2).
	Author string
	// State is the successor artifact's own current folded lifecycle
	// state, as a plain string (internal/fold's State vocabulary, e.g.
	// "approved" — this package carries no fold.State import to name it
	// with).
	State string
}

// SuccessorResolver is validate's own consumer-side optional upgrade to
// Resolver — the SAME pattern ParentCriteriaCounter (incompleteness.go)
// and ActiveParticipantLister (classification.go) establish: an optional
// capability a concrete Resolver MAY also implement, type-asserted by a
// caller rather than added to the Resolver interface itself (Resolver's
// own doc comment: "deliberately NOT widened for optional, rule-specific
// facts a Resolver may also be able to answer"). It answers the one fact
// CandidateEvent.SuccessorEnvelope needs: the author and current folded
// state of the artifact a decision-supersede event's own `refs[].ref`
// names as its successor (§3.4.4; internal/validate/supersession.go's own
// SupersedeLink doc comment: "the real link lives on the supersede EVENT's
// refs[].ref", the `supersedes` envelope field being dead).
//
// A concrete Resolver that does not implement this interface cannot be
// asked — the caller (a concrete LegalityChecker's own construction site)
// leaves CandidateEvent.SuccessorEnvelope nil, which D9's own rule (fold's
// SuccessorPrecondition doc comment) reads as UNRESOLVED, never a resolved
// grant: ADR-019's second half forbids a capability miss from reading as a
// resolved negative OR positive.
type SuccessorResolver interface {
	// Successor resolves successorID's own author (envelope `from`) and
	// current folded lifecycle state, and whether successorID could be
	// resolved as a known, folded artifact at all. ok=false covers every
	// "cannot resolve" case alike (successorID absent, unparseable, or its
	// own history unfoldable) — never a synthesized author/state.
	Successor(successorID string) (author, state string, ok bool)
}

// LegalityChecker is the consumer-side seam onto internal/fold's
// transition tables and legality function (spec 03 §7 "Legality check"
// row: given the subject's current folded state, the candidate
// transition, the actor block, and the manifest as staged locally, return
// a 3-valued verdict). internal/validate defines this interface (ISP,
// go-conventions.md "consumer-side interface where it is used") and takes
// it via constructor DI; cmd/a2a (P6) supplies the concrete
// implementation backed by internal/fold, once that package exists.
//
// The manifest-as-of-commit and current-folded-state inputs the seam
// table describes are NOT threaded through this call explicitly: a
// concrete implementation is expected to close over whatever locally-
// staged history/manifest it needs (the same way a Resolver
// implementation closes over its own local cache, per the Resolver doc
// comment below) — this keeps the interface at the single method spec
// 03's Amendment describes ("a 1-method LegalityChecker interface")
// without validate itself carrying fold-shaped state.
type LegalityChecker interface {
	CheckLegality(candidate CandidateEvent) (Verdict, error)
}

// Resolver is validate's consumer-side seam onto the local artifact/ref/
// manifest cache (populated by internal/space, out of this footprint —
// validate itself does no I/O, "Pure core" per go-conventions.md). A
// concrete implementation is expected to close over a local git clone's
// staged state; validate only ever calls these three methods.
//
// This interface is deliberately NOT widened for optional, rule-specific
// facts a Resolver may also be able to answer — ParentCriteriaCounter
// (incompleteness.go) is exactly such a consumer-side optional upgrade,
// type-asserted against a Resolver rather than added here. cmd/a2a's
// mirrorResolverWithCriteria (validate_resolver.go) is the one shipped
// implementation P6's write paths wire.
type Resolver interface {
	// KnownArtifact reports whether id is a known artifact in the local
	// cache (referential class: unresolvable ref/id).
	KnownArtifact(id string) bool
	// Digest returns the digest recorded for ref's target (an
	// `id@version` or `id#digest` pin, §5.7) as of the local cache, and
	// whether it was found at all.
	Digest(ref string) (digest string, found bool)
	// System reports whether system is a known member of the space per
	// the manifest cache, and (if known) whether its membership status
	// is `left` (§10.3, CC-008, CC-062).
	System(system string) (member bool, left bool)
}

// LocalContext carries ValidateForSubmit's pre-write, locally-cached
// inputs (§5.5 V2 row: referential + authz classes need the local
// artifact/manifest cache). validate never fetches any of this itself.
type LocalContext struct {
	// OwnSystem is this project's own configured system ID (§10.3 "own
	// section") — the authz class's from==own-section check compares
	// against this. Supplied by the caller (config/DI layer, never
	// os.Getenv inside this package).
	OwnSystem string
	// Resolver resolves IDs/refs/systems against the local cache.
	Resolver Resolver
	// Legality checks lifecycle legality for each accompanying event.
	Legality LegalityChecker
}
