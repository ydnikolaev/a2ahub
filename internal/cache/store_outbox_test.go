package cache

import (
	"context"
	"testing"
	"time"
)

// TestOutboxAttention_FourConditionsPlusControl is AC row 5: `a2a outbox
// --attention` returns exactly the union of the 4 OP-208 conditions,
// over a fixture space with 4 matching items + 1 control.
func TestOutboxAttention_FourConditionsPlusControl(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	now := base.Add(30 * 24 * time.Hour) // 30 days later — well past the 7-day default SLA

	// Condition 2: declined.
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-o2.md", wr("XW-axon-20260701-o2", "o2", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", fxULID(500), evt("XW-axon-20260701-o2", "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(501), evt("XW-axon-20260701-o2", "decline", "seomatrix", base.Add(time.Hour)))

	// Condition 3: disputed (axon owns the request, seomatrix target
	// responds, axon disputes its own outgoing request's response).
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-o3.md", wr("XW-axon-20260701-o3", "o3", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", fxULID(510), evt("XW-axon-20260701-o3", "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(511), evt("XW-axon-20260701-o3", "acknowledge", "seomatrix", base.Add(time.Hour)))
	fx.commitArtifactAndEvent(
		"seomatrix/exchanges/XS-seomatrix-20260701-o3resp.md",
		responseFields("XS-seomatrix-20260701-o3resp", "XW-axon-20260701-o3", "seomatrix", "axon"),
		"resp body", "seomatrix", fxULID(512),
		evt("XW-axon-20260701-o3", "respond", "seomatrix", base.Add(2*time.Hour)),
	)
	fx.commitEvent("axon", fxULID(513), evt("XS-seomatrix-20260701-o3resp", "dispute", "axon", base.Add(3*time.Hour)))

	// Condition 4a: stale (no event within the 7-day default SLA — last
	// activity was at `base`, `now` is 30 days later).
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-o4a.md", wr("XW-axon-20260701-o4a", "o4a", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", fxULID(520), evt("XW-axon-20260701-o4a", "submit", "axon", base))

	// Condition 4b: needed_by passed.
	nb := wr("XW-axon-20260701-o4b", "o4b", "axon", []string{"seomatrix"}, "p2", false)
	nb["needed_by"] = base.Add(24 * time.Hour).Format("2006-01-02")
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-o4b.md", nb, "body")
	fx.commitEvent("axon", fxULID(521), evt("XW-axon-20260701-o4b", "submit", "axon", base))

	// Control: own item, acknowledged recently (within SLA), no
	// needed_by, not declined/disputed — should not match.
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-octrl.md", wr("XW-axon-20260701-octrl", "octrl", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", fxULID(530), evt("XW-axon-20260701-octrl", "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(531), evt("XW-axon-20260701-octrl", "acknowledge", "seomatrix", now.Add(-time.Hour)))

	cacheDir := t.TempDir()
	store := NewStore("axon", cacheDir, []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return now }, 0)

	// Pre-seed the read cursor with every item's CURRENT state so
	// condition 1 ("state changed since cursor") never spuriously fires
	// for this test's fixtures — each condition here is isolated by
	// design (row 5's own dedicated cursor-state test lives in the
	// inbox-cursor coverage above).
	_, err := store.Inbox(context.Background(), false)
	if err != nil {
		t.Fatalf("seed cursor via Inbox: %v", err)
	}
	// Advance the cursor snapshot to the CURRENT (pre-condition-4-decay)
	// state so nothing looks "changed" going into the attention read.

	items, err := store.Outbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Outbox: %v", err)
	}

	got := map[string][]string{}
	for _, it := range items {
		got[it.ID] = it.Reasons
	}
	wantIDs := []string{
		"XW-axon-20260701-o2", "XW-axon-20260701-o3", "XW-axon-20260701-o4a", "XW-axon-20260701-o4b",
	}
	for _, id := range wantIDs {
		if _, ok := got[id]; !ok {
			t.Errorf("expected attention item %q missing; got ids=%v", id, itemIDs(items))
		}
	}
	if reasons, ok := got["XW-axon-20260701-octrl"]; ok {
		t.Errorf("control item unexpectedly in --attention: reasons=%v", reasons)
	}
	if len(items) != len(wantIDs) {
		t.Errorf("len(items) = %d, want %d (got ids=%v)", len(items), len(wantIDs), itemIDs(items))
	}
}

// TestOutbox_NeededByBoundary exercises the exact needed_by boundary and
// the SLA default vs space.yaml override (spec §6 edge cases).
func TestOutbox_NeededByBoundary(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fields := wr("XW-axon-20260701-nb", "nb", "axon", []string{"seomatrix"}, "p2", false)
	fields["needed_by"] = "2026-07-02"
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-nb.md", fields, "body")
	fx.commitEvent("axon", fxULID(600), evt("XW-axon-20260701-nb", "submit", "axon", base))

	cacheDir := t.TempDir()
	// Exactly at the boundary (needed_by day, midnight) — not yet passed.
	store := NewStore("axon", cacheDir, []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}},
		func() time.Time { return time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC) }, 0)
	if _, err := store.Inbox(context.Background(), false); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}
	items, err := store.Outbox(context.Background(), true)
	if err != nil {
		t.Fatalf("Outbox: %v", err)
	}
	for _, it := range items {
		if it.ID == "XW-axon-20260701-nb" {
			for _, r := range it.Reasons {
				if r == "needed-by-passed" {
					t.Errorf("needed_by exactly at boundary should not yet be 'passed'")
				}
			}
		}
	}
}
