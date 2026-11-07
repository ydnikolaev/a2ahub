package cache

import (
	"context"
	"testing"
	"time"
)

// fb-20261106-96837b: the owed party wrote notes on an item for a week and
// never made the transition it owed. A note is transition-free, so the fold
// was right to leave the state where it was — but nothing told either side,
// and every note reset the staleness clock besides. notes-without-move is
// that signal: the parties the verdict names as owing the move have written
// notesWithoutMoveThreshold or more notes since the last state transition.
func TestNotesWithoutMoveFlagsTheOwedPartysNotes(t *testing.T) {
	t.Parallel()

	const id = "XW-seomatrix-20261101-notes"
	note := func(actor string, at time.Time) map[string]any {
		fields := evt(id, "note", actor, at)
		fields["note"] = "still looking at it"
		return fields
	}
	base := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name string
		// seed commits the events after the work request's own submit.
		seed func(fx *fixtureSpace)
		want bool
	}{
		{
			name: "three notes by the owed party since the transition",
			seed: func(fx *fixtureSpace) {
				fx.commitEvent("axon", fxULID(902), note("axon", base.Add(1*time.Hour)))
				fx.commitEvent("axon", fxULID(903), note("axon", base.Add(2*time.Hour)))
				fx.commitEvent("axon", fxULID(904), note("axon", base.Add(3*time.Hour)))
			},
			want: true,
		},
		{
			name: "two notes stay under the threshold",
			seed: func(fx *fixtureSpace) {
				fx.commitEvent("axon", fxULID(902), note("axon", base.Add(1*time.Hour)))
				fx.commitEvent("axon", fxULID(903), note("axon", base.Add(2*time.Hour)))
			},
			want: false,
		},
		{
			name: "notes by the party that is waiting do not count",
			seed: func(fx *fixtureSpace) {
				fx.commitEvent("seomatrix", fxULID(902), note("seomatrix", base.Add(1*time.Hour)))
				fx.commitEvent("seomatrix", fxULID(903), note("seomatrix", base.Add(2*time.Hour)))
				fx.commitEvent("seomatrix", fxULID(904), note("seomatrix", base.Add(3*time.Hour)))
			},
			want: false,
		},
		{
			name: "a transition resets the count: only notes after it are counted",
			seed: func(fx *fixtureSpace) {
				fx.commitEvent("axon", fxULID(902), note("axon", base.Add(1*time.Hour)))
				fx.commitEvent("axon", fxULID(903), note("axon", base.Add(2*time.Hour)))
				fx.commitEvent("axon", fxULID(904), note("axon", base.Add(3*time.Hour)))
				fx.commitEvent("axon", fxULID(905), evt(id, "acknowledge", "axon", base.Add(4*time.Hour)))
				fx.commitEvent("axon", fxULID(906), note("axon", base.Add(5*time.Hour)))
				fx.commitEvent("axon", fxULID(907), note("axon", base.Add(6*time.Hour)))
			},
			want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
			fx.commitArtifact("seomatrix/exchanges/"+id+".md", wr(id, "notes only", "seomatrix", []string{"axon"}, "p2", false), "body")
			fx.commitEvent("seomatrix", fxULID(901), evt(id, "submit", "seomatrix", base))
			tc.seed(fx)
			manifest := mustManifest(t, fx)
			now := func() time.Time { return base.Add(7 * time.Hour) }

			// Both sides of the exchange see it: the owed party (it is their
			// move) and the counterparty waiting on them — the same party
			// test the p1-or-blocking condition uses.
			for _, me := range []string{"axon", "seomatrix"} {
				store := NewStore(me, t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: manifest}}, now, time.Hour)
				actionable, err := store.Inbox(context.Background(), true)
				if err != nil {
					t.Fatalf("%s: Inbox(actionable): %v", me, err)
				}
				got := false
				for _, it := range actionable {
					if it.ID == id && HasReason(it.Reasons, ReasonNotesWithoutMove) {
						got = true
					}
				}
				if got != tc.want {
					t.Errorf("%s: notes-without-move = %v, want %v (actionable %+v)", me, got, tc.want, actionable)
				}
			}
		})
	}
}

// An outsider — neither a party nor the owed system — never sees the reason,
// and a party sees it without it lifting the statusline to urgent: §7.5's
// urgency is p1, blocking or a pending gate, and widening it is a protocol
// change (SeverityOf's own comment).
func TestNotesWithoutMoveAudienceAndSeverity(t *testing.T) {
	t.Parallel()

	const id = "XW-seomatrix-20261101-notes2"
	base := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"}, fixtureParticipant{System: "gamma"})
	fx.commitArtifact("seomatrix/exchanges/"+id+".md", wr(id, "notes only", "seomatrix", []string{"axon"}, "p2", false), "body")
	fx.commitEvent("seomatrix", fxULID(911), evt(id, "submit", "seomatrix", base))
	fx.commitEvent("axon", fxULID(912), evt(id, "acknowledge", "axon", base.Add(time.Hour)))
	for i := 0; i < notesWithoutMoveThreshold; i++ {
		fields := evt(id, "note", "axon", base.Add(time.Duration(2+i)*time.Hour))
		fields["note"] = "progress"
		fx.commitEvent("axon", fxULID(913+i), fields)
	}
	manifest := mustManifest(t, fx)
	now := func() time.Time { return base.Add(10 * time.Hour) }

	outsider := NewStore("gamma", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: manifest}}, now, time.Hour)
	items, err := outsider.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("outsider Inbox: %v", err)
	}
	if containsItemID(items, id) {
		t.Fatalf("an outsider sees %s on --actionable: %+v", id, items)
	}

	owed := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: manifest}}, now, time.Hour)
	items, err = owed.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("owed Inbox: %v", err)
	}
	var found *Item
	for i := range items {
		if items[i].ID == id {
			found = &items[i]
		}
	}
	if found == nil || !HasReason(found.Reasons, ReasonNotesWithoutMove) {
		t.Fatalf("owed party does not see notes-without-move on %s: %+v", id, items)
	}
	result, err := owed.Statusline(context.Background())
	if err != nil {
		t.Fatalf("Statusline: %v", err)
	}
	if result.Urgent != 0 || result.Exit != int(SeverityItemsPending) {
		t.Fatalf("statusline = %+v, want items pending (exit %d) and no urgent item", result, SeverityItemsPending)
	}
}
