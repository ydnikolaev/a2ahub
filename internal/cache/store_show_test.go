package cache

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestShowMany_SpaceQualifiedDetailsAndEnvelope(t *testing.T) {
	t.Parallel()
	const id = "XW-shared-20260731-abcd"
	base := time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC)
	alpha := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	beta := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	alphaFields := wr(id, "alpha title", "seomatrix", []string{"axon"}, "p2", false)
	alphaFields["context"] = "alpha context"
	alpha.commitArtifact("seomatrix/exchanges/"+id+".md", alphaFields, "alpha body")
	alpha.commitEvent("seomatrix", fxULID(720), evt(id, "submit", "seomatrix", base))

	betaFields := wr(id, "beta title", "seomatrix", []string{"axon"}, "p1", true)
	betaFields["context"] = "beta context"
	beta.commitArtifact("seomatrix/exchanges/"+id+".md", betaFields, "beta body")
	beta.commitEvent("seomatrix", fxULID(721), evt(id, "submit", "seomatrix", base.Add(time.Minute)))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{
		{SpaceID: "alpha", Dir: alpha.dir, Manifest: mustManifest(t, alpha)},
		{SpaceID: "beta", Dir: beta.dir, Manifest: mustManifest(t, beta)},
	}, func() time.Time { return base.Add(time.Hour) }, 0)

	results, err := store.ShowMany(context.Background(), []string{"beta:" + id, "alpha:" + id})
	if err != nil {
		t.Fatalf("ShowMany: %v", err)
	}
	if len(results) != 2 || results[0].Space != "beta" || results[0].Body != "beta body" ||
		results[1].Space != "alpha" || results[1].Body != "alpha body" {
		t.Fatalf("space-qualified results lost order or space identity: %+v", results)
	}
	if got := results[0].Envelope["context"]; got != "beta context" {
		t.Fatalf("Envelope[context] = %#v, want beta context", got)
	}
	if len(results[0].Events) != 1 || results[0].Events[0].Transition != "submit" {
		t.Fatalf("canonical event history missing: %+v", results[0].Events)
	}

	encoded, err := json.Marshal(results[0])
	if err != nil {
		t.Fatalf("marshal ShowResult: %v", err)
	}
	if strings.Contains(string(encoded), "beta context") || strings.Contains(string(encoded), `"Envelope"`) {
		t.Fatalf("dashboard-only envelope leaked into stable show JSON: %s", encoded)
	}
	if _, err := store.Show(context.Background(), "missing:"+id); err == nil {
		t.Fatal("space-qualified Show must not fall through to another space")
	}
}

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
