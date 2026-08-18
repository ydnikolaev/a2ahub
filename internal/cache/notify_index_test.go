package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// notify_index_test.go is space-notify-2026-08 P3's own test for the ONE
// exported entry point this phase adds to internal/cache (spec 03 §5 /
// plan's "design decision"): BuildNotifyIndex must fold once, resolve the
// COMPLETE addressee set (AC11b), and carry the same verdict/actions a
// per-participant reader (buildOpenItems/resolveVerdict) would produce.

func TestBuildNotifyIndex_WorksOnABareCheckout(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	fx.commitArtifact("axon/exchanges/XW-axon-20260701-aaaa.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260701-aaaa", "type": "work_request",
		"title": "todo feed pagination", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(time.Now()), "priority": "p1", "blocking": false, "classification": "internal",
	}, "please paginate the feed before Friday")

	// No .a2a/ anywhere under fx.dir — AC1's own bare-checkout condition.
	idx, skips, err := BuildNotifyIndex(context.Background(), "sp1", fx.dir, mustManifest(t, fx), time.Now())
	if err != nil {
		t.Fatalf("BuildNotifyIndex: %v", err)
	}
	if len(skips) != 0 {
		t.Fatalf("unexpected skips: %+v", skips)
	}
	if len(idx) != 1 {
		t.Fatalf("len(idx) = %d, want 1", len(idx))
	}
	fa := idx[0]
	if fa.ID != "XW-axon-20260701-aaaa" || fa.Kind != string(fold.KindWorkRequest) {
		t.Fatalf("unexpected artifact: %+v", fa)
	}
	if fa.State != string(fold.StateSubmitted) {
		t.Fatalf("state = %q, want submitted", fa.State)
	}
	if string(fa.Body) == "" {
		t.Fatalf("Body is empty, want the artifact's own markdown body")
	}
	if fa.Verdict.Why == "" {
		t.Fatalf("Verdict.Why must always be populated (pendency's own contract)")
	}
	if len(fa.Actions) == 0 {
		t.Fatalf("Actions is empty, want at least the legal `acknowledge`")
	}
}

// TestBuildNotifyIndex_CompleteAddresseeSet is AC11b: a late adopter whose
// consumes.yaml is the ONLY evidence (not named in the deprecation
// announcement's own frozen `to:`) must still be handed to pendency as an
// ExtraAddressee — the complete-registry read this function's own doc
// comment claims over myDependencyContracts' single-system narrowness.
func TestBuildNotifyIndex_CompleteAddresseeSet(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"}, fixtureParticipant{System: "latecomer"})

	// latecomer depends on the contract axon is about to deprecate, but the
	// announcement's own `to:` (frozen at author time) never named it.
	fxMkdirAll(t, filepath.Join(fx.dir, "latecomer"))
	consumesYAML := "schema: consumes/v1\nsystem: latecomer\ndependencies:\n  - contract: XC-axon-widget\n    major: 1\n    since: \"2026-08-01\"\n"
	if err := os.WriteFile(filepath.Join(fx.dir, "latecomer", "consumes.yaml"), []byte(consumesYAML), 0o644); err != nil {
		t.Fatalf("write consumes.yaml: %v", err)
	}
	fxCommitAndPush(t, fx.dir, "fixture: latecomer consumes.yaml")

	fx.commitArtifact("axon/exchanges/XA-axon-20260701-dddd.md", map[string]any{
		"schema": "envelope/v2", "id": "XA-axon-20260701-dddd", "type": "announcement",
		"title": "widget sunset", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "deprecates": "XC-axon-widget@1.0.0",
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(time.Now()), "ack_requested": true, "priority": "p3", "blocking": false,
		"classification": "internal",
	}, "widget v1 is sunsetting")

	idx, _, err := BuildNotifyIndex(context.Background(), "sp1", fx.dir, mustManifest(t, fx), time.Now())
	if err != nil {
		t.Fatalf("BuildNotifyIndex: %v", err)
	}
	fa := findNotifyArtifact(t, idx, "XA-axon-20260701-dddd")

	if !containsString(fa.Addressees, "latecomer") {
		t.Fatalf("Addressees = %v, want it to include the late adopter latecomer", fa.Addressees)
	}
	if !containsString(fa.Verdict.Owners, "latecomer") {
		t.Fatalf("Verdict.Owners = %v, want it to name the late adopter latecomer (AC11b)", fa.Verdict.Owners)
	}
	if !containsString(fa.Verdict.Owners, "seomatrix") {
		t.Fatalf("Verdict.Owners = %v, want it to STILL name seomatrix (union, not replacement)", fa.Verdict.Owners)
	}
	if fa.Verdict.Expected != "acknowledge" {
		t.Fatalf("Verdict.Expected = %q, want acknowledge (never the empty-owners fallback's own Why)", fa.Verdict.Expected)
	}
	if fa.Verdict.Why == "" || fa.Verdict.Why == "domain 3.4.7 — delivery completes on publish, no acknowledgement is required, and no registry-matched consumer is owed one either" {
		t.Fatalf("Verdict.Why = %q, want the POPULATED row's rationale, not the settled/nobody-owes text", fa.Verdict.Why)
	}
}

// TestBuildNotifyIndex_FoldsOnce is AC10's own package-level half — a grep
// assertion that this function calls buildIndex exactly once, never a
// second artifact/event walk of its own.
func TestBuildNotifyIndex_FoldsOnce(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("mirror.go")
	if err != nil {
		t.Fatalf("read mirror.go: %v", err)
	}
	count := strings.Count(string(raw), "buildIndex(ctx")
	// buildIndex's own func declaration ("func buildIndex(ctx ...") plus
	// exactly one call site inside BuildNotifyIndex — never a second call,
	// and never a second filepath.WalkDir anywhere in this file for
	// artifacts/events.
	if count != 2 {
		t.Fatalf("found %d occurrences of `buildIndex(ctx` in mirror.go, want exactly 2 (the func decl + BuildNotifyIndex's one call)", count)
	}
}

func findNotifyArtifact(t *testing.T, idx []NotifyArtifact, id string) NotifyArtifact {
	t.Helper()
	for _, fa := range idx {
		if fa.ID == id {
			return fa
		}
	}
	t.Fatalf("artifact %q not found in index", id)
	return NotifyArtifact{}
}
