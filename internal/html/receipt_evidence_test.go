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
	for _, absent := range []string{"claimed_state", "actor_kind", "actor_model", "actor_session", "produced_by", "consistency"} {
		if strings.Contains(got, `"`+absent+`"`) {
			t.Fatalf("absent field %q emitted in %s", absent, got)
		}
	}
}

func TestReceiptMismatchTemplateIsConsistencyActualFirst(t *testing.T) {
	t.Parallel()
	tmpl := string(DefaultTemplate())
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
