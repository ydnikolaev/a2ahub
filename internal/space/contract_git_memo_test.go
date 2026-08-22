package space

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The memo's whole safety argument is its allowlist, so the allowlist is what
// the test asserts — in BOTH directions. A test that only proved the eligible
// shapes are admitted could not tell this predicate from `return true`.
func TestContractGitMemoizableAdmitsOnlyObjectAddressedReads(t *testing.T) {
	t.Parallel()
	const oid = "0dc31cb9818d5f9924d94b4e43b0b6eae2daef90"
	admitted := [][]string{
		{"cat-file", "-s", oid},
		{"cat-file", "blob", oid},
		{"ls-tree", "-z", "--full-tree", oid, "--", "axon/events"},
		{"diff-tree", "--no-commit-id", "--diff-filter=A", "--name-only", "-r", "-z", oid},
		{"rev-list", "--parents", "-n", "1", oid},
		{"rev-parse", "--verify", "--end-of-options", oid + "^{commit}"},
	}
	for _, args := range admitted {
		if !contractGitMemoizable(args) {
			t.Errorf("object-addressed read refused by the memo: git %s", strings.Join(args, " "))
		}
	}

	refused := [][]string{
		{},
		{"log", "--first-parent", "--reverse", "--name-only"},
		{"fetch", "origin"},
		{"rev-parse", "--verify", "HEAD"},
		{"rev-parse", "--verify", "--end-of-options", "main^{commit}"},
		{"rev-parse", "--verify", "--end-of-options", "refs/heads/main"},
		{"cat-file", "-s", "HEAD"},
		{"ls-tree", "-z", "--full-tree", "refs/heads/main"},
		{"rev-list", "--parents", "-n", "1", "HEAD~2"},
	}
	for _, args := range refused {
		if contractGitMemoizable(args) {
			t.Errorf("a read that can answer differently mid-render was admitted: git %s", strings.Join(args, " "))
		}
	}
}

// Without an installed memo nothing changes, and with one the SECOND identical
// read must not reach git at all. Proven by moving the repository out from
// under the second call: a served memo answers, a fresh exec cannot.
func TestContractGitCacheServesTheSecondIdenticalRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// contractTestGit is this package's own fixture seam — every git exec site
	// routes through gitfixture.Args, and TestHygiene_R1 enforces it.
	contractTestGit(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("contract body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	contractTestGit(t, repo, "add", "-A")
	contractTestGit(t, repo, "commit", "-qm", "seed")

	ctx := WithContractGitCache(context.Background())
	head, err := contractGitBytes(ctx, repo, maxTreeCommitMetadataBytes, "rev-parse", "--verify", "HEAD")
	if err != nil {
		t.Fatalf("resolve head: %v", err)
	}
	oid := strings.TrimSpace(string(head))

	first, err := contractGitBytes(ctx, repo, maxContractGitListingBytes, "ls-tree", "-z", "--full-tree", oid)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}

	// The repository is gone. Anything that still answers did not exec git.
	if err := os.RemoveAll(repo); err != nil {
		t.Fatalf("remove repo: %v", err)
	}
	second, err := contractGitBytes(ctx, repo, maxContractGitListingBytes, "ls-tree", "-z", "--full-tree", oid)
	if err != nil {
		t.Fatalf("the memo did not serve an identical object-addressed read: %v", err)
	}
	if string(second) != string(first) {
		t.Fatalf("memo answered differently:\n first: %q\nsecond: %q", first, second)
	}

	// The same command under a DIFFERENT read ceiling is a different question,
	// so it must not be served from the entry the wider ceiling produced.
	if _, err := contractGitBytes(ctx, repo, 8, "ls-tree", "-z", "--full-tree", oid); err == nil {
		t.Fatal("a narrower read ceiling was served from a wider ceiling's memo entry")
	}

	// And with no memo installed, the same read is an ordinary exec — which
	// against a removed repository fails.
	if _, err := contractGitBytes(context.Background(), repo, maxContractGitListingBytes,
		"ls-tree", "-z", "--full-tree", oid); err == nil {
		t.Fatal("a read with no memo installed answered without the repository present")
	}
}

func TestWithContractGitCacheDoesNotNest(t *testing.T) {
	t.Parallel()
	first := WithContractGitCache(context.Background())
	second := WithContractGitCache(first)
	if contractGitMemoFrom(first) != contractGitMemoFrom(second) {
		t.Fatal("nesting split one render's memo into two")
	}
}
