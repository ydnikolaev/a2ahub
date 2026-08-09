package fold

import "testing"

// TestOutcomeIsTotalOverRestingStates is the acceptance criterion stated
// as the enumerator demands it: iterate RestingStates(), never a literal
// list. A state added to the table with no outcome fails HERE rather than
// defaulting to `open` on four surfaces at once.
func TestOutcomeIsTotalOverRestingStates(t *testing.T) {
	t.Parallel()

	for _, krs := range RestingStates() {
		if got := OutcomeOf(krs.Kind, krs.State); got == "" {
			t.Errorf("Outcome(%s, %s) is empty — every pair RestingStates() yields must have a decided meaning", krs.Kind, krs.State)
		}
	}
}

// TestOutcomesCarriesNoSurplusPair is the other direction. A pair the map
// claims but no subject can hold reads as coverage without being any: it
// would satisfy the totality test above by existing, while describing a
// state the domain cannot produce.
func TestOutcomesCarriesNoSurplusPair(t *testing.T) {
	t.Parallel()

	resting := make(map[KindRestingState]bool)
	for _, krs := range RestingStates() {
		resting[krs] = true
	}
	for kind, byState := range outcomes {
		for state := range byState {
			if !resting[KindRestingState{Kind: kind, State: state}] {
				t.Errorf("outcomes declares (%s, %s) but RestingStates() does not yield it — either the row table lost a state or this entry describes one no subject can hold", kind, state)
			}
		}
	}
}

// TestOutcomeOutsideRestingStatesIsEmpty pins the zero value as the honest
// answer for a pair the domain cannot produce. Returning OutcomeOpen here
// would have a renderer promise a reader that a state it does not
// understand is still live.
func TestOutcomeOutsideRestingStatesIsEmpty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind  Kind
		state State
	}{
		{KindContract, StateInProgress}, // a contract is never in progress
		{KindQuestion, StateRetired},    // retirement is a contract's ending
		{KindDecision, StateClosed},     // decisions approve or reject
		{KindQuestion, StateDynamic},    // the sentinel is never a resting state
		{"no_such_kind", StatePublished},
	}
	for _, c := range cases {
		if got := OutcomeOf(c.kind, c.state); got != "" {
			t.Errorf("Outcome(%s, %s) = %q, want the zero value", c.kind, c.state, got)
		}
	}
}

// TestOpenOutcomeIsNeverTerminal is the consistency the two accessors owe
// each other: if the domain calls a state live, something must be able to
// follow it. A state that is `open` with no legal transition out is a
// dead end the domain describes as live — a real defect, not a naming
// quibble, and this is where it surfaces.
//
// The converse is deliberately NOT asserted: a concluded state is often
// non-terminal (a rejected decision can be superseded), which is exactly
// why Terminal is derived from the rows rather than from Outcome.
func TestOpenOutcomeIsNeverTerminal(t *testing.T) {
	t.Parallel()

	for _, krs := range RestingStates() {
		if OutcomeOf(krs.Kind, krs.State) != OutcomeOpen {
			continue
		}
		if Terminal(krs.Kind, krs.State) {
			t.Errorf("(%s, %s) is OutcomeOpen but Terminal — no row leaves it, so nothing can ever follow a state the domain calls live", krs.Kind, krs.State)
		}
	}
}

// TestTerminalNamedCases pins the pairs seam.md calls out, so a future
// change to the rows that quietly flips one of them reads as the decision
// it is.
func TestTerminalNamedCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind  Kind
		state State
		want  bool
		why   string
	}{
		{KindDecision, StateRejected, false, "a rejected decision can still be superseded — refused AND non-terminal"},
		{KindHandoff, StateRejected, false, "the producer may supersede a failed handoff; internal/cache's openStates agrees it is live"},
		{KindHandoff, StateAccepted, true, "verify-pass is where a handoff ends; no row leaves it"},
		{KindQuestion, StateAccepted, false, "the same word on an exchange is the middle of the work, not its end"},
		{KindContract, StateRetired, true, "retirement is a contract's ending"},
		{KindContract, StateDeprecated, false, "a deprecated contract can still be retired"},
		{KindQuestion, StateClosed, true, "a closed exchange has no successor transition"},
		// Was `true` ("the dispute concludes this response"), then `false`
		// from 2026-08-08 to 2026-08-09 while a supersede row gave the
		// producer an exit matching decision/rejected and handoff/rejected.
		// P8's tagged conformance matrix proved no shipped verb ever reached
		// that row — a response's closure state is sub-state on the
		// PARENT's Result.Responses, and the only reader of that sub-state
		// never dispatches a bare `supersede` to it — so the row was
		// deleted (spec 06's amendment, epic-backlog B8) rather than routed
		// to. Terminal is back to `true`: no row in table.go departs
		// (response, disputed) any more. The remedy still exists, one level
		// up — the dispute reopens the PARENT to in_progress, where the
		// producer owes a fresh respond — but that is a fact about the
		// PARENT's Terminal, never this response's own.
		{KindResponse, StateDisputed, true, "the supersede exit was deleted 2026-08-09: no row in table.go departs (response, disputed) any more, and the remedy (a fresh respond) lives on the reopened PARENT, not on this artifact"},
	}
	for _, c := range cases {
		if got := Terminal(c.kind, c.state); got != c.want {
			t.Errorf("Terminal(%s, %s) = %v, want %v — %s", c.kind, c.state, got, c.want, c.why)
		}
	}
}

// TestOutcomeKindIsLoadBearing is the single case that proves the Kind on
// the signature earns its place: `accepted` means opposite things on a
// handoff and on an exchange, and the dashboard's retired literal sets
// could not have been right for both.
func TestOutcomeKindIsLoadBearing(t *testing.T) {
	t.Parallel()

	if got := OutcomeOf(KindHandoff, StateAccepted); got != OutcomeSettled {
		t.Errorf("Outcome(handoff, accepted) = %q, want %q — it is what verify-pass produces and no row leaves it", got, OutcomeSettled)
	}
	if got := OutcomeOf(KindQuestion, StateAccepted); got != OutcomeOpen {
		t.Errorf("Outcome(question, accepted) = %q, want %q — the target took the work on and has not done it yet", got, OutcomeOpen)
	}
}

// TestRetiredIsSettledNotCancelled is the second literal-set defect named
// as its own assertion: the component filed `retired` under CANCELLED, so
// an orderly retirement and an abandoned artifact rendered identically.
func TestRetiredIsSettledNotCancelled(t *testing.T) {
	t.Parallel()

	if got := OutcomeOf(KindContract, StateRetired); got != OutcomeSettled {
		t.Errorf("Outcome(contract, retired) = %q, want %q — retiring a contract is how a contract's life is supposed to end", got, OutcomeSettled)
	}
}
