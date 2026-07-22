package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleHandlerAckLegalBatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	ids := []string{"XQ-axon-20260721-a001", "XQ-axon-20260721-a002"}
	for i, id := range ids {
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", i, id, "submit", "axon")
	}

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps) // ack

	args, _ := json.Marshal(LifecycleInput{IDs: ids})
	result, body, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if body != "" {
		t.Fatalf("expected no body for a write tool, got %q", body)
	}
	sr, ok := result.(submitResult)
	if !ok {
		t.Fatalf("expected submitResult, got %T", result)
	}
	if sr.Verb != "ack" {
		t.Fatalf("Verb = %q, want ack", sr.Verb)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly ONE funnel call (batch = one commit), got %d", len(fake.calls))
	}
	if len(fake.calls[0].Files) != 2 {
		t.Fatalf("expected 2 event files in the one commit, got %d", len(fake.calls[0].Files))
	}
	for _, fw := range fake.calls[0].Files {
		if !strings.Contains(string(fw.Content), "transition: acknowledge") {
			t.Fatalf("expected an acknowledge event, got:\n%s", fw.Content)
		}
	}
}

func TestLifecycleHandlerIllegalTransitionRefusedLocally(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-b001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps) // ack

	args, _ := json.Marshal(LifecycleInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal (already acknowledged)")
	}
	if !strings.Contains(err.Error(), "LFC-001") {
		t.Fatalf("expected the refusal to name LFC-001; got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

func TestLifecycleHandlerMissingIDs(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	deps := testWriteDeps(t.TempDir(), fake)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps)
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for missing ids")
	}
}

func TestLifecycleHandlerRequireReason(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-c001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	// decline is index 2 in LifecycleVerbTable and RequireReason=true.
	handler := newLifecycleHandler(LifecycleVerbTable[2], deps)

	args, _ := json.Marshal(LifecycleInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("expected a reason-required error, got %v", err)
	}
}

func TestRespondHandlerDeterministicResponseID(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-d001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 3, id, "start", "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)

	in := RespondInput{ParentIDs: []string{id}, Result: "answered", Fields: map[string]string{"summary": "done"}}
	args, _ := json.Marshal(in)

	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("first respond call failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 funnel call, got %d", len(fake.calls))
	}
	firstFileCount := len(fake.calls[0].Files)

	// A second, identical respond call (fresh fake funnel, same fixture)
	// must mint the SAME response id (content-derived seed).
	fake2 := &fakeFunnel{}
	deps2 := testWriteDeps(mirrorDir, fake2)
	handler2 := newRespondHandler(deps2)
	_, _, err = handler2(context.Background(), args)
	if err != nil {
		t.Fatalf("second respond call failed: %v", err)
	}
	if len(fake2.calls) != 1 || len(fake2.calls[0].Files) != firstFileCount {
		t.Fatalf("expected the same file shape on retry")
	}
	if fake.calls[0].ArtifactID != fake2.calls[0].ArtifactID {
		t.Fatalf("expected the same deterministic ArtifactID on retry: %q vs %q", fake.calls[0].ArtifactID, fake2.calls[0].ArtifactID)
	}
}

func TestRespondHandlerInvalidResult(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	deps := testWriteDeps(t.TempDir(), fake)
	handler := newRespondHandler(deps)
	in := RespondInput{ParentIDs: []string{"XQ-axon-20260721-e001"}, Result: "bogus"}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for an invalid result value")
	}
}

func TestVerifyHandlerSingleResponseAutoCloses(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-f001"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")

	// respond first (own funnel, own materialization), THEN verify — same
	// idiom as internal/cli's respondFlow helper.
	respondFake := &fakeFunnel{}
	respondDeps := testWriteDeps(mirrorDir, respondFake)
	respondHandler := newRespondHandler(respondDeps)
	respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{parentID}, Result: "answered"})
	if _, _, err := respondHandler(context.Background(), respondArgs); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if len(respondFake.calls) != 1 {
		t.Fatalf("expected 1 respond funnel call, got %d", len(respondFake.calls))
	}
	for _, fw := range respondFake.calls[0].Files {
		full := filepath.Join(mirrorDir, fw.Path)
		if err := writeFileAllDirs(full, fw.Content); err != nil {
			t.Fatalf("materialize %s: %v", fw.Path, err)
		}
	}
	var responseID string
	for _, fw := range respondFake.calls[0].Files {
		base := filepath.Base(fw.Path)
		if strings.HasPrefix(base, "XS-") {
			responseID = strings.TrimSuffix(base, ".md")
		}
	}
	if responseID == "" {
		t.Fatalf("could not find minted response id in %+v", respondFake.calls[0].Files)
	}

	// verify's role is RoleOwner (the parent's original requester, axon —
	// NOT beta, the responder) per fold's own responseRows table.
	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon"
	handler := newVerifyHandler(deps)

	in := VerifyInput{Targets: []string{responseID}}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("verify handler failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 funnel call, got %d", len(fake.calls))
	}
	if len(fake.calls[0].Files) != 2 {
		t.Fatalf("expected 2 events (verify + convenience close), got %d: %+v", len(fake.calls[0].Files), fake.calls[0].Files)
	}
	var sawVerify, sawClose bool
	for _, fw := range fake.calls[0].Files {
		c := string(fw.Content)
		if strings.Contains(c, "transition: verify") {
			sawVerify = true
		}
		if strings.Contains(c, "transition: close") {
			sawClose = true
		}
	}
	if !sawVerify || !sawClose {
		t.Fatalf("expected both a verify and a close event; got:\n%v", fake.calls[0].Files)
	}
}

func TestDisputeHandlerLegal(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-m001"
	writeQuestionArtifact(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")

	respondFake := &fakeFunnel{}
	respondDeps := testWriteDeps(mirrorDir, respondFake)
	respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{parentID}, Result: "answered"})
	if _, _, err := newRespondHandler(respondDeps)(context.Background(), respondArgs); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	for _, fw := range respondFake.calls[0].Files {
		if err := writeFileAllDirs(filepath.Join(mirrorDir, fw.Path), fw.Content); err != nil {
			t.Fatalf("materialize: %v", err)
		}
	}
	var responseID string
	for _, fw := range respondFake.calls[0].Files {
		base := filepath.Base(fw.Path)
		if strings.HasPrefix(base, "XS-") {
			responseID = strings.TrimSuffix(base, ".md")
		}
	}

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "axon" // dispute's role is RoleOwner (the parent's original requester)
	handler := newDisputeHandler(deps)
	args, _ := json.Marshal(DisputeInput{IDs: []string{responseID}, Reason: "wrong answer"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("dispute failed: %v", err)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 1 {
		t.Fatalf("expected exactly one commit with exactly one event file, got %+v", fake.calls)
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "transition: dispute") {
		t.Fatalf("expected a dispute event, got:\n%s", fake.calls[0].Files[0].Content)
	}
}

func TestDisputeHandlerMissingReason(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	deps := testWriteDeps(t.TempDir(), fake)
	handler := newDisputeHandler(deps)
	in := DisputeInput{IDs: []string{"XS-beta-20260721-g001"}}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for a missing reason")
	}
}

func TestNoteHandlerSkipsLegalityCheck(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-h001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	// No prior events at all — note has no legality precondition (D-025).

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newNoteHandler(deps)

	in := NoteInput{IDs: []string{id}, Note: "fyi"}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("note handler failed: %v", err)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 1 {
		t.Fatalf("expected exactly one event file committed")
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "note: fyi") {
		t.Fatalf("expected the note text in the event, got:\n%s", fake.calls[0].Files[0].Content)
	}
}

func TestRefsFromList(t *testing.T) {
	t.Parallel()
	out := refsFromList([]string{" a ", "", "b"})
	if len(out) != 2 || out[0].Ref != "a" || out[1].Ref != "b" {
		t.Fatalf("unexpected refsFromList output: %+v", out)
	}
}
