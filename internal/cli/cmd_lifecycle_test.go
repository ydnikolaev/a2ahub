package cli_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// fakeLifecycleFunnel is a hand-written test double for cmd_lifecycle.go's
// own unexported lifecycleFunnel seam (structural typing) — used by every
// test that must prove either the funnel IS called exactly once (batch/
// success) or is NEVER called (local legality refusal).
type fakeLifecycleFunnel struct {
	calls  []space.SubmitRequest
	result space.WriteResult
	err    error
}

func (f *fakeLifecycleFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return space.WriteResult{}, f.err
	}
	if f.result.State == "" {
		return space.WriteResult{State: space.WriteStatePendingMerge, PRNumber: len(f.calls), PRURL: "https://example.invalid/pr/x", Branch: req.ArtifactID}, nil
	}
	return f.result, nil
}

// materializeFiles writes every FileWrite of req onto disk under
// mirrorDir — a fake funnel records the call but never touches disk, so a
// test chaining two commands (e.g. respond then verify) must persist the
// first command's output itself, exactly as a real commit would.
func materializeFiles(t *testing.T, mirrorDir string, req space.SubmitRequest) {
	t.Helper()
	for _, fw := range req.Files {
		full := filepath.Join(mirrorDir, fw.Path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("materializeFiles: mkdir: %v", err)
		}
		if err := os.WriteFile(full, fw.Content, 0o644); err != nil {
			t.Fatalf("materializeFiles: write %s: %v", full, err)
		}
	}
}

// lifecycleActorResolver is a fixed-identity resolveActor func — every
// lifecycle command needs one injected (§7.4 seam); tests never exercise
// ResolveActor's own env/harness/config fallback chain (P6's own
// coverage), just a stable actor.kind/name for state-fold assertions.
func lifecycleActorResolver(kind, name string) func(cli.ActorFlags) (template.Actor, error) {
	return func(cli.ActorFlags) (template.Actor, error) { return template.Actor{Kind: kind, Name: name}, nil }
}

func lifecycleManifest() space.Manifest {
	return space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "beta", Status: "active"},
		{System: "gamma", Status: "active"},
	}}
}

func lifecycleHostConfig() cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL: "https://example.invalid/org/space.git", Repo: host.Repo{Owner: "org", Name: "space"},
		BaseBranch: "main", Credential: host.Credential{Token: "test-token"},
		CommitAuthorName: "a2a-beta", CommitAuthorEmail: "a2a-beta@a2ahub.invalid",
	}
}

// writeMirrorFile writes content at mirrorDir/relPath, creating parent
// directories as needed.
func writeMirrorFile(t *testing.T, mirrorDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(mirrorDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("writeMirrorFile: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("writeMirrorFile: write %s: %v", full, err)
	}
}

// writeQuestionArtifact seeds a committed `question` exchange (§4.2) under
// axon's own section, from axon to `to`. It carries a well-formed `thread:`
// (spec 46 §T1 R1: every real artifact `a2a new` drafts mints one) so
// `respond`'s R2 propagation (cmd_lifecycle.go) has a real value to inherit.
func writeQuestionArtifact(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	writeQuestionArtifactWithThread(t, mirrorDir, id, to, "thread:axon-20260721-t9a1")
}

// writeQuestionArtifactWithThread is writeQuestionArtifact's own
// parameterized form — thread == "" omits the `thread:` key entirely
// (the pre-spec-46 / legacy-fixture shape), used by
// TestRespondThreadIsNotInTheResponseIDSeed (R5) to prove the parent's
// thread value never changes the minted responseID.
func writeQuestionArtifactWithThread(t *testing.T, mirrorDir, id, to, thread string) {
	t.Helper()
	threadLine := ""
	if thread != "" {
		threadLine = "thread: " + thread + "\n"
	}
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		threadLine +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// writeDecisionArtifact seeds a committed `decision` under axon's own
// section, authored by axon, requiring approvals from every id in
// approvers.
func writeDecisionArtifact(t *testing.T, mirrorDir, id string, approvers []string) {
	t.Helper()
	quoted := make([]string, len(approvers))
	copy(quoted, approvers)
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: decision\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + strings.Join(quoted, ", ") + "]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"required_approvers: [" + strings.Join(quoted, ", ") + "]\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "decisions/"+id+".md", content)
}

// writeLifecycleEvent seeds a pre-existing committed event under
// actingSystem's own section, at a caller-supplied sequence number: this
// file's own commands sort committed events by ULID string (fold's own
// fallback ordering, inherited from adapters.go's precedent), so seq is
// minted as a REAL ULID at a fixed 2020 baseline (seq seconds apart) —
// strictly earlier, and correctly ORDERED among each other, relative to
// any event a command under test mints at real wall-clock "now" (2026+).
// A plain string id would not sort correctly against a real ULID's
// Crockford-base32 timestamp prefix.
func writeLifecycleEvent(t *testing.T, mirrorDir, actingSystem string, seq int, subject, transition, actorSystem string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 0, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("writeLifecycleEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:00:00Z\n",
		id.String(), subject, transition, actorSystem,
	)
	writeMirrorFile(t, mirrorDir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
}

// TestAckLegalTransitionAndBatch is AC-302.1 (legal path) + P8-1 (batch
// triage): 3 submitted questions -> `a2a ack` by the target system ->
// exactly one funnel call carrying 3 event files.
func TestAckLegalTransitionAndBatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	ids := []string{"XQ-axon-20260721-a001", "XQ-axon-20260721-a002", "XQ-axon-20260721-a003"}
	for i, id := range ids {
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", i, id, "submit", "axon")
	}

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), ids, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly ONE funnel call (batch = one commit/one PR), got %d", len(fake.calls))
	}
	if len(fake.calls[0].Files) != 3 {
		t.Fatalf("expected exactly 3 event files in the one commit, got %d", len(fake.calls[0].Files))
	}
	for _, fw := range fake.calls[0].Files {
		if !strings.Contains(string(fw.Content), "transition: acknowledge") {
			t.Fatalf("expected an acknowledge event, got:\n%s", fw.Content)
		}
	}
}

// TestAckIllegalTransitionRefusedLocally is AC-302.1's illegal-transition
// half: an already-closed question cannot be acknowledged again — refused
// locally, funnel NEVER called.
func TestAckIllegalTransitionRefusedLocally(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-b001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{id}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit (already acknowledged, ack is illegal from `acknowledged`)")
	}
	if !strings.Contains(errOut.String(), "LFC-001") {
		t.Fatalf("expected the refusal to name LFC-001; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestAckUnauthorizedActorRefusedLocally is AC-302.1's unauthorized-actor
// half: only the target system (`beta`) may ack; a differently-configured
// own system is refused locally.
func TestAckUnauthorizedActorRefusedLocally(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-c001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	// gamma is a member but not the addressed target.
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "gamma", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{id}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit (gamma is not the addressed target)")
	}
	if !strings.Contains(errOut.String(), "LFC-002") {
		t.Fatalf("expected the refusal to name LFC-002; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestDeclineRequiresReasonFlag is a usage-error case: `decline` without
// --reason is refused at flag-parse time (exit 2), before any legality
// check or funnel call.
func TestDeclineRequiresReasonFlag(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewDeclineCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"XQ-axon-20260721-d001"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage: --reason required)", code)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestApproveRejectAlwaysGateMarker is P8-3: approve/reject always open a
// G3-gated PR (an advisory marker in PRBody) regardless of prior state —
// the funnel call itself is uniform (same auto-merge-always shape), only
// the marker differs.
func TestApproveRejectAlwaysGateMarker(t *testing.T) {
	t.Parallel()

	t.Run("approve", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XD-axon-20260721-e001"
		writeDecisionArtifact(t, mirrorDir, id, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "propose", "axon")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewApproveCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("human", "owner"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{id}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
		if fake.calls[0].PRBody == "" {
			t.Fatal("expected approve to always carry an advisory G3 gate marker in PRBody")
		}
	})

	t.Run("reject", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XD-axon-20260721-e002"
		writeDecisionArtifact(t, mirrorDir, id, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "propose", "axon")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRejectCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("human", "owner"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--reason", "scope creep", id}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
		if fake.calls[0].PRBody == "" {
			t.Fatal("expected reject to always carry an advisory G3 gate marker in PRBody")
		}
	})
}

// respondFlow drives RespondCommand for one parent and materializes its
// output onto mirrorDir, returning the minted response id (parsed back out
// of the recorded funnel call's file paths). extraArgs is appended
// between --result and the parent id — used by tests that need two
// respond calls to the SAME parent to mint two DISTINCT response ids
// (HIGH-1 fix-wave finding: responseID is now content-derived, so two
// respond calls with otherwise-identical content mint the SAME id).
func respondFlow(t *testing.T, mirrorDir, parentID, ownSystem string, extraArgs ...string) string {
	t.Helper()
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", ownSystem, lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	args := append([]string{"--result", "answered"}, extraArgs...)
	args = append(args, parentID)
	code := cmd.Run(context.Background(), args, io)
	if code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("respond: expected exactly one funnel call, got %d", len(fake.calls))
	}
	materializeFiles(t, mirrorDir, fake.calls[0])

	var responseID string
	for _, fw := range fake.calls[0].Files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			responseID = strings.TrimSuffix(filepath.Base(fw.Path), ".md")
		}
	}
	if responseID == "" {
		t.Fatalf("respond: could not find the minted response id in %+v", fake.calls[0].Files)
	}
	return responseID
}

func seedAcceptedQuestion(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	writeQuestionArtifact(t, mirrorDir, id, to)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, to, 1, id, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, to, 2, id, "accept", to)
}

// TestVerifySingleResponseAutoCloses is the D-024 convenience: a
// single-response exchange's `verify` ALSO emits `close` on the parent in
// the same PR.
func TestVerifySingleResponseAutoCloses(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-f001"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	files := fake.calls[0].Files
	if len(files) != 2 {
		t.Fatalf("expected verify+close (2 events) in the same PR, got %d files: %+v", len(files), files)
	}
	var sawVerify, sawClose bool
	for _, fw := range files {
		c := string(fw.Content)
		if strings.Contains(c, "transition: verify") {
			sawVerify = true
		}
		if strings.Contains(c, "transition: close") {
			sawClose = true
		}
	}
	if !sawVerify || !sawClose {
		t.Fatalf("expected both a verify and a close event; got:\n%v", files)
	}
}

// TestVerifyMultiResponseDoesNotAutoClose: a parent with TWO attached
// responses does NOT auto-close on verifying just one of them (close
// stays a separate, deliberate act).
func TestVerifyMultiResponseDoesNotAutoClose(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-g001"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	firstResponse := respondFlow(t, mirrorDir, parentID, "beta")
	// Second response MUST carry different content (HIGH-1 fix-wave
	// finding: responseID is now content-derived) — otherwise it would
	// mint the SAME id as the first and collapse onto it instead of
	// exercising the genuine multi-response case this test targets.
	_ = respondFlow(t, mirrorDir, parentID, "beta", "--field", "title=second response")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{firstResponse}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	files := fake.calls[0].Files
	if len(files) != 1 {
		t.Fatalf("expected only the verify event (no auto-close, multi-response parent), got %d files: %+v", len(files), files)
	}
	if !strings.Contains(string(files[0].Content), "transition: verify") {
		t.Fatalf("expected a verify event, got:\n%s", files[0].Content)
	}
}

// TestDisputeAuthorsExactlyOneEvent: dispute's parent-reopen is fold's own
// side effect (applyResponseScoped) — the CLI verb authors exactly ONE
// event, never two.
func TestDisputeAuthorsExactlyOneEvent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-h001"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewDisputeCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--reason", "wrong answer", responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 || len(fake.calls[0].Files) != 1 {
		t.Fatalf("expected exactly one commit with exactly one event file, got %+v", fake.calls)
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "transition: dispute") {
		t.Fatalf("expected a dispute event, got:\n%s", fake.calls[0].Files[0].Content)
	}
}

// TestNoteChecksAuthorizationWithoutStatePrecondition is D-025 at the CLI
// authoring seam: `note` is transition-free, so draft/closed state never
// refuses it, but the command still rejects an actor outside either party
// before it opens a PR.
func TestNoteChecksAuthorizationWithoutStatePrecondition(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-k001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	// No committed history at all: the subject is still draft, but a note has
	// no state precondition.
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewNoteCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--note", "reminder: please respond", id}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (note has no state precondition); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "transition: note") {
		t.Fatalf("expected a note event, got:\n%s", fake.calls[0].Files[0].Content)
	}

	outsiderFake := &fakeLifecycleFunnel{}
	outsider := cli.NewNoteCommand(outsiderFake, mirrorDir, "fixture-space", "gamma", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	outsiderIO, _, outsiderErr := newIO()
	if code := outsider.Run(context.Background(), []string{"--note", "not my thread", id}, outsiderIO); code != 1 {
		t.Fatalf("outsider code = %d, want 1; stderr=%s", code, outsiderErr.String())
	}
	if !strings.Contains(outsiderErr.String(), "LFC-002") {
		t.Fatalf("outsider refusal = %q, want LFC-002", outsiderErr.String())
	}
	if len(outsiderFake.calls) != 0 {
		t.Fatalf("unauthorized note reached funnel: %d call(s)", len(outsiderFake.calls))
	}
}

// TestAckEndToEndWithRealFunnelAndFakeHost is a fixture-space integration
// test (spec 08's own "how to verify" column): a real space.WriteFunnel +
// host.NewFakeHost, no SubmitValidator wired (this phase's own local
// legality gate already refused/allowed before the funnel is ever
// reached) — proves the batch really lands as one commit + one open PR
// against a real (local) git remote.
func TestAckEndToEndWithRealFunnelAndFakeHost(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("beta")

	id := "XQ-axon-20260721-j001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	cmd := cli.NewAckCommand(funnel, mirrorDir, "fixture-space", "beta", lifecycleManifest(), hostCfg, lifecycleActorResolver("agent", "bot"))
	cacheDir := t.TempDir()
	pending := cli.NewCacheBackedPendingMarker(cacheDir)
	cmd.SetPendingMarker(pending)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{id}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call, got %d", len(fakeHost.Opens))
	}
	if len(fakeHost.Pushes) != 1 {
		t.Fatalf("expected exactly one PushBranch call, got %d", len(fakeHost.Pushes))
	}
	marker, err := cache.ReadMarker(cacheDir, "fixture-space", id)
	if err != nil {
		t.Fatalf("lifecycle write did not persist a pending marker: %v", err)
	}
	if marker.PRURL == "" {
		t.Fatalf("pending marker = %+v, want PR URL", marker)
	}

	// The ack is not on main yet, so accept is genuinely early. Its LFC-001
	// refusal must identify the pending PR and the supported recovery command.
	accept := cli.NewAcceptCommand(funnel, mirrorDir, "fixture-space", "beta", lifecycleManifest(), hostCfg, lifecycleActorResolver("agent", "bot"))
	accept.SetPendingMarker(pending)
	acceptIO, _, acceptErr := newIO()
	if code := accept.Run(context.Background(), []string{id}, acceptIO); code != 1 {
		t.Fatalf("early accept code = %d, want 1", code)
	}
	if !strings.Contains(acceptErr.String(), "a2a await "+id) || !strings.Contains(acceptErr.String(), marker.PRURL) {
		t.Fatalf("early refusal = %q, want pending PR and await remedy", acceptErr.String())
	}
}

func writeRequirementArtifact(t *testing.T, mirrorDir, id string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: requirement\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: new-capability\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"acceptance_criteria: [\"works\"]\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/requires/"+id+".md", content)
}

func writeHandoffArtifact(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: handoff\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
}

// TestRemainingGenericVerbsLegalPath rounds out AC-302.1's "every legal
// §3.4 transition, via its verb" coverage (TestAckLegalTransitionAndBatch/
// TestApproveRejectAlwaysGateMarker already cover ack/decline/approve/
// reject) for the rest of the table-driven OP-211 verb set: each subtest
// seeds the minimal prior state a verb's own transition requires, then
// asserts a legal run exits 0 and reaches the funnel exactly once.
func TestRemainingGenericVerbsLegalPath(t *testing.T) {
	t.Parallel()

	t.Run("accept_from_acknowledged", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XQ-axon-20260721-m001"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewAcceptCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})

	t.Run("start_from_accepted", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XQ-axon-20260721-m002"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewStartCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
	})

	t.Run("cancel_from_submitted", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XQ-axon-20260721-m003"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewCancelCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
	})

	t.Run("withdraw_requirement_from_published", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XR-axon-widget"
		writeRequirementArtifact(t, mirrorDir, id)
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewWithdrawCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
	})

	t.Run("supersede_requirement", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XR-axon-legacy"
		writeRequirementArtifact(t, mirrorDir, id)
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewSupersedeCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", "XR-axon-legacy-v2", id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
	})

	t.Run("satisfy_requirement_from_acknowledged", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XR-axon-satisfiable"
		writeRequirementArtifact(t, mirrorDir, id)
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "publish", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		fake := &fakeLifecycleFunnel{}
		// satisfy is the REQUESTER's own event (RoleOwner = axon, domain
		// doc §3.4.2: "target publishes, requester verifies + authors
		// satisfy").
		cmd := cli.NewSatisfyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", "XC-axon-widget@1.0.0,XS-beta-20260721-p1p1", id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
	})

	t.Run("block_and_unblock_recovers_prior_state", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XQ-axon-20260721-m004"
		writeQuestionArtifact(t, mirrorDir, id, "beta")
		writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
		writeLifecycleEvent(t, mirrorDir, "beta", 2, id, "accept", "beta")

		blockFake := &fakeLifecycleFunnel{}
		blockCmd := cli.NewBlockCommand(blockFake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := blockCmd.Run(context.Background(), []string{"--refs", "XQ-axon-20260721-blocker", id}, io); code != 0 {
			t.Fatalf("block: code = %d, want 0; stderr=%s", code, errOut.String())
		}
		materializeFiles(t, mirrorDir, blockFake.calls[0])

		unblockFake := &fakeLifecycleFunnel{}
		unblockCmd := cli.NewUnblockCommand(unblockFake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io2, _, errOut2 := newIO()
		if code := unblockCmd.Run(context.Background(), []string{id}, io2); code != 0 {
			t.Fatalf("unblock: code = %d, want 0; stderr=%s", code, errOut2.String())
		}
	})

	t.Run("verify_pass_and_verify_fail_on_handoff", func(t *testing.T) {
		t.Parallel()

		t.Run("pass", func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			id := "XH-axon-20260721-n001"
			writeHandoffArtifact(t, mirrorDir, id, "beta")
			writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
			writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewVerifyPassCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			io, _, errOut := newIO()
			if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
				t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
			}
		})

		t.Run("fail", func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			id := "XH-axon-20260721-n002"
			writeHandoffArtifact(t, mirrorDir, id, "beta")
			writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
			writeLifecycleEvent(t, mirrorDir, "beta", 1, id, "acknowledge", "beta")
			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewVerifyFailCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			io, _, errOut := newIO()
			if code := cmd.Run(context.Background(), []string{"--findings", "did not meet spec", id}, io); code != 0 {
				t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
			}
		})
	})

	t.Run("close_from_responded", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		id := "XQ-axon-20260721-m005"
		seedAcceptedQuestion(t, mirrorDir, id, "beta")
		_ = respondFlow(t, mirrorDir, id, "beta")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
	})
}

// TestAckAcceptsFlagWrittenAfterPositional is Wave K's live-run 6 defect
// ("thirteen verbs refuse a flag written after their positional
// argument") applied to LifecycleCommand.Run — "the most important one"
// per the wave's own brief, since every N-id batch verb (ack/accept/
// decline/start/block/unblock/cancel/close/withdraw/supersede/satisfy/
// approve/reject/verify-pass/verify-fail) shares this one Run method via
// the table. `a2a ack <id> --actor-name reviewer-bot` used to exit 2 with
// "usage: a2a ack <id...>" (Go's flag package stops parsing at the first
// non-flag token, so `--actor-name reviewer-bot` was counted as two more
// positionals).
//
// TEETH: reverting LifecycleCommand.Run's parseArgsAnyOrder call
// (cmd_lifecycle.go) back to a bare `fs.Parse(args)` reds this with
// exactly that usage error.
func TestAckAcceptsFlagWrittenAfterPositional(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-p001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	// --reason is accepted (and, per LifecycleCommand.Run, written onto
	// the authored event's `note`) for every table-driven verb regardless
	// of RequireReason — used here purely as a flag whose VALUE is
	// independently verifiable in the funnel call's own content, unlike
	// --actor-name/--actor-kind/--actor-model, which this test's fixed
	// lifecycleActorResolver ignores by design (§7.4 seam, see its own
	// doc comment).
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{id, "--reason", "reviewed after the flag"}, io)
	if code != 0 {
		t.Fatalf("ack <id> --reason x: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	found := false
	for _, fw := range fake.calls[0].Files {
		if strings.Contains(string(fw.Content), "note: reviewed after the flag") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the --reason text (written after the positional) to reach the authored event, got:\n%v", fake.calls[0].Files)
	}
}

// TestNoteAcceptsFlagWrittenAfterPositional is the same Wave K defect
// applied to NoteCommand.Run (its own Run method, distinct from
// LifecycleCommand.Run above): `a2a note <id> --note text` used to refuse.
//
// TEETH: reverting NoteCommand.Run's parseArgsAnyOrder call
// (cmd_lifecycle.go) back to a bare `fs.Parse(args)` reds this.
func TestNoteAcceptsFlagWrittenAfterPositional(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-p002"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewNoteCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{id, "--note", "context for the record"}, io)
	if code != 0 {
		t.Fatalf("note <id> --note x: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	found := false
	for _, fw := range fake.calls[0].Files {
		if strings.Contains(string(fw.Content), "note: context for the record") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the --note text to reach the authored event, got:\n%v", fake.calls[0].Files)
	}
}

// TestRespondAcceptsFlagWrittenAfterPositional is the same Wave K defect
// applied to RespondCommand.Run: `a2a respond <parent-id> --result
// answered` used to refuse. Distinct from TestRespondSetsToAsParentAuthor
// AndPassesSubmitValidation below (Part 1's own fix, `to`), which always
// writes flags before the positional and would not have caught this.
//
// TEETH: reverting RespondCommand.Run's parseArgsAnyOrder call
// (cmd_lifecycle.go) back to a bare `fs.Parse(args)` reds this.
func TestRespondAcceptsFlagWrittenAfterPositional(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-p003"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{parentID, "--result", "answered"}, io)
	if code != 0 {
		t.Fatalf("respond <parent-id> --result x: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
}

// extractResponseID pulls the minted XS- response id out of a funnel
// call's committed files (same lookup respondFlow uses, factored out for
// tests that need to compare TWO calls' own ids directly rather than
// materializing either onto disk).
func extractResponseID(files []space.FileWrite) string {
	for _, fw := range files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			return strings.TrimSuffix(filepath.Base(fw.Path), ".md")
		}
	}
	return ""
}

// TestRespondDeterministicResponseID is HIGH-1's own discriminating test
// (AC-301.1, anti-pattern #4): with a FIXED injected clock, two `respond`
// invocations against the SAME parent with IDENTICAL content mint the
// IDENTICAL responseID (a retry lands on the SAME funnel branch, no
// duplicate PR); two invocations with DIFFERENT content mint DISTINCT
// ids (multiple genuine responses to one parent are never collapsed).
func TestRespondDeterministicResponseID(t *testing.T) {
	t.Parallel()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }

	newRespondCmd := func(t *testing.T, mirrorDir, parentID string) (*cli.RespondCommand, *fakeLifecycleFunnel) {
		t.Helper()
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(fixedNow)
		return cmd, fake
	}

	t.Run("identical_content_mints_identical_id", func(t *testing.T) {
		t.Parallel()
		parentID := "XQ-axon-20260721-r001"

		mirrorDir1 := t.TempDir()
		cmd1, fake1 := newRespondCmd(t, mirrorDir1, parentID)
		io1, _, errOut1 := newIO()
		if code := cmd1.Run(context.Background(), []string{"--result", "answered", parentID}, io1); code != 0 {
			t.Fatalf("respond (1st): code = %d, want 0; stderr=%s", code, errOut1.String())
		}

		mirrorDir2 := t.TempDir()
		cmd2, fake2 := newRespondCmd(t, mirrorDir2, parentID)
		io2, _, errOut2 := newIO()
		if code := cmd2.Run(context.Background(), []string{"--result", "answered", parentID}, io2); code != 0 {
			t.Fatalf("respond (2nd): code = %d, want 0; stderr=%s", code, errOut2.String())
		}

		id1 := extractResponseID(fake1.calls[0].Files)
		id2 := extractResponseID(fake2.calls[0].Files)
		if id1 == "" || id2 == "" {
			t.Fatalf("expected a minted response id in both calls; got %q and %q", id1, id2)
		}
		if id1 != id2 {
			t.Fatalf("responseID = %q vs %q; expected the SAME id for identical content under a fixed clock", id1, id2)
		}
		if fake1.calls[0].ArtifactID != fake2.calls[0].ArtifactID {
			t.Fatalf("ArtifactID = %q vs %q; expected the SAME funnel branch key for identical content", fake1.calls[0].ArtifactID, fake2.calls[0].ArtifactID)
		}
	})

	t.Run("different_content_mints_different_id", func(t *testing.T) {
		t.Parallel()
		parentID := "XQ-axon-20260721-r002"

		mirrorDir1 := t.TempDir()
		cmd1, fake1 := newRespondCmd(t, mirrorDir1, parentID)
		io1, _, errOut1 := newIO()
		if code := cmd1.Run(context.Background(), []string{"--result", "answered", parentID}, io1); code != 0 {
			t.Fatalf("respond (answered): code = %d, want 0; stderr=%s", code, errOut1.String())
		}

		mirrorDir2 := t.TempDir()
		cmd2, fake2 := newRespondCmd(t, mirrorDir2, parentID)
		io2, _, errOut2 := newIO()
		if code := cmd2.Run(context.Background(), []string{"--result", "partial", parentID}, io2); code != 0 {
			t.Fatalf("respond (partial): code = %d, want 0; stderr=%s", code, errOut2.String())
		}

		id1 := extractResponseID(fake1.calls[0].Files)
		id2 := extractResponseID(fake2.calls[0].Files)
		if id1 == "" || id2 == "" {
			t.Fatalf("expected a minted response id in both calls; got %q and %q", id1, id2)
		}
		if id1 == id2 {
			t.Fatalf("expected DIFFERENT ids for --result answered vs --result partial, got the same id %q", id1)
		}
	})

	t.Run("same_intent_across_midnight_keeps_operation_key", func(t *testing.T) {
		t.Parallel()
		parentID := "XQ-axon-20260721-r003"
		runAt := func(t *testing.T, now time.Time) space.SubmitRequest {
			t.Helper()
			mirrorDir := t.TempDir()
			seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			cmd.SetClockForTest(func() time.Time { return now })
			io, _, errOut := newIO()
			if code := cmd.Run(context.Background(), []string{"--result", "answered", parentID}, io); code != 0 {
				t.Fatalf("respond: code=%d stderr=%s", code, errOut.String())
			}
			return fake.calls[0]
		}
		before := runAt(t, time.Date(2026, 7, 21, 23, 59, 0, 0, time.UTC))
		after := runAt(t, time.Date(2026, 7, 22, 0, 1, 0, 0, time.UTC))
		if before.ArtifactID == after.ArtifactID {
			t.Fatalf("public date-bearing IDs unexpectedly equal: %q", before.ArtifactID)
		}
		if before.OperationKey == "" || before.OperationKey != after.OperationKey {
			t.Fatalf("operation keys differ across midnight: %q vs %q", before.OperationKey, after.OperationKey)
		}
	})
}

// TestRespondPropagatesParentThreadVerbatim is spec 46 §T1 R2: a response
// is a DERIVED artifact — it inherits its parent's `thread` verbatim rather
// than minting a fresh one (that would fork the conversation) or leaving
// the template's placeholder unfilled.
func TestRespondPropagatesParentThreadVerbatim(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-th01"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "answered", parentID}, io)
	if code != 0 {
		t.Fatalf("respond: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	var respContent string
	for _, fw := range fake.calls[0].Files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			respContent = string(fw.Content)
		}
	}
	if respContent == "" {
		t.Fatalf("no committed XS- response file found among %+v", fake.calls[0].Files)
	}
	if !strings.Contains(respContent, "thread: thread:axon-20260721-t9a1\n") {
		t.Fatalf("expected the response to inherit the parent's thread verbatim, got:\n%s", respContent)
	}
}

// TestRespondThreadIsNotInTheResponseIDSeed is spec 46 §T1 R5's own
// discriminating proof: two respond calls against the SAME parentID,
// result, and actor under a FIXED clock, differing ONLY in the underlying
// parent's `thread:` value, must mint the IDENTICAL responseID — if the
// derived thread default leaked into lifecycleRespondSeed (the hazard
// R5 warns about), this test reds while
// TestRespondDeterministicResponseID (which never varies the thread)
// would stay green, which is exactly why that test alone cannot stand in
// for this one.
func TestRespondThreadIsNotInTheResponseIDSeed(t *testing.T) {
	t.Parallel()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }
	parentID := "XQ-axon-20260721-r900"

	run := func(t *testing.T, thread string) string {
		t.Helper()
		mirrorDir := t.TempDir()
		writeQuestionArtifactWithThread(t, mirrorDir, parentID, "beta", thread)
		writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
		writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(fixedNow)
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--result", "answered", parentID}, io); code != 0 {
			t.Fatalf("respond (thread=%q): code = %d; stderr=%s", thread, code, errOut.String())
		}
		id := extractResponseID(fake.calls[0].Files)
		if id == "" {
			t.Fatalf("respond (thread=%q): expected a minted response id", thread)
		}
		return id
	}

	idA := run(t, "thread:axon-20260721-t9a1")
	idB := run(t, "thread:axon-20260721-zzzz")
	if idA != idB {
		t.Fatalf("responseID = %q vs %q; the parent's thread must NOT be part of lifecycleRespondSeed (R5) — got "+
			"different ids for otherwise-identical respond calls that differ only in the parent's thread", idA, idB)
	}
}

// TestRespondConflictingExplicitThreadRefused is spec 46 §T1 R4: an
// explicit --field thread=<id> that differs from the PARENT's own thread is
// an ERROR naming BOTH values — never a silent precedence, never a guess.
func TestRespondConflictingExplicitThreadRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-th02"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	const otherThread = "thread:beta-20260721-z9z9"
	code := cmd.Run(context.Background(), []string{"--result", "answered", "--field", "thread=" + otherThread, parentID}, io)
	if code != 2 {
		t.Fatalf("respond: code = %d, want 2 (bad caller input — a --field thread that conflicts with the parent's own thread)", code)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to NEVER be called on a thread conflict, got %d calls", len(fake.calls))
	}
	msg := errOut.String()
	if !strings.Contains(msg, otherThread) {
		t.Fatalf("expected stderr to name the explicit conflicting value %q, got: %s", otherThread, msg)
	}
	if !strings.Contains(msg, "thread:axon-20260721-t9a1") {
		t.Fatalf("expected stderr to name the parent's own thread value, got: %s", msg)
	}
}

// TestRespondIdempotentRetryReturnsAlreadyOpen is HIGH-1's end-to-end
// proof against a REAL space.WriteFunnel + host.NewFakeHost (the same
// fixture-space integration pattern as
// TestAckEndToEndWithRealFunnelAndFakeHost): a retried `respond` with
// IDENTICAL content and a FIXED clock lands on the SAME deterministic
// branch, so the SECOND call short-circuits to
// space.WriteStateAlreadyOpen — no second PR is opened.
func TestRespondIdempotentRetryReturnsAlreadyOpen(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("beta")

	parentID := "XQ-axon-20260721-r003"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }

	cmd1 := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", lifecycleManifest(), hostCfg, lifecycleActorResolver("agent", "bot"))
	cmd1.SetClockForTest(fixedNow)
	io1, out1, errOut1 := newIO()
	if code := cmd1.Run(context.Background(), []string{"--result", "answered", parentID}, io1); code != 0 {
		t.Fatalf("respond (1st): code = %d, want 0; stdout=%s stderr=%s", code, out1.String(), errOut1.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one OpenPR call after the 1st respond, got %d", len(fakeHost.Opens))
	}

	// A SECOND, freshly-constructed command (simulating a retried CLI
	// invocation) against the SAME still-pending mirror state.
	cmd2 := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", lifecycleManifest(), hostCfg, lifecycleActorResolver("agent", "bot"))
	cmd2.SetClockForTest(fixedNow)
	io2, out2, errOut2 := newIO()
	if code := cmd2.Run(context.Background(), []string{"--result", "answered", parentID}, io2); code != 0 {
		t.Fatalf("respond (retry): code = %d, want 0; stdout=%s stderr=%s", code, out2.String(), errOut2.String())
	}
	if len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one OpenPR call after the retry (dedup), got %d", len(fakeHost.Opens))
	}
	if !strings.Contains(out2.String(), "already submitted") {
		t.Fatalf("expected the retry's stdout to report the already-submitted idempotent path, got %q", out2.String())
	}
}

// TestRespondSetsToAsParentAuthorAndPassesSubmitValidation is Wave K's
// regression proof for live run 6, part 1 ("a2a respond writes the
// response template's to: placeholder verbatim"): response.md's own
// `to: [<requester-system>]` is a SEQUENCE node, and internal/template's
// applyFills/setScalar only ever rewrites a SCALAR node (P18's
// deliberately-deferred "Fix C (--field lists)", off-limits this wave) —
// so the rendered draft used to still carry the literal placeholder text,
// and the real write funnel's own V2 pass refused it on every one of
// three separate live rows with the identical message:
//
//	respond: space: Submit: submit validation failed: [REF-006 to: `to`
//	includes an unknown system: <requester-system>]
//
// Unlike TestRespondIdempotentRetryReturnsAlreadyOpen (which passes a
// NIL SubmitValidator to space.NewWriteFunnel and so would NOT have
// caught this — no validation ever runs), this test wires the REAL
// validate.Engine through the funnel's own SubmitValidator seam
// (SubmitValidatorAdapter, the exact adapter cmd/a2a's own wiring uses),
// so it exercises precisely the check that failed on the live run: the
// assertion is that `a2a respond` SUCCEEDS end to end, not merely that
// `to` changed.
//
// It also asserts `space`/`title` are filled (a SECOND, related defect
// found while checking — per the pinned `to` fix's own instruction — this
// verb's other unfilled placeholders): V2 has no guard for either, so a
// `code == 0` assertion alone would have missed a response permanently
// committing `space: <space-id>`.
//
// TEETH: reverting RespondCommand.Run's respFm/respDoc["to"] assignment
// (cmd_lifecycle.go, the decode/mutate/re-encode block right after
// template.Render) back to using the unfilled template output reds this
// test with the exact REF-006 message the live run reported. Separately,
// reverting the `respFields["space"] = parentProbe.Space` line (same
// function, a few lines above) reds this test's own `<space-id>`
// assertion instead — a distinct edit, a distinct red, both proven below.
func TestRespondSetsToAsParentAuthorAndPassesSubmitValidation(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("beta")

	parentID := "XQ-axon-20260721-t900"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := lifecycleManifest()
	legality := cli.NewLegalityAdapter(mirrorDir, "beta", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, "beta", resolver, legality)

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, validator, "0.1.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	cmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", manifest, hostCfg, lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "answered", parentID}, io)
	if code != 0 {
		t.Fatalf("respond: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message; got %q", out.String())
	}
	if len(fakeHost.Pushes) != 1 {
		t.Fatalf("expected exactly one PushBranch call, got %d", len(fakeHost.Pushes))
	}

	// Read the committed response back off the pushed branch and confirm
	// `to` names axon — the PARENT's own author (writeQuestionArtifact
	// seeds the parent `from: axon`) — never the literal placeholder.
	branch := fakeHost.Pushes[0].Branch
	changed := gitDiffNames(t, mirrorDir, "main", branch)
	var responsePath string
	for _, p := range changed {
		if strings.HasPrefix(filepath.Base(p), "XS-") && strings.HasSuffix(p, ".md") {
			responsePath = p
		}
	}
	if responsePath == "" {
		t.Fatalf("expected a committed XS- response file among %v", changed)
	}
	content := runGitOutputForTest(t, mirrorDir, "show", branch+":"+responsePath)
	if !strings.Contains(content, "to:\n    - axon\n") && !strings.Contains(content, "to: [axon]") {
		t.Fatalf("expected the committed response's `to` to be [axon] (the parent's own author), got:\n%s", content)
	}
	if strings.Contains(content, "<requester-system>") {
		t.Fatalf("the committed response still carries the unfilled template `to` placeholder:\n%s", content)
	}
	// `space` and `title` are the SAME class of unfilled-placeholder
	// defect as `to` (template.Render fills neither; `contract deprecate`
	// already had to fix the identical `space`/`title` gap on its own
	// draft-and-write-in-one-call announcement) — found while checking,
	// per the pinned `to` fix's own instruction, whether any OTHER field
	// on this exact verb shape goes out unfilled. V2 has no guard for
	// either (space is a free-form string; title has no placeholder-
	// literal check outside SubmitCommand.Run, which respond never goes
	// through), so ONLY a content assertion like this one would catch a
	// regression here — a bare `code == 0` would not.
	if strings.Contains(content, "<space-id>") {
		t.Fatalf("the committed response still carries the unfilled template `space` placeholder:\n%s", content)
	}
	if !strings.Contains(content, "space: fixture-space\n") {
		t.Fatalf("expected the committed response's `space` to be fixture-space (the parent's own space), got:\n%s", content)
	}
	if strings.Contains(content, "<human/agent-scannable title") {
		t.Fatalf("the committed response still carries the unfilled template `title` placeholder:\n%s", content)
	}
}

// TestRespondThreadlessParentWithExplicitThreadNamesTheRealCondition is the
// gap a closeout audit found between this surface and its MCP twin: both
// refuse, but for a threadless parent plus an explicit `--field thread=X` they
// used to refuse DIFFERENTLY — this side took the conflict branch and printed
// "conflicts with parent's thread " with an empty value where the message's
// whole job is naming both sides, while MCP fell through to the informative
// "the parent carries no thread" refusal.
//
// Why a test and not just the one-word guard: the two surfaces carry this rule
// twice on purpose (ADR-001 forbids internal/mcp importing internal/cli), and
// the parity suite cannot see this case — its own normalization comment says
// so. A duplicated rule with no test on the branch where the copies differ is
// how they drift.
func TestRespondThreadlessParentWithExplicitThreadNamesTheRealCondition(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-th03"
	// A pre-P46 parent: committed with no thread at all.
	writeQuestionArtifactWithThread(t, mirrorDir, parentID, "beta", "")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 2, parentID, "accept", "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "answered", "--field", "thread=thread:beta-20260721-z9z9", parentID}, io)

	if code != 1 {
		t.Fatalf("respond: code = %d, want 1 (the threadless-parent refusal, not the conflict refusal's exit 2)", code)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel never to be called, got %d calls", len(fake.calls))
	}
	msg := errOut.String()
	if !strings.Contains(msg, "carries no thread") {
		t.Fatalf("expected the refusal to name the actual condition (the parent has no thread), got: %s", msg)
	}
	if strings.Contains(msg, "conflicts with parent's thread") {
		t.Fatalf("took the conflict branch and printed an empty parent value: %s", msg)
	}
}
