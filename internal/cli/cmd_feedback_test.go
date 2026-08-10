package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func feedbackDraftYAML(id, title string) string {
	return fmt.Sprintf(`feedback: v1
id: %s
kind: bug
severity: major
title: %q
summary: "a grounded batch-submission report"
context:
  a2a_version: v0.13.0
  os_arch: darwin/arm64
  surface: cli
evidence:
  steps:
    - "run the affected command"
  expected: "the documented result"
  actual: "the observed conflicting result"
checks:
  docs_consulted: true
  grounded_in_real_work: true
  not_space_specific: true
  no_sensitive_content: true
  duplicates_checked: true
status: new
`, id, title)
}

func TestFeedbackSubmit_JSONValidationBatchIsCompleteAndDeterministic(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	projectRoot := t.TempDir()
	ledgerPath := filepath.Join(projectRoot, ".a2a", "feedback", "ledger.yaml")
	submitter := feedback.NewSubmitter(
		space.NewWriteFunnel(fakeHost, nil, "0.13.0"),
		ledgerPath, projectRoot, "test-repo",
		feedback.SubmitConfig{RemoteURL: fx.RemoteURL(), Repo: host.Repo{Owner: "a2ahub", Name: "a2ahub"}, BaseBranch: "main"},
	)
	submitter.SetMirrorDirForTest(func(string, string) string { return fx.Clone("feedback") })
	submitter.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	cmd := cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", nil)

	first := filepath.Join(t.TempDir(), "a-valid.yaml")
	second := filepath.Join(t.TempDir(), "z-invalid.yaml")
	if err := os.WriteFile(first, []byte(feedbackDraftYAML("fb-20260728-ddd444", "valid but batch-refused")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("feedback: v1\nkind: bug\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", second, first, "--json"}, io); code != 1 {
		t.Fatalf("code=%d, want validation refusal; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var results []feedback.SubmitResult
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("JSON output is not parseable: %v\n%s", err, out.String())
	}
	if len(results) != 2 || results[0].InputPath != first || results[1].InputPath != second {
		t.Fatalf("results are not complete and bytewise path-sorted: %+v", results)
	}
	if results[0].ErrorCode != feedback.ErrorCodeBatchRefused || results[1].ErrorCode != feedback.ErrorCodeValidation {
		t.Fatalf("validation outcomes = %+v", results)
	}
	if errOut.Len() != 0 || len(fakeHost.Pushes) != 0 || len(fakeHost.Opens) != 0 {
		t.Fatalf("JSON validation refusal leaked prose or wrote externally: stderr=%q pushes=%d opens=%d", errOut.String(), len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}

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

// TestFeedbackValidate_FlagAfterPositional is Wave K's live-run 6 defect
// applied to `a2a feedback validate`: `validate <file> --ci` used to leave
// --ci unset (Go's flag package stops parsing at the first non-flag
// token), so it validated with the LESS strict, non-CI ruleset than the
// caller asked for — and a report that would fail the CI-only guards
// silently reported 0/"valid" instead of catching it.
//
// TEETH: reverting FeedbackCommand.runValidate's parseArgsAnyOrder call
// (cmd_feedback.go) back to a bare `fs.Parse(args)` reds this: the report
// below fails ONLY the CI-mode filename guard, so it passes as 0/valid
// again once --ci silently stops being recognized.
func TestFeedbackValidate_FlagAfterPositional(t *testing.T) {
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
	// CI mode's own filename guard (§T1) requires the file's basename to
	// match its own `id:` field — this path deliberately does NOT, so
	// --ci recognized is the ONLY thing standing between "valid" and a
	// refusal.
	path := filepath.Join(t.TempDir(), "mismatched-name.yaml")
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"validate", path, "--ci"}, io)
	if code != 1 {
		t.Fatalf("feedback validate <file> --ci: code = %d, want 1 (CI filename guard must fire); stdout=%s stderr=%s", code, out.String(), errOut.String())
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
	cmd.SetFreshnessResolver(func() feedback.Freshness {
		return feedback.Freshness{Status: feedback.FreshnessNotApplicable}
	})
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "inbox clean") {
		t.Fatalf("expected 'inbox clean', got %q", out.String())
	}
}

// TestFeedbackTriage_RefusedFreshnessNamesReasonAndHub is AC3's refusal
// wiring proven at the CLI seam: runTriage must refuse — naming the reason
// AND the hub of record, and never printing "inbox clean" — when the
// injected freshness resolver reports FreshnessRefused, even though the
// inbox itself is empty and would otherwise be clean (own-loop 09 §8 AC3,
// §11 wave D2).
func TestFeedbackTriage_RefusedFreshnessNamesReasonAndHub(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hubRoot, "feedback", "inbox"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	cmd.SetFreshnessResolver(func() feedback.Freshness {
		return feedback.Freshness{
			Status:      feedback.FreshnessRefused,
			Reason:      "hub's default branch carries fb-20260808-999999, which this tree lacks",
			HubOfRecord: "https://github.com/ydnikolaev/a2ahub",
		}
	})
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"triage"}, io)
	if code == 0 {
		t.Fatalf("code = %d, want non-zero on a refused freshness verdict", code)
	}
	if strings.Contains(out.String(), "inbox clean") {
		t.Fatalf("stdout = %q, must never print 'inbox clean' on a refused verdict", out.String())
	}
	if !strings.Contains(errOut.String(), "fb-20260808-999999") {
		t.Errorf("stderr = %q, want it to name the drift reason", errOut.String())
	}
	if !strings.Contains(errOut.String(), "https://github.com/ydnikolaev/a2ahub") {
		t.Errorf("stderr = %q, want it to name the hub of record", errOut.String())
	}
}

// TestFeedbackTriage_NilResolverRefuses is the "nobody wired a resolver"
// case: a nil resolveFreshness must fail closed (refuse), never read as
// "confirmed current" by omission.
func TestFeedbackTriage_NilResolverRefuses(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hubRoot, "feedback", "inbox"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"triage"}, io)
	if code == 0 {
		t.Fatalf("code = %d, want non-zero for a nil freshness resolver", code)
	}
	if strings.Contains(out.String(), "inbox clean") {
		t.Fatalf("stdout = %q, must never print 'inbox clean' with no resolver wired", out.String())
	}
	if !strings.Contains(errOut.String(), "no freshness resolver was configured") {
		t.Errorf("stderr = %q, want it to say no freshness resolver was configured", errOut.String())
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
	submitter.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })

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
	wantBranch := "a2a/feedback/submit/fb-20260723-cafe01"
	if fakeHost.Pushes[0].Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", fakeHost.Pushes[0].Branch, wantBranch)
	}
	wantTitle := "feedback(friction): inbox output floods small-context agents"
	if fakeHost.Opens[0].Title != wantTitle {
		t.Fatalf("PR title = %q, want %q", fakeHost.Opens[0].Title, wantTitle)
	}

	// One-file-payload assertion: the pushed COMMIT carries exactly
	// feedback/inbox/<id>.yaml.
	//
	// This is what the comment here always claimed — "walk the committed
	// tree at the pushed branch" — while the code underneath it stat'ed the
	// working tree, and passed only because the funnel used to leave the
	// mirror parked on the write branch. Now that a write restores the
	// mirror to its base (see space.WriteFunnel's restoreTreeToBase), the
	// assertion has to read the commit it names.
	committedPath := "feedback/inbox/fb-20260723-cafe01.yaml"
	changed := strings.Fields(gitOutputForTest(t, mirrorDir, "show", "--name-only", "--pretty=format:", wantBranch))
	if len(changed) != 1 || changed[0] != committedPath {
		t.Fatalf("the pushed commit changed %v, want exactly [%s]", changed, committedPath)
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

func TestFeedbackSubmit_MultipleFilesAndAllKeepOnePRPerItem(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	funnel := space.NewWriteFunnel(fakeHost, nil, "0.13.0")
	projectRoot := t.TempDir()
	feedbackDir := filepath.Join(projectRoot, ".a2a", "feedback")
	if err := os.MkdirAll(feedbackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(feedbackDir, "ledger.yaml")
	submitter := feedback.NewSubmitter(funnel, ledgerPath, projectRoot, "test-repo", feedback.SubmitConfig{
		RemoteURL: fx.RemoteURL(), Repo: host.Repo{Owner: "a2ahub", Name: "a2ahub"}, BaseBranch: "main",
	})
	submitter.SetMirrorDirForTest(func(string, string) string { return fx.Clone("feedback") })
	submitter.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	cmd := cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", nil)

	first := filepath.Join(feedbackDir, "fb-20260728-aaa111.yaml")
	second := filepath.Join(feedbackDir, "fb-20260728-bbb222.yaml")
	for path, raw := range map[string]string{
		first:  feedbackDraftYAML("fb-20260728-aaa111", "first independent batch item"),
		second: feedbackDraftYAML("fb-20260728-bbb222", "second independent batch item"),
	} {
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", first, second}, io); code != 0 {
		t.Fatalf("multi submit code=%d stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(fakeHost.Opens) != 2 || len(fakeHost.Pushes) != 2 {
		t.Fatalf("opens=%d pushes=%d, want two independent PRs", len(fakeHost.Opens), len(fakeHost.Pushes))
	}

	third := filepath.Join(feedbackDir, "fb-20260728-ccc333.yaml")
	if err := os.WriteFile(third, []byte(feedbackDraftYAML("fb-20260728-ccc333", "third unledgered item")), 0o644); err != nil {
		t.Fatal(err)
	}
	allIO, _, allErr := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", "--all"}, allIO); code != 0 {
		t.Fatalf("--all code=%d stderr=%s", code, allErr.String())
	}
	if len(fakeHost.Opens) != 3 {
		t.Fatalf("--all reopened ledgered items: opens=%d, want 3 total", len(fakeHost.Opens))
	}

	// Once external writes begin there is no transaction to roll back. A
	// failed item must make the invocation red while later independent items
	// still get their own result/PR.
	fourth := filepath.Join(feedbackDir, "fb-20260728-ddd444.yaml")
	fifth := filepath.Join(feedbackDir, "fb-20260728-eee555.yaml")
	if err := os.WriteFile(fourth, []byte(feedbackDraftYAML("fb-20260728-ddd444", "the simulated failed item")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fifth, []byte(feedbackDraftYAML("fb-20260728-eee555", "the later successful item")), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeHost.PushBranchFunc = func(_ context.Context, req host.PushBranchRequest) (host.PushBranchResult, error) {
		if strings.Contains(req.Branch, "ddd444") {
			return host.PushBranchResult{}, fmt.Errorf("%w: simulated push failure", host.ErrPushRejected)
		}
		return host.PushBranchResult{Branch: req.Branch}, nil
	}
	partialIO, partialOut, partialErr := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", fourth, fifth}, partialIO); code != 1 {
		t.Fatalf("partial batch code=%d, want 1; stdout=%s stderr=%s", code, partialOut.String(), partialErr.String())
	}
	if len(fakeHost.Opens) != 4 || !strings.Contains(partialOut.String(), "eee555") {
		t.Fatalf("later item did not continue after failure: opens=%d stdout=%s stderr=%s", len(fakeHost.Opens), partialOut.String(), partialErr.String())
	}
}

func TestFeedbackSubmit_ValidatesWholeBatchBeforeFirstWrite(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	projectRoot := t.TempDir()
	ledgerPath := filepath.Join(projectRoot, ".a2a", "feedback", "ledger.yaml")
	submitter := feedback.NewSubmitter(
		space.NewWriteFunnel(fakeHost, nil, "0.13.0"),
		ledgerPath, projectRoot, "test-repo",
		feedback.SubmitConfig{RemoteURL: fx.RemoteURL(), Repo: host.Repo{Owner: "a2ahub", Name: "a2ahub"}, BaseBranch: "main"},
	)
	submitter.SetMirrorDirForTest(func(string, string) string { return fx.Clone("feedback") })
	submitter.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	cmd := cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", nil)

	valid := filepath.Join(t.TempDir(), "fb-20260728-ddd444.yaml")
	invalid := filepath.Join(t.TempDir(), "fb-20260728-eee555.yaml")
	if err := os.WriteFile(valid, []byte(feedbackDraftYAML("fb-20260728-ddd444", "valid item must not write early")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte("feedback: v1\nkind: bug\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"submit", valid, invalid}, io); code != 1 {
		t.Fatalf("code=%d, want validation refusal", code)
	}
	if len(fakeHost.Pushes) != 0 || len(fakeHost.Opens) != 0 {
		t.Fatalf("invalid batch wrote before full validation: pushes=%d opens=%d", len(fakeHost.Pushes), len(fakeHost.Opens))
	}
}
