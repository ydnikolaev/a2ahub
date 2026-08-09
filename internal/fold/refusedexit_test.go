package fold

import "testing"

// refusedButTerminal is the honest, closed list of (Kind, State) pairs
// where OutcomeOf == OutcomeRefused AND Terminal is true, deliberately — a
// refused resting state with genuinely no legal exit, not a gap the Q3 fix
// (response/disputed's own supersede row, table.go) forgot to also close.
// Every entry carries a reason a reader can check against the rows
// themselves, same discipline internal/livee2e/pathcoverage_test.go's own
// uncoveredTransitions() already applies to a declared gap.
var refusedButTerminal = map[KindRestingState]string{
	{Kind: KindQuestion, State: StateDeclined}: "exchangeRows()'s cancel and supersede loops (table.go) both " +
		"omit StateDeclined — unlike requirementRows()'s own supersede loop, which DOES admit declined. A " +
		"declined question/work_request has no escape hatch at all; the remedy is a new exchange on the " +
		"thread, never a move on this one. Asymmetric with requirement/declined by construction, not by " +
		"omission — reported as a product finding, not fixed here (out of this phase's own scope).",
	{Kind: KindWorkRequest, State: StateDeclined}: "same shape as question/declined immediately above — " +
		"exchangeRows() is shared between the two kinds (table.go), so the gap and its reason are identical.",
}

// TestEveryRefusedStateHasAnExitOrADeliberateReason is the SHAPE test the
// 2026-08-08 amendment ("Q3 is no longer an argument, it is a measurement",
// plan 06) asked for over a count: TestTransitionRowsUniverse's row count
// would not have caught response/disputed's missing exit — the count went
// up when the row was ADDED, and would go up identically whether that row
// fixed this defect or created a different one. This test instead asks the
// question the defect was actually about: every REFUSED resting state
// either has a legal exit (Terminal == false) or is named, with a reason,
// as deliberately having none.
func TestEveryRefusedStateHasAnExitOrADeliberateReason(t *testing.T) {
	t.Parallel()

	for _, krs := range RestingStates() {
		if OutcomeOf(krs.Kind, krs.State) != OutcomeRefused {
			continue
		}
		if !Terminal(krs.Kind, krs.State) {
			// Has an exit — nothing more to check, and this pair must NOT
			// also be listed as deliberately terminal (that would claim a
			// row does not exist when it does).
			if reason, listed := refusedButTerminal[krs]; listed {
				t.Errorf("(%s, %s) is refused but has a legal exit (Terminal=false), yet refusedButTerminal names it deliberately terminal (%q) — stale entry", krs.Kind, krs.State, reason)
			}
			continue
		}
		reason, listed := refusedButTerminal[krs]
		if !listed {
			t.Errorf("(%s, %s) is refused AND terminal (no row in table.go departs it) but is not named in refusedButTerminal with a reason — either table.go is missing an exit, or this is a deliberate dead end that needs one", krs.Kind, krs.State)
			continue
		}
		if reason == "" {
			t.Errorf("(%s, %s) is listed in refusedButTerminal with an empty reason", krs.Kind, krs.State)
		}
	}
}

// TestRefusedButTerminalNamesOnlyRealPairs is the reverse half — same
// discipline as internal/livee2e's TestUncoveredTransitionsNamesOnlyRealTriples:
// an entry naming a pair that is not actually (refused AND terminal) today
// would exempt nothing, silently.
func TestRefusedButTerminalNamesOnlyRealPairs(t *testing.T) {
	t.Parallel()

	for krs := range refusedButTerminal {
		if OutcomeOf(krs.Kind, krs.State) != OutcomeRefused {
			t.Errorf("refusedButTerminal names (%s, %s), which is not OutcomeRefused", krs.Kind, krs.State)
		}
		if !Terminal(krs.Kind, krs.State) {
			t.Errorf("refusedButTerminal names (%s, %s), which is NOT terminal — a row in table.go departs it, so this entry is stale", krs.Kind, krs.State)
		}
	}
}
