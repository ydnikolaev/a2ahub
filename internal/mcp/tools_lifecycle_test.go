package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
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

// --- P2 (defects-fix-2026-08): unmet/standing/blocked_by ------------------

// mcpSeedAcceptedQuestion mirrors internal/cli's own seedAcceptedQuestion
// (cmd_lifecycle_test.go, off this phase's allowlist) — submit/acknowledge/
// accept, the minimum legal history `respond` needs.
func mcpSeedAcceptedQuestion(t *testing.T, mirrorDir, id, to string) {
	t.Helper()
	writeQuestionArtifact(t, mirrorDir, id, to)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, to, 1, id, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, to, 2, id, "accept", to)
}

// mcpUnmetIndex builds one bare-index `unmet[]` entry (defects-fix-2026-08
// P3: RespondUnmetEntry.Index is a pointer, so tests need a helper for it
// rather than a struct literal per call site).
func mcpUnmetIndex(n int) RespondUnmetEntry {
	idx := n
	return RespondUnmetEntry{Index: &idx}
}

// mcpUnmetCriterion builds one criterion-id `unmet[]` entry.
func mcpUnmetCriterion(id string) RespondUnmetEntry {
	return RespondUnmetEntry{Criterion: id}
}

// mcpSeedAcceptedQuestionWithCriteria is mcpSeedAcceptedQuestion plus an
// injected `acceptance_criteria:` block (defects-fix-2026-08 P3) —
// criteriaYAML is the already-indented block body, mirroring internal/cli's
// own seedAcceptedQuestionWithCriteria (cmd_lifecycle_test.go, off this
// phase's allowlist, reproduced here per ADR-001).
func mcpSeedAcceptedQuestionWithCriteria(t *testing.T, mirrorDir, id, to, criteriaYAML string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [" + to + "]\n" +
		"thread: " + testFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"acceptance_criteria:\n" + criteriaYAML +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md", content)
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, to, 1, id, "acknowledge", to)
	writeLifecycleEvent(t, mirrorDir, to, 2, id, "accept", to)
}

// respondWithRealValidation drives a2a_respond through the SAME real
// schema.Load/validate.New/space.NewWriteFunnel stack
// internal/mcp/adapters_test.go's own TestSubmitValidatorAdapter* tests use
// — never fakeFunnel — because this is the parity half of spec 02's own
// ordering guard: it must be possible for this call to fail for a REAL
// schema reason (envelope/v1's unevaluatedProperties:false, or envelope/v2's
// own `if result: partial|cannot` conditional), not merely whatever a stub
// agrees to record.
func respondWithRealValidation(t *testing.T, parentID string, in RespondInput) (mirrorDir string, fakeHost *host.FakeHost, callErr error) {
	t.Helper()
	fx := spacefixture.New(t, "axon", "beta")
	mirrorDir = fx.Clone("beta")
	mcpSeedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	manifest := testManifest()
	legality := NewLegalityAdapter(mirrorDir, "beta", manifest)
	resolver := NewMirrorResolver(mirrorDir, manifest)
	validator := NewSubmitValidatorAdapter(engine, "beta", resolver, legality)

	fakeHost = host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, validator, "0.1.0")
	hostCfg := testHostConfig()
	hostCfg.RemoteURL = fx.RemoteURL()

	deps := WriteDeps{
		Funnel: funnel, MirrorDir: mirrorDir, SpaceID: "fixture-space", OwnSystem: "beta",
		Manifest: manifest, HostCfg: hostCfg, ResolveActor: fixedActorResolver("agent", "bot"),
		Now:     func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) },
		Entropy: repeatingReader{pattern: []byte("0123456789abcdef")}, ReadFile: os.ReadFile,
	}
	handler := newRespondHandler(deps)
	in.ParentIDs = []string{parentID}
	args, merr := json.Marshal(in)
	if merr != nil {
		t.Fatalf("marshal RespondInput: %v", merr)
	}
	_, _, callErr = handler(context.Background(), args)
	return mirrorDir, fakeHost, callErr
}

// mcpGitOutputForTest runs a read-only git command in dir and returns its
// stdout — the output-returning counterpart wire_test.go's own runGitTest
// (same package, off this phase's allowlist) doesn't need, kept local to
// this file rather than widening that helper.
func mcpGitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v", args, dir, err)
	}
	return string(out)
}

// mcpGitDiffNames lists paths that differ between base and branch — the
// same idiom internal/cli's own gitDiffNames (off this phase's allowlist)
// uses.
func mcpGitDiffNames(t *testing.T, dir, base, branch string) []string {
	t.Helper()
	out := mcpGitOutputForTest(t, dir, "diff", "--name-only", base, branch)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names
}

// mcpCommittedResponseContent reads back the ONE committed XS- response
// file off the branch the fake host received the push on — identical
// extraction idiom to internal/cli's committedResponseContent.
func mcpCommittedResponseContent(t *testing.T, mirrorDir string, fakeHost *host.FakeHost) string {
	t.Helper()
	if len(fakeHost.Pushes) != 1 {
		t.Fatalf("expected exactly one PushBranch call, got %d", len(fakeHost.Pushes))
	}
	branch := fakeHost.Pushes[0].Branch
	changed := mcpGitDiffNames(t, mirrorDir, "main", branch)
	var responsePath string
	for _, p := range changed {
		if strings.HasPrefix(filepath.Base(p), "XS-") && strings.HasSuffix(p, ".md") {
			responsePath = p
		}
	}
	if responsePath == "" {
		t.Fatalf("expected a committed XS- response file among %v", changed)
	}
	return mcpGitOutputForTest(t, mirrorDir, "show", branch+":"+responsePath)
}

// TestRespondHandlerResultPartialWithStandingProvisionalValidates is spec 02
// AC4/AC5's MCP half: `a2a_respond` renders `--result partial` +
// `standing: provisional` through the REAL production path and it validates
// — the MCP twin of internal/cli's
// TestRespondResultPartialGenerationOrderingGuard. Recorded mutation
// evidence for this file's own deviations report: run BEFORE
// newRespondHandler's `template.Render` call was given
// `EnvelopeSchema: "envelope/v2"`, this test failed red with the identical
// SCH-003/unevaluatedProperties refusal internal/cli's guard test recorded.
func TestRespondHandlerResultPartialWithStandingProvisionalValidates(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-mrd1"
	mirrorDir, fakeHost, callErr := respondWithRealValidation(t, parentID, RespondInput{Result: "partial", Standing: "provisional"})
	if callErr != nil {
		t.Fatalf("respond partial+standing=provisional: %v", callErr)
	}
	content := mcpCommittedResponseContent(t, mirrorDir, fakeHost)
	if !strings.Contains(content, "schema: envelope/v2") {
		t.Fatalf("expected the committed response to carry schema: envelope/v2, got:\n%s", content)
	}
	if !strings.Contains(content, "standing: provisional") {
		t.Fatalf("expected the committed response to carry standing: provisional, got:\n%s", content)
	}
}

// TestRespondHandlerPartialWithNoneOfTheThreeRefused is spec 02 AC2's
// refusal half, MCP side.
func TestRespondHandlerPartialWithNoneOfTheThreeRefused(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-mrd2"
	_, _, callErr := respondWithRealValidation(t, parentID, RespondInput{Result: "partial"})
	if callErr == nil {
		t.Fatal("expected `result: partial` with none of unmet/standing/blocked_by to be refused")
	}
}

// TestRespondHandlerUnmetAndBlockedByValidates is spec 02 AC1/AC5's MCP
// half: `unmet` + `blocked_by` validates without any `standing` override,
// and carries the SAME shape internal/cli's TestRespondPartialWithUnmet
// AndBlockedByPasses asserts on (the parity this phase's spec calls for).
func TestRespondHandlerUnmetAndBlockedByValidates(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-mrd3"
	mirrorDir, fakeHost, callErr := respondWithRealValidation(t, parentID, RespondInput{
		Result: "partial", Unmet: []RespondUnmetEntry{mcpUnmetIndex(2)},
		BlockedBy: &RespondBlockedBy{ReasonCode: "out-of-scope", Owner: "seomatrix", Needs: "decision"},
	})
	if callErr != nil {
		t.Fatalf("respond partial+unmet+blocked_by: %v", callErr)
	}
	content := mcpCommittedResponseContent(t, mirrorDir, fakeHost)
	if !strings.Contains(content, "unmet:") || !strings.Contains(content, "- 2") {
		t.Fatalf("expected the committed response to carry unmet: [2], got:\n%s", content)
	}
	if !strings.Contains(content, "reason_code: out-of-scope") || !strings.Contains(content, "owner: seomatrix") || !strings.Contains(content, "needs: decision") {
		t.Fatalf("expected the committed response to carry the full blocked_by object, got:\n%s", content)
	}
}

// TestRespondHandlerNoNewFieldsOmitsAllThreeKeys is P-1's own discipline:
// a plain `a2a_respond` call that never sets unmet/standing/blocked_by must
// not declare any of them.
func TestRespondHandlerNoNewFieldsOmitsAllThreeKeys(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-mrd4"
	mirrorDir, fakeHost, callErr := respondWithRealValidation(t, parentID, RespondInput{Result: "answered"})
	if callErr != nil {
		t.Fatalf("respond answered: %v", callErr)
	}
	content := mcpCommittedResponseContent(t, mirrorDir, fakeHost)
	for _, key := range []string{"unmet:", "standing:", "blocked_by:"} {
		if strings.Contains(content, key) {
			t.Fatalf("expected no %s key on a flagless respond, got:\n%s", key, content)
		}
	}
}

// TestRespondHandlerInvalidStandingRefused is respondStandingEnum's own
// mutation-tested guard.
func TestRespondHandlerInvalidStandingRefused(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	deps := testWriteDeps(t.TempDir(), fake)
	handler := newRespondHandler(deps)
	in := RespondInput{ParentIDs: []string{"XQ-axon-20260721-mrd5"}, Result: "partial", Standing: "bogus"}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	// Asserted on the SPECIFIC message, not merely err != nil: this call's
	// mirrorDir carries no seeded parent, so a downstream failure (loadEnvelope
	// finding no such artifact) would ALSO produce a non-nil error and let a
	// broken respondStandingEnum check pass unnoticed — caught exactly this
	// way while mutation-testing this test (see this phase's mutation_evidence).
	if err == nil || !strings.Contains(err.Error(), "standing must be one of") {
		t.Fatalf("expected a standing-enum refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for an invalid standing, got %d calls", len(fake.calls))
	}
}

// TestRespondHandlerBlockedByMissingNeedsRefused is respondValidateBlockedBy's
// own mutation-tested guard: `needs` is required by
// envelope/v2/response.schema.json (`"required": ["reason_code", "owner",
// "needs"]`) and this phase gives it no default (P-1).
func TestRespondHandlerBlockedByMissingNeedsRefused(t *testing.T) {
	t.Parallel()
	fake := &fakeFunnel{}
	deps := testWriteDeps(t.TempDir(), fake)
	handler := newRespondHandler(deps)
	in := RespondInput{ParentIDs: []string{"XQ-axon-20260721-mrd6"}, Result: "partial", BlockedBy: &RespondBlockedBy{ReasonCode: "out-of-scope", Owner: "seomatrix"}}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	// Asserted on the SPECIFIC message — see TestRespondHandlerInvalidStandingRefused's
	// own comment for why a bare err != nil is not enough here (empty
	// mirrorDir, no seeded parent).
	if err == nil || !strings.Contains(err.Error(), "blocked_by.needs must be one of") {
		t.Fatalf("expected a blocked_by.needs refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for a malformed blocked_by, got %d calls", len(fake.calls))
	}
}

// TestRespondHandlerUnmetDuplicateIndexRefused is respondResolveUnmet's own
// mutation-tested guard (defects-fix-2026-08 P3 renamed the function that
// carries this rule from respondValidateUnmet — see that function's own
// doc comment — but the guard, and this test's own load-bearing lesson
// about asserting on a SPECIFIC message, both carry forward unchanged).
func TestRespondHandlerUnmetDuplicateIndexRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-mrd7"
	mcpSeedAcceptedQuestion(t, mirrorDir, parentID, "beta")
	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{ParentIDs: []string{parentID}, Result: "partial", Standing: "provisional", Unmet: []RespondUnmetEntry{mcpUnmetIndex(1), mcpUnmetIndex(1)}}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	// Asserted on the SPECIFIC message — see TestRespondHandlerInvalidStandingRefused's
	// own comment: with a bare err != nil this test PASSED even after
	// respondValidateUnmet's own duplicate-index branch was mutated dead,
	// because the empty mirrorDir's downstream loadEnvelope failure produced
	// an unrelated non-nil error. Caught during this phase's own mutation
	// testing (see mutation_evidence) and fixed here rather than left silent.
	if err == nil || !strings.Contains(err.Error(), "two entries resolve to the same criterion") {
		t.Fatalf("expected a duplicate-unmet-index refusal, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the funnel to never be called for a duplicate unmet index, got %d calls", len(fake.calls))
	}
}

// --- defects-fix-2026-08 P3: "a criterion has a name" — MCP unmet[] -------

// TestRespondHandlerUnmetByIDResolvesAgainstDeclaredCriteria is spec 03
// AC7's MCP half: `unmet: [{"criterion": "ac2"}]` against a parent
// declaring ids writes `unmet: [{criterion: ac2}]` — never a bare index.
func TestRespondHandlerUnmetByIDResolvesAgainstDeclaredCriteria(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-mc01"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n  - id: ac2\n    text: \"second\"\n")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{
		ParentIDs: []string{parentID}, Result: "partial", Unmet: []RespondUnmetEntry{mcpUnmetCriterion("ac2")},
		BlockedBy: &RespondBlockedBy{ReasonCode: "out-of-scope", Owner: "seomatrix", Needs: "bytes"},
	}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	var respContent string
	for _, fw := range fake.calls[0].Files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			respContent = string(fw.Content)
		}
	}
	if respContent == "" {
		t.Fatalf("no XS- response file among %+v", fake.calls[0].Files)
	}
	if !strings.Contains(respContent, "criterion: ac2") {
		t.Fatalf("expected the committed response to carry unmet: [{criterion: ac2}], got:\n%s", respContent)
	}
	if strings.Contains(respContent, "unmet:\n    - 1") || strings.Contains(respContent, "unmet: [1]") {
		t.Fatalf("expected unmet to be written by CRITERION, never resolved back to a bare index, got:\n%s", respContent)
	}
}

// TestRespondHandlerUnmetUnknownIDRefusedNamingDeclaredIDs is AC4's MCP
// half: an id that resolves to nothing is refused, naming what the parent
// DOES declare.
func TestRespondHandlerUnmetUnknownIDRefusedNamingDeclaredIDs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-mc02"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{
		ParentIDs: []string{parentID}, Result: "partial", Unmet: []RespondUnmetEntry{mcpUnmetCriterion("nosuch")},
		BlockedBy: &RespondBlockedBy{ReasonCode: "out-of-scope", Owner: "seomatrix", Needs: "bytes"},
	}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an unresolvable criterion id to be refused")
	}
	if !strings.Contains(err.Error(), "ac1") {
		t.Fatalf("refusal does not name the ids the parent declares: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for an id that does not resolve, got %d calls", len(fake.calls))
	}
}

// TestRespondHandlerUnmetBareIndexRefusedWhenParentDeclaresIDs closes the
// mis-binding mechanism itself: a bare positional index against an
// id-declaring parent is refused, not silently accepted.
func TestRespondHandlerUnmetBareIndexRefusedWhenParentDeclaresIDs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-mc03"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - id: ac1\n    text: \"first\"\n")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{
		ParentIDs: []string{parentID}, Result: "partial", Unmet: []RespondUnmetEntry{mcpUnmetIndex(0)},
		BlockedBy: &RespondBlockedBy{ReasonCode: "out-of-scope", Owner: "seomatrix", Needs: "bytes"},
	}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a bare index against an id-declaring parent to be refused")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for a bare index against an id-declaring parent, got %d calls", len(fake.calls))
	}
}

// TestRespondHandlerUnmetIDRefusedWhenParentDeclaresNoIDs is the mirror
// direction: a criterion-id entry against an ordinal-only parent is
// refused too.
func TestRespondHandlerUnmetIDRefusedWhenParentDeclaresNoIDs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-mc04"
	mcpSeedAcceptedQuestionWithCriteria(t, mirrorDir, parentID, "beta", "  - \"plain string criterion\"\n")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{
		ParentIDs: []string{parentID}, Result: "partial", Unmet: []RespondUnmetEntry{mcpUnmetCriterion("ac1")},
		BlockedBy: &RespondBlockedBy{ReasonCode: "out-of-scope", Owner: "seomatrix", Needs: "bytes"},
	}
	args, _ := json.Marshal(in)
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a criterion id against an ordinal-only parent to be refused")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("funnel called for a criterion id against an ordinal-only parent, got %d calls", len(fake.calls))
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

// supersedeVerbSpec finds the "supersede" row in LifecycleVerbTable by
// name rather than a hardcoded index — this file's own existing call sites
// use LifecycleVerbTable[N] with a trailing verb comment; a lookup avoids
// silently drifting onto the wrong row if the table is ever reordered.
func supersedeVerbSpec(t *testing.T) lifecycleVerbSpec {
	t.Helper()
	for _, spec := range LifecycleVerbTable {
		if spec.Verb == "supersede" {
			return spec
		}
	}
	t.Fatal("LifecycleVerbTable carries no \"supersede\" row")
	return lifecycleVerbSpec{}
}

// TestSupersedeDecisionSuccessorPrecondition is the MCP write-tool surface's
// own regression proof, mirroring internal/cli's
// TestSupersedeDecisionRegressionFix (cmd_lifecycle_test.go) exactly: before
// this fix, newLifecycleHandler's own generic table row reached
// evaluateCandidate (eventdoc.go), which called fold's nil-successor
// EvaluateCandidate wrapper UNCONDITIONALLY for every `a2a_lifecycle
// supersede` call — so both decision-supersede rows (internal/fold/table.go)
// refused for every actor, including one with a genuinely satisfying
// successor. newLifecycleHandler now resolves refs and calls
// evaluateCandidateWithRefs, which resolves real *fold.SuccessorFacts via
// MirrorResolver.Successor (the SAME capability the SUBMIT path's
// resolveSuccessorEnvelope already used) and calls
// fold.EvaluateCandidateWithSuccessor directly.
func TestSupersedeDecisionSuccessorPrecondition(t *testing.T) {
	t.Parallel()

	t.Run("satisfying_successor_succeeds_through_mcp", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260827-p001"
		successorID := "XD-axon-20260827-p002"
		writeDecisionArtifactMCP(t, mirrorDir, predecessorID, "axon", []string{"beta"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "reject", "beta")
		// writeDecisionArtifactMCP's own `from` argument ("axon") matches the
		// acting actor below, satisfying §3.4.4's "author of the successor"
		// (PreconditionSuccessorAuthor, internal/fold/table.go) — the exact
		// case that was structurally unreachable through this surface before
		// this fix, whatever actor called it.
		writeDecisionArtifactMCP(t, mirrorDir, successorID, "axon", []string{"beta"})

		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		deps.OwnSystem = "axon"
		handler := newLifecycleHandler(supersedeVerbSpec(t), deps)

		args, err := json.Marshal(LifecycleInput{IDs: []string{predecessorID}, Refs: []string{successorID}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := handler(context.Background(), args); err != nil {
			t.Fatalf("supersede: unexpected refusal (legitimate actor, satisfying successor must NOT be refused): %v", err)
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})

	t.Run("resolved_but_mismatched_successor_author_is_LFC005_alone", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260827-p003"
		successorID := "XD-axon-20260827-p004"
		writeDecisionArtifactMCP(t, mirrorDir, predecessorID, "axon", []string{"beta"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "reject", "beta")
		// Successor is authored by axon, but the ACTING actor below is beta —
		// the precondition requires the successor's own author to equal the
		// acting actor, so this must still refuse: the fix resolves real
		// facts, it does not grant unconditionally.
		writeDecisionArtifactMCP(t, mirrorDir, successorID, "axon", []string{"beta"})

		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		deps.OwnSystem = "beta"
		handler := newLifecycleHandler(supersedeVerbSpec(t), deps)

		args, err := json.Marshal(LifecycleInput{IDs: []string{predecessorID}, Refs: []string{successorID}})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = handler(context.Background(), args)
		if err == nil {
			t.Fatal("expected a refusal (successor authored by axon, acting actor is beta)")
		}
		// The successor WAS resolved (it exists in this mirror) but the
		// author precondition failed, so this is LFC-005 ALONE — never
		// LFC-002 (verdictError's own generic VerdictUnauthorizedActor label,
		// which this call site's own decision-supersede branch bypasses) and
		// never paired with LFC-006 (reserved for an UNRESOLVED successor).
		if !strings.Contains(err.Error(), "LFC-005") {
			t.Fatalf("expected the refusal to name LFC-005; got %v", err)
		}
		if strings.Contains(err.Error(), "LFC-006") {
			t.Fatalf("expected NO LFC-006 (the successor WAS resolved, just failed the precondition); got %v", err)
		}
		if strings.Contains(err.Error(), "LFC-002") {
			t.Fatalf("expected NO LFC-002 (the generic mislabel this fix closes); got %v", err)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
		}
	})

	t.Run("unresolvable_successor_is_LFC005_plus_LFC006", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260827-p005"
		writeDecisionArtifactMCP(t, mirrorDir, predecessorID, "axon", []string{"beta"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "reject", "beta")
		// No successor artifact written at all: refs names an id this
		// resolver's own index can never contain.

		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		deps.OwnSystem = "axon"
		handler := newLifecycleHandler(supersedeVerbSpec(t), deps)

		args, err := json.Marshal(LifecycleInput{IDs: []string{predecessorID}, Refs: []string{"XD-axon-20260827-gh57"}})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = handler(context.Background(), args)
		if err == nil {
			t.Fatal("expected a refusal (successor entirely unresolvable)")
		}
		if !strings.Contains(err.Error(), "LFC-005") {
			t.Fatalf("expected the refusal to name LFC-005; got %v", err)
		}
		if !strings.Contains(err.Error(), "LFC-006") {
			t.Fatalf("expected the refusal to ALSO name LFC-006 (unresolved successor); got %v", err)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
		}
	})

	// satisfying_successor_via_approved_state_succeeds is the OTHER
	// decision-supersede row's own satisfying case — PreconditionSuccessorApproved
	// (internal/fold/table.go's `{From: StateApproved, ...}` row), never
	// exercised by the two subtests above (both drive the `rejected` row's
	// PreconditionSuccessorAuthor, which reads *fold.SuccessorFacts.Author
	// only). resolveSuccessorFacts (eventdoc.go) also carries `.State`
	// through into the resolved *fold.SuccessorFacts — this is the one
	// subtest in this file that can only pass if THAT field survives:
	// the acting actor (beta) does NOT match the successor's author (axon),
	// so the author precondition alone would refuse this; it can only
	// succeed via the successor's OWN folded state resolving `approved`.
	// Mirrors internal/cli's TestSupersedeDecisionRegressionFix's own
	// approved_by_successor_with_real_quorum_across_sections_succeeds.
	t.Run("satisfying_successor_via_approved_state_succeeds", func(t *testing.T) {
		t.Parallel()
		mirrorDir := t.TempDir()
		predecessorID := "XD-axon-20260827-p006"
		successorID := "XD-axon-20260827-p007"
		writeDecisionArtifactMCP(t, mirrorDir, predecessorID, "axon", []string{"beta"})
		writeLifecycleEvent(t, mirrorDir, "axon", 0, predecessorID, "propose", "axon")
		// beta is the predecessor's ONLY required_approvers entry, so this
		// single approve reaches quorum: the predecessor folds to `approved`,
		// selecting table.go's OTHER decision-supersede row.
		writeLifecycleEvent(t, mirrorDir, "beta", 1, predecessorID, "approve", "beta")

		writeDecisionArtifactMCP(t, mirrorDir, successorID, "axon", []string{"beta"})
		writeLifecycleEvent(t, mirrorDir, "axon", 2, successorID, "propose", "axon")
		writeLifecycleEvent(t, mirrorDir, "beta", 3, successorID, "approve", "beta")

		fake := &fakeFunnel{}
		deps := testWriteDeps(mirrorDir, fake)
		// NOT axon (the successor's own author) — this actor can only pass
		// through the successor's resolved STATE, never its author.
		deps.OwnSystem = "beta"
		handler := newLifecycleHandler(supersedeVerbSpec(t), deps)

		args, err := json.Marshal(LifecycleInput{IDs: []string{predecessorID}, Refs: []string{successorID}})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := handler(context.Background(), args); err != nil {
			t.Fatalf("supersede: unexpected refusal (successor genuinely resolves as approved): %v", err)
		}
		if len(fake.calls) != 1 {
			t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
		}
	})
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

// --- multi-space per-call derivation (wave 2) ------------------------------

// testTwoSpaceWriteDepsWithSystem builds a WriteDeps whose ResolveSpace/
// SpaceOfArtifacts closures route between TWO independently-seeded mirrors —
// the shape wire.go's own buildWriteDeps installs in production
// (wire.go:372-419, off this phase's allowlist), reproduced narrowly here
// rather than a second implementation: same refusal messages (space
// required/not connected, batch spans multiple spaces, no connected mirror
// holds), same "resolved deps carries no ResolveSpace/SpaceOfArtifacts of its
// own" value semantics (wire.go's bySpace map holds VALUES, and so does
// byID here). Returns the FIRST space's own WriteDeps (mirrors
// buildWriteDeps returning primary.write) — this stands in for
// cfg.Spaces[0], the silent-default target this phase exists to stop a
// handler from falling back to.
func testTwoSpaceWriteDepsWithSystem(ownSystem, aID, aDir string, fakeA Funnel, bID, bDir string, fakeB Funnel) WriteDeps {
	connected := []string{aID, bID}
	build := func(dir string, funnel Funnel, spaceID string) WriteDeps {
		d := testWriteDeps(dir, funnel)
		d.SpaceID = spaceID
		d.OwnSystem = ownSystem
		return d
	}
	byID := map[string]WriteDeps{aID: build(aDir, fakeA, aID), bID: build(bDir, fakeB, bID)}
	dirByID := map[string]string{aID: aDir, bID: bDir}

	resolveSpace := func(spaceID string) (WriteDeps, error) {
		if spaceID == "" {
			return WriteDeps{}, fmt.Errorf(
				"mcp: space is required when multiple spaces are connected; connected spaces are %s",
				strings.Join(connected, ", "))
		}
		d, ok := byID[spaceID]
		if !ok {
			return WriteDeps{}, fmt.Errorf(
				"mcp: space %q is not connected; connected spaces are %s", spaceID, strings.Join(connected, ", "))
		}
		return d, nil
	}
	spaceOfArtifacts := func(ids []string) (string, error) {
		resolvedSpace, resolvedID := "", ""
		for _, id := range ids {
			found := ""
			for _, sid := range connected {
				if mirrorHoldsArtifact(dirByID[sid], id) {
					found = sid
					break
				}
			}
			if found == "" {
				return "", fmt.Errorf(
					"mcp: no connected space's mirror holds %s; connected spaces are %s",
					id, strings.Join(connected, ", "))
			}
			if resolvedSpace == "" {
				resolvedSpace, resolvedID = found, id
				continue
			}
			if found != resolvedSpace {
				return "", fmt.Errorf(
					"mcp: batch spans multiple spaces: %s resolves to %q, %s resolves to %q — one call targets one space",
					resolvedID, resolvedSpace, id, found)
			}
		}
		return resolvedSpace, nil
	}

	primary := byID[aID]
	primary.ResolveSpace = resolveSpace
	primary.SpaceOfArtifacts = spaceOfArtifacts
	return primary
}

// testTwoSpaceWriteDeps is testTwoSpaceWriteDepsWithSystem's own-system="beta"
// default, matching testWriteDeps' own default — the shape every
// generic-verb/note test below needs (respond's own recipient is "beta");
// verify/dispute's RoleOwner="axon" tests call the WithSystem form directly.
func testTwoSpaceWriteDeps(aID, aDir string, fakeA Funnel, bID, bDir string, fakeB Funnel) WriteDeps {
	return testTwoSpaceWriteDepsWithSystem("beta", aID, aDir, fakeA, bID, bDir, fakeB)
}

// TestLifecycleHandlerMultiSpaceDerivesFromSecondSpaceIDs is the test that
// must redden without the change: ids living ONLY in the second connected
// space must build their request against that space's own MirrorDir, never
// silently against cfg.Spaces[0] (here, space-a, which holds nothing).
func TestLifecycleHandlerMultiSpaceDerivesFromSecondSpaceIDs(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	id := "XQ-axon-20260721-msa1"
	writeQuestionArtifact(t, mirrorB, id, "beta")
	writeLifecycleEvent(t, mirrorB, "axon", 0, id, "submit", "axon")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps) // ack

	args, _ := json.Marshal(LifecycleInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("ack against the second space failed: %v", err)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("expected the write to land in space-b's funnel, got %d calls", len(fakeB.calls))
	}
	if len(fakeA.calls) != 0 {
		t.Fatalf("expected space-a's funnel to see NO calls, got %d", len(fakeA.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("RepoDir = %q, want the second space's mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestLifecycleHandlerMultiSpaceBatchSpanningRefused covers a batch whose
// ids span two spaces: the refusal comes from SpaceOfArtifacts, and the
// handler must surface it with its own verb prefix.
func TestLifecycleHandlerMultiSpaceBatchSpanningRefused(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	idA := "XQ-axon-20260721-msb1"
	idB := "XQ-axon-20260721-msb2"
	writeQuestionArtifact(t, mirrorA, idA, "beta")
	writeLifecycleEvent(t, mirrorA, "axon", 0, idA, "submit", "axon")
	writeQuestionArtifact(t, mirrorB, idB, "beta")
	writeLifecycleEvent(t, mirrorB, "axon", 0, idB, "submit", "axon")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps)

	args, _ := json.Marshal(LifecycleInput{IDs: []string{idA, idB}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal for a batch spanning two spaces")
	}
	if !strings.HasPrefix(err.Error(), "ack:") {
		t.Fatalf("expected the refusal to carry the ack: verb prefix, got %v", err)
	}
	if !strings.Contains(err.Error(), "space-a") || !strings.Contains(err.Error(), "space-b") {
		t.Fatalf("expected the refusal to name BOTH spaces, got %v", err)
	}
	if len(fakeA.calls) != 0 || len(fakeB.calls) != 0 {
		t.Fatalf("expected neither funnel to be called, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}
}

// TestLifecycleHandlerMultiSpaceUnknownArtifactRefused covers an id no
// connected mirror holds: refused naming the connected spaces.
func TestLifecycleHandlerMultiSpaceUnknownArtifactRefused(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps)

	args, _ := json.Marshal(LifecycleInput{IDs: []string{"XQ-axon-20260721-nowhere"}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected a refusal for an id no connected mirror holds")
	}
	if !strings.Contains(err.Error(), "space-a") || !strings.Contains(err.Error(), "space-b") {
		t.Fatalf("expected the refusal to name the connected spaces, got %v", err)
	}
	if len(fakeA.calls) != 0 || len(fakeB.calls) != 0 {
		t.Fatalf("expected neither funnel to be called, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}
}

// TestLifecycleHandlerSingleSpaceResolveSpaceNilUnchanged is the single-space
// control: deps.ResolveSpace is nil (testWriteDeps' own default), and the
// call must land exactly where it always did.
func TestLifecycleHandlerSingleSpaceResolveSpaceNilUnchanged(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-ss01"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	if deps.ResolveSpace != nil {
		t.Fatal("testWriteDeps must build a single-space WriteDeps (ResolveSpace nil)")
	}
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps)

	args, _ := json.Marshal(LifecycleInput{IDs: []string{id}})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("single-space ack failed: %v", err)
	}
	if len(fake.calls) != 1 || fake.calls[0].RepoDir != mirrorDir {
		t.Fatalf("expected the single-space call to land unchanged in %q, got %+v", mirrorDir, fake.calls)
	}
}

// TestLifecycleHandlerMultiSpacePoisoningAcrossCalls is the poisoning test:
// two sequential calls through ONE constructed handler, targeting different
// spaces, each landing in its own — the resolved deps must be a shadowed
// LOCAL inside the closure, never an assignment to the captured deps that
// every later call through the same handler would share (see
// newLifecycleHandler's own doc comment). A resolved WriteDeps carries no
// ResolveSpace/SpaceOfArtifacts of its own (value semantics, matching
// wire.go's bySpace map) — so if call 1's resolved deps leaked into the
// captured variable, call 2 (a DIFFERENT space) would silently keep call 1's
// ResolveSpace==nil deps and fail to resolve at all, surfacing as a spurious
// "cannot read" error against the wrong mirror rather than landing in
// space-b's funnel.
func TestLifecycleHandlerMultiSpacePoisoningAcrossCalls(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	idA := "XQ-axon-20260721-psa1"
	idB := "XQ-axon-20260721-psb1"
	writeQuestionArtifact(t, mirrorA, idA, "beta")
	writeLifecycleEvent(t, mirrorA, "axon", 0, idA, "submit", "axon")
	writeQuestionArtifact(t, mirrorB, idB, "beta")
	writeLifecycleEvent(t, mirrorB, "axon", 0, idB, "submit", "axon")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newLifecycleHandler(LifecycleVerbTable[0], deps) // constructed ONCE

	args1, _ := json.Marshal(LifecycleInput{IDs: []string{idA}})
	if _, _, err := handler(context.Background(), args1); err != nil {
		t.Fatalf("first call (space-a) failed: %v", err)
	}
	if len(fakeA.calls) != 1 || len(fakeB.calls) != 0 {
		t.Fatalf("after call 1: expected a=1 b=0, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}

	args2, _ := json.Marshal(LifecycleInput{IDs: []string{idB}})
	if _, _, err := handler(context.Background(), args2); err != nil {
		t.Fatalf("second call (space-b), through the SAME handler, failed: %v — a poisoned deps from call 1 would try space-a's mirror for a space-b id", err)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("after call 2: expected space-b's funnel to see exactly 1 call, got %d", len(fakeB.calls))
	}
	if len(fakeA.calls) != 1 {
		t.Fatalf("second call poisoned space-a's funnel: expected it to stay at 1, got %d", len(fakeA.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("second call's RepoDir = %q, want space-b's mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestRespondHandlerMultiSpaceDerivesFromParentIDs is the respond family's
// own id source: parent_ids, not ids.
func TestRespondHandlerMultiSpaceDerivesFromParentIDs(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	id := "XQ-axon-20260721-msr1"
	writeQuestionArtifact(t, mirrorB, id, "beta")
	writeLifecycleEvent(t, mirrorB, "axon", 0, id, "submit", "axon")
	writeLifecycleEvent(t, mirrorB, "beta", 1, id, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorB, "beta", 2, id, "accept", "beta")
	writeLifecycleEvent(t, mirrorB, "beta", 3, id, "start", "beta")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newRespondHandler(deps)

	args, _ := json.Marshal(RespondInput{ParentIDs: []string{id}, Result: "answered"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("respond against the second space failed: %v", err)
	}
	if len(fakeB.calls) != 1 || len(fakeA.calls) != 0 {
		t.Fatalf("expected respond to land in space-b only, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("RepoDir = %q, want %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestVerifyHandlerMultiSpaceDerivesFromTargets is the "needs care" case
// called out for this handler: the space is derived from in.Targets (here,
// the PARENT id, not the response id, so resolveResponseID's own
// deps.MirrorDir read below the derivation exercises the RESOLVED deps, not
// the captured one).
func TestVerifyHandlerMultiSpaceDerivesFromTargets(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	parentID := "XQ-axon-20260721-msv1"
	writeQuestionArtifact(t, mirrorB, parentID, "beta")
	writeLifecycleEvent(t, mirrorB, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorB, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorB, "beta", 2, parentID, "accept", "beta")

	respondFake := &fakeFunnel{}
	respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{parentID}, Result: "answered"})
	if _, _, err := newRespondHandler(testWriteDeps(mirrorB, respondFake))(context.Background(), respondArgs); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	for _, fw := range respondFake.calls[0].Files {
		if err := writeFileAllDirs(filepath.Join(mirrorB, fw.Path), fw.Content); err != nil {
			t.Fatalf("materialize %s: %v", fw.Path, err)
		}
	}

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	// verify's role is RoleOwner (the parent's original requester, axon).
	deps := testTwoSpaceWriteDepsWithSystem("axon", "space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newVerifyHandler(deps)

	// targets the PARENT id (not the response id) so resolveResponseID must
	// walk readAllEvents against the RESOLVED mirror to find the response.
	args, _ := json.Marshal(VerifyInput{Targets: []string{parentID}})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("verify against the second space failed: %v", err)
	}
	if len(fakeB.calls) != 1 || len(fakeA.calls) != 0 {
		t.Fatalf("expected verify to land in space-b only, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("RepoDir = %q, want %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestDisputeHandlerMultiSpaceDerivesFromIDs is the dispute family's own id
// source: ids (response ids), same field name as lifecycle's but a distinct
// input type — RoleOwner ("axon") disputes the response.
func TestDisputeHandlerMultiSpaceDerivesFromIDs(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	parentID := "XQ-axon-20260721-msd1"
	writeQuestionArtifact(t, mirrorB, parentID, "beta")
	writeLifecycleEvent(t, mirrorB, "axon", 0, parentID, "submit", "axon")
	writeLifecycleEvent(t, mirrorB, "beta", 1, parentID, "acknowledge", "beta")
	writeLifecycleEvent(t, mirrorB, "beta", 2, parentID, "accept", "beta")

	respondFake := &fakeFunnel{}
	respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{parentID}, Result: "answered"})
	if _, _, err := newRespondHandler(testWriteDeps(mirrorB, respondFake))(context.Background(), respondArgs); err != nil {
		t.Fatalf("respond failed: %v", err)
	}
	for _, fw := range respondFake.calls[0].Files {
		if err := writeFileAllDirs(filepath.Join(mirrorB, fw.Path), fw.Content); err != nil {
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

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDepsWithSystem("axon", "space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newDisputeHandler(deps)

	args, _ := json.Marshal(DisputeInput{IDs: []string{responseID}, Reason: "wrong answer"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("dispute against the second space failed: %v", err)
	}
	if len(fakeB.calls) != 1 || len(fakeA.calls) != 0 {
		t.Fatalf("expected dispute to land in space-b only, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("RepoDir = %q, want %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestNoteHandlerMultiSpaceDerivesFromIDs is the note family's own id source:
// ids, no legality precondition (D-025), so this only needs the envelope
// itself.
func TestNoteHandlerMultiSpaceDerivesFromIDs(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	id := "XQ-axon-20260721-msn1"
	writeQuestionArtifact(t, mirrorB, id, "beta")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newNoteHandler(deps)

	args, _ := json.Marshal(NoteInput{IDs: []string{id}, Note: "fyi"})
	_, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("note against the second space failed: %v", err)
	}
	if len(fakeB.calls) != 1 || len(fakeA.calls) != 0 {
		t.Fatalf("expected note to land in space-b only, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("RepoDir = %q, want %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestRespondHandlerDoesNotPoisonAcrossCalls is the poisoning test for
// newRespondHandler (audit MED finding): two sequential calls through ONE
// constructed handler, each deriving its target space from parent_ids, must
// never let call 1's resolved deps leak into call 2 through the captured
// deps variable — see TestLifecycleHandlerMultiSpacePoisoningAcrossCalls's
// own doc comment for why a single-call test cannot see this.
func TestRespondHandlerDoesNotPoisonAcrossCalls(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	idA := "XQ-axon-20260721-rpa1"
	idB := "XQ-axon-20260721-rpb1"
	for _, pair := range []struct {
		dir string
		id  string
	}{{mirrorA, idA}, {mirrorB, idB}} {
		writeQuestionArtifact(t, pair.dir, pair.id, "beta")
		writeLifecycleEvent(t, pair.dir, "axon", 0, pair.id, "submit", "axon")
		writeLifecycleEvent(t, pair.dir, "beta", 1, pair.id, "acknowledge", "beta")
		writeLifecycleEvent(t, pair.dir, "beta", 2, pair.id, "accept", "beta")
		writeLifecycleEvent(t, pair.dir, "beta", 3, pair.id, "start", "beta")
	}

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newRespondHandler(deps) // constructed ONCE

	args1, _ := json.Marshal(RespondInput{ParentIDs: []string{idA}, Result: "answered"})
	if _, _, err := handler(context.Background(), args1); err != nil {
		t.Fatalf("first call (space-a) failed: %v", err)
	}
	if len(fakeA.calls) != 1 || len(fakeB.calls) != 0 {
		t.Fatalf("after call 1: expected a=1 b=0, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}

	args2, _ := json.Marshal(RespondInput{ParentIDs: []string{idB}, Result: "answered"})
	if _, _, err := handler(context.Background(), args2); err != nil {
		t.Fatalf("second call (space-b), through the SAME handler, failed: %v — a poisoned deps from call 1 would try space-a's mirror for a space-b id", err)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("after call 2: expected space-b's funnel to see exactly 1 call, got %d", len(fakeB.calls))
	}
	if len(fakeA.calls) != 1 {
		t.Fatalf("second call poisoned space-a's funnel: expected it to stay at 1, got %d", len(fakeA.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("second call's RepoDir = %q, want space-b's mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestVerifyHandlerDoesNotPoisonAcrossCalls is the poisoning test for
// newVerifyHandler: two sequential calls through ONE constructed handler,
// each deriving its target space from targets (see
// TestVerifyHandlerMultiSpaceDerivesFromTargets's own doc comment for why
// this handler's resolveResponseID read needs care — it must run against the
// RESOLVED deps, not the captured one), must never let call 1's resolved
// deps leak into call 2.
func TestVerifyHandlerDoesNotPoisonAcrossCalls(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	parentIDA := "XQ-axon-20260721-vpa1"
	parentIDB := "XQ-axon-20260721-vpb1"

	for _, pair := range []struct {
		dir string
		id  string
	}{{mirrorA, parentIDA}, {mirrorB, parentIDB}} {
		writeQuestionArtifact(t, pair.dir, pair.id, "beta")
		writeLifecycleEvent(t, pair.dir, "axon", 0, pair.id, "submit", "axon")
		writeLifecycleEvent(t, pair.dir, "beta", 1, pair.id, "acknowledge", "beta")
		writeLifecycleEvent(t, pair.dir, "beta", 2, pair.id, "accept", "beta")

		respondFake := &fakeFunnel{}
		respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{pair.id}, Result: "answered"})
		if _, _, err := newRespondHandler(testWriteDeps(pair.dir, respondFake))(context.Background(), respondArgs); err != nil {
			t.Fatalf("respond fixture for %s failed: %v", pair.dir, err)
		}
		for _, fw := range respondFake.calls[0].Files {
			if err := writeFileAllDirs(filepath.Join(pair.dir, fw.Path), fw.Content); err != nil {
				t.Fatalf("materialize %s: %v", fw.Path, err)
			}
		}
	}

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	// verify's role is RoleOwner (the parent's original requester, axon).
	deps := testTwoSpaceWriteDepsWithSystem("axon", "space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newVerifyHandler(deps) // constructed ONCE

	args1, _ := json.Marshal(VerifyInput{Targets: []string{parentIDA}})
	if _, _, err := handler(context.Background(), args1); err != nil {
		t.Fatalf("first call (space-a) failed: %v", err)
	}
	if len(fakeA.calls) != 1 || len(fakeB.calls) != 0 {
		t.Fatalf("after call 1: expected a=1 b=0, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}

	args2, _ := json.Marshal(VerifyInput{Targets: []string{parentIDB}})
	if _, _, err := handler(context.Background(), args2); err != nil {
		t.Fatalf("second call (space-b), through the SAME handler, failed: %v — a poisoned deps from call 1 would try space-a's mirror for a space-b target", err)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("after call 2: expected space-b's funnel to see exactly 1 call, got %d", len(fakeB.calls))
	}
	if len(fakeA.calls) != 1 {
		t.Fatalf("second call poisoned space-a's funnel: expected it to stay at 1, got %d", len(fakeA.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("second call's RepoDir = %q, want space-b's mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestDisputeHandlerDoesNotPoisonAcrossCalls is the poisoning test for
// newDisputeHandler: two sequential calls through ONE constructed handler,
// each deriving its target space from ids (response ids), must never let
// call 1's resolved deps leak into call 2.
func TestDisputeHandlerDoesNotPoisonAcrossCalls(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	parentIDA := "XQ-axon-20260721-dpa1"
	parentIDB := "XQ-axon-20260721-dpb1"

	responseID := make(map[string]string, 2)
	for _, pair := range []struct {
		dir string
		id  string
	}{{mirrorA, parentIDA}, {mirrorB, parentIDB}} {
		writeQuestionArtifact(t, pair.dir, pair.id, "beta")
		writeLifecycleEvent(t, pair.dir, "axon", 0, pair.id, "submit", "axon")
		writeLifecycleEvent(t, pair.dir, "beta", 1, pair.id, "acknowledge", "beta")
		writeLifecycleEvent(t, pair.dir, "beta", 2, pair.id, "accept", "beta")

		respondFake := &fakeFunnel{}
		respondArgs, _ := json.Marshal(RespondInput{ParentIDs: []string{pair.id}, Result: "answered"})
		if _, _, err := newRespondHandler(testWriteDeps(pair.dir, respondFake))(context.Background(), respondArgs); err != nil {
			t.Fatalf("respond fixture for %s failed: %v", pair.dir, err)
		}
		for _, fw := range respondFake.calls[0].Files {
			if err := writeFileAllDirs(filepath.Join(pair.dir, fw.Path), fw.Content); err != nil {
				t.Fatalf("materialize %s: %v", fw.Path, err)
			}
		}
		for _, fw := range respondFake.calls[0].Files {
			base := filepath.Base(fw.Path)
			if strings.HasPrefix(base, "XS-") {
				responseID[pair.dir] = strings.TrimSuffix(base, ".md")
			}
		}
		if responseID[pair.dir] == "" {
			t.Fatalf("could not find minted response id in %+v", respondFake.calls[0].Files)
		}
	}

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDepsWithSystem("axon", "space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newDisputeHandler(deps) // constructed ONCE

	args1, _ := json.Marshal(DisputeInput{IDs: []string{responseID[mirrorA]}, Reason: "wrong answer"})
	if _, _, err := handler(context.Background(), args1); err != nil {
		t.Fatalf("first call (space-a) failed: %v", err)
	}
	if len(fakeA.calls) != 1 || len(fakeB.calls) != 0 {
		t.Fatalf("after call 1: expected a=1 b=0, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}

	args2, _ := json.Marshal(DisputeInput{IDs: []string{responseID[mirrorB]}, Reason: "wrong answer"})
	if _, _, err := handler(context.Background(), args2); err != nil {
		t.Fatalf("second call (space-b), through the SAME handler, failed: %v — a poisoned deps from call 1 would try space-a's mirror for a space-b id", err)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("after call 2: expected space-b's funnel to see exactly 1 call, got %d", len(fakeB.calls))
	}
	if len(fakeA.calls) != 1 {
		t.Fatalf("second call poisoned space-a's funnel: expected it to stay at 1, got %d", len(fakeA.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("second call's RepoDir = %q, want space-b's mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// TestNoteHandlerDoesNotPoisonAcrossCalls is the poisoning test for
// newNoteHandler: two sequential calls through ONE constructed handler,
// each deriving its target space from ids, must never let call 1's resolved
// deps leak into call 2.
func TestNoteHandlerDoesNotPoisonAcrossCalls(t *testing.T) {
	t.Parallel()
	mirrorA := t.TempDir()
	mirrorB := t.TempDir()
	idA := "XQ-axon-20260721-npa1"
	idB := "XQ-axon-20260721-npb1"
	writeQuestionArtifact(t, mirrorA, idA, "beta")
	writeQuestionArtifact(t, mirrorB, idB, "beta")

	fakeA := &fakeFunnel{}
	fakeB := &fakeFunnel{}
	deps := testTwoSpaceWriteDeps("space-a", mirrorA, fakeA, "space-b", mirrorB, fakeB)
	handler := newNoteHandler(deps) // constructed ONCE

	args1, _ := json.Marshal(NoteInput{IDs: []string{idA}, Note: "fyi"})
	if _, _, err := handler(context.Background(), args1); err != nil {
		t.Fatalf("first call (space-a) failed: %v", err)
	}
	if len(fakeA.calls) != 1 || len(fakeB.calls) != 0 {
		t.Fatalf("after call 1: expected a=1 b=0, got a=%d b=%d", len(fakeA.calls), len(fakeB.calls))
	}

	args2, _ := json.Marshal(NoteInput{IDs: []string{idB}, Note: "fyi"})
	if _, _, err := handler(context.Background(), args2); err != nil {
		t.Fatalf("second call (space-b), through the SAME handler, failed: %v — a poisoned deps from call 1 would try space-a's mirror for a space-b id", err)
	}
	if len(fakeB.calls) != 1 {
		t.Fatalf("after call 2: expected space-b's funnel to see exactly 1 call, got %d", len(fakeB.calls))
	}
	if len(fakeA.calls) != 1 {
		t.Fatalf("second call poisoned space-a's funnel: expected it to stay at 1, got %d", len(fakeA.calls))
	}
	if fakeB.calls[0].RepoDir != mirrorB {
		t.Fatalf("second call's RepoDir = %q, want space-b's mirror %q", fakeB.calls[0].RepoDir, mirrorB)
	}
}

// --- delivers (judge-the-thing-2026-08 P2, closing half of B22) -----------

// respondDeliversFile finds the ONE drafted XS- response file among a
// respond call's Files, mirroring the extraction internal/cli's own
// extractResponseContent (cmd_lifecycle_test.go, off this phase's
// allowlist) does at its own tier.
func respondDeliversFile(t *testing.T, files []space.FileWrite) string {
	t.Helper()
	for _, fw := range files {
		if strings.HasPrefix(filepath.Base(fw.Path), "XS-") {
			return string(fw.Content)
		}
	}
	t.Fatalf("could not find a drafted XS- response file among %+v", files)
	return ""
}

// TestRespondHandlerWritesDeliversInGivenOrder is the MCP half of §8
// criterion 1's "ordering" edge case (spec 02 §6): `delivers[]` is a
// SEQUENCE on the wire, so the given order survives into the committed
// response, never sorted — internal/cli's own
// TestRespondWritesDeliversInGivenOrder pins the identical rule at its own
// tier.
func TestRespondHandlerWritesDeliversInGivenOrder(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-dvm1"
	mcpSeedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{
		ParentIDs: []string{parentID}, Result: "delivered",
		Delivers: []string{"DP-beta-20260821-dpk2", "DP-beta-20260821-dpk1"},
	}
	args, _ := json.Marshal(in)
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("respond delivers: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 funnel call, got %d", len(fake.calls))
	}
	content := respondDeliversFile(t, fake.calls[0].Files)
	first := strings.Index(content, "DP-beta-20260821-dpk2")
	second := strings.Index(content, "DP-beta-20260821-dpk1")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("expected delivers written in GIVEN order (dpk2 before dpk1), got:\n%s", content)
	}
}

// TestRespondHandlerWithoutDeliversOmitsTheKey is the oracle at MCP tier
// (§8 criterion 3, P-1): an ordinary `result: delivered` answer with no
// `delivers` given carries no `delivers` key at all — internal/cli's own
// TestRespondWithoutDeliversWritesNoKey is the CLI half of this same rule.
func TestRespondHandlerWithoutDeliversOmitsTheKey(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-dvm2"
	mcpSeedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{ParentIDs: []string{parentID}, Result: "delivered"}
	args, _ := json.Marshal(in)
	if _, _, err := handler(context.Background(), args); err != nil {
		t.Fatalf("respond delivered (no delivers): %v", err)
	}
	content := respondDeliversFile(t, fake.calls[0].Files)
	if strings.Contains(content, "delivers") {
		t.Fatalf("the rendered response mentions `delivers` with none given:\n%s", content)
	}
}

// TestRespondHandlerEmptyDeliversIsRefused mirrors internal/cli's own
// TestRespondEmptyDeliversIsRefused: an empty (or whitespace-only) entry is
// refused before ever reaching the funnel, the same as every other
// repeatable id-bearing field on this handler.
func TestRespondHandlerEmptyDeliversIsRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	parentID := "XQ-axon-20260721-dvm4"
	mcpSeedAcceptedQuestion(t, mirrorDir, parentID, "beta")

	fake := &fakeFunnel{}
	deps := testWriteDeps(mirrorDir, fake)
	handler := newRespondHandler(deps)
	in := RespondInput{ParentIDs: []string{parentID}, Result: "delivered", Delivers: []string{""}}
	args, _ := json.Marshal(in)
	if _, _, err := handler(context.Background(), args); err == nil {
		t.Fatal("respond delivers ['']: want a refusal, got nil")
	}
}

// TestRespondHandlerRefusesUnlandedDeliversThroughTheRealFunnel is §8
// criterion 2 at the MCP tier, driven through the production entry point
// (respondWithRealValidation's real schema.Load/validate.New/
// space.NewWriteFunnel stack) rather than a direct call to the check —
// internal/cli's own TestRespondRefusesUnlandedDeliversThroughTheRealFunnel
// pins the identical rule at its own tier: a response naming a package
// whose payload PR has not merged is refused (REF-024), naming the
// package, with zero pushes/opens — the SAME funnel seat both surfaces
// reach through funnel.Submit (ADR-004), so this surface inherits the
// refusal the moment it can author the field at all.
func TestRespondHandlerRefusesUnlandedDeliversThroughTheRealFunnel(t *testing.T) {
	t.Parallel()
	parentID := "XQ-axon-20260721-dvm3"
	packageID := "DP-beta-20260821-dpk9"
	_, fakeHost, callErr := respondWithRealValidation(t, parentID, RespondInput{
		Result: "delivered", Delivers: []string{packageID},
	})
	if callErr == nil {
		t.Fatal("respond delivers <unlanded>: want a refusal, got nil")
	}
	if !strings.Contains(callErr.Error(), "REF-024") {
		t.Fatalf("expected the refusal to name REF-024, got: %v", callErr)
	}
	if !strings.Contains(callErr.Error(), packageID) {
		t.Fatalf("expected the refusal to name the package %q, got: %v", packageID, callErr)
	}
	if len(fakeHost.Pushes) != 0 || len(fakeHost.Opens) != 0 {
		t.Fatalf("expected zero pushes/opens on a refused response, got %d/%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}
