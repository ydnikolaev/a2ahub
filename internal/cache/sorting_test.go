package cache

import (
	"context"
	"testing"
	"time"
)

func TestItemActivityKeyFollowsCommittedLifecycleOrder(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	const id = "XW-axon-20260802-order"
	fields := wr(id, "activity ordering", "axon", []string{"seomatrix"}, "p2", false)
	fields["created"] = fxAt(base)
	fx.commitArtifact("axon/exchanges/"+id+".md", fields, "body")
	fx.commitEvent("axon", fxULID(950), evt(id, "submit", "axon", base.Add(2*time.Hour)))
	// The repository says this acknowledge happened later even though its
	// author clock moved backwards. Activity ordering must keep all three keys
	// (commit sequence, displayed time and event identity) on that same event.
	fx.commitEvent("seomatrix", fxULID(951), evt(id, "acknowledge", "seomatrix", base.Add(time.Hour)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(3 * time.Hour) }, 0)
	items, err := store.Search(context.Background(), id, SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if got, want := items[0].LatestEventID, fxULID(951); got != want {
		t.Fatalf("LatestEventID = %q, want committed-later event %q", got, want)
	}
	if got, want := items[0].LatestEventAt, base.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("LatestEventAt = %s, want same event's time %s", got, want)
	}
	if items[0].LatestEventSeq == 0 {
		t.Fatal("LatestEventSeq = 0, want committed order key")
	}
}

func TestItemCreationKeyIgnoresLateLifecycleActivity(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)

	const olderID = "XA-axon-20260802-older"
	older := wr(olderID, "late lifecycle older", "axon", []string{"seomatrix"}, "p3", false)
	older["type"] = "announcement"
	older["created"] = fxAt(base)
	fx.commitArtifact("axon/exchanges/"+olderID+".md", older, "older body")
	fx.commitEvent("axon", fxULID(952), evt(olderID, "publish", "axon", base))

	const newerID = "XQ-axon-20260802-newer"
	newer := wr(newerID, "late lifecycle newer", "axon", []string{"seomatrix"}, "p3", false)
	newer["type"] = "question"
	newer["created"] = fxAt(base.Add(time.Minute))
	fx.commitArtifact("axon/exchanges/"+newerID+".md", newer, "newer body")
	fx.commitEvent("axon", fxULID(953), evt(newerID, "submit", "axon", base.Add(time.Minute)))

	// A receipt arriving later is activity on the old announcement, not a new
	// document. It must advance LatestEvent*, but never its immutable creation
	// key — otherwise Exchange moves old cards above documents born later.
	fx.commitEvent("seomatrix", fxULID(954), evt(olderID, "acknowledge", "seomatrix", base.Add(2*time.Hour)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(3 * time.Hour) }, 0)
	items, err := store.Search(context.Background(), "late lifecycle", SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	byID := map[string]Item{}
	for _, item := range items {
		byID[item.ID] = item
	}
	olderItem, olderOK := byID[olderID]
	newerItem, newerOK := byID[newerID]
	if !olderOK || !newerOK {
		t.Fatalf("search ids = %v, want %s and %s", itemIDs(items), olderID, newerID)
	}
	if !olderItem.CreatedOrderKnown || !newerItem.CreatedOrderKnown {
		t.Fatalf("creation order not known: older=%v newer=%v", olderItem.CreatedOrderKnown, newerItem.CreatedOrderKnown)
	}
	if olderItem.CreatedSeq >= newerItem.CreatedSeq {
		t.Fatalf("creation sequence older=%d newer=%d, want older < newer", olderItem.CreatedSeq, newerItem.CreatedSeq)
	}
	if !olderItem.CreatedAt.Equal(base) || !newerItem.CreatedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("creation times older=%s newer=%s", olderItem.CreatedAt, newerItem.CreatedAt)
	}
	if olderItem.LatestEventSeq <= newerItem.LatestEventSeq {
		t.Fatalf("fixture did not reproduce late activity: older=%d newer=%d", olderItem.LatestEventSeq, newerItem.LatestEventSeq)
	}
}

func TestShowEventOrderBreaksTimestampTieByULID(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	const id = "XW-axon-20260802-show"
	fields := wr(id, "show ordering", "axon", []string{"seomatrix"}, "p2", false)
	fields["created"] = fxAt(base)
	fx.commitArtifact("axon/exchanges/"+id+".md", fields, "body")
	fx.commitEvent("axon", fxULID(960), evt(id, "submit", "axon", base))
	fx.commitEvent("seomatrix", fxULID(961), evt(id, "acknowledge", "seomatrix", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)
	result, err := store.Show(context.Background(), id)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(result.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(result.Events))
	}
	if result.Events[0].ULID != fxULID(960) || result.Events[1].ULID != fxULID(961) {
		t.Fatalf("equal-time events not in ULID order: %q, %q", result.Events[0].ULID, result.Events[1].ULID)
	}
}

func TestThreadUsesCommittedArtifactOrderNotLatestActivity(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	const threadID = "thread:axon-20260802-order"

	parent := wr("XW-axon-20260802-parent", "parent", "axon", []string{"seomatrix"}, "p2", false)
	parent["thread"] = threadID
	parent["created"] = fxAt(base)
	fx.commitArtifact("axon/exchanges/XW-axon-20260802-parent.md", parent, "body")
	fx.commitEvent("axon", fxULID(970), evt("XW-axon-20260802-parent", "submit", "axon", base.Add(2*time.Hour)))

	child := wr("XQ-seomatrix-20260802-child", "child", "seomatrix", []string{"axon"}, "p2", false)
	child["type"] = "question"
	child["thread"] = threadID
	child["created"] = fxAt(base.Add(time.Hour))
	fx.commitArtifact("seomatrix/exchanges/XQ-seomatrix-20260802-child.md", child, "body")
	fx.commitEvent("seomatrix", fxULID(971), evt("XQ-seomatrix-20260802-child", "submit", "seomatrix", base.Add(time.Hour)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(3 * time.Hour) }, 0)
	items, err := store.Thread(context.Background(), threadID)
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(items) != 2 || items[0].ID != "XW-axon-20260802-parent" || items[1].ID != "XQ-seomatrix-20260802-child" {
		t.Fatalf("thread creation order = %v, want parent then child", itemIDs(items))
	}
}
