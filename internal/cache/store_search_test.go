package cache

import (
	"context"
	"testing"
	"time"
)

func TestThread_OrderedConversationView(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	t1 := wr("XW-axon-20260701-t1", "thread item 1", "axon", []string{"seomatrix"}, "p2", false)
	t1["thread"] = "TH-axon-thread1"
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-t1.md", t1, "body")
	fx.commitEvent("axon", fxULID(900), evt("XW-axon-20260701-t1", "submit", "axon", base))

	t2 := wr("XQ-seomatrix-20260701-t2", "thread item 2", "seomatrix", []string{"axon"}, "p2", false)
	t2["type"] = "question"
	t2["thread"] = "TH-axon-thread1"
	fx.commitArtifact("seomatrix/exchanges/XQ-seomatrix-20260701-t2.md", t2, "body")
	fx.commitEvent("seomatrix", fxULID(901), evt("XQ-seomatrix-20260701-t2", "submit", "seomatrix", base.Add(time.Hour)))

	// Unrelated, different thread.
	other := wr("XW-axon-20260701-other", "other", "axon", []string{"seomatrix"}, "p2", false)
	other["thread"] = "TH-axon-different"
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-other.md", other, "body")
	fx.commitEvent("axon", fxULID(902), evt("XW-axon-20260701-other", "submit", "axon", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(2 * time.Hour) }, 0)
	items, err := store.Thread(context.Background(), "TH-axon-thread1")
	if err != nil {
		t.Fatalf("Thread: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (got %v)", len(items), itemIDs(items))
	}
	if items[0].ID != "XW-axon-20260701-t1" || items[1].ID != "XQ-seomatrix-20260701-t2" {
		t.Fatalf("thread not in chronological order: %v", itemIDs(items))
	}
}

func TestSearch_ZeroHitsIsEmptyNotError(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, 0)
	items, err := store.Search(context.Background(), "no-such-term-anywhere", SearchFilters{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", items)
	}
}

func TestSearch_MatchesTitleAndFilters(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-s1.md", wr("XW-axon-20260701-s1", "todo feed pagination", "axon", []string{"seomatrix"}, "p2", false), "body")
	fx.commitEvent("axon", fxULID(910), evt("XW-axon-20260701-s1", "submit", "axon", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)
	items, err := store.Search(context.Background(), "pagination", SearchFilters{Type: "work_request"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(items) != 1 || items[0].ID != "XW-axon-20260701-s1" {
		t.Fatalf("got %v", itemIDs(items))
	}

	none, err := store.Search(context.Background(), "pagination", SearchFilters{Type: "question"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("type filter should exclude: got %v", itemIDs(none))
	}
}

func TestContracts_ProviderFilterAndVersion(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/provides/ingest/contract.md", map[string]any{
		"schema": "envelope/v1", "id": "XC-axon-ingest", "type": "contract", "title": "ingest",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "contract body")
	ev := evt("XC-axon-ingest", "publish", "axon", base)
	ev["version"] = "1.0.0"
	fx.commitEvent("axon", fxULID(920), ev)

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)
	all, err := store.Contracts(context.Background(), "")
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	if len(all) != 1 || all[0].Version != "1.0.0" || all[0].Provider != "axon" {
		t.Fatalf("got %+v", all)
	}

	none, err := store.Contracts(context.Background(), "seomatrix")
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("provider filter should exclude: got %+v", none)
	}
}
