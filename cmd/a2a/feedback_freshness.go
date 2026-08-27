package main

// feedback_freshness.go resolves AC3's three-valued freshness verdict for
// `a2a feedback triage` (docs/features/active/agent-exchange-2026-08/specs/
// 09-own-loop.md §8 AC3, §11 wave D2): triage may only print "inbox clean"
// after resolving the hub of record as CURRENT. Resolution happens HERE —
// the only place `runFeedback` may fetch, shell out, or reach a remote for
// this purpose — and the already-resolved feedback.Freshness value is
// passed into internal/cli.NewFeedbackCommand, which passes it into
// internal/feedback.Triage; neither of those two ever reaches a remote
// themselves.
//
// This file is this concern's single subprocess-launch seam, mirroring
// internal/lane/fsread.go's own convention (see that file's header): every
// `git` invocation this resolution needs — rev-parse, fetch, ls-tree,
// cat-file — lives HERE and nowhere else in cmd/a2a, so a gosec G204
// ("subprocess launched with variable") finding lands on exactly this one
// file's allowlist entry rather than being spread across call sites. The
// argv is fixed (`git` resolved via exec.CommandContext's normal PATH
// lookup, never a shell); hubRoot and hubURL are the only variable
// arguments, and neither is attacker-controlled — hubRoot is this process's
// own cwd, hubURL is an operator-configured env var or the hardcoded
// canonicalFeedbackRepo constant.
//
// DEVIATION (reported, not silently worked around): `.golangci.yml`'s G204
// exact-file allowlist does not yet include this file, and .golangci.yml is
// outside this wave's allowlist. The lead needs to add
// `cmd/a2a/feedback_freshness.go` to that allowlist's G204 entry (same
// rationale as the internal/lane/fsread.go row) before `make lint`/`make
// check` will pass clean on this file.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/feedback"
)

// feedbackHubURLEnv is the override docs/runbooks/feedback-sync.sh itself
// uses for its own git-level pull — deliberately NOT A2A_FEEDBACK_REPO,
// which parseGitHubRepo mis-slices for a local-path hub and which the
// status HubReader resolves through raw.githubusercontent.com, never git
// (own-loop 09 wave D brief).
const feedbackHubURLEnv = "A2A_FEEDBACK_HUB_URL"

// feedbackInboxRecordPattern is feedback-sync.sh's own INBOX_PATTERN,
// mirrored rather than re-derived: a hub tree path outside this grammar
// (the `.gitkeep` sentinel, a stray file) is the sync's business to refuse,
// never triage's (§11 wave D2 correction).
var feedbackInboxRecordPattern = regexp.MustCompile(`^feedback/inbox/fb-[0-9]{8}-[0-9a-f]{6}\.yaml$`)

// resolveFeedbackFreshness resolves AC3's freshness verdict for hubRoot
// against hubURL. It never panics; every failure mode becomes an explicit
// feedback.Freshness{Status: feedback.FreshnessRefused} naming a reason and
// (when known) the hub of record — the fail-closed shape AC3 requires.
//
// Case (a) — content-level, not commit-level (§11 wave D2 correction): the
// hub's default branch carries a feedback/inbox/fb-*.yaml record this tree
// lacks at HEAD. Case (b): the fetch/list comparison could not be
// completed (unreachable hub, fetch error, missing branch — folded into one
// git-fetch failure branch, since git reports a missing `main` the same way
// it reports an unreachable remote). Case (c): hubURL is empty, so there is
// no hub of record to check against at all.
func resolveFeedbackFreshness(ctx context.Context, hubRoot, hubURL string) feedback.Freshness {
	inWorkTree, wtErr := feedbackHubIsGitWorkTree(ctx, hubRoot)
	if wtErr != nil {
		return feedback.Freshness{
			Status:      feedback.FreshnessRefused,
			HubOfRecord: hubURL,
			Reason:      fmt.Sprintf("could not determine whether %s is a git work tree: %v", hubRoot, wtErr),
		}
	}
	if !inWorkTree {
		// AC3: "Only a hubRoot that is NOT a git work tree yields
		// not-applicable, from which clean is reachable."
		return feedback.Freshness{Status: feedback.FreshnessNotApplicable}
	}

	if hubURL == "" {
		// Case (c). Structurally unreachable through runFeedback today —
		// A2A_FEEDBACK_HUB_URL always falls back to the hardcoded
		// canonicalFeedbackRepo constant there — but this function must
		// still refuse rather than silently treat a blank hub as current
		// (§11 wave D2).
		return feedback.Freshness{
			Status: feedback.FreshnessRefused,
			Reason: "no hub-of-record remote is configured (A2A_FEEDBACK_HUB_URL is unset and no fallback hub was supplied)",
		}
	}

	if err := feedbackHubURLIsFetchable(hubURL); err != nil {
		// Refused before the value reaches an argv. `git fetch` reads
		// `<helper>::<arg>` as a transport helper, and hubURL arrives here
		// from A2A_FEEDBACK_HUB_URL — an environment variable.
		//
		// Measured, so the claim is the right size: with this guard disabled,
		// git ITSELF refuses `ext::sh -c whoami` with "transport 'ext' not
		// allowed", because `protocol.ext.allow` defaults to none. So this is
		// a SECOND line, not the only one, and saying otherwise would be the
		// kind of overclaim this epic keeps finding in its own comments.
		//
		// It is still worth having, for two reasons that do not depend on
		// git's default: that default is configuration, and configuration is
		// exactly what an environment can change; and a refusal that names
		// the transport-helper syntax tells an operator what they typed,
		// where git's own message tells them about a transport they never
		// meant to use.
		return feedback.Freshness{
			Status:      feedback.FreshnessRefused,
			HubOfRecord: hubURL,
			Reason:      fmt.Sprintf("hub of record is not a fetchable location: %v", err),
		}
	}

	if err := feedbackFetchHubInbox(ctx, hubRoot, hubURL); err != nil {
		// Case (b): remote unreachable, fetch error, or missing branch —
		// git reports all three as a non-zero `git fetch` exit.
		return feedback.Freshness{
			Status:      feedback.FreshnessRefused,
			HubOfRecord: hubURL,
			Reason:      fmt.Sprintf("could not complete the hub-of-record comparison: %v", err),
		}
	}

	hubOnlyPath, err := feedbackHubOnlyInboxPath(ctx, hubRoot)
	if err != nil {
		return feedback.Freshness{
			Status:      feedback.FreshnessRefused,
			HubOfRecord: hubURL,
			Reason:      fmt.Sprintf("could not complete the hub-of-record comparison: %v", err),
		}
	}
	if hubOnlyPath != "" {
		// Case (a).
		return feedback.Freshness{
			Status:      feedback.FreshnessRefused,
			HubOfRecord: hubURL,
			Reason:      fmt.Sprintf("hub's default branch carries %s, which this tree lacks", hubOnlyPath),
		}
	}

	return feedback.Freshness{Status: feedback.FreshnessCurrent}
}

// feedbackHubIsGitWorkTree reports whether hubRoot is inside a git work
// tree, distinguishing "git ran and said no" (ok=false, err=nil — a
// filesystem fact, independent of whether git is installed) from "git could
// not be asked at all" (err != nil — e.g. the git binary is missing), so
// the latter is never silently folded into FreshnessNotApplicable.
func feedbackHubIsGitWorkTree(ctx context.Context, hubRoot string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", hubRoot, "rev-parse", "--is-inside-work-tree")
	var execErr *exec.Error
	out, err := cmd.Output()
	if err != nil {
		if errors.As(err, &execErr) {
			return false, err
		}
		// A non-zero exit here means git ran and reported "not a git
		// repository" — a real, checked "no", not a failure to check.
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

// feedbackFetchHubInbox runs the exact `git fetch` docs/runbooks/
// feedback-sync.sh's sync_feedback uses, so the two consumers of "can this
// hub be reached" share one proven argv rather than drifting (§11 wave D2).
// The branch is feedbackBaseBranch (wire.go), NOT the literal "main" it was
// until P15 M2. This site is worth a sentence because the spec's own
// enumeration missed it: G3 counted READS OF the space base-branch setting
// (a constant at the time, since deleted by no-silent-yes-2026-08 P2b — a
// space's base branch is now DERIVED per space instead) and found six, and
// this one hard-coded the string instead — so a repoint of that setting
// alone would have left `a2a feedback triage` computing its freshness
// verdict against a branch that no longer receives reports, and printing
// "inbox clean" on the strength of it. A hand-maintained enumeration of call
// sites has the same failure mode as any other hand-maintained list.
func feedbackFetchHubInbox(ctx context.Context, hubRoot, hubURL string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", hubRoot, "fetch", "--no-tags", "--depth=1", hubURL, feedbackBaseBranch)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return errors.New(detail)
	}
	return nil
}

// feedbackHubOnlyInboxPath returns the first feedback/inbox/fb-*.yaml path
// FETCH_HEAD (the hub's default branch, already fetched by
// feedbackFetchHubInbox) carries that this tree's HEAD lacks — "" if none.
// This is deliberately presence/absence only (§11 wave D2 correction: "a
// record present here and differing is NOT case (a)") — content comparison
// is docs/runbooks/feedback-sync.sh's merge rule to make, not triage's.
func feedbackHubOnlyInboxPath(ctx context.Context, hubRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", hubRoot, "ls-tree", "-r", "--name-only", "FETCH_HEAD", "--", "feedback/inbox")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, path := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if path == "" || !feedbackInboxRecordPattern.MatchString(path) {
			continue
		}
		checkCmd := exec.CommandContext(ctx, "git", "-C", hubRoot, "cat-file", "-e", "HEAD:"+path)
		if err := checkCmd.Run(); err != nil {
			// The error IS the answer, which is why nilerr is silenced here
			// rather than obeyed: `cat-file -e` exits non-zero precisely when
			// HEAD does not carry the path, and "HEAD does not carry this
			// hub record" is exactly the hub-only drift AC3 case (a) asks
			// about. Returning the error instead would report a fetch
			// failure for a successful comparison.
			return path, nil //nolint:nilerr // reason: a non-zero `git cat-file -e` means the path is absent from HEAD — the signal this function exists to detect, not a failure.
		}
	}
	return "", nil
}

// feedbackHubURLIsFetchable rejects any hub location that is not plainly an
// HTTPS/SSH URL or an absolute local path. The narrowness is the point: `git
// fetch` treats `<helper>::<argument>` as a transport helper and will EXECUTE
// it (`ext::sh -c …` is the canonical example), so a permissive check here
// would turn A2A_FEEDBACK_HUB_URL into a command channel. Everything this
// repository actually points a hub at — the canonical https remote, and the
// local fixture directories --teeth and the tests use — passes.
func feedbackHubURLIsFetchable(hubURL string) error {
	if strings.Contains(hubURL, "::") {
		return errors.New("`::` is git's transport-helper syntax, which executes its argument; refusing to pass it to `git fetch`")
	}
	switch {
	case strings.HasPrefix(hubURL, "https://"), strings.HasPrefix(hubURL, "http://"):
		return nil
	case strings.HasPrefix(hubURL, "git@"):
		return nil
	case filepath.IsAbs(hubURL):
		return nil
	}
	return fmt.Errorf("expected an https/ssh URL or an absolute local path, got %q", hubURL)
}
