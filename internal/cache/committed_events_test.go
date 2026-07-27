package cache

import (
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestCommittedEvents_FiltersBySubjectAndYear proves CommittedEvents'
// contract: the identical subject-filtered committed-history read
// internal/cli's and internal/mcp's own LegalityAdapter.committedEvents
// used to carry verbatim in both adapter files (spec
// 01-resolver-one-home.md §5).
func TestCommittedEvents_FiltersBySubjectAndYear(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	base := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)

	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": "XQ-axon-20260721-k3f9", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base),
	})
	fx.commitEvent("axon", fxULID(2), map[string]any{
		"subject": "XQ-axon-20260721-OTHER", "transition": "submit",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base.Add(time.Hour)),
	})

	events, err := CommittedEvents(fx.dir, "axon", "XQ-axon-20260721-k3f9")
	if err != nil {
		t.Fatalf("CommittedEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1 (the OTHER subject's event must be filtered out)", len(events))
	}
	got := events[0]
	if got.Subject != "XQ-axon-20260721-k3f9" || got.Transition != fold.TSubmit || got.Actor.System != "axon" {
		t.Fatalf("event = %+v, want subject=XQ-axon-20260721-k3f9 transition=submit actor.system=axon", got)
	}
}

// TestCommittedEvents_DecodesVersion proves the P4 (04-per-version-
// lifecycle.plan.md) fix to mirrorCommittedEvent: a committed contract
// publish/deprecate/retire event's `version` field must survive into the
// returned fold.Event, or every LegalityAdapter.CheckLegality caller that
// reads this history sees a version-less candidate no matter what the
// event actually recorded — silently falling back to the legacy
// subject-scoped path forever. Teeth: reverting mirrorCommittedEvent's
// `Version` field (or the `Version: ev.Version` assignment below it)
// reds this on the empty-string check alone.
func TestCommittedEvents_DecodesVersion(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	base := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)

	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": "XC-axon-20260721-k3f9", "transition": "publish", "version": "1.2.0",
		"actor": map[string]any{"kind": "agent", "name": "axon-bot", "system": "axon"},
		"at":    fxAt(base),
	})

	events, err := CommittedEvents(fx.dir, "axon", "XC-axon-20260721-k3f9")
	if err != nil {
		t.Fatalf("CommittedEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Version != "1.2.0" {
		t.Fatalf("events[0].Version = %q, want %q", events[0].Version, "1.2.0")
	}
}

// TestCommittedEvents_NoEventsDirReturnsEmptyNotError proves the
// fresh-mirror degradation both former adapter-local copies relied on: an
// absent system/events/ directory (nothing committed yet) is (nil, nil),
// never an error.
func TestCommittedEvents_NoEventsDirReturnsEmptyNotError(t *testing.T) {
	t.Parallel()
	events, err := CommittedEvents(t.TempDir(), "axon", "XQ-axon-20260721-k3f9")
	if err != nil {
		t.Fatalf("CommittedEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}
