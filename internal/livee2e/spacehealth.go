package livee2e

// spacehealth.go answers a question this tier could not previously ask: is
// the SPACE's own CI healthy, beyond the specific checks the matrix waited
// for?
//
// Twice on 2026-07-26 a real failure on the live space reached the operator
// by email while this tier reported 38/38 green. Both were invisible here
// for the same structural reason: the matrix awaits the checks it opened
// PRs for, and is blind to every other workflow run in the same repository.
//
//   - The post-merge full-repo audit failed on every push to main, because
//     a probe artifact with malformed frontmatter had been pushed there.
//     That job is flag-only and never a required check BY DESIGN, so no row
//     asserts it — and nothing else looked.
//   - A killed run left the space pinned to a workflow tag that did not
//     exist. GitHub does not report an unresolvable reusable workflow as a
//     failing check; it reports a run with NO JOBS. The required context
//     never appears at all, so a tier that only reads check conclusions sees
//     nothing to complain about.
//
// # Why a red run is EXPLAINED by a claim, never by a name
//
// Failing checks on pull requests are normal here: at least one row exists
// precisely to prove that an illegal write is refused, and it leaves a red
// PR check behind by design. The first version of this file dealt with that
// by staying NARROW — it reported two shapes and passed everything else,
// while telling the reader "every failure among them is one a scenario here
// caused on purpose". That sentence was never verified. It was an assumption
// rendered as evidence, and it is exactly the sentence an operator was
// reading as green while their inbox filled with failure mail.
//
// So a deliberately-red run is now EXPLAINED BY ITS ID: the scenario that
// reddens it declares the run ids it caused (harness.claimRed), and this
// classifier reports every failed run nobody claimed. Keying on ids rather
// than on a branch-name or title pattern is the whole point — a pattern goes
// stale the first time somebody adds a second probe, and then it silently
// absolves a real failure. Inferring intent from a field the caller never
// populated is also precisely how this file's first live run produced 96
// false findings out of 100.
//
// A claim excuses a run's CONCLUSION. It never excuses its SHAPE: a claimed
// run that ran zero jobs is still an unloadable workflow, and is still
// reported. The two rules below are therefore checked unconditionally, and
// the claim is consulted only for the third.
//
// The residual risk is deliberate and is the safe direction. If a row reddens
// a run without claiming it, this over-reports: the matrix reds, and the
// report names the exact run id so the fix is either one claim or a real
// defect found. The opposite bias — passing over an unclaimed red — is the
// bias that already shipped twice.
//
// This file is untagged on purpose. Every scenario file in this package sits
// behind `//go:build livee2e`, where `make check` cannot see it — which is
// exactly how the malformed probe survived for weeks. The decision logic
// lives out here with its own test; only the GitHub call that feeds it needs
// the tag.

import "fmt"

// SpaceRun is the subset of a GitHub workflow run needed to judge the
// space's own CI health.
//
// JobCount is load-bearing and not derivable from Conclusion: an
// unresolvable workflow yields a completed, FAILED run with zero jobs, which
// is a different fault from a job that ran and failed.
//
// It is MEASURED ONLY FOR FAILED RUNS, because that is the only place the
// answer changes a verdict and every other run would pay an API call for
// nothing. So a zero here means one of three things — genuinely no jobs, not
// measured, or a lookup that failed — and only the first is a finding. The
// first live run of this classifier flagged 96 of 100 runs because it read
// the unmeasured zero of every SUCCESSFUL run as "ran nothing". The rule
// below therefore requires a failed conclusion before it looks at JobCount
// at all; a caller whose lookup errored sets -1 so it cannot collide with a
// real zero either.
type SpaceRun struct {
	ID         int64
	Name       string
	Event      string // "push", "pull_request", …
	HeadBranch string
	Status     string // "completed" while terminal
	Conclusion string // "success", "failure", "cancelled"; empty while running
	JobCount   int    // measured only when Conclusion == "failure"; -1 = lookup failed
}

// SpaceFailure is one unexplained failure, carrying the reason in words a
// reader can act on rather than a code they must look up.
type SpaceFailure struct {
	Run    SpaceRun
	Reason string
}

// SpaceCIVerdict is the whole answer the health row renders: what is wrong,
// and — just as important for a reader staring at a red Actions tab — which
// red runs this matrix owns up to having caused.
//
// Accounted is reported even when Findings is empty, because it is the
// evidence behind the row's pass. A pass that merely asserts "the reds were
// all ours" is the claim this type exists to replace with a list.
type SpaceCIVerdict struct {
	// Findings are the runs that mean something is wrong — in the order the
	// runs were given.
	Findings []SpaceFailure
	// Accounted lists the failed runs a scenario claimed, so the report can
	// name them instead of asserting they exist.
	Accounted []int64
}

// ClassifySpaceRuns judges every run the space produced during this matrix
// against the three shapes that are never legitimate. baseBranch is the
// space's own base (normally "main"); claimed carries the run ids scenarios
// declared they reddened on purpose.
//
// A run still in flight is never reported: this is called at the end of a
// matrix run, and a run that has not concluded has not failed.
func ClassifySpaceRuns(runs []SpaceRun, baseBranch string, claimed []int64) SpaceCIVerdict {
	if baseBranch == "" {
		baseBranch = "main"
	}
	claims := make(map[int64]bool, len(claimed))
	for _, id := range claimed {
		claims[id] = true
	}

	var out SpaceCIVerdict
	for _, r := range runs {
		if r.Status != "completed" {
			continue
		}

		// Everything below is about a FAILED run. A successful one is not a
		// finding, and — see SpaceRun.JobCount — its job count is never
		// measured, so reading it would flag every green run in the space.
		if r.Conclusion != "failure" {
			continue
		}

		// A failed run that ran nothing means the workflow file could not be
		// loaded — most often a reusable-workflow ref that does not resolve.
		// Checked FIRST because such a run reports Conclusion "failure" like
		// any other, and this is the more specific, more actionable
		// diagnosis.
		//
		// Deliberately BEFORE the claim: a scenario claims that it made a
		// gate go red, which says nothing about whether the workflow behind
		// that gate could be loaded at all.
		if r.JobCount == 0 {
			out.Findings = append(out.Findings, SpaceFailure{Run: r, Reason: fmt.Sprintf(
				"workflow run %d on %q ran ZERO jobs — the workflow could not be loaded "+
					"(usually a reusable-workflow ref that does not resolve). The required check never "+
					"reports at all, so every row waiting on it times out rather than failing",
				r.ID, r.HeadBranch)})
			continue
		}

		if r.Event == "push" && r.HeadBranch == baseBranch {
			out.Findings = append(out.Findings, SpaceFailure{Run: r, Reason: fmt.Sprintf(
				"workflow run %d failed on the space's own base branch %q after a push — no scenario "+
					"here should ever leave main red. This is the post-merge audit class: flag-only, "+
					"asserted by no row, and therefore invisible unless something looks for it",
				r.ID, r.HeadBranch)})
			continue
		}

		if claims[r.ID] {
			out.Accounted = append(out.Accounted, r.ID)
			continue
		}

		out.Findings = append(out.Findings, SpaceFailure{Run: r, Reason: fmt.Sprintf(
			"workflow run %d failed on %q (%s event) and NO scenario claimed it. Either the row that "+
				"caused it must claim the run id it reddened, or this is a real failure that would "+
				"otherwise have reached the operator by email while this report read green",
			r.ID, r.HeadBranch, r.Event)})
	}
	return out
}
