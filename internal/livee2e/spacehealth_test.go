package livee2e

import (
	"strings"
	"testing"
)

// TestClassifySpaceRuns pins both directions, because this classifier fails
// badly in either: too narrow and it stays as blind as the tier was on
// 2026-07-26, too wide and it flags the red PR checks a row exists to
// produce — noise nobody reads, which loses the real signal just as
// thoroughly.
//
// The claim is what separates the two. A row that reddens a gate on purpose
// declares the run id it reddened; anything else that failed is a finding.
// The cases below therefore come in pairs — the same run, claimed and
// unclaimed — because a claim that changed nothing and a claim that excused
// everything would both pass a test that only ever ran one of the two.
func TestClassifySpaceRuns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		run        SpaceRun
		claimed    []int64
		wantFlag   bool
		wantReason string
	}{
		{
			name: "a failed push on main — the post-merge audit class the emails came from",
			run: SpaceRun{ID: 1, Event: "push", HeadBranch: "main", Status: "completed",
				Conclusion: "failure", JobCount: 2},
			wantFlag: true, wantReason: "base branch",
		},
		{
			name: "a completed run with zero jobs — the unresolvable-workflow class",
			run: SpaceRun{ID: 2, Event: "pull_request", HeadBranch: "a2a/alpha/submit/XQ-1",
				Status: "completed", Conclusion: "failure", JobCount: 0},
			wantFlag: true, wantReason: "ZERO jobs",
		},
		{
			name: "zero jobs on main is reported as the workflow fault, not the audit fault",
			run: SpaceRun{ID: 3, Event: "push", HeadBranch: "main", Status: "completed",
				Conclusion: "failure", JobCount: 0},
			wantFlag: true, wantReason: "ZERO jobs",
		},

		// A CLAIM EXCUSES A CONCLUSION, NEVER A SHAPE. Both of these are
		// claimed and both are still findings: a scenario can testify that it
		// made a gate go red, and that testimony says nothing about whether
		// the workflow behind the gate could be loaded, nor about how a push
		// to main came to fail.
		{
			name: "a CLAIMED run that ran zero jobs is still an unloadable workflow",
			run: SpaceRun{ID: 10, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: 0},
			claimed:  []int64{10},
			wantFlag: true, wantReason: "ZERO jobs",
		},
		{
			name: "a CLAIMED failed push on main is still main going red",
			run: SpaceRun{ID: 11, Event: "push", HeadBranch: "main", Status: "completed",
				Conclusion: "failure", JobCount: 2},
			claimed:  []int64{11},
			wantFlag: true, wantReason: "base branch",
		},

		// The refusal row REQUIRES this red, and says so by id.
		{
			name: "a CLAIMED failed PR check — cross-section-retrigger-stays-red depends on exactly this",
			run: SpaceRun{ID: 4, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: 2},
			claimed:  []int64{4},
			wantFlag: false,
		},
		{
			// The other half of the pair, and the reason the claim exists at
			// all: the SAME run, unclaimed, is a finding. Before claims this
			// row passed on a branch-name reading — which is how a red run
			// nobody caused on purpose could sit in the Actions tab while
			// this report read green.
			name: "the same failed PR check UNCLAIMED is a finding",
			run: SpaceRun{ID: 4, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: 2},
			wantFlag: true, wantReason: "NO scenario claimed it",
		},
		{
			// A pattern-matched excuse would have absolved this one too, on
			// the strength of the branch name alone. Keying on ids is what
			// makes a second probe unable to inherit the first one's licence.
			name: "an unclaimed failure on a probe-shaped branch is not absolved by its name",
			run: SpaceRun{ID: 12, Event: "pull_request", HeadBranch: "probe/something-else",
				Status: "completed", Conclusion: "failure", JobCount: 1},
			claimed:  []int64{4},
			wantFlag: true, wantReason: "NO scenario claimed it",
		},
		{
			// THE CASE THAT WAS MISSING, and it cost 96 false positives on
			// this classifier's first live run.
			//
			// The caller measures JobCount only for FAILED runs, so a green
			// run arrives here with the zero value — not with a real count.
			// The original version of this case passed JobCount: 2, a value
			// the caller never produces for a green run, so the test agreed
			// with the code about a situation neither would ever see. Every
			// successful run in the space was then reported as "ran zero
			// jobs".
			//
			// The lesson is narrower than "test more cases": a fixture must
			// use the values the real caller produces, especially the zero
			// ones, because a zero that means "unmeasured" and a zero that
			// means "none" are the same bits.
			name: "a green push on main, with the UNMEASURED job count the caller actually passes",
			run: SpaceRun{ID: 5, Event: "push", HeadBranch: "main", Status: "completed",
				Conclusion: "success", JobCount: 0},
			wantFlag: false,
		},
		{
			name: "a green PR run, likewise unmeasured — and needing no claim, because it did not fail",
			run: SpaceRun{ID: 8, Event: "pull_request", HeadBranch: "a2a/alpha/submit/XQ-1",
				Status: "completed", Conclusion: "success", JobCount: 0},
			wantFlag: false,
		},
		{
			name: "a failed run whose job lookup errored is NOT called zero-jobs, but is still unclaimed",
			run: SpaceRun{ID: 9, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: -1},
			wantFlag: true, wantReason: "NO scenario claimed it",
		},
		{
			name: "…and claimed, it is accounted for rather than invented as a zero-job fault",
			run: SpaceRun{ID: 9, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: -1},
			claimed:  []int64{9},
			wantFlag: false,
		},
		{
			name: "a run still in flight has not failed",
			run: SpaceRun{ID: 6, Event: "push", HeadBranch: "main", Status: "in_progress",
				Conclusion: "", JobCount: 0},
			wantFlag: false,
		},
		{
			name: "a cancelled run is not a failure",
			run: SpaceRun{ID: 7, Event: "push", HeadBranch: "main", Status: "completed",
				Conclusion: "cancelled", JobCount: 1},
			wantFlag: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifySpaceRuns([]SpaceRun{tc.run}, "main", tc.claimed)
			if tc.wantFlag && len(got.Findings) == 0 {
				t.Fatalf("run %+v (claimed %v) was not flagged — this is the shape that reached the "+
					"operator by email while the tier reported green", tc.run, tc.claimed)
			}
			if !tc.wantFlag && len(got.Findings) != 0 {
				t.Fatalf("run %+v (claimed %v) was flagged as %q — flagging what a row deliberately "+
					"causes buries the real signal", tc.run, tc.claimed, got.Findings[0].Reason)
			}
			if tc.wantFlag && !strings.Contains(got.Findings[0].Reason, tc.wantReason) {
				t.Fatalf("reason = %q, want it to name %q — a reader must be able to act on it without "+
					"looking anything up", got.Findings[0].Reason, tc.wantReason)
			}
		})
	}
}

// TestClassifySpaceRunsAccountsForTheRedsItOwns pins the evidence half. A row
// that passes by asserting "the reds were all ours" is the sentence this
// whole mechanism replaces: the pass must carry the ids, so a reader looking
// at a red Actions tab can check the list against what they see rather than
// take the tier's word for it.
func TestClassifySpaceRunsAccountsForTheRedsItOwns(t *testing.T) {
	t.Parallel()

	runs := []SpaceRun{
		{ID: 100, Event: "pull_request", HeadBranch: "probe/xsec", Status: "completed",
			Conclusion: "failure", JobCount: 1},
		{ID: 101, Event: "pull_request", HeadBranch: "probe/xsec", Status: "completed",
			Conclusion: "failure", JobCount: 1},
		{ID: 102, Event: "push", HeadBranch: "main", Status: "completed",
			Conclusion: "success", JobCount: 0},
	}
	got := ClassifySpaceRuns(runs, "main", []int64{100, 101})
	if len(got.Findings) != 0 {
		t.Fatalf("findings = %v, want none: both reds were claimed", got.Findings)
	}
	if len(got.Accounted) != 2 || got.Accounted[0] != 100 || got.Accounted[1] != 101 {
		t.Fatalf("accounted = %v, want [100 101] — the ids ARE the evidence behind the pass; "+
			"without them the row is back to asserting that the reds were deliberate", got.Accounted)
	}
	// A claim for a run that never failed must not inflate the accounting —
	// the list has to describe what actually went red, or a reader comparing
	// it against the Actions tab finds ids that are not there.
	if got := ClassifySpaceRuns(runs, "main", []int64{100, 101, 102}); len(got.Accounted) != 2 {
		t.Fatalf("accounted = %v, want only the two runs that actually failed", got.Accounted)
	}
}

func TestClassifySpaceRunsDefaultsTheBaseBranch(t *testing.T) {
	t.Parallel()

	run := SpaceRun{ID: 1, Event: "push", HeadBranch: "main", Status: "completed",
		Conclusion: "failure", JobCount: 2}
	if got := ClassifySpaceRuns([]SpaceRun{run}, "", nil); len(got.Findings) != 1 {
		t.Fatalf("an empty baseBranch must default to §4.2's normative \"main\", else a caller that "+
			"forgets to pass it silently stops checking anything; got %d findings", len(got.Findings))
	}
}
