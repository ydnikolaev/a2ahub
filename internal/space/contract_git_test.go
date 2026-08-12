package space

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

func TestContractGitBytesIsBoundedAndContextCancelled(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	writeContractCandidateFile(t, repo, "large.txt", "0123456789")
	contractTestGit(t, repo, "add", "--", "large.txt")
	contractTestGit(t, repo, "commit", "-q", "-m", "large")

	if _, err := contractGitBytes(t.Context(), repo, 4, "show", "HEAD:large.txt"); !errors.Is(err, ErrContractBoundedResult) {
		t.Fatalf("bounded git read error = %v", err)
	}
	if raw, err := contractGitBytes(t.Context(), repo, 10, "show", "HEAD:large.txt"); err != nil || string(raw) != "0123456789" {
		t.Fatalf("exact-bound git read = %q, %v", raw, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := contractGitBytes(ctx, repo, 1024, "rev-list", "HEAD"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled git read error = %v, want context.Canceled", err)
	}
}

func TestContractGitBytesIgnoresAmbientGitRepositoryAndObjectOverrides(t *testing.T) {
	// reason: process environment mutation must not overlap a parallel test.
	repo := newContractHistoryRepo(t)
	writeContractCandidateFile(t, repo, "tracked", "right repository")
	contractTestGit(t, repo, "add", "--", "tracked")
	contractTestGit(t, repo, "commit", "-q", "-m", "tracked")
	want := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD"))
	foreign := newContractHistoryRepo(t)
	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(foreign, ".git", "objects"))
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", filepath.Join(foreign, ".git", "objects"))

	got, err := contractGitResolveCommit(t.Context(), repo, "HEAD")
	if err != nil {
		t.Fatalf("resolve with hostile ambient Git environment: %v", err)
	}
	if got != want {
		t.Fatalf("resolved commit = %s, want %s", got, want)
	}
}

func TestContractGitResolveCommitRejectsBlobObject(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	writeContractCandidateFile(t, repo, "blob", "not a commit")
	contractTestGit(t, repo, "add", "--", "blob")
	contractTestGit(t, repo, "commit", "-q", "-m", "blob")
	blob := strings.TrimSpace(contractTestGitOutput(t, repo, "rev-parse", "HEAD:blob"))
	if _, err := contractGitResolveCommit(t.Context(), repo, blob); !errors.Is(err, ErrContractObjectUnavailable) {
		t.Fatalf("contractGitResolveCommit(blob) = %v", err)
	}
}

func newContractHistoryRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	contractTestGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitfixture.HardenRepo(t, origin)
	repo := filepath.Join(root, "work")
	contractTestGit(t, root, "clone", "-q", origin, repo)
	return repo
}

func contractTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
}

func contractTestGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, stderr.Bytes())
	}
	return stdout.String()
}
