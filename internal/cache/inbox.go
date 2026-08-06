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

// actionableReasons evaluates every one of OP-207's 5 normative
// `--actionable` conditions (quoted verbatim in spec 07 §T1) against fa
// for system me, returning the subset that matched (nil if none). This
// is NOT scoped by addressedToMe: condition 2 keys off `from`==me
// (I'm the owner awaiting my own verify/close) and condition 4 applies
// to any item I'm a party to (from OR to) — see this phase's Deviations
// report for the reading of condition 4's unqualified "any open state".
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
func actionableReasons(fa foldedArtifact, me string, manifest space.Manifest) []string {
	var reasons []string
	kind := fa.kind()
	env := fa.Env
	state := fa.Result.State

	verdict, err := pendency.Resolve(pendency.Input{
		Kind: kind, State: state,
		From: env.From, To: normalizeTo(env.To),
		Broadcast: env.isBroadcast(), AckRequested: env.AckRequested,
		Acks: fa.Result.Acks, Approvals: fa.Result.Approvals,
		RequiredApprovers:  env.RequiredApprovers,
		ActiveParticipants: activeParticipants(manifest, env.From),
		// P4 Edge 3, handed in rather than re-derived: this system's own
		// registry says it consumes the contract this announcement
		// deprecates, so it is addressed even though the frozen `to:`
		// predates its adoption. pendency owns what that MEANS; this call
		// site owns only the registry read.
		ExtraAddressees: extraAddressees(fa, me),
	})
	if err != nil {
		// A lookup miss means fa's own (kind, state) is not one of the
		// pairs internal/fold.SubjectStates enumerates — a terminal state
		// pendency carries no row for at all (every terminal state this
		// package's own isOpen would also reject) or a kind pendency does
		// not model. Every condition below is a strict subset of "the
		// relation names me for transition X"; with no verdict there is
		// nothing to filter, so all four legitimately answer no rather
		// than re-deriving their own fallback rule.
		verdict = pendency.Verdict{}
	}
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

	return reasons
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
