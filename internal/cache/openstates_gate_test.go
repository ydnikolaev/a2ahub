package cache

import (
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
)

// TestOpenStatesReachesEveryStateSomebodyOwesAMoveFrom is the reachability
// gate between this package's LIVENESS allowlist and internal/pendency's
// OWED-MOVE relation.
//
// buildOpenItems asks `isOpen` BEFORE it asks pendency (threadview.go). So a
// (kind, state) pair that pendency says somebody owes a move from, but
// openStates does not admit, produces an answer nobody can ever read: the
// relation is right, the surface never asks it, and the system owing the move
// is never told. That is not a narrower rendering — it is a silent one.
//
// It had already happened when this gate was written. `handoff/rejected` is a
// state the domain's own §3.4.5 puts the producer on the hook for (resubmit as
// a new XH, superseding this one) and pendency's table says exactly that, but
// openStates listed handoff as live only in draft/submitted/acknowledged. A
// rejected handoff therefore vanished from `a2a thread`'s open items entirely,
// and the producer was never told to resubmit. Found by W3's data-loop
// conformance path — by composition, not by any single-transition test, which
// is the whole argument for that catalogue.
//
// The converse is deliberately NOT asserted. openStates may legitimately admit
// a state pendency answers "nobody" for: a published contract is alive, may
// still be deprecated, and belongs in next_actions — it simply owes nothing.
// Liveness ⊋ pendency is the model; this gate polices only the direction that
// loses information.
func TestOpenStatesReachesEveryStateSomebodyOwesAMoveFrom(t *testing.T) {
	t.Parallel()

	pairs := fold.SubjectStates()
	if len(pairs) == 0 {
		t.Fatal("fold.SubjectStates() returned no pairs — the universe is empty and this gate is watching nothing")
	}

	var unreachable []string
	checked := 0
	for _, p := range pairs {
		verdict, err := pendency.Resolve(pendency.Input{
			Kind: p.Kind, State: p.State,
			// Facts chosen so every productive resolver yields somebody:
			// a `nobody` answer below is then the TABLE's own verdict, never
			// an artefact of a missing envelope fact.
			From: "owner-sys", To: []string{"target-sys"},
			AckRequested:       true,
			RequiredApprovers:  []string{"approver-sys"},
			ActiveParticipants: []string{"owner-sys", "target-sys"},
			ParentFrom:         "parent-owner-sys",
		})
		if err != nil {
			// A pair with no row is I8's business, not this gate's.
			continue
		}
		checked++
		if len(verdict.Owners) == 0 {
			continue
		}
		if !isOpen(p.Kind, p.State) {
			unreachable = append(unreachable, string(p.Kind)+"/"+string(p.State)+
				" — pendency says "+strings.Join(verdict.Owners, ",")+
				" owes "+verdict.Expected+", but openStates does not admit the state, "+
				"so buildOpenItems filters it out before ever asking")
		}
	}

	if checked == 0 {
		t.Fatal("no (kind, state) pair resolved at all — pendency's table or this gate's inputs are broken, and a green result here would mean nothing")
	}

	if len(unreachable) > 0 {
		sort.Strings(unreachable)
		t.Fatalf("%d state(s) carry an owed move that no surface can report (%d pair(s) checked):\n  %s",
			len(unreachable), checked, strings.Join(unreachable, "\n  "))
	}
}

// TestEveryLiveStateHasAPendencyRow is the OTHER direction, and it is the one
// a runtime string used to narrate instead of proving.
//
// The gate above polices `pendency owes ⟹ isOpen`. This one polices
// `isOpen ⟹ pendency has a row at all` — not that the row answers somebody
// (a published contract legitimately owes nothing, which is why the converse
// stays unasserted above), but that `pendency.Resolve` does not return
// ErrUnknownState for a pair this package calls live.
//
// It existed as prose. `buildOpenItems` degraded a table miss to a Why that
// read "this should be unreachable — see buildOpenItems' own doc comment",
// and when P2 gave the inbox the same justification contract that string
// went ON THE WIRE — where it is not merely unhelpful but false, because the
// inbox lists terminal artifacts and asking about one is ordinary.
//
// A claim about a table is a gate's job. All 30 live pairs pass today, so
// the claim is true; what it lacked was anything that would notice when it
// stopped being.
func TestEveryLiveStateHasAPendencyRow(t *testing.T) {
	t.Parallel()

	var missing []string
	live := 0
	for kind, states := range openStates {
		for state, isLive := range states {
			if !isLive {
				continue
			}
			live++
			// From/To are placeholders: this asks whether a ROW exists for
			// the (kind, state) pair, never who it names.
			if _, err := pendency.Resolve(pendency.Input{
				Kind: kind, State: state,
				From: "sender-sys", To: []string{"target-sys"},
			}); err != nil {
				missing = append(missing, string(kind)+"/"+string(state)+": "+err.Error())
			}
		}
	}

	if live == 0 {
		t.Fatal("openStates admits no live pair at all — this gate is watching nothing")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d of %d live (kind, state) pairs have no pendency row, so every surface "+
			"asking about one gets a degraded verdict instead of an answer:\n  %s",
			len(missing), live, strings.Join(missing, "\n  "))
	}
}
