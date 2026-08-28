package notes

// impactRank is the release-note `impact:` vocabulary in reading order.
// Anything outside it sorts last rather than being refused: impact is
// authored prose, and a note with an unrecognised impact is still a note.
var impactRank = map[string]int{"high": 0, "normal": 1, "low": 2}

// ImpactOrder ranks a change's `impact:` for display. Lower sorts first;
// an unknown impact sorts after every known one.
//
// It lives HERE, below both surfaces, because both of them order by it and
// ADR-019 makes that the default rather than the exception. It was
// `whatsnewImpactOrder` in internal/cli until 2026-08-29, and the move was
// not tidying — it closed a live divergence. internal/mcp may not import
// internal/cli (ADR-001), so a2a_adapt could not reach the tie-break and
// sorted by group alone, while `a2a adapt` sorted by group AND impact. Two
// surfaces, one predicate, different answers — the class this epic exists
// to remove, committed inside it.
//
// check-cross-surface-citations is what found it, and it found it the way
// its own header says it would: not by catching the divergence, but by
// catching the COMMENT that wrote the divergence down.
func ImpactOrder(impact string) int {
	if r, ok := impactRank[impact]; ok {
		return r
	}
	return len(impactRank)
}
