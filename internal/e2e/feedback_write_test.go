package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/feedback"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// feedbackSubmitTestID is a hand-fixed feedback report id (NOT minted via
// feedback.MintFeedbackID's crypto/rand seam) so this test's branch/PR-
// title/committed-path assertions are deterministic.
const feedbackSubmitTestID = "fb-20260701-1a2b3c"

// writeFeedbackDraft writes a schema-valid, checks-all-true, status:new
// feedback report (kind:friction needs no evidence block) — the spec 25
// §11 A5 direct-construction submit test's own input.
func writeFeedbackDraft(t *testing.T, dir string) string {
	t.Helper()
	content := "feedback: v1\n" +
		"id: " + feedbackSubmitTestID + "\n" +
		"kind: friction\n" +
		"severity: minor\n" +
		"title: \"inbox command floods small-context agents with noise\"\n" +
		"summary: \"grounded in this session's own inbox output review\"\n" +
		"context:\n" +
		"  a2a_version: 0.1.0\n" +
		"  os_arch: darwin/arm64\n" +
		"  surface: cli\n" +
		"checks:\n" +
		"  docs_consulted: true\n" +
		"  grounded_in_real_work: true\n" +
		"  not_space_specific: true\n" +
		"  no_sensitive_content: true\n" +
		"  duplicates_checked: true\n" +
		"status: new\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, feedbackSubmitTestID+".yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write draft: %v", err)
	}
	return path
}

// TestFeedbackSubmitWrite is spec 25 §11 A5's mandated direct-construction
// e2e for `a2a feedback submit`: the t3 exec harness cannot reach a host
// (cmd/a2a/wire.go hardcodes githubAPIBaseURL, no env override), so this is
// the TestT3Submit idiom's own copy — real feedback.Submitter + real
// space.WriteFunnel + host.NewFakeHost() + a testkit/spacefixture bare
// origin standing in for the feedback repo's remote (§11 A8: a local
// mirror clone under the project's own .a2a/cache/feedback-repo/<slug>/,
// no space.yaml dependency). Asserts the exact branch/PR-title/committed-
// path contract (§T1, §11 A6) and the idempotent re-submit no-op.
func TestFeedbackSubmitWrite(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")

	projectRoot := t.TempDir()
	stagingDir := t.TempDir()
	path := writeFeedbackDraft(t, stagingDir)

	ledgerPath := filepath.Join(projectRoot, ".a2a", "feedback", "ledger.yaml")
	slug := "a2ahub-feedback-fixture"

	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	submitCfg := feedback.SubmitConfig{
		RemoteURL:         fx.RemoteURL(),
		Repo:              host.Repo{Owner: "a2ahub-fixture", Name: "feedback"},
		BaseBranch:        "main",
		Credential:        host.Credential{},
		CommitAuthorName:  "a2a-feedback",
		CommitAuthorEmail: "a2a-feedback@a2a.local",
	}
	submitter := feedback.NewSubmitter(funnel, ledgerPath, projectRoot, slug, submitCfg)

	cmd := cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", nil)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"submit", path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message; got %q", out.String())
	}

	wantBranch := "a2a/feedback/" + feedbackSubmitTestID
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one real PushBranch + OpenPR call, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	if fakeHost.Pushes[0].Branch != wantBranch {
		t.Fatalf("push branch = %q, want %q", fakeHost.Pushes[0].Branch, wantBranch)
	}
	if fakeHost.Opens[0].Head != wantBranch {
		t.Fatalf("open PR head = %q, want %q", fakeHost.Opens[0].Head, wantBranch)
	}
	wantTitle := "feedback(friction): inbox command floods small-context agents with noise"
	if fakeHost.Opens[0].Title != wantTitle {
		t.Fatalf("open PR title = %q, want %q", fakeHost.Opens[0].Title, wantTitle)
	}

	// The committed file itself is never carried on any host.Host call —
	// the funnel commits it into the mirror clone's own working tree via
	// real git (space.WriteFunnel's commitOne) before ever pushing; assert
	// the path/content contract there (§T1: the single committed file is
	// feedback/inbox/<id>.yaml).
	mirrorDir := filepath.Join(projectRoot, ".a2a", "cache", "feedback-repo", slug)
	committed := filepath.Join(mirrorDir, "feedback", "inbox", feedbackSubmitTestID+".yaml")
	got, err := os.ReadFile(committed)
	if err != nil {
		t.Fatalf("read committed file: %v", err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read source draft: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("committed file content mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}

	wantPRURL := "https://example.invalid/pr/" + wantBranch
	if !strings.Contains(out.String(), wantPRURL) {
		t.Fatalf("expected stdout to report the PR URL %q; got %q", wantPRURL, out.String())
	}

	ledgerItem, err := feedback.FindLedgerItem(ledgerPath, feedbackSubmitTestID)
	if err != nil {
		t.Fatalf("FindLedgerItem: %v", err)
	}
	if ledgerItem == nil {
		t.Fatalf("expected a ledger row for %s, found none", feedbackSubmitTestID)
	}
	if ledgerItem.Kind != "friction" || ledgerItem.PRURL != wantPRURL {
		t.Fatalf("ledger row = %+v, want kind=friction pr_url=%s", ledgerItem, wantPRURL)
	}

	// AC-301.1 idempotent re-run: the SAME draft, resubmitted, is a no-op —
	// WriteFunnel.Submit's own step-0 FindPRByHeadBranch short-circuit fires
	// (the SAME fakeHost/funnel/submitter is reused, so its byBranch record
	// from the first Submit persists) before any second push/open.
	io2, out2, _ := newIO()
	code2 := cmd.Run(context.Background(), []string{"submit", path}, io2)
	if code2 != 0 {
		t.Fatalf("retry: code = %d, want 0 (idempotent no-op); stdout=%s", code2, out2.String())
	}
	if !strings.Contains(out2.String(), "already submitted") {
		t.Fatalf("expected the retry's stdout to report the already-submitted idempotent path, got %q", out2.String())
	}
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one push/open after the retry, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}
