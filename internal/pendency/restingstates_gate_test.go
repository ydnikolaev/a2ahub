package pendency

import (
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestEveryRestingStateHasAPendencyRow is AC10's gate (spec 06, §8) and it
// checks a WIDER universe than TestI8Totality does.
//
// TestI8Totality asks the table against fold.SubjectStates() — every (Kind,
// From) pair some transition row departs FROM. That misses every state a
// subject can be found sitting AT REST but that no row ever departs from
// again: a terminal state such as `question/closed`, `handoff/accepted` (the
// verified-and-done resting point) or `contract/retired`. Those pairs never
// appear as a From in the fold table, so I8 never asks pendency about them —
// and until this gate, pendency carried no row for them at all.
//
// That gap was not hypothetical: internal/cache/inbox.go's own
// resolveVerdict doc comment records that a lookup miss on exactly one of
// these terminal pairs used to degrade to a runtime string ("this should be
// unreachable...") that reached `a2a inbox --json` and told a reader their
// declined work request was an internal invariant violation. A claim about a
// table is this gate's job; the runtime string could only narrate it.
//
// AC10's ordering requirement is the point of this gate's existence: it must
// be committed, green, and PROVEN to have teeth (a removed row reddens it by
// name) BEFORE `{response, disputed, supersede}` is deleted from table.go —
// deleting that row changes what fold.RestingStates() returns (response's
// own `superseded` leaves the universe entirely, because no other response
// row ever produces it), and a gate written after the deletion could not
// have told anyone the deletion was safe.
func TestEveryRestingStateHasAPendencyRow(t *testing.T) {
	pairs := fold.RestingStates()
	if len(pairs) == 0 {
		t.Fatal("fold.RestingStates() returned no pairs — universe is empty, gate cannot check anything")
	}

	var missing []string
	for _, p := range pairs {
		if _, ok := table[key{Kind: p.Kind, State: p.State}]; !ok {
			missing = append(missing, string(p.Kind)+"/"+string(p.State))
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("pendency table has no row for %d/%d fold.RestingStates() pairs: %v",
			len(missing), len(pairs), missing)
	}
}

// TestEveryRestingStateHasAJustifiedWhy is README criterion 1's "justified
// terminal verdict" half, quantified over the SAME universe as
// TestEveryRestingStateHasAPendencyRow above, rather than asserted by a
// hand-maintained case table (agent-exchange-2026-08 P12 G4: the readiness
// audit found TestResolveRows/pendency_test.go:308-310 only asserts
// `Why != ""` over its own 36-case table, which happens to cover every
// fold.SubjectStates() pair but was never checked against
// fold.RestingStates() itself — the wider universe the criterion actually
// names).
//
// Every pair gets the same generic (From, To) facts — the resolvers this
// package ships (owner/target/parentOwner/unackedTargets/pendingApprovers,
// see pendency.go) all degrade to a nil owner set on a zero-value fact
// rather than panicking, and Resolve's own "owners empty" branch (pendency.go
// Resolve, `len(owners) == 0`) still runs r.why or r.onEmpty(in) — so a row
// with an unattributable owner is exactly where a blank Why would slip
// through undetected by presence-only coverage. Some pairs (e.g. a
// `blocked` row's BlockedByOwner-set branch) need a real fact to reach their
// non-default whyFor branch; those are already exercised by
// TestBlockedNamesTheOwnerInsteadOfTheTarget and pendency_test.go's own
// table. This loop's job is the FLOOR: no pair, reached with nothing more
// than From/To, is ever left with an empty rationale.
func TestEveryRestingStateHasAJustifiedWhy(t *testing.T) {
	pairs := fold.RestingStates()
	if len(pairs) == 0 {
		t.Fatal("fold.RestingStates() returned no pairs — universe is empty, gate cannot check anything")
	}

	checked := 0
	for _, p := range pairs {
		in := Input{Kind: p.Kind, State: p.State, From: "sys-a", To: []string{"sys-b"}}
		v, err := Resolve(in)
		if err != nil {
			t.Errorf("Resolve(%+v) returned error: %v", in, err)
			continue
		}
		checked++
		if v.Why == "" {
			t.Errorf("%s/%s: Why is empty — every resting-state verdict, including a settled/nobody one, must name its rule", p.Kind, p.State)
		}
	}
	if checked != len(pairs) {
		t.Fatalf("checked %d of %d fold.RestingStates() pairs — every pair must reach Resolve without error for this gate to mean anything", checked, len(pairs))
	}
}
