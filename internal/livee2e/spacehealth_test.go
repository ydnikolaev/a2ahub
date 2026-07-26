package livee2e

import (
	"strings"
	"testing"
)

// TestUnexplainedSpaceFailures pins both directions, because this classifier
// fails badly in either: too narrow and it stays as blind as the tier was on
// 2026-07-26, too wide and it flags the red PR checks several rows exist to
// produce — noise nobody reads, which loses the real signal just as
// thoroughly.
func TestUnexplainedSpaceFailures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		run        SpaceRun
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

		// The refusal rows REQUIRE these. Flagging them would make the
		// signal useless.
		{
			name: "a failed PR check — cross-section-retrigger-stays-red depends on exactly this",
			run: SpaceRun{ID: 4, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: 2},
			wantFlag: false,
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
			name: "a green PR run, likewise unmeasured",
			run: SpaceRun{ID: 8, Event: "pull_request", HeadBranch: "a2a/alpha/submit/XQ-1",
				Status: "completed", Conclusion: "success", JobCount: 0},
			wantFlag: false,
		},
		{
			name: "a failed run whose job lookup errored is NOT called zero-jobs",
			run: SpaceRun{ID: 9, Event: "pull_request", HeadBranch: "probe/xsec",
				Status: "completed", Conclusion: "failure", JobCount: -1},
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
			got := UnexplainedSpaceFailures([]SpaceRun{tc.run}, "main")
			if tc.wantFlag && len(got) == 0 {
				t.Fatalf("run %+v was not flagged — this is the shape that reached the operator by email "+
					"while the tier reported green", tc.run)
			}
			if !tc.wantFlag && len(got) != 0 {
				t.Fatalf("run %+v was flagged as %q — flagging what the refusal rows deliberately cause "+
					"buries the real signal", tc.run, got[0].Reason)
			}
			if tc.wantFlag && !strings.Contains(got[0].Reason, tc.wantReason) {
				t.Fatalf("reason = %q, want it to name %q — a reader must be able to act on it without "+
					"looking anything up", got[0].Reason, tc.wantReason)
			}
		})
	}
}

func TestUnexplainedSpaceFailuresDefaultsTheBaseBranch(t *testing.T) {
	t.Parallel()

	run := SpaceRun{ID: 1, Event: "push", HeadBranch: "main", Status: "completed",
		Conclusion: "failure", JobCount: 2}
	if got := UnexplainedSpaceFailures([]SpaceRun{run}, ""); len(got) != 1 {
		t.Fatalf("an empty baseBranch must default to §4.2's normative \"main\", else a caller that "+
			"forgets to pass it silently stops checking anything; got %d findings", len(got))
	}
}
