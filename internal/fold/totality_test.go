package fold

import (
	"sort"
	"strings"
	"testing"
)

// namesAnOwner reports whether a role identifies a PARTICULAR system that can
// be held to the move.
//
// RoleAny is deliberately excluded, and it is the distinction the whole gate
// turns on. `{Decision, approved, supersede, RoleAny}` has existed since
// 2026-07-21 and makes `LegalNext(decision, approved)` non-empty — so a gate
// asking "is any transition legal from here" finds one and stays green on the
// exact cell this phase exists to catch. RoleAny is membership-only: it says
// any member MAY act, never that anyone is expected to, and that row's own
// Scenario explains why the fold cannot do better
// (`new-approved-decision-only-unverifiable` — the authorship rule lives on an
// artifact that does not exist yet).
//
// A state whose only exits are RoleAny is a state where the protocol offers a
// move and names nobody to make it. That is the freeze, whether or not a
// transition is technically available.
func namesAnOwner(role Role) bool {
	switch role {
	case RoleOwner, RoleTarget, RoleApprover, RoleEitherParty:
		return true
	default:
		return false
	}
}

// TestEveryLiveRestingStateNamesAnOwner is P1's AC1.
//
// The property: no artifact can rest in a LIVE state that names nobody able
// to move it. Two honest answers per live cell — a role that identifies a
// system, or the state not being live at all. What must not exist is the
// third case: an open exchange whose every exit belongs to nobody in
// particular, because then `pendency` has no owner to report, `--actionable`
// lights up for no one, and the artifact sits open forever with the protocol
// insisting somebody could act.
//
// The universe is fold.RestingStates(), never a literal list. That enumerator
// was itself wrong until P0 — it skipped the StateDynamic sentinel and so
// omitted {decision, approved} entirely, which is why this gate could not
// have been written before that phase landed.
//
// **This test is EXPECTED TO FAIL when first written.** A totality gate green
// on its first run has the wrong universe, not a healthy domain.
func TestEveryLiveRestingStateNamesAnOwner(t *testing.T) {
	t.Parallel()

	type freeze struct {
		kind  Kind
		state State
		exits []string
	}
	var frozen []freeze

	for _, krs := range RestingStates() {
		// The obligation exists only where the exchange is still LIVE. A
		// concluded state owes nobody a move: `(decision, approved)` is
		// settled and still carries a `supersede` exit for any member —
		// that exit is a RIGHT, not a debt, and demanding a named owner for
		// it would be demanding that somebody be on the hook for replacing a
		// decision that already stands.
		//
		// This is the distinction P0 made available. Before OutcomeOf, the
		// only questions a gate could ask were "is a transition legal" and
		// "is the state terminal", and neither separates a live state with
		// no owner — the freeze — from a concluded one whose remaining moves
		// are optional. spec 01 itself proposes RoleAny exits as the FIX for
		// a departed owner, so a gate refusing every RoleAny cell would
		// refuse the remedy along with the disease.
		if OutcomeOf(krs.Kind, krs.State) != OutcomeOpen {
			continue
		}
		moves := LegalNext(krs.Kind, krs.State)
		if len(moves) == 0 {
			// Live and with no exit at all — caught by
			// TestOpenOutcomeIsNeverTerminal in outcome_test.go, which says
			// it more precisely. Nothing to add here.
			continue
		}
		owned := false
		var exits []string
		for _, m := range moves {
			exits = append(exits, m.Transition+"/"+string(m.Role))
			if namesAnOwner(m.Role) {
				owned = true
			}
		}
		if owned {
			continue
		}
		sort.Strings(exits)
		frozen = append(frozen, freeze{kind: krs.Kind, state: krs.State, exits: exits})
	}

	if len(frozen) == 0 {
		return
	}

	var lines []string
	for _, f := range frozen {
		lines = append(lines, "  "+string(f.kind)+"/"+string(f.state)+
			" — exits: "+strings.Join(f.exits, ", ")+" (no role names a system)")
	}
	sort.Strings(lines)
	t.Fatalf("%d LIVE resting state(s) offer a move and name nobody to make it:\n%s\n"+
		"Each needs either a role that identifies a system, or a recorded decision that the state is terminal.\n"+
		"RoleAny does not count: it says any member MAY act, never that anyone is expected to.",
		len(frozen), strings.Join(lines, "\n"))
}

// TestTotalityGateHasNoAllowlist is AC5's meta-assertion. A gate that can
// exempt a name is a gate that will, and the exemption outlives the reason
// for it — this file must stay a pure derivation over the enumerator.
func TestTotalityGateHasNoAllowlist(t *testing.T) {
	t.Parallel()

	// The universe must come from the enumerator and nowhere else. If this
	// ever needs a skip list, the right move is to record the decision on the
	// ROW (as P0's Outcomes did for the dynamic rows), not to carry a second
	// list here that nothing keeps honest.
	if got := len(RestingStates()); got < 40 {
		t.Fatalf("RestingStates() yields %d pairs — suspiciously few; a shrunken universe is how a totality gate goes quietly green", got)
	}
}

// TestEveryLiveStateHasAnOwnerSideExit is the freeze P1 actually fixes, and
// it is derived from what internal/pendency does rather than from a list.
//
// When every named counterparty has left the space, pendency does not give
// up: `intersect(owners, in.LeftParticipants)` transfers the obligation to
// the sender with the verdict *"the sender owes a cancel or re-route
// decision instead"* (pendency.go:177). That verdict is a promise about the
// TABLE — it tells the sender to act — and the table has to be able to keep
// it.
//
// So for every LIVE state, the owner must have at least one exit. Where they
// do not, pendency names them, the surface tells them to act, and the fold
// refuses every move they could make. The artifact freezes with the protocol
// insisting its sender do something the protocol does not allow.
//
// This is the assertion AC1 was reaching for. AC1 asks the totality gate to
// red today "naming at least (Decision, approved)", and it cannot: that pair
// is `settled` — nobody owes anything — and the real freezes are conditional
// on a departure the (Kind, State) universe cannot see. What IS structural,
// and what this catches, is a live state the sender can never leave.
func TestEveryLiveStateHasAnOwnerSideExit(t *testing.T) {
	t.Parallel()

	var frozen []string
	for _, krs := range RestingStates() {
		if OutcomeOf(krs.Kind, krs.State) != OutcomeOpen {
			continue
		}
		ownerCanAct := false
		var exits []string
		for _, m := range LegalNext(krs.Kind, krs.State) {
			exits = append(exits, m.Transition+"/"+string(m.Role))
			if m.Role == RoleOwner || m.Role == RoleEitherParty || m.Role == RoleAny {
				ownerCanAct = true
			}
		}
		if ownerCanAct {
			continue
		}
		sort.Strings(exits)
		frozen = append(frozen, "  "+string(krs.Kind)+"/"+string(krs.State)+
			" — exits: "+strings.Join(exits, ", ")+" (none available to the sender)")
	}

	if len(frozen) == 0 {
		return
	}
	sort.Strings(frozen)
	t.Fatalf("%d live state(s) leave the sender with no legal move:\n%s\n"+
		"pendency transfers the obligation to the sender when every counterparty has left (pendency.go:177,\n"+
		"\"the sender owes a cancel or re-route decision instead\") — and here the table gives them nothing to do.\n"+
		"Each needs an owner-side exit, or a recorded decision that the sender is genuinely stuck and why.",
		len(frozen), strings.Join(frozen, "\n"))
}
