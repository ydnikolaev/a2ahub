package cache

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// showWorkRequestWithAttachments writes a work_request drafted on
// envelope/v2 (the ONE schema attachments[] is declared on) carrying
// attachments, plus the publish event that makes Show/ShowMany resolve it.
func showWorkRequestWithAttachments(fx *fixtureSpace, id, from string, base time.Time, attachments []map[string]any) {
	fields := map[string]any{
		"schema": "envelope/v2", "id": id, "type": "work_request", "title": "carries bytes",
		"space": "fixture-space", "from": from, "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": from + "-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
		"category": "data", "acceptance_criteria": []string{"bytes attached"},
		"attachments": attachments,
	}
	fx.commitArtifact(from+"/exchanges/"+id+".md", fields, "carries bytes")
	fx.commitEvent(from, fxULID(900), evt(id, "publish", from, base))
}

// TestShow_AttachmentsCarryRawFieldsAndVerificationClaim is spec 04
// (agent-exchange-2026-08) §11's amendment: cache.ShowResult.Attachments
// carries the committed attachments[] entry's own fields (ref, digest,
// verification, retention) plus the derived VerificationClaim — one
// datapackage.AttachmentClaim per entry, from ONE decode
// (attachmentEntriesFromEnvelope) rather than a per-surface re-decode.
func TestShow_AttachmentsCarryRawFieldsAndVerificationClaim(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)

	showWorkRequestWithAttachments(fx, "XW-axon-20260801-raw", "axon", base, []map[string]any{
		{
			"ref": "sha256:aaaa", "digest": "sha256:aaaa", "role": "evidence",
			"verification": datapackage.VerificationOffered, "retention": datapackage.RetentionPinned,
		},
	})

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)

	result, err := store.Show(context.Background(), "XW-axon-20260801-raw")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1: %+v", len(result.Attachments), result.Attachments)
	}
	got := result.Attachments[0]
	if got.Ref != "sha256:aaaa" || got.Digest != "sha256:aaaa" || got.Role != "evidence" ||
		got.Verification != datapackage.VerificationOffered || got.Retention != datapackage.RetentionPinned {
		t.Fatalf("raw attachment fields did not carry through: %+v", got)
	}
	if got.VerificationClaim != "a verdict is offered for these bytes" {
		t.Fatalf("VerificationClaim = %q, want the offered claim", got.VerificationClaim)
	}
	if got.Lapsed {
		t.Fatalf("pinned retention reported Lapsed=true, want false")
	}
}

// TestShow_AttachmentLapseUsesTheStoresInjectedClockNotWallClock is AC5's
// clock-injection proof, distinguishing it from a test that would pass by
// coincidence against real time.Now(): the store's own Clock is set WELL
// BEFORE expires_at, so the attachment must read as NOT lapsed. If
// buildShowResult (or datapackage.ProjectAttachmentClaim's caller) used
// time.Now() instead of the injected clock, this attachment — whose
// expires_at is in 2026, safely behind the real wall clock — would read as
// lapsed and this test would fail.
func TestShow_AttachmentLapseUsesTheStoresInjectedClockNotWallClock(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2020, 1, 1, 8, 0, 0, 0, time.UTC)

	showWorkRequestWithAttachments(fx, "XW-axon-20200101-early", "axon", base, []map[string]any{
		{
			"ref": "sha256:bbbb", "digest": "sha256:bbbb",
			"verification": datapackage.VerificationRequired, "retention": "1h",
			"expires_at": "2026-08-05T00:00:00Z",
		},
	})

	// The injected clock reads well BEFORE expires_at (2020, vs. a 2026
	// expiry) — the opposite of "well after real wall-clock now", so a
	// buggy time.Now() read would (at the time this wave was built) report
	// Lapsed=true here, and this assertion would catch it.
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)

	result, err := store.Show(context.Background(), "XW-axon-20200101-early")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(result.Attachments) != 1 {
		t.Fatalf("len(Attachments) = %d, want 1: %+v", len(result.Attachments), result.Attachments)
	}
	got := result.Attachments[0]
	if got.Lapsed {
		t.Fatalf("Lapsed = true against the store's injected 2020 clock and a 2026 expiry — the store is not using its own injected Clock")
	}
	if got.ExpiresAt != "2026-08-05T00:00:00Z" {
		t.Fatalf("ExpiresAt = %q, want the committed expires_at carried through", got.ExpiresAt)
	}

	// Now advance the injected clock PAST expiresAt and re-Show: the same
	// artifact must flip to Lapsed=true, proving the clock (not a frozen
	// decode) drives the verdict.
	lateStore := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }, 0)
	lateResult, err := lateStore.Show(context.Background(), "XW-axon-20200101-early")
	if err != nil {
		t.Fatalf("Show (late clock): %v", err)
	}
	if len(lateResult.Attachments) != 1 || !lateResult.Attachments[0].Lapsed {
		t.Fatalf("Attachments = %+v, want Lapsed=true against a 2030 injected clock", lateResult.Attachments)
	}
}

// TestShow_AttachmentsJSONOmitsKeyWhenThereAreNone guards the top-level
// brief's "additive, omitempty" constraint at the cache layer directly: an
// artifact with no attachments[] must not gain an "attachments" key in the
// marshaled ShowResult at all.
func TestShow_AttachmentsJSONOmitsKeyWhenThereAreNone(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	fx.commitArtifact("axon/requires/XR-axon-nokey.md", map[string]any{
		"schema": "envelope/v1", "id": "XR-axon-nokey", "type": "requirement", "title": "target",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "target body")
	fx.commitEvent("axon", fxULID(901), evt("XR-axon-nokey", "publish", "axon", base))

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)

	result, err := store.Show(context.Background(), "XR-axon-nokey")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if result.Attachments != nil {
		t.Fatalf("Attachments = %+v, want nil for an artifact with none", result.Attachments)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal ShowResult: %v", err)
	}
	if strings.Contains(string(encoded), `"attachments"`) {
		t.Fatalf("marshaled ShowResult %s carries an \"attachments\" key for an artifact with none", encoded)
	}
}
