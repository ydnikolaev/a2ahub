package cache

import "github.com/ydnikolaev/a2ahub/internal/fold"

// addressedToMe is §4.2's base inbox query ("all artifacts across the
// space where `to` includes it") — the SCOPE `a2a inbox` (no flag)
// lists; `--actionable`'s own 5-condition union (actionableReasons) is a
// DIFFERENT, broader query that is not pre-filtered by this (some
// conditions, e.g. "responded awaiting my verify/close", key off `from`,
// not `to` — see actionableReasons' own doc comment).
func addressedToMe(fa foldedArtifact, me string) bool {
	if fa.Env.isBroadcast() {
		return true
	}
	return containsString(normalizeTo(fa.Env.To), me)
}

// actionableReasons evaluates every one of OP-207's 5 normative
// `--actionable` conditions (quoted verbatim in spec 07 §T1) against fa
// for system me, returning the subset that matched (nil if none). This
// is NOT scoped by addressedToMe: condition 2 keys off `from`==me
// (I'm the owner awaiting my own verify/close) and condition 4 applies
// to any item I'm a party to (from OR to) — see this phase's Deviations
// report for the reading of condition 4's unqualified "any open state".
func actionableReasons(fa foldedArtifact, me string) []string {
	var reasons []string
	kind := fa.kind()
	env := fa.Env
	state := fa.Result.State

	// 1: {addressed to me with no ack by me}.
	switch kind {
	case fold.KindAnnouncement:
		if addressedToMe(fa, me) && !fa.Result.Acks[me] {
			reasons = append(reasons, "addressed-no-ack")
		}
	default:
		if pre, ok := preAckState[kind]; ok && env.to0() == me && state == pre {
			reasons = append(reasons, "addressed-no-ack")
		}
	}

	// 2: {responded awaiting my verify/close} — I'm the requester
	// (from==me); only question/work_request carry a `respond` row.
	if (kind == fold.KindQuestion || kind == fold.KindWorkRequest) && state == fold.StateResponded && env.From == me {
		reasons = append(reasons, "responded-awaiting-verify-close")
	}

	// 3: {disputed toward me} — I'm the target the reopened item is now
	// back on.
	if (kind == fold.KindQuestion || kind == fold.KindWorkRequest) && env.to0() == me {
		for _, rs := range fa.Result.Responses {
			if rs == fold.StateDisputed {
				reasons = append(reasons, "disputed-toward-me")
				break
			}
		}
	}

	// 4: {p1 or blocking, any open state} — either party (from or to);
	// this condition carries no explicit "to me" qualifier in OP-207's
	// text, unlike the other four (see Deviations report).
	if (env.Priority == "p1" || env.Blocking) && isOpen(kind, state) &&
		(env.From == me || addressedToMe(fa, me)) {
		reasons = append(reasons, "p1-or-blocking-open")
	}

	// 5: {gate pending on me} — the only gate internal/fold models is the
	// decision quorum gate (RequiredApprovers/Approvals); G1/G2/G4/G5 are
	// GitHub PR-review gates this read-only mirror composition cannot see
	// (see Deviations report).
	if kind == fold.KindDecision && state == fold.StateProposed &&
		containsString(env.RequiredApprovers, me) && !fa.Result.Approvals[me] {
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
