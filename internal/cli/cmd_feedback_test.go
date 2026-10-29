package cli_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		space.NewWriteFunnel(fakeHost, nil, testBuild("0.13.0")),
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

// writeFeedbackInboxItem writes one feedback/inbox/<id>.yaml fixture. It is
// this file's own copy of internal/feedback/triage_test.go's unexported
// writeInboxItem helper — that package is outside this phase's allowlist
// (internal/cli/cmd_feedback.go, internal/cli/cmd_feedback_test.go only), so
// the CLI-level fixture cannot import the package-private one.
func writeFeedbackInboxItem(t *testing.T, hubRoot, id, status string) {
	t.Helper()
	dir := filepath.Join(hubRoot, "feedback", "inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "feedback: v1\n" +
		"id: " + id + "\n" +
		"kind: bug\n" +
		"severity: minor\n" +
		"title: \"triage --apply fixture\"\n" +
		"summary: \"summary\"\n" +
		"context:\n  a2a_version: v0.1.1\n  os_arch: darwin/arm64\n  surface: cli\n" +
		"checks:\n  docs_consulted: true\n  grounded_in_real_work: true\n  not_space_specific: true\n  no_sensitive_content: true\n  duplicates_checked: true\n" +
		"status: " + status + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write inbox item: %v", err)
	}
}

func writeFeedbackVerdictsFile(t *testing.T, dir string, verdicts ...feedback.Verdict) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("verdicts:\n")
	for _, v := range verdicts {
		fmt.Fprintf(&b, "  - id: %s\n    status: %s\n", v.ID, v.Status)
	}
	path := filepath.Join(dir, "verdicts.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write verdicts file: %v", err)
	}
	return path
}

// TestFeedbackTriageApply_AllRefusedExitsNonZero is P4 row 3 (spec 04
// §"three states"), reproduced live by the spec author: `--apply` used to
// exit 0 even when every named verdict was refused ("feedback triage:
// applied 0, skipped 1" -> exit 0). A single already-triaged (not status:
// new) target is a REFUSED verdict, not a typo, and is now rendered under
// its own "refused" label, distinct from "unknown-id".
func TestFeedbackTriageApply_AllRefusedExitsNonZero(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	writeFeedbackInboxItem(t, hubRoot, "fb-20260801-aaaaaa", "accepted") // not "new" -> refused
	verdictsPath := writeFeedbackVerdictsFile(t, t.TempDir(), feedback.Verdict{ID: "fb-20260801-aaaaaa", Status: "rejected"})

	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage", "--apply", verdictsPath}, io)
	if code == 0 {
		t.Fatalf("code = %d, want non-zero when every named verdict is refused", code)
	}
	if !strings.Contains(out.String(), "applied 0, refused 1, unknown-id 0, wip-limit-hit 0") {
		t.Fatalf("stdout = %q, want the counts line naming 0 applied, 1 refused", out.String())
	}
	if !strings.Contains(out.String(), "refused: fb-20260801-aaaaaa") {
		t.Fatalf("stdout = %q, want the refused id named, not only counted", out.String())
	}
	if strings.Contains(out.String(), "unknown-id:") {
		t.Fatalf("stdout = %q, must not print an unknown-id line when there are none", out.String())
	}
}

// TestFeedbackTriageApply_UnknownIDAlsoExitsNonZeroAndIsNamed covers the
// other half of row 3's "typo'd id" case: an id with no matching inbox
// record at all is a distinct outcome from the refused-not-new case
// (feedback.ApplyResult.UnknownID vs .Refused), rendered under its own
// "unknown-id" label — but it must still exit non-zero, for the reason
// recorded next to feedbackTriageExitObjections' use site in
// internal/cli/cmd_feedback.go: the check DID run for this id, it just
// found nothing to apply, which is the same "measured objection" class as
// a refused-not-new id, not the "check could not run at all" class.
func TestFeedbackTriageApply_UnknownIDAlsoExitsNonZeroAndIsNamed(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hubRoot, "feedback", "inbox"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	verdictsPath := writeFeedbackVerdictsFile(t, t.TempDir(), feedback.Verdict{ID: "fb-typo-does-not-exist", Status: "rejected"})

	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage", "--apply", verdictsPath}, io)
	if code == 0 {
		t.Fatalf("code = %d, want non-zero when the only named verdict is an unknown id", code)
	}
	if !strings.Contains(out.String(), "unknown-id: fb-typo-does-not-exist") {
		t.Fatalf("stdout = %q, want the unknown id named", out.String())
	}
	if strings.Contains(out.String(), "refused:") {
		t.Fatalf("stdout = %q, must not print a refused line when there are none", out.String())
	}
}

// TestFeedbackTriageApply_BothRefusedAndUnknownIDInOneRun asserts that when
// a single apply names both an already-triaged id and an unknown id, both
// are named under their own distinct label, in one non-zero-exit run — the
// case the split exists for (spec 04 §8 row 3, AGENTS.md anti-pattern row
// 22: two values cannot carry three states).
func TestFeedbackTriageApply_BothRefusedAndUnknownIDInOneRun(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	writeFeedbackInboxItem(t, hubRoot, "fb-20260801-aaaaaa", "accepted") // not "new" -> refused
	verdictsPath := writeFeedbackVerdictsFile(t, t.TempDir(),
		feedback.Verdict{ID: "fb-20260801-aaaaaa", Status: "rejected"},
		feedback.Verdict{ID: "fb-typo-does-not-exist", Status: "rejected"},
	)

	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage", "--apply", verdictsPath}, io)
	if code == 0 {
		t.Fatalf("code = %d, want non-zero when nothing was applied", code)
	}
	if !strings.Contains(out.String(), "refused: fb-20260801-aaaaaa") {
		t.Fatalf("stdout = %q, want the refused id named", out.String())
	}
	if !strings.Contains(out.String(), "unknown-id: fb-typo-does-not-exist") {
		t.Fatalf("stdout = %q, want the unknown id named", out.String())
	}
}

// TestFeedbackTriageApply_SomeAppliedExitsZeroWithRefusedNamed asserts that
// a mixed run — at least one verdict actually applied — still exits 0
// (this phase narrows non-zero to the ALL-refused/all-objection case, per
// row 3), while still naming the id that was refused alongside it.
func TestFeedbackTriageApply_SomeAppliedExitsZeroWithRefusedNamed(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	writeFeedbackInboxItem(t, hubRoot, "fb-20260801-bbbbbb", "new")
	writeFeedbackInboxItem(t, hubRoot, "fb-20260801-cccccc", "accepted") // refused
	verdictsPath := writeFeedbackVerdictsFile(t, t.TempDir(),
		feedback.Verdict{ID: "fb-20260801-bbbbbb", Status: "rejected"},
		feedback.Verdict{ID: "fb-20260801-cccccc", Status: "rejected"},
	)

	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	cmd.SetClockForTest(func() time.Time { return time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) })
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage", "--apply", verdictsPath}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 when at least one verdict applied", code)
	}
	if !strings.Contains(out.String(), "refused: fb-20260801-cccccc") {
		t.Fatalf("stdout = %q, want the refused id named even on a mixed run", out.String())
	}
}

// TestFeedbackTriageApply_WipLimitHitAlsoExitsNonZero: an all-wip-limit-hit
// run is the same class of objection as all-refused (a real verdict that
// was named and NOT applied), and previously exited 0 through the same
// gap — this phase's fix (len(Applied)==0 with a non-empty verdict input)
// covers it too rather than leaving a second silent-0 case behind.
func TestFeedbackTriageApply_WipLimitHitAlsoExitsNonZero(t *testing.T) {
	t.Parallel()
	hubRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(hubRoot, "feedback"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// wip_limit: 0 is not "closed" — ApplyVerdicts treats <= 0 as "unset"
	// and falls back to defaultWipLimit (triage.go: "if wipLimit <= 0 {
	// wipLimit = defaultWipLimit }"), so the limit must be reached with a
	// positive limit already at capacity, mirroring
	// internal/feedback/triage_test.go's own TestApplyVerdicts_WipLimitHit
	// fixture shape.
	backlog := "backlog: v1\nwip_limit: 1\nitems:\n  - id: fb-20260601-cccccc\n    kind: bug\n    severity: major\n    title: already at limit\n    verdict: accepted\n    route: backlog\n    date: \"2026-06-01\"\n"
	if err := os.WriteFile(filepath.Join(hubRoot, "feedback", "backlog.yaml"), []byte(backlog), 0o644); err != nil {
		t.Fatalf("write backlog.yaml: %v", err)
	}
	writeFeedbackInboxItem(t, hubRoot, "fb-20260801-dddddd", "new")
	verdictsPath := writeFeedbackVerdictsFile(t, t.TempDir(), feedback.Verdict{ID: "fb-20260801-dddddd", Status: "accepted", Route: "backlog"})

	cmd := cli.NewFeedbackCommand(nil, nil, "", hubRoot, nil)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"triage", "--apply", verdictsPath}, io)
	if code == 0 {
		t.Fatalf("code = %d, want non-zero when every named verdict is blocked at the wip limit", code)
	}
	if !strings.Contains(out.String(), "wip-limit-hit: fb-20260801-dddddd") {
		t.Fatalf("stdout = %q, want the wip-limit-hit id named", out.String())
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
	funnel := space.NewWriteFunnel(fakeHost, nil, testBuild("0.1.0"))
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
	funnel := space.NewWriteFunnel(fakeHost, nil, testBuild("0.13.0"))
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
		space.NewWriteFunnel(fakeHost, nil, testBuild("0.13.0")),
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

// inboxCmdRig is a feedback command over a hub fixture AND a local inbox, so
// one rig can take either route: --inbox (or SetInboxFallback) files locally,
// and a plain submit reaches the fake host.
type inboxCmdRig struct {
	cmd         *cli.FeedbackCommand
	fakeHost    *host.FakeHost
	ledgerPath  string
	inboxDir    string
	feedbackDir string
}

func newInboxCmdRig(t *testing.T, hubReader feedback.HubReader) inboxCmdRig {
	t.Helper()
	fx := spacefixture.New(t, "feedback")
	fakeHost := host.NewFakeHost()
	projectRoot := t.TempDir()
	feedbackDir := filepath.Join(projectRoot, ".a2a", "feedback")
	if err := os.MkdirAll(feedbackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(feedbackDir, "ledger.yaml")
	inboxDir := filepath.Join(t.TempDir(), "data", "feedback", "inbox")
	submitter := feedback.NewSubmitter(space.NewWriteFunnel(fakeHost, nil, testBuild("0.13.0")), ledgerPath, projectRoot, "test-repo", feedback.SubmitConfig{
		RemoteURL: fx.RemoteURL(), Repo: host.Repo{Owner: "a2ahub", Name: "a2ahub"}, BaseBranch: "main", InboxDir: inboxDir,
	})
	submitter.SetMirrorDirForTest(func(string, string) string { return fx.Clone("feedback") })
	submitter.SetCloneOrFetchForTest(func(context.Context, string, string, host.Credential) error { return nil })
	return inboxCmdRig{
		cmd:      cli.NewFeedbackCommand(nil, submitter, ledgerPath, "", hubReader),
		fakeHost: fakeHost, ledgerPath: ledgerPath, inboxDir: inboxDir, feedbackDir: feedbackDir,
	}
}

func (r inboxCmdRig) draft(t *testing.T, id, title string) string {
	t.Helper()
	path := filepath.Join(r.feedbackDir, id+".yaml")
	if err := os.WriteFile(path, []byte(feedbackDraftYAML(id, title)), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func (r inboxCmdRig) run(args ...string) (code int, stdout, stderr string) {
	io, out, errOut := newIO()
	code = r.cmd.Run(context.Background(), args, io)
	return code, out.String(), errOut.String()
}

// TestFeedbackSubmit_InboxFlagFilesLocallyAndSaysSo pins §T1's result lines:
// --inbox files to the local inbox with reason `--inbox`, reaches no host, and
// a re-run says the report is already there, both exiting 0.
func TestFeedbackSubmit_InboxFlagFilesLocallyAndSaysSo(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "an offline report on purpose")
	filed := filepath.Join(rig.inboxDir, id+".yaml")

	code, stdout, stderr := rig.run("submit", "--inbox", draft)
	want := "feedback submit: filed " + id + " to the local inbox " + filed + " (--inbox); deliver it later with a2a feedback submit --all once a GitHub credential is set\n"
	if code != 0 || stdout != want {
		t.Fatalf("code=%d stdout=%q stderr=%q, want exit 0 and %q", code, stdout, stderr, want)
	}
	if len(rig.fakeHost.Pushes) != 0 || len(rig.fakeHost.Opens) != 0 {
		t.Fatalf("--inbox reached the host: pushes=%d opens=%d", len(rig.fakeHost.Pushes), len(rig.fakeHost.Opens))
	}

	code, stdout, stderr = rig.run("submit", draft, "--inbox")
	want = "feedback submit: " + id + " is already in the local inbox " + filed + "\n"
	if code != 0 || stdout != want {
		t.Fatalf("re-run code=%d stdout=%q stderr=%q, want exit 0 and %q", code, stdout, stderr, want)
	}
}

// TestFeedbackSubmit_InboxFallbackReasonIsTheWiringsReason: with the fallback
// the wiring detected (no credential) and no flag, the route is the inbox and
// the line names that reason; with both, the flag's reason wins (§T1.1 row 2
// precedes row 3).
func TestFeedbackSubmit_InboxFallbackReasonIsTheWiringsReason(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	rig.cmd.SetInboxFallback("no GitHub credential")
	draft := rig.draft(t, "fb-20260728-aaa111", "an offline report by necessity")

	code, stdout, stderr := rig.run("submit", draft)
	if code != 0 || !strings.Contains(stdout, "(no GitHub credential); deliver it later") {
		t.Fatalf("code=%d stdout=%q stderr=%q, want a filing with reason `no GitHub credential`", code, stdout, stderr)
	}
	other := rig.draft(t, "fb-20260728-bbb222", "an offline report that also asks for it")
	code, stdout, stderr = rig.run("submit", "--inbox", other)
	if code != 0 || !strings.Contains(stdout, "(--inbox); deliver it later") {
		t.Fatalf("code=%d stdout=%q stderr=%q, want reason `--inbox` to win", code, stdout, stderr)
	}
	if len(rig.fakeHost.Opens) != 0 {
		t.Fatalf("the inbox route reached the host: opens=%d", len(rig.fakeHost.Opens))
	}
}

// TestFeedbackSubmit_InboxJSONRowCarriesInbox: the --json row gains `inbox` on
// the inbox route; a hub-route row encodes with no such key.
func TestFeedbackSubmit_InboxJSONRowCarriesInbox(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	local := rig.draft(t, "fb-20260728-aaa111", "an offline report in json")
	code, stdout, stderr := rig.run("submit", "--inbox", "--json", local)
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(stdout), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("--json output %q is not one row: %v", stdout, err)
	}
	if got := rows[0]["inbox"]; got != filepath.Join(rig.inboxDir, "fb-20260728-aaa111.yaml") {
		t.Fatalf("row inbox = %v, want the filed path; row=%v", got, rows[0])
	}

	hub := rig.draft(t, "fb-20260728-bbb222", "a hub report in json")
	code, stdout, stderr = rig.run("submit", "--json", hub)
	if code != 0 {
		t.Fatalf("hub route code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, `"inbox"`) {
		t.Fatalf("a hub-route --json row must carry no inbox key, got %s", stdout)
	}
}

// TestFeedbackSubmit_InboxBatchRefusedFilesNothing is spec 08 AC12: on the
// inbox route, one invalid draft files nothing; every result carries
// `validation` or `batch-validation-refused`, and the inbox stays empty.
func TestFeedbackSubmit_InboxBatchRefusedFilesNothing(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	valid := rig.draft(t, "fb-20260728-aaa111", "valid but batch-refused")
	invalid := filepath.Join(rig.feedbackDir, "fb-20260728-bbb222.yaml")
	if err := os.WriteFile(invalid, []byte("feedback: v1\nkind: bug\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := rig.run("submit", "--inbox", "--json", valid, invalid)
	if code != 1 {
		t.Fatalf("code=%d, want 1; stdout=%s stderr=%s", code, stdout, stderr)
	}
	var results []feedback.SubmitResult
	if err := json.Unmarshal([]byte(stdout), &results); err != nil || len(results) != 2 {
		t.Fatalf("results = %q, %v, want two rows", stdout, err)
	}
	if results[0].ErrorCode != feedback.ErrorCodeBatchRefused || results[1].ErrorCode != feedback.ErrorCodeValidation {
		t.Fatalf("results = %+v, want batch-validation-refused then validation", results)
	}
	if entries, err := os.ReadDir(rig.inboxDir); !os.IsNotExist(err) && len(entries) != 0 {
		t.Fatalf("inbox holds %v after a refused batch, want nothing (err=%v)", entries, err)
	}
	if items, err := feedback.ReadLedger(rig.ledgerPath); err != nil || len(items) != 0 {
		t.Fatalf("ledger = %+v, %v, want no rows", items, err)
	}
}

// TestFeedbackSubmit_AllDeliversTheInboxCopyOnce is the in-process half of spec
// 08 AC10: after an inbox filing and an edit of the draft, `submit --all` on
// the hub route opens one PR from the inbox copy, the ledger row gains pr_url
// and keeps inbox, and a second --all selects nothing.
func TestFeedbackSubmit_AllDeliversTheInboxCopyOnce(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "filed offline then delivered")
	if code, out, errOut := rig.run("submit", "--inbox", draft); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if err := os.WriteFile(draft, []byte(feedbackDraftYAML(id, "an edit made after filing")), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := rig.run("submit", "--all")
	if code != 0 {
		t.Fatalf("--all code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if len(rig.fakeHost.Opens) != 1 || !strings.HasSuffix(rig.fakeHost.Opens[0].Title, "filed offline then delivered") {
		t.Fatalf("opens = %+v, want one PR titled from the INBOX copy, not the edited draft", rig.fakeHost.Opens)
	}
	item, err := feedback.FindLedgerItem(rig.ledgerPath, id)
	if err != nil || item == nil || item.PRURL == "" || item.Inbox != filepath.Join(rig.inboxDir, id+".yaml") {
		t.Fatalf("ledger row = %+v, %v, want pr_url set and the inbox path kept", item, err)
	}

	code, stdout, _ = rig.run("submit", "--all")
	if code != 0 || !strings.Contains(stdout, "no unsubmitted drafts") || len(rig.fakeHost.Opens) != 1 {
		t.Fatalf("second --all code=%d stdout=%q opens=%d, want a no-op", code, stdout, len(rig.fakeHost.Opens))
	}
}

// TestFeedbackSubmit_AllOnTheInboxRouteRefilesIdempotently: without a
// credential, --all re-files the same inbox bytes and reports them as already
// filed, exit 0 (§T1.3 "Delivery idempotency").
func TestFeedbackSubmit_AllOnTheInboxRouteRefilesIdempotently(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	rig.cmd.SetInboxFallback("no GitHub credential")
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "filed offline and still offline")
	if code, out, errOut := rig.run("submit", draft); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	code, stdout, stderr := rig.run("submit", "--all")
	if code != 0 || !strings.Contains(stdout, id+" is already in the local inbox") {
		t.Fatalf("--all code=%d stdout=%q stderr=%q, want exit 0 and `already in the local inbox`", code, stdout, stderr)
	}
}

// TestFeedbackSubmit_AllReportsAMissingInboxCopy is spec 08 AC11: a ledger row
// filed to the inbox whose copy has been deleted is reported by its path, exits
// 1 and opens no PR, and the draft is NOT silently used instead.
func TestFeedbackSubmit_AllReportsAMissingInboxCopy(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "filed offline then its copy was deleted")
	if code, out, errOut := rig.run("submit", "--inbox", draft); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	copyPath := filepath.Join(rig.inboxDir, id+".yaml")
	if err := os.Remove(copyPath); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := rig.run("submit", "--all")
	if code != 1 || !strings.Contains(stderr, "inbox copy "+copyPath+" is gone; submit the draft explicitly") {
		t.Fatalf("--all code=%d stdout=%q stderr=%q, want exit 1 naming `inbox copy %s is gone`", code, stdout, stderr, copyPath)
	}
	if len(rig.fakeHost.Pushes) != 0 || len(rig.fakeHost.Opens) != 0 {
		t.Fatalf("a missing inbox copy fell back to the draft: pushes=%d opens=%d", len(rig.fakeHost.Pushes), len(rig.fakeHost.Opens))
	}
}

// TestFeedbackSubmit_AllSelectsAnUndeliveredRowWithoutAnInboxCopy: a row with
// no pr_url and no inbox path (not written by any release) is undelivered, so
// its draft is selected like an unledgered one.
func TestFeedbackSubmit_AllSelectsAnUndeliveredRowWithoutAnInboxCopy(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	rig.draft(t, id, "a row without a pr and without an inbox")
	if err := feedback.AppendLedger(rig.ledgerPath, feedback.LedgerItem{ID: id, Kind: "bug", Title: "x", Filed: "2026-10-28T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := rig.run("submit", "--all"); code != 0 || len(rig.fakeHost.Opens) != 1 {
		t.Fatalf("--all code=%d opens=%d stdout=%s stderr=%s, want the draft submitted once", code, len(rig.fakeHost.Opens), out, errOut)
	}
}

// TestFeedbackStatus_LocalInboxRowPrintsDashAndSkipsTheHub is the CLI half of
// spec 08 AC9: an undelivered inbox row prints `-` in the PR column and
// `local-inbox` as the hub status, and the hub reader is never called for it.
func TestFeedbackStatus_LocalInboxRowPrintsDashAndSkipsTheHub(t *testing.T) {
	t.Parallel()
	const id = "fb-20260728-aaa111"
	rig := newInboxCmdRig(t, func(got string) ([]byte, error) {
		t.Errorf("the hub reader was called for %s", got)
		return nil, nil
	})
	if err := feedback.AppendLedger(rig.ledgerPath, feedback.LedgerItem{ID: id, Kind: "bug", Title: "offline", Inbox: "/data/inbox/" + id + ".yaml", Filed: "2026-10-28T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := rig.run("status")
	want := id + "\tbug\toffline\t-\tlocal-inbox\t-\n"
	if code != 0 || stdout != want {
		t.Fatalf("status code=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, want)
	}
	code, stdout, _ = rig.run("status", "--json")
	if code != 0 || !strings.Contains(stdout, `"Inbox": "/data/inbox/`+id+`.yaml"`) {
		t.Fatalf("status --json code=%d stdout=%s, want the Inbox key", code, stdout)
	}
}

// TestFeedbackSynopsis_NamesTheInboxFlag is spec 08 AC15's in-process half: the
// catalog row's synopsis (reference/commands.md is generated from it) names
// the new flag.
func TestFeedbackSynopsis_NamesTheInboxFlag(t *testing.T) {
	t.Parallel()
	got := cli.NewFeedbackCommand(nil, nil, "", "", nil).Synopsis()
	if !strings.Contains(got, "submit <file...> [--all] [--inbox]") {
		t.Fatalf("Synopsis() = %q, want it to contain %q", got, "submit <file...> [--all] [--inbox]")
	}
}

// TestFeedbackSubmit_ExplicitPathOfAFiledIDRefusesAnEditedDraft is spec 08 §11
// "Delivery sends the filed bytes" and US-6 on the hub route: after an inbox
// filing and an edit of the draft, an EXPLICIT `submit <draft>` with a
// credential is refused as inbox-conflict naming both paths, pushes nothing and
// leaves the ledger row undelivered; re-running it changes nothing; and the
// filed bytes are still delivered by --all.
func TestFeedbackSubmit_ExplicitPathOfAFiledIDRefusesAnEditedDraft(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "filed offline then edited")
	if code, out, errOut := rig.run("submit", "--inbox", draft); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	filed := filepath.Join(rig.inboxDir, id+".yaml")
	if err := os.WriteFile(draft, []byte(feedbackDraftYAML(id, "an edit made after filing")), 0o644); err != nil {
		t.Fatal(err)
	}

	for run := 1; run <= 2; run++ {
		code, stdout, stderr := rig.run("submit", draft)
		if code != 1 || !strings.Contains(stderr, "(inbox-conflict)") || !strings.Contains(stderr, filed) || !strings.Contains(stderr, draft) {
			t.Fatalf("run %d: code=%d stdout=%q stderr=%q, want exit 1 and an inbox-conflict naming %s and %s", run, code, stdout, stderr, filed, draft)
		}
		if len(rig.fakeHost.Pushes) != 0 || len(rig.fakeHost.Opens) != 0 {
			t.Fatalf("run %d: the edited draft reached the host: pushes=%d opens=%d", run, len(rig.fakeHost.Pushes), len(rig.fakeHost.Opens))
		}
		item, err := feedback.FindLedgerItem(rig.ledgerPath, id)
		if err != nil || item == nil || item.PRURL != "" || item.Inbox != filed {
			t.Fatalf("run %d: ledger row = %+v, %v, want it undelivered and still pointing at %s", run, item, err, filed)
		}
	}

	code, stdout, stderr := rig.run("submit", "--json", draft)
	var rows []feedback.SubmitResult
	if code != 1 || json.Unmarshal([]byte(stdout), &rows) != nil || len(rows) != 1 || rows[0].ErrorCode != feedback.ErrorCodeInboxConflict || rows[0].Inbox != filed {
		t.Fatalf("--json code=%d stdout=%s stderr=%s, want one inbox-conflict row carrying inbox %s", code, stdout, stderr, filed)
	}

	if code, out, errOut := rig.run("submit", "--all"); code != 0 {
		t.Fatalf("--all code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if len(rig.fakeHost.Opens) != 1 || !strings.HasSuffix(rig.fakeHost.Opens[0].Title, "filed offline then edited") {
		t.Fatalf("opens = %+v, want one PR titled from the filed copy", rig.fakeHost.Opens)
	}
}

// TestFeedbackSubmit_ExplicitPathOfAFiledIDDeliversTheFiledBytesOnce: an
// unedited draft of a filed id IS the filed record, so the explicit path
// delivers it as one PR, the ledger row gains pr_url and keeps inbox, and a
// second explicit submit is the idempotent "already submitted".
func TestFeedbackSubmit_ExplicitPathOfAFiledIDDeliversTheFiledBytesOnce(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "filed offline then delivered by path")
	if code, out, errOut := rig.run("submit", "--inbox", draft); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}

	code, stdout, stderr := rig.run("submit", draft)
	if code != 0 || len(rig.fakeHost.Opens) != 1 || !strings.HasSuffix(rig.fakeHost.Opens[0].Title, "filed offline then delivered by path") {
		t.Fatalf("code=%d opens=%+v stdout=%s stderr=%s, want one PR", code, rig.fakeHost.Opens, stdout, stderr)
	}
	item, err := feedback.FindLedgerItem(rig.ledgerPath, id)
	if err != nil || item == nil || item.PRURL == "" || item.Inbox != filepath.Join(rig.inboxDir, id+".yaml") {
		t.Fatalf("ledger row = %+v, %v, want pr_url set and the inbox path kept", item, err)
	}

	code, stdout, stderr = rig.run("submit", draft)
	if code != 0 || !strings.Contains(stdout, "already submitted") || len(rig.fakeHost.Opens) != 1 {
		t.Fatalf("second submit code=%d stdout=%q stderr=%q opens=%d, want the idempotent no-op", code, stdout, stderr, len(rig.fakeHost.Opens))
	}
}

// TestFeedbackSubmit_BatchWithAnInboxConflictOpensNothing: the conflict is part
// of the batch pre-check, so a batch holding one conflicting draft opens no PR
// for the valid one beside it.
func TestFeedbackSubmit_BatchWithAnInboxConflictOpensNothing(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	conflicting := rig.draft(t, id, "filed offline then edited")
	if code, out, errOut := rig.run("submit", "--inbox", conflicting); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if err := os.WriteFile(conflicting, []byte(feedbackDraftYAML(id, "an edit made after filing")), 0o644); err != nil {
		t.Fatal(err)
	}
	valid := rig.draft(t, "fb-20260728-bbb222", "a fresh report beside it")

	code, stdout, stderr := rig.run("submit", "--json", conflicting, valid)
	var rows []feedback.SubmitResult
	if code != 1 || json.Unmarshal([]byte(stdout), &rows) != nil || len(rows) != 2 {
		t.Fatalf("code=%d stdout=%s stderr=%s, want exit 1 and two rows", code, stdout, stderr)
	}
	if rows[0].ErrorCode != feedback.ErrorCodeInboxConflict || rows[1].ErrorCode != feedback.ErrorCodeBatchRefused {
		t.Fatalf("rows = %+v, want inbox-conflict then batch-validation-refused", rows)
	}
	if len(rig.fakeHost.Pushes) != 0 || len(rig.fakeHost.Opens) != 0 {
		t.Fatalf("a batch with a conflict reached the host: pushes=%d opens=%d", len(rig.fakeHost.Pushes), len(rig.fakeHost.Opens))
	}
}

// TestFeedbackSubmit_ExplicitPathDeliversTheDraftWhenTheInboxCopyIsGone: --all
// refuses a row whose inbox copy is gone and says "submit the draft
// explicitly"; that remedy must work.
func TestFeedbackSubmit_ExplicitPathDeliversTheDraftWhenTheInboxCopyIsGone(t *testing.T) {
	t.Parallel()
	rig := newInboxCmdRig(t, nil)
	const id = "fb-20260728-aaa111"
	draft := rig.draft(t, id, "filed offline then its copy was deleted")
	if code, out, errOut := rig.run("submit", "--inbox", draft); code != 0 {
		t.Fatalf("inbox filing code=%d stdout=%s stderr=%s", code, out, errOut)
	}
	if err := os.Remove(filepath.Join(rig.inboxDir, id+".yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte(feedbackDraftYAML(id, "the draft is now the only copy")), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := rig.run("submit", draft)
	if code != 0 || len(rig.fakeHost.Opens) != 1 || !strings.HasSuffix(rig.fakeHost.Opens[0].Title, "the draft is now the only copy") {
		t.Fatalf("code=%d opens=%+v stdout=%s stderr=%s, want the draft delivered", code, rig.fakeHost.Opens, stdout, stderr)
	}
}
