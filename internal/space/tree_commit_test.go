package space

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitTreeFromBaseMixedMutationPreservesBase(t *testing.T) {
	t.Parallel()
	repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/keep.txt":    {content: "keep\n", mode: 0o644},
		"axon/replace.txt": {content: "before\n", mode: 0o644},
		"axon/delete.txt":  {content: "delete\n", mode: 0o644},
	})
	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()

	sha, fresh, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
		RepoDir: repo, BaseRef: base, UpdateRef: "refs/heads/a2a/axon/test/XW-one",
		Message: "test: mixed mutation", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
		Mutations: []Mutation{
			{Path: "axon/replace.txt", Operation: MutationWrite, Bytes: []byte("after\n"), ExpectedBefore: treeCommitPrecondition("before\n", "100644")},
			{Path: "axon/new.txt", Operation: MutationWrite, Bytes: []byte("new\n")},
			{Path: "axon/delete.txt", Operation: MutationDelete, ExpectedBefore: treeCommitPrecondition("delete\n", "100644")},
		},
	})
	if err != nil {
		t.Fatalf("commitTreeFromBase: %v", err)
	}
	if !fresh {
		t.Fatal("changed tree reported fresh=false")
	}
	assertTreeCommitFile(t, repo, sha, "axon/keep.txt", "keep\n")
	assertTreeCommitFile(t, repo, sha, "axon/replace.txt", "after\n")
	assertTreeCommitFile(t, repo, sha, "axon/new.txt", "new\n")
	if err := runGit(context.Background(), repo, "cat-file", "-e", sha+":axon/delete.txt"); err == nil {
		t.Fatal("deleted path remains in committed tree")
	}
	parent, err := runGitOutput(context.Background(), repo, nil, "rev-parse", sha+"^")
	if err != nil || parent != base {
		t.Fatalf("commit parent = %q, %v; want %q", parent, err, base)
	}
	ref, err := runGitOutput(context.Background(), repo, nil, "rev-parse", "refs/heads/a2a/axon/test/XW-one")
	if err != nil || ref != sha {
		t.Fatalf("updated ref = %q, %v; want %q", ref, err, sha)
	}
	// No checkout is involved: the worktree remains on its original branch
	// and its files retain the base bytes.
	raw, err := os.ReadFile(filepath.Join(repo, "axon", "replace.txt"))
	if err != nil || string(raw) != "before\n" {
		t.Fatalf("worktree replace.txt = %q, %v; want untouched base bytes", raw, err)
	}
}

func TestCommitOneUsesFrozenBaseSHAWhenCurrentBaseAdvances(t *testing.T) {
	t.Parallel()

	repo, frozenBase := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/base.txt": {content: "frozen\n", mode: 0o644},
	})
	if err := os.WriteFile(filepath.Join(repo, "axon", "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGit(context.Background(), repo, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	identity := []string{
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@a2a.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@a2a.invalid",
	}
	if _, err := runGitOutput(context.Background(), repo, identity, "commit", "-m", "advanced base"); err != nil {
		t.Fatal(err)
	}
	advanced, err := runGitOutput(context.Background(), repo, nil, "rev-parse", "HEAD")
	if err != nil || advanced == frozenBase {
		t.Fatalf("advanced base = %q err=%v, frozen=%q", advanced, err, frozenBase)
	}

	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()
	sha, fresh, err := NewWriteFunnel(nil, nil, "0.1.0").commitOne(context.Background(), lock, SubmitRequest{
		RepoDir: repo, System: "axon", Verb: "submit", ArtifactID: "XQ-axon-frozen",
		Mutations:     []Mutation{{Path: "axon/new.txt", Operation: MutationWrite, Bytes: []byte("new\n")}},
		CommitMessage: "test: frozen base", ExpectedBaseSHA: frozenBase,
	}, "a2a/axon/submit/XQ-axon-frozen")
	if err != nil || !fresh {
		t.Fatalf("commitOne = sha=%q fresh=%v err=%v", sha, fresh, err)
	}
	parent, err := runGitOutput(context.Background(), repo, nil, "rev-parse", sha+"^")
	if err != nil || parent != frozenBase {
		t.Fatalf("commit parent = %q err=%v, want frozen %q (not current %q)", parent, err, frozenBase, advanced)
	}
	if err := runGit(context.Background(), repo, "cat-file", "-e", sha+":axon/later.txt"); err == nil {
		t.Fatal("commit inherited a file from the advanced mutable base")
	}
}

func TestCommitTreeFromBaseUnchangedTreeReturnsBaseAndCreatesRef(t *testing.T) {
	t.Parallel()
	repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/file.txt": {content: "same\n", mode: 0o644},
	})
	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()
	const target = "refs/heads/a2a/axon/test/XW-same"

	sha, fresh, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
		RepoDir: repo, BaseRef: base, UpdateRef: target,
		Message: "test: no change", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
		Mutations: []Mutation{{
			Path: "axon/file.txt", Operation: MutationWrite, Bytes: []byte("same\n"),
			ExpectedBefore: treeCommitPrecondition("same\n", "100644"),
		}},
	})
	if err != nil {
		t.Fatalf("commitTreeFromBase: %v", err)
	}
	if fresh {
		t.Fatal("unchanged tree reported fresh=true")
	}
	if sha != base {
		t.Fatalf("unchanged sha = %q; want base %q", sha, base)
	}
	got, err := runGitOutput(context.Background(), repo, nil, "rev-parse", target)
	if err != nil || got != base {
		t.Fatalf("unchanged target = %q, %v; want %q", got, err, base)
	}
}

func TestCommitTreeFromBasePreservesExecutableMode(t *testing.T) {
	t.Parallel()
	repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/tool": {content: "#!/bin/sh\nexit 0\n", mode: 0o755},
	})
	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()

	sha, fresh, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
		RepoDir: repo, BaseRef: base, UpdateRef: "refs/heads/a2a/axon/test/XW-mode",
		Message: "test: executable mode", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
		Mutations: []Mutation{{
			Path: "axon/tool", Operation: MutationWrite, Bytes: []byte("#!/bin/sh\nexit 1\n"),
			ExpectedBefore: treeCommitPrecondition("#!/bin/sh\nexit 0\n", "100755"),
		}},
	})
	if err != nil {
		t.Fatalf("commitTreeFromBase: %v", err)
	}
	if !fresh {
		t.Fatal("changed tree reported fresh=false")
	}
	entry, err := runGitOutput(context.Background(), repo, nil, "ls-tree", sha, "--", "axon/tool")
	if err != nil || !strings.HasPrefix(entry, "100755 blob ") {
		t.Fatalf("ls-tree executable = %q, %v", entry, err)
	}
}

func TestCommitTreeFromBaseStalePreconditionDoesNotMoveRef(t *testing.T) {
	t.Parallel()
	repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/file.txt": {content: "current\n", mode: 0o644},
	})
	const target = "refs/heads/a2a/axon/test/XW-stale"
	if err := runGit(context.Background(), repo, "update-ref", target, base); err != nil {
		t.Fatalf("seed target ref: %v", err)
	}
	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()

	_, _, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
		RepoDir: repo, BaseRef: base, UpdateRef: target,
		Message: "test: stale", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
		Mutations: []Mutation{{
			Path: "axon/file.txt", Operation: MutationWrite, Bytes: []byte("replacement\n"),
			ExpectedBefore: treeCommitPrecondition("not-current\n", "100644"),
		}},
	})
	if !errors.Is(err, ErrMutationPrecondition) {
		t.Fatalf("error = %v; want ErrMutationPrecondition", err)
	}
	got, refErr := runGitOutput(context.Background(), repo, nil, "rev-parse", target)
	if refErr != nil || got != base {
		t.Fatalf("target moved after refusal: %q, %v; want %q", got, refErr, base)
	}
}

func TestCommitTreeFromBaseRefusesBaseTreePreconditionMismatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		path     string
		expected *MutationPrecondition
	}{
		{name: "missing", path: "axon/missing.txt", expected: treeCommitPrecondition("current\n", "100644")},
		{name: "type", path: "axon/dir", expected: treeCommitPrecondition("current\n", "100644")},
		{name: "mode", path: "axon/file.txt", expected: treeCommitPrecondition("current\n", "100755")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
				"axon/file.txt":  {content: "current\n", mode: 0o644},
				"axon/dir/child": {content: "child\n", mode: 0o644},
			})
			target := "refs/heads/a2a/axon/test/XW-" + test.name
			if err := runGit(context.Background(), repo, "update-ref", target, base); err != nil {
				t.Fatalf("seed target ref: %v", err)
			}
			lock := acquireTreeCommitLock(t, repo)
			defer func() { _ = lock.Release() }()

			_, _, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
				RepoDir: repo, BaseRef: base, UpdateRef: target,
				Message: "test: precondition", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
				Mutations: []Mutation{{
					Path: test.path, Operation: MutationWrite, Bytes: []byte("replacement\n"),
					ExpectedBefore: test.expected,
				}},
			})
			if !errors.Is(err, ErrMutationPrecondition) {
				t.Fatalf("error = %v; want ErrMutationPrecondition", err)
			}
			got, refErr := runGitOutput(context.Background(), repo, nil, "rev-parse", target)
			if refErr != nil || got != base {
				t.Fatalf("target moved after refusal: %q, %v; want %q", got, refErr, base)
			}
		})
	}
}

func TestCommitTreeFromBaseRefusesTraversal(t *testing.T) {
	t.Parallel()
	repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/file.txt": {content: "current\n", mode: 0o644},
	})
	const target = "refs/heads/a2a/axon/test/XW-traversal"
	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()

	_, _, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
		RepoDir: repo, BaseRef: base, UpdateRef: target,
		Message: "test: traversal", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
		Mutations: []Mutation{{Path: "../outside", Operation: MutationWrite, Bytes: []byte("bad")}},
	})
	if !errors.Is(err, ErrMutationInvalid) {
		t.Fatalf("error = %v; want ErrMutationInvalid", err)
	}
	if err := runGit(context.Background(), repo, "rev-parse", "--verify", target); err == nil {
		t.Fatal("invalid mutation unexpectedly created target ref")
	}
}

func TestCommitTreeFromBaseIgnoresWorktreeSymlink(t *testing.T) {
	t.Parallel()
	repo, base := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"axon/file.txt": {content: "base\n", mode: 0o644},
	})
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("outside\n"), 0o644); err != nil {
		t.Fatalf("write external fixture: %v", err)
	}
	worktreePath := filepath.Join(repo, "axon", "file.txt")
	if err := os.Remove(worktreePath); err != nil {
		t.Fatalf("remove worktree fixture: %v", err)
	}
	if err := os.Symlink(external, worktreePath); err != nil {
		t.Fatalf("symlink worktree fixture: %v", err)
	}
	lock := acquireTreeCommitLock(t, repo)
	defer func() { _ = lock.Release() }()

	sha, fresh, err := commitTreeFromBase(context.Background(), lock, treeCommitRequest{
		RepoDir: repo, BaseRef: base, UpdateRef: "refs/heads/a2a/axon/test/XW-symlink",
		Message: "test: symlink independence", AuthorName: "a2a test", AuthorMail: "test@a2a.invalid",
		Mutations: []Mutation{{
			Path: "axon/file.txt", Operation: MutationWrite, Bytes: []byte("committed\n"),
			ExpectedBefore: treeCommitPrecondition("base\n", "100644"),
		}},
	})
	if err != nil {
		t.Fatalf("commitTreeFromBase: %v", err)
	}
	if !fresh {
		t.Fatal("changed tree reported fresh=false")
	}
	assertTreeCommitFile(t, repo, sha, "axon/file.txt", "committed\n")
	raw, err := os.ReadFile(external)
	if err != nil || string(raw) != "outside\n" {
		t.Fatalf("external file = %q, %v; want unchanged", raw, err)
	}
	info, err := os.Lstat(worktreePath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("worktree symlink changed: mode=%v err=%v", info.Mode(), err)
	}
}

type treeCommitFixtureFile struct {
	content string
	mode    os.FileMode
}

func newTreeCommitRepo(t *testing.T, files map[string]treeCommitFixtureFile) (string, string) {
	t.Helper()
	repo := t.TempDir()
	ctx := context.Background()
	if err := runGit(ctx, repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	for path, file := range files {
		absolute := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
		if err := os.WriteFile(absolute, []byte(file.content), file.mode); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	if err := runGit(ctx, repo, "add", "--all"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	env := []string{
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@a2a.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@a2a.invalid",
	}
	if _, err := runGitOutput(ctx, repo, env, "commit", "-m", "fixture"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	base, err := runGitOutput(ctx, repo, nil, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("resolve fixture head: %v", err)
	}
	return repo, base
}

func acquireTreeCommitLock(t *testing.T, repo string) *MirrorLock {
	t.Helper()
	lock, err := AcquireMirrorLock(context.Background(), repo)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}
	return lock
}

func treeCommitPrecondition(content, mode string) *MutationPrecondition {
	digest := sha256.Sum256([]byte(content))
	return &MutationPrecondition{Digest: "sha256:" + fmt.Sprintf("%x", digest[:]), Mode: mode}
}

func assertTreeCommitFile(t *testing.T, repo, sha, path, want string) {
	t.Helper()
	got, err := runGitOutput(context.Background(), repo, nil, "show", sha+":"+path)
	if err != nil {
		t.Fatalf("read %s from %s: %v", path, sha, err)
	}
	if got+"\n" != want {
		t.Fatalf("%s = %q; want %q", path, got+"\n", want)
	}
}
