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
