package cli_test

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

func TestFeedbackSubcommands_MatchDispatch(t *testing.T) {
	t.Parallel()
	want := []string{"new", "validate", "submit", "status", "triage"}
	got := cli.FeedbackSubcommands()
	if len(got) != len(want) {
		t.Fatalf("FeedbackSubcommands() = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("FeedbackSubcommands()[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestFeedbackCommand_UnknownSubcommand(t *testing.T) {
	t.Parallel()
	cmd := cli.NewFeedbackCommand(nil, nil, "", "", nil)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"bogus"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 for an unknown subcommand", code)
	}
}

func TestFeedbackCommand_NoArgs(t *testing.T) {
	t.Parallel()
	cmd := cli.NewFeedbackCommand(nil, nil, "", "", nil)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), nil, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 for no args", code)
	}
}

func TestFeedbackNew_DraftsAndPrintsPath(t *testing.T) {
	t.Parallel()
	draftsDir := filepath.Join(t.TempDir(), ".a2a", "feedback")
	drafter := feedback.NewDrafter(draftsDir, "v0.1.1")
	cmd := cli.NewFeedbackCommand(drafter, nil, "", "", nil)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"new", "bug", "--title", "a2a sync reports clean but stale"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	path := strings.TrimSpace(out.String())
	if filepath.Dir(path) != draftsDir {
		t.Fatalf("printed path = %q, want dir %q", path, draftsDir)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("drafted file does not exist: %v", err)
	}
}

func TestFeedbackNew_UnknownKindIsRefusal(t *testing.T) {
	t.Parallel()
	drafter := feedback.NewDrafter(t.TempDir(), "v0.1.1")
	cmd := cli.NewFeedbackCommand(drafter, nil, "", "", nil)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"new", "wontfix"}, io)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for an unknown kind", code)
	}
}

func TestFeedbackValidate_ExitCodes(t *testing.T) {
	t.Parallel()
	cmd := cli.NewFeedbackCommand(nil, nil, "", "", nil)

	valid := `feedback: v1
id: fb-20260701-1a2b3c
kind: docs
severity: minor
title: "a clean, honestly-cleared report"
summary: "summary"
context:
  a2a_version: v0.1.1
  os_arch: darwin/arm64
  surface: docs
checks:
  docs_consulted: true
  grounded_in_real_work: true
  not_space_specific: true
  no_sensitive_content: true
  duplicates_checked: true
status: new
`
	path := filepath.Join(t.TempDir(), "fb-20260701-1a2b3c.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"validate", path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 for a valid report; stdout=%s", code, out.String())
	}

	badPath := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(badPath, []byte("kind: wontfix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	io2, out2, _ := newIO()
	code2 := cmd.Run(context.Background(), []string{"validate", badPath}, io2)
	if code2 != 1 {
		t.Fatalf("code = %d, want 1 for an invalid report; stdout=%s", code2, out2.String())
	}
	if !strings.Contains(out2.String(), "FB-001") {
		t.Fatalf("expected FB-001 in machine output, got %q", out2.String())
	}

	io3, _, _ := newIO()
	code3 := cmd.Run(context.Background(), []string{"validate"}, io3)
	if code3 != 2 {
		t.Fatalf("code = %d, want 2 for a usage error (missing file arg)", code3)
	}
}

func TestFeedbackStatus_EmptyLedger(t *testing.T) {
	t.Parallel()
	ledgerPath := filepath.Join(t.TempDir(), "ledger.yaml")
	cmd := cli.NewFeedbackCommand(nil, nil, ledgerPath, "", func(string) ([]byte, error) { return nil, nil })
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"status"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 for an empty ledger", code)
	}
	if !strings.Contains(out.String(), "no feedback filed") {
		t.Fatalf("expected a friendly empty-ledger message, got %q", out.String())
	}
}

func TestFeedbackTriage_EmptyInboxIsClean(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hubRoot, "feedback", "inbox"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "inbox clean") {
		t.Fatalf("expected 'inbox clean', got %q", out.String())
	}
}

// TestFeedbackSubmit_FakeHostRoundTrip is this wave's own AC-1-adjacent CLI
// coverage: the verb dispatch drives feedback.Submitter end to end against
// host.NewFakeHost(), asserting the exact branch/file/title contract (§T1,
// §11 A6) and the idempotent no-second-push resubmit.
func TestFeedbackSubmit_FakeHostRoundTrip(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.1.0")
	ledgerPath := filepath.Join(t.TempDir(), "ledger.yaml")

	submitter := feedback.NewSubmitter(funnel, ledgerPath, t.TempDir(), "test-repo", feedback.SubmitConfig{
		RemoteURL:  fx.RemoteURL(),
		Repo:       host.Repo{Owner: "a2ahub", Name: "a2ahub"},
		BaseBranch: "main",
	})
	mirrorDir := fx.Clone("feedback")
	submitter.SetMirrorDirForTest(func(string, string) string { return mirrorDir })
	submitter.SetCloneOrFetchForTest(func(context.Context, string, string) error { return nil })

	cmd := cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", nil)

	raw := `feedback: v1
id: fb-20260723-cafe01
kind: friction
severity: minor
title: "inbox output floods small-context agents"
summary: "3 lines would be actionable, output is 4k tokens"
context:
  a2a_version: v0.1.1
  os_arch: darwin/arm64
  surface: cli
  command: a2a inbox
checks:
  docs_consulted: true
  grounded_in_real_work: true
  not_space_specific: true
  no_sensitive_content: true
  duplicates_checked: true
status: new
`
	path := filepath.Join(t.TempDir(), "fb-20260723-cafe01.yaml")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"submit", path}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "opened PR") {
		t.Fatalf("expected an 'opened PR' message, got %q", out.String())
	}

	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected exactly one PushBranch + OpenPR call, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
	wantBranch := "a2a/feedback/fb-20260723-cafe01"
	if fakeHost.Pushes[0].Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", fakeHost.Pushes[0].Branch, wantBranch)
	}
	wantTitle := "feedback(friction): inbox output floods small-context agents"
	if fakeHost.Opens[0].Title != wantTitle {
		t.Fatalf("PR title = %q, want %q", fakeHost.Opens[0].Title, wantTitle)
	}

	// One-file-payload assertion: the pushed commit's working tree carries
	// exactly feedback/inbox/<id>.yaml (nothing else) — walk the committed
	// tree at the pushed branch via the mirror's own git history.
	committedPath := filepath.Join(mirrorDir, "feedback", "inbox", "fb-20260723-cafe01.yaml")
	if _, err := os.Stat(committedPath); err != nil {
		t.Fatalf("expected the committed file at %s: %v", committedPath, err)
	}

	// Idempotent resubmit: no second push/open.
	io2, out2, _ := newIO()
	code2 := cmd.Run(context.Background(), []string{"submit", path}, io2)
	if code2 != 0 {
		t.Fatalf("retry code = %d, want 0; stdout=%s", code2, out2.String())
	}
	if !strings.Contains(out2.String(), "already submitted") {
		t.Fatalf("expected the retry to report already submitted, got %q", out2.String())
	}
	if len(fakeHost.Pushes) != 1 || len(fakeHost.Opens) != 1 {
		t.Fatalf("expected STILL exactly one push/open after the retry, got pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}
