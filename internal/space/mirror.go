package space

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	return nil
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
