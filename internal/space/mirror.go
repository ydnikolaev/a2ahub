package space

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// indexLockPollInterval is how long runGitRetryLocked sleeps between
// attempts while another process holds a mirror's git index.lock. Short
// enough that a competing checkout+reset — which on this project's
// mirrors completes in tens of milliseconds on local disk — is caught on
// the very next poll; long enough not to turn "wait for a lock" into a
// busy-spin.
const indexLockPollInterval = 100 * time.Millisecond

// indexLockWaitBudget bounds the TOTAL time checkoutRemoteHead will wait
// across BOTH its git calls (checkout, then reset — the deadline is
// computed once and shared) for a contending process's index.lock to
// clear before giving up. internal/cache's SyncIfStale gives the whole
// CloneOrFetch call (fetch, which runs first, PLUS this wait) a single
// perMirrorTimeout of 5s (internal/cache/refresh.go) so a stale lock
// degrades a read verb like `a2a inbox` into "returns a named
// contention error promptly", never "hangs until the caller's own outer
// timeout fires" — 2s leaves at least 3s of that budget for the network
// fetch and branch resolution that run before this wait even starts,
// while comfortably outlasting a normal concurrent checkout+reset.
const indexLockWaitBudget = 2 * time.Second

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

	// The refresh holds the SAME mirror lock the write funnel holds, for the
	// same reason: `checkoutRemoteHead` below runs `reset --hard`, and a
	// mirror is shared by every project on this machine that connects the
	// space (mirror_root is a machine-level setting). Without this, a read
	// verb refreshing a stale mirror can reset the working tree out from under
	// a concurrent `a2a submit` between the moment it writes its files and the
	// moment it commits them — the same crossed/lost write the funnel's own
	// lock exists to prevent, arriving from the reader's side instead.
	//
	// The degradation is deliberately asymmetric and correct: a reader that
	// cannot get the lock within the budget returns a named contention error,
	// and internal/cache's SyncIfStale treats a refresh failure as non-fatal —
	// so a read during a write shows slightly stale data with a note, which is
	// strictly better than corrupting the write. No re-entrancy: Submit never
	// calls CloneOrFetch (the caller runs it first, then submits), so the two
	// lock holders are always sequential.
	lock, err := AcquireMirrorLock(ctx, dir)
	if err != nil {
		return &Error{Op: op, Input: dir, Err: err}
	}
	defer func() { _ = lock.Release() }()

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
//
// The mirror directory is SHARED across processes by construction
// (mirror_root puts every project's clone of a space in one place), and
// this step is the only one that mutates the working tree — so it is the
// only one that can collide with a second process's own concurrent
// checkout/reset on one of git's OWN lock files. `checkout -B` alone can
// take index.lock (the index itself), HEAD.lock and refs/heads/<b>.lock
// (moving the branch ref), and config.lock (writing the upstream-tracking
// config) depending on timing — this is not one lock but git's general
// lock-then-rename-into-place convention applied to several files.
// Neither git call below is retried blindly: runGitRetryLocked only
// retries when the failure is one of THOSE locks (see
// isLockContention's doc comment for how that is detected WITHOUT
// matching a locale-translated die() sentence), and only within one
// shared indexLockWaitBudget for the pair, so a second process WAITS
// instead of losing its write outright (the live-e2e concurrent-writers
// regression).
func checkoutRemoteHead(ctx context.Context, dir string) error {
	branch := remoteHeadBranch(ctx, dir)
	deadline := time.Now().Add(indexLockWaitBudget)
	if err := runGitRetryLocked(ctx, dir, deadline, "checkout", "-B", branch, "origin/"+branch); err != nil {
		return err
	}
	return runGitRetryLocked(ctx, dir, deadline, "reset", "--hard", "origin/"+branch)
}

// runGitRetryLocked runs `git <args...>` with cwd=dir, retrying ONLY when
// the failure is contention on one of git's own `*.lock` files (another
// process's concurrent checkout/reset in the same mirror), until either
// the call succeeds, deadline passes, or ctx is cancelled/times out —
// whichever comes first.
//
// A failure that is NOT lock contention (a genuinely broken remote ref, a
// corrupt repo, ...) is returned immediately, unretried — retrying it
// would only slow down a real error, and it is not the failure mode this
// function exists to smooth over.
//
// ctx is checked at the TOP of every iteration, not only while sleeping:
// a git subprocess killed mid-run by ctx expiring can itself surface as a
// failure that isLockContention would otherwise have to classify —
// checking first means a cancelled/expired ctx always wins that race.
func runGitRetryLocked(ctx context.Context, dir string, deadline time.Time, args ...string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := runGitOutput(ctx, dir, []string{"LC_ALL=C"}, args...)
		if err == nil {
			return nil
		}
		if !isLockContention(dir, err) {
			return err
		}
		if !time.Now().Before(deadline) {
			// Both sentinels are wrapped: a caller may want to recognise the
			// contention (ErrMirrorLocked) OR the underlying git failure, and
			// dropping either to %v makes errors.Is blind to it.
			return fmt.Errorf("%w: waited %s for a git lock file to clear (git %v): %w",
				ErrMirrorLocked, indexLockWaitBudget, args, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jitteredPollInterval()):
		}
	}
}

// isLockContention reports whether a failed checkout/reset attempt was
// caused by one of git's own `*.lock` files, checked two ways — either
// is sufficient:
//
//  1. The failed attempt's stderr (folded into err by runGitOutput)
//     names a lock collision. git's own die() prose is translated under
//     a non-C locale — "Unable to create '<path>': File exists."
//     becomes different wording per locale, "cannot lock ref '<ref>'"
//     has its own separate translation, and the `checkout -B` upstream-
//     tracking step's own config-lock failure doesn't even print a
//     ".lock"-suffixed path in its message ("could not lock config file
//     .git/config: File exists") — so neither ".lock"-path matching nor
//     die()-sentence matching alone covers every one of git's own lock
//     files this pair of commands can collide on. What IS stable across
//     every one of those cases is the trailing OS errno text for
//     EEXIST, "File exists" — and runGitRetryLocked forces LC_ALL=C on
//     the git subprocess it runs (via runGitOutput's extraEnv)
//     specifically so that text is guaranteed English regardless of the
//     ambient locale, rather than relying on git's own high-level
//     phrasing around it. False positives are not a concern here: this
//     check is scoped to exactly two argv shapes (`checkout -B ...`,
//     `reset --hard ...`) against an unconditional mirror clone, neither
//     of which has another legitimate "File exists" failure mode.
//  2. One of git's known lock files exists RIGHT NOW (gitLockHeld):
//     covers the case where the holder is still mid-checkout — a
//     pre-emptive signal a pure post-failure text check would miss.
//
// Checking (1) is what closes the race a pure post-failure file-exists
// probe cannot: the holder frequently finishes and unlinks its lock file
// in the gap between this process's own failed git subprocess exiting
// and any check performed afterwards (fork/exec/wait teardown is tens of
// milliseconds on darwin — comparable to the holder's entire
// checkout+reset) — a file-existence-only probe reports "not locked" for
// a failure that unambiguously WAS lock contention, and the raw git
// error would escape unretried. See this phase's Deviations report.
func isLockContention(dir string, err error) bool {
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, ".lock") || strings.Contains(msg, "File exists") {
			return true
		}
	}
	return gitLockHeld(dir)
}

// jitteredPollInterval returns indexLockPollInterval +/- 50% jitter.
// Without jitter, N contenders that all failed on the same lock at
// (near-)the same instant would all sleep the identical interval and
// wake in lockstep, re-colliding on the SAME retry attempt over and
// over (a thundering herd) — spreading the wake-ups avoids that.
func jitteredPollInterval() time.Duration {
	half := indexLockPollInterval / 2
	//nolint:gosec // reason: G404 — this jitter spreads retry wake-ups so N
	// contenders do not re-collide in lockstep. It decides nothing an attacker
	// could exploit by predicting it (there is no secret, no token, no
	// ordering guarantee derived from it), and crypto/rand here would add an
	// error path to a sleep duration for no gain. math/rand/v2 is the correct
	// tool for scheduling jitter.
	return half + rand.N(indexLockPollInterval)
}

// gitLockHeld reports whether dir's git directory currently holds any of
// the specific lock files checkoutRemoteHead's own two git calls are
// known to take: the index, HEAD, the branch's own ref, or the repo
// config. One of the two signals isLockContention checks — see its doc
// comment for why a post-failure check on this alone is not sufficient
// by itself.
func gitLockHeld(dir string) bool {
	gitDir, err := resolveGitDir(dir)
	if err != nil {
		return false
	}
	for _, name := range []string{"index.lock", "HEAD.lock", "config.lock"} {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(gitDir, "refs", "heads", "*.lock"))
	return len(matches) > 0
}

// resolveGitDir returns the git directory backing the repository at dir.
//
// Every mirror this package creates is a plain `git clone` (never a
// linked worktree or a submodule checkout), so in practice this is always
// dir/.git as a directory — verified against this repo: nothing under
// internal/space or internal/host runs `git worktree` or checks out a
// submodule. isGitRepo (below) already tolerates dir/.git being a
// regular FILE too (the linked-worktree/submodule form, "gitdir: <path>"
// content), so this resolves that form as well rather than assuming the
// directory case and leaving index.lock detection silently unable to
// find the real git dir if that assumption ever stops holding.
func resolveGitDir(dir string) (string, error) {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return dotGit, nil
	}
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return "", err
	}
	const prefix = "gitdir: "
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("space: %s: unrecognized .git file contents", dotGit)
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	return gitDir, nil
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
