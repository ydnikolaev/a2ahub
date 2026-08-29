package cache

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestBlockedOwnerNamesTheBlockerNotTheTarget is P1's US-3, transferred to
// P6's blocked_by field (2026-08-08 amendment, docs/features/archive/
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

// TestBlockedOwnerRefusesABystanderCandidate is README AC3's own gate:
// resolveBlockedByOwner (mirror.go) must check the candidate against
// fold.CheckLegality before naming it, and a blocked artifact must never
// surface an owner who has no legal move on the PARENT envelope.
// "thirdwheel" is a real space member — MembershipMember, so a nil/absent
// membership status could not explain a refusal on its own — but is
// neither the parent's `from` (axon) nor any of its `to` (seomatrix), so
// RoleEitherParty's floor (notePermitted's own predicate, fold.go) refuses
// it exactly the way a genuine bystander's `note` is refused (fold.go
// :369-375's own worked example). resolveVerdict's fallback then behaves
// exactly as TestBlockedOwnerFallsBackWithoutABlockedByFact's no-fact case
// — the target is still owed the unblock, because the named blocker was
// never a legally-possible one.
func TestBlockedOwnerRefusesABystanderCandidate(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t,
		fixtureParticipant{System: "axon"},
		fixtureParticipant{System: "seomatrix"},
		fixtureParticipant{System: "thirdwheel"},
	)

	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XW-axon-20260801-blk3.md", map[string]any{
		"schema": "envelope/v1", "id": "XW-axon-20260801-blk3", "type": "work_request",
		"title": "seo matrix rework 3", "space": "fixture-space", "from": "axon",
		"to": []string{"seomatrix"}, "actor": map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
	}, "body")
	fx.commitEvent("axon", fxULID(40), map[string]any{
		"subject": "XW-axon-20260801-blk3", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"}, "at": fxAt(base),
	})
	fx.commitEvent("seomatrix", fxULID(41), map[string]any{
		"subject": "XW-axon-20260801-blk3", "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(time.Hour)),
	})
	fx.commitEvent("seomatrix", fxULID(42), map[string]any{
		"subject": "XW-axon-20260801-blk3", "transition": "block",
		"actor": map[string]any{"kind": "agent", "name": "seo-bot", "system": "seomatrix"}, "at": fxAt(base.Add(2 * time.Hour)),
	})

	// seomatrix's own facts name "thirdwheel" as the blocker, but
	// thirdwheel is neither party to XW-axon-20260801-blk3 — a bystander
	// CheckLegality's RoleEitherParty floor refuses.
	fx.commitArtifact("seomatrix/exchanges/XS-seomatrix-20260801-blk3.md", map[string]any{
		"schema": "envelope/v2", "id": "XS-seomatrix-20260801-blk3", "type": "response",
		"title": "why this is blocked", "space": "fixture-space", "from": "seomatrix",
		"to": []string{"axon"}, "parent": "XW-axon-20260801-blk3", "result": "partial",
		"blocked_by": map[string]any{"reason_code": "other", "owner": "thirdwheel", "needs": "decision"},
		"actor":      map[string]any{"kind": "agent", "name": "seo-bot"}, "created": fxAt(base.Add(3 * time.Hour)),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "resp body")

	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "seomatrix", mustManifest(t, fx))
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}

	fa := findArtifact(t, idx, "XW-axon-20260801-blk3")
	if fa.Result.State != fold.StateBlocked {
		t.Fatalf("state = %q, want blocked", fa.Result.State)
	}
	if fa.BlockedByOwner != "" {
		t.Errorf("BlockedByOwner = %q, want \"\" — thirdwheel has no legal move on XW-axon-20260801-blk3 (neither from nor to), so CheckLegality must refuse it", fa.BlockedByOwner)
	}

	verdict := resolveVerdict(fa, "seomatrix", mustManifest(t, fx), "")
	if len(verdict.Owners) != 1 || verdict.Owners[0] != "seomatrix" {
		t.Errorf("verdict.Owners = %v, want [seomatrix] — falls back to the target-owed unblock exactly as the no-fact case does, since the named owner was never legally possible", verdict.Owners)
	}
	if verdict.Expected != fold.TUnblock {
		t.Errorf("verdict.Expected = %q, want %q", verdict.Expected, fold.TUnblock)
	}
	for _, owner := range verdict.Owners {
		if owner == "thirdwheel" {
			t.Errorf("verdict.Owners leaks the refused bystander candidate: %v", verdict.Owners)
		}
	}
}
