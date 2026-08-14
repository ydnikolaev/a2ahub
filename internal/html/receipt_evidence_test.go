package html

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
)

func TestReceiptEvidenceProjectsToArtifactAndThreadModels(t *testing.T) {
	t.Parallel()
	mismatch := receiptHTMLMismatch()
	producer := provenance.Producer{Tool: "a2a", Version: "0.19.0"}

	detail, err := toArtifactDetail(cache.ShowResult{
		ID: "XQ-receipt", Type: "question", Title: "Receipt", From: "axon", To: []string{"beta"},
		State: "acknowledged", Body: "body", Envelope: map[string]any{},
		Events: []cache.EventSummary{
			{ULID: "01", Subject: "XQ-receipt", Transition: fold.TSubmit, ClaimedState: "submitted", Actor: "axon-bot", ActorSystem: "axon"},
			{
				ULID: "02", Subject: "XQ-receipt", Transition: fold.TAcknowledge, ClaimedState: "closed",
				ActorKind: "agent", Actor: "beta-bot", ActorSystem: "beta", ActorModel: "gpt-5.6",
				ActorSession: "sha256:360f0b0743ceb50fd947fd94c0137516a7ddc7b52bbc5e9a4e6e855174e63a6a",
				ProducedBy:   producer, Consistency: mismatch,
			},
		},
	})
	if err != nil {
		t.Fatalf("toArtifactDetail: %v", err)
	}
	if detail.Events[0].Consistency != nil {
		t.Fatalf("matching receipt created a diagnostic row: %+v", detail.Events[0])
	}
	if detail.Events[0].ClaimedState != "submitted" {
		t.Fatalf("matching receipt absent from artifact audit model: %+v", detail.Events[0])
	}
	got := detail.Events[1]
	if got.ActorKind != "agent" || got.ActorModel != "gpt-5.6" || got.ActorSession == "" ||
		got.ProducedBy != producer || got.Consistency == nil || got.Consistency.Actual != "acknowledged" {
		t.Fatalf("artifact receipt projection = %+v", got)
	}

	thread := toThreadView(cache.ThreadResult{
		Thread: "thread:receipt", Space: "sp1", Order: cache.ThreadOrderCommitted,
		Opener: cache.ThreadOpener{ID: "XQ-receipt"},
		Transcript: []cache.TranscriptEntry{{
			Seq: 2, Kind: "event", At: time.Date(2026, 8, 3, 10, 2, 0, 0, time.UTC),
			Event: &cache.TranscriptEvent{
				ULID: "02", Subject: "XQ-receipt", Transition: fold.TAcknowledge, ClaimedState: "closed",
				Actor:      cache.TranscriptEventActor{Kind: "agent", Name: "beta-bot", System: "beta", Model: "gpt-5.6", Session: "session:beta"},
				ProducedBy: producer, Consistency: mismatch,
			},
		}},
		Flags: []cache.ThreadFlag{{Kind: string(fold.FlagStateClaimMismatch), Subject: "XQ-receipt", EventULID: "02", Consistency: mismatch}},
	}, "axon")
	threadEvent := thread.Transcript[0].Event
	if threadEvent == nil || threadEvent.Actor["model"] != "gpt-5.6" || threadEvent.Actor["session"] != "session:beta" ||
		threadEvent.ProducedBy != producer || threadEvent.Consistency == nil {
		t.Fatalf("thread receipt projection = %+v", threadEvent)
	}
	if len(thread.Flags) != 1 || thread.Flags[0].Consistency == nil || thread.Flags[0].Consistency.Actual != "acknowledged" {
		t.Fatalf("thread consistency flags = %+v", thread.Flags)
	}
}

// TestThreadViewCarriesVerdicts is gap #4's own html-layer proof (P6 wave C,
// threat-model.md T5): cache.TranscriptEvent.Verdicts must reach
// html.TranscriptEvent.Verdicts through toThreadView's hand-built
// projection — the same boundary TestTranscriptEventCarriesEveryCacheField
// (transcriptevent_projection_test.go) guards by field NAME; this proves
// the VALUE actually crosses it, not just that the field exists.
func TestThreadViewCarriesVerdicts(t *testing.T) {
	t.Parallel()
	thread := toThreadView(cache.ThreadResult{
		Thread: "thread:verdicts", Space: "sp1", Order: cache.ThreadOrderCommitted,
		Opener: cache.ThreadOpener{ID: "XW-verdicts"},
		Transcript: []cache.TranscriptEntry{{
			Seq: 1, Kind: "event", At: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
			Event: &cache.TranscriptEvent{
				ULID: "01", Subject: "XW-verdicts", Transition: "close",
				Actor: cache.TranscriptEventActor{Kind: "agent", Name: "axon-bot", System: "axon"},
				Verdicts: []cache.TranscriptVerdict{
					{Index: 0, Verdict: "met", CauseOwner: "axon"},
					{Index: 1, Verdict: "unmet", CauseOwner: "seomatrix"},
				},
			},
		}},
	}, "axon")
	threadEvent := thread.Transcript[0].Event
	if threadEvent == nil {
		t.Fatal("toThreadView dropped the event row entirely")
	}
	want := []cache.TranscriptVerdict{
		{Index: 0, Verdict: "met", CauseOwner: "axon"},
		{Index: 1, Verdict: "unmet", CauseOwner: "seomatrix"},
	}
	if len(threadEvent.Verdicts) != len(want) || threadEvent.Verdicts[0] != want[0] || threadEvent.Verdicts[1] != want[1] {
		t.Fatalf("threadEvent.Verdicts = %+v, want %+v", threadEvent.Verdicts, want)
	}
}

// TestArtifactDetailCarriesVerdicts is B34's own html-layer proof
// (agent-exchange-2026-08 wave 37b): cache.EventSummary.Verdicts must reach
// html.ArtifactDetailEvent.Verdicts through toArtifactDetail — the same
// cache-type-straight-through idiom TestThreadViewCarriesVerdicts already
// proves for the thread transcript half, now proven for the artifact-detail
// panel that wave 36 left with a source but no field.
func TestArtifactDetailCarriesVerdicts(t *testing.T) {
	t.Parallel()
	detail, err := toArtifactDetail(cache.ShowResult{
		ID: "XW-verdicts", Type: "work_request", Events: []cache.EventSummary{{
			ULID: "01", Subject: "XW-verdicts", Transition: "close", Actor: "axon-bot", ActorSystem: "axon",
			Verdicts: []cache.TranscriptVerdict{
				{Index: 0, Verdict: "met", CauseOwner: "axon"},
				{Index: 1, Verdict: "unmet", CauseOwner: "seomatrix"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("toArtifactDetail: %v", err)
	}
	want := []cache.TranscriptVerdict{
		{Index: 0, Verdict: "met", CauseOwner: "axon"},
		{Index: 1, Verdict: "unmet", CauseOwner: "seomatrix"},
	}
	got := detail.Events[0].Verdicts
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("detail.Events[0].Verdicts = %+v, want %+v", got, want)
	}
}

func TestReceiptEvidenceHTMLJSONOmitsAbsentOptionalFields(t *testing.T) {
	t.Parallel()
	detail, err := toArtifactDetail(cache.ShowResult{
		ID: "XQ-legacy", Type: "question", Events: []cache.EventSummary{{
			ULID: "01", Subject: "XQ-legacy", Transition: fold.TNote, Actor: "human", ActorSystem: "axon",
		}},
	})
	if err != nil {
		t.Fatalf("toArtifactDetail: %v", err)
	}
	raw, err := json.Marshal(detail.Events[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(raw)
	for _, absent := range []string{"claimed_state", "actor_kind", "actor_model", "actor_session", "produced_by", "consistency", "verdicts"} {
		if strings.Contains(got, `"`+absent+`"`) {
			t.Fatalf("absent field %q emitted in %s", absent, got)
		}
	}
}

func TestReceiptMismatchTemplateIsConsistencyActualFirst(t *testing.T) {
	t.Parallel()
	tmpl := dashboardTemplateCorpus(t)
	for _, required := range []string{
		"Consistency, protocol and read evidence",
		"authoritative actual ",
		"; producer claimed ",
		`f.source === "consistency"`,
		`ev.consistency`,
	} {
		if !strings.Contains(tmpl, required) {
			t.Fatalf("template missing receipt consistency contract %q", required)
		}
	}
	if strings.Contains(tmpl, "ev.claimed_state") {
		t.Fatal("ordinary HTML timeline reads a matching receipt as a display signal")
	}
}

func receiptHTMLMismatch() *cache.ReceiptMismatch {
	return &cache.ReceiptMismatch{
		Kind: string(fold.FlagStateClaimMismatch), EventULID: "02", Subject: "XQ-receipt",
		Scope:  cache.ReceiptScope{Kind: string(fold.EvaluationScopePrimary), Subject: "XQ-receipt"},
		Actual: "acknowledged", Claimed: "closed",
		Actor:    provenance.Actor{Kind: "agent", Name: "beta-bot", System: "beta", Model: "gpt-5.6", Session: "session:beta"},
		Producer: provenance.Producer{Tool: "a2a", Version: "0.19.0"}, Cause: "unknown",
	}
}
