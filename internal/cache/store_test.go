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

// TestShow_CarriesAcceptanceCriteriaAndExpectedResponse: a responder composes
// verdicts per criterion id, so `a2a show` must expose the artifact's own
// acceptance_criteria[] (id-addressed and ordinal forms) and expected_response
// (fb-20261107-37ce55).
func TestShow_CarriesAcceptanceCriteriaAndExpectedResponse(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"}, fixtureParticipant{System: "seomatrix"})
	base := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		id       string
		criteria any
		want     []AcceptanceCriterion
	}{
		{"XW-axon-20260801-ids", []map[string]any{{"id": "c1", "text": "first"}, {"id": "c2", "text": "second"}}, []AcceptanceCriterion{{ID: "c1", Text: "first"}, {ID: "c2", Text: "second"}}},
		{"XW-axon-20260801-ord", []string{"plain one"}, []AcceptanceCriterion{{Text: "plain one"}}},
	} {
		fields := map[string]any{
			"schema": "envelope/v2", "id": tc.id, "type": "work_request", "title": "t",
			"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
			"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
			"priority": "p2", "blocking": false, "classification": "internal",
			"category": "data", "acceptance_criteria": tc.criteria,
			"expected_response": map[string]any{"shape": "verdict", "by": "2026-09-01"},
		}
		fx.commitArtifact("axon/exchanges/"+tc.id+".md", fields, "body")
		fx.commitEvent("axon", fxULID(910), evt(tc.id, "publish", "axon", base))
	}
	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx)}}, func() time.Time { return base.Add(time.Hour) }, 0)

	for _, id := range []string{"XW-axon-20260801-ids", "XW-axon-20260801-ord"} {
		res, err := store.Show(context.Background(), id)
		if err != nil {
			t.Fatalf("Show %s: %v", id, err)
		}
		if res.ExpectedResponse == nil || res.ExpectedResponse.Shape != "verdict" || res.ExpectedResponse.By != "2026-09-01" {
			t.Fatalf("%s ExpectedResponse = %+v", id, res.ExpectedResponse)
		}
		if len(res.AcceptanceCriteria) == 0 {
			t.Fatalf("%s: no acceptance criteria", id)
		}
		if id == "XW-axon-20260801-ids" && (len(res.AcceptanceCriteria) != 2 || res.AcceptanceCriteria[1] != (AcceptanceCriterion{ID: "c2", Text: "second"})) {
			t.Fatalf("ids criteria = %+v", res.AcceptanceCriteria)
		}
		if id == "XW-axon-20260801-ord" && res.AcceptanceCriteria[0] != (AcceptanceCriterion{Text: "plain one"}) {
			t.Fatalf("ordinal criteria = %+v", res.AcceptanceCriteria)
		}
	}
}
