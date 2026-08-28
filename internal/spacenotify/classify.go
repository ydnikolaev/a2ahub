package spacenotify

import "github.com/ydnikolaev/a2ahub/internal/cache"

// classify computes fa's own class — spec 03 step 3, evaluated ONCE per
// artifact, before any per-route filtering (step 4 never changes it, only
// keeps or drops the artifact for a given route). An artifact that is
// both p1 AND gate-pending gets exactly one class, deterministically:
// human-gate always wins, because a gate blocks on a human regardless of
// priority.
//
// P11 (answers-that-hold-2026-08 spec 11) turns this cascade into three
// named PRESET predicates below — presetHumanGate/presetBlocking/
// presetPublished — and classify is now their composition, never a second,
// independently-maintained copy of the same three-way split (AC-6/AC-7:
// "each legacy class keeps EXACTLY its current membership when expressed
// as a preset" is true by CONSTRUCTION here, not by coincidence).
func classify(fa cache.NotifyArtifact) string {
	switch {
	case presetHumanGate(fa):
		return ClassHumanGate
	case presetBlocking(fa):
		return ClassBlocking
	default:
		return ClassPublished
	}
}

// globalInbound is classify's own "receiving participant" test — the
// ARTIFACT's own fact (does ANY participant receive it, addressed or
// broadcast), never scoped to one route's own `for:`. This is deliberately
// NOT the same predicate as the P11 selector's `direction: inbound`
// dimension (selector.go's directionMatches), which asks whether THIS
// route's own `for:` participant is among the addressees. Reusing the
// route-scoped predicate here would make presetBlocking's membership
// depend on which route asked, which classify (a route-independent,
// once-per-artifact computation, AC-14) must never do.
func globalInbound(fa cache.NotifyArtifact) bool {
	return len(fa.Addressees) > 0 || fa.Broadcast
}

// presetHumanGate is the human-gate preset (P11 spec 11 §T1): "urgency:
// human-gate" — fa.Verdict.HumanGate is non-empty, internal/fold's own
// §3.7 gate name (G1-G5), never a second gate list here.
func presetHumanGate(fa cache.NotifyArtifact) bool {
	return fa.Verdict.HumanGate != ""
}

// presetBlocking is the blocking preset: "direction: inbound" +
// "urgency: [p1, blocking]" — an artifact addressed to at least one
// participant (globalInbound, above) AND p1 or flagged blocking. Ranked
// below presetHumanGate: an artifact satisfying both gets human-gate, the
// same precedence classify's own cascade has always applied.
func presetBlocking(fa cache.NotifyArtifact) bool {
	return !presetHumanGate(fa) && globalInbound(fa) && (fa.Priority == "p1" || fa.Blocking)
}

// presetPublished is the published preset: the complement of the other
// two — every other qualifying artifact.
func presetPublished(fa cache.NotifyArtifact) bool {
	return !presetHumanGate(fa) && !presetBlocking(fa)
}
