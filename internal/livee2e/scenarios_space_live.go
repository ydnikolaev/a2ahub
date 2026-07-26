//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// spaceRestoreBudget bounds the detached context the drift restore runs on.
// Generous enough for a clone-less commit + push, small enough that a wedged
// restore cannot hold the run open indefinitely.
const spaceRestoreBudget = 90 * time.Second

// runSpaceScenarios drives the space-lifecycle rows of the live matrix (spec
// 36 plan "Per-row adjudication" #13-14): `a2a space update`'s fine-grained-
// credential scope pre-flight (participant B, AC-961.5, the v0.6.3 class) and
// a real update plan driven against DELIBERATELY induced drift (provisioner
// A). Both rows only ever return VerdictPass/VerdictFail/VerdictRefused —
// never VerdictNotRun (Run.Record refuses it).
func runSpaceScenarios(ctx context.Context, h *harness) []Result {
	return []Result{
		spaceLiveUpdateScopePreflightScenario(ctx, h),
		spaceLiveUpdateScenario(ctx, h),
	}
}

// spaceLiveScopeAdvisory is the EXACT first line `a2a space update` prints
// (internal/cli/cmd_space.go: spaceCheckWorkflowScope's !reported branch,
// c197e54) when the write credential is a fine-grained PAT or App token —
// which never advertises OAuth scopes at all (internal/host TokenScopes
// reported=false). It is the row's whole evidence: a participant token
// (spec 36 §9.5.3 — always fine-grained) can never take the hard-refusal
// (reported=true, missing `workflow`) path a classic token would, so the only
// LIVE-OBSERVABLE shape of AC-961.5 is this advisory printed BEFORE any git
// action, never a raw push rejection. Copied verbatim from source, not
// guessed.
const spaceLiveScopeAdvisory = "space update: scope could not be verified for this credential type (fine-grained PAT or GitHub App token does not advertise scopes) — the write may still be refused by GitHub if the token lacks Workflows permission"

// spaceLiveUpdateScopePreflightScenario drives plan row 13
// (space-update-scope-preflight, SystemB, AC-961.5).
//
// It runs `a2a space update` from the PARTICIPANT's own checkout — the
// credential whose fine-grained PAT can never take the classic-token hard-
// refusal path — and asserts spaceLiveScopeAdvisory is the very FIRST line of
// stdout. "First line" is the precise reading of "before any git action":
// runUpdate (cmd_space.go) prints the scope note before the version guard,
// before computing a plan, and before any funnel Submit call, so nothing
// legitimate can precede it. Whether the run then succeeds ("already
// current", the common case on a freshly reset space) or fails later is
// irrelevant to this row — the row is about ORDER, not outcome.
func spaceLiveUpdateScopePreflightScenario(ctx context.Context, h *harness) Result {
	const scenario = "space-update-scope-preflight"
	res := Result{
		Scenario: scenario,
		System:   SystemB,
		Surface:  SurfaceCLI,
		Expected: "a fine-grained participant credential produces the cannot-verify-workflow-scope advisory as the FIRST line of `a2a space update`'s output, before any git action — never a raw push rejection (AC-961.5, c197e54)",
	}

	stdout, stderr, runErr := h.B.Run(ctx, "space", "update")
	firstLine, _, _ := strings.Cut(stdout, "\n")

	switch {
	case firstLine == spaceLiveScopeAdvisory:
		res.Verdict = VerdictPass
		res.Detail = fmt.Sprintf("first stdout line: %q", firstLine)
		if runErr != nil {
			res.Detail += fmt.Sprintf("; command then exited non-zero (%v) — the advisory still printed first, which is the amended AC-961.5 shape, not a regression", runErr)
		}
	case runErr != nil:
		res.Verdict = VerdictFail
		res.Observed = fmt.Sprintf("a2a space update (%s) exited %v WITHOUT the advisory as its first line", h.B.System, runErr)
		res.Detail = fmt.Sprintf("stdout: %q | stderr: %q", strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	default:
		res.Verdict = VerdictFail
		res.Observed = fmt.Sprintf("a2a space update (%s) succeeded but stdout's first line was %q, not the cannot-verify advisory", h.B.System, firstLine)
		res.Detail = fmt.Sprintf("full stdout: %q", strings.TrimSpace(stdout))
	}
	return res
}

// spaceLiveDriftFloor is a semver guaranteed to sort below any real template
// floor this repo will ever ship — row 14's own DELIBERATE drift, never a
// real version.
const spaceLiveDriftFloor = "0.0.1"

// spaceLiveMinVersionLine matches space.yaml's `min_binary_version:` line,
// capturing the key+whitespace prefix separately from the semver value so it
// can be rewritten without disturbing anything else on the line or file. This
// is a scenario-local rewrite (row 14's own drift injection), not a copy of
// internal/cli's field-managed update logic: that package's own pattern
// (cmd_space.go spaceMinVersionPattern) is unexported in a different package,
// and what it does — reconcile the whole update plan — is a different
// question from "make one field wrong on purpose, then put it back".
var spaceLiveMinVersionLine = regexp.MustCompile(`(?m)^(min_binary_version:\s*)([0-9]+\.[0-9]+\.[0-9]+)\b`)

// spaceLiveLowerFloor rewrites raw's min_binary_version down to
// spaceLiveDriftFloor, returning the ORIGINAL value so the caller can restore
// it later. ok is false when space.yaml carries no such line — a scaffold
// invariant broken deeply enough that drifting it further would prove
// nothing.
func spaceLiveLowerFloor(raw []byte) (updated []byte, original string, ok bool) {
	m := spaceLiveMinVersionLine.FindSubmatch(raw)
	if m == nil {
		return nil, "", false
	}
	return spaceLiveMinVersionLine.ReplaceAll(raw, []byte("${1}"+spaceLiveDriftFloor)), string(m[2]), true
}

// spaceLiveSetFloor rewrites raw's min_binary_version to floor — used both to
// drift (via spaceLiveLowerFloor above) and, symmetrically, to restore.
func spaceLiveSetFloor(raw []byte, floor string) []byte {
	return spaceLiveMinVersionLine.ReplaceAll(raw, []byte("${1}"+floor))
}

// spaceLiveRemoteURL is the space repo's authenticated git remote — the same
// shape newHarness/ResetSpace build (harness_live.go, provision_live.go), not
// re-derived from anything guessed.
func spaceLiveRemoteURL(h *harness) string {
	return "https://github.com/" + h.Org + "/" + h.Repo + ".git"
}

// spaceLiveCloneSpace clones a SCRATCH copy of the live space's main branch —
// deliberately NOT either checkout's own mirror, so drifting main for this
// row can never race or interleave with what `a2a init`/`connect` set up for
// A or B. The returned cleanup always removes the temp dir; callers must
// defer it unconditionally.
func spaceLiveCloneSpace(ctx context.Context, h *harness) (dir string, cleanup func(), err error) {
	noop := func() {}
	work, err := os.MkdirTemp("", "livee2e-space-update-*")
	if err != nil {
		return "", noop, fmt.Errorf("livee2e: space-update: mkdtemp: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(work) }

	cloneDir := filepath.Join(work, "space")
	remote := spaceLiveRemoteURL(h)
	if err := runCmd(ctx, "git", "clone", "--quiet", "--branch", "main", remote, cloneDir); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("livee2e: space-update: clone %s: %w", remote, err)
	}
	return cloneDir, cleanup, nil
}

// spaceLiveCommitAndPush commits space.yaml's CURRENT on-disk content in
// cloneDir with message and force-pushes it straight to main, as the
// PROVISIONER — the same "admin pushes main directly" shape ResetSpace
// already establishes (provision_live.go), reused via gitForcePush rather
// than re-implemented.
func spaceLiveCommitAndPush(ctx context.Context, h *harness, cloneDir, message string) error {
	steps := [][]string{
		{"-C", cloneDir, "config", "user.name", "a2a live-e2e (space-update drift)"},
		{"-C", cloneDir, "config", "user.email", "live-e2e@a2ahub.invalid"},
		{"-C", cloneDir, "add", "space.yaml"},
		{"-C", cloneDir, "commit", "-q", "-m", message},
	}
	for _, args := range steps {
		if err := runCmd(ctx, "git", args...); err != nil {
			return err
		}
	}
	return gitForcePush(ctx, cloneDir, spaceLiveRemoteURL(h), h.Cfg.ProvisionerToken, "HEAD", "main")
}

// spaceLiveUpdateScenario drives plan row 14 (space-update, SystemA).
//
// The harness scaffolds the space from the CURRENT template (ResetSpace runs
// `a2a space init` fresh), so a plain `space update` finds nothing to do —
// proving little. This makes the space drift for real first: as the
// PROVISIONER (admin — enforce_admins:false lets an admin push main directly,
// the same property row 12 relies on), it force-pushes main with
// min_binary_version lowered below the template's floor. It then runs
// `a2a space update --dry-run` from the provisioner's own checkout — dry-run
// so the exercise reads the command's OWN plan and directive output without
// opening a PR or writing anything on the real space — and asserts:
//   - a real field-managed plan line for space.yaml's min_binary_version,
//   - BOTH scope:space directives (branch-protection rename, secret deletion)
//     rendered as directives (text a2a prints), never attempted (a2a's Host
//     interface exposes no branch-protection primitive at all — structural,
//     not just --dry-run's doing),
//   - --dry-run's own confirmation that nothing was written or pushed.
//
// Whatever the outcome, main is restored to its original min_binary_version
// in a defer — the corollary spec 36's wave-3-2 brief states explicitly: a
// family that mutates shared space state must restore it before returning.
func spaceLiveUpdateScenario(ctx context.Context, h *harness) (res Result) {
	const scenario = "space-update"
	res = Result{
		Scenario: scenario,
		System:   SystemA,
		Surface:  SurfaceCLI,
		Expected: "a2a space update --dry-run computes a real plan for the induced min_binary_version drift and prints both scope:space directives, having written and pushed nothing",
	}

	cloneDir, cleanup, err := spaceLiveCloneSpace(ctx, h)
	if err != nil {
		res.Verdict = VerdictRefused
		res.Observed = fmt.Sprintf("could not clone the space to induce drift: %v", err)
		return res
	}
	defer cleanup()

	spacePath := filepath.Join(cloneDir, "space.yaml")
	raw, err := os.ReadFile(spacePath)
	if err != nil {
		res.Verdict = VerdictRefused
		res.Observed = fmt.Sprintf("could not read the cloned space.yaml: %v", err)
		return res
	}
	drifted, original, ok := spaceLiveLowerFloor(raw)
	if !ok {
		res.Verdict = VerdictRefused
		res.Observed = "the cloned space.yaml carries no min_binary_version line to drift"
		return res
	}
	if err := os.WriteFile(spacePath, drifted, 0o644); err != nil {
		res.Verdict = VerdictRefused
		res.Observed = fmt.Sprintf("could not write the drifted space.yaml: %v", err)
		return res
	}
	if err := spaceLiveCommitAndPush(ctx, h, cloneDir,
		"chore(live-e2e): lower min_binary_version to force a real space-update plan"); err != nil {
		res.Verdict = VerdictRefused
		res.Observed = fmt.Sprintf("could not push the induced drift to main: %v", err)
		return res
	}

	// Binding corollary: main must never stay drifted, regardless of what the
	// exercise below observes. Runs AFTER the drift push above (LIFO), using
	// the same clone, before the temp-dir cleanup deferred above it runs.
	//
	// On a DETACHED context with its own budget, never the caller's. `ctx`
	// here is the whole run's single 75-minute ceiling, already spent down by
	// every family and every bounded check-wait before this point — and the
	// exercise immediately below can spend more. Restoring on a context that
	// may already be dead is how "the restore is deferred" becomes "the
	// restore was skipped", and this row lowers a live space's write floor.
	// The refusal family reached the same conclusion for the same reason.
	defer func() {
		restoreCtx, cancelRestore := context.WithTimeout(context.Background(), spaceRestoreBudget)
		defer cancelRestore()

		restored := spaceLiveSetFloor(drifted, original)
		if writeErr := os.WriteFile(spacePath, restored, 0o644); writeErr != nil {
			res.Verdict = VerdictFail
			res.Observed = joinNonEmpty(res.Observed, fmt.Sprintf("FAILED TO RESTORE min_binary_version: %v", writeErr))
			return
		}
		if pushErr := spaceLiveCommitAndPush(restoreCtx, h, cloneDir,
			"chore(live-e2e): restore min_binary_version after space-update drive"); pushErr != nil {
			res.Verdict = VerdictFail
			res.Observed = joinNonEmpty(res.Observed, fmt.Sprintf("FAILED TO RESTORE min_binary_version: %v", pushErr))
		}
	}()

	stdout, stderr, runErr := h.A.Run(ctx, "space", "update", "--dry-run")
	if runErr != nil {
		res.Verdict = VerdictFail
		res.Observed = fmt.Sprintf("a2a space update --dry-run (%s) failed: %v", h.A.System, runErr)
		res.Detail = fmt.Sprintf("stdout: %q | stderr: %q", strings.TrimSpace(stdout), strings.TrimSpace(stderr))
		return res
	}

	var missing []string
	if !strings.Contains(stdout, "field-update space.yaml") {
		missing = append(missing, "a field-update plan line for space.yaml's min_binary_version")
	}
	if !strings.Contains(stdout, "scope: space directive") {
		missing = append(missing, "the scope: space directive lines")
	}
	if !strings.Contains(stdout, "checks[][context]=a2a-validate / validate") {
		missing = append(missing, "the branch-protection rename directive command")
	}
	if !strings.Contains(stdout, "gh secret delete A2A_BINARY_FETCH_TOKEN") {
		missing = append(missing, "the secret-deletion directive command")
	}
	if !strings.Contains(stdout, "no files written, nothing pushed") {
		missing = append(missing, "--dry-run's own confirmation that nothing was written or pushed")
	}

	if len(missing) > 0 {
		res.Verdict = VerdictFail
		res.Observed = "missing: " + strings.Join(missing, "; ")
		res.Detail = strings.TrimSpace(stdout)
		return res
	}

	res.Verdict = VerdictPass
	res.Detail = fmt.Sprintf("drifted min_binary_version %s -> %s; a2a space update --dry-run reported a real plan and both scope:space directives; drift restored on return", original, spaceLiveDriftFloor)
	return res
}

// joinNonEmpty appends addition to base with "; " when base is already
// non-empty, so a restore failure recorded in a defer never clobbers an
// Observed message the primary exercise already set.
func joinNonEmpty(base, addition string) string {
	if base == "" {
		return addition
	}
	return base + "; " + addition
}

// scenarioSpaceCIHealthy is the row that looks at the repository rather
// than at the checks this matrix asked for.
const scenarioSpaceCIHealthy = "space-ci-has-no-unexplained-failures"

// runSpaceCIHealth asks GitHub what ELSE failed in the space while the
// matrix was working, and refuses a clean verdict if anything did.
//
// It exists because on 2026-07-26 the tier reported 38/38 green twice over
// while the space's own Actions tab was red, and both times the operator
// found out by email. Every other row here asserts a check this matrix
// deliberately caused; nothing looked at the rest of the repository it had
// just spent an hour writing to.
//
// Only failed runs pay for a second API call: the job count is what tells an
// unloadable workflow (zero jobs, no check ever reported) apart from a job
// that ran and failed, and fetching it for every run would triple this row's
// cost to learn nothing about the green ones.
func runSpaceCIHealth(ctx context.Context, h *harness, since string) Result {
	const expected = "no workflow run in the space failed for a reason this matrix did not deliberately cause"
	res := func(v Verdict, observed, detail string) Result {
		return Result{Scenario: scenarioSpaceCIHealthy, System: SystemA, Surface: SurfaceCLI,
			Verdict: v, Expected: expected, Observed: observed, Detail: detail}
	}

	runs, err := h.Prov.ListRunsSince(ctx, h.Org, h.Repo, since)
	if err != nil {
		if v, ok := verdictForError(err); ok {
			return res(v, err.Error(), "")
		}
		return res(VerdictFail, "could not list the space's workflow runs: "+err.Error(), "")
	}

	for i := range runs {
		if runs[i].Status == "completed" && runs[i].Conclusion == "failure" {
			n, cerr := h.Prov.CountJobs(ctx, h.Org, h.Repo, runs[i].ID)
			if cerr != nil {
				// Unknown is not zero. Treating a failed lookup as "ran no
				// jobs" would invent the most alarming finding this row can
				// report out of a transient API error.
				runs[i].JobCount = -1
				continue
			}
			runs[i].JobCount = n
		}
	}

	verdict := ClassifySpaceRuns(runs, "main", h.claimedReds())

	// The accounting is rendered on a PASS as well as on a fail, because on a
	// pass it IS the evidence. This row used to pass with the words "every
	// failure among them is one a scenario here caused on purpose" — an
	// assumption dressed as a finding, and the exact sentence an operator was
	// reading as green while their inbox filled with failure mail from this
	// space. Naming the ids lets them check the list against what they see.
	accounted := "no run failed"
	if n := len(verdict.Accounted); n > 0 {
		ids := make([]string, 0, n)
		for _, id := range verdict.Accounted {
			ids = append(ids, fmt.Sprintf("%d", id))
		}
		accounted = fmt.Sprintf("%d failure(s) claimed by the rows that caused them: run %s",
			n, strings.Join(ids, ", "))
	}

	if len(verdict.Findings) == 0 {
		return res(VerdictPass, "", fmt.Sprintf("%d workflow run(s) since %s; %s",
			len(runs), since, accounted))
	}

	msgs := make([]string, 0, len(verdict.Findings))
	for _, f := range verdict.Findings {
		msgs = append(msgs, f.Reason)
	}
	return res(VerdictFail, strings.Join(msgs, " | "),
		fmt.Sprintf("%d unexplained failure(s) among %d run(s) since %s; %s",
			len(verdict.Findings), len(runs), since, accounted))
}
