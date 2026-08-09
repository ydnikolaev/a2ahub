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
