package mcp

import (
	"context"
	"encoding/json"
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
