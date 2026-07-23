package space

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneOrFetch establishes or refreshes a plain mirror clone of repoURL at
// dir (§7.4): clones if dir is absent or empty; fetches (never re-clones)
// if dir already holds a git repo. A dir that exists, is non-empty, and is
// NOT a git repository is a typed error (ErrNonGitTarget) — this function
// never overwrites unrelated content. Git plumbing is os/exec with
// explicit argv (never sh -c); no network happens in this package's own
// tests (testkit/spacefixture's bare-repo fixtures are local paths).
func CloneOrFetch(ctx context.Context, dir, repoURL string) error {
	const op = "CloneOrFetch"

	empty, err := dirIsEmptyOrAbsent(dir)
	if err != nil {
		return &Error{Op: op, Input: dir, Err: err}
	}

	if empty {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return &Error{Op: op, Input: dir, Err: err}
		}
		if err := runGit(ctx, "", "clone", repoURL, dir); err != nil {
			return &Error{Op: op, Input: dir, Err: err}
		}
		return nil
	}

	if !isGitRepo(dir) {
		return &Error{Op: op, Input: dir, Err: ErrNonGitTarget}
	}
	if err := runGit(ctx, dir, "fetch", "origin"); err != nil {
		return &Error{Op: op, Input: dir, Err: err}
	}
	if err := checkoutRemoteHead(ctx, dir); err != nil {
		return &Error{Op: op, Input: dir, Err: err}
	}
	return nil
}

// checkoutRemoteHead moves the mirror's WORKING TREE onto the freshly
// fetched remote head.
//
// Fetching alone updates refs and nothing else, and every read in the
// product — the cache walks the mirror DIRECTORY, and so does every
// precondition (`contract retire`'s consumer registry among them) — reads
// files, not refs. Without this step a mirror showed whatever it held at
// clone time forever: another system's merged artifact never appeared, and
// after this system's own write the tree was left standing on the funnel's
// ephemeral branch (commitOne checks it out and never leaves), pinning the
// whole read surface to a branch instead of the space.
//
// A mirror is a cache, so the move is unconditional: nothing in it is
// authored by hand, and the write funnel commits and pushes within a single
// invocation rather than leaving work parked here.
func checkoutRemoteHead(ctx context.Context, dir string) error {
	branch := remoteHeadBranch(ctx, dir)
	if err := runGit(ctx, dir, "checkout", "-B", branch, "origin/"+branch); err != nil {
		return err
	}
	return runGit(ctx, dir, "reset", "--hard", "origin/"+branch)
}

// remoteHeadBranch resolves the remote's default branch, falling back to
// §4.2's normative "main" when the remote publishes no HEAD (a bare fixture
// origin often does not).
func remoteHeadBranch(ctx context.Context, dir string) string {
	ref, err := runGitOutput(ctx, dir, nil, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		if _, branch, found := cutLast(ref, "/"); found && branch != "" {
			return branch
		}
	}
	return "main"
}

// cutLast splits s at its LAST occurrence of sep.
func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func dirIsEmptyOrAbsent(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}

func isGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && (info.IsDir() || info.Mode().IsRegular())
}

// runGit runs `git <args...>` with cwd=dir (dir=="" uses the process's
// own cwd) via explicit argv, returning the combined output on failure.
func runGit(ctx context.Context, dir string, args ...string) error {
	_, err := runGitOutput(ctx, dir, nil, args...)
	return err
}

// runGitOutput runs `git <args...>` with cwd=dir and optional extraEnv,
// returning trimmed stdout on success. Explicit argv only, never sh -c.
func runGitOutput(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, stderr.String())
	}
	return string(bytes.TrimSpace(out.Bytes())), nil
}
