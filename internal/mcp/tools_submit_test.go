package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
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
