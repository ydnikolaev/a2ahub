package cache

// outcome_waiter_test.go is defects-fix-2026-08 P5's own repro at the
// integration level (docs/inbox/defects/01-open-without-a-waiter.md): a
// document must not render `outcome: open` while nobody's verdict names a
// mover. Unit coverage for fold.OutcomeOfDocument lives in
// internal/fold/outcome_test.go; this file proves the whole seam —
// mirror.go's build-pass moveOwed/registryLateAdopters facts, through
// resolveVerdict, into toItem's projected Item.Outcome — against a real
// fixture mirror, the same shape defects/01's own axon/getvisa probe used.

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestAnnouncementPublishedWithNoAckRequestedIsNotAwaitingAMove is AC1's
// first named case: an announcement published with `ack_requested` left
// UNSET (the schema's own documented default — defects/01's own finding,
// "the flag was simply never set") must not read `outcome: open` while
// WaitingOn/ExpectedTransition are empty.
func TestAnnouncementPublishedWithNoAckRequestedIsNotAwaitingAMove(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	id := "XA-axon-20260801-noack"

	fx.commitArtifact("axon/announcements/"+id+".md", map[string]any{
		"schema": "envelope/v1", "id": id, "type": "announcement", "title": "status update",
		"space": "fixture-space", "from": "axon", "to": []string{"beta"},
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
		// ack_requested DELIBERATELY absent — defects/01's own repro.
	}, "informational only")
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": id, "transition": "publish",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Minute)),
	})

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, id)
	if fa.Result.State != fold.StatePublished {
		t.Fatalf("state = %q, want published", fa.Result.State)
	}

	verdict := resolveVerdict(fa, "axon", manifest, "")
	if len(verdict.Owners) != 0 {
		t.Fatalf("verdict.Owners = %v, want none — no ack_requested and no registered late adopter", verdict.Owners)
	}
	if verdict.Expected != "" {
		t.Fatalf("verdict.Expected = %q, want empty", verdict.Expected)
	}

	item := toItem(fa, false, false)
	if item.Outcome == fold.OutcomeOpen {
		t.Fatalf("Item.Outcome = %q with WaitingOn/Expected empty — exactly the defect (open, nobody waiting)", item.Outcome)
	}
	if item.Outcome != fold.OutcomeSettled {
		t.Errorf("Item.Outcome = %q, want %q", item.Outcome, fold.OutcomeSettled)
	}
}

// TestContractPublishedWithNoRegisteredConsumerIsNotAwaitingAMove is AC1's
// second named case, the contract half.
func TestContractPublishedWithNoRegisteredConsumerIsNotAwaitingAMove(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-nowaiter"

	fx.commitArtifact("axon/provides/nowaiter/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(2), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})
	// Deliberately NO rcWriteConsumesYAML call — no registered consumer.

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, contractID)
	if fa.Result.State != fold.StatePublished {
		t.Fatalf("state = %q, want published", fa.Result.State)
	}
	if fa.OperationalDebtOwed {
		t.Fatal("OperationalDebtOwed = true, want false — no registered consumer exists")
	}

	verdict := resolveVerdict(fa, "axon", manifest, "")
	if len(verdict.Owners) != 0 {
		t.Fatalf("verdict.Owners = %v, want none — no registered consumer, no activation debt", verdict.Owners)
	}

	item := toItem(fa, false, false)
	if item.Outcome == fold.OutcomeOpen {
		t.Fatalf("Item.Outcome = %q with WaitingOn/Expected empty — exactly the defect (open, nobody waiting)", item.Outcome)
	}
	if item.Outcome != fold.OutcomeSettled {
		t.Errorf("Item.Outcome = %q, want %q", item.Outcome, fold.OutcomeSettled)
	}
	if exchangeActive(fa, "axon", manifest) {
		t.Error("exchangeActive = true — a published contract with no registered consumer must not linger in the producer's active feed forever (defects/02, \"Not a finding\")")
	}
}

// TestDeprecationAnnouncementWithRegistryMatchedConsumerStaysOwed is the
// spec's own testing-requirement edge case (spec 05 §6): a deprecation
// announcement with NO ack_requested flag but a REGISTRY-matched consumer
// must stay owed — the registry half of unackedTargets carries no flag
// qualifier (03-domain.md:115). This guards against a fix that swings too
// far and settles every flagless announcement regardless of a real debt.
func TestDeprecationAnnouncementWithRegistryMatchedConsumerStaysOwed(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"}, fixtureParticipant{System: "gamma"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	id := "XA-axon-20260801-deprecation"
	contractID := "XC-axon-widget"

	// `to:` is frozen at deprecate time and names only beta — gamma adopts
	// the contract AFTER, via its own consumes.yaml (F1's own late-adopter
	// shape). ack_requested is left unset on purpose: the registry-matched
	// half of the rule carries no flag qualifier at all.
	fx.commitArtifact("axon/announcements/"+id+".md", map[string]any{
		"schema": "envelope/v1", "id": id, "type": "announcement", "title": "deprecating " + contractID,
		"space": "fixture-space", "from": "axon", "to": []string{"beta"},
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
		"thread": id, "category": "deprecation", "deprecates": contractID + "@1.0.0",
	}, "deprecation notice")
	fx.commitEvent("axon", fxULID(3), map[string]any{
		"subject": id, "transition": "publish",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Minute)),
	})
	fxWriteConsumes(t, fx, "beta", contractID, 1)
	fxWriteConsumes(t, fx, "gamma", contractID, 1)
	fx.commitEvent("beta", fxULID(4), map[string]any{
		"subject": id, "transition": "acknowledge",
		"actor": map[string]any{"kind": "agent", "name": "beta-bot", "system": "beta"},
		"at":    fxAt(base.Add(2 * time.Minute)),
	})

	manifest := mustManifest(t, fx)
	idx, _, err := buildIndex(context.Background(), "sp1", fx.dir, "axon", manifest)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	fa := findArtifact(t, idx, id)

	// The AUTHOR's own perspective (axon), NOT gamma's — this is exactly
	// F1's sharpest case (defects/02): the author checking their own
	// outbox previously could never see anyone else's late adoption.
	verdict := resolveVerdict(fa, "axon", manifest, "")
	if !containsString(verdict.Owners, "gamma") {
		t.Fatalf("verdict.Owners = %v, want to include gamma — F1: the author's own outbox must see a mirror-wide late adopter, not only its own registry", verdict.Owners)
	}

	if got := fold.OutcomeOfDocument(fa.kind(), fa.Result.State, fa.moveOwed); got != fold.OutcomeOpen {
		t.Errorf("OutcomeOfDocument = %q, want %q — a real registry-matched debt must not be hidden", got, fold.OutcomeOpen)
	}
	item := toItem(fa, false, false)
	if item.Outcome != fold.OutcomeOpen {
		t.Errorf("Item.Outcome = %q, want %q — gamma still owes an acknowledge", item.Outcome, fold.OutcomeOpen)
	}
}

// TestApplyExpiredOverlayIsAProductionCaller is F5's own test (defects/02):
// fold.ExpiredOverlay had zero production callers; Store.index's own
// applyExpiredOverlay (store.go) is now one, reached through the same
// chokepoint every read verb funnels through.
func TestApplyExpiredOverlayIsAProductionCaller(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

	expiredID := "XA-axon-20260801-expired"
	fx.commitArtifact("axon/announcements/"+expiredID+".md", map[string]any{
		"schema": "envelope/v1", "id": expiredID, "type": "announcement", "title": "time-boxed notice",
		"space": "fixture-space", "from": "axon", "to": []string{"beta"},
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
		"valid_until": "2026-07-01T00:00:00Z", // already past `now`, below
	}, "expires before now")
	fx.commitEvent("axon", fxULID(5), map[string]any{
		"subject": expiredID, "transition": "publish",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Minute)),
	})

	freshID := "XA-axon-20260801-fresh"
	fx.commitArtifact("axon/announcements/"+freshID+".md", map[string]any{
		"schema": "envelope/v1", "id": freshID, "type": "announcement", "title": "no expiry",
		"space": "fixture-space", "from": "axon", "to": []string{"beta"},
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false, "classification": "internal",
		// valid_until absent — never expires.
	}, "no stated expiry")
	fx.commitEvent("axon", fxULID(6), map[string]any{
		"subject": freshID, "transition": "publish",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Minute)),
	})

	now := base.Add(30 * 24 * time.Hour) // well after the expired one's valid_until
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{
		SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return now }, 0)

	idx, _, err := store.index(context.Background())
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	got := findArtifact(t, idx["sp1"], expiredID)
	if !got.expired {
		t.Error("expired announcement's own foldedArtifact.expired = false, want true — fold.ExpiredOverlay must have a real caller")
	}
	gotFresh := findArtifact(t, idx["sp1"], freshID)
	if gotFresh.expired {
		t.Error("announcement with no valid_until read as expired, want false — a zero validUntil means never expired")
	}
}

// TestDashboardSnapshotArchivesTheNamedPairsInsteadOfOutboxOpen proves the
// fix reaches the REAL production dashboard path, not only toItem in
// isolation: internal/html/assemble.go:205 calls Store.DashboardSnapshot
// (dashboard.go, off-limits to this wave), and this wave's own fix reaches
// it with ZERO edits there — dashboard.go's own toItem(fa, ...) call
// already picks up fa.moveOwed, because mirror.go's build pass computes it
// during the SAME s.index(ctx) call dashboard.go already makes.
func TestDashboardSnapshotArchivesTheNamedPairsInsteadOfOutboxOpen(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	odSetManifest(t, fx, "0.20.0", fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"})
	base := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	contractID := "XC-axon-dashboard"

	fx.commitArtifact("axon/provides/dashboard/contract.md",
		odContractFields(contractID, "axon", nil, base), "contract body")
	fx.commitEvent("axon", fxULID(7), map[string]any{
		"subject": contractID, "transition": "publish", "version": "1.0.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{
		SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return base.Add(24 * time.Hour) }, 0)

	snap, err := store.DashboardSnapshot(context.Background())
	if err != nil {
		t.Fatalf("DashboardSnapshot: %v", err)
	}
	for _, item := range snap.Outbox {
		if item.ID == contractID {
			t.Fatalf("contract with no registered consumer sat in Outbox as %+v — outcome=%q waiting_on=%v, want it archived (settled)", item, item.Outcome, item.WaitingOn)
		}
	}
	found := false
	for _, item := range snap.Archive {
		if item.ID != contractID {
			continue
		}
		found = true
		if item.Outcome == fold.OutcomeOpen {
			t.Errorf("archived contract's own Item.Outcome = %q, want settled — the defect this wave fixes reaches DashboardSnapshot's own projection with no edits to dashboard.go", item.Outcome)
		}
	}
	if !found {
		t.Fatalf("contract %s missing from both Outbox and Archive entirely", contractID)
	}
}
