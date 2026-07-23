package cli_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// fakeSubmitFunnel is a hand-written test double for cmd_submit.go's own
// unexported submitFunnel seam (Go's structural typing lets an external
// test satisfy it without naming it) — used by tests that must prove the
// funnel (a git/host call) is NEVER reached.
type fakeSubmitFunnel struct {
	calls  []space.SubmitRequest
	result space.WriteResult
	err    error
}

func (f *fakeSubmitFunnel) Submit(_ context.Context, req space.SubmitRequest) (space.WriteResult, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return space.WriteResult{}, f.err
	}
	if f.result.State == "" {
		return space.WriteResult{State: space.WriteStatePendingMerge, PRNumber: 1, PRURL: "https://example.invalid/pr/1", Branch: req.ArtifactID}, nil
	}
	return f.result, nil
}

func writeQuestionDraft(t *testing.T, dir, id, from, to string) string {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: " + from + "\n" +
		"to: [" + to + "]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	return path
}

func testManifest() space.Manifest {
	return space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"},
		{System: "other", Status: "active"},
	}}
}

// testHostConfig is a generic, fully-populated SubmitHostConfig for
// tests that never reach a real host call (the foreign-section/
// idempotency short-circuit tests) but must still construct a
// SubmitCommand.
func testHostConfig() cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL:         "https://example.invalid/org/space.git",
		Repo:              host.Repo{Owner: "org", Name: "space"},
		BaseBranch:        "main",
		Credential:        host.Credential{Token: "test-token"},
		CommitAuthorName:  "a2a-axon",
		CommitAuthorEmail: "a2a-axon@a2ahub.invalid",
	}
}

// fixtureHostConfig is a SubmitHostConfig wired to a spacefixture.Fixture
// for the end-to-end tests, so RemoteURL genuinely matches the fixture's
// own local git remote.
func fixtureHostConfig(fx *spacefixture.Fixture) cli.SubmitHostConfig {
	cfg := testHostConfig()
	cfg.RemoteURL = fx.RemoteURL()
	return cfg
}

// TestSubmitForeignSectionRefusal is AC-201.3: an artifact whose `from`
// does not match the configured own system is refused locally, exits
// non-zero, and the write funnel is NEVER called (no git/network call).
func TestSubmitForeignSectionRefusal(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-other-20260721-k3f9", "other", "axon")

	mirrorDir := t.TempDir()
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a foreign-section artifact")
	}
	if !strings.Contains(errOut.String(), "CC-002") {
		t.Fatalf("expected the refusal message to name CC-002; got %q", errOut.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called; got %d call(s)", len(fake.calls))
	}
}

// TestSubmitForeignSectionRefusalOwnSystemForeignToIsNotRefused is the
// §6 edge case: an own-system artifact with a foreign `to` must NOT be
// refused — refusal is about the acting section (`from`), not the
// addressee.
func TestSubmitOwnSystemForeignToIsNotRefused(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	mirrorDir := t.TempDir()
	writeMinimalSpaceYAML(t, mirrorDir)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (own from, foreign to, must not be refused); stderr=%s", code, errOut.String())
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly one funnel call, got %d", len(fake.calls))
	}
	if fake.calls[0].MinBinaryVersion != "0.1.0" {
		t.Fatalf("MinBinaryVersion = %q, want 0.1.0 (read from the mirror's space.yaml, CC-085)", fake.calls[0].MinBinaryVersion)
	}
	if fake.calls[0].RemoteURL == "" || fake.calls[0].BaseBranch == "" || fake.calls[0].CommitAuthorName == "" {
		t.Fatalf("expected the SubmitRequest to carry the host config through: %+v", fake.calls[0])
	}
}

// writeMinimalSpaceYAML writes a bare space.yaml with only the
// min_binary_version field this package's submit path reads (CC-085) —
// deliberately NOT the full space.Manifest shape (testkit/spacefixture's
// own seeded space.yaml uses a map-shaped `participants:` block that does
// not structurally decode into space.Manifest.Participants ([]Participant)
// either; this phase's own min_binary_version read is its own minimal,
// permissive decode for exactly this reason — see readMinBinaryVersion's
// doc comment in cmd_submit.go).
func writeMinimalSpaceYAML(t *testing.T, mirrorDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(mirrorDir, "space.yaml"), []byte("min_binary_version: \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatalf("write space.yaml: %v", err)
	}
}

// TestSubmitIdempotentAlreadySubmitted is AC-301.1: re-running submit on
// an artifact whose id already has a committed event is a no-op "already
// done" — exit 0, and the write funnel is never called again.
func TestSubmitIdempotentAlreadySubmitted(t *testing.T) {
	t.Parallel()
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	mirrorDir := t.TempDir()
	writeCommittedEvent(t, mirrorDir, "axon", "2026", "01J8QYK2Z3ABCDEFGHJKMNPQRS", "XQ-axon-20260721-k3f9", "submit", "axon")
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", testManifest())
	fake := &fakeSubmitFunnel{}
	cmd := cli.NewSubmitCommand(fake, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, testHostConfig())

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (idempotent already-submitted no-op)", code)
	}
	if !strings.Contains(out.String(), "already submitted") {
		t.Fatalf("expected an 'already submitted' message; got %q", out.String())
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected the write funnel NEVER to be called on an already-submitted re-run; got %d call(s)", len(fake.calls))
	}
}

// --- end-to-end (real WriteFunnel + FakeHost + spacefixture) ---------------

func newRealFunnelDeps(t *testing.T) (*space.WriteFunnel, *cli.LegalityAdapter, string, *spacefixture.Fixture) {
	t.Helper()
	fx := spacefixture.New(t, "axon")
	mirrorDir := fx.Clone("axon")
	manifest := testManifest()

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)
	legality := cli.NewLegalityAdapter(mirrorDir, "axon", manifest)
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, "axon", resolver, legality)

	fake := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fake, validator, "0.1.0")
	return funnel, legality, mirrorDir, fx
}

// TestSubmitEndToEndSingleArtifact drives the whole submit pipeline
// (foreign-section check -> idempotency check -> V2 via the real
// validate.Engine -> the real write funnel against a local git fixture)
// for a single, valid artifact.
func TestSubmitEndToEndSingleArtifact(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)
	stagingDir := t.TempDir()
	path := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-k3f9", "axon", "other")

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{path}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message; got %q", out.String())
	}

	changed := gitDiffNames(t, mirrorDir, "main", "a2a/axon/submit/XQ-axon-20260721-k3f9")
	if len(changed) != 2 {
		t.Fatalf("changed files = %v, want exactly 2 (artifact + event)", changed)
	}
	_ = fx
}

// TestSubmitBatchAllOrNothing is spec 06 §8 acceptance row 4: one
// V2-invalid artifact among N aborts the whole batch — zero pushed, no
// new commit.
func TestSubmitBatchAllOrNothing(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)
	stagingDir := t.TempDir()
	p1 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-aaa1", "axon", "other")
	p2 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-aaa2", "axon", "other")
	// Third draft is missing the required `category` field -> V2-invalid.
	invalid := "---\n" +
		"schema: envelope/v1\n" +
		"id: XQ-axon-20260721-aaa3\n" +
		"type: question\n" +
		"title: t\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [other]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	p3 := filepath.Join(stagingDir, "XQ-axon-20260721-aaa3.md")
	if err := os.WriteFile(p3, []byte(invalid), 0o644); err != nil {
		t.Fatalf("write invalid draft: %v", err)
	}

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--batch", p1, p2, p3}, io)
	if code == 0 {
		t.Fatal("expected a non-zero exit for a batch containing one V2-invalid artifact")
	}
	if errOut.Len() == 0 {
		t.Fatal("expected an actionable error message")
	}

	count := gitRevListCount(t, mirrorDir, "main", "a2a/axon/submit/*")
	if count != 0 {
		t.Fatalf("expected zero new commits on an aborted batch, found %d branch(es)/commit(s)", count)
	}
}

// TestSubmitBatchOneCommitNEvents is spec 06 §8 acceptance row 5: a batch
// of N valid artifacts produces exactly one commit and N submit events.
func TestSubmitBatchOneCommitNEvents(t *testing.T) {
	t.Parallel()
	funnel, legality, mirrorDir, fx := newRealFunnelDeps(t)
	stagingDir := t.TempDir()
	p1 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-bbb1", "axon", "other")
	p2 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-bbb2", "axon", "other")
	p3 := writeQuestionDraft(t, stagingDir, "XQ-axon-20260721-bbb3", "axon", "other")

	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, "fixture-space", "axon", stagingDir, fixtureHostConfig(fx))
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--batch", p1, p2, p3}, io)
	if code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	branch := "a2a/axon/submit/XQ-axon-20260721-bbb1+XQ-axon-20260721-bbb2+XQ-axon-20260721-bbb3"
	commits := gitRevListCountBranch(t, mirrorDir, "main", branch)
	if commits != 1 {
		t.Fatalf("commits ahead of main on %s = %d, want exactly 1", branch, commits)
	}
	changed := gitDiffNames(t, mirrorDir, "main", branch)
	if len(changed) != 6 { // 3 artifacts + 3 events
		t.Fatalf("changed files = %v, want exactly 6 (3 artifacts + 3 events)", changed)
	}
}

// gitDiffNames/gitRevListCount(Branch) are small git-plumbing test
// helpers (explicit argv, never sh -c) mirroring internal/space's own
// test-file idiom (funnel_test.go).
func gitDiffNames(t *testing.T, dir, base, head string) []string {
	t.Helper()
	out := runGitOutputForTest(t, dir, "diff", "--name-only", base, head)
	return strings.Fields(out)
}

func gitRevListCountBranch(t *testing.T, dir, base, head string) int {
	t.Helper()
	out := runGitOutputForTest(t, dir, "rev-list", "--count", base+".."+head)
	n := 0
	for _, c := range out {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// gitRevListCount reports whether ANY ref matching pattern exists ahead
// of base (used by the all-or-nothing test, which does not know the exact
// branch name an aborted batch would have used).
func gitRevListCount(t *testing.T, dir, base, branchGlob string) int {
	t.Helper()
	out := runGitOutputForTestAllowFail(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/a2a/axon/")
	if strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Fields(out))
}

func runGitOutputForTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitCombined(dir, args...)
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(out)
}

func runGitOutputForTestAllowFail(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, _ := runGitCombined(dir, args...)
	return out
}

func runGitCombined(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}
