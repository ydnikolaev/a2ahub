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
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// runHappyScenarios drives the wave-3-2 adjudication table's rows 1-6 and 7
// (plan §"Per-row adjudication"): the ordinary paths, both systems where the
// catalogue declares both. It is this family's ONE exported-shape entry
// (plan §"Wave-3 design notes": "the fan-out seam is four functions, not a
// registry") — the lead-owned wave-3-3 runner calls it and Records what it
// returns.
//
// Every helper below is prefixed happy* so four concurrent scenario-family
// agents sharing this package cannot collide on an identifier.
func runHappyScenarios(ctx context.Context, h *harness) []Result {
	var results []Result
	appendOne := func(scenario, system string, surface Surface, run func() Result) {
		if h.cellSelected(scenario, system, surface) {
			results = append(results, run())
		}
	}
	appendOne("space-init-two-participants", SystemA, SurfaceCLI, func() Result { return happySpaceInit(ctx, h) })
	appendOne("submit-gate-merge", SystemA, SurfaceCLI, func() Result { return happySubmitGateMerge(ctx, h, SystemA, h.A) })
	appendOne("submit-gate-merge", SystemB, SurfaceCLI, func() Result { return happySubmitGateMerge(ctx, h, SystemB, h.B) })
	appendOne("lifecycle-transitions", SystemA, SurfaceCLI, func() Result { return happyLifecycleTransitions(ctx, h, SystemA, h.A) })
	appendOne("lifecycle-transitions", SystemB, SurfaceCLI, func() Result { return happyLifecycleTransitions(ctx, h, SystemB, h.B) })
	appendOne("contract-publish-deprecate-retire", SystemA, SurfaceCLI, func() Result { return happyContractLifecycle(ctx, h, SystemA, h.A) })
	appendOne("contract-publish-deprecate-retire", SystemB, SurfaceCLI, func() Result { return happyContractLifecycle(ctx, h, SystemB, h.B) })
	appendOne("cross-system-visibility", SystemA, SurfaceCLI, func() Result { return happyCrossSystemVisibility(ctx, h, SystemA, h.A, h.B) })
	appendOne("cross-system-visibility", SystemB, SurfaceCLI, func() Result { return happyCrossSystemVisibility(ctx, h, SystemB, h.B, h.A) })
	appendOne("validate-ci-both-modes", SystemA, SurfaceCLI, func() Result { return happyValidateCIBothModes(ctx, h) })
	appendOne("checkstatus-compound-context", SystemB, SurfaceCLI, func() Result { return happyCheckstatusCompoundContext(ctx, h) })

	appendMCPSubmit := func(system string, author, observer *checkout) {
		if !h.cellSelected("submit-gate-merge", system, SurfaceMCP) &&
			!h.cellSelected("cross-system-visibility", system, SurfaceMCP) {
			return
		}
		for _, result := range mcpSubmitAndVisibility(ctx, h, system, author, observer) {
			if h.cellSelected(result.Scenario, result.System, result.Surface) {
				results = append(results, result)
			}
		}
	}
	appendMCPSubmit(SystemA, h.A, h.B)
	appendMCPSubmit(SystemB, h.B, h.A)
	appendOne("lifecycle-transitions", SystemA, SurfaceMCP, func() Result { return mcpLifecycle(ctx, h, SystemA, h.A) })
	appendOne("lifecycle-transitions", SystemB, SurfaceMCP, func() Result { return mcpLifecycle(ctx, h, SystemB, h.B) })
	appendOne("contract-publish-deprecate-retire", SystemA, SurfaceMCP, func() Result { return mcpContractLifecycle(ctx, h, SystemA, h.A) })
	appendOne("contract-publish-deprecate-retire", SystemB, SurfaceMCP, func() Result { return mcpContractLifecycle(ctx, h, SystemB, h.B) })

	// Leave both checkouts parked on a clean main before returning — this
	// family never mutates SHARED space state (protection/space.yaml/
	// min_binary_version, the state-disjointness corollary's named
	// examples), but it does leave each checkout's own working tree on
	// whichever ephemeral branch its last write created. A checkout is
	// itself part of what the NEXT family (run serially, after this one)
	// reads through the same *harness, so parking it anywhere other than
	// main would hand that family a dirty starting point that looks like
	// its own bug. Best-effort: a failure here does not change any
	// already-recorded verdict above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// happyMergeWaitCeiling/Interval bound happyAwaitMerged's poll — GitHub's
// auto-merge fires asynchronously after the required check completes, so a
// green check and a not-yet-merged PR is expected for a few seconds, not a
// defect (spec 36 §T4: bounded waits, an honest "timed out", never an
// optimistic pass).
const (
	happyMergeWaitCeiling  = 3 * time.Minute
	happyMergeWaitInterval = 10 * time.Second
)

// happyVerdictForErr is now a thin delegation: the two classes it used to add
// locally — ErrNoPRForBranch and a bounded ctx expiring — moved INTO
// verdictForError once this family's finding was confirmed, so all four
// families share one mapping instead of three of them silently lacking it.
// Kept as a name so this family's call sites read unchanged; it adds only the
// "any other non-nil error is a real failure" tail.
func happyVerdictForErr(err error) (Verdict, bool) {
	if v, ok := verdictForError(err); ok {
		return v, ok
	}
	return VerdictFail, err != nil
}

// happyResultFromErr renders err into a Result via happyVerdictForErr —
// the one place every scenario below turns a harness/product error into a
// row, so the verdict mapping rule (brief §4) is applied consistently
// rather than re-decided at each call site. ErrNoPRForBranch's branch name
// is already embedded in its own error string (harness_live.go:
// `fmt.Errorf("%w: %s", ErrNoPRForBranch, branch)`), so mirroring it into
// Detail satisfies "the branch in Detail" without re-parsing the error.
func happyResultFromErr(scenario, system string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	res := Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI,
		Verdict: verdict, Expected: expected, Observed: err.Error(),
	}
	if errors.Is(err, ErrNoPRForBranch) {
		res.Detail = err.Error()
	}
	return res
}

// happyAwaitMerged polls h.Prov (read access to a public repo needs no
// specific identity) for prNumber's Merged flag until true, the required
// check concludes FAILURE, or the bounded wait expires. On success it
// returns the merged PR state, including the exact base-reachable merge
// commit the checkout must prove visible before a dependent transition
// starts.
//
// Three outcomes, and the middle one is the point: (PR, nil);
// (zero, ErrRequiredCheckFailed) when the gate went red, which no wait can
// undo; (zero, ErrCheckWaitTimedOut) when we genuinely did not wait long
// enough.
// Before the middle case existed, a red gate spent the full budget and then
// reported a TIMEOUT — which this tier's own rule reads as "we were
// impatient", not "the product is wrong". That is how a production blocker
// (the diff-authz base defect) survived a whole run unnamed.
func happyAwaitMerged(ctx context.Context, h *harness, prNumber int) (PullState, error) {
	deadline := time.Now().Add(happyMergeWaitCeiling)
	for {
		pr, err := h.Prov.Pull(ctx, h.Org, h.Repo, prNumber)
		if err != nil {
			return PullState{}, err
		}
		if pr.Merged {
			return pr, nil
		}
		// A red required check is DECISIVE — waiting cannot turn it into a
		// merge, and reporting it as a timeout is how a real defect hid for a
		// whole run on 2026-07-26. Asked every poll rather than only after the
		// budget expires, so the row fails in seconds with the reason instead
		// of in minutes without it.
		if runs, cerr := h.Prov.CheckRuns(ctx, h.Org, h.Repo, pr.HeadSHA); cerr == nil {
			for _, r := range runs {
				if r.Name == requiredCheckContext && r.Conclusion == "failure" {
					return PullState{}, fmt.Errorf("%w: PR #%d, check %q (mergeable_state=%q)",
						ErrRequiredCheckFailed, prNumber, r.Name, pr.MergeableState)
				}
			}
		}
		if time.Now().After(deadline) {
			return PullState{}, fmt.Errorf("%w: PR #%d not merged after %s (mergeable_state=%q)",
				ErrCheckWaitTimedOut, prNumber, happyMergeWaitCeiling, pr.MergeableState)
		}
		select {
		case <-ctx.Done():
			return PullState{}, ctx.Err()
		case <-time.After(happyMergeWaitInterval):
		}
	}
}

// happyProtectionDoc is the slice of GitHub's branch-protection GET response
// this file needs (both the legacy `contexts` list and the newer `checks`
// array — PutProtection, provision_live.go, writes via `checks`, but this
// reads defensively for either shape GitHub might echo back).
type happyProtectionDoc struct {
	RequiredStatusChecks struct {
		Contexts []string `json:"contexts"`
		Checks   []struct {
			Context string `json:"context"`
		} `json:"checks"`
	} `json:"required_status_checks"`
}

// happyRequiredCheckArmed reads main's branch protection live and reports
// whether requiredCheckContext is one of its required checks.
//
// This calls h.Prov.do directly rather than adding a new ghClient method:
// client_live.go is off-limits to this family (every existing file in
// internal/livee2e/ is), and `do` is the harness's own single retry/
// classify path (client_live.go) already reachable on *ghClient from
// anywhere in this package — reusing it here is exactly "the harness
// client stays the harness's" (plan §"Wave-3 design notes"), not a private
// copy of it. Recorded as a deviation: client_live.go has no GET-protection
// method of its own, so this is the one row in this family that reaches
// into ghClient.do rather than calling a named ghClient method.
func happyRequiredCheckArmed(ctx context.Context, h *harness) (bool, error) {
	path := "/repos/" + url.PathEscape(h.Org) + "/" + url.PathEscape(h.Repo) + "/branches/main/protection"
	var doc happyProtectionDoc
	if _, err := h.Prov.do(ctx, http.MethodGet, path, nil, &doc); err != nil {
		return false, err
	}
	for _, c := range doc.RequiredStatusChecks.Checks {
		if c.Context == requiredCheckContext {
			return true, nil
		}
	}
	for _, c := range doc.RequiredStatusChecks.Contexts {
		if c == requiredCheckContext {
			return true, nil
		}
	}
	return false, nil
}

// happySpaceParticipantsDoc is the slice of space.yaml this file needs to
// assert the reset scaffold named both participants (spec 36 §T1: `a2a
// space init` scaffolds the space, so this row also continuously proves the
// scaffolder against real GitHub).
type happySpaceParticipantsDoc struct {
	Participants []struct {
		System string   `yaml:"system"`
		Owners []string `yaml:"owners"`
	} `yaml:"participants"`
}

// happySpaceInit is row 1 (AC-960.1, SystemA only): the harness already
// reset the space via `a2a space init` before this family ran (newHarness
// folds ResetSpace in). This asserts the RESULT rather than re-running it —
// main carries a space.yaml naming both participants, a CODEOWNERS, and the
// required check armed on branch protection — read entirely through h.Prov
// (the branch-protection half) and the local mirror h.A's own `a2a connect`
// already fetched from main (the file-content half; no ghClient
// contents-read method exists — see happyRequiredCheckArmed's doc comment
// for why that ONE sub-assertion goes through h.Prov.do instead).
func happySpaceInit(ctx context.Context, h *harness) Result {
	const scenario = "space-init"

	// Sync FIRST. `a2a connect` leaves the mirror directory empty — proven
	// live, not assumed — so reading space.yaml straight after harness
	// construction reads a hole and reports the scaffold as missing when it
	// is actually fine. The product question this raises (should connect
	// populate the mirror?) is a backlog row, not this row's verdict.
	if _, stderr, syncErr := h.A.Run(ctx, "sync"); syncErr != nil {
		return happyResultFromErr(scenario, SystemA,
			fmt.Errorf("sync before reading the scaffold: %w: %s", syncErr, stderr),
			"a2a sync populates the mirror so the scaffold can be read")
	}
	mirrorDir := h.A.MirrorDir()

	raw, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "space.yaml present on main after the reset scaffold",
			Observed: err.Error(),
		}
	}
	var doc happySpaceParticipantsDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "space.yaml parses with a participants: list",
			Observed: err.Error(),
		}
	}
	systems := map[string]bool{}
	for _, p := range doc.Participants {
		systems[p.System] = true
	}
	if !systems[systemAlpha] || !systems[systemBravo] {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: fmt.Sprintf("space.yaml names both %q and %q as participants", systemAlpha, systemBravo),
			Observed: fmt.Sprintf("participants found: %v", doc.Participants),
		}
	}

	craw, err := os.ReadFile(filepath.Join(mirrorDir, "CODEOWNERS"))
	if err != nil {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "CODEOWNERS present on main after the reset scaffold",
			Observed: err.Error(),
		}
	}
	if !strings.Contains(string(craw), "@"+h.Pre.ProvisionerLogin) {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: fmt.Sprintf("CODEOWNERS names the provisioner %q as the trust-root owner", h.Pre.ProvisionerLogin),
			Observed: string(craw),
		}
	}

	armed, err := happyRequiredCheckArmed(ctx, h)
	if err != nil {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: fmt.Sprintf("branch protection on main requires %q", requiredCheckContext),
			Observed: "reading branch protection: " + err.Error(),
		}
	}
	if !armed {
		return Result{
			Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: fmt.Sprintf("branch protection on main requires %q", requiredCheckContext),
			Observed: "required check context not present in the live protection document",
		}
	}

	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("main: space.yaml names %s(owner=%s)+%s(owner=%s), CODEOWNERS roots on %s, protection requires %q",
			systemAlpha, h.Pre.ProvisionerLogin, systemBravo, h.Pre.ParticipantLogin, h.Pre.ProvisionerLogin, requiredCheckContext),
	}
}

// happySubmitGateMerge is row 2 (AC-960.2, A and B): draft+submit an
// announcement from system's own checkout, await the required check,
// expect conclusion "success", then confirm the PR merged with no human —
// the space's auto-merge, never a human review (announcements are outside
// CODEOWNERS' trust-root paths).
func happySubmitGateMerge(ctx context.Context, h *harness, system string, c *checkout) Result {
	const scenario = "submit-gate-merge"

	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("sync before a fresh submit: %w: %s", err, stderr),
			"a2a sync succeeds before a fresh submit")
	}

	sub, err := h.DraftAndSubmit(ctx, c, "announcement")
	if err != nil {
		return happyResultFromErr(scenario, system, err, "draft+submit an announcement opens a PR")
	}

	res, err := h.AwaitCheck(ctx, c, sub.PRNumber)
	if err != nil {
		return happyResultFromErr(scenario, system, err,
			fmt.Sprintf("PR #%d's required check reaches a terminal state", sub.PRNumber))
	}
	if res.Conclusion != "success" {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "required check concludes success",
			Observed: fmt.Sprintf("state=%q conclusion=%q", res.State, res.Conclusion),
			Detail:   fmt.Sprintf("PR #%d (%s)", sub.PRNumber, sub.ID),
		}
	}

	if _, err := happyAwaitMerged(ctx, h, sub.PRNumber); err != nil {
		return happyResultFromErr(scenario, system, err,
			fmt.Sprintf("PR #%d auto-merges with no human once the required check is green", sub.PRNumber))
	}

	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: PR #%d green + auto-merged, no human", sub.ID, sub.PRNumber),
	}
}

// happyLifecycleTransitionsSlug is the fixed, lowercase-kebab slug (spec 03
// §3.3's standingSlugShape) every lifecycle-transitions requirement drafts
// under. Fixed rather than per-system: the standing id already carries the
// system (XR-<system>-<slug>), so two systems drafting the SAME slug still
// mint two distinct ids — a per-system suffix would only need to avoid the
// catalogue's own SystemA/SystemB labels ("A"/"B"), which are uppercase and
// would violate the slug's lowercase-kebab grammar.
const happyLifecycleTransitionsSlug = "matrix-lifecycle-req"

// happyLifecycleTransitions is row 3 (A and B): a real transition sequence
// through the funnel — publish (the first `a2a submit` on a requirement) then
// `a2a withdraw` on the SAME artifact, by the SAME system. This is the
// P30(a) defect class directly: two distinct writes by one system on one
// artifact must not collapse into one branch/PR (BranchName keys on VERB,
// not just artifact — funnel.go's own doc comment on the historical "ack
// then accept lost the accept" defect). No `a2a sync` between the two
// writes: withdraw must see the publish event that only exists on the
// submit's own local branch so far (commitOne forks the next write from
// CURRENT HEAD, which the write funnel deliberately leaves the working tree
// parked on) — this is the intended chaining, not a state leak.
// happyLandAndSync waits for a submitted write to actually LAND on main and
// then refreshes the checkout's mirror.
//
// Both multi-write rows need it and neither had it, which is why both failed
// live with "cannot read <id>": a second transition (`withdraw`, `contract
// deprecate`) reads the artifact out of the local mirror, and the artifact is
// only there once its PR merged AND a sync pulled it down. Submitting and
// immediately transitioning asks the CLI to act on something that does not
// exist yet anywhere it can see — the harness's mistake, not the product's.
func happyLandAndSync(ctx context.Context, h *harness, c *checkout, prNumber int) error {
	if _, err := h.AwaitCheck(ctx, c, prNumber); err != nil {
		return fmt.Errorf("waiting for PR #%d's required check: %w", prNumber, err)
	}
	merged, err := happyAwaitMerged(ctx, h, prNumber)
	if err != nil {
		return fmt.Errorf("waiting for PR #%d to merge: %w", prNumber, err)
	}
	if merged.MergeCommitSHA == "" {
		return fmt.Errorf("waiting for PR #%d to merge: GitHub returned an empty merge_commit_sha", prNumber)
	}
	if err := awaitSyncVisibility(
		ctx,
		mergeVisibilityAttempts,
		func() error {
			_, stderr, syncErr := c.Run(ctx, "sync")
			if syncErr != nil {
				return fmt.Errorf("sync after PR #%d merged: %w: %s", prNumber, syncErr, stderr)
			}
			return nil
		},
		func() (bool, error) {
			return c.MainContains(ctx, merged.MergeCommitSHA)
		},
		func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(mergeVisibilityPollInterval):
				return nil
			}
		},
	); err != nil {
		return fmt.Errorf("waiting for PR #%d merge commit %s to reach origin/main: %w", prNumber, merged.MergeCommitSHA, err)
	}
	return nil
}

func happyLifecycleTransitions(ctx context.Context, h *harness, system string, c *checkout) Result {
	const scenario = "lifecycle-transitions"

	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("sync before a fresh write pair: %w: %s", err, stderr),
			"a2a sync succeeds before a fresh publish+withdraw pair")
	}

	sub, err := h.DraftAndSubmit(ctx, c, "requirement", "--slug", happyLifecycleTransitionsSlug)
	if err != nil {
		return happyResultFromErr(scenario, system, err, "publish (the first submit) opens its own PR")
	}

	if err := happyLandAndSync(ctx, h, c, sub.PRNumber); err != nil {
		return happyResultFromErr(scenario, system, err,
			"the published requirement lands on main and reaches the mirror before withdraw reads it")
	}

	if _, stderr, err := c.Run(ctx, "withdraw", sub.ID); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("a2a withdraw %s: %w: %s", sub.ID, err, stderr),
			"withdraw on the just-published requirement opens a SECOND, distinct PR")
	}

	withdrawPR, err := h.pullForBranch(ctx, space.BranchName(c.System, "withdraw", sub.ID))
	if err != nil {
		return happyResultFromErr(scenario, system, err, "withdraw's own branch has an open PR")
	}

	if withdrawPR.Number == sub.PRNumber {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "publish and withdraw open DISTINCT PRs (P30(a): the branch is keyed on verb, not just artifact)",
			Observed: fmt.Sprintf("both writes resolved to PR #%d", sub.PRNumber),
			Detail:   sub.ID,
		}
	}
	if err := happyLandAndSync(ctx, h, c, withdrawPR.Number); err != nil {
		return happyResultFromErr(scenario, system, err,
			"the final withdraw PR passes its required check, lands, and leaves main stable for the next scenario")
	}

	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: publish PR #%d, withdraw PR #%d — distinct", sub.ID, sub.PRNumber, withdrawPR.Number),
	}
}

// happyContractLifecycle is row 4 (A and B): the full contract lifecycle —
// submit (publish), `a2a contract deprecate`, `a2a contract retire` — the
// SAME P30(a) concern as lifecycle-transitions, now over THREE writes on one
// contract by one system. `--sunset` is set safely in the past so retire's
// precondition never depends on it: with zero registered consumers (this
// space never ran `a2a contract adopt`), CheckRetirePrecondition returns
// (nil, nil) before SunsetPassed is even consulted (internal/validate/
// policy_retire.go) — retire is expected to succeed ungated, no --override.
func happyContractLifecycle(ctx context.Context, h *harness, system string, c *checkout) Result {
	const scenario = "contract-publish-deprecate-retire"

	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("sync before a fresh contract lifecycle: %w: %s", err, stderr),
			"a2a sync succeeds before a fresh publish/deprecate/retire sequence")
	}

	slug := liveRunSlug("matrix-contract-lifecycle", h.PRFloor)
	sub, err := h.DraftAndSubmit(ctx, c, "contract", "--slug", slug)
	if err != nil {
		return happyResultFromErr(scenario, system, err, "submit publishes a new contract, opening its own PR")
	}

	if err := happyLandAndSync(ctx, h, c, sub.PRNumber); err != nil {
		return happyResultFromErr(scenario, system, err,
			"the published contract lands on main and reaches the mirror before deprecate reads it")
	}

	successor := sub.ID + "-successor@2.0.0"
	deprecateStdout, stderr, err := c.Run(ctx, "contract", "deprecate", sub.ID, "--successor", successor, "--sunset", "2020-01-01")
	if err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("a2a contract deprecate %s: %w: %s", sub.ID, err, stderr),
			"contract deprecate opens its own, distinct PR")
	}
	// P36 defect fix: `contract deprecate` batches the deprecated contract
	// AND its linked deprecation announcement into ONE write
	// (lifecycleDeps.buildRequest sorts+joins both ids with "+"), so its
	// branch is a2a/<sys>/contract-deprecate/<XA-id>+<XC-id> — not the bare
	// contract id pullForBranch's exact-HeadRef lookup expects. Both live
	// runs timed out here ("submit left no pull request on its branch") for
	// a PR that had, in fact, opened, gone green, and auto-merged exactly as
	// intended — the harness was asking for the wrong branch name, not the
	// product failing to open one. pullForBranchContaining (harness_live.go)
	// is the composite-aware lookup.
	deprecatePR, err := h.pullForBranchContaining(ctx, c.System, "contract-deprecate", sub.ID)
	if err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("%w; command stdout: %q", err, strings.TrimSpace(deprecateStdout)),
			"contract deprecate's own branch has an open PR")
	}

	if err := happyLandAndSync(ctx, h, c, deprecatePR.Number); err != nil {
		return happyResultFromErr(scenario, system, err,
			"the deprecation lands on main and reaches the mirror before retire reads it (retire needs a prior deprecate)")
	}

	retireStdout, stderr, err := c.Run(ctx, "contract", "retire", sub.ID)
	if err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("a2a contract retire %s: %w: %s", sub.ID, err, stderr),
			"contract retire opens its own, distinct PR (no registered consumers, so it succeeds ungated)")
	}
	retirePR, err := h.pullForBranch(ctx, space.BranchName(c.System, "contract-retire", sub.ID))
	if err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("%w; command stdout: %q", err, strings.TrimSpace(retireStdout)),
			"contract retire's own branch has an open PR")
	}

	if sub.PRNumber == deprecatePR.Number || deprecatePR.Number == retirePR.Number || sub.PRNumber == retirePR.Number {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "publish, deprecate and retire open THREE distinct PRs (P30(a))",
			Observed: fmt.Sprintf("publish=#%d deprecate=#%d retire=#%d", sub.PRNumber, deprecatePR.Number, retirePR.Number),
			Detail:   sub.ID,
		}
	}
	if err := happyLandAndSync(ctx, h, c, retirePR.Number); err != nil {
		return happyResultFromErr(scenario, system, err,
			"the final retirement PR passes its required check, lands, and leaves main stable for the next scenario")
	}

	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: publish PR #%d, deprecate PR #%d, retire PR #%d — all distinct",
			sub.ID, sub.PRNumber, deprecatePR.Number, retirePR.Number),
	}
}

// happyCrossSystemVisibility is row 5 (A and B, the P30(c) class): after
// author merges a write addressed to observer (every drafted announcement
// is addressed to the peer by draftFieldArgs), the
// OTHER system's `a2a sync` then `a2a inbox` must actually SEE it — sync
// must MOVE the mirror's working tree, not merely fetch refs
// (space/mirror.go's checkoutRemoteHead exists for exactly this defect
// class). system names WHOSE write this row is about (the author), matching
// submit-gate-merge's own System-labels-the-author convention.
func happyCrossSystemVisibility(ctx context.Context, h *harness, system string, author, observer *checkout) Result {
	const scenario = "cross-system-visibility"

	if _, stderr, err := author.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("author sync before submit: %w: %s", err, stderr),
			"a2a sync succeeds before a fresh cross-system write")
	}

	sub, err := h.DraftAndSubmit(ctx, author, "announcement")
	if err != nil {
		return happyResultFromErr(scenario, system, err, "draft+submit an announcement addressed to the peer opens a PR")
	}

	res, err := h.AwaitCheck(ctx, author, sub.PRNumber)
	if err != nil {
		return happyResultFromErr(scenario, system, err,
			fmt.Sprintf("PR #%d's required check reaches a terminal state", sub.PRNumber))
	}
	if res.Conclusion != "success" {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "required check concludes success",
			Observed: fmt.Sprintf("state=%q conclusion=%q", res.State, res.Conclusion),
			Detail:   fmt.Sprintf("PR #%d (%s)", sub.PRNumber, sub.ID),
		}
	}

	if _, err := happyAwaitMerged(ctx, h, sub.PRNumber); err != nil {
		return happyResultFromErr(scenario, system, err,
			fmt.Sprintf("PR #%d merges so the write reaches main", sub.PRNumber))
	}

	if _, stderr, err := observer.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("observer sync: %w: %s", err, stderr),
			"the observer's a2a sync fetches the merged commit AND moves its working tree onto it")
	}

	stdout, stderr, err := observer.Run(ctx, "inbox", "--json")
	if err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("observer inbox: %w: %s", err, stderr),
			"the observer's a2a inbox lists the just-merged announcement")
	}
	if !strings.Contains(stdout, sub.ID) {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: fmt.Sprintf("the observer's `a2a inbox --json` lists %s after sync (P30(c): sync must move the mirror's working tree, not just fetch refs)", sub.ID),
			Observed: "id not present in inbox output",
			Detail:   stdout,
		}
	}

	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s merged (PR #%d) by %s, observed in the peer's inbox after sync", sub.ID, sub.PRNumber, author.System),
	}
}

// happyValidateCIBothModes is row 6 (AC-960.2, SystemA only): `a2a validate
// --ci` in both modes against the real space — v3-pr on a live PR's own
// diff, v3-full-repo on the synced mirror — expecting exit 0 on a clean
// tree (internal/cli/cmd_validate_ci_test.go's own flag shapes: --mode,
// --base, --author). --author is passed explicitly as the provisioner's
// login (alpha's own CODEOWNERS/space.yaml owner) rather than relying on an
// ambient GITHUB_ACTOR this local run does not set.
func happyValidateCIBothModes(ctx context.Context, h *harness) Result {
	const scenario = "validate-ci-both-modes"
	const system = SystemA
	c := h.A

	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("sync before submit: %w: %s", err, stderr),
			"a2a sync succeeds before a fresh submit")
	}

	sub, err := h.DraftAndSubmit(ctx, c, "announcement")
	if err != nil {
		return happyResultFromErr(scenario, system, err, "draft+submit opens a live PR to validate against")
	}

	if stdout, stderr, err := c.RunIn(ctx, c.MirrorDir(), "validate", "--ci", "--mode=v3-pr", "--base=main", "--author="+h.Pre.ProvisionerLogin); err != nil {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "validate --ci --mode=v3-pr exits 0 on the just-submitted PR's own diff",
			Observed: err.Error(),
			Detail:   fmt.Sprintf("PR #%d; stdout=%s stderr=%s", sub.PRNumber, stdout, stderr),
		}
	}

	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("sync back to main before the full-repo pass: %w: %s", err, stderr),
			"a2a sync returns the checkout to main before validate --ci --mode=v3-full-repo")
	}

	if stdout, stderr, err := c.RunIn(ctx, c.MirrorDir(), "validate", "--ci", "--mode=v3-full-repo"); err != nil {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "validate --ci --mode=v3-full-repo exits 0 on the clean synced mirror",
			Observed: err.Error(),
			Detail:   fmt.Sprintf("stdout=%s stderr=%s", stdout, stderr),
		}
	}

	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("v3-pr against PR #%d's own diff, then v3-full-repo against the synced mirror — both exit 0", sub.PRNumber),
	}
}

// happyCheckstatusCompoundContext is row 7 (AC-961.1, SystemB only): asserts
// that h.AwaitCheck — which goes THROUGH the product's own host.CheckStatus
// resolver, never a private copy of its selection rule — resolved the REAL
// compound context. Name must be exactly requiredCheckContext and Ambiguous
// must be empty; this is the P34 defect class's live evidence, and it also
// settles spec 34 §6's open spacing question empirically (both compared
// against the SAME literal `provision_live.go` used to arm branch
// protection with, so a mismatch here is either the check's own spacing or
// this rig's, never both silently agreeing on the wrong string).
func happyCheckstatusCompoundContext(ctx context.Context, h *harness) Result {
	const scenario = "checkstatus-compound-context"
	const system = SystemB
	c := h.B

	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return happyResultFromErr(scenario, system, fmt.Errorf("sync before submit: %w: %s", err, stderr),
			"a2a sync succeeds before a fresh submit")
	}

	sub, err := h.DraftAndSubmit(ctx, c, "announcement")
	if err != nil {
		return happyResultFromErr(scenario, system, err, "draft+submit opens a PR to observe the required check on")
	}

	res, err := h.AwaitCheck(ctx, c, sub.PRNumber)
	if err != nil {
		return happyResultFromErr(scenario, system, err,
			fmt.Sprintf("PR #%d's required check reaches a terminal state", sub.PRNumber))
	}

	if res.Name != requiredCheckContext || len(res.Ambiguous) != 0 {
		return Result{
			Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: fmt.Sprintf("host.CheckStatus resolves the check named exactly %q, unambiguously", requiredCheckContext),
			Observed: fmt.Sprintf("name=%q ambiguous=%v", res.Name, res.Ambiguous),
			Detail:   fmt.Sprintf("PR #%d (%s)", sub.PRNumber, sub.ID),
		}
	}

	return Result{
		Scenario: scenario, System: system, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("host.CheckStatus resolved Name=%q on PR #%d — the live P34 evidence, settling spec 34 §6's spacing question", res.Name, sub.PRNumber),
	}
}
