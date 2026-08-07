package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
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
		if !strings.Contains(string(fw.Content), "state: acknowledged") {
			t.Fatalf("expected an evaluator-authored acknowledged receipt, got:\n%s", fw.Content)
		}
	}
}

func TestLifecycleAnnouncementAckOmitsReceiptState(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XA-axon-20260721-a007"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", "---\n"+
		"schema: envelope/v1\n"+
		"id: "+id+"\n"+
		"type: announcement\n"+
		"space: fixture-space\n"+
		"from: axon\n"+
		"to: [beta]\n"+
		"actor: {kind: agent, name: bot}\n"+
		"---\nbody\n")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")

	fake := &fakeFunnel{}
	handler := newLifecycleHandler(LifecycleVerbTable[0], testWriteDeps(mirrorDir, fake))
	args, _ := json.Marshal(LifecycleInput{IDs: []string{id}})
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("ack announcement: %v", err)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 1 {
		t.Fatalf("expected one ack event, got %+v", fake.calls)
	}
	content := string(fake.calls[0].Files[0].Content)
	if strings.Contains(content, "state:") {
		t.Fatalf("broadcast acknowledgement is receipt-N/A and must omit state:\n%s", content)
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

	// `title`, not the invented `summary` this used to pass: response/v1 has
	// no `summary` field and neither does its template, so the override was
	// silently discarded — every MCP respond carrying one lost it without a
	// word. P46 W1 made applyFills refuse an override naming no template key,
	// which is what surfaced this; the test now exercises a field that exists.
	in := RespondInput{ParentIDs: []string{id}, Result: "answered", Fields: map[string]string{"title": "done"}}
	args, _ := json.Marshal(in)

	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("first respond call failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 funnel call, got %d", len(fake.calls))
	}
	for _, file := range fake.calls[0].Files {
		content := string(file.Content)
		if strings.Contains(content, "transition: respond") && strings.Contains(content, "state:") {
			t.Fatalf("respond is receipt-N/A and must omit state:\n%s", content)
		}
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

// writeQuestionArtifactWithThread mirrors mcp_testutil_test.go's own
// writeQuestionArtifact, plus a `thread:` line — mcp_testutil_test.go is
// outside this wave's allowlist, so this is a local, file-scoped variant
// rather than an edit to the shared helper.
func writeQuestionArtifactWithThread(t *testing.T, mirrorDir, id, to, thread string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"thread: " + thread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// TestRespondHandlerInheritsParentThread covers §T1 "Propagate": a derived
// artifact (here, a response) inherits its SOURCE's thread with no field
// supplied by the caller.
func TestRespondHandlerInheritsParentThread(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-f001"
	const parentThreadID = "thread:axon-20260720-wxyz"
	writeQuestionArtifactWithThread(t, mirrorDir, id, "beta", parentThreadID)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 3, id, "start", "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)

	in := RespondInput{ParentIDs: []string{id}, Result: "answered"}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 funnel call, got %d", len(fake.calls))
	}
	found := false
	for _, fw := range fake.calls[0].Files {
		if strings.Contains(fw.Path, "exchanges") && strings.Contains(string(fw.Content), "thread: "+parentThreadID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the response draft to carry the parent's thread %q, got:\n%#v", parentThreadID, fake.calls[0].Files)
	}
}

// TestRespondHandlerThreadConflictRefused covers §T1 "Explicit conflict
// refuses": an explicit `thread` field that differs from the parent's is an
// error naming both values, never a silent precedence or a guess.
func TestRespondHandlerThreadConflictRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-g001"
	const parentThreadID = "thread:axon-20260720-wxyz"
	const conflictingThreadID = "thread:beta-20260721-zzzz"
	writeQuestionArtifactWithThread(t, mirrorDir, id, "beta", parentThreadID)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 3, id, "start", "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)

	in := RespondInput{ParentIDs: []string{id}, Result: "answered", Fields: map[string]string{"thread": conflictingThreadID}}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for a thread that conflicts with the parent's")
	}
	if !strings.Contains(err.Error(), parentThreadID) || !strings.Contains(err.Error(), conflictingThreadID) {
		t.Fatalf("expected the error to name BOTH values, got: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called on a thread conflict; got %d call(s)", len(fake.calls))
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
			if !strings.Contains(c, "state: verified") {
				t.Fatalf("verify event omitted its evaluator receipt:\n%s", c)
			}
		}
		if strings.Contains(c, "transition: close") {
			sawClose = true
			if !strings.Contains(c, "state: closed") {
				t.Fatalf("close event omitted its evaluator receipt:\n%s", c)
			}
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
	if strings.Contains(string(fake.calls[0].Files[0].Content), "state:") {
		t.Fatalf("dispute is receipt-N/A and must omit state, got:\n%s", fake.calls[0].Files[0].Content)
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

func TestNoteHandlerChecksAuthorizationWithoutStatePrecondition(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-h001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	// No prior events at all — note has no legality precondition (D-025).

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	deps.OwnSystem = "beta"
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
	if strings.Contains(string(fake.calls[0].Files[0].Content), "state:") {
		t.Fatalf("note is receipt-N/A and must omit state, got:\n%s", fake.calls[0].Files[0].Content)
	}

	outsiderFake := &fakeFunnel{}
	outsiderDeps := testWriteDeps(mirrorDir, outsiderFake)
	outsiderDeps.OwnSystem = "gamma"
	_, _, err = newNoteHandler(outsiderDeps)(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "LFC-002") {
		t.Fatalf("outsider note error = %v, want LFC-002", err)
	}
	if len(outsiderFake.calls) != 0 {
		t.Fatalf("unauthorized note reached funnel: %d call(s)", len(outsiderFake.calls))
	}
}

func TestRefsFromList(t *testing.T) {
	t.Parallel()
	out := refsFromList([]string{" a ", "", "b"})
	if len(out) != 2 || out[0].Ref != "a" || out[1].Ref != "b" {
		t.Fatalf("unexpected refsFromList output: %+v", out)
	}
}

// TestGateMarkerAgreesWithFold is the same binding internal/cli draws, for
// this package's own copy of the verb table: the "always G3-gated" fact
// lives in fold.HumanGate, and this asserts this table cannot drift from it.
// See internal/cli/cmd_lifecycle_test.go's own doc for why three copies of
// one rule is what made spec 11 §18e/J3 possible.
func TestGateMarkerAgreesWithFold(t *testing.T) {
	t.Parallel()

	gated := 0
	for _, spec := range LifecycleVerbTable {
		want := fold.HumanGate(spec.Transition) != ""
		if want {
			gated++
		}
		if spec.GateMarker != want {
			t.Errorf("verb %q (transition %q): GateMarker=%v, fold.HumanGate=%q — the two homes of one rule disagree",
				spec.Verb, spec.Transition, spec.GateMarker, fold.HumanGate(spec.Transition))
		}
	}
	if gated == 0 {
		t.Fatal("no verb in the table is human-gated according to fold — the registry lost its rows and a green result here would mean nothing")
	}
}
