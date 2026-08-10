package cache

import (
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// addressedToMe is §4.2's base inbox query ("all artifacts across the
// space where `to` includes it") — the SCOPE `a2a inbox` (no flag)
// lists; `--actionable`'s own 5-condition union (actionableReasons) is a
// DIFFERENT, broader query that is not pre-filtered by this (some
// conditions, e.g. "responded awaiting my verify/close", key off `from`,
// not `to` — see actionableReasons' own doc comment).
// P4 Edge 3 (04-per-version-lifecycle.md §4) adds the third clause below,
// and it is a UNION — it never takes anything away.
//
// A deprecation announcement's `to:` is the registered-consumer set
// computed once, at deprecate time, and then frozen; the announcement's id
// is seeded from {id, version, sunset} without the recipient set, so
// re-running `contract deprecate` after a new `adopt` lands on the write
// funnel's dedup branch and the newcomer is never addressed. Under a
// rolling window deprecations ARE the steady state, so that race stops
// being an edge case and starts being how a consumer silently fails to
// learn the contract under them is expiring.
//
// The retire gate already reads the LIVE registry for "who blocks me".
// Until now the two halves of one fact came from two sources — who blocks
// me, live; who was told, frozen — and this closes that. `to:` degrades to
// a courtesy snapshot: still honoured, never authoritative. Union rather
// than replacement is deliberate, because a system that WAS in `to:` and
// has since dropped the dependency would otherwise watch the announcement
// vanish from its inbox, and disappearing messages are not an acceptable
// price for fixing a race in an append-only system.
func addressedToMe(fa foldedArtifact, me string) bool {
	if fa.Env.isBroadcast() {
		return true
	}
	if containsString(normalizeTo(fa.Env.To), me) {
		return true
	}
	return fa.DeprecatesMyDependency
}

// extraAddressees is the registry-derived half of "who is addressed",
// resolved here because this package can do the I/O and internal/pendency
// deliberately cannot (pendency.Input.ExtraAddressees' own doc comment).
//
// It carries exactly P4 Edge 3: mirror.go resolved DeprecatesMyDependency
// once, from THIS system's consumes.yaml, so the only extra addressee this
// package can name is me. That narrowness is a property of the fact, not of
// the rule — a caller able to read every participant's registry hands in a
// wider set and nothing in pendency changes.
func extraAddressees(fa foldedArtifact, me string) []string {
	if !fa.DeprecatesMyDependency || me == "" {
		return nil
	}
	return []string{me}
}

// hasFulfillingResponse reports whether a response artifact naming this one
// as its parent has been submitted — the fact internal/pendency's requirement
// row splits on, resolved here for the same reason extraAddressees is: the
// evidence lives on a DIFFERENT artifact, and that package answers about one
// at a time.
//
// It reads mirror.go's own `parent`-field pass, NOT fold's Result.Responses.
// The latter was the first attempt and is permanently empty for a requirement: fold
// populates that map only from a `respond` event's ResponseID, and
// requirementRows carries no respond row. Meaningful only for a requirement;
// every other kind's row ignores it.
func hasFulfillingResponse(fa foldedArtifact) bool {
	return fa.FulfillingResponse
}

// blockedByOwner returns the system a fulfilling response's own
// `blocked_by.owner` names as who this artifact's `blocked` wait is
// actually on — internal/pendency's `blocked` row splits on it
// (Input.BlockedByOwner). Resolved and legality-checked in mirror.go, for
// the same reason hasFulfillingResponse is: the evidence lives on a
// DIFFERENT artifact, and P-2's legality check needs manifest membership
// pendency does not read.
func blockedByOwner(fa foldedArtifact) string {
	return fa.BlockedByOwner
}

// actionableReasons evaluates every one of OP-207's 5 normative
// `--actionable` conditions (quoted verbatim in spec 07 §T1), PLUS the
// one condition P5 AC1 (specs/05-declared-nature.md) adds on top of that
// frozen set, against fa for system me, returning the subset that matched
// (nil if none). This is NOT scoped by addressedToMe: condition 2 keys
// off `from`==me (I'm the owner awaiting my own verify/close) and
// condition 4 applies to any item I'm a party to (from OR to) — see this
// phase's Deviations report for the reading of condition 4's unqualified
// "any open state". Condition 6 is From==me too, for the same reason
// condition 2 is: the contract's own author is never in its own `to:`,
// so without this the row this wave's own pendency change adds would be
// computed and then unreachable through `--actionable` — Store.inbox's
// `actionableOnly` branch does not check addressedToMe at all (it is the
// one inbox scope that lists an item not addressed to me, precisely for
// From==me conditions like this one and condition 2).
//
// P11 W1 (11-authoring-path-and-seam-verification.plan.md, I7): conditions
// 1, 2, 3 and 5 are FILTERS over internal/pendency's own relation — "does
// the relation name me, and is the owed transition of this shape" — never
// a second, locally re-derived pendency rule. A single pendency.Resolve
// call per artifact answers most of them directly; two conditions keep one
// additional local read each, in both cases because the extra fact is
// genuinely outside pendency.Input's own (artifact, system) no-clock,
// no-registry shape (its own package doc comment), never because the
// relation's OWNERSHIP answer was re-derived: condition 1's announcement
// case still reads Result.Acks/addressedToMe for P4 Edge 3's late-adopter
// addressing (a registry-derived fact), and condition 3 still reads
// fold.Result.Responses' per-response disputed sub-state. See each
// condition's own inline comment.
//
// Condition 4 is the one deliberate NON-pendency exception (a different
// axis entirely, not a missing-input carve-out) — see its own inline doc
// comment for why folding it into pendency would be wrong, not merely
// redundant.

// resolveVerdict asks internal/pendency whose move it is on fa, for system
// `me`, from the facts this package resolved and that package structurally
// cannot.
//
// It exists so that ONE function in this file builds a pendency.Input. Every
// consumer inside this package — the `--actionable` conditions, the exchange
// feed's liveness answer, the fields on Item — reads the SAME verdict rather
// than assembling its own call with its own idea of which carried facts
// matter. That is not tidiness: the two facts pendency cannot recover
// (ExtraAddressees, ActiveParticipants) are silently omittable, and P11 W1
// shipped one commit where inbox.go passed one and threadview.go did not.
//
// parentFrom is the one fact a caller can know and this function cannot: a
// response artifact carries no envelope of its own, so whose exchange it
// answers is resolved from the PARENT. Store.inbox and Store.outbox each
// build a per-space id->artifact index (byArtifactID) and resolve it through
// responseParentFrom before calling in here — the SAME resolution
// buildOpenItems (threadview.go) already performs for the thread surface,
// including its degrade-to-the-response's-own-`from` fallback when the
// parent cannot be found in that space's index. This was the one remaining
// asymmetry between the inbox/outbox surfaces and the thread surface (a
// response's pendency verdict differed depending on which one was asked);
// it is closed now, not merely recorded. Every OTHER caller passes ""
// deliberately, for a reason specific to it — this file's own
// actionableReasons 3-arg convenience wrapper (see its own doc comment for
// why that is behaviour-preserving) and types.go's exchangeActive, whose
// only resolveVerdict call fires on the announcement/published branch, a
// kind for which parentFrom is never read at all.
func resolveVerdict(fa foldedArtifact, me string, manifest space.Manifest, parentFrom string) pendency.Verdict {
	env := fa.Env
	in := pendency.Input{
		Kind: fa.kind(), State: fa.Result.State,
		From: env.From, To: normalizeTo(env.To),
		Broadcast: env.isBroadcast(), AckRequested: env.AckRequested,
		Acks: fa.Result.Acks, Approvals: fa.Result.Approvals,
		RequiredApprovers:  env.RequiredApprovers,
		ActiveParticipants: activeParticipants(manifest, env.From),
		LeftParticipants:   leftParticipants(manifest),
		// P4 Edge 3, handed in rather than re-derived: this system's own
		// registry says it consumes the contract this announcement
		// deprecates, so it is addressed even though the frozen `to:`
		// predates its adoption. pendency owns what that MEANS; this call
		// site owns only the registry read.
		ExtraAddressees:       extraAddressees(fa, me),
		HasFulfillingResponse: hasFulfillingResponse(fa),
		BlockedByOwner:        blockedByOwner(fa),
		DeliveryUnresolvable:  fa.DeliveryUnresolvable,
		// P5 AC1 (spec 05): resolved once in mirror.go's own build pass
		// (contractOperationalDebtOwed) for the same reason every other
		// registry/floor-derived fact here is — this package can do the
		// I/O internal/pendency deliberately cannot.
		OperationalDebtOwed: fa.OperationalDebtOwed,
	}
	if fa.kind() == fold.KindResponse {
		in.ParentFrom = parentFrom
		in.ParentDisputeReopenFailed = fa.ParentDisputeReopenFailed
	}
	verdict, err := pendency.Resolve(in)
	if err != nil {
		// A lookup miss means fa's own (kind, state) is not one of the
		// pairs internal/fold.SubjectStates enumerates — a terminal state
		// pendency carries no row for at all (every terminal state this
		// package's own isOpen would also reject) or a kind pendency does
		// not model. Every consumer below is a strict subset of "the
		// relation names me for transition X"; with no verdict there is
		// nothing to filter, so all of them legitimately answer no rather
		// than re-deriving their own fallback rule.
		//
		// Why is populated even here, and deliberately: pendency's contract
		// is that "nobody owes anything" is a CLAIM to be justified, never a
		// fall-through. The thread view had this and the inbox did not,
		// which is the same one-answer-two-surfaces split this phase exists
		// to close, one level below the fields themselves.
		//
		// The text is plain because it GOES ON THE WIRE. It used to read
		// "this should be unreachable — see buildOpenItems' own doc comment",
		// which was true where it was written: the thread view filters by
		// isOpen before it asks, so a miss there means the two tables have
		// drifted. It is not true here. The inbox lists a terminal artifact
		// addressed to me, asks about it, and legitimately gets no row — so
		// that sentence reached `a2a inbox --json` and told a reader their
		// declined work request was an internal invariant violation.
		//
		// The drift claim did not disappear, it moved somewhere it can be
		// PROVEN: TestEveryLiveStateHasAPendencyRow. A runtime string cannot
		// detect a table gap that a gate can refuse outright.
		return pendency.Verdict{Why: "no obligation is defined for a " + string(fa.kind()) + " in state " + string(fa.Result.State)}
	}
	return verdict
}

// actionableReasons returns why this artifact is actionable for `me`, AND
// the pendency verdict it derived them from.
//
// The verdict is returned rather than discarded because the four surfaces
// this epic must reconcile all need it, and recomputing it per surface is
// what I7 forbids. `a2a inbox --json` carries waiting_on, expected_transition
// and why for the first time as a consequence: they were computed here all
// along and thrown away at the door.
//
// This is a 3-arg convenience wrapper over actionableReasonsForResponse that
// passes parentFrom="" — kept because notifications.go and statusline.go
// call this exact form and neither iterates with a per-space id->artifact
// index in hand to resolve a response's parent. That is safe FOR THEM
// specifically, not in general: of the five OP-207 conditions below, only
// 1/2/3/5 read verdict.Expected, and each tests it against TAcknowledge,
// TClose, TRespond (kind-gated to exclude KindResponse) or TApprove — none
// of which a response artifact's own verdict ever produces (its only two
// possible Expected values are TVerify, with a resolved parent, or "",
// without one). So `reasons` for a response artifact is byte-identical
// whether or not parentFrom is supplied; only the discarded Verdict return
// value would differ, and both of this wrapper's callers discard it. Store
// itself never calls this form — Store.inbox calls
// actionableReasonsForResponse directly, with a real parentFrom.
func actionableReasons(fa foldedArtifact, me string, manifest space.Manifest) ([]string, pendency.Verdict) {
	return actionableReasonsForResponse(fa, me, manifest, "")
}

// actionableReasonsForResponse is actionableReasons' full form: parentFrom
// is threaded straight into resolveVerdict, so a caller iterating a space's
// full artifact set (Store.inbox, via responseParentFrom) gets the SAME
// verdict for a response artifact that the thread surface does, rather than
// the degraded "nobody owes anything" answer a bare "" produces.
func actionableReasonsForResponse(fa foldedArtifact, me string, manifest space.Manifest, parentFrom string) ([]string, pendency.Verdict) {
	var reasons []string
	kind := fa.kind()
	env := fa.Env
	state := fa.Result.State

	verdict := resolveVerdict(fa, me, manifest, parentFrom)
	iOwe := containsString(verdict.Owners, me)

	// 1: {addressed to me with no ack by me} — a pure filter, for every
	// kind including announcement. For requirement/published, question or
	// work_request/submitted and handoff/submitted (all `target` rows) the
	// state transition itself IS the ack, so "currently in that state"
	// already means "not yet acked"; for announcement/published the
	// relation's `unackedTargets` row does the per-recipient Acks read.
	//
	// This condition gains the `ack_requested` qualifier in this wave: an
	// announcement with no ack requested is no longer
	// actionable-for-lack-of-ack (domain 3.4.7 — delivery completes on
	// publish, no acknowledgement required). Verified safe before it was
	// written: `contract deprecate` does set `ack_requested: true` on the
	// announcement it emits (internal/mcp/tools_contract.go:426, pinned by
	// internal/cli/cmd_contract_test.go:287).
	//
	// P4 Edge 3's late adopter is preserved WITHOUT a second rule here:
	// the registry-derived addressee is handed to the relation as
	// ExtraAddressees above, so "who is addressed" stays a caller-resolved
	// FACT while "an addressed system that has not acknowledged owes an
	// acknowledge" stays the relation's own single RULE.
	// TestInboxDeprecationReachesALateAdopter keeps the teeth on it.
	if verdict.Expected == fold.TAcknowledge && iOwe {
		reasons = append(reasons, "addressed-no-ack")
	}

	// 2: {responded awaiting my verify/close} — the relation's
	// question/work_request `responded` row names the SENDER (envelope
	// `from`) as owing `close`; I match it when I'm that sender.
	if verdict.Expected == fold.TClose && iOwe {
		reasons = append(reasons, "responded-awaiting-verify-close")
	}

	// 3: {disputed toward me} — the relation names me as the target
	// presently owing `respond` (D-024's reopen re-admits the
	// acknowledged/accepted/in_progress target-owes-respond row after a
	// dispute), filtered to fire only when THIS exchange was actually
	// reopened by a dispute. fold.Result.Responses' per-response
	// sub-state is outside pendency.Input's own shape (it is not one of
	// the facts any row resolves from), so that half stays a local read
	// of the fold-computed fact rather than a second rule about WHO owes
	// the respond — the relation still answers that half.
	if (kind == fold.KindQuestion || kind == fold.KindWorkRequest) &&
		verdict.Expected == fold.TRespond && iOwe {
		for _, rs := range fa.Result.Responses {
			if rs == fold.StateDisputed {
				reasons = append(reasons, "disputed-toward-me")
				break
			}
		}
	}

	// 4: {p1 or blocking, any open state} — either party (from or to);
	// this condition carries no explicit "to me" qualifier in OP-207's
	// text, unlike the other four (v1-min spec 07 §11).
	//
	// DELIBERATELY NOT a pendency filter (P11 W1's own named exemption
	// from the I7 "no second pendency implementation" gate): this is a
	// priority filter over LIVENESS (isOpen), a different axis, on
	// purpose broader than pendency. Pendency answers "is the next move
	// owed TO me"; this condition answers "is a p1/blocking item still
	// open at all, on either side" — it exists precisely to surface an
	// urgent item that is the OTHER side's move, so I is a party to it
	// while awaiting them stays visible instead of silently dropping out
	// once I'm not the one who owes the next transition. Folding this
	// into pendency would delete the one case it exists for. It keeps
	// computing over isOpen (never pendency), unchanged from before this
	// wave.
	if (env.Priority == "p1" || env.Blocking) && isOpen(kind, state) &&
		(env.From == me || addressedToMe(fa, me)) {
		reasons = append(reasons, "p1-or-blocking-open")
	}

	// 5: {gate pending on me} — the relation's decision/proposed row
	// names the required approvers who have not yet approved; the only
	// gate internal/fold models (G1/G2/G4/G5 are GitHub PR-review gates
	// this read-only mirror composition cannot see, v1-min spec 07 §11).
	if verdict.Expected == fold.TApprove && iOwe {
		reasons = append(reasons, "gate-pending-on-me")
	}

	// 6: {operational activation owed on my own published contract} — P5
	// AC1 (specs/05-declared-nature.md). Not one of OP-207's own 5
	// (§T1), added on top of it: without a matching condition here, the
	// row internal/pendency's contract/published resolver now computes
	// (epic-backlog B19's own warning — "a row nobody can see is not a
	// named owner") would be silently unreachable through
	// `a2a inbox --actionable`, the ONE inbox scope that lists an item
	// not addressed to me (Store.inbox's `actionableOnly` branch skips
	// addressedToMe entirely). The literal below is not a fold.T*
	// constant for the same reason pendency.go's own contractActivate
	// documents: internal/fold's transition table carries no `activate`
	// row at all, because activation is a side fact about a published
	// version's operational readiness, never a contract lifecycle
	// transition.
	if verdict.Expected == "activate" && iOwe {
		reasons = append(reasons, "activation-owed")
	}

	return reasons, verdict
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// byArtifactID builds an id -> foldedArtifact index from a []foldedArtifact
// slice, keyed on Env.ID — the shape three separate call sites in this
// package built inline (buildShowResult's ref-resolution,
// yourMoveByArtifact's open-item lookup, and Store.inbox/Store.outbox's own
// per-space response-parent lookup below), extracted once a third caller
// needed it (rule of three). A fourth, pre-existing inline copy lives in
// threadview.go's renderThread (byID); that file is outside this change's
// allowlist and is left as-is.
func byArtifactID(artifacts []foldedArtifact) map[string]foldedArtifact {
	out := make(map[string]foldedArtifact, len(artifacts))
	for _, fa := range artifacts {
		out[fa.Env.ID] = fa
	}
	return out
}

// responseParentFrom resolves the parent's envelope `from` for a response
// artifact, given byID (a per-space id->artifact index built by
// byArtifactID). Returns "" for every non-response kind — that field is
// meaningless for anything else, and resolveVerdict itself only reads
// parentFrom for fold.KindResponse.
//
// When the parent cannot be found in byID, this degrades to the response's
// OWN `from` rather than "" — the SAME fallback buildOpenItems
// (threadview.go) already applies for the thread surface's authEnv. That
// keeps the DEGRADED ANSWER the same shape on both surfaces (a real, if
// weaker, from-id — never a silent "nobody owes anything").
//
// It does not, by itself, keep the two surfaces choosing the SAME branch on
// every input, because the two byID indexes have different domains by
// design: this one is the WHOLE SPACE (this function's own caller, per the
// brief that introduced it), while buildOpenItems' byID is deliberately the
// RENDERED THREAD MEMBER SET only (threadview.go's own doc comment on
// renderThread's byID). So a response whose parent lives in the same space
// but a DIFFERENT thread — already a REF-009/CC-073 violation, "a fork or a
// broken propagation" per that same comment — resolves its real parent here
// and degrades to its own `from` on the thread surface: a residual
// divergence on that one already-anomalous shape, not on the normal case
// (parent absent from the space entirely, or parent present in the thread),
// where both surfaces agree. See this phase's own Deviations report.
func responseParentFrom(fa foldedArtifact, byID map[string]foldedArtifact) string {
	if fa.kind() != fold.KindResponse {
		return ""
	}
	if parent, ok := byID[fa.Env.Parent]; ok {
		return parent.Env.From
	}
	return fa.Env.From
}
