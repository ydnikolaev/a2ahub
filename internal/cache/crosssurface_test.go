package cache

import (
	"context"
	"testing"
	"time"
)

// multiTargetRequirement builds a requirement addressed to THREE systems.
//
// The shape is the point. `to` carries `minItems: 1` and no `maxItems` on the
// base schema — only the four exchange types add one — so a requirement,
// contract, decision or announcement may legally name several addressees. A
// single-target fixture cannot exhibit the divergence this file exists to
// pin, and P2's plan says so in as many words: today's fixture would certify
// the bug.
func multiTargetRequirement(id, from string, to []string) map[string]any {
	return map[string]any{
		"schema": "envelope/v1", "id": id, "type": "requirement",
		"title": "three systems must acknowledge this", "space": "fixture-space",
		"from": from, "to": to,
		"actor":          map[string]any{"kind": "agent", "name": "bot"},
		"created":        "2026-08-01T08:00:00Z",
		"priority":       "p2",
		"blocking":       false,
		"classification": "internal",
		"thread":         id,
	}
}

// TestOneAnswerAcrossSurfacesOnAMultiTargetArtifact is P2's AC1 at the level
// the surfaces are actually built.
//
// Every surface reads the SAME pendency verdict, and until this phase two of
// them threw it away: `actionableReasons` derived it, returned four strings,
// and dropped the rest at the door — so `a2a inbox --json` showed a conclusion
// with no reasoning while `a2a thread --json` showed both, for one artifact.
//
// The artifact is addressed to three systems. Each of them must see the same
// answer, and each must be told the move is theirs, because P1 made every
// named target authorized rather than only the first.
func TestOneAnswerAcrossSurfacesOnAMultiTargetArtifact(t *testing.T) {
	t.Parallel()

	const id = "XR-axon-20260801-multi"
	targets := []string{"betaco", "gammaco", "seomatrix"}

	fx := newFixtureSpace(t,
		fixtureParticipant{System: "axon"},
		fixtureParticipant{System: "betaco"},
		fixtureParticipant{System: "gammaco"},
		fixtureParticipant{System: "seomatrix"},
	)
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifactAndEvent(
		"axon/requirements/"+id+".md",
		multiTargetRequirement(id, "axon", targets),
		"Three systems are addressed and all three owe an acknowledgement.",
		"axon", fxULID(300), evt(id, "publish", "axon", base),
	)
	manifest := mustManifest(t, fx)

	for _, self := range targets {
		t.Run(self, func(t *testing.T) {
			t.Parallel()
			store := NewStore(self, t.TempDir(),
				[]SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: manifest}},
				func() time.Time { return base.Add(24 * time.Hour) }, 0)

			items, err := store.InboxSnapshot(context.Background(), false)
			if err != nil {
				t.Fatalf("InboxSnapshot: %v", err)
			}
			var item *Item
			for i := range items {
				if items[i].ID == id {
					item = &items[i]
					break
				}
			}
			if item == nil {
				t.Fatalf("%s does not see %s in its inbox at all — it is named in `to`", self, id)
			}

			// The verdict reaches the inbox surface. Before P2 these three
			// were computed and discarded, so this is the assertion that
			// "four surfaces, one answer" is about.
			if len(item.WaitingOn) == 0 {
				t.Error("inbox item carries no waiting_on — the verdict was computed and thrown away")
			}
			if item.ExpectedTransition == "" {
				t.Error("inbox item carries no expected_transition")
			}
			if item.Why == "" {
				t.Error("inbox item carries no why — pendency's contract is that even \"nobody owes anything\" is a claim it must justify")
			}

			// Every addressee is named, not just the first. This is the
			// half P1 fixed in the domain; here it is asserted through the
			// surface a reader actually looks at.
			for _, target := range targets {
				if !containsString(item.WaitingOn, target) {
					t.Errorf("waiting_on = %v, missing %q — pendency names every addressee and the fold now authorizes each of them", item.WaitingOn, target)
				}
			}

			// And the thread view — a different construction path, same
			// artifact, same relation — must agree.
			view, err := store.ThreadView(context.Background(), id, "")
			if err != nil {
				t.Fatalf("ThreadView: %v", err)
			}
			var open *OpenItem
			for i := range view.OpenItems {
				if view.OpenItems[i].ID == id {
					open = &view.OpenItems[i]
					break
				}
			}
			if open == nil {
				t.Fatalf("%s is open on the inbox and absent from the thread view's open items — the two surfaces disagree about whether anything is owed", self)
			}
			if open.ExpectedTransition != item.ExpectedTransition {
				t.Errorf("thread says %q is expected, inbox says %q — one artifact, two answers",
					open.ExpectedTransition, item.ExpectedTransition)
			}
			if open.Why != item.Why {
				t.Errorf("thread and inbox justify the same verdict differently:\n  thread: %s\n  inbox:  %s", open.Why, item.Why)
			}
			if len(open.WaitingOn) != len(item.WaitingOn) {
				t.Errorf("thread waiting_on = %v, inbox waiting_on = %v", open.WaitingOn, item.WaitingOn)
			}

			// The assertion that would have caught the original defect, and
			// which surface-to-surface agreement alone cannot: everyone the
			// relation NAMES must appear among the systems the fold admits
			// for the expected move.
			//
			// The live divergence was one object carrying
			// `waiting_on: [betaco, gammaco, seomatrix]` beside
			// `next_actions: [{transition: acknowledge, by: [seomatrix]}]`.
			// Both surfaces reported it identically, so they AGREED — and
			// both were wrong. Agreement is not correctness, which is the
			// first thing P2's plan says about itself.
			admitted := map[string]bool{}
			for _, action := range open.NextActions {
				if action.Transition != open.ExpectedTransition {
					continue
				}
				for _, by := range action.By {
					admitted[by] = true
				}
			}
			for _, owed := range open.WaitingOn {
				if !admitted[owed] {
					t.Errorf("waiting_on names %q for %q, but next_actions admits only %v — the surface tells a system to make a move the fold would refuse",
						owed, open.ExpectedTransition, admitted)
				}
			}
		})
	}
}
