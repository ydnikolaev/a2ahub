package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The local feedback inbox, through the BUILT BINARY (local-space-2026-10 spec
// 08, D-039). This file imports no internal package: it reads the binary's
// own output, and restates the one external contract it needs (the inbox
// path under HOME) the way host_loop_test.go restates the hub branch name.
//
// AC10 is driven through the binary and a throwaway local hub rather than the
// spec's direct construction (space.WriteFunnel over host.NewFakeHost()):
// internal/e2e must not gain an internal import (the cross-layer ratchet), and
// the binary path exercises cmd/a2a's route selection, which direct
// construction skips.

// feedbackInboxRelative is where a machine's local inbox sits under HOME.
var feedbackInboxRelative = filepath.Join(".local", "share", "a2a", "feedback", "inbox")

// feedbackInboxEnv is the process environment with every variable that could
// give `a2a feedback` a credential, a hub or an API root removed, plus the
// given KEY=VALUE overrides. A test that wants no credential must not inherit
// the developer's.
func feedbackInboxEnv(extra ...string) []string {
	drop := map[string]bool{
		"A2A_FEEDBACK_TOKEN": true, "GITHUB_TOKEN": true, "GH_TOKEN": true,
		"A2A_FEEDBACK_REPO": true, "A2A_GITHUB_API": true, "A2A_FEEDBACK_HUB_URL": true,
		"HOME": true,
	}
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if !drop[name] {
			env = append(env, kv)
		}
	}
	return append(env, extra...)
}

// loggedOutGHFirstOnPath returns a PATH whose first entry holds a `gh` that is
// logged out (every call exits 1), so the credential probe finds nothing no
// matter what the developer's machine has.
func loggedOutGHFirstOnPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "gh"), "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(dir, "gh"), 0o755); err != nil {
		t.Fatalf("chmod fake gh: %v", err)
	}
	return "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// runFeedbackInbox execs the built binary's `feedback` family in dir.
func runFeedbackInbox(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "a2a"), append([]string{"feedback"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		exitErr := &exec.ExitError{}
		if !errors.As(err, &exitErr) {
			t.Fatalf("exec a2a feedback %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errBuf.String(), code
}

func assertAbsent(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists (stat err = %v): %s", path, err, why)
	}
}

// TestFeedbackInboxFiledWithoutCredential is spec 08 AC1 and AC2. With the
// canonical GitHub target, no token variable and a logged-out gh, submit exits
// 0, says it filed to the local inbox, and writes the normalized draft under
// HOME with mode 0600 and no mirror clone. The copy-is-triageable subtest then
// does what the runbook says: copy the file into a hub tree that is not a git
// work tree, and validate and triage it there.
func TestFeedbackInboxFiledWithoutCredential(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	project := t.TempDir()
	draft := writeFeedbackDraft(t, filepath.Join(project, ".a2a", "feedback"))
	env := feedbackInboxEnv("HOME="+home, loggedOutGHFirstOnPath(t))

	stdout, stderr, code := runFeedbackInbox(t, project, env, "submit", draft)
	if code != 0 {
		t.Fatalf("submit: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	filed := filepath.Join(home, feedbackInboxRelative, feedbackSubmitTestID+".yaml")
	if !strings.Contains(stdout, "filed "+feedbackSubmitTestID+" to the local inbox "+filed+" (no GitHub credential)") {
		t.Fatalf("stdout = %q, want it to say it filed %s to the local inbox %s (no GitHub credential)", stdout, feedbackSubmitTestID, filed)
	}
	info, err := os.Stat(filed)
	if err != nil {
		t.Fatalf("the filed report is missing: %v\nstdout: %s", err, stdout)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("filed report mode = %v, want 0600", info.Mode().Perm())
	}
	want, err := os.ReadFile(draft)
	if err != nil {
		t.Fatalf("read draft: %v", err)
	}
	if got := mustReadFile(t, filed); got != string(want) {
		t.Fatalf("filed bytes differ from the draft (which carries no comments, so normalized == raw):\ngot:\n%s\nwant:\n%s", got, want)
	}
	assertAbsent(t, filepath.Join(project, ".a2a", "cache", "feedback-repo"), "the inbox route clones nothing")

	t.Run("copy-is-triageable", func(t *testing.T) {
		hub := t.TempDir() // not a git work tree
		mustMkdirAll(t, filepath.Join(hub, "feedback", "inbox"))
		cp := exec.Command("cp", "-n", filed, filepath.Join(hub, "feedback", "inbox")+string(os.PathSeparator))
		if out, err := cp.CombinedOutput(); err != nil {
			t.Fatalf("cp -n: %v\n%s", err, out)
		}
		hubEnv := feedbackInboxEnv("HOME="+t.TempDir(), "A2A_FEEDBACK_HUB_URL="+t.TempDir())

		rel := filepath.Join("feedback", "inbox", feedbackSubmitTestID+".yaml")
		stdout, stderr, code := runFeedbackInbox(t, hub, hubEnv, "validate", "--ci", rel)
		if code != 0 || !strings.Contains(stdout, "valid") {
			t.Fatalf("validate --ci: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
		}
		stdout, stderr, code = runFeedbackInbox(t, hub, hubEnv, "triage")
		if code != 0 || !strings.Contains(stdout, feedbackSubmitTestID) {
			t.Fatalf("triage: exit %d, want it to list %s\nstdout: %s\nstderr: %s", code, feedbackSubmitTestID, stdout, stderr)
		}
	})
}

// TestFeedbackInboxExplicitFlagWithCredential is spec 08 AC3: a machine WITH a
// token files to the inbox when it says --inbox, and the fake GitHub it is
// pointed at records no request at all.
func TestFeedbackInboxExplicitFlagWithCredential(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon")
	draft := writeFeedbackDraft(t, filepath.Join(r.projectDir, "drafts"))
	env := feedbackInboxEnv("HOME="+r.homeDir, "A2A_FEEDBACK_TOKEN=dummy-token", "A2A_GITHUB_API="+r.gh.URL)

	stdout, stderr, code := runFeedbackInbox(t, r.projectDir, env, "submit", "--inbox", draft)
	if code != 0 {
		t.Fatalf("submit --inbox: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "to the local inbox") || !strings.Contains(stdout, "(--inbox)") {
		t.Fatalf("stdout = %q, want a filing with reason `--inbox`", stdout)
	}
	if _, err := os.Stat(filepath.Join(r.homeDir, feedbackInboxRelative, feedbackSubmitTestID+".yaml")); err != nil {
		t.Fatalf("the filed report is missing: %v", err)
	}
	if reqs := r.gh.Requests(); len(reqs) != 0 {
		t.Fatalf("the fake GitHub recorded %d request(s) for an inbox filing, want none: %v", len(reqs), reqs)
	}
	assertAbsent(t, filepath.Join(r.projectDir, ".a2a", "cache", "feedback-repo"), "the inbox route clones nothing")
}

// TestFeedbackInboxDeliveredLaterOnce is spec 08 AC10, through the binary: a
// report filed to the inbox and then edited in its draft is delivered by
// `submit --all` with a credential as exactly one PR whose committed file is
// the INBOX copy; the ledger row gains the PR and keeps the inbox path, and a
// second `--all` selects nothing.
func TestFeedbackInboxDeliveredLaterOnce(t *testing.T) {
	t.Parallel()
	r := newHostRig(t, "axon", "axon")
	r.seedFeedbackHubBranch()
	draft := writeFeedbackDraft(t, filepath.Join(r.projectDir, ".a2a", "feedback"))
	local := feedbackInboxEnv("HOME="+r.homeDir, "A2A_FEEDBACK_TOKEN=dummy-token",
		"A2A_FEEDBACK_REPO="+r.fx.RemoteURL(), "A2A_GITHUB_API="+r.gh.URL)

	if stdout, stderr, code := runFeedbackInbox(t, r.projectDir, local, "submit", "--inbox", draft); code != 0 {
		t.Fatalf("submit --inbox: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	inboxCopy := filepath.Join(r.homeDir, feedbackInboxRelative, feedbackSubmitTestID+".yaml")
	filedBytes := mustReadFile(t, inboxCopy)
	if prs := r.gh.PRs(); len(prs) != 0 {
		t.Fatalf("an inbox filing opened %d PR(s)", len(prs))
	}

	// The draft is edited after filing. Delivery must send what was FILED.
	edited := strings.Replace(filedBytes, "grounded in this session's own inbox output review", "an edit made after the report was filed", 1)
	if edited == filedBytes {
		t.Fatal("the test's edit did not change the draft")
	}
	mustWrite(t, draft, edited)

	stdout, stderr, code := runFeedbackInbox(t, r.projectDir, local, "submit", "--all")
	if code != 0 {
		t.Fatalf("submit --all: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	prs := r.gh.PRs()
	if len(prs) != 1 {
		t.Fatalf("PRs = %d, want exactly 1 (host calls: %v)\nstdout: %s", len(prs), r.gh.Requests(), stdout)
	}
	// The fake host lands the PR, so the report is read off the hub branch it
	// landed on (host_loop_test.go's feedbackHubBranchForFixtures).
	committed := gitOutput(t, r.fx.RemoteURL(), "show", feedbackHubBranchForFixtures+":feedback/inbox/"+feedbackSubmitTestID+".yaml")
	if committed != filedBytes {
		t.Fatalf("the delivered file is not the inbox copy:\ngot:\n%s\nwant:\n%s", committed, filedBytes)
	}

	ledger := mustReadFile(t, filepath.Join(r.projectDir, ".a2a", "feedback", "ledger.yaml"))
	if !strings.Contains(ledger, "pr_url: https://") || !strings.Contains(ledger, "inbox: "+inboxCopy) {
		t.Fatalf("ledger = %s\nwant pr_url set and the inbox path kept", ledger)
	}

	stdout, stderr, code = runFeedbackInbox(t, r.projectDir, local, "submit", "--all")
	if code != 0 || !strings.Contains(stdout, "no unsubmitted drafts") {
		t.Fatalf("second submit --all: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if got := len(r.gh.PRs()); got != 1 {
		t.Fatalf("PRs after the second --all = %d, want still 1", got)
	}
}

// TestFeedbackInboxNoDataRoot is spec 08 AC13: with HOME empty the machine has
// no place for the inbox (nor for the machine config), so the no-credential
// submit exits 1 with a message that names the data root, and writes neither an
// inbox file nor a ledger row.
func TestFeedbackInboxNoDataRoot(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	draft := writeFeedbackDraft(t, t.TempDir())
	env := feedbackInboxEnv("HOME=", loggedOutGHFirstOnPath(t))

	stdout, stderr, code := runFeedbackInbox(t, project, env, "submit", draft)
	if code != 1 {
		t.Fatalf("submit with HOME empty: exit %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "data root") {
		t.Fatalf("stderr = %q, want it to name the data root", stderr)
	}
	assertAbsent(t, filepath.Join(project, ".a2a"), "a refused filing must write no ledger row")
}
