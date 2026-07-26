//go:build livee2e

package livee2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
)

// This file drives wave 3-2's boundary/identity family — the rows plan
// docs/features/v1-min-2026-07/plans/36-live-e2e-matrix.plan.md's per-row
// adjudication table names 8, 10, 11, 12 and 15:
//
//	protection-blocks-until-green        SystemB
//	cross-section-retrigger-stays-red    SystemB
//	protection-binds-participant         SystemB
//	protection-skips-provisioner         SystemA
//	executed-ref-not-stale               SystemB
//
// Spec 36 §T5 is the whole point of this family: every row but
// protection-skips-provisioner is authored by B, the non-admin machine
// account, because an assertion authored by an admin passes without testing
// anything. h.Prov is used ONLY where the row is explicitly about the
// provisioner's own privileged reach (protection-skips-provisioner, and the
// two re-trigger mechanisms inside cross-section-retrigger-stays-red, which
// are specifically about what the PROVISIONER can do to another login's PR).

// boundaryArtifactType is the artifact family every row here submits — the
// same "announcement" shape docs/runbooks/live-e2e/run.sh and the harness's
// own doc comments use; nothing about these assertions depends on which
// artifact type carries them.
const boundaryArtifactType = "announcement"

// boundaryProbeBranch is the raw-git branch cross-section-retrigger-stays-red
// pushes directly (never through the funnel, which would refuse it
// client-side) — named after docs/runbooks/live-e2e/run.sh's own probe/xsec.
const boundaryProbeBranch = "probe/xsec"

// requiredWorkflowName is the space template's own GitHub Actions workflow
// name (space-template/.github/workflows/a2a-validate.yml's `name:` key) —
// distinct from requiredCheckContext (provision_live.go), which is the
// check-RUN name branch protection matches ("a2a-validate / validate"). This
// is what ListWorkflowRuns filters on to find the run mechanism (a) of
// §T6-c's two re-trigger mechanisms reruns.
const requiredWorkflowName = "a2a-validate"

// crockfordAlphabet is the digit/letter set
// schemas/envelope/v1/base.schema.json's id pattern requires for the
// exchange/broadcast id shape's random suffix (Crockford base32, excluding
// i/l/o/u — the schema's own character class,
// "[0-9abcdefghjkmnpqrstvwxyz]{4}").
const crockfordAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// boundaryEnvelopeID mints a SCHEMA-VALID exchange/broadcast id
// (XA-<system>-<YYYYMMDD>-<4 crockford chars>) for an envelope this file
// writes directly via raw git, never through the funnel, so nothing else
// mints or validates this id before GitHub's own check does.
//
// This matters more than it looks: docs/runbooks/live-e2e/run.sh's own
// probe/xsec fixture is NOT reused verbatim here on purpose. This row's only
// observable is the check's Conclusion — if the envelope were invalid for
// any reason OTHER than the section it writes into, the check would still
// report "failure", and the row would PASS having asserted nothing about
// diff-authz specifically (the exact vacuity failure spec 36 §T5 exists to
// rule out, one field down). The suffix is derived from the nanosecond clock
// rather than a fixed literal because boundaryMoveMainDirect's use of this
// same id shape DOES land permanently on main — a fixed id there would find
// nothing to stage (identical content already committed) on a second live
// run against the same space.
func boundaryEnvelopeID(system string) string {
	n := time.Now().UnixNano()
	suffix := make([]byte, 4)
	for i := range suffix {
		suffix[i] = crockfordAlphabet[n%int64(len(crockfordAlphabet))]
		n /= int64(len(crockfordAlphabet))
	}
	return fmt.Sprintf("XA-%s-%s-%s", system, time.Now().UTC().Format("20060102"), suffix)
}

// boundaryRetriggerPollInterval / boundaryRetriggerPropagationCeiling bound
// the wait for a re-trigger to actually have STARTED on GitHub's side (a new
// or reused workflow run leaving/re-entering a non-"completed" state) before
// polling for its terminal outcome. A fixed sleep here would be exactly the
// vacuous-guard failure this row exists to rule out: reading the
// PRE-retrigger "completed" state and mistaking it for the retriggered run's
// own conclusion (this phase's own log records catching that failure mode
// twice already, one layer down). The interval magnitude matches
// docs/runbooks/live-e2e/run.sh's own settle sleeps; here it bounds a poll,
// not a guarantee.
const boundaryRetriggerPollInterval = 8 * time.Second
const boundaryRetriggerPropagationCeiling = 2 * time.Minute

// boundaryMergeableStateCeiling / boundaryMergeableStatePollInterval bound
// the wait for GitHub to finish computing a fresh PR's mergeable_state past
// "unknown" — a merge attempted in that window can answer 409 for a reason
// that has nothing to do with branch protection, which would misreport a
// transient as protection-skips-provisioner's CONFIG DRIFT finding. The
// required check itself still will not be green in this window (Actions
// takes minutes; mergeability computation takes seconds), so the assertion
// that row exists to make stays intact.
const boundaryMergeableStateCeiling = 1 * time.Minute
const boundaryMergeableStatePollInterval = 3 * time.Second

// boundaryResult builds one row for this family. Surface is always
// SurfaceCLI for this wave (spec 36 §10's cliOnly() amendment).
func boundaryResult(scenario, system string, verdict Verdict, expected, observed, detail string) Result {
	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI,
		Verdict: verdict, Expected: expected, Observed: observed, Detail: detail,
	}
}

// boundaryFromErr classifies a harness/GitHub-API error into this family's
// verdict, applying verdictForError's rule (harness_live.go) uniformly rather
// than each row deciding differently: a bounded wait expiring, or an
// exhausted 5xx retry, is VerdictTimedOut — never a fail. Any other error is
// a genuine failure to drive the scenario, reported as VerdictFail with the
// error's own text as Observed (verdictForError's documented contract: ok is
// false there both when err is nil and when the error is a genuine product
// failure — this function is the "caller decides" half of that contract).
//
// Deviation, named rather than silent: verdictForError does not map
// context.Canceled/DeadlineExceeded, so a ctx cancellation reaching here
// (e.g. the overall run's own deadline) renders as VerdictFail rather than
// VerdictTimedOut. Left as-is rather than special-cased locally, since
// verdictForError is the single cross-family mapping and widening its
// contract here — without the other three families agreeing — would leave
// them classifying the same shape differently.
func boundaryFromErr(scenario, system, expected string, err error) Result {
	if v, ok := verdictForError(err); ok {
		return boundaryResult(scenario, system, v, expected, err.Error(), "")
	}
	return boundaryResult(scenario, system, VerdictFail, expected, err.Error(), "")
}

// boundaryRefused builds a refused row for a SETUP failure this scenario
// could not get past (a raw-git shell step, a scratch dir, a missing
// workflow run to re-trigger) — distinct from a decisive GitHub-API answer,
// which boundaryFromErr classifies instead. Per the recording rules: a cell
// that could not be driven is reported REFUSED with an Observed that says
// why, never silently left not-run. This Refused-vs-Fail split (setup step
// vs. GitHub-API answer) is this family's own rule, not a foundation
// primitive — named here so the lead can reconcile it with the sibling
// families if they classify differently.
func boundaryRefused(scenario, system, expected, observed string) Result {
	return boundaryResult(scenario, system, VerdictRefused, expected, observed, "")
}

// boundaryDelay sleeps for d, ctx-aware and bounded — never an unconditional
// blocking sleep.
func boundaryDelay(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// runBoundaryScenarios drives wave 3-2's boundary/identity family. It returns
// one Result per row it drove; the lead-owned runner Records each into the
// declared matrix.
func runBoundaryScenarios(ctx context.Context, h *harness) []Result {
	return []Result{
		boundaryProtectionBlocksUntilGreen(ctx, h),
		boundaryProtectionSkipsProvisioner(ctx, h),
		boundaryProtectionBindsParticipant(ctx, h),
		boundaryCrossSectionRetriggerStaysRed(ctx, h),
		boundaryExecutedRefNotStale(ctx, h),
	}
}

// scenarioProtectionBlocksUntilGreen is row 8 (AC-961.2): B opens a PR; while
// the required check is NOT yet successful, an explicit MergePull attempt by
// B must be REFUSED. Attempted immediately after submit — before any wait —
// because a real Actions run cannot possibly have already completed in that
// window, so the check is guaranteed pending at the moment of the call.
const scenarioProtectionBlocksUntilGreen = "protection-blocks-until-green"

func boundaryProtectionBlocksUntilGreen(ctx context.Context, h *harness) Result {
	const expected = "MergePull is refused (status 405/409) while the required check has not yet completed"

	sub, err := h.DraftAndSubmit(ctx, h.B, boundaryArtifactType)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBlocksUntilGreen, SystemB, expected, err)
	}

	// MergePull's own contract (client_live.go): merged=false, err=nil means
	// GitHub answered 405 or 409 — a refusal is the PASS here, not an error.
	// Either status counts: this row asserts "the merge did not go through
	// while pending", not which of protection's two refusal codes answered.
	merged, status, err := h.Part.MergePull(ctx, h.Org, h.Repo, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBlocksUntilGreen, SystemB, expected, err)
	}
	if merged {
		return boundaryResult(scenarioProtectionBlocksUntilGreen, SystemB, VerdictFail,
			expected,
			fmt.Sprintf("MergePull succeeded (status %d) though the required check had not yet completed", status),
			fmt.Sprintf("PR #%d (head %s)", sub.PRNumber, sub.HeadSHA))
	}
	return boundaryResult(scenarioProtectionBlocksUntilGreen, SystemB, VerdictPass, "", "",
		fmt.Sprintf("PR #%d: MergePull refused (status %d) while the check was pending — branch protection blocks non-admin B", sub.PRNumber, status))
}

// boundaryWaitMergeableStateKnown polls prNumber until GitHub reports a
// mergeable_state other than "unknown" — see the ceiling's doc comment for
// why this matters for protection-skips-provisioner specifically.
func boundaryWaitMergeableStateKnown(ctx context.Context, h *harness, prNumber int) (PullState, error) {
	deadline := time.Now().Add(boundaryMergeableStateCeiling)
	for {
		pr, err := h.Prov.Pull(ctx, h.Org, h.Repo, prNumber)
		if err != nil {
			return PullState{}, err
		}
		if pr.MergeableState != "unknown" {
			return pr, nil
		}
		if time.Now().After(deadline) {
			return PullState{}, fmt.Errorf("%w: PR #%d mergeable_state stayed 'unknown'", ErrCheckWaitTimedOut, prNumber)
		}
		if err := boundaryDelay(ctx, boundaryMergeableStatePollInterval); err != nil {
			return PullState{}, err
		}
	}
}

// scenarioProtectionSkipsProvisioner is row 12 (AC-961.4, §T6-a): the test
// space mirrors production's enforce_admins:false, so protection does not
// bind an admin. The provisioner opens a PR whose required check is not yet
// green and merges it via the API anyway — a PASS here converts a footgun
// into a tested, named property. A FAILURE here (an admin blocked) is a
// config-drift finding — the test space no longer mirrors production — not a
// product bug, and is worded that way in Observed.
//
// Deviation noted, not silently left implicit: a PASS here merges a PR onto
// main whose required check never necessarily completed before the merge —
// that is the assertion, by design. Its content is a normal, schema-valid
// envelope in the provisioner's own section, so main stays structurally
// valid; later scenarios reading main should expect that commit's check may
// still resolve asynchronously after the merge.
const scenarioProtectionSkipsProvisioner = "protection-skips-provisioner"

func boundaryProtectionSkipsProvisioner(ctx context.Context, h *harness) Result {
	const expected = "the provisioner's merge succeeds (a 2xx status) even while the check is pending — enforce_admins:false does not bind admins (§T6-a), mirroring production"

	sub, err := h.DraftAndSubmit(ctx, h.A, boundaryArtifactType)
	if err != nil {
		return boundaryFromErr(scenarioProtectionSkipsProvisioner, SystemA, expected, err)
	}

	pr, err := boundaryWaitMergeableStateKnown(ctx, h, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionSkipsProvisioner, SystemA, expected, err)
	}

	merged, status, err := h.Prov.MergePull(ctx, h.Org, h.Repo, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionSkipsProvisioner, SystemA, expected, err)
	}
	if !merged {
		observed := fmt.Sprintf("not mergeable (status %d, mergeable_state=%s) — not a protection refusal", status, pr.MergeableState)
		if status == http.StatusMethodNotAllowed {
			observed = fmt.Sprintf("CONFIG DRIFT, not a product bug: MergePull refused the provisioner (status %d) though the space is configured enforce_admins:false — the test space's protection no longer mirrors production", status)
		}
		return boundaryResult(scenarioProtectionSkipsProvisioner, SystemA, VerdictFail, expected, observed,
			fmt.Sprintf("PR #%d (head %s, mergeable_state=%s)", sub.PRNumber, sub.HeadSHA, pr.MergeableState))
	}
	return boundaryResult(scenarioProtectionSkipsProvisioner, SystemA, VerdictPass, "", "",
		fmt.Sprintf("PR #%d: provisioner MergePull succeeded (status %d) while the check was pending — protection does not bind admins, matching production", sub.PRNumber, status))
}

// scenarioProtectionBindsParticipant is row 11 (AC-961.4's other half): the
// eventual green merge DID happen, and only after the check completed — the
// positive counterpart to protection-blocks-until-green's refusal. This is
// asserted as an ORDER, not merely as two facts observed independently: the
// PR must NOT be merged immediately after submit (while the check is still
// pending — the same immediacy argument row 8 relies on), and only once the
// check has completed success may the PR be merged. `a2a submit` arms
// GitHub's native auto-merge, so the PR may already be merged by the time
// the check completes; the PR is re-read before attempting an explicit
// merge, so this scenario asserts the fact regardless of which caller
// happened to trigger the actual merge.
const scenarioProtectionBindsParticipant = "protection-binds-participant"

func boundaryProtectionBindsParticipant(ctx context.Context, h *harness) Result {
	const expected = "the PR is not merged immediately after submit (check pending), and lands merged only once the required check completes success"

	sub, err := h.DraftAndSubmit(ctx, h.B, boundaryArtifactType)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, err)
	}

	prBefore, err := h.Part.Pull(ctx, h.Org, h.Repo, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, err)
	}
	if prBefore.Merged {
		return boundaryResult(scenarioProtectionBindsParticipant, SystemB, VerdictFail, expected,
			"the PR was already merged immediately after submit — no pending gate to observe binding against",
			fmt.Sprintf("PR #%d", sub.PRNumber))
	}

	res, err := h.AwaitCheck(ctx, h.B, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, err)
	}
	if res.Conclusion != "success" {
		return boundaryResult(scenarioProtectionBindsParticipant, SystemB, VerdictFail,
			expected,
			fmt.Sprintf("required check concluded %q, not success — no green gate to test the merge against", res.Conclusion),
			fmt.Sprintf("PR #%d", sub.PRNumber))
	}

	pr, err := h.Part.Pull(ctx, h.Org, h.Repo, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, err)
	}
	if pr.Merged {
		return boundaryResult(scenarioProtectionBindsParticipant, SystemB, VerdictPass, "", "",
			fmt.Sprintf("PR #%d: not merged while pending, check completed success, and the PR is merged (landed only once green)", sub.PRNumber))
	}

	merged, status, err := h.Part.MergePull(ctx, h.Org, h.Repo, sub.PRNumber)
	if err != nil {
		return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, err)
	}
	if !merged {
		// A refused explicit merge is NOT yet a failure, and the first full
		// 38-row run (2026-07-26) is why this branch exists: the check went
		// green, this row's re-read above still saw the PR unmerged, the
		// explicit merge got 405 — and GitHub's own auto-merge landed the PR
		// 52 seconds later. The row's thesis ("lands merged only once the
		// required check completes success") HELD; what raced was the method.
		// Every narrowed run had passed this row, so it took an uninterrupted
		// run to surface the timing at all.
		//
		// The retry is deliberately a re-read, never a second merge attempt:
		// the funnel arms GitHub's native auto-merge, so the merge is already
		// someone's job and this row's business is only whether it happens.
		// Bounded, so a PR that genuinely never merges still fails — with the
		// original 405 named, not swallowed.
		deadline := time.Now().Add(boundaryMergeableStateCeiling)
		for time.Now().Before(deadline) {
			if err := boundaryDelay(ctx, boundaryMergeableStatePollInterval); err != nil {
				return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, err)
			}
			after, perr := h.Part.Pull(ctx, h.Org, h.Repo, sub.PRNumber)
			if perr != nil {
				return boundaryFromErr(scenarioProtectionBindsParticipant, SystemB, expected, perr)
			}
			if after.Merged {
				return boundaryResult(scenarioProtectionBindsParticipant, SystemB, VerdictPass, "", "",
					fmt.Sprintf("PR #%d: not merged while pending, check completed success, and auto-merge landed it "+
						"after this row's own explicit merge raced it (status %d) — landed only once green, unattended",
						sub.PRNumber, status))
			}
		}
		return boundaryResult(scenarioProtectionBindsParticipant, SystemB, VerdictFail,
			expected,
			fmt.Sprintf("check completed success, but MergePull was refused (status %d) and the PR was still unmerged %s later",
				status, boundaryMergeableStateCeiling),
			fmt.Sprintf("PR #%d", sub.PRNumber))
	}
	return boundaryResult(scenarioProtectionBindsParticipant, SystemB, VerdictPass, "", "",
		fmt.Sprintf("PR #%d: not merged while pending, check completed success, MergePull then succeeded (status %d)", sub.PRNumber, status))
}

// boundaryProbeEnvelope renders a schema-valid envelope/v1 announcement in
// alpha's own section (reused here for both the cross-section probe, which
// deliberately writes it under `from: alpha` while being pushed and opened
// by B, and for moving main directly, where it is a perfectly ordinary
// alpha-authored artifact). id must come from boundaryEnvelopeID — see that
// function's doc comment for why an invalid id (or, as here, an invalid
// `category`) would make this row pass vacuously. category is "notice"
// rather than docs/runbooks/live-e2e/run.sh's own literal "other", which
// schemas/envelope/v1/announcement.schema.json's enum does not accept
// (["release","deprecation","migration","incident","notice","status"]) — a
// real, previously undiscovered vacuity gap in that reference fixture,
// caught while verifying the schema this row's honesty depends on.
// boundaryCheckRunCount counts the check runs named requiredCheckContext at
// headSHA, through CheckRuns' now-corrected `filter=all` (client_live.go).
// It is NO LONGER the gating mechanism cross-section-retrigger-stays-red
// waits on (see boundaryAwaitCheckLeavesCompleted's own doc comment for why
// that changed) — kept and still called, at each re-trigger boundary, so the
// row's own Detail records the count as EVIDENCE rather than deleting a
// still-useful, now-correct observation. Whether a re-trigger mints a
// distinct check-run object or mutates one in place is a real open question
// this wave's diagnosis could not answer from source alone (see
// retrigger_diagnosis in this wave's own report); the recorded counts are
// what lets the NEXT live run answer it for free — 1 -> 2 -> 3 means
// distinct objects, 1 -> 1 -> 1 means GitHub mutates in place and
// count-based detection could never have worked here regardless of the
// filter fix.
func boundaryCheckRunCount(ctx context.Context, h *harness, headSHA string) (int, error) {
	runs, err := h.Prov.CheckRuns(ctx, h.Org, h.Repo, headSHA)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, run := range runs {
		if run.Name == requiredCheckContext {
			n++
		}
	}
	return n, nil
}

// boundaryCheckStatusOnce performs ONE non-blocking read of prNumber's
// required check via host.CheckStatus, authenticated as c — the single-shot
// primitive boundaryAwaitCheckLeavesCompleted needs. h.AwaitCheck
// (WaitForRequiredCheck, harness_live.go) is the WRONG tool for that job: it
// blocks UNTIL the check reaches "completed", so it structurally cannot
// observe the check LEAVING "completed" — the one signal a re-trigger
// actually produces.
func boundaryCheckStatusOnce(ctx context.Context, h *harness, c *checkout, prNumber int) (host.CheckStatusResult, error) {
	hc := host.NewGitHubHost(nil, defaultAPIRoot)
	return hc.CheckStatus(ctx, host.StatusRequest{
		Repo:       host.Repo{Owner: h.Org, Name: h.Repo},
		PRNumber:   prNumber,
		Credential: host.Credential{Token: c.Token},
	})
}

// boundaryAwaitCheckLeavesCompleted polls until prNumber's required check
// reports a State other than "completed" — proof a re-trigger genuinely
// STARTED a new execution, replacing the pre-fix mechanism
// (boundaryWaitCheckRunAppears, since removed) that polled
// boundaryCheckRunCount for a count exceeding a baseline.
//
// That mechanism's premise was that a re-trigger leaves the superseded
// check run visible and adds a NEW, distinctly-countable entry at the same
// head SHA. This wave's diagnosis (retrigger_diagnosis, this wave's report)
// found the count could never have worked as gating logic: CheckRuns built
// its query with no `filter` param, and GitHub's own documented default for
// that endpoint IS `filter=latest` even when the caller omits it —
// internal/host/github.go's own CheckStatus states this explicitly, one
// file over, for that function's own (deliberate, correct) use of the same
// default. An unfiltered read therefore ALSO collapsed to "one run per
// check name", so `n > baseline` could never be satisfied — not a timing
// race, an unsatisfiable condition from the day the row was written, which
// is exactly why it timed out identically on four consecutive live runs
// with "no new check run appeared after the re-trigger" (always the SAME
// message, never a different one — see this wave's report for why that
// detail is itself evidence: RerunWorkflow/SetPullState answering
// non-2xx would have surfaced as a DIFFERENT error, through boundaryFromErr,
// not this timeout).
//
// A STATUS-transition wait sidesteps the open question the count-based
// mechanism could not settle from source alone (does a re-trigger mint a
// distinct check-run object, or mutate one in place?) entirely: whichever
// GitHub does, the SAME check name cannot stay "completed" while a
// genuinely fresh execution runs for it — it must report something other
// than "completed" (queued/in_progress) for the window that execution
// takes. If GitHub's read is momentarily stale (the exact failure mode
// boundaryRetriggerPollInterval's own doc comment already names: "reading
// the PRE-retrigger 'completed' state and mistaking it for the retriggered
// run's own conclusion"), this loop simply has not seen the transition
// YET; it has genuinely not seen one only if the whole ceiling elapses
// without ever observing anything but "completed" — which renders
// VerdictTimedOut (never a false pass, never a false fail: a missed
// transient window and "the re-trigger truly did nothing" are
// indistinguishable from here, and the honest verdict for both is "we did
// not confirm it").
func boundaryAwaitCheckLeavesCompleted(ctx context.Context, h *harness, c *checkout, prNumber int) error {
	deadline := time.Now().Add(boundaryRetriggerPropagationCeiling)
	for {
		res, err := boundaryCheckStatusOnce(ctx, h, c, prNumber)
		if err != nil {
			return err
		}
		if res.State != "completed" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%w: PR #%d's required check never left \"completed\" within %s after the re-trigger", ErrCheckWaitTimedOut, prNumber, boundaryRetriggerPropagationCeiling)
		}
		if err := boundaryDelay(ctx, boundaryRetriggerPollInterval); err != nil {
			return err
		}
	}
}

// scenarioCrossSectionRetriggerStaysRed is row 10 (AC-961.3) — THE
// highest-value row in the matrix: the v0.6.4 GITHUB_ACTOR authorization
// bypass. B pushes a raw git branch writing into A's section and opens the
// PR itself; the check must be FAILURE, and stay failure after the
// provisioner re-triggers it through BOTH §T6-c mechanisms — the rerun-API
// button (needs Actions:write) and close/reopen (needs only Pull
// requests:write, and is the one the original report described, so the more
// important of the two to assert). A pass needs both to stay red.
//
// AC-984.1 (spec 38 wave G): this row timed out on FOUR consecutive live
// runs with "no new check run appeared after the re-trigger" before this
// wave. The cause was the gating mechanism, not the product or the space:
// see boundaryAwaitCheckLeavesCompleted's own doc comment for the fix, and
// this wave's own report for the full retrigger_diagnosis.
const scenarioCrossSectionRetriggerStaysRed = "cross-section-retrigger-stays-red"

func boundaryCrossSectionRetriggerStaysRed(ctx context.Context, h *harness) Result {
	const expected = "the check stays failure before AND after both provisioner re-trigger mechanisms — never success (the v0.6.4 GITHUB_ACTOR bypass class)"

	work, err := os.MkdirTemp("", "livee2e-boundary-xsec-*")
	if err != nil {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "could not create a scratch dir: "+err.Error())
	}
	defer func() { _ = os.RemoveAll(work) }()
	dir := filepath.Join(work, "raw")

	remoteURL := "https://github.com/" + h.Org + "/" + h.Repo + ".git"
	// The test space is PUBLIC (spec 36 §9.5.1), so the clone itself needs
	// no credential — only the push below does.
	if err := runCmd(ctx, "git", "clone", "-q", remoteURL, dir); err != nil {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "git clone: "+err.Error())
	}
	for _, args := range [][]string{
		{"-C", dir, "config", "user.name", "livee2e-boundary-probe"},
		{"-C", dir, "config", "user.email", "livee2e-boundary-probe@a2ahub.invalid"},
		{"-C", dir, "checkout", "-q", "-b", boundaryProbeBranch},
	} {
		if err := runCmd(ctx, "git", args...); err != nil {
			return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "git "+strings.Join(args, " ")+": "+err.Error())
		}
	}

	id := boundaryEnvelopeID(systemAlpha)
	envelopePath := filepath.Join(dir, "alpha", "exchanges", id+".md")
	if err := os.WriteFile(envelopePath, []byte(boundaryProbeEnvelope(id)), 0o644); err != nil {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "write probe envelope: "+err.Error())
	}
	if err := runCmd(ctx, "git", "-C", dir, "add", "-A"); err != nil {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "git add: "+err.Error())
	}
	if err := runCmd(ctx, "git", "-C", dir, "commit", "-q", "-m", "probe: bravo into alpha section"); err != nil {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "git commit: "+err.Error())
	}

	if err := gitForcePush(ctx, dir, remoteURL, h.Part.Token, "HEAD", boundaryProbeBranch); err != nil {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, "push probe branch: "+err.Error())
	}
	// Cleaned up regardless of how this scenario concludes — a family that
	// leaves the space altered breaks every family after it. Deleting the
	// branch also causes GitHub to auto-close the PR it backs, since its
	// source branch is gone.
	defer func() { _ = h.Part.DeleteRef(ctx, h.Org, h.Repo, "heads/"+boundaryProbeBranch) }()

	prNumber, err := h.Part.OpenPull(ctx, h.Org, h.Repo,
		boundaryProbeTitle, boundaryProbeBody(h.Org, h.Repo), boundaryProbeBranch, "main")
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}

	// The head SHA is read HERE rather than after the first assertion, so the
	// deferred claim below can account for every run this probe reddens no
	// matter which exit the row takes. A row that fails halfway still leaves
	// real red runs in the space, and leaving them unclaimed would red the
	// space-health row too — two findings for one cause, with the second one
	// pointing at nothing.
	pr, err := h.Part.Pull(ctx, h.Org, h.Repo, prNumber)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	// Every workflow run at this head SHA belongs to this probe: the branch
	// carries exactly the one probe commit, and both re-trigger mechanisms
	// below act on the same head. Claimed by OBSERVED ID — never by branch
	// name, which is the pattern that would absolve the next probe somebody
	// adds without its having asserted anything (spacehealth.go).
	defer func() {
		runs, lerr := h.Prov.ListWorkflowRuns(ctx, h.Org, h.Repo, pr.HeadSHA)
		if lerr != nil {
			// Best effort. An unclaimed red is REPORTED by the health row,
			// naming the run id — over-reporting, which is the safe
			// direction, and never a silent pass.
			return
		}
		ids := make([]int64, 0, len(runs))
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
		h.claimRed(ids...)
	}()

	res1, err := h.AwaitCheck(ctx, h.B, prNumber)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	if res1.Conclusion != "failure" {
		return boundaryResult(scenarioCrossSectionRetriggerStaysRed, SystemB, VerdictFail, expected,
			fmt.Sprintf("initial cross-section PR concluded %q, not failure — diff-authz did not refuse B writing into A's section", res1.Conclusion),
			fmt.Sprintf("PR #%d", prNumber))
	}

	// Mechanism (a): the literal "re-run CI" button — needs the
	// provisioner's Actions:write.
	runs, err := h.Prov.ListWorkflowRuns(ctx, h.Org, h.Repo, pr.HeadSHA)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	var runID int64
	for _, run := range runs {
		if run.Name == requiredWorkflowName {
			runID = run.ID
			break
		}
	}
	if runID == 0 {
		return boundaryRefused(scenarioCrossSectionRetriggerStaysRed, SystemB, expected,
			fmt.Sprintf("no %q workflow run found at head %s to re-run via the Actions API", requiredWorkflowName, pr.HeadSHA))
	}
	// baseline/baseline2 are recorded EVIDENCE, not the gate: see
	// boundaryCheckRunCount's own doc comment for why counting alone cannot
	// be trusted to gate this row, and boundaryAwaitCheckLeavesCompleted's
	// for the mechanism that actually does.
	baseline, err := boundaryCheckRunCount(ctx, h, pr.HeadSHA)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	if err := h.Prov.RerunWorkflow(ctx, h.Org, h.Repo, runID); err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	if err := boundaryAwaitCheckLeavesCompleted(ctx, h, h.B, prNumber); err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	res2, err := h.AwaitCheck(ctx, h.B, prNumber)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	afterRerunCount, countErr := boundaryCheckRunCount(ctx, h, pr.HeadSHA)
	if countErr != nil {
		afterRerunCount = -1 // evidence only; never fail the row over a read this assertion does not depend on
	}
	if res2.Conclusion != "failure" {
		return boundaryResult(scenarioCrossSectionRetriggerStaysRed, SystemB, VerdictFail, expected,
			fmt.Sprintf("after the provisioner's rerun-API re-trigger, the check concluded %q, not failure — the v0.6.4 GITHUB_ACTOR bypass is back", res2.Conclusion),
			fmt.Sprintf("PR #%d, rerun of workflow run %d, check-run count %d -> %d", prNumber, runID, baseline, afterRerunCount))
	}

	// Mechanism (b): close then reopen — needs only Pull requests:write, the
	// one the original report described, so it is the more important of the
	// two to assert. Re-baseline right before closing, so the recorded
	// count evidence below is not confused by mechanism (a)'s own count.
	baseline2, err := boundaryCheckRunCount(ctx, h, pr.HeadSHA)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	if err := h.Prov.SetPullState(ctx, h.Org, h.Repo, prNumber, "closed"); err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	if err := h.Prov.SetPullState(ctx, h.Org, h.Repo, prNumber, "open"); err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	if err := boundaryAwaitCheckLeavesCompleted(ctx, h, h.B, prNumber); err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	res3, err := h.AwaitCheck(ctx, h.B, prNumber)
	if err != nil {
		return boundaryFromErr(scenarioCrossSectionRetriggerStaysRed, SystemB, expected, err)
	}
	afterReopenCount, countErr := boundaryCheckRunCount(ctx, h, pr.HeadSHA)
	if countErr != nil {
		afterReopenCount = -1
	}
	if res3.Conclusion != "failure" {
		return boundaryResult(scenarioCrossSectionRetriggerStaysRed, SystemB, VerdictFail, expected,
			fmt.Sprintf("after the provisioner's close/reopen re-trigger, the check concluded %q, not failure — the v0.6.4 GITHUB_ACTOR bypass is back", res3.Conclusion),
			fmt.Sprintf("PR #%d, closed+reopened, check-run count %d -> %d", prNumber, baseline2, afterReopenCount))
	}

	return boundaryResult(scenarioCrossSectionRetriggerStaysRed, SystemB, VerdictPass, "", "",
		fmt.Sprintf("PR #%d stayed failure throughout: initial=%s, after rerun-API=%s (check-run count %d->%d), after close/reopen=%s (check-run count %d->%d) — the count is recorded evidence, not the gate (see boundaryCheckRunCount's own doc comment)",
			prNumber, res1.Conclusion, res2.Conclusion, baseline, afterRerunCount, res3.Conclusion, baseline2, afterReopenCount))
}

// boundaryPushMain fast-forward pushes dir's current HEAD onto main as
// token's identity. Deliberately NOT gitForcePush (provision_live.go): that
// helper is force-specific (ResetSpace's own rewrite-history need), and this
// call only ever needs to move main FORWARD by one ordinary commit —
// force-pushing it would risk running into `allow_force_pushes:false` for no
// reason (ResetSpace itself removes protection first before it force-pushes,
// which is evidence enough not to assume force is always permitted for an
// admin here). Auth follows the same per-invocation `-c http.extraheader`
// shape gitForcePush uses, reused as the PATTERN, not duplicated as the
// primitive (it omits --force, which is the whole point of this function
// existing separately).
func boundaryPushMain(ctx context.Context, dir, remoteURL, token string) error {
	basicAuth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
	return runCmd(ctx, "git",
		"-C", dir,
		"-c", "http.extraheader=AUTHORIZATION: basic "+basicAuth,
		"push", remoteURL, "HEAD:refs/heads/main",
	)
}

// boundaryMoveMainDirect lands ONE commit directly on main as the
// provisioner — the cheaper of the brief's two "move main" routes for
// executed-ref-not-stale (§T6-b: "move `main` (merge something else, or have
// the provisioner land a commit)"). A full submit -> wait-green -> merge
// cycle for a second artifact takes MINUTES, and that window is exactly long
// enough for the SUBJECT PR's own auto-merge (armed by `a2a submit`,
// internal/space/funnel.go Step 4) to land it before UpdateBranch ever runs
// below — turning this row's own setup into a false AC-960.5 red for a
// reason that has nothing to do with a stale ref. A direct push closes that
// window to seconds. Content is a normal, schema-valid envelope in alpha's
// own section — never through the funnel, so no section-guard or CC-085
// concern applies to this commit, but it stays a real artifact so it does
// not leave main in a shape another scenario's full-repo validation would
// trip over.
func boundaryMoveMainDirect(ctx context.Context, h *harness) error {
	work, err := os.MkdirTemp("", "livee2e-boundary-move-main-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	dir := filepath.Join(work, "raw")

	remoteURL := "https://github.com/" + h.Org + "/" + h.Repo + ".git"
	if err := runCmd(ctx, "git", "clone", "-q", remoteURL, dir); err != nil {
		return fmt.Errorf("clone: %w", err)
	}
	for _, args := range [][]string{
		{"-C", dir, "config", "user.name", "livee2e-boundary-move-main"},
		{"-C", dir, "config", "user.email", "livee2e-boundary-move-main@a2ahub.invalid"},
	} {
		if err := runCmd(ctx, "git", args...); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}

	id := boundaryEnvelopeID(systemAlpha)
	path := filepath.Join(dir, "alpha", "exchanges", id+".md")
	if err := os.WriteFile(path, []byte(boundaryProbeEnvelope(id)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := runCmd(ctx, "git", "-C", dir, "add", "-A"); err != nil {
		return fmt.Errorf("add: %w", err)
	}
	if err := runCmd(ctx, "git", "-C", dir, "commit", "-q", "-m", "chore: move main (executed-ref-not-stale)"); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if err := boundaryPushMain(ctx, dir, remoteURL, h.Prov.Token); err != nil {
		return fmt.Errorf("push main: %w", err)
	}
	return nil
}

// scenarioExecutedRefNotStale is row 15 (AC-960.5, §T6-b): GitHub can serve a
// check computed against a STALE merge commit after the base moved. Route
// chosen at plan time (do not invent another): B opens a PR; main moves
// (boundaryMoveMainDirect, so the window before UpdateBranch stays seconds,
// not minutes); the branch is force-recomputed via UpdateBranch so the PR's
// head SHA changes (called only after main has already moved, since
// UpdateBranch 422s on an already-current branch); the PR is re-read for its
// NEW head — the old value is exactly the stale read this row is about;
// then the check is awaited and ExecutedRefIsCurrent asserts the executed
// ref matches the new head, never the stale one.
const scenarioExecutedRefNotStale = "executed-ref-not-stale"

// boundaryStaleRefCeiling/Interval bound the poll below.
const (
	boundaryStaleRefCeiling  = 4 * time.Minute
	boundaryStaleRefInterval = 10 * time.Second
)

// boundaryAwaitCheckAtHead polls until the check the PRODUCT resolves for a PR
// is both completed AND computed against that PR's CURRENT head, returning how
// long the answer stayed stale.
//
// Asking once is not enough, and the first live run proved why: seconds after
// UpdateBranch moved the head, CheckStatus answered with a run that had
// executed against the PREVIOUS commit — GitHub really does serve the old
// ref's run for a window. That is exactly §T6-b, and a single-shot assertion
// turns it into a coin flip: fail if you asked early, pass if you asked late,
// and neither outcome tells you anything about the product.
//
// So the row asserts CONVERGENCE and reports the window. A verdict that never
// becomes current within the bound is the real defect this row exists to
// catch — that is when a caller reading Conclusion acts on a ref that is not
// the one under review.
func boundaryAwaitCheckAtHead(ctx context.Context, h *harness, prNumber int) (host.CheckStatusResult, string, time.Duration, error) {
	started := time.Now()
	deadline := started.Add(boundaryStaleRefCeiling)
	var lastRes host.CheckStatusResult
	var lastHead, lastDetail string

	for {
		pr, err := h.Part.Pull(ctx, h.Org, h.Repo, prNumber)
		if err != nil {
			return host.CheckStatusResult{}, "", 0, err
		}
		res, err := h.AwaitCheck(ctx, h.B, prNumber)
		if err != nil {
			return host.CheckStatusResult{}, "", 0, err
		}
		lastRes, lastHead = res, pr.HeadSHA

		ok, detail := ExecutedRefIsCurrent(pr.HeadSHA, resolvedCheckRun(res))
		lastDetail = detail
		if ok {
			return res, pr.HeadSHA, time.Since(started), nil
		}
		if time.Now().After(deadline) {
			return lastRes, lastHead, time.Since(started), fmt.Errorf(
				"the resolved check never became current within %s: %s", boundaryStaleRefCeiling, lastDetail)
		}
		select {
		case <-ctx.Done():
			return host.CheckStatusResult{}, "", 0, ctx.Err()
		case <-time.After(boundaryStaleRefInterval):
		}
	}
}

func boundaryExecutedRefNotStale(ctx context.Context, h *harness) Result {
	const expected = "the required check's executed HeadSHA equals the PR's NEW head after main moved and the branch was force-recomputed — never a stale merge commit (§T6-b)"

	sub, err := h.DraftAndSubmit(ctx, h.B, boundaryArtifactType)
	if err != nil {
		return boundaryFromErr(scenarioExecutedRefNotStale, SystemB, expected, err)
	}

	if err := boundaryMoveMainDirect(ctx, h); err != nil {
		return boundaryRefused(scenarioExecutedRefNotStale, SystemB, expected, "could not move main: "+err.Error())
	}

	if err := h.Part.UpdateBranch(ctx, h.Org, h.Repo, sub.PRNumber); err != nil {
		return boundaryFromErr(scenarioExecutedRefNotStale, SystemB, expected, fmt.Errorf("force-recompute: %w", err))
	}

	res, head, staleFor, err := boundaryAwaitCheckAtHead(ctx, h, sub.PRNumber)
	if err != nil {
		if v, ok := verdictForError(err); ok {
			return boundaryResult(scenarioExecutedRefNotStale, SystemB, v, expected, err.Error(), fmt.Sprintf("PR #%d", sub.PRNumber))
		}
		return boundaryResult(scenarioExecutedRefNotStale, SystemB, VerdictFail, expected, err.Error(), fmt.Sprintf("PR #%d", sub.PRNumber))
	}

	// The window is the evidence, not a footnote: it is the measured size of
	// the interval in which reading a conclusion would have meant reading the
	// wrong ref's verdict.
	return boundaryResult(scenarioExecutedRefNotStale, SystemB, VerdictPass, "", "",
		fmt.Sprintf("PR #%d: check run %q became current at head %s after %s of serving the previous ref",
			sub.PRNumber, resolvedCheckRun(res).Name, head, staleFor.Round(time.Second)))
}
