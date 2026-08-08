package pendency

import (
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestEveryRowNamesAMoveItsOwnerMayActuallyMake is the authority gate: for
// every row in the table, the transition it says is owed must be (a) legal
// from that state at all, and (b) authorised for the very system the row
// names.
//
// Without it, a row can name a move the tool will then refuse — which is the
// one thing `skill/a2ahub/reference/threads.md` promises can never happen
// ("the computation comes from the same lifecycle engine the write verbs
// enforce, so the view can never name a move the tool would then refuse").
//
// It was written because that promise had already been broken. The
// requirement/acknowledged row said the TARGET owes `satisfy`. Both
// 03-domain.md §3.4.2 ("satisfy event is requester's") and fold's own row
// (Role: RoleOwner) say it is the REQUESTER's. So `a2a inbox --actionable`
// would have told the target to perform an event fold refuses from it. A
// human reading of the table missed it; this asks fold directly.
//
// The check composes two things fold already exports and adds no rule of its
// own: LegalNext for "is this transition available from this state, and under
// which role", and RoleAuthorizes for "does that role resolve to this system".
//
// Deliberately NOT checked here: rows that answer "nobody" (there is no move
// to authorise), and the announcement acknowledge (D-025 is transition-free
// and carries no table row at all — fold/legalnext.go's own doc comment).
func TestEveryRowNamesAMoveItsOwnerMayActuallyMake(t *testing.T) {
	t.Parallel()

	const ownerSys, targetSys, approverSys, parentOwnerSys = "owner-sys", "target-sys", "approver-sys", "parent-owner-sys"
	// A SECOND and THIRD addressee, and they are the point of this probe.
	const targetSys2, targetSys3 = "target-sys-2", "target-sys-3"

	// One envelope shape for every probe: `from` is the artifact's own
	// author, `to` its addressees. RoleAuthorizes resolves against exactly
	// these, so a row naming a system that is neither reads as unauthorised
	// — which is the failure this gate is for.
	//
	// `to` carries THREE systems, and the single-target probe this replaced
	// could not see the violation it exists to catch. `to` has no `maxItems`
	// on the base schema — only the four exchange types add one — so a
	// requirement, contract, decision or announcement may legally name
	// several. pendency's `target()` returns every entry while
	// fold.roleAuthorizes compared against `To0()` alone, so a live artifact
	// carried `waiting_on` naming three systems and `next_actions` naming
	// one. With one addressee in the probe, the two agreed trivially and the
	// gate stayed green through the exact divergence it was written for.
	env := fold.Envelope{
		From:              ownerSys,
		To:                []string{targetSys, targetSys2, targetSys3},
		RequiredApprovers: []string{approverSys},
	}

	var problems []string
	checked := 0

	for _, p := range fold.SubjectStates() {
		verdict, err := Resolve(Input{
			Kind: p.Kind, State: p.State,
			From: ownerSys, To: []string{targetSys, targetSys2, targetSys3},
			AckRequested:       true,
			RequiredApprovers:  []string{approverSys},
			ActiveParticipants: []string{ownerSys, targetSys, targetSys2, targetSys3},
			ParentFrom:         parentOwnerSys,
		})
		if err != nil || len(verdict.Owners) == 0 {
			continue
		}
		// Owners with no Expected is the legal third answer (Verdict's own
		// doc): somebody owes an act the §3.4 tables cannot name as a
		// transition of this artifact. There is no role to authorise, so
		// there is nothing for this gate to check — and demanding a
		// transition here would push the table back toward naming a move
		// the tool refuses, which is what it exists to prevent.
		if verdict.Expected == "" {
			continue
		}
		if p.Kind == fold.KindAnnouncement {
			// D-025's per-recipient ack has no table row to check against.
			continue
		}
		checked++

		where := string(p.Kind) + "/" + string(p.State)

		var move *fold.NextMove
		for _, mv := range fold.LegalNext(p.Kind, p.State) {
			if mv.Transition == verdict.Expected {
				m := mv
				move = &m
				break
			}
		}
		if move == nil {
			problems = append(problems, where+": says "+verdict.Expected+
				" is owed, but fold.LegalNext admits no such transition from this state")
			continue
		}

		// A response's verify/dispute resolves against the PARENT's envelope,
		// never its own (fold/legality.go's documented response-scoped trap),
		// so the probe envelope has to match what the row is talking about.
		probe := env
		if p.Kind == fold.KindResponse {
			probe.From = parentOwnerSys
		}

		for _, sys := range verdict.Owners {
			if !fold.RoleAuthorizes(move.Role, probe, sys) {
				problems = append(problems, where+": says "+sys+" owes "+verdict.Expected+
					", but that transition's role ("+string(move.Role)+
					") does not authorise "+sys+" — the surface would name a move the tool refuses")
			}
		}
	}

	if checked == 0 {
		t.Fatal("no row with an owed move was checked — the probe inputs or the table are broken, and a green result here would mean nothing")
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d pendency row(s) name a move their owner cannot make (%d row(s) checked):\n  %s",
			len(problems), checked, strings.Join(problems, "\n  "))
	}
}

// TestEveryHumanGatedMoveIsMarkedAsOne closes the hole that let §18e/J3
// through.
//
// The authority gate above asks fold.RoleAuthorizes, which resolves a role
// to a SYSTEM and never looks at actor KIND. G3 is a rule about actor kind:
// 03-domain.md §3.7 requires the approve/reject event to arrive in a PR
// authored by the human owner's own account, and CC-021 says an agent that
// approves a decision is ignored and flagged by the fold. So a row could
// name `approve` as owed, pass the authority gate cleanly, and still be
// telling an AGENT to make a move the tool refuses — which is precisely
// what shipped.
//
// The check is a bare implication and adds no rule of its own: whatever
// fold.HumanGate says about the owed transition must appear on the verdict.
func TestEveryHumanGatedMoveIsMarkedAsOne(t *testing.T) {
	t.Parallel()

	var problems []string
	gated := 0

	for _, p := range fold.SubjectStates() {
		verdict, err := Resolve(Input{
			Kind: p.Kind, State: p.State,
			From: "owner-sys", To: []string{"target-sys"},
			AckRequested:       true,
			RequiredApprovers:  []string{"approver-sys"},
			ActiveParticipants: []string{"owner-sys", "target-sys"},
			ParentFrom:         "parent-owner-sys",
		})
		if err != nil || verdict.Expected == "" {
			continue
		}
		want := fold.HumanGate(verdict.Expected)
		if want != "" {
			gated++
		}
		if verdict.HumanGate != want {
			problems = append(problems, string(p.Kind)+"/"+string(p.State)+
				": owes "+verdict.Expected+", HumanGate="+strconv.Quote(verdict.HumanGate)+
				", want "+strconv.Quote(want))
		}
	}

	if gated == 0 {
		t.Fatal("no owed transition in the whole table is human-gated — either fold.HumanGate lost its rows or the probe stopped reaching decision/proposed, and a green result here would mean nothing")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("%d row(s) disagree with fold.HumanGate (%d gated row(s) found):\n  %s",
			len(problems), gated, strings.Join(problems, "\n  "))
	}
}
