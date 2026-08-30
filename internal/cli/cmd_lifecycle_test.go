package cli_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
	"gopkg.in/yaml.v3"
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
		{System: "axon", Status: fold.MembershipMember},
		{System: "beta", Status: fold.MembershipMember},
		{System: "gamma", Status: fold.MembershipMember},
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

// TestAckRefusesEmptyHostBaseBranch is no-silent-yes-2026-08 Group A's own
// acceptance for lifecycleDeps.submit — the ONE chokepoint shared by every
// lifecycle verb in this file (5) plus cmd_contract.go's own 4 contract
// verbs (buildRequest itself cannot refuse without an out-of-allowlist
// signature change across all 9 callers — see buildRequest's own doc
// comment). An empty HostCfg.BaseBranch must be refused, naming the field,
// BEFORE the write funnel is ever called.
func TestAckRefusesEmptyHostBaseBranch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-c001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	hostCfg := lifecycleHostConfig()
	hostCfg.BaseBranch = ""
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), hostCfg, lifecycleActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{id}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for an empty HostCfg.BaseBranch")
	}
	if !strings.Contains(errOut.String(), "HostCfg.BaseBranch") {
		t.Fatalf("expected the refusal to name HostCfg.BaseBranch; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
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

// TestSupersedeDecisionRegressionFix closes wave 2b's own regression (that
// wave's report): before that fix, `lifecycleEvaluateCandidate`
// (cmd_lifecycle.go) reached fold's nil-facts EvaluateCandidate wrapper for
// EVERY `a2a supersede` call, so both decision-supersede rows (table.go) —
// which declare a SuccessorPrecondition — refused UNCONDITIONALLY for
// every actor, including a legitimate one with a satisfying successor. It
// now resolves real SuccessorFacts via MirrorResolver.Successor (the SAME
// capability the SUBMIT path's resolveSuccessorEnvelope already uses) and
// calls fold.EvaluateCandidateWithSuccessor directly.
//
// The `rejected` row (PreconditionSuccessorAuthor) is exercised with both a
// satisfying AND a non-satisfying successor, proving the fix resolves REAL
// facts rather than granting unconditionally (the same failure mode from
// the opposite direction) — wave 2b's own acceptance demonstration ("a
// legitimate actor with a satisfying successor is NOT refused").
//
// The `approved` row (PreconditionSuccessorApproved) used to have NO
// satisfying case here — wave 2b's own report's product finding, and
// this epic's own defect INVERTED: MirrorResolver.Successor
// (internal/cli/adapters.go) folded the successor through a synthetic
// `fold.Envelope{ID, Kind, From}` that never carried RequiredApprovers, so
// quorumReached (internal/fold/fold.go) was always false and StateApproved
// (set at exactly that one gated line) could never be reached through this
// resolver — regardless of how many real approve events the successor
// actually carried. Wave 2c (D-1: RequiredApprovers now reaches the folded
// envelope; D-2: the committed-history read now spans every participant's
// own section, not just the successor id's own home system's) fixes both
// halves — see approved_by_successor_with_real_quorum_across_sections_
// succeeds below, this epic's own acceptance proof "on BOTH surfaces"
// (internal/mcp/adapters_test.go carries the resolver-level twin).
func TestSupersedeDecisionRegressionFix(t *testing.T) {
	t.Parallel()

	t.Run("rejected_by_successor_author_succeeds", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260721-f001"
		successorID := "XD-axon-20260721-f002"
		writeDecisionArtifact(t, mirrorDir, predecessorID, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "reject", "beta")
		// writeDecisionArtifact always writes `from: axon` — matches the
		// acting actor below, satisfying §3.4.4's "author of the
		// successor" (PreconditionSuccessorAuthor, table.go).
		writeDecisionArtifact(t, mirrorDir, successorID, []string{"beta", "gamma"})

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewSupersedeCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", successorID, predecessorID}, io); code != 0 {
			t.Fatalf("code = %d, want 0 (legitimate actor, satisfying successor must NOT be refused); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})

	t.Run("rejected_by_mismatched_successor_author_still_refused", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260721-f003"
		successorID := "XD-axon-20260721-f004"
		writeDecisionArtifact(t, mirrorDir, predecessorID, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "reject", "beta")
		// Successor is authored by axon (writeDecisionArtifact's own fixed
		// `from:`), but the ACTING actor below is gamma — the precondition
		// requires the successor's own author to equal the ACTING actor, so
		// this must still refuse: the fix resolves real facts, it does not
		// grant unconditionally.
		writeDecisionArtifact(t, mirrorDir, successorID, []string{"beta", "gamma"})

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewSupersedeCommand(fake, mirrorDir, "fixture-space", "gamma", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", successorID, predecessorID}, io); code == 0 {
			t.Fatal("expected a non-zero exit (successor authored by axon, acting actor is gamma)")
		}
		// Wave 2c, D-3: the successor WAS resolved (it exists in this
		// mirror) but the author precondition failed, so this is now
		// LFC-005 ALONE — never LFC-002 (that mislabel is exactly what D-3
		// closes) and never paired with LFC-006 (that pairing is reserved
		// for an UNRESOLVED successor, see unresolvable_successor_is_
		// LFC005_plus_LFC006 below).
		if !strings.Contains(errOut.String(), "LFC-005") {
			t.Fatalf("expected the refusal to name LFC-005; got %q", errOut.String())
		}
		if strings.Contains(errOut.String(), "LFC-006") {
			t.Fatalf("expected NO LFC-006 (the successor WAS resolved, just failed the precondition); got %q", errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
		}
	})

	// approved_by_successor_with_real_quorum_across_sections_succeeds is
	// D-1+D-2's own proof through the FULL verb (this wave's report, the
	// whole point of the wave — this epic's own defect INVERTED, now
	// closed): before wave 2c this exact scenario refused unconditionally
	// (MirrorResolver.Successor could never resolve `approved` — see this
	// function's own doc comment). It now succeeds.
	t.Run("approved_by_successor_with_real_quorum_across_sections_succeeds", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260721-f005"
		successorID := "XD-axon-20260721-f006"
		writeDecisionArtifact(t, mirrorDir, predecessorID, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "approve", "beta")
		writeLifecycleEvent(t, mirrorDir, "gamma", 2, predecessorID, "approve", "gamma")

		// The successor carries a REAL, full-quorum approve history — every
		// required_approver has approved — with both approve events
		// committed under the APPROVING participant's OWN section (beta's,
		// gamma's), never the successor id's own home system's section
		// (axon's): D-2's own shape. RequiredApprovers now reaches the
		// folded envelope (D-1), so quorumReached (fold.go) sees a real
		// quorum and StateApproved is reached through this resolver.
		writeDecisionArtifact(t, mirrorDir, successorID, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 3, successorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 4, successorID, "approve", "beta")
		writeLifecycleEvent(t, mirrorDir, "gamma", 5, successorID, "approve", "gamma")

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewSupersedeCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", successorID, predecessorID}, io); code != 0 {
			t.Fatalf("code = %d, want 0 (successor genuinely resolves as approved); stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})

	// unresolvable_successor_is_LFC005_plus_LFC006 is D-3's own OTHER half,
	// through the verb: a successor id this resolver's own index can never
	// contain (D9's own "unresolved" case, types.go) refuses LFC-005 WITH
	// an LFC-006 advisory alongside it — never LFC-002, and never LFC-005
	// alone (that shape is reserved for a RESOLVED-but-failing successor,
	// see rejected_by_mismatched_successor_author_still_refused above).
	t.Run("unresolvable_successor_is_LFC005_plus_LFC006", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260721-f007"
		writeDecisionArtifact(t, mirrorDir, predecessorID, []string{"beta", "gamma"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "reject", "beta")
		// No successor artifact written at all: refs names an id this
		// resolver's own index can never contain.

		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewSupersedeCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", "XD-axon-20260721-gh57", predecessorID}, io); code == 0 {
			t.Fatal("expected a non-zero exit (successor entirely unresolvable)")
		}
		if !strings.Contains(errOut.String(), "LFC-005") {
			t.Fatalf("expected the refusal to name LFC-005; got %q", errOut.String())
		}
		if !strings.Contains(errOut.String(), "LFC-006") {
			t.Fatalf("expected the refusal to ALSO name LFC-006 (unresolved successor); got %q", errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
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

// seedAcceptedQuestionWithCriteria is seedAcceptedQuestion, plus an injected
// `acceptance_criteria:` block (defects-fix-2026-08 P3) — criteriaYAML is
// the already-indented block body (e.g. `"  - \"text\"\n"` or
// `"  - id: ac1\n    text: \"text\"\n"`), letting callers exercise both
// shapes without a second seed function. These tests go through
// fakeLifecycleFunnel (never the real funnel/schema pipeline — the same
// pattern every other verify/close test in this file already uses), so the
// seeded frontmatter is read by cmd_lifecycle.go's own probe directly and
// never itself schema-validated here; the schema-level shape guarantees are
// proven separately, against the real corpus, by
// TestSchemaAcceptanceCriteria* below.
func seedAcceptedQuestionWithCriteria(t *testing.T, mirrorDir, id, to, criteriaYAML string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
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
		"thread: thread:axon-20260721-t9a1\n" +
		"acceptance_criteria:\n" + criteriaYAML +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
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

func writeSatisfyContractArtifact(t *testing.T, mirrorDir, id string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/provides/widget/contract.md", content)
}

func writeSatisfyResponseArtifact(t *testing.T, mirrorDir, id, parent string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: response\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"parent: " + parent + "\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"classification: internal\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+id+".md", content)
}

func writeSatisfyProofEvent(t *testing.T, mirrorDir, actingSystem string, seq int, subject, transition, actorSystem, version string) {
	t.Helper()
	id, err := artifact.MintULIDAt(time.Date(2020, 1, 1, 0, 1, seq, 0, time.UTC), rand.Reader)
	if err != nil {
		t.Fatalf("writeSatisfyProofEvent: mint ulid: %v", err)
	}
	content := fmt.Sprintf(
		"schema: event/v1\nevent: %s\nspace: fixture-space\nsubject: %s\ntransition: %s\nactor: {kind: agent, name: bot, system: %s}\nat: 2020-01-01T00:01:00Z\n",
		id.String(), subject, transition, actorSystem,
	)
	if version != "" {
		content += "version: " + version + "\n"
	}
	writeMirrorFile(t, mirrorDir, actingSystem+"/events/2020/"+id.String()+".yaml", content)
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
		contractID := "XC-axon-widget"
		responseID := "XS-beta-20260721-p1p1"
		writeSatisfyContractArtifact(t, mirrorDir, contractID)
		writeSatisfyResponseArtifact(t, mirrorDir, responseID, id)
		writeSatisfyProofEvent(t, mirrorDir, "axon", 0, contractID, "publish", "axon", "1.0.0")
		writeSatisfyProofEvent(t, mirrorDir, "axon", 1, responseID, "verify", "axon", "")
		fake := &fakeLifecycleFunnel{}
		// satisfy is the REQUESTER's own event (RoleOwner = axon, domain
		// doc §3.4.2: "target publishes, requester verifies + authors
		// satisfy").
		cmd := cli.NewSatisfyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), []string{"--refs", contractID + "@1.0.0," + responseID, id}, io); code != 0 {
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

func TestSatisfyRequiresResolvedContractAndVerifiedMatchingResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		refs            string
		contract        bool
		contractVersion string
		response        bool
		responseParent  string
		verified        bool
		want            string
	}{
		{name: "missing_ref", refs: "XC-axon-widget@1.0.0", contract: true, contractVersion: "1.0.0", response: true, responseParent: "self", verified: true, want: "exactly one XC contract@version and one XS response"},
		{name: "missing_contract", refs: "XC-axon-widget@1.0.0,XS-beta-20260721-s001", response: true, responseParent: "self", verified: true, want: "does not resolve"},
		{name: "missing_response", refs: "XC-axon-widget@1.0.0,XS-beta-20260721-s001", contract: true, contractVersion: "1.0.0", want: "response ref XS-beta-20260721-s001 does not resolve"},
		{name: "unpinned_contract", refs: "XC-axon-widget,XS-beta-20260721-s001", contract: true, contractVersion: "1.0.0", response: true, responseParent: "self", verified: true, want: "must pin an explicit semantic version"},
		{name: "wrong_kind_contract", refs: "XQ-axon-20260721-s001@1.0.0,XS-beta-20260721-s001", contract: true, contractVersion: "1.0.0", response: true, responseParent: "self", verified: true, want: "want XC-...@<semver>"},
		{name: "unresolved_contract_version", refs: "XC-axon-widget@2.0.0,XS-beta-20260721-s001", contract: true, contractVersion: "1.0.0", response: true, responseParent: "self", verified: true, want: "does not resolve to a recorded version"},
		{name: "wrong_kind", refs: "XC-axon-widget@1.0.0,XQ-axon-20260721-s001", contract: true, contractVersion: "1.0.0", response: true, responseParent: "self", verified: true, want: "want XS-"},
		{name: "unverified_response", refs: "XC-axon-widget@1.0.0,XS-beta-20260721-s001", contract: true, contractVersion: "1.0.0", response: true, responseParent: "self", want: "want \"verified\""},
		{name: "mismatched_response_parent", refs: "XC-axon-widget@1.0.0,XS-beta-20260721-s001", contract: true, contractVersion: "1.0.0", response: true, responseParent: "XR-axon-other", verified: true, want: "want \"XR-axon-satisfy-guard\""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			const requirementID = "XR-axon-satisfy-guard"
			const contractID = "XC-axon-widget"
			const responseID = "XS-beta-20260721-s001"
			writeRequirementArtifact(t, mirrorDir, requirementID)
			writeLifecycleEvent(t, mirrorDir, "axon", 0, requirementID, "publish", "axon")
			writeLifecycleEvent(t, mirrorDir, "beta", 1, requirementID, "acknowledge", "beta")
			if tc.contract {
				writeSatisfyContractArtifact(t, mirrorDir, contractID)
				writeSatisfyProofEvent(t, mirrorDir, "axon", 0, contractID, "publish", "axon", tc.contractVersion)
			}
			if tc.response {
				parent := tc.responseParent
				if parent == "self" {
					parent = requirementID
				}
				writeSatisfyResponseArtifact(t, mirrorDir, responseID, parent)
				if tc.verified {
					writeSatisfyProofEvent(t, mirrorDir, "axon", 1, responseID, "verify", "axon", "")
				}
			}

			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewSatisfyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			io, _, errOut := newIO()
			if code := cmd.Run(context.Background(), []string{"--refs", tc.refs, requirementID}, io); code != 1 {
				t.Fatalf("code = %d, want 1; stderr=%s", code, errOut.String())
			}
			if len(fake.calls) != 0 {
				t.Fatalf("invalid satisfy proof reached the funnel: %d calls", len(fake.calls))
			}
			if got := errOut.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("stderr = %q, want substring %q", got, tc.want)
			}
		})
	}
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

// --- --ref (wave 23, agent-exchange-2026-08 spec 06 §11's 2026-08-10
// "AC9 wire decision"): a repeatable flag writing refs[] onto the
// committed response — the array-typed field --field cannot reach. -------

// extractResponseContent returns the raw bytes of the committed XS- response
// file in files — extractResponseID's own sibling, needed by any test that
// must inspect the response's OWN frontmatter rather than merely its
// minted id.
func extractResponseContent(files []space.FileWrite) string {
	for _, fw := range files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			return string(fw.Content)
		}
	}
	return ""
}

// respondRefsProbe decodes only `refs[].ref` — response.md's template
// carries a COMMENTED-OUT `# refs:` placeholder line (this epic's own
// documented trap: a raw strings.Contains over a rendered artifact once
// matched a comment and stayed green with the feature removed), so every
// assertion below decodes through this probe rather than grepping raw
// bytes.
type respondRefsProbe struct {
	Refs []struct {
		Ref string `yaml:"ref"`
	} `yaml:"refs"`
}

func decodeResponseRefs(t *testing.T, content string) []string {
	t.Helper()
	fm, err := artifact.ParseFrontmatter([]byte(content))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	var probe respondRefsProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		t.Fatalf("decode refs: %v", err)
	}
	out := make([]string, len(probe.Refs))
	for i, r := range probe.Refs {
		out[i] = r.Ref
	}
	return out
}

func TestRespondWritesRefsFromRepeatableRefFlag(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ref1"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "delivered", "--ref", "XH-beta-20260721-h001", parentID}, io)
	if code != 0 {
		t.Fatalf("respond --ref: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	content := extractResponseContent(fake.calls[0].Files)
	if content == "" {
		t.Fatalf("no committed XS- response file found among %+v", fake.calls[0].Files)
	}
	refs := decodeResponseRefs(t, content)
	if len(refs) != 1 || refs[0] != "XH-beta-20260721-h001" {
		t.Fatalf("decoded refs = %v, want exactly [XH-beta-20260721-h001]", refs)
	}
}

// TestRespondNoRefFlagOmitsRefsKey is the "made unconditional" mutation
// guard for the WRITE side: a respond call with no --ref must decode to
// ZERO refs — not one, which would be the case if the template's own
// commented-out `# refs:` placeholder ever leaked through, or if this
// command wrote an empty refs[] block regardless of --ref.
func TestRespondNoRefFlagOmitsRefsKey(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ref2"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "answered", parentID}, io)
	if code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	content := extractResponseContent(fake.calls[0].Files)
	refs := decodeResponseRefs(t, content)
	if len(refs) != 0 {
		t.Fatalf("decoded refs = %v, want none when --ref was never given", refs)
	}
}

func TestRespondEmptyRefIsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ref3"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "delivered", "--ref", "", parentID}, io)
	if code != 2 {
		t.Fatalf("respond --ref '': code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel never to be called on a usage refusal, got %d calls", len(fake.calls))
	}
}

// TestRespondWritesRefsInGivenOrder pins the ordering decision recorded in
// lifecycleRespondSeed's own doc comment: `refs[]` is a sequence and its
// written order is part of the document's own bytes, so --ref values are
// written in the order given, never sorted.
func TestRespondWritesRefsInGivenOrder(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ref7"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"--result", "delivered",
		"--ref", "XH-beta-20260721-h002",
		"--ref", "XH-beta-20260721-h001",
		parentID,
	}, io)
	if code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	content := extractResponseContent(fake.calls[0].Files)
	refs := decodeResponseRefs(t, content)
	want := []string{"XH-beta-20260721-h002", "XH-beta-20260721-h001"}
	if len(refs) != 2 || refs[0] != want[0] || refs[1] != want[1] {
		t.Fatalf("decoded refs = %v, want %v (GIVEN order preserved)", refs, want)
	}
}

// TestRespondRefsParticipateInResponseIDSeed is the brief's own
// requirement, made concrete: "the new flag's values MUST participate in
// [lifecycleRespondSeed], in a deterministic order, or two different
// responses collide on one id." Two calls, identical in every other way,
// differing ONLY in whether --ref was given, must mint DISTINCT response
// ids AND distinct operation keys (operation.Respond's own dedup key,
// carried via a synthetic field this command adds — see Run's own
// comment).
func TestRespondRefsParticipateInResponseIDSeed(t *testing.T) {
	t.Parallel()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }
	parentID := "XQ-axon-20260721-ref4"

	run := func(t *testing.T, refs ...string) (string, string) {
		t.Helper()
		mirrorDir := t.TempDir()
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(fixedNow)
		args := []string{"--result", "delivered"}
		for _, r := range refs {
			args = append(args, "--ref", r)
		}
		args = append(args, parentID)
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), args, io); code != 0 {
			t.Fatalf("respond: code=%d stderr=%s", code, errOut.String())
		}
		return extractResponseID(fake.calls[0].Files), fake.calls[0].OperationKey
	}

	idNoRefs, keyNoRefs := run(t)
	idWithRef, keyWithRef := run(t, "XH-beta-20260721-h001")
	if idNoRefs == idWithRef {
		t.Fatalf("expected DIFFERENT response ids for no-refs vs one ref, got the same id %q", idNoRefs)
	}
	if keyNoRefs == keyWithRef {
		t.Fatalf("expected DIFFERENT operation keys for no-refs vs one ref, got the same key %q", keyNoRefs)
	}
}

// TestRespondIdenticalRefsRepeatMintsIdenticalID is the OTHER half of the
// same requirement: an identical retry (same refs, same order) must land
// on the SAME id/key so the funnel's dedup branch finds it — refs
// participating in the seed must not defeat retry-safety.
func TestRespondIdenticalRefsRepeatMintsIdenticalID(t *testing.T) {
	t.Parallel()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }
	parentID := "XQ-axon-20260721-ref5"

	run := func(t *testing.T) (string, string) {
		t.Helper()
		mirrorDir := t.TempDir()
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(fixedNow)
		io, _, errOut := newIO()
		args := []string{
			"--result", "delivered",
			"--ref", "XH-beta-20260721-h001",
			"--ref", "XH-beta-20260721-h002",
			parentID,
		}
		if code := cmd.Run(context.Background(), args, io); code != 0 {
			t.Fatalf("respond: code=%d stderr=%s", code, errOut.String())
		}
		return extractResponseID(fake.calls[0].Files), fake.calls[0].OperationKey
	}

	id1, key1 := run(t)
	id2, key2 := run(t)
	if id1 != id2 {
		t.Fatalf("expected an IDENTICAL retry (same refs, same order) to mint the SAME response id, got %q vs %q", id1, id2)
	}
	if key1 != key2 {
		t.Fatalf("expected an IDENTICAL retry to reproduce the SAME operation key, got %q vs %q", key1, key2)
	}
}

// TestRespondRefOrderChangesResponseID pins the ordering decision: --ref
// values are folded into the seed in GIVEN order, not sorted, because
// refs[]'s written order is part of the document's own bytes.
func TestRespondRefOrderChangesResponseID(t *testing.T) {
	t.Parallel()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }
	parentID := "XQ-axon-20260721-ref6"

	run := func(t *testing.T, refs ...string) string {
		t.Helper()
		mirrorDir := t.TempDir()
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(fixedNow)
		args := []string{"--result", "delivered"}
		for _, r := range refs {
			args = append(args, "--ref", r)
		}
		args = append(args, parentID)
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), args, io); code != 0 {
			t.Fatalf("respond: code=%d stderr=%s", code, errOut.String())
		}
		return extractResponseID(fake.calls[0].Files)
	}

	forward := run(t, "XH-beta-20260721-h001", "XH-beta-20260721-h002")
	backward := run(t, "XH-beta-20260721-h002", "XH-beta-20260721-h001")
	if forward == backward {
		t.Fatalf("expected a different refs[] ORDER to mint a different response id (refs[] order is on the wire), got the same id %q", forward)
	}
}

// --- P2 (defects-fix-2026-08): unmet/standing/blocked_by ------------------

// respondWithRealValidation drives `a2a respond` through the SAME real
// schema.Load/validate.New/space.NewWriteFunnel stack
// TestRespondSetsToAsParentAuthorAndPassesSubmitValidation uses — never a
// fakeLifecycleFunnel stub — because this is the ordering guard: it must be
// possible for it to go red for a REAL reason (the funnel's own V2 pass
// refusing an envelope/v1 draft that carries a field only envelope/v2
// declares), not merely a stub agreeing to whatever it's handed.
func respondWithRealValidation(t *testing.T, parentID string, args []string) (code int, out, errOut, mirrorDir string, fakeHost *host.FakeHost) {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir = fx.Clone("beta")
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

	fakeHost = host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, validator, "0.1.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	cmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", manifest, hostCfg, lifecycleActorResolver("agent", "bot"))
	io, outBuf, errBuf := newIO()
	full := append([]string{}, args...)
	full = append(full, parentID)
	code = cmd.Run(context.Background(), full, io)
	return code, outBuf.String(), errBuf.String(), mirrorDir, fakeHost
}

// committedResponseContent reads back the ONE committed XS- response file
// from the branch the fake host received the push on — identical extraction
// idiom to TestRespondSetsToAsParentAuthorAndPassesSubmitValidation.
func committedResponseContent(t *testing.T, mirrorDir string, fakeHost *host.FakeHost) string {
	t.Helper()
	if len(fakeHost.Pushes) != 1 {
		t.Fatalf("expected exactly one PushBranch call, got %d", len(fakeHost.Pushes))
	}
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
	return runGitOutputForTest(t, mirrorDir, "show", branch+":"+responsePath)
}

// TestRespondResultPartialGenerationOrderingGuard is spec 02 AC4: it renders
// `--result partial` through the REAL production path (respondWithRealValidation)
// and validates the committed result — the test that would have caught this
// phase's fix order being flipped (schemas/templates/v2/response.md and the
// flags land BEFORE generationTable["response"] moves, per spec 02 §"The fix
// order is load-bearing"). Recorded mutation evidence for this file's own
// deviations report: run BEFORE the RespondCommand.Run `template.Render` call
// was given `EnvelopeSchema: "envelope/v2"`, this test failed red (the
// response rendered envelope/v1, whose unevaluatedProperties:false refused
// the `standing` key at submit) — see the deviations report for the captured
// stderr.
func TestRespondResultPartialGenerationOrderingGuard(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-grd1"
	code, out, errOut, mirrorDir, fakeHost := respondWithRealValidation(t, parentID, []string{"--result", "partial", "--standing", "provisional"})
	if code != 0 {
		t.Fatalf("respond --result partial --standing provisional: code=%d, want 0; stdout=%s stderr=%s", code, out, errOut)
	}
	content := committedResponseContent(t, mirrorDir, fakeHost)
	if !strings.Contains(content, "schema: envelope/v2") {
		t.Fatalf("expected the committed response to carry schema: envelope/v2, got:\n%s", content)
	}
	if !strings.Contains(content, "standing: provisional") {
		t.Fatalf("expected the committed response to carry standing: provisional, got:\n%s", content)
	}
}

// TestRespondPartialWithNoneOfTheThreeIsRefused is spec 02 AC2's refusal
// half: envelope/v2/response's own conditional requires (unmet AND
// blocked_by) OR a non-authoritative standing on result partial/cannot — a
// bare `--result partial` satisfies neither branch and must be refused by
// the real V2 validation pass, not merely accepted and left to drift.
func TestRespondPartialWithNoneOfTheThreeIsRefused(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-grd2"
	code, _, errOut, _, _ := respondWithRealValidation(t, parentID, []string{"--result", "partial"})
	if code == 0 {
		t.Fatalf("expected `--result partial` with none of unmet/standing/blocked_by to be refused, got code=0")
	}
	if errOut == "" {
		t.Fatalf("expected a refusal message naming the real condition, got empty stderr")
	}
}

// TestRespondPartialWithStandingAuthoritativeAloneIsRefused is spec 02 AC2's
// sharpest edge: a bare `standing: authoritative` supplies no new signal
// (response.schema.json's own `then` branch description) and must NOT
// satisfy the conditional — the schema's `anyOf` second arm requires
// `standing` to be `provisional` or `advisory`, never merely present.
func TestRespondPartialWithStandingAuthoritativeAloneIsRefused(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-grd3"
	code, _, errOut, _, _ := respondWithRealValidation(t, parentID, []string{"--result", "partial", "--standing", "authoritative"})
	if code == 0 {
		t.Fatalf("expected `--result partial --standing authoritative` (alone) to be refused, got code=0")
	}
	if errOut == "" {
		t.Fatalf("expected a refusal message, got empty stderr")
	}
}

// TestRespondPartialWithUnmetAndBlockedByPasses is spec 02 AC1/AC2's other
// passing branch: `unmet` + `blocked_by` (both required by the schema's
// first `anyOf` arm) validates without any `standing` override.
func TestRespondPartialWithUnmetAndBlockedByPasses(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-grd4"
	code, out, errOut, mirrorDir, fakeHost := respondWithRealValidation(t, parentID, []string{
		"--result", "partial", "--unmet", "2", "--blocked-by", "out-of-scope:seomatrix:decision",
	})
	if code != 0 {
		t.Fatalf("respond --result partial --unmet 2 --blocked-by out-of-scope:seomatrix:decision: code=%d, want 0; stdout=%s stderr=%s", code, out, errOut)
	}
	content := committedResponseContent(t, mirrorDir, fakeHost)
	if !strings.Contains(content, "unmet:") || !strings.Contains(content, "- 2") {
		t.Fatalf("expected the committed response to carry unmet: [2], got:\n%s", content)
	}
	if !strings.Contains(content, "reason_code: out-of-scope") || !strings.Contains(content, "owner: seomatrix") || !strings.Contains(content, "needs: decision") {
		t.Fatalf("expected the committed response to carry the full blocked_by object, got:\n%s", content)
	}
}

// TestRespondBlockedByRequiresAllThreeSegments is a CLI-level format guard
// (exit 2, before any funnel call) for the deviation this phase's report
// carries: the spec's own flag-shape text names only <reason_code>:<owner>,
// but envelope/v2/response.schema.json's `blocked_by` requires `needs` too
// (`"required": ["reason_code", "owner", "needs"]`) — a two-segment value
// can never author an object that validates.
func TestRespondBlockedByRequiresAllThreeSegments(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-grd5"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--blocked-by", "out-of-scope:seomatrix", parentID}, io)
	if code != 2 {
		t.Fatalf("expected a two-segment --blocked-by to be refused with exit 2, got code=%d stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for a malformed --blocked-by, got %d calls", len(fake.calls))
	}
}

// TestRespondBlockedByInvalidReasonCodeRefused is lifecycleParseBlockedBy's
// own reason_code enum guard (schema's own vocabulary — split-required|
// security-concern|out-of-scope|duplicate|other).
func TestRespondBlockedByInvalidReasonCodeRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-grd8"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--blocked-by", "bogus-reason:seomatrix:bytes", parentID}, io)
	if code != 2 {
		t.Fatalf("expected an invalid reason_code to be refused with exit 2, got code=%d stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for an invalid reason_code, got %d calls", len(fake.calls))
	}
}

// TestRespondBlockedByInvalidNeedsRefused is lifecycleParseBlockedBy's own
// `needs` enum guard (bytes|judgement|decision).
func TestRespondBlockedByInvalidNeedsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-grd9"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--blocked-by", "out-of-scope:seomatrix:bogus-needs", parentID}, io)
	if code != 2 {
		t.Fatalf("expected an invalid needs value to be refused with exit 2, got code=%d stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for an invalid needs value, got %d calls", len(fake.calls))
	}
}

// TestRespondInvalidStandingValueRefused is the CLI-level enum guard on
// --standing (distinct from TestRespondPartialWithStandingAuthoritative
// AloneIsRefused, which exercises the SCHEMA's own conditional on a
// schema-valid value).
func TestRespondInvalidStandingValueRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-grda"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--standing", "bogus", parentID}, io)
	if code != 2 {
		t.Fatalf("expected an invalid --standing value to be refused with exit 2, got code=%d stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for an invalid --standing value, got %d calls", len(fake.calls))
	}
}

// TestRespondUnmetDuplicateIndexRefused is lifecycleParseUnmet's own
// mutation-tested guard (see this phase's mutation_evidence): a repeated
// --unmet index is a caller error, not a silent dedup.
func TestRespondUnmetDuplicateIndexRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-grd6"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "1", "--unmet", "1", "--standing", "provisional", parentID}, io)
	if code != 2 {
		t.Fatalf("expected a duplicate --unmet index to be refused with exit 2, got code=%d stderr=%s", code, errOut.String())
	}
}

// TestRespondNoFlagsOmitsAllThreeKeys is P-1's own discipline carried into
// this phase's new fields: an `a2a respond` call that never uses --unmet/
// --standing/--blocked-by must not declare any of them — absence stays
// absence, never a placeholder or a silent default.
func TestRespondNoFlagsOmitsAllThreeKeys(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-grd7"
	code, out, errOut, mirrorDir, fakeHost := respondWithRealValidation(t, parentID, []string{"--result", "answered"})
	if code != 0 {
		t.Fatalf("respond --result answered: code=%d, want 0; stdout=%s stderr=%s", code, out, errOut)
	}
	content := committedResponseContent(t, mirrorDir, fakeHost)
	for _, key := range []string{"unmet:", "standing:", "blocked_by:"} {
		if strings.Contains(content, key) {
			t.Fatalf("expected no %s key on a flagless respond, got:\n%s", key, content)
		}
	}
}

// --- verify --verdict / event/v2 authoring (P6 wave C, T5's discharge) -----

// lifecycleVerdictProbe/lifecycleEventProbe decode the wire shape these
// tests assert on — DECODED values, never strings.Contains over rendered
// bytes (this epic's own paid-for trap).
type lifecycleVerdictProbe struct {
	Index      int    `yaml:"index"`
	Criterion  string `yaml:"criterion,omitempty"`
	Verdict    string `yaml:"verdict"`
	CauseOwner string `yaml:"cause_owner"`
}

type lifecycleEventProbe struct {
	Schema     string `yaml:"schema"`
	Transition string `yaml:"transition"`
	Subject    string `yaml:"subject"`
	// State is the evaluator receipt (§5.2.2) — added alongside Verdicts so
	// B24's own headline claim ("the same record") can be proven on a
	// DECODED value, not a strings.Contains over rendered bytes (rails:
	// "assert on decoded values, never a substring of rendered bytes").
	State string `yaml:"state,omitempty"`
	// Verdicts is a POINTER so these tests can distinguish "the verdicts key
	// is absent" (nil) from "the verdicts key is present and empty" (non-nil,
	// len 0) — the exact distinction the schema's own conditional makes and
	// the exact distinction cmd_lifecycle.go's own lifecycleEventDoc.Verdicts
	// field exists to preserve.
	Verdicts *[]lifecycleVerdictProbe `yaml:"verdicts"`
}

// findEventByTransition decodes every file in files and returns the first
// whose transition matches want, failing the test if none matches.
func findEventByTransition(t *testing.T, files []space.FileWrite, want string) lifecycleEventProbe {
	t.Helper()
	for _, fw := range files {
		var probe lifecycleEventProbe
		if err := yaml.Unmarshal(fw.Content, &probe); err != nil {
			t.Fatalf("decode event %s: %v", fw.Path, err)
		}
		if probe.Transition == want {
			return probe
		}
	}
	t.Fatalf("no %s event found among %d files", want, len(files))
	return lifecycleEventProbe{}
}

// lifecycleManifestAtFloor returns lifecycleManifest() with min_binary_version
// set to floor — the CC-085 field cmd_lifecycle.go's lifecycleEventSchema
// reads to decide event/v1 vs event/v2, mirroring internal/contract/
// publication_plan.go's own authoring-floor split.
func lifecycleManifestAtFloor(floor string) space.Manifest {
	m := lifecycleManifest()
	m.MinBinaryVersion = floor
	return m
}

// TestVerifyBelowFloorStaysEventV1AndOmitsVerdicts is this wave's own
// regression pin: a space that has never set (or has set below the release
// floor) min_binary_version keeps authoring event/v1 verify/close events —
// T5's conditional requirement never applies to them, and no `verdicts` key
// is written at all (event/v1's own schema has no such property).
func TestVerifyBelowFloorStaysEventV1AndOmitsVerdicts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf01"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	// lifecycleManifest() sets no min_binary_version at all — the common,
	// pre-this-wave case.
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{responseID}, io); code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	files := fake.calls[0].Files
	verify := findEventByTransition(t, files, "verify")
	close_ := findEventByTransition(t, files, "close")
	if verify.Schema != "event/v1" || close_.Schema != "event/v1" {
		t.Fatalf("expected event/v1 below the floor, got verify=%q close=%q", verify.Schema, close_.Schema)
	}
	if verify.Verdicts != nil || close_.Verdicts != nil {
		t.Fatalf("expected NO verdicts key on event/v1, got verify=%v close=%v", verify.Verdicts, close_.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when --verdict was never used, got %q", fake.calls[0].OperationKey)
	}
}

// TestVerifyAtFloorAuthorsEventV2WithVerdicts is Part 1 + Part 2's positive
// case: at/above contract.ContractPublicationFloor, `a2a verify --verdict
// 0:met:axon` authors event/v2 on BOTH the verify event and the D-024
// convenience close riding in the same PR, both carrying the SAME
// per-criterion judgement.
func TestVerifyAtFloorAuthorsEventV2WithVerdicts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf02"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	files := fake.calls[0].Files
	verify := findEventByTransition(t, files, "verify")
	close_ := findEventByTransition(t, files, "close")
	if verify.Schema != "event/v2" || close_.Schema != "event/v2" {
		t.Fatalf("expected event/v2 at/above the floor, got verify=%q close=%q", verify.Schema, close_.Schema)
	}
	for name, probe := range map[string]lifecycleEventProbe{"verify": verify, "close": close_} {
		if probe.Verdicts == nil {
			t.Fatalf("%s: expected a verdicts key, got none", name)
		}
		got := *probe.Verdicts
		if len(got) != 1 || got[0].Index != 0 || got[0].Verdict != "met" || got[0].CauseOwner != "axon" {
			t.Fatalf("%s: verdicts = %+v, want [{0 met axon}]", name, got)
		}
	}
	if fake.calls[0].OperationKey == "" {
		t.Fatal("expected a non-empty operation key once --verdict carried content")
	}
}

// TestVerifyEmptyVerdictsArrayWhenNoneSupplied is the AC the schema's own
// description protects: at/above the floor, a plain `a2a verify` with NO
// --verdict flags still authors `verdicts: []` (present, empty) on both the
// verify and the D-024 close — never an absent key, which the schema's own
// conditional requirement would refuse — proving a parent carrying no
// judged criteria stays closable.
func TestVerifyEmptyVerdictsArrayWhenNoneSupplied(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf03"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	files := fake.calls[0].Files
	verify := findEventByTransition(t, files, "verify")
	close_ := findEventByTransition(t, files, "close")
	if verify.Verdicts == nil || len(*verify.Verdicts) != 0 {
		t.Fatalf("verify: expected verdicts: [] (present, empty), got %v", verify.Verdicts)
	}
	if close_.Verdicts == nil || len(*close_.Verdicts) != 0 {
		t.Fatalf("close: expected verdicts: [] (present, empty), got %v", close_.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when --verdict was never used, got %q", fake.calls[0].OperationKey)
	}
}

// TestVerifyRejectsMalformedVerdictFlag exercises every locally-refused
// --verdict shape (exit 2, funnel never called): the flag is refused before
// any legality check or write, the same discipline decline's --reason and
// block's --refs already carry.
func TestVerifyRejectsMalformedVerdictFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []string
	}{
		{"missing colons", []string{"0met"}},
		{"only one colon", []string{"0:met"}},
		{"non-integer index", []string{"x:met:axon"}},
		{"negative index", []string{"-1:met:axon"}},
		{"unknown verdict enum", []string{"0:maybe:axon"}},
		{"empty cause_owner", []string{"0:met:"}},
		{"whitespace-only cause_owner", []string{"0:met:  "}},
		// Two --verdict entries naming the SAME index: not "last one wins" —
		// see lifecycleParseVerdicts's own doc comment on why an ambiguous
		// judgement would also break the sort-by-index canonicalisation
		// operation.Verify's determinism relies on.
		{"duplicate index", []string{"0:met:axon", "0:unmet:beta"}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			parentID := fmt.Sprintf("XQ-axon-20260721-mvf%d", i)
			seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
			responseID := respondFlow(t, mirrorDir, parentID, "beta")

			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			io, _, _ := newIO()
			var args []string
			for _, v := range test.values {
				args = append(args, "--verdict", v)
			}
			args = append(args, responseID)
			code := cmd.Run(context.Background(), args, io)
			if code != 2 {
				t.Fatalf("code = %d, want 2 for --verdict %v", code, test.values)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("funnel called for a locally-refused --verdict %v", test.values)
			}
		})
	}
}

// TestVerifyVerdictBelowFloorRefusedLocally: --verdict is meaningless below
// the floor (event/v1 carries no such field) — this must be a NAMED local
// refusal, never a silent drop of the caller's judgements.
func TestVerifyVerdictBelowFloorRefusedLocally(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf05"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", responseID}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a --verdict refused below the floor")
	}
	if !strings.Contains(errOut.String(), "min_binary_version") {
		t.Fatalf("refusal does not name the real condition: %s", errOut.String())
	}
}

// TestVerifyDifferentVerdictsMintDifferentOperationKeys is the determinism
// requirement, driven end to end through the CLI (not just operation.Verify
// in isolation): two `a2a verify` invocations against the SAME response with
// DIFFERENT --verdict content must NOT collide onto one operation key, or
// the second invocation's judgement is silently treated as a retry of the
// first and never committed.
func TestVerifyDifferentVerdictsMintDifferentOperationKeys(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf06"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	run := func(verdictFlag string) string {
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--verdict", verdictFlag, responseID}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		return fake.calls[0].OperationKey
	}

	met := run("0:met:axon")
	unmet := run("0:unmet:axon")
	if met == unmet {
		t.Fatalf("differing verdicts minted the same operation key %q", met)
	}
}

// TestVerifyReorderedVerdictFlagsMintTheSameOperationKey pins the
// canonical-by-index decision: the SAME judgement set, given via --verdict
// in a DIFFERENT flag order, mints the IDENTICAL operation key — an
// identical retry (or a caller assembling flags from an unordered source)
// must not mint a second branch for one already-recorded judgement set.
func TestVerifyReorderedVerdictFlagsMintTheSameOperationKey(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-vf07"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	run := func(verdictFlags ...string) (string, []lifecycleVerdictProbe) {
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		var args []string
		for _, v := range verdictFlags {
			args = append(args, "--verdict", v)
		}
		args = append(args, responseID)
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), args, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		verify := findEventByTransition(t, fake.calls[0].Files, "verify")
		if verify.Verdicts == nil {
			t.Fatal("expected a verdicts key")
		}
		return fake.calls[0].OperationKey, *verify.Verdicts
	}

	givenKey, givenVerdicts := run("0:met:axon", "1:unmet:beta")
	reorderedKey, reorderedVerdicts := run("1:unmet:beta", "0:met:axon")
	if givenKey != reorderedKey {
		t.Fatalf("reordering --verdict flags (same judgement set) changed the operation key: %q vs %q", givenKey, reorderedKey)
	}
	// The WRITTEN array itself must also be canonical (by index), not just
	// the derived operation key — a caller reading the committed event
	// should never see two different orderings for one identical judgement
	// set.
	want := []lifecycleVerdictProbe{{Index: 0, Verdict: "met", CauseOwner: "axon"}, {Index: 1, Verdict: "unmet", CauseOwner: "beta"}}
	for name, got := range map[string][]lifecycleVerdictProbe{"given order": givenVerdicts, "reordered": reorderedVerdicts} {
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("%s: written verdicts = %+v, want %+v (canonical by index)", name, got, want)
		}
	}
}

// TestVerifyEndToEndAuthorsEventV2ThatPassesSubmitValidation is item 2 of
// this wave's advisor pass: every OTHER --verdict test above drives
// fakeLifecycleFunnel, which stops at recording req.Files and never proves
// the v2 event survives the REAL funnel's own stamp/SubmitValidator (V2)
// pipeline (space/funnel.go's own doc comment: both run BEFORE the commit,
// funnel.go:400-403) — the exact "shipped a field nothing decodes/reaches"
// failure class this epic keeps finding in itself (spec 06 §11's own C7
// amendment). This test uses the real space.WriteFunnel, a real
// SubmitValidatorAdapter wired the same way
// TestRespondSetsToAsParentAuthorAndPassesSubmitValidation (above) proves
// respond's own v2-adjacent write, and reads the ACTUALLY PUSHED git blob —
// not the fake funnel's in-memory req.Files — for both the verify event and
// the D-024 close riding the same PR.
func TestVerifyEndToEndAuthorsEventV2ThatPassesSubmitValidation(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("axon")

	parentID := "XQ-axon-20260721-vfe2"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := lifecycleManifestAtFloor("0.19.0")
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	fakeHost := host.NewFakeHost()
	// The funnel's own CC-085 guard compares ITS OWN binary version against
	// the space's min_binary_version (req.MinBinaryVersion, set from
	// manifest.MinBinaryVersion by lifecycleDeps.buildRequest) — a binary
	// older than the floor it is about to author against is refused before
	// any write, so this must be at/above "0.19.0" too, not the "0.1.0"
	// every OTHER end-to-end test in this file uses (they stay below the
	// floor on purpose).
	funnel := space.NewWriteFunnel(fakeHost, validator, "0.19.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	cmd := cli.NewVerifyCommand(funnel, mirrorDir, "fixture-space", "axon", manifest, hostCfg, lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", responseID}, io)
	if code != 0 {
		t.Fatalf("verify: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Pushes) != 1 {
		t.Fatalf("expected exactly one PushBranch call (real funnel stamped/validated/committed), got %d", len(fakeHost.Pushes))
	}

	branch := fakeHost.Pushes[0].Branch
	changed := gitDiffNames(t, mirrorDir, "main", branch)
	var verifyPath, closePath string
	for _, p := range changed {
		if !strings.HasSuffix(p, ".yaml") {
			continue
		}
		content := runGitOutputForTest(t, mirrorDir, "show", branch+":"+p)
		var probe lifecycleEventProbe
		if err := yaml.Unmarshal([]byte(content), &probe); err != nil {
			t.Fatalf("decode pushed event %s: %v", p, err)
		}
		switch probe.Transition {
		case "verify":
			verifyPath = p
		case "close":
			closePath = p
		}
	}
	if verifyPath == "" || closePath == "" {
		t.Fatalf("expected both a verify and a close event among the pushed files, got %v", changed)
	}

	for _, p := range []string{verifyPath, closePath} {
		content := runGitOutputForTest(t, mirrorDir, "show", branch+":"+p)
		if !strings.Contains(content, "schema: event/v2") {
			t.Fatalf("pushed event %s is not event/v2:\n%s", p, content)
		}
		var probe lifecycleEventProbe
		if err := yaml.Unmarshal([]byte(content), &probe); err != nil {
			t.Fatalf("decode pushed event %s: %v", p, err)
		}
		if probe.Verdicts == nil || len(*probe.Verdicts) != 1 {
			t.Fatalf("pushed event %s: verdicts = %v, want one entry (survived the real funnel's own stamp/validate pipeline):\n%s", p, probe.Verdicts, content)
		}
		got := (*probe.Verdicts)[0]
		if got.Index != 0 || got.Verdict != "met" || got.CauseOwner != "axon" {
			t.Fatalf("pushed event %s: verdicts[0] = %+v, want {0 met axon}", p, got)
		}
	}
}

// --- standalone `a2a close` --verdict / event/v2 authoring (B24) -----------
//
// B24 (docs/features/archive/agent-exchange-2026-08/epic-backlog.md): the
// standalone `a2a close <parent-id>` verb — the shared table-driven
// LifecycleCommand.Run, NOT VerifyCommand's own D-024 convenience close —
// used to author event/v1 unconditionally, so the SAME artifact closed via
// `a2a verify`'s convenience close (event/v2, verdicts[]) and via `a2a
// close` directly (event/v1, no verdicts) produced two disagreeing records.
// These tests mirror the "--- verify --verdict / event/v2 authoring ---"
// section above one-for-one, proving the standalone verb now carries the
// SAME guarantees, PLUS two tests (Test*OtherVerbUnaffected*) proving the
// other 14 table rows are untouched.

// TestCloseBelowFloorStaysEventV1AndOmitsVerdicts mirrors
// TestVerifyBelowFloorStaysEventV1AndOmitsVerdicts for the standalone verb.
func TestCloseBelowFloorStaysEventV1AndOmitsVerdicts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf01"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	// lifecycleManifest() sets no min_binary_version — the common,
	// pre-this-wave case.
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{parentID}, io); code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	closeEvent := findEventByTransition(t, fake.calls[0].Files, "close")
	if closeEvent.Schema != "event/v1" {
		t.Fatalf("expected event/v1 below the floor, got %q", closeEvent.Schema)
	}
	if closeEvent.Verdicts != nil {
		t.Fatalf("expected NO verdicts key on event/v1, got %v", closeEvent.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when --verdict was never used, got %q", fake.calls[0].OperationKey)
	}
}

// TestCloseAtFloorAuthorsEventV2WithVerdicts mirrors
// TestVerifyAtFloorAuthorsEventV2WithVerdicts for the standalone verb.
func TestCloseAtFloorAuthorsEventV2WithVerdicts(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf02"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", parentID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	closeEvent := findEventByTransition(t, fake.calls[0].Files, "close")
	if closeEvent.Schema != "event/v2" {
		t.Fatalf("expected event/v2 at/above the floor, got %q", closeEvent.Schema)
	}
	if closeEvent.Verdicts == nil {
		t.Fatal("expected a verdicts key, got none")
	}
	got := *closeEvent.Verdicts
	if len(got) != 1 || got[0].Index != 0 || got[0].Verdict != "met" || got[0].CauseOwner != "axon" {
		t.Fatalf("verdicts = %+v, want [{0 met axon}]", got)
	}
	if fake.calls[0].OperationKey == "" {
		t.Fatal("expected a non-empty operation key once --verdict carried content")
	}
}

// TestCloseEmptyVerdictsArrayWhenNoneSupplied mirrors
// TestVerifyEmptyVerdictsArrayWhenNoneSupplied for the standalone verb: at/
// above the floor, a plain `a2a close` with no --verdict flags still
// authors `verdicts: []` (present, empty), never an absent key.
func TestCloseEmptyVerdictsArrayWhenNoneSupplied(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf03"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{parentID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	closeEvent := findEventByTransition(t, fake.calls[0].Files, "close")
	if closeEvent.Verdicts == nil || len(*closeEvent.Verdicts) != 0 {
		t.Fatalf("expected verdicts: [] (present, empty), got %v", closeEvent.Verdicts)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key when --verdict was never used, got %q", fake.calls[0].OperationKey)
	}
}

// TestCloseRejectsMalformedVerdictFlag mirrors
// TestVerifyRejectsMalformedVerdictFlag for the standalone verb.
func TestCloseRejectsMalformedVerdictFlag(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values []string
	}{
		{"missing colons", []string{"0met"}},
		{"only one colon", []string{"0:met"}},
		{"non-integer index", []string{"x:met:axon"}},
		{"negative index", []string{"-1:met:axon"}},
		{"unknown verdict enum", []string{"0:maybe:axon"}},
		{"empty cause_owner", []string{"0:met:"}},
		{"whitespace-only cause_owner", []string{"0:met:  "}},
		{"duplicate index", []string{"0:met:axon", "0:unmet:beta"}},
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			mirrorDir := t.TempDir()
			parentID := fmt.Sprintf("XQ-axon-20260721-mcf%d", i)
			seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
			_ = respondFlow(t, mirrorDir, parentID, "beta")

			fake := &fakeLifecycleFunnel{}
			cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
			io, _, _ := newIO()
			var args []string
			for _, v := range test.values {
				args = append(args, "--verdict", v)
			}
			args = append(args, parentID)
			code := cmd.Run(context.Background(), args, io)
			if code != 2 {
				t.Fatalf("code = %d, want 2 for --verdict %v", code, test.values)
			}
			if len(fake.calls) != 0 {
				t.Fatalf("funnel called for a locally-refused --verdict %v", test.values)
			}
		})
	}
}

// TestCloseVerdictBelowFloorRefusedLocally mirrors
// TestVerifyVerdictBelowFloorRefusedLocally for the standalone verb.
func TestCloseVerdictBelowFloorRefusedLocally(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf05"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", parentID}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a --verdict refused below the floor")
	}
	if !strings.Contains(errOut.String(), "min_binary_version") {
		t.Fatalf("refusal does not name the real condition: %s", errOut.String())
	}
}

// TestCloseDifferentVerdictsMintDifferentOperationKeys mirrors
// TestVerifyDifferentVerdictsMintDifferentOperationKeys for the standalone
// verb.
func TestCloseDifferentVerdictsMintDifferentOperationKeys(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf06"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	run := func(verdictFlag string) string {
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--verdict", verdictFlag, parentID}, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		return fake.calls[0].OperationKey
	}

	met := run("0:met:axon")
	unmet := run("0:unmet:axon")
	if met == unmet {
		t.Fatalf("differing verdicts minted the same operation key %q", met)
	}
}

// TestCloseReorderedVerdictFlagsMintTheSameOperationKey mirrors
// TestVerifyReorderedVerdictFlagsMintTheSameOperationKey for the standalone
// verb.
func TestCloseReorderedVerdictFlagsMintTheSameOperationKey(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-cf07"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	run := func(verdictFlags ...string) (string, []lifecycleVerdictProbe) {
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		var args []string
		for _, v := range verdictFlags {
			args = append(args, "--verdict", v)
		}
		args = append(args, parentID)
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), args, io)
		if code != 0 {
			t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
		}
		closeEvent := findEventByTransition(t, fake.calls[0].Files, "close")
		if closeEvent.Verdicts == nil {
			t.Fatal("expected a verdicts key")
		}
		return fake.calls[0].OperationKey, *closeEvent.Verdicts
	}

	givenKey, givenVerdicts := run("0:met:axon", "1:unmet:beta")
	reorderedKey, reorderedVerdicts := run("1:unmet:beta", "0:met:axon")
	if givenKey != reorderedKey {
		t.Fatalf("reordering --verdict flags (same judgement set) changed the operation key: %q vs %q", givenKey, reorderedKey)
	}
	want := []lifecycleVerdictProbe{{Index: 0, Verdict: "met", CauseOwner: "axon"}, {Index: 1, Verdict: "unmet", CauseOwner: "beta"}}
	for name, got := range map[string][]lifecycleVerdictProbe{"given order": givenVerdicts, "reordered": reorderedVerdicts} {
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("%s: written verdicts = %+v, want %+v (canonical by index)", name, got, want)
		}
	}
}

// TestCloseOperationKeyDiffersFromVerifysDrivenClose is the end-to-end
// regression pin for B24's actual finding: closing the SAME kind of
// artifact through `a2a verify`'s own D-024 convenience close and through
// `a2a close` directly must not be mistaken for one operation — this drives
// operation.Close (not operation.Verify) at the CLI boundary, complementing
// TestCloseKeyDiffersFromVerifyKeyForIdenticalInput's unit-level proof in
// internal/operation.
//
// Both commands are driven with the SAME parentID argument deliberately —
// `verify <parent-id>` resolves it to the parent's own single response
// (lifecycleResolveResponseID), but operation.Verify's own operationKey is
// derived from the RAW `targets` slice (the CLI argument), never the
// resolved response id. A first version of this test drove verify with the
// parent's own resolved RESPONSE id and close with the PARENT id, which are
// two different strings by construction — that version stayed green even
// with operation.Close's own encoder domain tag mutated to match Verify's
// (watched: mutating "close" -> "verify" in operation.Close reds the
// UNIT-level TestCloseKeyDiffersFromVerifyKeyForIdenticalInput but left this
// CLI-level test green), which made it prove nothing about domain
// separation at this boundary. Driving BOTH commands with the identical
// parentID string makes every OTHER input to the two key functions
// byte-identical (system, actor, targets/ids, verdicts) — the ONLY
// remaining variable is which function derived the key — so this test now
// reds under that same mutation.
//
// Neither command's fake funnel persists its write (fakeLifecycleFunnel
// only records the call; materializeFiles is never called on either
// result), so the second command evaluates legality against the exact same
// committed state the first one saw — a single `respond`, no `verify` and
// no `close` yet — independent of call order.
func TestCloseOperationKeyDiffersFromVerifysDrivenClose(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()

	parentID := "XQ-axon-20260721-cf08"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	verifyFake := &fakeLifecycleFunnel{}
	verifyCmd := cli.NewVerifyCommand(verifyFake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io1, _, errOut1 := newIO()
	if code := verifyCmd.Run(context.Background(), []string{"--verdict", "0:met:axon", parentID}, io1); code != 0 {
		t.Fatalf("verify: code = %d, want 0; stderr=%s", code, errOut1.String())
	}
	verifyOperationKey := verifyFake.calls[0].OperationKey

	closeFake := &fakeLifecycleFunnel{}
	closeCmd := cli.NewCloseCommand(closeFake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io2, _, errOut2 := newIO()
	if code := closeCmd.Run(context.Background(), []string{"--verdict", "0:met:axon", parentID}, io2); code != 0 {
		t.Fatalf("close: code = %d, want 0; stderr=%s", code, errOut2.String())
	}
	closeOperationKey := closeFake.calls[0].OperationKey

	if verifyOperationKey == "" || closeOperationKey == "" {
		t.Fatalf("expected non-empty operation keys, got verify=%q close=%q", verifyOperationKey, closeOperationKey)
	}
	if verifyOperationKey == closeOperationKey {
		t.Fatalf("verify's D-024 close and a standalone close minted the SAME operation key %q for the byte-identical (system, actor, targets, verdicts) tuple", verifyOperationKey)
	}
}

// TestOtherVerbRefusesVerdictFlagUnregistered is B24's own "thirteen [sic;
// the table carries 15 rows, 14 other than close] verbs must not grow the
// flag" requirement, proven the only way that actually tests it: an
// undefined flag on Go's own flag.FlagSet is a parse error under
// flag.ContinueOnError (parseArgsAnyOrder, cli.go), never a silently
// swallowed no-op or a positional argument — `ack` (an arbitrary other
// table row) must refuse `--verdict` at exit 2, funnel never called.
func TestOtherVerbRefusesVerdictFlagUnregistered(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-cf10"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", id}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (--verdict is not a registered flag on ack)", code)
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for an unregistered --verdict flag")
	}
}

// TestOtherVerbStaysEventV1AboveFloor proves the schema choice is
// TRANSITION-scoped (B24's own framing: "the schema choice has to be
// transition-scoped rather than applied to the loop"), not a blanket switch
// that would upgrade every LifecycleCommand.Run verb to event/v2 once a
// space crosses the floor — only `close` (SupportsVerdicts) does.
func TestOtherVerbStaysEventV1AboveFloor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-cf11"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewAckCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{id}, io); code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	ackEvent := findEventByTransition(t, fake.calls[0].Files, "acknowledge")
	if ackEvent.Schema != "event/v1" {
		t.Fatalf("expected ack to stay event/v1 above the floor (transition-scoped, not loop-applied), got %q", ackEvent.Schema)
	}
	if fake.calls[0].OperationKey != "" {
		t.Fatalf("expected no operation key on a verb that never carries verdicts, got %q", fake.calls[0].OperationKey)
	}
}

// TestCloseEndToEndAuthorsEventV2ThatPassesSubmitValidation is the
// standalone verb's own counterpart to
// TestVerifyEndToEndAuthorsEventV2ThatPassesSubmitValidation above: proves
// the v2 event `a2a close --verdict` authors survives the REAL funnel's own
// stamp/SubmitValidator (V2) pipeline, not just the fake funnel's in-memory
// req.Files.
func TestCloseEndToEndAuthorsEventV2ThatPassesSubmitValidation(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("axon")

	parentID := "XQ-axon-20260721-cfe1"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	_ = respondFlow(t, mirrorDir, parentID, "beta")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := lifecycleManifestAtFloor("0.19.0")
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, validator, "0.19.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	cmd := cli.NewCloseCommand(funnel, mirrorDir, "fixture-space", "axon", manifest, hostCfg, lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:axon", parentID}, io)
	if code != 0 {
		t.Fatalf("close: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Pushes) != 1 {
		t.Fatalf("expected exactly one PushBranch call (real funnel stamped/validated/committed), got %d", len(fakeHost.Pushes))
	}

	branch := fakeHost.Pushes[0].Branch
	changed := gitDiffNames(t, mirrorDir, "main", branch)
	var closePath string
	for _, p := range changed {
		if !strings.HasSuffix(p, ".yaml") {
			continue
		}
		content := runGitOutputForTest(t, mirrorDir, "show", branch+":"+p)
		var probe lifecycleEventProbe
		if err := yaml.Unmarshal([]byte(content), &probe); err != nil {
			t.Fatalf("decode pushed event %s: %v", p, err)
		}
		if probe.Transition == "close" {
			closePath = p
		}
	}
	if closePath == "" {
		t.Fatalf("expected a close event among the pushed files, got %v", changed)
	}

	content := runGitOutputForTest(t, mirrorDir, "show", branch+":"+closePath)
	if !strings.Contains(content, "schema: event/v2") {
		t.Fatalf("pushed close event is not event/v2:\n%s", content)
	}
	var probe lifecycleEventProbe
	if err := yaml.Unmarshal([]byte(content), &probe); err != nil {
		t.Fatalf("decode pushed close event: %v", err)
	}
	if probe.Verdicts == nil || len(*probe.Verdicts) != 1 {
		t.Fatalf("pushed close event: verdicts = %v, want one entry (survived the real funnel's own stamp/validate pipeline):\n%s", probe.Verdicts, content)
	}
	got := (*probe.Verdicts)[0]
	if got.Index != 0 || got.Verdict != "met" || got.CauseOwner != "axon" {
		t.Fatalf("pushed close event: verdicts[0] = %+v, want {0 met axon}", got)
	}
}

// --- defects-fix-2026-08 P3: "a criterion has a name" -----------------

// zzzResponseHeaderV2 is the base frontmatter every schema-level P3 case
// below shares — schemas/envelope/v2 response's own required fields plus
// `parent`/`result` (mirrors internal/schema's own responseV2Header
// fixture shape, off this phase's allowlist, reproduced here so this
// package's own schema-fidelity proof does not need that file touched).
const p3ResponseHeaderV2 = `
schema: envelope/v2
id: XS-axon-20260808-p9d3
type: response
title: A valid v2 response
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260808-k3f9
actor: {kind: agent, name: codex}
created: "2026-08-08T08:40:00Z"
priority: p3
blocking: true
classification: internal
parent: XW-axon-20260808-p9d3
result: answered
`

func p3ValidateResponse(t *testing.T, doc string) []schema.FieldViolation {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	instance, err := schema.DecodeYAMLInstance([]byte(doc))
	if err != nil {
		t.Fatalf("DecodeYAMLInstance: %v", err)
	}
	violations, err := corpus.ValidateEnvelope("response", "envelope/v2", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	return violations
}

func p3ValidateEvent(t *testing.T, doc string) []schema.FieldViolation {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	instance, err := schema.DecodeYAMLInstance([]byte(doc))
	if err != nil {
		t.Fatalf("DecodeYAMLInstance: %v", err)
	}
	violations, err := corpus.ValidateEvent("v2", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	return violations
}

// TestSchemaAcceptanceCriteriaBothShapesValidate is spec 03 AC3/AC8's
// positive half (T2): schemas/envelope/v2/base.schema.json's
// `acceptance_criteria[]` accepts EITHER the plain-string (ordinal) form or
// the `{id, text}` (id-addressed) form, each internally homogeneous.
func TestSchemaAcceptanceCriteriaBothShapesValidate(t *testing.T) {
	t.Parallel()
	t.Run("string form", func(t *testing.T) {
		t.Parallel()
		doc := p3ResponseHeaderV2 + "acceptance_criteria:\n  - \"plain string form\"\n"
		if v := p3ValidateResponse(t, doc); len(v) != 0 {
			t.Fatalf("string-form acceptance_criteria refused: %+v", v)
		}
	})
	t.Run("id/text object form", func(t *testing.T) {
		t.Parallel()
		doc := p3ResponseHeaderV2 + "acceptance_criteria:\n  - id: ac1\n    text: \"object form\"\n"
		if v := p3ValidateResponse(t, doc); len(v) != 0 {
			t.Fatalf("object-form acceptance_criteria refused: %+v", v)
		}
	})
}

// TestSchemaAcceptanceCriteriaMixedArrayRefused is spec 03 AC8: a MIXED
// acceptance_criteria array (one string, one {id,text} object) has no
// single addressing mode and is refused.
//
// TEETH: this test was watched RED before base.schema.json's `anyOf`
// widening landed (a plain `items: {type: string}` schema silently accepts
// only the first entry's shape and produces zero violations for the
// second) — mutation_evidence in this phase's own report reproduces that
// red on demand by reverting the schema edit.
func TestSchemaAcceptanceCriteriaMixedArrayRefused(t *testing.T) {
	t.Parallel()
	doc := p3ResponseHeaderV2 + "acceptance_criteria:\n  - \"plain string\"\n  - id: ac1\n    text: \"object form\"\n"
	v := p3ValidateResponse(t, doc)
	if len(v) == 0 {
		t.Fatal("expected a mixed acceptance_criteria array to be refused")
	}
	for _, fv := range v {
		if !strings.HasPrefix(fv.Path, "acceptance_criteria") {
			t.Fatalf("violation does not name acceptance_criteria: %+v", v)
		}
	}
}

// TestSchemaUnmetObjectFormValidates/TestSchemaUnmetMixedArrayRefused prove
// response.schema.json's `unmet[]` item-type widening (spec 03 T2: "an
// item-TYPE change, not a new field") the same way as acceptance_criteria
// above.
func TestSchemaUnmetObjectFormValidates(t *testing.T) {
	t.Parallel()
	doc := p3ResponseHeaderV2 + "unmet:\n  - criterion: ac1\n"
	if v := p3ValidateResponse(t, doc); len(v) != 0 {
		t.Fatalf("unmet {criterion} form refused: %+v", v)
	}
}

func TestSchemaUnmetMixedArrayRefused(t *testing.T) {
	t.Parallel()
	doc := p3ResponseHeaderV2 + "unmet:\n  - 0\n  - criterion: ac1\n"
	v := p3ValidateResponse(t, doc)
	if len(v) == 0 {
		t.Fatal("expected a mixed unmet array (one int, one {criterion}) to be refused")
	}
}

// TestSchemaResidueCriterionMutualExclusivity proves T2's "residue[].
// criterion and criterion_index are mutually exclusive alternatives —
// naming both in one entry is refused" for all three cells: both named,
// criterion-only, neither named.
func TestSchemaResidueCriterionMutualExclusivity(t *testing.T) {
	t.Parallel()
	t.Run("both named is refused", func(t *testing.T) {
		t.Parallel()
		doc := p3ResponseHeaderV2 + "residue:\n  - criterion_index: 0\n    criterion: ac1\n    carried_to: XS-axon-20260809-q7d2\n"
		if v := p3ValidateResponse(t, doc); len(v) == 0 {
			t.Fatal("expected residue naming both criterion_index and criterion to be refused")
		}
	})
	t.Run("criterion only is accepted", func(t *testing.T) {
		t.Parallel()
		doc := p3ResponseHeaderV2 + "residue:\n  - criterion: ac1\n    carried_to: XS-axon-20260809-q7d2\n"
		if v := p3ValidateResponse(t, doc); len(v) != 0 {
			t.Fatalf("residue criterion-only refused: %+v", v)
		}
	})
	t.Run("neither named is refused", func(t *testing.T) {
		t.Parallel()
		doc := p3ResponseHeaderV2 + "residue:\n  - carried_to: XS-axon-20260809-q7d2\n"
		if v := p3ValidateResponse(t, doc); len(v) == 0 {
			t.Fatal("expected residue naming neither criterion_index nor criterion to be refused")
		}
	})
}

// TestSchemaVerdictsCriterionMutualExclusivity is spec 03 AC6, event/v2's
// own side of the identical rule.
//
// TEETH: "both named is refused" was watched RED before event.schema.json's
// allOf if/then (`properties: {criterion: false}`) landed — a bare
// `oneOf: [{required:[index]},{required:[criterion]}]` (this phase's first
// draft) DOES refuse it too, but classifies as an unclassified
// `other:*kind.OneOf` FieldViolation keyword rather than the existing
// `falseSchema`/SCH-003 code every other "field this schema forbids" case
// already uses — reverting to that shape keeps this test green (len(v) is
// still > 0) while silently regressing the registry-code mapping, which is
// why the mutation this phase actually ran was the KEYWORD, not the count
// (see mutation_evidence in this phase's own report).
func TestSchemaVerdictsCriterionMutualExclusivity(t *testing.T) {
	t.Parallel()
	header := `{
  "schema": "event/v2",
  "event": "01ARZ3NDEKTSV4RRFFQ69G5FAV",
  "space": "getvisa",
  "subject": "XS-axon-20260808-p9d3",
  "transition": "verify",
  "actor": {"kind": "agent", "name": "codex", "system": "axon"},
  "at": "2026-08-08T08:40:00Z"`
	t.Run("criterion only is accepted", func(t *testing.T) {
		t.Parallel()
		doc := header + `, "verdicts": [{"criterion": "ac1", "verdict": "met", "cause_owner": "axon"}]}`
		if v := p3ValidateEvent(t, doc); len(v) != 0 {
			t.Fatalf("verdicts criterion-only refused: %+v", v)
		}
	})
	t.Run("both named is refused", func(t *testing.T) {
		t.Parallel()
		doc := header + `, "verdicts": [{"index": 0, "criterion": "ac1", "verdict": "met", "cause_owner": "axon"}]}`
		v := p3ValidateEvent(t, doc)
		if len(v) == 0 {
			t.Fatal("expected verdicts naming both index and criterion to be refused")
		}
		var sawClassified bool
		for _, fv := range v {
			if fv.Keyword == "falseSchema" || fv.Keyword == "required" {
				sawClassified = true
			}
			if strings.HasPrefix(fv.Keyword, "other:") {
				t.Fatalf("verdicts both-named violation used an UNCLASSIFIED keyword %q (needs a new schemas/errors/v1/registry.yaml row) rather than an existing classified one: %+v", fv.Keyword, v)
			}
		}
		if !sawClassified {
			t.Fatalf("expected a classified (falseSchema/required) violation, got: %+v", v)
		}
	})
	t.Run("neither named is refused", func(t *testing.T) {
		t.Parallel()
		doc := header + `, "verdicts": [{"verdict": "met", "cause_owner": "axon"}]}`
		if v := p3ValidateEvent(t, doc); len(v) == 0 {
			t.Fatal("expected verdicts naming neither index nor criterion to be refused")
		}
	})
}

// TestVerifyEchoesVerdictBindingBeforeMinting is spec 03 AC1/AC2 (US-1):
// `a2a verify --verdict` prints each binding's criterion text (truncated to
// 80 RUNES — []rune-bounded, never a byte slice, so this fixture uses a
// non-ASCII criterion to actually exercise that: a byte-length truncation
// would split "é" mid-character and this test would still pass on an
// ASCII-only fixture) BEFORE the funnel is ever called, for an ORDINAL
// parent (prints the bare index).
func TestVerifyEchoesVerdictBindingBeforeMinting(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec01"
	longText := strings.Repeat("é", 85)
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - \""+longText+"\"\n")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:seomatrix", responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	wantTrunc := strings.Repeat("é", 80) + "…"
	if !strings.Contains(out.String(), "0 -> ") {
		t.Fatalf("expected the echo to name the bare index for an ordinal parent, got stdout=%q", out.String())
	}
	if !strings.Contains(out.String(), wantTrunc) {
		t.Fatalf("expected the echo to carry the []rune-truncated 80-character criterion text, got stdout=%q", out.String())
	}
	if strings.Contains(out.String(), longText) {
		t.Fatalf("echo printed the FULL untruncated criterion text, want only its first 80 runes: stdout=%q", out.String())
	}
	if !strings.Contains(out.String(), "met") || !strings.Contains(out.String(), "seomatrix") {
		t.Fatalf("expected the echo to also carry the verdict and cause_owner, got stdout=%q", out.String())
	}
	// AC1: printed BEFORE minting — the echo must already be on stdout even
	// though this assertion runs after Run() returns, so the REAL proof is
	// ordering-independent: the funnel is a fake that only records calls
	// after Run() drives them, and code==0 here already proves the write
	// path completed; TestVerifyRejectsMalformedVerdictFlag-style negative
	// cases (below) are what prove the echo does NOT print when the
	// resolution refuses before any write.
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
}

// TestVerifyEchoesVerdictBindingForIDDeclaringParent is AC2's other cell:
// a parent whose acceptance_criteria[] declares ids gets the id-form echo
// (`ac1 -> "…"`), not a bare index.
func TestVerifyEchoesVerdictBindingForIDDeclaringParent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec02"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"Every code exists in the registry.\"\n")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ac1 -> \"Every code exists in the registry.\"") {
		t.Fatalf("expected the echo to name the criterion id, got stdout=%q", out.String())
	}
}

// TestVerifyVerdictByIDResolvesAgainstDeclaredCriteria is AC3/AC7's CLI
// half: `--verdict ac1:met:owner` against an id-declaring parent writes
// `verdicts: [{criterion: ac1, ...}]` — never an `index` key.
func TestVerifyVerdictByIDResolvesAgainstDeclaredCriteria(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec03"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n  - id: ac2\n    text: \"second\"\n")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac2:met:seomatrix", responseID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	verify := findEventByTransition(t, fake.calls[0].Files, "verify")
	if verify.Verdicts == nil || len(*verify.Verdicts) != 1 {
		t.Fatalf("expected exactly one verdicts entry, got %v", verify.Verdicts)
	}
	got := (*verify.Verdicts)[0]
	if got.Criterion != "ac2" || got.Index != 0 || got.Verdict != "met" || got.CauseOwner != "seomatrix" {
		t.Fatalf("verdicts[0] = %+v, want {Criterion:ac2 Index:0 met seomatrix} (ac2 is array position 1, resolved — never written as an index)", got)
	}
}

// TestVerifyVerdictUnknownIDRefusedNamingDeclaredIDs is AC4: an id that
// resolves to nothing is refused locally, exit 2, naming what the parent
// DOES declare.
func TestVerifyVerdictUnknownIDRefusedNamingDeclaredIDs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec04"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "nosuch:met:seomatrix", responseID}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for an id that does not resolve")
	}
	if !strings.Contains(errOut.String(), "ac1") {
		t.Fatalf("refusal does not name the ids the parent declares: %s", errOut.String())
	}
}

// TestVerifyVerdictBareIndexRefusedWhenParentDeclaresIDs closes the
// mis-binding mechanism itself (spec 03, not just documentation): a bare
// positional index against an id-declaring parent is refused, not silently
// accepted — accepting it would reopen fb-20260818-76f29d's exact class of
// bug for the id form.
func TestVerifyVerdictBareIndexRefusedWhenParentDeclaresIDs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec05"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "0:met:seomatrix", responseID}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a bare index against an id-declaring parent")
	}
}

// TestVerifyVerdictIDRefusedWhenParentDeclaresNoIDs is the mirror
// direction: a criterion-id token against an ordinal-only parent is
// refused too (nothing to resolve it against).
func TestVerifyVerdictIDRefusedWhenParentDeclaresNoIDs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec06"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - \"plain string criterion\"\n")
	responseID := respondFlow(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", responseID}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a criterion id against an ordinal-only parent")
	}
}

// TestCloseEchoesVerdictBindingBeforeMinting is AC1/AC2's `close` half —
// close's own ids ARE the parents directly (no response indirection).
func TestCloseEchoesVerdictBindingBeforeMinting(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-ec07"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first criterion\"\n")
	respondFlow(t, mirrorDir, parentID, "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 3, parentID, "respond", "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", parentID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ac1 -> \"first criterion\"") {
		t.Fatalf("expected close's own pre-mint echo, got stdout=%q", out.String())
	}
}

// TestRespondUnmetByIDWritesCriterionForm is AC7: `a2a respond --unmet ac3`
// writes `unmet: [{criterion: ac3}]` against a parent declaring ids.
func TestRespondUnmetByIDWritesCriterionForm(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-am81"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n  - id: ac2\n    text: \"second\"\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "ac2", "--blocked-by", "other:seomatrix:bytes", parentID}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	var respPath string
	for _, fw := range fake.calls[0].Files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			respPath = fw.Path
		}
	}
	if respPath == "" {
		t.Fatalf("no XS- response file among %+v", fake.calls[0].Files)
	}
	var content string
	for _, fw := range fake.calls[0].Files {
		if fw.Path == respPath {
			content = string(fw.Content)
		}
	}
	var doc struct {
		Unmet []map[string]string `yaml:"unmet"`
	}
	fm, err := artifact.ParseFrontmatter([]byte(content))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		t.Fatalf("decode response frontmatter: %v; content=%s", err, content)
	}
	if len(doc.Unmet) != 1 || doc.Unmet[0]["criterion"] != "ac2" {
		t.Fatalf("unmet = %+v, want [{criterion: ac2}]; content=%s", doc.Unmet, content)
	}
}

// TestRespondUnmetIDRefusedAgainstOrdinalParent mirrors --verdict's own
// direction check: a criterion id against a parent with no declared ids is
// refused, not silently accepted.
func TestRespondUnmetIDRefusedAgainstOrdinalParent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-am82"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - \"plain string criterion\"\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "ac1", "--blocked-by", "other:seomatrix:bytes", parentID}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a criterion id against an ordinal-only parent")
	}
}

// --- spec 04 P4: a batch judges every target against its own parent -------

// TestVerifyEchoesPerTargetWithIDPrefix is spec 04 AC1 (US-1): a 2-target
// `verify` batch prints ONE echo block PER target, each carrying that
// target's own criterion text, prefixed by the target id — not a single
// echo (against the first target's parent) reused for the whole batch.
func TestVerifyEchoesPerTargetWithIDPrefix(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p401"
	parentB := "XQ-axon-20260721-p402"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - id: ac1\n    text: \"criterion A\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - id: ac1\n    text: \"criterion A\"\n")
	responseA := respondFlow(t, mirrorDir, parentA, "beta")
	responseB := respondFlow(t, mirrorDir, parentB, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", responseA, responseB}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), responseA+":") {
		t.Fatalf("expected the echo to be prefixed with target %s, got stdout=%q", responseA, out.String())
	}
	if !strings.Contains(out.String(), responseB+":") {
		t.Fatalf("expected the echo to be prefixed with target %s, got stdout=%q", responseB, out.String())
	}
	if got := strings.Count(out.String(), "ac1 -> \"criterion A\""); got != 2 {
		t.Fatalf("expected TWO echo blocks (one per target), got %d in stdout=%q", got, out.String())
	}
}

// TestVerifyBatchPerEventVerdictsMatchOwnParent is spec 04 AC5 (US-3): on a
// uniform 2-target batch, EVERY minted event — both verify events AND both
// D-024 close events — carries verdicts resolved against ITS OWN parent,
// asserted per event rather than assumed from one representative event.
func TestVerifyBatchPerEventVerdictsMatchOwnParent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p410"
	parentB := "XQ-axon-20260721-p411"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - id: ac1\n    text: \"shared criterion\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - id: ac1\n    text: \"shared criterion\"\n")
	responseA := respondFlow(t, mirrorDir, parentA, "beta")
	responseB := respondFlow(t, mirrorDir, parentB, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", responseA, responseB}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr=%s", code, errOut.String())
	}
	files := fake.calls[0].Files
	var verifyEvents, closeEvents int
	for _, fw := range files {
		var probe lifecycleEventProbe
		if err := yaml.Unmarshal(fw.Content, &probe); err != nil {
			t.Fatalf("decode event %s: %v", fw.Path, err)
		}
		switch probe.Transition {
		case "verify":
			verifyEvents++
		case "close":
			closeEvents++
		default:
			continue
		}
		if probe.Verdicts == nil || len(*probe.Verdicts) != 1 {
			t.Fatalf("event %s (%s of %s): expected exactly one verdicts entry, got %v", fw.Path, probe.Transition, probe.Subject, probe.Verdicts)
		}
		got := (*probe.Verdicts)[0]
		if got.Criterion != "ac1" || got.Verdict != "met" || got.CauseOwner != "seomatrix" {
			t.Fatalf("event %s (%s of %s): verdicts[0] = %+v, want {Criterion:ac1 met seomatrix}", fw.Path, probe.Transition, probe.Subject, got)
		}
	}
	if verifyEvents != 2 || closeEvents != 2 {
		t.Fatalf("expected 2 verify + 2 D-024 close events, got verify=%d close=%d (files=%d)", verifyEvents, closeEvents, len(files))
	}
}

// TestVerifyBatchDivergentCriteriaCountsRefused is spec 04 AC2: two targets
// whose parents declare different criteria counts, with an index in range
// for only one, refuse the WHOLE batch by name — naming the target that
// cannot bind.
func TestVerifyBatchDivergentCriteriaCountsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p420" // 5 criteria: index 4 in range
	parentB := "XQ-axon-20260721-p421" // 3 criteria: index 4 out of range
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - \"c0\"\n  - \"c1\"\n  - \"c2\"\n  - \"c3\"\n  - \"c4\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - \"c0\"\n  - \"c1\"\n  - \"c2\"\n")
	responseA := respondFlow(t, mirrorDir, parentA, "beta")
	responseB := respondFlow(t, mirrorDir, parentB, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "4:met:seomatrix", responseA, responseB}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a batch that cannot bind uniformly")
	}
	if !strings.Contains(errOut.String(), responseB) {
		t.Fatalf("expected the refusal to name the disagreeing target %s; got %q", responseB, errOut.String())
	}
}

// TestVerifyBatchMixedIDAndOrdinalRefused is spec 04 AC3: a batch mixing an
// id-declaring parent and an ordinal parent is refused for EITHER token
// form.
func TestVerifyBatchMixedIDAndOrdinalRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	idParent := "XQ-axon-20260721-p430"
	ordinalParent := "XQ-axon-20260721-p431"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, idParent, "beta", "  - id: ac1\n    text: \"criterion\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, ordinalParent, "beta", "  - \"criterion\"\n")
	responseID1 := respondFlow(t, mirrorDir, idParent, "beta")
	responseID2 := respondFlow(t, mirrorDir, ordinalParent, "beta")

	t.Run("id token", func(t *testing.T) {
		t.Parallel()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", responseID1, responseID2}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatal("funnel called for a mixed id/ordinal batch")
		}
	})

	t.Run("ordinal token", func(t *testing.T) {
		t.Parallel()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--verdict", "0:met:seomatrix", responseID1, responseID2}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatal("funnel called for a mixed id/ordinal batch")
		}
	})
}

// TestVerifyBatchSameIDDifferentTextRefused is spec 04 AC4: two parents
// both declaring `ac1` with DIFFERENT criterion text refuse the batch — the
// referent is not the same judgement.
func TestVerifyBatchSameIDDifferentTextRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p500"
	parentB := "XQ-axon-20260721-p501"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - id: ac1\n    text: \"criterion A\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - id: ac1\n    text: \"criterion B (different)\"\n")
	responseA := respondFlow(t, mirrorDir, parentA, "beta")
	responseB := respondFlow(t, mirrorDir, parentB, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewVerifyCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", responseA, responseB}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a batch whose parents disagree on criterion text")
	}
	if !strings.Contains(errOut.String(), "criterion A") || !strings.Contains(errOut.String(), "criterion B (different)") {
		t.Fatalf("expected the refusal to name BOTH criterion texts that disagree; got %q", errOut.String())
	}
}

// TestCloseBatchDivergentCriteriaCountsRefused is AC7's `close` half of
// TestVerifyBatchDivergentCriteriaCountsRefused (AC2): close's own ids ARE
// the parents directly (no response indirection).
func TestCloseBatchDivergentCriteriaCountsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p470" // 5 criteria: index 4 in range
	parentB := "XQ-axon-20260721-p471" // 3 criteria: index 4 out of range
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - \"c0\"\n  - \"c1\"\n  - \"c2\"\n  - \"c3\"\n  - \"c4\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - \"c0\"\n  - \"c1\"\n  - \"c2\"\n")
	respondFlow(t, mirrorDir, parentA, "beta")
	respondFlow(t, mirrorDir, parentB, "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 10, parentA, "respond", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 11, parentB, "respond", "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "4:met:seomatrix", parentA, parentB}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a batch that cannot bind uniformly")
	}
	if !strings.Contains(errOut.String(), parentB) {
		t.Fatalf("expected the refusal to name the disagreeing target %s; got %q", parentB, errOut.String())
	}
}

// TestCloseBatchMixedIDAndOrdinalRefused is AC7's `close` half of AC3.
func TestCloseBatchMixedIDAndOrdinalRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	idParent := "XQ-axon-20260721-p480"
	ordinalParent := "XQ-axon-20260721-p481"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, idParent, "beta", "  - id: ac1\n    text: \"criterion\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, ordinalParent, "beta", "  - \"criterion\"\n")
	respondFlow(t, mirrorDir, idParent, "beta")
	respondFlow(t, mirrorDir, ordinalParent, "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 20, idParent, "respond", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 21, ordinalParent, "respond", "beta")

	t.Run("id token", func(t *testing.T) {
		t.Parallel()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", idParent, ordinalParent}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatal("funnel called for a mixed id/ordinal batch")
		}
	})

	t.Run("ordinal token", func(t *testing.T) {
		t.Parallel()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--verdict", "0:met:seomatrix", idParent, ordinalParent}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatal("funnel called for a mixed id/ordinal batch")
		}
	})
}

// TestCloseBatchSameIDDifferentTextRefused is AC7's `close` half of AC4.
func TestCloseBatchSameIDDifferentTextRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p490"
	parentB := "XQ-axon-20260721-p491"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - id: ac1\n    text: \"criterion A\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - id: ac1\n    text: \"criterion B (different)\"\n")
	respondFlow(t, mirrorDir, parentA, "beta")
	respondFlow(t, mirrorDir, parentB, "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 30, parentA, "respond", "beta")
	writeLifecycleEvent(t, mirrorDir, "beta", 31, parentB, "respond", "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewCloseCommand(fake, mirrorDir, "fixture-space", "axon", lifecycleManifestAtFloor("0.19.0"), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--verdict", "ac1:met:seomatrix", parentA, parentB}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a batch whose parents disagree on criterion text")
	}
	if !strings.Contains(errOut.String(), "criterion A") || !strings.Contains(errOut.String(), "criterion B (different)") {
		t.Fatalf("expected the refusal to name BOTH criterion texts that disagree; got %q", errOut.String())
	}
}

// TestRespondBatchUnmetDivergentCriteriaCountsRefused is AC7's `respond`
// half of AC2, over `--unmet` (respond has no `--verdict`; unmet is its
// own analogous per-criterion flag, resolved the same way).
func TestRespondBatchUnmetDivergentCriteriaCountsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p440" // 5 criteria: index 4 in range
	parentB := "XQ-axon-20260721-p441" // 3 criteria: index 4 out of range
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - \"c0\"\n  - \"c1\"\n  - \"c2\"\n  - \"c3\"\n  - \"c4\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - \"c0\"\n  - \"c1\"\n  - \"c2\"\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "4", "--blocked-by", "other:seomatrix:bytes", parentA, parentB}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a batch that cannot bind uniformly")
	}
	if !strings.Contains(errOut.String(), parentB) {
		t.Fatalf("expected the refusal to name the disagreeing target %s; got %q", parentB, errOut.String())
	}
}

// TestRespondBatchUnmetMixedIDAndOrdinalRefused is AC7's `respond` half of
// AC3.
func TestRespondBatchUnmetMixedIDAndOrdinalRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	idParent := "XQ-axon-20260721-p450"
	ordinalParent := "XQ-axon-20260721-p451"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, idParent, "beta", "  - id: ac1\n    text: \"criterion\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, ordinalParent, "beta", "  - \"criterion\"\n")

	t.Run("id token", func(t *testing.T) {
		t.Parallel()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "ac1", "--blocked-by", "other:seomatrix:bytes", idParent, ordinalParent}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatal("funnel called for a mixed id/ordinal batch")
		}
	})

	t.Run("ordinal token", func(t *testing.T) {
		t.Parallel()
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		io, _, errOut := newIO()
		code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "0", "--blocked-by", "other:seomatrix:bytes", idParent, ordinalParent}, io)
		if code != 2 {
			t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
		}
		if len(fake.calls) != 0 {
			t.Fatal("funnel called for a mixed id/ordinal batch")
		}
	})
}

// TestRespondBatchUnmetSameIDDifferentTextRefused is AC7's `respond` half
// of AC4.
func TestRespondBatchUnmetSameIDDifferentTextRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentA := "XQ-axon-20260721-p460"
	parentB := "XQ-axon-20260721-p461"
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentA, "beta", "  - id: ac1\n    text: \"criterion A\"\n")
	seedAcceptedQuestionWithCriteria(t, mirrorDir, parentB, "beta", "  - id: ac1\n    text: \"criterion B (different)\"\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "partial", "--unmet", "ac1", "--blocked-by", "other:seomatrix:bytes", parentA, parentB}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatal("funnel called for a batch whose parents disagree on criterion text")
	}
	if !strings.Contains(errOut.String(), "criterion A") || !strings.Contains(errOut.String(), "criterion B (different)") {
		t.Fatalf("expected the refusal to name BOTH criterion texts that disagree; got %q", errOut.String())
	}
}

// --- respond --delivers (judge-the-thing-2026-08 P1) ----------------------

type respondDeliversProbe struct {
	Delivers []string `yaml:"delivers"`
}

func decodeResponseDelivers(t *testing.T, content string) []string {
	t.Helper()
	fm, err := artifact.ParseFrontmatter([]byte(content))
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	var probe respondDeliversProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		t.Fatalf("decode delivers: %v", err)
	}
	return probe.Delivers
}

// TestRespondWritesDeliversInGivenOrder: `delivers[]` is a SEQUENCE on the
// wire, so --delivers values are written in the order given, never sorted —
// the same decision lifecycleRespondSeed's doc comment records for refs.
func TestRespondWritesDeliversInGivenOrder(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-dvr1"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"--result", "delivered",
		"--delivers", "DP-beta-20260821-dpk2",
		"--delivers", "DP-beta-20260821-dpk1",
		parentID,
	}, io)
	if code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	got := decodeResponseDelivers(t, extractResponseContent(fake.calls[0].Files))
	want := []string{"DP-beta-20260821-dpk2", "DP-beta-20260821-dpk1"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("decoded delivers = %v, want %v (GIVEN order preserved)", got, want)
	}
}

// TestRespondWithoutDeliversWritesNoKey is the oracle at CLI tier (§8
// criterion 2): an ordinary `--result delivered` answer — what the declared
// plain-answer catalogue paths drive — carries no `delivers` key at all, so
// the submit-time refusal has nothing to read and the document is
// byte-identical to what it was before this field existed.
func TestRespondWithoutDeliversWritesNoKey(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-dvr2"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"--result", "delivered", parentID}, io); code != 0 {
		t.Fatalf("respond: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	content := extractResponseContent(fake.calls[0].Files)
	if got := decodeResponseDelivers(t, content); len(got) != 0 {
		t.Fatalf("decoded delivers = %v, want none when --delivers was never given", got)
	}
	if strings.Contains(content, "delivers") {
		t.Fatalf("the rendered response mentions `delivers` with no flag given:\n%s", content)
	}
}

func TestRespondEmptyDeliversIsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-dvr3"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "delivered", "--delivers", "", parentID}, io)
	if code != 2 {
		t.Fatalf("respond --delivers '': code = %d, want 2; stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel never to be called on a usage refusal, got %d calls", len(fake.calls))
	}
}

// TestRespondDeliversMintDistinctIDsAndDistinctOperationKeys pins both halves
// of what `--delivers` must distinguish, and it is the closure of the residue
// P1 reported rather than a new claim.
//
// The DOCUMENT's identity: two responses differing only in --delivers are two
// different documents and mint two different response ids
// (lifecycleRespondSeed carries delivers).
//
// The OPERATION KEY: same, and it was NOT so until 2026-08-21. P1 could not
// reach internal/operation/key.go and left this test asserting the collision
// out loud, with a failure message naming its own closure condition. It fired
// exactly as written the day the lead added the field. Two responses on one
// parent differing only in the package they announce used to derive ONE key,
// hence one branch, so the second met the funnel's already-open short-circuit
// and was read as a RETRY of the first — a second delivery on one
// work_request silently disappearing.
//
// The third assertion is the one that would be easy to omit: a response with
// NO delivers must derive the key it derived before the field existed. The
// section is written only when non-empty for exactly that reason, and without
// this check a stability property the whole design rests on would be resting
// on a comment.
func TestRespondDeliversMintDistinctIDsAndDistinctOperationKeys(t *testing.T) {
	t.Parallel()
	fixedNow := func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }
	parentID := "XQ-axon-20260721-dvr4"

	run := func(t *testing.T, delivers ...string) (string, string) {
		t.Helper()
		mirrorDir := t.TempDir()
		seedAcceptedQuestion(t, mirrorDir, parentID, "beta")
		fake := &fakeLifecycleFunnel{}
		cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
		cmd.SetClockForTest(fixedNow)
		args := []string{"--result", "delivered"}
		for _, d := range delivers {
			args = append(args, "--delivers", d)
		}
		args = append(args, parentID)
		io, _, errOut := newIO()
		if code := cmd.Run(context.Background(), args, io); code != 0 {
			t.Fatalf("respond: code=%d stderr=%s", code, errOut.String())
		}
		return extractResponseID(fake.calls[0].Files), fake.calls[0].OperationKey
	}

	idNone, keyNone := run(t)
	idOne, keyOne := run(t, "DP-beta-20260821-dpk1")
	idTwo, keyTwo := run(t, "DP-beta-20260821-dpk2")
	if idNone == idOne {
		t.Fatalf("expected DIFFERENT response ids for no-delivers vs one package, got the same id %q", idNone)
	}
	if keyNone == keyOne {
		t.Fatalf("two responses on one parent differing only in --delivers share operation key %q — "+
			"the second collapses onto the first's branch and is read as a retry", keyNone)
	}
	if idOne == idTwo || keyOne == keyTwo {
		t.Fatalf("announcing a DIFFERENT package is a different act: ids %q/%q keys %q/%q", idOne, idTwo, keyOne, keyTwo)
	}
	// Byte-stability. Not a paraphrase of the run above: this asserts the
	// no-delivers key against a literal derived before the field existed, so
	// a future change to the encoding cannot quietly rename every response
	// branch already in every space.
	const keyBeforeDeliversExisted = "op-v1-daccc5327c0a49b5923225ff8b2eb66a5a99bcda3dc84cff0f3b60bbaf9e5001"
	if keyNone != keyBeforeDeliversExisted {
		t.Fatalf("a response carrying no --delivers derives %q, but historically derived %q — "+
			"the delivers section must write NOTHING when empty or every existing branch id moves",
			keyNone, keyBeforeDeliversExisted)
	}
}

// TestRespondRefusesUnlandedDeliversThroughTheRealFunnel is §8 criterion 1
// at the CLI tier, driven through the production entry point rather than a
// direct call to the check: a REAL space.WriteFunnel over a fixture space,
// a response announcing a package whose payload PR has not merged (nothing
// resolves it on origin/main), and the CLI must exit non-zero naming both
// the package and REF-024 — with no PR opened at all.
func TestRespondRefusesUnlandedDeliversThroughTheRealFunnel(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir := fx.Clone("beta")

	parentID := "XQ-axon-20260721-dvr5"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	hostCfg := lifecycleHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	packageID := "DP-beta-20260821-dpk7"
	cmd := cli.NewRespondCommand(funnel, mirrorDir, "fixture-space", "beta", lifecycleManifest(), hostCfg, lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--result", "delivered", "--delivers", packageID, parentID}, io)
	if code == 0 {
		t.Fatalf("respond --delivers <unlanded>: code = 0, want a refusal; stderr=%s", errOut.String())
	}
	msg := errOut.String()
	if !strings.Contains(msg, "REF-024") {
		t.Fatalf("expected the refusal to name REF-024, got: %s", msg)
	}
	if !strings.Contains(msg, packageID) {
		t.Fatalf("expected the refusal to name the package %q, got: %s", packageID, msg)
	}
	if len(fakeHost.Opens) != 0 {
		t.Fatalf("expected zero OpenPR calls on a refused response, got %d", len(fakeHost.Opens))
	}
}

// --- P5 (answers-that-hold-2026-08) parent-transition guard, CLI tier ---

// TestSubmitRefusesResponseDraftWithNoParentTransitionThroughTheRealFunnel
// is US-1/AC-1/AC-2/AC-3 driven through the REAL write funnel from the
// `a2a submit` surface — the same "real WriteFunnel + FakeHost +
// spacefixture" idiom TestSubmitEndToEndSingleArtifact and
// TestRespondRefusesUnlandedDeliversThroughTheRealFunnel already
// establish (cmd_submit_test.go's own newRealFunnelDeps). `a2a submit`'s
// own event names the RESPONSE as its event `subject`
// (cmd_submit.go's buildRequest, submitFirstTransition), never the
// parent — so internal/space's REF-027 guard fires here exactly as it
// would through the MCP write path, which authors the byte-identical
// event shape (US-5: both surfaces reach the SAME funnel seat,
// funnel.Submit — internal/mcp is off this phase's allowlist, so an
// MCP-tier test is not added here; this is the CLI half of AC-3).
func TestSubmitRefusesResponseDraftWithNoParentTransitionThroughTheRealFunnel(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)

	parentID := "XQ-axon-20260721-k3f9"
	// The parent must RESOLVE for this test to reach its own subject. It did
	// not before, and the test passed anyway because nothing checked — an
	// unresolvable parent produced no finding at all (internal/validate's
	// checkFork returned nothing, the gap computed-not-listed-2026-08 P7
	// closed). Now REF-003 rejects first and shadows REF-027, which is correct
	// behaviour and the wrong assertion to make here: this test is about a
	// response whose parent EXISTS but carries no transition, so the fixture
	// has to say so.
	writeQuestionArtifactWithThread(t, mirrorDir, parentID, "other", cliFixtureThread)
	stagingDir := t.TempDir()
	responseID := "XS-axon-20260828-sb1a"
	draft := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + responseID + "\n" +
		"type: response\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"parent: " + parentID + "\n" +
		"result: answered\n" +
		"---\nbody\n"
	path := filepath.Join(stagingDir, responseID+".md")
	if err := os.WriteFile(path, []byte(draft), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code == 0 {
		t.Fatalf("submit of a lone response draft: code = 0, want a refusal")
	}
	msg := errOut.String()
	for _, want := range []string{"REF-027", parentID, "a2a respond"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not name %q; got: %s", want, want, msg)
		}
	}

	count := gitRevListCount(t, mirrorDir, "main", "a2a/axon/submit/*")
	if count != 0 {
		t.Fatalf("expected zero new commits/branches before any git action, found %d", count)
	}
}

// TestRespondResponseFlagAdoptsOrphanKeepsBodyAndEnablesVerify is
// US-2/AC-6/AC-7: an already-filed orphan response — committed with no
// accompanying parent transition, exactly the pre-guard shape `a2a
// submit` used to leave behind — is adopted via `a2a respond --response
// <id>`. Only the parent's own `respond` event is authored (one file,
// not two); the response's own committed body is never rewritten; and a
// subsequent `a2a verify` on the thread is legal afterward.
func TestRespondResponseFlagAdoptsOrphanKeepsBodyAndEnablesVerify(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-rp1a"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	responseID := "XS-beta-20260828-rp1a"
	orphanBody := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + responseID + "\n" +
		"type: response\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"parent: " + parentID + "\n" +
		"result: answered\n" +
		"---\nthe already-authored answer, verbatim.\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+responseID+".md", orphanBody)

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--response", responseID}, io)
	if code != 0 {
		t.Fatalf("respond --response %s: code = %d, want 0; stderr=%s", responseID, code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if got := len(fake.calls[0].Files); got != 1 {
		t.Fatalf("expected exactly ONE file (the parent's own respond event, no response rewrite), got %d: %+v", got, fake.calls[0].Files)
	}
	eventContent := string(fake.calls[0].Files[0].Content)
	if !strings.Contains(eventContent, "subject: "+parentID) || !strings.Contains(eventContent, "transition: respond") || !strings.Contains(eventContent, "ref: "+responseID) {
		t.Fatalf("expected a respond event naming subject=%s and refs[].ref=%s, got:\n%s", parentID, responseID, eventContent)
	}

	// AC-6: the existing body is kept — the response's OWN file was never
	// part of this funnel call, so its on-disk content is untouched.
	onDisk, err := os.ReadFile(filepath.Join(mirrorDir, "beta/exchanges/"+responseID+".md"))
	if err != nil {
		t.Fatalf("read response file: %v", err)
	}
	if string(onDisk) != orphanBody {
		t.Fatalf("response body changed by the repair verb:\nwant: %s\ngot:  %s", orphanBody, onDisk)
	}

	// Materialize the repair's own event, then prove AC-7: verify is now
	// legal on the thread.
	materializeFiles(t, mirrorDir, fake.calls[0])
	verifyFake := &fakeLifecycleFunnel{}
	verifyCmd := cli.NewVerifyCommand(verifyFake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	vio, _, verrOut := newIO()
	vcode := verifyCmd.Run(context.Background(), []string{responseID}, vio)
	if vcode != 0 {
		t.Fatalf("verify after repair: code = %d, want 0; stderr=%s", vcode, verrOut.String())
	}
}

// TestRespondResponseFlagEnablesDispute is AC-7's other half: dispute is
// ALSO legal on the thread after the repair (a separate seed from the
// verify test above — verify and dispute are mutually exclusive outgoing
// moves from the same post-repair state, so each needs its own fixture).
func TestRespondResponseFlagEnablesDispute(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-rp2a"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	responseID := "XS-beta-20260828-rp2a"
	orphanBody := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + responseID + "\n" +
		"type: response\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"thread: " + cliFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"parent: " + parentID + "\n" +
		"result: answered\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+responseID+".md", orphanBody)

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"--response", responseID}, io); code != 0 {
		t.Fatalf("respond --response %s: code = %d, want 0; stderr=%s", responseID, code, errOut.String())
	}
	materializeFiles(t, mirrorDir, fake.calls[0])

	disputeFake := &fakeLifecycleFunnel{}
	disputeCmd := cli.NewDisputeCommand(disputeFake, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	dio, _, derrOut := newIO()
	if code := disputeCmd.Run(context.Background(), []string{"--reason", "not satisfactory", responseID}, dio); code != 0 {
		t.Fatalf("dispute after repair: code = %d, want 0; stderr=%s", code, derrOut.String())
	}
}

// TestRespondResponseFlagRefusesWhenNoParentDeclared is the "not an
// orphan" family's first edge case (spec 05 §6): a response naming no
// parent at all has nothing to adopt, refused before any git action.
func TestRespondResponseFlagRefusesWhenNoParentDeclared(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	responseID := "XS-beta-20260828-rp3a"
	body := "---\nschema: envelope/v2\nid: " + responseID + "\ntype: response\nresult: answered\n---\nbody\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+responseID+".md", body)

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--response", responseID}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a response naming no parent")
	}
	if !strings.Contains(errOut.String(), "names no parent") {
		t.Fatalf("expected the refusal to name the missing parent, got: %s", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called, got %d", len(fake.calls))
	}
}

// TestRespondResponseFlagRefusesAlreadyAdoptedOrphan is the "not an
// orphan" family's second edge case: a response already linked by a
// committed respond event is refused, never silently re-adopted (which
// would double-link one response id into a parent's Result.Responses).
func TestRespondResponseFlagRefusesAlreadyAdoptedOrphan(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-rp4a"
	seedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	responseID := "XS-beta-20260828-rp4a"
	body := "---\nschema: envelope/v2\nid: " + responseID + "\ntype: response\nparent: " + parentID + "\nresult: answered\n---\nbody\n"
	writeMirrorFile(t, mirrorDir, "beta/exchanges/"+responseID+".md", body)
	// A committed respond event already links this exact response.
	writeMirrorFile(t, mirrorDir, "beta/events/2020/01J8QYK2Z3ABCDEFGHJKMNPQRZ.yaml",
		"schema: event/v1\nevent: 01J8QYK2Z3ABCDEFGHJKMNPQRZ\nspace: fixture-space\nsubject: "+parentID+"\ntransition: respond\nactor: {kind: agent, name: bot, system: beta}\nat: 2020-01-01T00:00:03Z\nrefs:\n  - ref: "+responseID+"\n")

	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--response", responseID}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for an already-adopted orphan")
	}
	if !strings.Contains(errOut.String(), "not an orphan") {
		t.Fatalf("expected the refusal to say this is not an orphan, got: %s", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called, got %d", len(fake.calls))
	}
}

// TestRespondResponseFlagRejectsPositionalParents is the flag's own usage
// guard: --response repairs exactly one already-filed orphan and takes
// no parent-id arguments — mixing the two silently picking one would be
// exactly the ambiguity this epic exists to refuse instead of guess.
func TestRespondResponseFlagRejectsPositionalParents(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--response", "XS-beta-20260828-rp5a", "XQ-axon-20260721-x1"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error)", code)
	}
	if !strings.Contains(errOut.String(), "--response") {
		t.Fatalf("expected the usage error to name --response, got: %s", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called, got %d", len(fake.calls))
	}
}

// TestRespondResponseFlagRefusesActorResolutionFailure is the refusal-
// ratchet migration's (answers-that-hold-2026-08 P4, spec 04) own coverage
// for --response's actor-resolution site: a three-part refusal (attempted/
// found/nextStep), not a bare "respond: <err>" passthrough.
func TestRespondResponseFlagRefusesActorResolutionFailure(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	failingResolver := func(cli.ActorFlags) (template.Actor, error) {
		return template.Actor{}, errors.New("cannot determine who is acting: pass --actor-name <name>, or set A2A_ACTOR_NAME")
	}
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), failingResolver)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--response", "XS-beta-20260828-rp6a"}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	text := errOut.String()
	if !strings.Contains(text, "resolve the actor") {
		t.Fatalf("expected the refusal to name what was attempted, got: %s", text)
	}
	if !strings.Contains(text, "A2A_ACTOR_NAME") {
		t.Fatalf("expected the refusal to name its next step, got: %s", text)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called, got %d", len(fake.calls))
	}
}

// TestRespondResponseFlagRefusesUnloadableResponseID is the refusal-ratchet
// migration's own coverage for --response's envelope-load site: an id this
// space's synced mirror does not carry refuses with a next step (`a2a
// sync`), not a bare "respond: <id>: <err>" passthrough.
func TestRespondResponseFlagRefusesUnloadableResponseID(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	fake := &fakeLifecycleFunnel{}
	cmd := cli.NewRespondCommand(fake, mirrorDir, "fixture-space", "beta", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	io, _, errOut := newIO()
	// No response of this id was ever written under mirrorDir.
	code := cmd.Run(context.Background(), []string{"--response", "XS-beta-20260828-rp6b"}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	text := errOut.String()
	if !strings.Contains(text, "XS-beta-20260828-rp6b") {
		t.Fatalf("expected the refusal to name the id it could not load, got: %s", text)
	}
	if !strings.Contains(text, "a2a sync") {
		t.Fatalf("expected the refusal to name its next step (a2a sync), got: %s", text)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called, got %d", len(fake.calls))
	}
}
