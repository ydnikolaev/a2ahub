package space

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// mirrorLockFileName is the advisory lock MirrorLock creates INSIDE a
// mirror's own git directory (resolveGitDir(dir), never the working tree
// itself) — it travels with the mirror, and it can never collide with the
// space's own validated content: space.IsInfrastructurePath and the write
// funnel's own section guard (sectionOK) both only ever see paths relative
// to the WORKING TREE, and .git/ is never inside that surface.
const mirrorLockFileName = "a2a-mirror.lock"

// mirrorLockStaleAfter bounds how long a lock's own recorded acquisition
// timestamp is trusted before a contender takes it over instead of waiting
// on it forever.
//
// It must be comfortably larger than any LEGITIMATE hold: the write
// funnel's own mutate-commit-push span is a handful of local git
// operations (milliseconds) plus one network push — normally well under a
// few seconds, occasionally longer under real GitHub latency/contention.
// Two minutes gives that a wide margin without leaving a crashed holder's
// mirror unusable for anywhere near "forever".
//
// Cost of a crash: every Submit against this mirror from a DIFFERENT
// process, for up to mirrorLockStaleAfter after the crash, waits out
// mirrorLockWaitBudget and then fails closed with ErrMirrorLocked (whose
// own doc already says the safe next move is to re-run) — the write funnel
// is idempotent by head branch, so a re-run never duplicates work. The
// FIRST attempt after mirrorLockStaleAfter elapses finds the lock stale,
// takes it over, and proceeds normally. So the crashed holder's cost to
// the next writer is bounded by mirrorLockStaleAfter of refused (not
// silently wrong) attempts, never a deadlock.
const mirrorLockStaleAfter = 2 * time.Minute

// mirrorLockWaitBudget bounds how long AcquireMirrorLock waits for a LIVE
// (not-yet-stale) holder to release before giving up with ErrMirrorLocked.
// It intentionally does NOT wait out mirrorLockStaleAfter: a slow-but-alive
// holder is expected to finish well inside this budget (see
// mirrorLockStaleAfter's doc), and a genuinely stuck one is recovered by
// staleness, not by every contender blocking for the full two minutes.
//
// This budget composes with (does not stack against) the *inner*
// index.lock retry runGitRetryLocked performs inside CloneOrFetch: nothing
// in this package acquires the mirror lock and then calls CloneOrFetch —
// AcquireMirrorLock is used by WriteFunnel.Submit only (see its own doc),
// and Submit never calls CloneOrFetch — so the two budgets never nest
// inside a single caller's stack and cannot multiply into a longer stall.
// A future caller that DOES hold the mirror lock across a CloneOrFetch
// call would, in the worst case, add indexLockWaitBudget (2s) on top of
// this one, not multiply it — see mirror.go's own git-lock retry, which is
// bounded per-call, not per-poll-of-this-lock.
const mirrorLockWaitBudget = 5 * time.Second

// mirrorLockPayload is the lock file's content: who holds it and when they
// acquired it. This is enough to recognise a stale holder (crash recovery)
// without relying on pid liveness — pids are reused, and checking a pid's
// liveness portably across darwin/linux/windows is a can of worms this
// project does not need to open for what is, in the end, a "is this
// timestamp old" check.
type mirrorLockPayload struct {
	PID        int       `json:"pid"`
	AcquiredAt time.Time `json:"acquired_at"`
}

// MirrorLock is a held advisory lock on one mirror directory, returned by
// AcquireMirrorLock. Release is idempotent and safe to call more than
// once (directly, then again via a deferred safety-net call) or never
// (a caller that already released explicitly need not defer as well,
// though the write funnel does both — see Submit's own doc).
type MirrorLock struct {
	path    string
	payload mirrorLockPayload

	releaseOnce sync.Once
	releaseErr  error
}

// AcquireMirrorLock acquires the advisory per-mirror-directory lock for
// dir (a mirror's WORKING TREE root — same argument shape as
// CloneOrFetch/resolveGitDir), waiting out contention from another LIVE
// holder up to mirrorLockWaitBudget and honouring ctx cancellation while
// it waits. A holder whose recorded acquisition is older than
// mirrorLockStaleAfter is taken over rather than waited on.
//
// dir must already be a git repository (resolveGitDir(dir) must resolve) —
// AcquireMirrorLock does not create mirrors, it locks an existing one; a
// caller locking a not-yet-cloned destination has nothing under .git/ to
// hold the lock file in anyway.
//
// Returns ErrMirrorLocked (wrapped, so errors.Is sees it) if the wait
// budget expires against a still-live holder. Returns ctx.Err() unwrapped
// if ctx is cancelled/expires first — a caller that wants to tell "gave up
// waiting" apart from "was cancelled" can already do so via errors.Is
// against each.
func AcquireMirrorLock(ctx context.Context, dir string) (*MirrorLock, error) {
	const op = "AcquireMirrorLock"

	gitDir, err := resolveGitDir(dir)
	if err != nil {
		return nil, &Error{Op: op, Input: dir, Err: err}
	}
	lockPath := filepath.Join(gitDir, mirrorLockFileName)
	deadline := time.Now().Add(mirrorLockWaitBudget)

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		lock, acquired, err := tryCreateMirrorLock(lockPath)
		if err != nil {
			return nil, &Error{Op: op, Input: dir, Err: err}
		}
		if acquired {
			return lock, nil
		}

		// Not acquired: a lock file is present. If it is stale, remove it
		// and loop back to RACE for it again via O_EXCL — never assume
		// removing a stale file means WE now hold it, or two contenders
		// that both see the same stale lock would both believe they
		// acquired it.
		tookOver, staleErr := takeOverIfStale(lockPath)
		if staleErr != nil {
			return nil, &Error{Op: op, Input: dir, Err: staleErr}
		}
		if tookOver {
			continue
		}

		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w: waited %s for the mirror lock at %s to release",
				ErrMirrorLocked, mirrorLockWaitBudget, lockPath)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(jitteredPollInterval()):
		}
	}
}

// tryCreateMirrorLock attempts to create path fully populated with a
// payload in one atomic step — the portable (darwin/linux/windows,
// amd64/arm64) "only one creator wins" primitive this lock is built on;
// syscall.Flock is unix-only and is deliberately not used here.
// acquired=false, err=nil means the file already exists (ordinary
// contention, not a failure).
//
// This is a temp-file-then-hardlink, NOT a plain O_CREATE|O_EXCL open
// followed by a write. An earlier version of this function did exactly
// that and had a real, reproduced race: os.OpenFile(O_CREATE|O_EXCL)
// creates a ZERO-LENGTH file the instant the syscall returns, and the
// payload write is a separate step after it — so a concurrent contender's
// takeOverIfStale could observe path between those two steps, see an
// empty/unparseable payload, treat it as a crashed holder (see that
// function's own "unreadable payload = stale" fallback), and take over a
// lock its rightful holder was still in the middle of acquiring. Both
// contenders then believed they held the lock, and the git-level race this
// whole mechanism exists to close reappeared underneath it (observed
// directly: TestFunnelConcurrentSubmitsOneMirrorNoCrossedWrite failed with
// git's own "Unable to create index.lock: File exists" — TWO real git
// processes running against req.RepoDir at once). Writing the payload to a
// SEPARATE temp file in the same directory (same filesystem, so the
// hardlink below cannot cross a volume boundary) and then hardlinking it
// into place means path never exists with anything other than its full,
// valid payload — there is no window left for a reader to observe.
//
// Known limitation, deliberately accepted: a crash between os.CreateTemp
// succeeding and the deferred os.Remove running leaves an orphaned
// ".../a2a-mirror.lock.tmp-*" file behind — nothing reaps it. It is inert
// (nothing ever reads or links from an existing tmp file; each attempt
// creates its own via the "*" pattern) and lives inside .git/, so it never
// reaches the validated working tree or the section guard. Left as a
// report item rather than fixed here (e.g. via a reaper) — out of this
// brief's scope, and .git/ already accumulates its own transient files
// (packed-refs.lock and friends) that nothing in this codebase reaps
// either.
func tryCreateMirrorLock(path string) (*MirrorLock, bool, error) {
	payload := mirrorLockPayload{PID: os.Getpid(), AcquiredAt: time.Now().UTC()}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), mirrorLockFileName+".tmp-*")
	if err != nil {
		return nil, false, err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // reason: best-effort cleanup of the staging file — the hardlink below is what actually publishes the lock, this file is scratch either way

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return nil, false, err
	}
	if err := tmp.Close(); err != nil {
		return nil, false, err
	}

	if err := os.Link(tmpPath, path); err != nil {
		if os.IsExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &MirrorLock{path: path, payload: payload}, true, nil
}

// takeOverIfStale reports whether the lock file at path was stale (its
// recorded AcquiredAt older than mirrorLockStaleAfter, or its payload
// unreadable — a holder that crashed mid-write of its own lock file can
// never release it, which is exactly the crash case this exists to
// recover) and, if so, removes it. It does NOT create a new lock in its
// place — the caller loops back to tryCreateMirrorLock to re-race for it,
// so two contenders that both observe the same stale file cannot both
// believe they now hold it.
func takeOverIfStale(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Released between our failed create and this read — not
			// stale, just gone; the caller's loop will re-race for it.
			return false, nil
		}
		return false, err
	}

	var payload mirrorLockPayload
	stale := true
	if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr == nil {
		stale = time.Since(payload.AcquiredAt) > mirrorLockStaleAfter
	}
	if !stale {
		return false, nil
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			// Someone else already took it over between our stat and our
			// remove — not an error, just lost the race to reclaim it.
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Release releases the lock. It is idempotent (safe to call more than
// once) and — the correctness-critical part — a compare-and-delete: it
// only removes the lock file if it STILL holds exactly the payload this
// MirrorLock wrote. If a holder is slow enough that mirrorLockStaleAfter
// elapses and a different contender takes the lock over (see
// takeOverIfStale), this holder's eventual Release must NOT delete the
// NEW legitimate holder's lock file out from under it — a plain
// unconditional os.Remove would do exactly that.
func (l *MirrorLock) Release() error {
	l.releaseOnce.Do(func() {
		raw, err := os.ReadFile(l.path)
		if err != nil {
			if os.IsNotExist(err) {
				// Already gone (taken over as stale, or double-released) —
				// nothing to do.
				return
			}
			l.releaseErr = err
			return
		}
		var current mirrorLockPayload
		if err := json.Unmarshal(raw, &current); err != nil || current != l.payload {
			// Not ours anymore (superseded by a stale-takeover) — leave it
			// for its real owner.
			return
		}
		if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
			l.releaseErr = err
		}
	})
	return l.releaseErr
}
