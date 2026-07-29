package livee2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

func TestGitMainContainsRequiresTheMirrorTreeAtOriginMain(t *testing.T) {
	t.Parallel()

	mirror := filepath.Join(t.TempDir(), "mirror")
	if err := os.MkdirAll(mirror, 0o755); err != nil {
		t.Fatalf("mkdir mirror: %v", err)
	}
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", gitfixture.Args(args...)...)
		cmd.Dir = mirror
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(mirror, "space.yaml"), []byte("space: livee2e\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit("add", "space.yaml")
	runGit("commit", "-m", "seed")
	mergedSHA := runGit("rev-parse", "HEAD")
	runGit("update-ref", "refs/remotes/origin/main", mergedSHA)

	ok, err := gitMainContains(context.Background(), mirror, mergedSHA)
	if err != nil || !ok {
		t.Fatalf("gitMainContains on main = %v, %v; want true, nil", ok, err)
	}

	runGit("checkout", "-b", "stale-ephemeral")
	if err := os.WriteFile(filepath.Join(mirror, "stale.txt"), []byte("unmerged\n"), 0o644); err != nil {
		t.Fatalf("write stale file: %v", err)
	}
	runGit("add", "stale.txt")
	runGit("commit", "-m", "stale branch")

	ok, err = gitMainContains(context.Background(), mirror, mergedSHA)
	if err != nil {
		t.Fatalf("gitMainContains on stale tree: %v", err)
	}
	if ok {
		t.Fatal("gitMainContains = true while HEAD differs from origin/main")
	}
}

func TestGitMainContainsRefusesAnUntrustedObjectIDBeforeGit(t *testing.T) {
	t.Parallel()

	ok, err := gitMainContains(context.Background(), t.TempDir(), "--help")
	if err == nil || ok {
		t.Fatalf("gitMainContains(--help) = %v, %v; want false and a validation error", ok, err)
	}
	if !strings.Contains(err.Error(), "not a full git object id") {
		t.Fatalf("error = %q, want the object-id refusal", err)
	}
}
