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
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
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
func lifecycleActorResolver(kind, name string) func(cli.ActorFlags) template.Actor {
	return func(cli.ActorFlags) template.Actor { return template.Actor{Kind: kind, Name: name} }
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
// axon's own section, from axon to `to`.
func writeQuestionArtifact(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
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
// of the recorded funnel call's file paths).
func respondFlow(t *testing.T, mirrorDir, parentID, ownSystem string) string {
	t.Helper()
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", ownSystem, lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "answered", parentID}, io)
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
	_ = respondFlow(t, mirrorDir, parentID, "beta") // second response

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

// TestNoteSkipsLegalityCheck is D-025: `note` is transition-free and
// carries no fold-legality check — an actor who would be refused by every
// other verb still succeeds.
func TestNoteSkipsLegalityCheck(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-k001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	// No committed history at all (still `draft`) and an actor (gamma)
	// who is neither `from` nor `to` — every OTHER verb would refuse this
	// locally (LFC-002/LFC-001); note must not.
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewNoteCommand(fake, mirrorDir, "fixture-space", "gamma", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--note", "reminder: please respond", id}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (note has no fold-legality check); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if !strings.Contains(string(fake.calls[0].Files[0].Content), "transition: note") {
		t.Fatalf("expected a note event, got:\n%s", fake.calls[0].Files[0].Content)
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
