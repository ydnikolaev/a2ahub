package cache

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestBlockedOwnerNamesTheBlockerNotTheTarget is P1's US-3, transferred to
// P6's blocked_by field (2026-08-08 amendment, docs/features/active/
// agent-exchange-2026-08/plans/06-incompleteness.plan.md): a work_request
// blocked by its own requester must put the pendency verdict's Owners on
// the requester, never unconditionally on the target — proving the wiring
// end to end (decode.go's `blocked_by` decode -> mirror.go's
// BlockedByOwner, legality-checked against THIS artifact's own envelope ->
// inbox.go's single pendency.Input call site -> pendency's blocked row).
func TestBlockedOwnerNamesTheBlockerNotTheTarget(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260801-blk1.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260801-blk1", "type": "work_request",
		"title": "seo matrix rework", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(20), map[string]any{
		"subject": "XW-axon-20260801-blk1", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(21), map[string]any{
		"subject": "XW-axon-20260801-blk1", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})
	fx.commitEvent("seomatrix", fxULID(22), map[string]any{
		"subject": "XW-axon-20260801-blk1", "transition": "block",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
	})

	// A response naming the blocked work_request as parent, carrying
	// envelope/v2/response's own `blocked_by.owner`: seomatrix's own facts
	// say axon — the REQUESTER — is who the wait is actually on. Only
	// this artifact's own `blocked_by` fact is under test, so no
	// accompanying `respond` event is committed on the parent (that would
	// move the parent's state past `blocked` entirely).
	fx.commitArtifact("seomatrix/exchanges/XS-seomatrix-20260801-blk1.md", map[string]any{
		"schema": "envelope/v2", "id": "XS-seomatrix-20260801-blk1", "type": "response",
		"title": "why this is blocked", "space": "fixture-space", "from": "seomatrix",
		"to": []string{"axon"}, "parent": "XW-axon-20260801-blk1", "result": "partial",
		"blocked_by": map[string]any{"reason_code": "other", "owner": "axon", "needs": "decision"},
		"actor":      map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(3 * time.Hour)),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "resp body")

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "seomatrix", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	fa := findArtifact(t, idx, "XW-axon-20260801-blk1")
	if fa.Result.State != fold.StateBlocked {
		t.Fatalf("state = %q, want blocked", fa.Result.State)
	}
	if fa.BlockedByOwner != "axon" {
		t.Fatalf("BlockedByOwner = %q, want %q (axon is env.From, so RoleEitherParty's floor check passes)", fa.BlockedByOwner, "axon")
	}

	verdict := resolveVerdict(fa, "seomatrix", mustManifest(t, fx), "")
	if len(verdict.Owners) != 1 || verdict.Owners[0] != "axon" {
		t.Fatalf("verdict.Owners = %v, want [axon] — the caller-resolved blocker, not the target (seomatrix)", verdict.Owners)
	}
	if verdict.Expected != "" {
		t.Fatalf("verdict.Expected = %q, want \"\" — unblock remains the target's own event", verdict.Expected)
	}
	for _, owner := range verdict.Owners {
		if owner == "seomatrix" {
			t.Fatalf("verdict.Owners names the target (seomatrix), which is exactly the US-3 attribution bug: %v", verdict.Owners)
		}
	}
}

// TestBlockedOwnerFallsBackWithoutABlockedByFact asserts the byte-identical
// regression floor: a blocked work_request with no fulfilling response at
// all still names the target, exactly as it did before BlockedByOwner
// existed.
func TestBlockedOwnerFallsBackWithoutABlockedByFact(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260801-blk2.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260801-blk2", "type": "work_request",
		"title": "seo matrix rework 2", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(30), map[string]any{
		"subject": "XW-axon-20260801-blk2", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(31), map[string]any{
		"subject": "XW-axon-20260801-blk2", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})
	fx.commitEvent("seomatrix", fxULID(32), map[string]any{
		"subject": "XW-axon-20260801-blk2", "transition": "block",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
	})

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "seomatrix", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, "XW-axon-20260801-blk2")
	if fa.BlockedByOwner != "" {
		t.Fatalf("BlockedByOwner = %q, want \"\" — no response named this artifact as parent", fa.BlockedByOwner)
	}

	verdict := resolveVerdict(fa, "seomatrix", mustManifest(t, fx), "")
	if len(verdict.Owners) != 1 || verdict.Owners[0] != "seomatrix" {
		t.Fatalf("verdict.Owners = %v, want [seomatrix] (unchanged target-owed behaviour)", verdict.Owners)
	}
	if verdict.Expected != fold.TUnblock {
		t.Fatalf("verdict.Expected = %q, want %q", verdict.Expected, fold.TUnblock)
	}
}
