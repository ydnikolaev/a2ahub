package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A git hook, rebase --exec or submodule foreach exports the invoker's
// repository through the environment. The host's git calls name their own
// directory and must never act on that one. Not parallel: it sets the process
// environment.
func TestRunRecoveryGitIgnoresInheritedRepositoryEnvironment(t *testing.T) {
	outer := t.TempDir()
	if res := runRecoveryGit(context.Background(), 4096, "init", "--quiet", outer); res.err != nil || res.exitCode != 0 {
		t.Fatalf("init outer: %+v", res)
	}
	keep := filepath.Join(outer, "ignored-by-nobody")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(outer, ".git"))
	t.Setenv("GIT_WORK_TREE", outer)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(outer, ".git", "index"))

	inner := filepath.Join(t.TempDir(), "land")
	if res := runRecoveryGit(context.Background(), 4096, "init", "--quiet", inner); res.err != nil || res.exitCode != 0 {
		t.Fatalf("init inner: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(inner, ".git", "HEAD")); err != nil {
		t.Fatalf("the inner repository was not created where it was named: %v", err)
	}
	if res := runRecoveryGit(context.Background(), 4096, "-C", inner, "clean", "-ffdxq"); res.err != nil || res.exitCode != 0 {
		t.Fatalf("clean inner: %+v", res)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a clean of the inner repository deleted a file in the invoker's work tree: %v", err)
	}
}
