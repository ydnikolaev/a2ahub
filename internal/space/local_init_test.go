package space

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func localInitGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitPlumbingOutput(context.Background(), dir, nil, nil, maxTreeCommitMetadataBytes,
		append([]string{"--git-dir=" + dir}, args...)...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func localInitFiles() []FileWrite {
	return []FileWrite{
		{Path: "space.yaml", Content: []byte("space: demo\nparticipants: []\n")},
		{Path: "decisions/.gitkeep", Content: []byte{}},
		{Path: "vendored/.gitkeep", Content: []byte{}},
	}
}

func TestInitLocalSpaceRepoWritesOneRootCommitWithExactlyTheGivenFiles(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "missing", "parent", "demo.git")
	sha, err := InitLocalSpaceRepo(context.Background(), dir, localInitFiles())
	if err != nil {
		t.Fatal(err)
	}
	if got := localInitGit(t, dir, "rev-parse", "refs/heads/main"); got != sha {
		t.Fatalf("refs/heads/main = %s, want the returned root commit %s", got, sha)
	}
	if got := localInitGit(t, dir, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Fatalf("HEAD = %q, want refs/heads/main", got)
	}
	if got := localInitGit(t, dir, "rev-list", "--count", "main"); got != "1" {
		t.Fatalf("commit count = %s, want 1 root commit", got)
	}
	if got := localInitGit(t, dir, "rev-list", "--parents", "-n", "1", "main"); got != sha {
		t.Fatalf("root commit has parents: %q", got)
	}
	tree := strings.Split(localInitGit(t, dir, "ls-tree", "-r", "--name-only", "main"), "\n")
	if got, want := strings.Join(tree, ","), "decisions/.gitkeep,space.yaml,vendored/.gitkeep"; got != want {
		t.Fatalf("tree = %s, want %s", got, want)
	}
	if got := localInitGit(t, dir, "show", "main:space.yaml"); got != "space: demo\nparticipants: []" {
		t.Fatalf("space.yaml = %q", got)
	}
	if got := localInitGit(t, dir, "rev-parse", "--is-bare-repository"); got != "true" {
		t.Fatalf("is-bare = %q", got)
	}
}

func TestInitLocalSpaceRepoInstallsNoHookAndIgnoresUserTemplates(t *testing.T) {
	// Not parallel: the template directory is process-environment state.
	tmpl := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpl, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, "hooks", "pre-receive"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", tmpl)
	dir := filepath.Join(t.TempDir(), "demo.git")
	if _, err := InitLocalSpaceRepo(context.Background(), dir, localInitFiles()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks", "pre-receive")); !os.IsNotExist(err) {
		t.Fatalf("pre-receive present after init (stat err %v): the bootstrap must install no hook and read no template", err)
	}
}

func TestInitLocalSpaceRepoIsNotAffectedByAnEnclosingRepository(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	if out, err := exec.Command("git", "init", "--quiet", outer).CombinedOutput(); err != nil {
		t.Fatalf("git init outer: %v: %s", err, out)
	}
	dir := filepath.Join(outer, "inside.git")
	if _, err := InitLocalSpaceRepo(context.Background(), dir, localInitFiles()); err != nil {
		t.Fatal(err)
	}
	if got := localInitGit(t, dir, "rev-list", "--count", "main"); got != "1" {
		t.Fatalf("commit count = %s", got)
	}
	if out, err := exec.Command("git", "-C", outer, "rev-parse", "--verify", "--quiet", "HEAD").CombinedOutput(); err == nil {
		t.Fatalf("the enclosing repository gained a commit: %s", out)
	}
}

func TestInitLocalSpaceRepoRefusesBadInput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	good := localInitFiles()
	cases := []struct {
		name  string
		dir   string
		files []FileWrite
		want  error
	}{
		{"no files", "x", nil, ErrMutationInvalid},
		{"no dir", "", good, ErrMutationInvalid},
		{"traversal", "x", []FileWrite{{Path: "../escape", Content: []byte("x")}}, ErrMutationInvalid},
		{"absolute", "x", []FileWrite{{Path: "/etc/passwd", Content: []byte("x")}}, ErrMutationInvalid},
		{"duplicate", "x", []FileWrite{{Path: "a", Content: []byte("1")}, {Path: "a", Content: []byte("2")}}, ErrMutationDuplicatePath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := tc.dir
			if dir == "x" {
				dir = filepath.Join(t.TempDir(), "never.git")
			}
			_, err := InitLocalSpaceRepo(ctx, dir, tc.files)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if dir != "" {
				if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
					t.Fatalf("a refused input still created %s (stat err %v)", dir, statErr)
				}
			}
		})
	}
}

func TestReadLocalSpaceRepoReportsOwnRootCommit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "demo.git")
	if got, err := ReadLocalSpaceRepo(ctx, dir); err != nil || got != (LocalSpaceRepoState{}) {
		t.Fatalf("missing dir: %+v, %v; want the zero state", got, err)
	}
	if _, err := InitLocalSpaceRepo(ctx, dir, []FileWrite{
		{Path: "space.yaml", Content: []byte("space: demo\nparticipants: []\n")},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLocalSpaceRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := (LocalSpaceRepoState{IsBareRepo: true, SpaceID: "demo", RootOnly: true}); got != want {
		t.Fatalf("state = %+v, want %+v", got, want)
	}
}

func TestReadLocalSpaceRepoSeesALandedCommitAsNotRootOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "demo.git")
	root, err := InitLocalSpaceRepo(ctx, dir, localInitFiles())
	if err != nil {
		t.Fatal(err)
	}
	tree := localInitGit(t, dir, "rev-parse", root+"^{tree}")
	child, err := gitPlumbingOutput(ctx, dir, []string{
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	}, []byte("second"), maxTreeCommitMetadataBytes, "--git-dir="+dir, "commit-tree", tree, "-p", root, "-F", "-")
	if err != nil {
		t.Fatal(err)
	}
	localInitGit(t, dir, "update-ref", "refs/heads/main", child, root)
	got, err := ReadLocalSpaceRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsBareRepo || got.SpaceID != "demo" || got.RootOnly {
		t.Fatalf("state = %+v, want a bare repo declaring its id with RootOnly false", got)
	}
}

func TestReadLocalSpaceRepoReadsZeroForForeignDirectories(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("plain directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte("space: demo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadLocalSpaceRepo(ctx, dir); err != nil || got != (LocalSpaceRepoState{}) {
			t.Fatalf("state = %+v, %v; want zero", got, err)
		}
	})
	t.Run("directory inside another repository", func(t *testing.T) {
		t.Parallel()
		outer := t.TempDir()
		if out, err := exec.Command("git", "init", "--quiet", outer).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		inner := filepath.Join(outer, "sub")
		if err := os.MkdirAll(inner, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadLocalSpaceRepo(ctx, inner); err != nil || got.IsBareRepo {
			t.Fatalf("state = %+v, %v; a subdirectory of a work tree is not a bare repository", got, err)
		}
	})
	t.Run("bare repository without a base branch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if out, err := exec.Command("git", "init", "--bare", "--quiet", dir).CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		got, err := ReadLocalSpaceRepo(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if want := (LocalSpaceRepoState{IsBareRepo: true}); got != want {
			t.Fatalf("state = %+v, want %+v", got, want)
		}
	})
	t.Run("empty directory argument", func(t *testing.T) {
		t.Parallel()
		if _, err := ReadLocalSpaceRepo(ctx, ""); !errors.Is(err, ErrMutationInvalid) {
			t.Fatalf("err = %v, want ErrMutationInvalid", err)
		}
	})
}
