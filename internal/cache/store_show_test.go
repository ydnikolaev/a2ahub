package cache

import (
	"context"
	"testing"
	"time"
)

// TestShow_DigestMismatchWarningNonBlocking is AC row 6: `a2a show`
// surfaces a V5 digest-mismatch (or staleness) warning as a non-blocking
// flag — Show succeeds (no error), and the RefFact carries the fact
// cmd_show.go maps to the V5 registry code.
func TestShow_DigestMismatchWarningNonBlocking(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	// Target: a published requirement, digest known.
	fx.commitArtifact("axon/requires/XR-axon-target.md", map[string]any{
		"schema": "envelope/v1", "id": "XR-axon-target", "type": "requirement", "title": "target req",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "target body")
	fx.commitEvent("axon", fxULID(700), evt("XR-axon-target", "publish", "axon", base))

	// The referring artifact pins a WRONG digest for the target.
	referring := wr("XW-axon-20260701-ref", "referring", "axon", []string{"seomatrix"}, "p2", false)
	referring["refs"] = []map[string]any{{"ref": "XR-axon-target#sha256:deadbeef"}}
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-ref.md", referring, "body")
	fx.commitEvent("axon", fxULID(701), evt("XW-axon-20260701-ref", "submit", "axon", base.Add(time.Hour)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(2 * time.Hour) }, 0)
	result, err := store.Show(context.Background(), "XW-axon-20260701-ref")
	if err != nil {
		t.Fatalf("Show: %v (must succeed — a V5 warning is never a hard error)", err)
	}
	if len(result.Refs) != 1 {
		t.Fatalf("len(Refs) = %d, want 1", len(result.Refs))
	}
	rf := result.Refs[0]
	if !rf.Resolved {
		t.Fatalf("ref target should resolve: %+v", rf)
	}
	if !rf.DigestMismatch {
		t.Fatalf("expected DigestMismatch=true: %+v", rf)
	}
}

// TestShow_RefNotFound is OP-209's own "ref not found -> clear error, no
// crash" requirement.
func TestShow_RefNotFound(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, time.Now, 0)
	_, err := store.Show(context.Background(), "XW-axon-20260701-nope")
	if err == nil {
		t.Fatal("Show: want error for unknown ref, got nil")
	}
}

// TestShow_StaleUnpinnedRef exercises the "stale pinned ref" half of AC
// row 6 (unresolvable pinned ref — a distinct fact from digest
// mismatch).
func TestShow_UnresolvableRef(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	referring := wr("XW-axon-20260701-ref2", "referring2", "axon", []string{"seomatrix"}, "p2", false)
	referring["refs"] = []map[string]any{{"ref": "XR-axon-nonexistent#sha256:deadbeef"}}
	fx.commitArtifact("axon/exchanges/XW-axon-20260701-ref2.md", referring, "body")
	fx.commitEvent("axon", fxULID(710), evt("XW-axon-20260701-ref2", "submit", "axon", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)
	result, err := store.Show(context.Background(), "XW-axon-20260701-ref2")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(result.Refs) != 1 || result.Refs[0].Resolved {
		t.Fatalf("expected an unresolved ref fact, got %+v", result.Refs)
	}
}
