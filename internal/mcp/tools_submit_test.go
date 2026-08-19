package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// writeStagedDraft writes a minimal staged draft under stagingDir/<id>.md.
func writeStagedDraft(t *testing.T, stagingDir, id, envType string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: " + envType + "\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitHandlerFreshArtifact(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-a001"
	writeStagedDraft(t, staging, id, "question")
	writeMirrorFile(t, mirrorDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")

	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	deps := SubmitDeps{WriteDeps: write, StagingDir: staging, Legality: legality}
	handler := newSubmitHandler(deps)

	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("submit handler failed: %v", err)
	}
	sr, ok := result.(submitResult)
	if !ok || sr.Verb != "submit" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 2 {
		t.Fatalf("expected 1 funnel call with 2 files (draft+event), got %+v", fake.calls)
	}
	if !strings.Contains(string(fake.calls[0].Files[1].Content), "transition: submit") {
		t.Fatalf("expected a submit-transition entry event, got:\n%s", fake.calls[0].Files[1].Content)
	}
	if !strings.Contains(string(fake.calls[0].Files[1].Content), "state: submitted") {
		t.Fatalf("expected an evaluator-authored submitted receipt, got:\n%s", fake.calls[0].Files[1].Content)
	}
}

func TestSubmitHandlerPreservesPartialWriteResultOnError(t *testing.T) {
	t.Parallel()

	mirrorDir := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-a009"
	writeStagedDraft(t, staging, id, "question")
	writeMirrorFile(t, mirrorDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	wantErr := errors.New("provider outcome is partial")
	partial := space.WriteResult{
		Branch: "a2a/beta/submit/op-v1-partial", PRNumber: 77,
		PRURL: "https://example.invalid/pr/77", CommitSHA: "abc123",
		Stage: space.WriteStagePRCreated, State: space.WriteStateNeedsAttention,
		RemainingAction: space.RemainingActionResolvePR, Note: "auto-merge is not armed",
		ArtifactIDs: []string{id},
	}
	write := testWriteDeps(mirrorDir, partialResultFunnel{result: partial, err: wantErr})
	handler := newSubmitHandler(SubmitDeps{
		WriteDeps: write, StagingDir: staging,
		Legality: NewLegalityAdapter(mirrorDir, "beta", testManifest()),
	})
	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	result, _, err := handler(context.Background(), args)
	if !errors.Is(err, wantErr) {
		t.Fatalf("handler error = %v, want %v", err, wantErr)
	}
	got, ok := result.(submitResult)
	if !ok || got.PRNumber != partial.PRNumber || got.Stage != string(partial.Stage) ||
		got.RemainingAction != string(partial.RemainingAction) || got.Note != partial.Note {
		t.Fatalf("handler discarded partial result: %#v", result)
	}
}

func TestSubmitHandlerContractCarriesTheNewScaffold(t *testing.T) {
	t.Parallel()

	mirrorDir := t.TempDir()
	staging := t.TempDir()
	newHandler := newNewHandler(testNewDeps(staging))
	newArgs, _ := json.Marshal(NewInput{Items: []NewItem{{
		Type: "contract",
		Slug: "widget-submit",
		Fields: map[string]string{
			"title": "widget contract", "space": "fixture-space",
			"to": "[axon]", "category": "other",
		},
	}}})
	result, _, err := newHandler(context.Background(), newArgs)
	if err != nil {
		t.Fatalf("a2a_new contract: %v", err)
	}
	drafts := result.([]newDraftResult)
	if len(drafts) != 5 {
		t.Fatalf("a2a_new returned %d paths, want flat draft + complete four-file candidate", len(drafts))
	}
	id := drafts[0].ID

	writeMirrorFile(t, mirrorDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	handler := newSubmitHandler(SubmitDeps{WriteDeps: write, StagingDir: staging, Legality: legality})

	submitArgs, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	if _, _, err := handler(context.Background(), submitArgs); err != nil {
		t.Fatalf("a2a_submit contract: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("funnel calls = %d, want 1", len(fake.calls))
	}

	got := map[string]bool{}
	for _, file := range fake.calls[0].Files {
		got[file.Path] = true
	}
	for _, want := range []string{
		"beta/provides/widget-submit/contract.md",
		"beta/provides/widget-submit/schema/widget-submit.schema.json",
		"beta/provides/widget-submit/fixtures/valid/widget-submit.json",
		"beta/provides/widget-submit/fixtures/invalid/widget-submit.json",
	} {
		if !got[want] {
			t.Fatalf("submit omitted %s; carried paths: %v", want, got)
		}
	}
	if len(fake.calls[0].Files) != 5 {
		t.Fatalf("submit carried %d files, want descriptor + 3 sidecars + publish event", len(fake.calls[0].Files))
	}
}

// TestSubmitWriterReceiptMatrix covers every entry-event constructor branch.
// The expected values are outcomes returned by fold.EvaluateCandidate; the
// writer merely projects them into event/v1 and never accepts a caller state.
func TestSubmitWriterReceiptMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		envType string
		id      string
		state   string
	}{
		{envType: "contract", id: "XC-beta-widget", state: "published"},
		{envType: "requirement", id: "XR-beta-req1", state: "published"},
		{envType: "question", id: "XQ-beta-20260721-a001", state: "submitted"},
		{envType: "work_request", id: "XW-beta-20260721-a002", state: "submitted"},
		{envType: "decision", id: "XD-beta-20260721-a003", state: "proposed"},
		{envType: "handoff", id: "XH-beta-20260721-a004", state: "submitted"},
		{envType: "response", id: "XS-beta-20260721-a005", state: "submitted"},
		{envType: "announcement", id: "XA-beta-20260721-a006", state: "published"},
	}

	mirrorDir := t.TempDir()
	writeMirrorFile(t, mirrorDir, "space.yaml", "min_binary_version: 0.0.0\n")
	deps := SubmitDeps{WriteDeps: testWriteDeps(mirrorDir, &fakeFunnel{}), StagingDir: t.TempDir()}
	items := make([]submitItem, 0, len(cases))
	want := make(map[string]string, len(cases))
	for _, tc := range cases {
		var env submitEnvelopeInfo
		env.ID = tc.id
		env.Type = tc.envType
		env.From = "beta"
		env.To = []string{"axon"}
		env.Space = "fixture-space"
		env.Actor.Kind = "agent"
		env.Actor.Name = "bot"
		items = append(items, submitItem{path: tc.id + ".md", raw: []byte("draft"), env: env})
		want[tc.id] = tc.state
	}

	req, _, err := buildSubmitRequest(deps, items)
	if err != nil {
		t.Fatalf("buildSubmitRequest: %v", err)
	}
	seen := map[string]bool{}
	for _, file := range req.Files {
		if !strings.Contains(file.Path, "/events/") {
			continue
		}
		var event eventDoc
		if err := yaml.Unmarshal(file.Content, &event); err != nil {
			t.Fatalf("decode %s: %v", file.Path, err)
		}
		if event.State != want[event.Subject] {
			t.Errorf("%s entry receipt = %q, want %q", event.Subject, event.State, want[event.Subject])
		}
		seen[event.Subject] = true
	}
	if len(seen) != len(cases) {
		t.Fatalf("saw receipts for %d subjects, want %d: %v", len(seen), len(cases), seen)
	}
}

func TestSubmitHandlerForeignSectionRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	staging := t.TempDir()
	id := "XQ-axon-20260721-b001"
	// from: axon, but our own system is beta — foreign-section refusal.
	content := "---\nschema: envelope/v1\nid: " + id + "\ntype: question\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n"
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	deps := SubmitDeps{WriteDeps: write, StagingDir: staging, Legality: legality}
	handler := newSubmitHandler(deps)

	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "CC-002") {
		t.Fatalf("expected a CC-002 foreign-section refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fake.calls))
	}
}

func TestSubmitHandlerIdempotentAlreadySubmitted(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-c001"
	writeStagedDraft(t, staging, id, "question")
	writeMirrorFile(t, mirrorDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	writeLifecycleEvent(t, mirrorDir, "beta", 0, id, "submit", "beta")

	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	deps := SubmitDeps{WriteDeps: write, StagingDir: staging, Legality: legality}
	handler := newSubmitHandler(deps)

	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("submit handler failed: %v", err)
	}
	sr := result.(submitResult)
	if sr.State != "already-submitted" {
		t.Fatalf("State = %q, want already-submitted", sr.State)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called on an already-submitted id, got %d calls", len(fake.calls))
	}
}

// TestSubmitSectionPath exercises every §4.2 layout branch
// submitSectionPath resolves — pure, no git fixture, cheap coverage
// margin independent of any fixture flake.
func TestSubmitSectionPath(t *testing.T) {
	t.Parallel()
	layout, err := space.NewLayout("beta")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}

	cases := []struct {
		envType string
		id      string
		want    string
		wantErr bool
	}{
		{envType: "contract", id: "XC-beta-widget", want: "beta/provides/widget/contract.md"},
		{envType: "requirement", id: "XR-beta-req1", want: "beta/requires/XR-beta-req1.md"},
		{envType: "decision", id: "XD-beta-20260721-a001", want: "decisions/XD-beta-20260721-a001.md"},
		{envType: "question", id: "XQ-beta-20260721-a001", want: "beta/exchanges/XQ-beta-20260721-a001.md"},
		{envType: "work_request", id: "XW-beta-20260721-a001", want: "beta/exchanges/XW-beta-20260721-a001.md"},
		{envType: "handoff", id: "XH-beta-20260721-a001", want: "beta/exchanges/XH-beta-20260721-a001.md"},
		{envType: "response", id: "XS-beta-20260721-a001", want: "beta/exchanges/XS-beta-20260721-a001.md"},
		{envType: "announcement", id: "XA-beta-20260721-a001", want: "beta/exchanges/XA-beta-20260721-a001.md"},
		{envType: "bogus", id: "X-beta-x", wantErr: true},
		{envType: "contract", id: "not-a-valid-id", wantErr: true},
	}
	for _, tc := range cases {
		got, err := submitSectionPath(layout, tc.envType, tc.id)
		if tc.wantErr {
			if err == nil {
				t.Errorf("submitSectionPath(%q, %q): expected an error", tc.envType, tc.id)
			}
			continue
		}
		if err != nil {
			t.Errorf("submitSectionPath(%q, %q): unexpected error: %v", tc.envType, tc.id, err)
			continue
		}
		if got != tc.want {
			t.Errorf("submitSectionPath(%q, %q) = %q, want %q", tc.envType, tc.id, got, tc.want)
		}
	}
}

// --- resolveSubmitSpace / SubmitDeps.ResolveSpace ------------------------

func writeStagedDraftForSpace(t *testing.T, stagingDir, id, envType, spaceID string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: " + envType + "\n" +
		"title: t\n" +
		"space: " + spaceID + "\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testSubmitDeps(mirrorDir, stagingDir string, funnel Funnel) SubmitDeps {
	return SubmitDeps{
		WriteDeps: testWriteDeps(mirrorDir, funnel), StagingDir: stagingDir,
		Legality: NewLegalityAdapter(mirrorDir, "beta", testManifest()),
	}
}

func TestResolveSubmitSpaceNilResolverReturnsUnchanged(t *testing.T) {
	t.Parallel()
	deps := SubmitDeps{StagingDir: "/single/staging"}
	got, err := resolveSubmitSpace(deps, "space-a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StagingDir != deps.StagingDir {
		t.Fatalf("resolveSubmitSpace(nil resolver) = %+v, want unchanged %+v", got, deps)
	}
}

func TestResolveSubmitSpaceExplicit(t *testing.T) {
	t.Parallel()
	want := SubmitDeps{StagingDir: "/space-b/staging"}
	deps := SubmitDeps{
		ResolveSpace: func(spaceID string) (SubmitDeps, error) {
			if spaceID != "space-b" {
				t.Fatalf("ResolveSpace called with %q, want %q", spaceID, "space-b")
			}
			return want, nil
		},
	}
	got, err := resolveSubmitSpace(deps, "space-b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.StagingDir != want.StagingDir {
		t.Fatalf("resolveSubmitSpace(explicit) = %+v, want %+v", got, want)
	}
}

func TestResolveSubmitSpacePlaceholderRefusedBeforeCallingResolveSpace(t *testing.T) {
	t.Parallel()
	deps := SubmitDeps{
		ResolveSpace: func(spaceID string) (SubmitDeps, error) {
			t.Fatalf("ResolveSpace must not be called for an unfilled placeholder; got %q", spaceID)
			return SubmitDeps{}, nil
		},
	}
	_, err := resolveSubmitSpace(deps, "<space-id>")
	if err == nil || !strings.Contains(err.Error(), "never filled in") {
		t.Fatalf("resolveSubmitSpace(placeholder) error = %v, want it to mention the unfilled placeholder", err)
	}
}

// TestSubmitHandlerResolvesToTheNamedSpace: a draft whose `space:` names the
// SECOND connected space lands there — the funnel it calls, and only it,
// must be the resolved space's.
func TestSubmitHandlerResolvesToTheNamedSpace(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-r001"
	writeStagedDraftForSpace(t, staging, id, "question", "space-b")
	writeMirrorFile(t, mirrorA, "space.yaml", "id: space-a\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	writeMirrorFile(t, mirrorB, "space.yaml", "id: space-b\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	primary := testSubmitDeps(mirrorA, staging, fakeA)
	resolvedB := testSubmitDeps(mirrorB, staging, fakeB)
	primary.ResolveSpace = func(spaceID string) (SubmitDeps, error) {
		if spaceID != "space-b" {
			t.Fatalf("ResolveSpace called with %q, want %q", spaceID, "space-b")
		}
		return resolvedB, nil
	}

	handler := newSubmitHandler(primary)
	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("submit handler failed: %v", err)
	}
	sr, ok := result.(submitResult)
	if !ok || sr.Verb != "submit" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(fakeA.calls) != 0 {
		t.Fatalf("expected NO calls against the primary (unresolved) space's funnel, got %d", len(fakeA.calls))
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("expected exactly 1 call against the resolved space-b funnel, got %d", len(fakeB.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("RepoDir = %q, want the resolved space's own mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestSubmitHandlerUsesTheResolvedSpacesLegality is the trap this brief
// names explicitly: SubmitDeps.Legality is mirror-bound. A handler that
// resolved only the promoted WriteDeps.ResolveSpace (or otherwise kept the
// PRIMARY space's Legality) would judge this id against space-a's committed
// history — which already carries a submit event for it — and short-circuit
// as already-submitted without ever reaching the resolved space's funnel.
func TestSubmitHandlerUsesTheResolvedSpacesLegality(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-r002"
	writeStagedDraftForSpace(t, staging, id, "question", "space-b")
	writeMirrorFile(t, mirrorA, "space.yaml", "id: space-a\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	writeMirrorFile(t, mirrorB, "space.yaml", "id: space-b\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	// Seeded ONLY in space-a's mirror — the primary/unresolved space.
	writeLifecycleEvent(t, mirrorA, "beta", 0, id, "submit", "beta")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	primary := testSubmitDeps(mirrorA, staging, fakeA)
	resolvedB := testSubmitDeps(mirrorB, staging, fakeB)
	primary.ResolveSpace = func(spaceID string) (SubmitDeps, error) { return resolvedB, nil }

	handler := newSubmitHandler(primary)
	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("submit handler failed: %v", err)
	}
	sr, ok := result.(submitResult)
	if !ok {
		t.Fatalf("unexpected result: %#v", result)
	}
	if sr.State == "already-submitted" {
		t.Fatalf("handler judged legality against the PRIMARY (unresolved) space's committed history; got state=%q", sr.State)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("expected the resolved space's funnel to be called exactly once, got %d", len(fakeB.calls))
	}
}

// TestSubmitHandlerDoesNotPoisonAcrossCalls: two sequential submits through
// ONE constructed handler naming different spaces. If the handler ever
// assigned to the captured `deps` (rather than shadowing a local), the
// SECOND call would inherit the first call's already-resolved (and
// ResolveSpace == nil) deps and silently land in the wrong space.
func TestSubmitHandlerDoesNotPoisonAcrossCalls(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	mirrorC := t.TempDir()
	staging := t.TempDir()
	idB := "XQ-beta-20260721-p001"
	idC := "XQ-beta-20260721-p002"
	writeStagedDraftForSpace(t, staging, idB, "question", "space-b")
	writeStagedDraftForSpace(t, staging, idC, "question", "space-c")
	for _, m := range []string{mirrorA, mirrorB, mirrorC} {
		writeMirrorFile(t, m, "space.yaml", "schema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")
	}

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	fakeC := &fakeFunnel{}
	primary := testSubmitDeps(mirrorA, staging, fakeA)
	resolvedB := testSubmitDeps(mirrorB, staging, fakeB)
	resolvedC := testSubmitDeps(mirrorC, staging, fakeC)
	primary.ResolveSpace = func(spaceID string) (SubmitDeps, error) {
		switch spaceID {
		case "space-b":
			return resolvedB, nil
		case "space-c":
			return resolvedC, nil
		default:
			t.Fatalf("ResolveSpace called with unexpected id %q", spaceID)
			return SubmitDeps{}, nil
		}
	}
	handler := newSubmitHandler(primary)

	argsB, _ := json.Marshal(SubmitInput{IDs: []string{idB}})
	if _, _, err := handler(context.Background(), argsB); err != nil {
		t.Fatalf("first call (space-b) failed: %v", err)
	}
	argsC, _ := json.Marshal(SubmitInput{IDs: []string{idC}})
	if _, _, err := handler(context.Background(), argsC); err != nil {
		t.Fatalf("second call (space-c) failed: %v", err)
	}

	if len(fakeA.calls) != 0 {
		t.Fatalf("primary (never-named) space funnel calls = %d, want 0", len(fakeA.calls))
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("space-b funnel calls = %d, want 1 — got poisoned by the other call", len(fakeB.calls))
	}
	if len(fakeC.calls) != 1 {
		t.Fatalf("space-c funnel calls = %d, want 1 — got poisoned by the other call", len(fakeC.calls))
	}
}

func TestSubmitHandlerRefusesUnconnectedSpaceByName(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-u001"
	writeStagedDraftForSpace(t, staging, id, "question", "ghost-space")
	writeMirrorFile(t, mirrorA, "space.yaml", "id: space-a\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")

	fakeA := &fakeFunnel{}
	primary := testSubmitDeps(mirrorA, staging, fakeA)
	primary.ResolveSpace = func(spaceID string) (SubmitDeps, error) {
		return SubmitDeps{}, fmt.Errorf("mcp: space %q is not connected; connected spaces are space-a, space-b", spaceID)
	}
	handler := newSubmitHandler(primary)

	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "ghost-space") || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("expected a by-name refusal naming the unconnected space, got %v", err)
	}
	if len(fakeA.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fakeA.calls))
	}
}

func TestSubmitHandlerRefusesUnfilledSpacePlaceholder(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-ph001"
	writeStagedDraftForSpace(t, staging, id, "question", "<space-id>")
	writeMirrorFile(t, mirrorA, "space.yaml", "id: space-a\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")

	fakeA := &fakeFunnel{}
	primary := testSubmitDeps(mirrorA, staging, fakeA)
	primary.ResolveSpace = func(spaceID string) (SubmitDeps, error) {
		t.Fatalf("ResolveSpace must not be called for an unfilled placeholder; got %q", spaceID)
		return SubmitDeps{}, nil
	}
	handler := newSubmitHandler(primary)

	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "never filled in") {
		t.Fatalf("expected the unfilled-placeholder refusal, got %v", err)
	}
	if len(fakeA.calls) != 0 {
		t.Fatalf("expected the funnel NEVER to be called, got %d calls", len(fakeA.calls))
	}
}

// TestSubmitHandlerSingleSpaceSessionUnchanged: SubmitDeps.ResolveSpace nil
// (the single-space wiring) never touches the space named on the draft —
// today's byte-identical behavior.
func TestSubmitHandlerSingleSpaceSessionUnchanged(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	staging := t.TempDir()
	id := "XQ-beta-20260721-s001"
	// Names a space this single-space session never connected — must be
	// ignored entirely, since deps.ResolveSpace is nil.
	writeStagedDraftForSpace(t, staging, id, "question", "some-other-space")
	writeMirrorFile(t, mirrorDir, "space.yaml", "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n  axon-bot: axon\n  beta-bot: beta\n")

	fake := &fakeFunnel{}
	deps := testSubmitDeps(mirrorDir, staging, fake)
	if deps.ResolveSpace != nil {
		t.Fatalf("testSubmitDeps must not set ResolveSpace by default")
	}
	handler := newSubmitHandler(deps)

	args, _ := json.Marshal(SubmitInput{IDs: []string{id}})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("submit handler failed: %v", err)
	}
	sr, ok := result.(submitResult)
	if !ok || sr.Verb != "submit" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly 1 funnel call against the single connected mirror, got %d", len(fake.calls))
	}
}

func TestSubmitHandlerEmptyIDs(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	write := testWriteDeps(t.TempDir(), fake)
	legality := NewLegalityAdapter(t.TempDir(), "beta", testManifest())
	deps := SubmitDeps{WriteDeps: write, StagingDir: t.TempDir(), Legality: legality}
	handler := newSubmitHandler(deps)

	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sr := result.(submitResult)
	if sr.State != "nothing-to-submit" {
		t.Fatalf("State = %q, want nothing-to-submit", sr.State)
	}
}
