package cache

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/provenance"
)

func TestReceiptEvidenceSurvivesMirrorShowThreadAndProtocolFlags(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t,
		fixtureParticipant{System: "axon"},
		fixtureParticipant{System: "beta"},
	)
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	const (
		id          = "XQ-axon-20260803-receipt"
		threadID    = "thread:axon-20260803-receipt"
		unsafe      = "token:super-secret"
		wantSession = "sha256:360f0b0743ceb50fd947fd94c0137516a7ddc7b52bbc5e9a4e6e855174e63a6a"
	)

	fx.commitArtifact("axon/exchanges/"+id+".md", map[string]any{
		"schema": "envelope/v1", "id": id, "type": "question", "title": "Receipt propagation",
		"space": "fixture-space", "from": "axon", "to": []string{"beta"},
		"actor":   map[string]any{"kind": "agent", "name": "axon-bot"},
		"created": fxAt(base), "priority": "p2", "blocking": false,
		"thread": threadID, "classification": "restricted",
	}, "Does the producer agree with the fold?")
	matchingULID := fxULID(1)
	fx.commitEvent("axon", matchingULID, map[string]any{
		"subject": id, "transition": "submit", "state": "submitted",
		"actor": map[string]any{
			"kind": "agent", "name": "axon-bot", "system": "axon",
			"model": "gpt-5.6", "session": "session:submit-1",
		},
		"produced_by": map[string]any{"tool": "a2a", "version": "0.19.0"},
		"at":          fxAt(base.Add(time.Minute)),
	})
	mismatchULID := fxULID(2)
	fx.commitEvent("beta", mismatchULID, map[string]any{
		"subject": id, "transition": "acknowledge", "state": "closed",
		"actor": map[string]any{
			"kind": "agent", "name": "beta-bot", "system": "beta",
			"model": "gpt-5.6", "session": unsafe,
		},
		"produced_by": map[string]any{"tool": "a2a", "version": "0.19.0"},
		"at":          fxAt(base.Add(2 * time.Minute)),
	})

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{
		SpaceID: "sp1", Dir: fx.dir, Manifest: mustManifest(t, fx),
	}}, func() time.Time { return base.Add(time.Hour) }, 0)

	show, err := store.Show(context.Background(), id)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(show.Events) != 2 {
		t.Fatalf("show events = %+v, want two", show.Events)
	}
	if show.Events[0].Consistency != nil {
		t.Fatalf("matching receipt created consistency timeline evidence: %+v", show.Events[0].Consistency)
	}
	if show.Events[0].ClaimedState != "submitted" {
		t.Fatalf("matching receipt absent from show audit data: %+v", show.Events[0])
	}
	mismatch := show.Events[1]
	assertReceiptMismatch(t, mismatch.Consistency, mismatchULID, id, "closed", "acknowledged")
	if mismatch.ActorKind != "agent" || mismatch.Actor != "beta-bot" || mismatch.ActorSystem != "beta" ||
		mismatch.ActorModel != "gpt-5.6" || mismatch.ActorSession != wantSession ||
		mismatch.ProducedBy.Tool != "a2a" || mismatch.ProducedBy.Version != "0.19.0" {
		t.Fatalf("show provenance = %+v", mismatch)
	}

	thread, err := store.ThreadView(context.Background(), threadID, "sp1")
	if err != nil {
		t.Fatalf("ThreadView: %v", err)
	}
	var transcriptMatch, transcriptMismatch *TranscriptEvent
	for _, entry := range thread.Transcript {
		if entry.Event != nil && entry.Event.ULID == matchingULID {
			transcriptMatch = entry.Event
		}
		if entry.Event != nil && entry.Event.ULID == mismatchULID {
			transcriptMismatch = entry.Event
		}
	}
	if transcriptMatch == nil || transcriptMatch.ClaimedState != "submitted" || transcriptMatch.Consistency != nil {
		t.Fatalf("matching receipt thread audit data = %+v", transcriptMatch)
	}
	if transcriptMismatch == nil {
		t.Fatalf("mismatch event absent from transcript: %+v", thread.Transcript)
	}
	assertReceiptMismatch(t, transcriptMismatch.Consistency, mismatchULID, id, "closed", "acknowledged")
	if transcriptMismatch.Actor.Session != wantSession || transcriptMismatch.ProducedBy.Tool != "a2a" {
		t.Fatalf("thread provenance = %+v", transcriptMismatch)
	}
	if len(thread.Flags) != 1 || thread.Flags[0].Consistency == nil {
		t.Fatalf("thread flags = %+v, want one evidence-rich consistency flag", thread.Flags)
	}

	flags, err := store.ProtocolFlags(context.Background())
	if err != nil {
		t.Fatalf("ProtocolFlags: %v", err)
	}
	if len(flags) != 1 || flags[0].Source != "consistency" || flags[0].Consistency == nil {
		t.Fatalf("protocol flags = %+v, want one consistency-sourced flag", flags)
	}
	if !strings.HasPrefix(flags[0].Message, "authoritative actual acknowledged; producer claimed closed") {
		t.Fatalf("mismatch message is not actual-first: %q", flags[0].Message)
	}
}

func TestReceiptEvidenceMetadataCannotAffectFoldOrOrdering(t *testing.T) {
	t.Parallel()
	env := fold.Envelope{ID: "XQ-receipt", Kind: fold.KindQuestion, From: "axon", To: []string{"beta"}}
	membership := func(string) fold.MembershipStatus { return fold.MembershipMember }
	events := []fold.Event{
		{ULID: "02", CommitSeq: 2, Subject: env.ID, Transition: fold.TAcknowledge, ClaimedState: "closed", Actor: fold.Actor{System: "beta"}},
		{ULID: "01", CommitSeq: 1, Subject: env.ID, Transition: fold.TSubmit, ClaimedState: fold.StateSubmitted, Actor: fold.Actor{System: "axon"}},
	}
	before := append([]fold.Event(nil), events...)
	baseEvidence := map[string]provenance.EventEvidence{
		"02": provenance.NewEventEvidence("closed", provenance.Actor{Model: "model-a", Session: "session:a"}, provenance.Producer{Tool: "a2a", Version: "0.19.0"}),
	}
	perturbed := map[string]provenance.EventEvidence{
		"02": provenance.NewEventEvidence("closed", provenance.Actor{Model: "model-b", Session: "session:b"}, provenance.Producer{Tool: "partner", Version: "build-9"}),
	}

	resultA, mismatchesA, _ := foldWithReceiptEvidence(fold.KindQuestion, env, events, membership, baseEvidence)
	resultB, mismatchesB, _ := foldWithReceiptEvidence(fold.KindQuestion, env, events, membership, perturbed)
	if !reflect.DeepEqual(resultA, resultB) {
		t.Fatalf("metadata changed fold result: A=%+v B=%+v", resultA, resultB)
	}
	if !reflect.DeepEqual(events, before) {
		t.Fatalf("canonical replay mutated caller ordering: before=%+v after=%+v", before, events)
	}
	if mismatchesA["02"].Actual != mismatchesB["02"].Actual || mismatchesA["02"].Scope != mismatchesB["02"].Scope {
		t.Fatalf("metadata changed canonical mismatch outcome/scope: A=%+v B=%+v", mismatchesA, mismatchesB)
	}
}

func TestClassificationSummariesReuseArtifactWalkerAndReportIncompleteScan(t *testing.T) {
	t.Parallel()
	fx := newFixtureSpace(t, fixtureParticipant{System: "axon"})
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	fx.commitArtifact("axon/exchanges/XQ-public.md", map[string]any{
		"schema": "envelope/v1", "id": "XQ-public", "type": "question", "title": "Public",
		"space": "fixture-space", "from": "axon", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p3", "blocking": false, "classification": "public",
	}, "public")
	fx.commitArtifact("axon/exchanges/XQ-restricted.md", map[string]any{
		"schema": "envelope/v1", "id": "XQ-restricted", "type": "question", "title": "Restricted",
		"space": "fixture-space", "from": "axon", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p3", "blocking": false, "classification": "restricted",
	}, "restricted")
	fx.commitArtifact("axon/exchanges/XQ-unknown.md", map[string]any{
		"schema": "envelope/v1", "id": "XQ-unknown", "type": "question", "title": "Unknown",
		"space": "fixture-space", "from": "axon", "to": []string{"axon"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": fxAt(base),
		"priority": "p3", "blocking": false, "classification": "confidential",
	}, "unknown classification")
	badRel := "axon/exchanges/zzz-broken.md"
	fxWriteFile(t, filepath.Join(fx.dir, filepath.FromSlash(badRel)), "---\nclassification: restricted\n")
	fxCommitAndPush(t, fx.dir, "fixture: malformed classification carrier")

	store := NewStore("axon", t.TempDir(), []SpaceMirror{{SpaceID: "sp1", Dir: fx.dir}}, time.Now, 0)
	summaries, err := store.ClassificationSummaries(context.Background())
	if err != nil {
		t.Fatalf("ClassificationSummaries: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries = %+v, want one", summaries)
	}
	got := summaries[0]
	if got.Highest != "restricted" || got.DecodedCount != 3 || got.SkippedCount != 1 ||
		got.IncompleteCount != 1 || got.Complete {
		t.Fatalf("classification summary = %+v", got)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].Path != badRel || got.Skipped[0].Reason != SkipReasonNotFrontmatterShaped {
		t.Fatalf("classification skipped paths = %+v", got.Skipped)
	}
	if len(got.IncompletePaths) != 1 || got.IncompletePaths[0] != "axon/exchanges/XQ-unknown.md" {
		t.Fatalf("classification incomplete paths = %+v", got.IncompletePaths)
	}
}

func TestEventSummaryJSONOmitsAbsentReceiptEvidence(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(EventSummary{ULID: "01", Subject: "XQ", Transition: fold.TNote})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(raw)
	for _, absent := range []string{"claimed_state", "actor_model", "actor_session", "produced_by", "consistency"} {
		if strings.Contains(got, `"`+absent+`"`) {
			t.Fatalf("absent field %q emitted in %s", absent, got)
		}
	}
}

func assertReceiptMismatch(t *testing.T, got *ReceiptMismatch, ulid, subject, claimed, actual string) {
	t.Helper()
	if got == nil || got.Kind != string(fold.FlagStateClaimMismatch) || got.EventULID != ulid ||
		got.Subject != subject || got.Scope.Kind != string(fold.EvaluationScopePrimary) ||
		got.Scope.Subject != subject || got.Claimed != claimed || got.Actual != actual || got.Cause != "unknown" {
		t.Fatalf("receipt mismatch = %+v", got)
	}
}
