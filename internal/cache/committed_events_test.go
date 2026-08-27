package cache

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
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

// TestCommittedEventsAllSections_ReadsEveryParticipantSection proves D-2's
// own fix: an event committed under a DIFFERENT participant's own section
// than the subject's home system is still found — the exact gap
// CommittedEvents (single-section, caller-supplied system) cannot close.
func TestCommittedEventsAllSections_ReadsEveryParticipantSection(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "beta"}, fixtureParticipant{System: "gamma"})
	base := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	const subject = "XD-axon-20260827-s001"

	// Committed under axon's OWN section (the subject's own home system).
	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": subject, "transition": "propose",
		"actor": map[string]any{"kind": "agent", "name": "bot", "system": "axon"},
		"at":    fxAt(base),
	})
	// Committed under beta's and gamma's OWN sections — neither is the
	// subject's own home system. CommittedEvents(mirrorDir, "axon", subject)
	// alone would never see these two.
	fx.commitEvent("beta", fxULID(2), map[string]any{
		"subject": subject, "transition": "approve",
		"actor": map[string]any{"kind": "agent", "name": "bot", "system": "beta"},
		"at":    fxAt(base.Add(time.Minute)),
	})
	fx.commitEvent("gamma", fxULID(3), map[string]any{
		"subject": subject, "transition": "approve",
		"actor": map[string]any{"kind": "agent", "name": "bot", "system": "gamma"},
		"at":    fxAt(base.Add(2 * time.Minute)),
	})
	// A different subject, also committed elsewhere, must NOT leak in.
	fx.commitEvent("beta", fxULID(4), map[string]any{
		"subject": "XD-axon-20260827-OTHER", "transition": "propose",
		"actor": map[string]any{"kind": "agent", "name": "bot", "system": "beta"},
		"at":    fxAt(base.Add(3 * time.Minute)),
	})

	events, err := CommittedEventsAllSections(fx.dir, subject)
	if err != nil {
		t.Fatalf("CommittedEventsAllSections: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("len(events) = %d, want 3 (propose + 2 cross-section approves); got %+v", len(events), events)
	}
	var transitions []string
	for _, e := range events {
		if e.Subject != subject {
			t.Fatalf("event carries subject %q, want %q: %+v", e.Subject, subject, e)
		}
		transitions = append(transitions, e.Transition)
	}
	wantSystems := map[string]bool{"axon": true, "beta": true, "gamma": true}
	for _, e := range events {
		if !wantSystems[e.Actor.System] {
			t.Fatalf("unexpected actor.system %q in %+v", e.Actor.System, e)
		}
		delete(wantSystems, e.Actor.System)
	}
	if len(wantSystems) != 0 {
		t.Fatalf("missing events from section(s): %+v (got transitions %v)", wantSystems, transitions)
	}
}

// TestCommittedEventsAllSections_AbsentMirrorDirReturnsEmptyNotError
// mirrors CommittedEvents' own degradation rule (TestCommittedEvents_
// NoEventsDirReturnsEmptyNotError), generalized one level up: a mirrorDir
// that does not exist at all is a zero history and a nil error, never a
// caller-visible failure.
func TestCommittedEventsAllSections_AbsentMirrorDirReturnsEmptyNotError(t *testing.T) {
	t.Parallel()
	events, err := CommittedEventsAllSections(filepath.Join(t.TempDir(), "does-not-exist"), "XD-axon-20260827-s001")
	if err != nil {
		t.Fatalf("CommittedEventsAllSections: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestCommittedEventsWithEvidenceRetainsReceiptAndSafeProvenance(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	ulid := fxULID(1)

	fx.commitEvent("axon", ulid, map[string]any{
		"subject": "XQ-axon-20260803-p1d", "transition": "acknowledge",
		"state": "acknowledged",
		"actor": map[string]any{
			"kind": "agent", "name": "axon-bot", "system": "axon",
			"model": "gpt-5.6", "session": "session:worker-17",
		},
		"produced_by": map[string]any{"tool": "a2a", "version": "0.19.0"},
		"at":          fxAt(base),
	})

	history, err := CommittedEventsWithEvidence(fx.dir, "axon", "XQ-axon-20260803-p1d")
	if err != nil {
		t.Fatalf("CommittedEventsWithEvidence: %v", err)
	}
	if len(history.Events) != 1 {
		t.Fatalf("len(history.Events) = %d, want 1", len(history.Events))
	}
	if history.Events[0].ClaimedState != fold.StateAcknowledged {
		t.Fatalf("receipt = %q, want %q", history.Events[0].ClaimedState, fold.StateAcknowledged)
	}
	want := provenance.EventEvidence{
		Receipt: "acknowledged",
		Actor: provenance.Actor{
			Kind: "agent", Name: "axon-bot", System: "axon",
			Model: "gpt-5.6", Session: "session:worker-17",
		},
		Producer: provenance.Producer{Tool: "a2a", Version: "0.19.0"},
	}
	if got := history.EvidenceByULID[ulid]; got != want {
		t.Fatalf("EvidenceByULID[%q] = %+v, want %+v", ulid, got, want)
	}
}

func TestCommittedEventsWithEvidenceRedactsUnsafeLegacySession(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	ulid := fxULID(1)
	const unsafe = "token:super-secret"
	const wantDigest = "sha256:360f0b0743ceb50fd947fd94c0137516a7ddc7b52bbc5e9a4e6e855174e63a6a"

	fx.commitEvent("axon", ulid, map[string]any{
		"subject": "XQ-axon-20260803-legacy", "transition": "submit",
		"actor": map[string]any{
			"kind": "agent", "name": "legacy-bot", "system": "axon", "session": unsafe,
		},
		"at": fxAt(time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)),
	})

	history, err := CommittedEventsWithEvidence(fx.dir, "axon", "XQ-axon-20260803-legacy")
	if err != nil {
		t.Fatalf("CommittedEventsWithEvidence: %v", err)
	}
	if got := history.EvidenceByULID[ulid].Actor.Session; got != wantDigest {
		t.Fatalf("legacy session = %q, want %q", got, wantDigest)
	}
}

func TestCommittedEventsCompatibilityAndMetadataPerturbation(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	subject := "XQ-axon-20260803-equivalence"

	fx.commitEvent("axon", fxULID(1), map[string]any{
		"subject": subject, "transition": "submit", "state": "submitted",
		"actor": map[string]any{
			"kind": "agent", "name": "axon-bot", "system": "axon",
			"model": "model-a", "session": "session:a",
		},
		"produced_by": map[string]any{"tool": "a2a", "version": "0.19.0"},
		"at":          fxAt(base),
	})

	history, err := CommittedEventsWithEvidence(fx.dir, "axon", subject)
	if err != nil {
		t.Fatalf("CommittedEventsWithEvidence: %v", err)
	}
	legacy, err := CommittedEvents(fx.dir, "axon", subject)
	if err != nil {
		t.Fatalf("CommittedEvents: %v", err)
	}
	if !reflect.DeepEqual(legacy, history.Events) {
		t.Fatalf("compatibility reader = %+v, richer events = %+v", legacy, history.Events)
	}

	before := append([]fold.Event(nil), history.Events...)
	env := fold.Envelope{ID: subject, Kind: fold.KindQuestion, From: "axon", To: []string{"beta"}}
	membership := func(string) fold.MembershipStatus { return fold.MembershipMember }
	resultBefore := fold.Fold(fold.KindQuestion, env, before, membership)
	evidence := history.EvidenceByULID[fxULID(1)]
	evidence.Actor.Model = "different-model"
	evidence.Actor.Session = "different:session"
	evidence.Producer = provenance.Producer{Tool: "other-tool", Version: "build-2"}
	history.EvidenceByULID[fxULID(1)] = evidence
	if !reflect.DeepEqual(history.Events, before) {
		t.Fatalf("sidecar metadata mutation changed fold events: before=%+v after=%+v", before, history.Events)
	}
	if resultAfter := fold.Fold(fold.KindQuestion, env, history.Events, membership); !reflect.DeepEqual(resultAfter, resultBefore) {
		t.Fatalf("sidecar metadata mutation changed fold result: before=%+v after=%+v", resultBefore, resultAfter)
	}
}
