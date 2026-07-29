//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// The refusal family — spec 36 plan Wave-3 adjudication rows 9, 16, 17: the
// three cells where the product itself must say no, observed against the
// real space. Every helper here is prefixed "refusal" so this file cannot
// collide with the three sibling families' own namespaces (plan §"the
// fan-out seam is four functions, not a registry").
const (
	refusalScenarioDirectPushRefused        = "direct-push-refused"
	refusalScenarioOutOfSectionWriteRefused = "out-of-section-write-refused"
	refusalScenarioStaleWriteFloorRefused   = "stale-write-floor-refused"
)

// runRefusalScenarios is this family's one entry point (plan §"the fan-out
// seam is four functions, not a registry"). Every row is System B — the
// non-admin participant — because a refusal authored by an admin proves
// nothing (spec 36 §T5).
func runRefusalScenarios(ctx context.Context, h *harness) []Result {
	return []Result{
		refusalDirectPushScenario(ctx, h),
		refusalOutOfSectionWriteScenario(ctx, h),
		mcpOutOfSectionRefused(ctx, h),
		refusalStaleWriteFloorScenario(ctx, h),
	}
}

// refusalCouldNotDrive folds an incidental setup/read failure into a
// Result, using verdictForError's own rule first (an exhausted 5xx retry or
// a bounded-wait timeout is never a defect) and falling back to
// VerdictRefused — "we could not construct this cell" — for anything else,
// per this wave's explicit guidance: omit, or better, refuse with a reason
// (never leave a cell silently not-run).
func refusalCouldNotDrive(res Result, step string, err error) Result {
	if v, ok := verdictForError(err); ok {
		res.Verdict = v
	} else {
		res.Verdict = VerdictRefused
	}
	res.Expected = "drive the scenario to a real verdict"
	res.Observed = fmt.Sprintf("could not %s: %v", step, err)
	return res
}

// refusalBranchHead reads owner/name's branch HEAD sha through GitHub's
// branches endpoint, going through c's own retrying/classifying `do`.
// ghClient (client_live.go) exposes no ref-read primitive yet — this is a
// new capability layered on the existing transport, not a private copy of
// one that already exists. direct-push-refused needs an INDEPENDENT,
// admin-authenticated confirmation that a refused participant push did not
// move main; trusting the failed push's own local clone state would not be
// independent evidence.
func refusalBranchHead(ctx context.Context, c *ghClient, owner, name, branch string) (string, error) {
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/branches/" + url.PathEscape(branch)
	var payload struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if _, err := c.do(ctx, http.MethodGet, path, nil, &payload); err != nil {
		return "", err
	}
	return payload.Commit.SHA, nil
}

// refusalConfigureGitIdentity sets a throwaway commit identity on a fresh
// clone — every raw-git scenario in this file needs one before its first
// commit, and no ambient git config is assumed on the machine running the
// live tier.
func refusalConfigureGitIdentity(ctx context.Context, dir string) error {
	for _, args := range [][]string{
		{"-C", dir, "config", "user.name", "a2a live-e2e refusal probe"},
		{"-C", dir, "config", "user.email", "live-e2e@a2ahub.invalid"},
	} {
		if err := runCmd(ctx, "git", args...); err != nil {
			return err
		}
	}
	return nil
}

// refusalProtectedBranchMarkers are the phrases GitHub/git emit when a push
// is refused specifically because the target is a protected branch — kept
// separate from internal/host's own pushForbiddenMarkers (which classifies
// ACCESS refusals, a different question) because direct-push-refused needs
// to distinguish "branch protection blocked this" from any other reason a
// raw push might fail (a missing git binary, a cancelled context, a
// transient network error) — asserting on the exit code and the
// unconfirmed sha alone would pass on any of those, which is exactly the
// false-pass this row exists to avoid (spec 36 plan row 9).
var refusalProtectedBranchMarkers = []string{
	"protected branch",
	"gh006",
	"must be made through a pull request",
}

func refusalLooksLikeProtectionRejection(errText string) bool {
	lower := strings.ToLower(errText)
	for _, marker := range refusalProtectedBranchMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// refusalTransportMarkers name a git push that never got a verdict out of
// GitHub at all. They are matched on the SAME text as the protection markers
// and deliberately checked first: `host.PushBranch` collapses every non-zero
// git exit into ErrPushRejected, so the message is the only thing that
// separates "protection refused me" from "I could not reach the server".
var refusalTransportMarkers = []string{
	"could not resolve host",
	"connection refused",
	"connection reset",
	"operation timed out",
	"timed out",
	"network is unreachable",
	"tls handshake",
	"unexpected disconnect",
	"early eof",
	"rpc failed",
	"http 5",
	"502",
	"503",
	"504",
	"remote end hung up",
	"context deadline exceeded",
	"context canceled",
}

// refusalLooksLikeTransportFailure reports whether a push error is a failure
// to reach GitHub rather than a refusal by it. A true here means the scenario
// observed nothing about protection, which is VerdictTimedOut — never a fail.
func refusalLooksLikeTransportFailure(errText string) bool {
	lower := strings.ToLower(errText)
	for _, marker := range refusalTransportMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// refusalDirectPushScenario drives spec 36 plan row 9 (AC-961.2's "a direct
// push to main is rejected" half): a raw `git push` straight to main, as
// participant B, deliberately bypassing the write funnel entirely (`a2a
// submit` goes through its own section/PR machinery — this is the raw path
// a maintainer's own `git push` would take).
//
// host.PushBranch — the product's own non-force push primitive — is reused
// rather than hand-rolled, so this exercises the exact push mechanics the
// funnel itself uses (D-019: core speaks plain git). Using it here is NOT
// "reusing the funnel": PushBranch has no section guard, no CC-085 check,
// no branch derivation — it is bare `git push`, exactly the raw path this
// row must drive.
func refusalDirectPushScenario(ctx context.Context, h *harness) Result {
	res := Result{Scenario: refusalScenarioDirectPushRefused, System: SystemB, Surface: SurfaceCLI}

	remoteURL := "https://github.com/" + h.Org + "/" + h.Repo + ".git"

	before, err := refusalBranchHead(ctx, h.Prov, h.Org, h.Repo, "main")
	if err != nil {
		return refusalCouldNotDrive(res, "read main's HEAD sha before the probe push", err)
	}

	work, err := os.MkdirTemp("", "livee2e-direct-push-*")
	if err != nil {
		return refusalCouldNotDrive(res, "create a scratch working copy", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	cloneDir := filepath.Join(work, "clone")
	if err := runCmd(ctx, "git", "clone", "-q", remoteURL, cloneDir); err != nil {
		return refusalCouldNotDrive(res, "clone the space to probe a direct push", err)
	}
	if err := refusalConfigureGitIdentity(ctx, cloneDir); err != nil {
		return refusalCouldNotDrive(res, "configure the probe clone's git identity", err)
	}

	marker := filepath.Join(cloneDir, ".livee2e-direct-push-probe")
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339Nano)+"\n"), 0o644); err != nil {
		return refusalCouldNotDrive(res, "write the probe file", err)
	}
	for _, args := range [][]string{
		{"-C", cloneDir, "add", "-A"},
		{"-C", cloneDir, "commit", "-q", "-m", "probe: direct push to main must be refused (spec 36 row 9)"},
	} {
		if err := runCmd(ctx, "git", args...); err != nil {
			return refusalCouldNotDrive(res, "commit the probe change", err)
		}
	}

	hc := host.NewGitHubHost(nil, defaultAPIRoot)
	_, pushErr := hc.PushBranch(ctx, host.PushBranchRequest{
		RepoDir: cloneDir, LocalRef: "HEAD", Branch: "main", RemoteURL: remoteURL,
		Credential: host.Credential{Token: h.B.Token},
	})

	// The re-read after the push attempt is the §T6-d shape, not
	// instrumentation: we DID drive the push, we just cannot yet confirm
	// its effect, so a failure here is VerdictTimedOut ("we do not know"),
	// never VerdictRefused ("we could not drive this").
	after, readErr := refusalBranchHead(ctx, h.Prov, h.Org, h.Repo, "main")
	if readErr != nil {
		res.Verdict = VerdictTimedOut
		res.Expected = "confirm main's HEAD sha after the push attempt"
		res.Observed = "could not re-read main's sha to confirm the push's effect: " + readErr.Error()
		res.Detail = fmt.Sprintf("main before=%s; push err=%v", before, pushErr)
		return res
	}

	res.Detail = fmt.Sprintf("main before=%s after=%s; push err=%v", before, after, pushErr)

	switch {
	case pushErr == nil:
		res.Verdict = VerdictFail
		res.Expected = "a raw git push to main by participant B (non-admin) is rejected by branch protection"
		res.Observed = "the push SUCCEEDED (no error) — branch protection did not block a direct write to main"
	case after != before:
		res.Verdict = VerdictFail
		res.Expected = "main's HEAD sha is unchanged by the refused push"
		res.Observed = fmt.Sprintf("main moved from %s to %s despite the push reporting an error", before, after)
	case !errors.Is(pushErr, host.ErrPushRejected):
		res.Verdict = VerdictFail
		res.Expected = "the push is refused as a protected-branch rejection (host.ErrPushRejected)"
		res.Observed = "the push failed, but not with a recognisable push-rejected error: " + pushErr.Error()
	case refusalLooksLikeTransportFailure(pushErr.Error()):
		// Checked BEFORE the protection-marker assertion below, and that
		// order is the whole point. PushBranch wraps every non-zero git exit
		// in ErrPushRejected, so a network blip during the raw push arrives
		// here indistinguishable from a real refusal by shape — and would
		// then fall to the next case and be reported as "branch protection
		// did not work". That is a false product defect on somebody else's
		// outage, which is exactly how a live tier teaches its readers to
		// ignore red (spec 36 §T6-d).
		res.Verdict = VerdictTimedOut
		res.Expected = "the push reaches GitHub and is refused by branch protection"
		res.Observed = "the push failed at the transport, so nothing about protection was observed: " + pushErr.Error()
	case !refusalLooksLikeProtectionRejection(pushErr.Error()):
		// Only asserting the exit code (or even ErrPushRejected, which
		// PushBranch also returns for a missing git binary or a cancelled
		// context — github.go wraps every non-zero exit the same way)
		// would pass for a reason that has nothing to do with branch
		// protection. Require the rejection to actually NAME protection.
		res.Verdict = VerdictFail
		res.Expected = "the push's rejection names branch protection (e.g. GH006 / \"must be made through a pull request\")"
		res.Observed = "the push failed, but the message does not read like a protection rejection: " + pushErr.Error()
	default:
		res.Verdict = VerdictPass
	}
	return res
}

// refusalNewElements returns the elements of after that are NOT in before —
// deliberately one-directional (not a set-equality diff): with
// `delete_branch_on_merge: true` armed, GitHub can asynchronously prune an
// EARLIER scenario's already-merged branch inside this scenario's own
// before/after window, and a branch disappearing is not out-of-section-
// write-refused's business. Only a NEW element is this row's defect.
func refusalNewElements(before, after []string) []string {
	seen := make(map[string]bool, len(before))
	for _, b := range before {
		seen[b] = true
	}
	var out []string
	for _, a := range after {
		if !seen[a] {
			out = append(out, a)
		}
	}
	return out
}

// refusalPullNumberStrings renders each pull's number as a comparable
// token for refusalNewElements.
func refusalPullNumberStrings(pulls []PullState) []string {
	out := make([]string, 0, len(pulls))
	for _, p := range pulls {
		out = append(out, fmt.Sprintf("#%d", p.Number))
	}
	return out
}

// refusalOutOfSectionWriteScenario drives spec 36 plan row 16: a write
// attempt whose declared `from:` does not match the authoring system's own
// identity — the client-side analogue of internal/space's ErrWrongSection.
//
// DEVIATION (recorded, not silently resolved): the brief points at the
// funnel's OWN ErrWrongSection (internal/space/funnel.go's sectionOK). That
// guard is structurally UNREACHABLE through `a2a submit`: buildRequest
// (internal/cli/cmd_submit.go) always derives the committed path from
// space.NewLayout(c.ownSystem) — the LOCAL system's own configured
// identity — never from the draft's `from:` field, so a forged `from`
// cannot make submitSectionPath compute a path outside the author's own
// section. The refusal this row actually observes is cmd_submit.go's own
// CC-002 "foreign-section" guard (line ~341), which compares the draft's
// declared `from` against the connected system's own identity and refuses
// BEFORE any git/network call — the exact shape the hermetic reference test
// (internal/e2e/testdata/t3/submit_refusal.txtar) already asserts. What
// THIS row adds over that hermetic test is the live-only half: a
// before/after read of the real space proving nothing reached GitHub — an
// observation a unit test structurally cannot make.
//
// `a2a new --field from=<other-system>` is what makes the attempt
// constructible from the CLI at all: cmd_new.go only DEFAULTS `from` to the
// connected system when the flag is absent (`if _, has := fields["from"];
// !has`), so an explicit override is honoured — this is the shape
// docs/runbooks/live-e2e/run.sh tried and, not having found it, marked
// not-run.
func refusalOutOfSectionWriteScenario(ctx context.Context, h *harness) Result {
	res := Result{Scenario: refusalScenarioOutOfSectionWriteRefused, System: SystemB, Surface: SurfaceCLI}

	branchesBefore, err := h.Prov.ListBranches(ctx, h.Org, h.Repo)
	if err != nil {
		return refusalCouldNotDrive(res, "list branches before the probe", err)
	}
	pullsBefore, err := h.Prov.ListPulls(ctx, h.Org, h.Repo, "all")
	if err != nil {
		return refusalCouldNotDrive(res, "list pull requests before the probe", err)
	}

	id, _, draftErr := h.B.Draft(ctx, "announcement", "--field", "from="+h.A.System)
	if draftErr != nil {
		return refusalCouldNotDrive(res, "draft an out-of-section artifact", draftErr)
	}

	stdout, stderr, submitErr := h.B.Run(ctx, "submit", id)

	branchesAfter, err := h.Prov.ListBranches(ctx, h.Org, h.Repo)
	if err != nil {
		return refusalCouldNotDrive(res, "list branches after the probe", err)
	}
	pullsAfter, err := h.Prov.ListPulls(ctx, h.Org, h.Repo, "all")
	if err != nil {
		return refusalCouldNotDrive(res, "list pull requests after the probe", err)
	}

	newBranches := refusalNewElements(branchesBefore, branchesAfter)
	newPulls := refusalNewElements(refusalPullNumberStrings(pullsBefore), refusalPullNumberStrings(pullsAfter))

	res.Detail = fmt.Sprintf("submit stdout=%q stderr=%q err=%v; new branches=%v; new pulls=%v",
		strings.TrimSpace(stdout), strings.TrimSpace(stderr), submitErr, newBranches, newPulls)

	switch {
	case submitErr == nil:
		res.Verdict = VerdictFail
		res.Expected = "a2a submit refuses an artifact whose `from` (" + h.A.System + ") does not match the authoring system (" + h.B.System + ") — CC-002 foreign-section"
		res.Observed = "submit SUCCEEDED (exit 0) instead of refusing"
	case !strings.Contains(stderr, "CC-002"):
		res.Verdict = VerdictFail
		res.Expected = "submit refuses citing CC-002 foreign-section"
		res.Observed = "submit refused, but not naming CC-002/foreign-section: " + strings.TrimSpace(stderr)
	case len(newBranches) > 0 || len(newPulls) > 0:
		res.Verdict = VerdictFail
		res.Expected = "no new branch and no new pull request appear on the real space after a client-side refusal"
		res.Observed = fmt.Sprintf("new branches=%v, new pulls=%v — something reached GitHub despite the local refusal", newBranches, newPulls)
	default:
		res.Verdict = VerdictPass
	}
	return res
}

// refusalMinBinaryVersionLine matches the live space.yaml's write-floor pin
// (the same bare `min_binary_version: x.y.z` shape scaffold.go's
// templateFloorPattern reads, preserving any trailing same-line comment —
// space-template/space.yaml carries one — since the match ends at the
// version's own word boundary).
var refusalMinBinaryVersionLine = regexp.MustCompile(`(?m)^(min_binary_version:\s*)[0-9]+\.[0-9]+\.[0-9]+\b`)

// refusalCurrentMinBinaryVersion reads a cloned space.yaml's declared
// min_binary_version, reusing the sibling's own parser (scaffold.go) rather
// than a second copy of the pattern.
func refusalCurrentMinBinaryVersion(cloneDir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(cloneDir, "space.yaml"))
	if err != nil {
		return "", fmt.Errorf("livee2e: refusal: read space.yaml: %w", err)
	}
	return TemplateMinBinaryVersion(string(raw))
}

// refusalSetSpaceMinBinaryVersion rewrites dir's checked-out space.yaml's
// min_binary_version to version, commits, and pushes to main via
// host.PushBranch (a plain, non-force push — enforce_admins:false means the
// provisioner may write straight to main without going through a PR,
// mirroring row 12's own established assertion; never touching branch
// protection itself keeps this the least invasive mutation that proves the
// row).
func refusalSetSpaceMinBinaryVersion(ctx context.Context, hc *host.GitHubHost, dir, remoteURL, token, version, commitMsg string) error {
	manifestPath := filepath.Join(dir, "space.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("livee2e: refusal: read space.yaml: %w", err)
	}
	if !refusalMinBinaryVersionLine.Match(raw) {
		return fmt.Errorf("livee2e: refusal: space.yaml declares no min_binary_version line to rewrite")
	}
	patched := refusalMinBinaryVersionLine.ReplaceAll(raw, []byte("${1}"+version))
	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		return fmt.Errorf("livee2e: refusal: write space.yaml: %w", err)
	}
	if err := runCmd(ctx, "git", "-C", dir, "add", "space.yaml"); err != nil {
		return err
	}
	if err := runCmd(ctx, "git", "-C", dir, "commit", "-q", "-m", commitMsg); err != nil {
		return err
	}
	if _, err := hc.PushBranch(ctx, host.PushBranchRequest{
		RepoDir: dir, LocalRef: "HEAD", Branch: "main", RemoteURL: remoteURL,
		Credential: host.Credential{Token: token},
	}); err != nil {
		return fmt.Errorf("livee2e: refusal: push min_binary_version=%s: %w", version, err)
	}
	return nil
}

// refusalRestoreMinBinaryVersion re-fetches main and hard-resets dir onto
// it before re-applying original — guaranteeing the restore push is a
// fast-forward regardless of what happened on main since the raise (never
// trusting dir's own possibly-stale working tree, per the same discipline
// that makes this row's defer the deliverable).
func refusalRestoreMinBinaryVersion(ctx context.Context, hc *host.GitHubHost, dir, remoteURL, token, original string) error {
	if err := runCmd(ctx, "git", "-C", dir, "fetch", "-q", remoteURL, "main"); err != nil {
		return fmt.Errorf("livee2e: refusal: fetch main before restore: %w", err)
	}
	if err := runCmd(ctx, "git", "-C", dir, "reset", "-q", "--hard", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("livee2e: refusal: reset to fetched main before restore: %w", err)
	}
	return refusalSetSpaceMinBinaryVersion(ctx, hc, dir, remoteURL, token, original,
		"chore(live-e2e): restore min_binary_version to "+original+" (row 17 cleanup)")
}

// refusalStaleWriteFloorScenario drives spec 36 plan row 17 (CC-085): the
// provisioner (admin) raises the live space's min_binary_version above the
// harness binary's own floor (the harness stamps its binary with the
// TEMPLATE's floor exactly, so the binary starts out sitting AT the line —
// harnessBinaryVersion, provision_live.go), participant B's write is then
// refused naming the floor, and the original value is restored in a defer
// that MUST run even on a failure path — the single most dangerous mutation
// in this family, because every scenario family that runs after this one
// shares the same live space (plan §"STATE DISJOINTNESS").
//
// The write funnel's CC-085 guard compares the binary's own version against
// space.yaml's min_binary_version as read out of B's LOCAL MIRROR, not
// GitHub directly (internal/cli/cmd_submit.go readMinBinaryVersion — "the
// connected space's own space.yaml, already fetched into the mirror by
// `a2a connect`/`a2a sync`"). So after the provisioner's raise lands on
// main, B must `a2a sync` — a pure fetch/cache-refresh path the CC-085
// guard cannot itself gate — before drafting, or the guard compares against
// the STALE pre-raise floor and never fires. The restore path mirrors this:
// once main is back at the original floor, BOTH checkouts are re-synced, so
// no later family inherits a cached raised floor that locks its own writes
// out on a CC-085 refusal naming nothing.
func refusalStaleWriteFloorScenario(ctx context.Context, h *harness) (res Result) {
	res = Result{Scenario: refusalScenarioStaleWriteFloorRefused, System: SystemB, Surface: SurfaceCLI}

	remoteURL := "https://github.com/" + h.Org + "/" + h.Repo + ".git"
	hc := host.NewGitHubHost(nil, defaultAPIRoot)

	work, err := os.MkdirTemp("", "livee2e-stale-floor-*")
	if err != nil {
		return refusalCouldNotDrive(res, "create a scratch working copy", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	cloneDir := filepath.Join(work, "clone")
	if err := runCmd(ctx, "git", "clone", "-q", remoteURL, cloneDir); err != nil {
		return refusalCouldNotDrive(res, "clone the space to read/raise its min_binary_version", err)
	}
	if err := refusalConfigureGitIdentity(ctx, cloneDir); err != nil {
		return refusalCouldNotDrive(res, "configure the probe clone's git identity", err)
	}

	original, err := refusalCurrentMinBinaryVersion(cloneDir)
	if err != nil {
		return refusalCouldNotDrive(res, "read the space's current min_binary_version", err)
	}

	// A fixed, absurdly-high floor rather than an increment off the
	// harness binary's own version: it is trivially above ANY realistic
	// binary version, so this row needs no version-arithmetic of its own
	// (the funnel's versionOlderThan already owns that comparison).
	const raisedFloor = "999.0.0"
	if original == raisedFloor {
		return refusalCouldNotDrive(res, "raise min_binary_version", fmt.Errorf("the space's floor is already %s", raisedFloor))
	}

	floorRaised := false
	defer func() {
		if !floorRaised {
			return
		}
		// A fresh, detached, generously-bounded context: the scenario's
		// own ctx may be near its deadline by the time we get here, and
		// this restore is the one step every later family depends on —
		// it must not be starved by whatever budget the probe above spent.
		restoreCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		restoreErr := refusalRestoreMinBinaryVersion(restoreCtx, hc, cloneDir, remoteURL, h.Cfg.ProvisionerToken, original)
		if restoreErr == nil {
			for _, c := range []*checkout{h.A, h.B} {
				if _, syncStderr, syncErr := c.Run(restoreCtx, "sync"); syncErr != nil {
					restoreErr = fmt.Errorf("restored main but could not re-sync %s's mirror: %w: %s", c.System, syncErr, strings.TrimSpace(syncStderr))
					break
				}
			}
		}
		if restoreErr != nil {
			res.Verdict = VerdictFail
			res.Expected = "min_binary_version restored to " + original + " (and both mirrors re-synced) after the probe"
			res.Observed = "RESTORE FAILED: " + restoreErr.Error() + " — the live space's write floor may still be raised to " + raisedFloor + ", or a mirror may still cache it; every family after this one is at risk of a CC-085 lockout"
		}
	}()

	if err := refusalSetSpaceMinBinaryVersion(ctx, hc, cloneDir, remoteURL, h.Cfg.ProvisionerToken, raisedFloor,
		"chore(live-e2e): raise min_binary_version to "+raisedFloor+" (row 17 probe)"); err != nil {
		return refusalCouldNotDrive(res, "raise the space's min_binary_version above the harness binary's floor", err)
	}
	floorRaised = true

	if _, syncStderr, syncErr := h.B.Run(ctx, "sync"); syncErr != nil {
		res.Verdict = VerdictRefused
		res.Expected = "sync B's mirror so it observes the raised floor"
		res.Observed = fmt.Sprintf("a2a sync failed: %v: %s", syncErr, strings.TrimSpace(syncStderr))
		return res
	}

	id, _, draftErr := h.B.Draft(ctx, "announcement")
	if draftErr != nil {
		res.Verdict = VerdictRefused
		res.Expected = "draft an ordinary in-section artifact to probe the write floor"
		res.Observed = "could not draft: " + draftErr.Error()
		return res
	}

	_, stderr, submitErr := h.B.Run(ctx, "submit", id)
	res.Detail = fmt.Sprintf("raised floor=%s (original=%s); submit stderr=%q err=%v", raisedFloor, original, strings.TrimSpace(stderr), submitErr)

	switch {
	case submitErr == nil:
		res.Verdict = VerdictFail
		res.Expected = "submit refuses with the CC-085 stale-binary-version guard once the space's min_binary_version (" + raisedFloor + ") exceeds the harness binary's own version"
		res.Observed = "submit SUCCEEDED (exit 0) instead of refusing"
	case !strings.Contains(stderr, "min_binary_version") || !strings.Contains(stderr, raisedFloor):
		res.Verdict = VerdictFail
		res.Expected = "submit's refusal NAMES min_binary_version and the raised floor " + raisedFloor
		res.Observed = "submit refused, but not naming the floor: " + strings.TrimSpace(stderr)
	default:
		res.Verdict = VerdictPass
	}
	return res
}
