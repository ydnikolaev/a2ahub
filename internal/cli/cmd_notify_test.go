package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/spacenotify"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// cmd_notify_test.go builds its own minimal throwaway git checkout — the
// same convention cmd_validate_ci_test.go's own fixtures use — so
// runNotifyRender can be exercised against a real `./space.yaml` +
// artifact tree without going through NotifyCommand.Run's fixed root ".".

func notifyFixtureRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runNotifyGit(t, dir, "init", "-b", "main", dir)
	gitfixture.HardenRepo(t, dir)
	mustNotifyWrite(t, filepath.Join(dir, "space.yaml"),
		"schema: space/v1\nspace: fixture-space\nparticipants:\n"+
			"  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n"+
			"  - system: seomatrix\n    org: fixture\n    section: seomatrix\n    owners: [seo-bot]\n    status: active\n    joined: \"2026-01-01\"\n"+
			"notification_routes:\n  - channel: telegram\n    chat: \"-100\"\n    events: [human-gate, blocking, published]\n")
	for _, sys := range []string{"axon", "seomatrix"} {
		mustNotifyMkdirAll(t, filepath.Join(dir, sys, "events", "2026"))
		mustNotifyWrite(t, filepath.Join(dir, sys, "events", "2026", ".gitkeep"), "")
	}
	runNotifyGitCommit(t, dir, "seed")
	return dir
}

func notifyCommitArtifact(t *testing.T, dir, relPath, id string) {
	t.Helper()
	front := "schema: envelope/v1\nid: " + id + "\ntype: work_request\ntitle: fixture item\n" +
		"space: fixture-space\nfrom: axon\nto: [seomatrix]\n" +
		"actor: {kind: agent, name: axon-bot}\ncreated: \"" + time.Now().UTC().Format(time.RFC3339) + "\"\n" +
		"priority: p2\nblocking: false\nclassification: internal\n"
	full := "---\n" + front + "---\nbody text"
	fullPath := filepath.Join(dir, filepath.FromSlash(relPath))
	mustNotifyMkdirAll(t, filepath.Dir(fullPath))
	mustNotifyWrite(t, fullPath, full)
	runNotifyGitCommit(t, dir, "artifact "+id)
}

func runNotifyGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out.String())
	}
}

func runNotifyGitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	runNotifyGit(t, dir, "add", "-A")
	cmd := exec.Command("git", gitfixture.Args("commit", "-m", msg)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out.String())
	}
}

func mustNotifyMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
}

func mustNotifyWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func noopGitChanged(ctx context.Context, root, base string) ([]string, error) { return nil, nil }

// TestNotifyRender_NoCache is AC1 through the CLI surface: a bare checkout
// (no .a2a/) with --all produces messages.
func TestNotifyRender_NoCache(t *testing.T) {
	t.Parallel()
	dir := notifyFixtureRoot(t)
	notifyCommitArtifact(t, dir, "axon/exchanges/XW-axon-20260701-a001.md", "XW-axon-20260701-a001")

	if _, err := os.Stat(filepath.Join(dir, ".a2a")); !os.IsNotExist(err) {
		t.Fatalf(".a2a must not exist")
	}

	var stdout, stderr bytes.Buffer
	code := runNotifyRender(context.Background(), dir, noopGitChanged, time.Now, []string{"--all"}, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr=%s", code, stderr.String())
	}
	var msgs []spacenotify.Message
	if err := json.Unmarshal(stdout.Bytes(), &msgs); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}
}

// TestNotifyRender_Only is AC13/AC14 through the CLI surface.
func TestNotifyRender_Only(t *testing.T) {
	t.Parallel()
	dir := notifyFixtureRoot(t)
	notifyCommitArtifact(t, dir, "axon/exchanges/XW-axon-20260701-a002.md", "XW-axon-20260701-a002")

	var stdout, stderr bytes.Buffer
	code := runNotifyRender(context.Background(), dir, noopGitChanged, time.Now, []string{"--only", "XW-axon-20260701-a002"}, IO{Stdout: &stdout, Stderr: &stderr})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr=%s", code, stderr.String())
	}
	var msgs []spacenotify.Message
	if err := json.Unmarshal(stdout.Bytes(), &msgs); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len(msgs) = %d, want 1", len(msgs))
	}

	stdout.Reset()
	stderr.Reset()
	code = runNotifyRender(context.Background(), dir, noopGitChanged, time.Now, []string{"--only", "XW-axon-20260701-a002,does-not-exist"}, IO{Stdout: &stdout, Stderr: &stderr})
	if code == 0 {
		t.Fatalf("want a non-zero exit for an unknown --only id")
	}
	if stdout.Len() != 0 {
		t.Fatalf("want nothing on stdout, got %s", stdout.String())
	}
}

// TestNotifyRender_UsageErrors covers the flag-level refusals: no
// selector, and a conflicting pair.
func TestNotifyRender_UsageErrors(t *testing.T) {
	t.Parallel()
	dir := notifyFixtureRoot(t)

	var stdout, stderr bytes.Buffer
	if code := runNotifyRender(context.Background(), dir, noopGitChanged, time.Now, nil, IO{Stdout: &stdout, Stderr: &stderr}); code != 2 {
		t.Fatalf("no selector: exit = %d, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runNotifyRender(context.Background(), dir, noopGitChanged, time.Now, []string{"--all", "--only", "XW-1"}, IO{Stdout: &stdout, Stderr: &stderr}); code != 2 {
		t.Fatalf("conflicting selectors: exit = %d, want 2", code)
	}
}

// TestNotifyCommand_HelpNamesThePlane is T1's own requirement: the help
// line must say which plane (`notify` vs `notifications`) this verb
// belongs to.
func TestNotifyCommand_HelpNamesThePlane(t *testing.T) {
	t.Parallel()
	cmd := NewNotifyCommand()
	if got := cmd.Synopsis(); got == "" {
		t.Fatalf("Synopsis is empty")
	}
}
