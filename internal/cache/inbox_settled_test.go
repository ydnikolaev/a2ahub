package cache

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// fb-20261106-3312bc: a published contract whose fold says outcome settled,
// waiting_on empty and no expected transition was still listed by
// `a2a inbox --actionable` and counted urgent by `a2a statusline`. The fold
// was right; the p1-or-blocking condition read liveness (every published
// contract is open) and never the outcome. Both directions are pinned here:
// the settled p1 contract drops out, and the open p1 contract — same author,
// same priority, one registered consumer away — stays actionable and urgent.
func TestSettledP1ContractIsNeitherActionableNorUrgent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		registered     bool
		wantActionable bool
	}{
		{name: "settled: no registered consumer, nobody owes a move", registered: false, wantActionable: false},
		{name: "open: a registered consumer makes activation owed", registered: true, wantActionable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
			odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
			base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
			contractID := "XC-axon-widget"

			fields := odContractFields(contractID, "axon", nil, base)
			fields["priority"] = "p1"
			fx.commitArtifact("axon/provides/widget/contract.md", fields, "contract body")
			fx.commitEvent("axon", fxULID(1), map[string]any{
				"subject": contractID, "transition": "publish", "version": "1.0.0",
				"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
				"at":    fxAt(base.Add(time.Hour)),
			})
			if tc.registered {
				rcWriteConsumesYAML(t, fx.dir, "beta", contractID, 1)
			}

			store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
				func() time.Time { return base.Add(2 * time.Hour) }, time.Hour)

			actionable, err := store.Inbox(context.Background(), true)
			if err != nil {
				t.Fatalf("Inbox(actionable): %v", err)
			}
			if got := containsItemID(actionable, contractID); got != tc.wantActionable {
				t.Fatalf("actionable lists %s = %v, want %v (items %v)", contractID, got, tc.wantActionable, itemIDs(actionable))
			}

			result, err := store.Statusline(context.Background())
			if err != nil {
				t.Fatalf("Statusline: %v", err)
			}
			wantUrgent := 0
			if tc.wantActionable {
				wantUrgent = 1
			}
			if result.Urgent != wantUrgent {
				t.Fatalf("statusline urgent = %d, want %d (%+v)", result.Urgent, wantUrgent, result)
			}

			outbox, err := store.Outbox(context.Background(), false)
			if err != nil {
				t.Fatalf("Outbox: %v", err)
			}
			wantSeverity := SeverityUrgent
			if !tc.wantActionable {
				// The settled contract is still the author's own open item,
				// so outbox lists it — but an item nobody can move is not
				// urgent on any surface's exit code either.
				wantSeverity = SeverityItemsPending
			}
			if got := SeverityOf(outbox); got != wantSeverity {
				t.Fatalf("SeverityOf(outbox) = %d, want %d (items %+v)", got, wantSeverity, outbox)
			}
		})
	}
}

// The p1-or-blocking condition exists to surface an urgent item that is the
// OTHER side's move. Excluding settled items must not take that away: a p1
// work request I sent, waiting on the recipient's acknowledge, stays
// actionable and urgent for me.
func TestOpenP1ItemAwaitingTheOtherSideStaysActionableAndUrgent(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	id := "XW-axon-20260701-urgent"
	fx.commitArtifact("axon/exchanges/"+id+".md", wr(id, "urgent ask", "axon", []string{"seomatrix"}, "p1", false), "body")
	fx.commitEvent("axon", fxULID(820), evt(id, "submit", "axon", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		func() time.Time { return base.Add(time.Hour) }, time.Hour)

	actionable, err := store.Inbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Inbox(actionable): %v", err)
	}
	var found *Item
	for i := range actionable {
		if actionable[i].ID == id {
			found = &actionable[i]
		}
	}
	if found == nil {
		t.Fatalf("open p1 item awaiting the other side dropped out of --actionable: %v", itemIDs(actionable))
	}
	if !HasReason(found.Reasons, ReasonP1OrBlockingOpen) {
		t.Fatalf("reasons = %v, want %s", found.Reasons, ReasonP1OrBlockingOpen)
	}
	if found.Outcome != fold.OutcomeOpen {
		t.Fatalf("outcome = %q, want open", found.Outcome)
	}

	result, err := store.Statusline(context.Background())
	if err != nil {
		t.Fatalf("Statusline: %v", err)
	}
	if result.Urgent != 1 || result.Exit != int(SeverityUrgent) {
		t.Fatalf("statusline = %+v, want one urgent item and exit %d", result, SeverityUrgent)
	}
}

// SeverityOf grades a settled item as never urgent, whatever its priority
// says: an item that waits on nobody cannot be anybody's urgent move.
func TestSeverityOfIgnoresSettledPriority(t *testing.T) {
	t.Parallel()
	settled := Item{ID: "c", Priority: "p1", Blocking: true, Outcome: fold.OutcomeSettled}
	if got := SeverityOf([]Item{settled}); got != SeverityItemsPending {
		t.Fatalf("SeverityOf(settled p1) = %d, want %d", got, SeverityItemsPending)
	}
	open := Item{ID: "o", Priority: "p1", Outcome: fold.OutcomeOpen}
	if got := SeverityOf([]Item{settled, open}); got != SeverityUrgent {
		t.Fatalf("SeverityOf(settled + open p1) = %d, want %d", got, SeverityUrgent)
	}
}

// The notification snapshot qualifies an owned item through stale-sla as
// well as through the actionable reasons, so a settled p1 contract left
// untouched past the SLA still reached it — and was counted urgent there
// after the inbox and statusline had stopped doing so (fb-20261106-3312bc).
func TestNotificationSnapshotDoesNotCountSettledStaleItemUrgent(t *testing.T) {
	t.Parallel()

	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-widget"

	fields := odContractFields(contractID, "axon", nil, base)
	fields["priority"] = "p1"
	fx.commitArtifact("axon/provides/widget/contract.md", fields, "contract body")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})

	later := base.Add(DefaultStalenessSLA + 30*24*time.Hour)
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		func() time.Time { return later }, time.Hour)

	snap, err := store.NotificationSnapshot(context.Background())
	if err != nil {
		t.Fatalf("NotificationSnapshot: %v", err)
	}
	if !containsItemID(snap.Items, contractID) {
		t.Fatalf("snapshot items %v: want %s qualified through stale-sla, or this test proves nothing", itemIDs(snap.Items), contractID)
	}
	for _, it := range snap.Items {
		if it.ID == contractID && it.Outcome != fold.OutcomeSettled {
			t.Fatalf("%s outcome = %q, want settled", contractID, it.Outcome)
		}
	}
	if snap.Level.Urgent != 0 {
		t.Fatalf("snapshot urgent = %d, want 0 for a settled item (%+v)", snap.Level.Urgent, snap.Level)
	}
}
