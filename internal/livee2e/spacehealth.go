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
// The classifier is deliberately NARROW. Failing checks on pull requests are
// normal and expected here — several rows exist precisely to prove that an
// illegal write is refused, and each leaves a red PR check behind. Flagging
// those would produce noise nobody reads, which is how a real signal gets
// lost. Only two shapes are reported, and neither is ever legitimate:
//
//	1. a FAILED push-event run on the space's base branch — nothing the
//	   matrix does should ever leave the space's own main red;
//	2. a COMPLETED run that ran zero jobs — the workflow itself could not be
//	   loaded, on any branch.
//
// This file is untagged on purpose. Every scenario file in this package sits
// behind `//go:build livee2e`, where `make check` cannot see it — which is
// exactly how the malformed probe survived for weeks. The decision logic
// lives out here with its own test; only the GitHub call that feeds it needs
// the tag.

import "fmt"

// SpaceRun is the subset of a GitHub workflow run needed to judge the
// space's own CI health. JobCount is load-bearing and not derivable from
// Conclusion: an unresolvable workflow yields a completed, FAILED run with
// zero jobs, which is a different fault from a job that ran and failed.
type SpaceRun struct {
	ID         int64
	Name       string
	Event      string // "push", "pull_request", …
	HeadBranch string
	Status     string // "completed" while terminal
	Conclusion string // "success", "failure", "cancelled"; empty while running
	JobCount   int
}

// SpaceFailure is one unexplained failure, carrying the reason in words a
// reader can act on rather than a code they must look up.
type SpaceFailure struct {
	Run    SpaceRun
	Reason string
}

// UnexplainedSpaceFailures reports the runs that mean the space itself is
// broken, as opposed to the many red PR checks this tier deliberately
// causes. baseBranch is the space's own base (normally "main").
//
// A run still in flight is never reported: this is called at the end of a
// matrix run, and a run that has not concluded has not failed.
func UnexplainedSpaceFailures(runs []SpaceRun, baseBranch string) []SpaceFailure {
	if baseBranch == "" {
		baseBranch = "main"
	}
	var out []SpaceFailure
	for _, r := range runs {
		if r.Status != "completed" {
			continue
		}

		// A completed run that ran nothing means the workflow file could not
		// be loaded — most often a reusable-workflow ref that does not
		// resolve. Checked FIRST because such a run also reports
		// Conclusion "failure", and this is the more specific, more
		// actionable diagnosis.
		if r.JobCount == 0 {
			out = append(out, SpaceFailure{Run: r, Reason: fmt.Sprintf(
				"workflow run %d on %q ran ZERO jobs — the workflow could not be loaded "+
					"(usually a reusable-workflow ref that does not resolve). The required check never "+
					"reports at all, so every row waiting on it times out rather than failing",
				r.ID, r.HeadBranch)})
			continue
		}

		if r.Conclusion == "failure" && r.Event == "push" && r.HeadBranch == baseBranch {
			out = append(out, SpaceFailure{Run: r, Reason: fmt.Sprintf(
				"workflow run %d failed on the space's own base branch %q after a push — no scenario "+
					"here should ever leave main red. This is the post-merge audit class: flag-only, "+
					"asserted by no row, and therefore invisible unless something looks for it",
				r.ID, r.HeadBranch)})
		}
	}
	return out
}
