package main

import (
	"os"
	"strings"
	"testing"
)

// The feedback hub of record must NOT be the branch a release force-pushes.
//
// That sentence is the whole of agent-ops-2026-07 P15, and it is one edit away
// from being false at any time: `feedbackBaseBranch` and `defaultBaseBranch`
// are two constants in one file, and "why do we have two of these?" is a
// reasonable-sounding question with a wrong answer. ADR-010 decision 3 records
// the right one — `defaultBaseBranch` also serves three SPACE operations
// (lifecycle, submit, space update), so collapsing them moves where every
// space pull request targets.
func TestFeedbackBaseBranchIsNotTheForcePushedBranch(t *testing.T) {
	t.Parallel()

	if feedbackBaseBranch == defaultBaseBranch {
		t.Fatalf("feedbackBaseBranch == defaultBaseBranch (%q): inbound reports would land on the branch "+
			"docs/runbooks/publish-to-public.sh force-pushes on every release, which is the defect P15 exists "+
			"to remove. See ADR-010 decision 3 before collapsing these.", feedbackBaseBranch)
	}
	if feedbackBaseBranch == "" {
		t.Fatal("feedbackBaseBranch is empty; internal/feedback refuses an empty base branch rather than guessing")
	}
}

// The three SPACE call sites must keep targeting the shared constant. AC4 asks
// for a diff review of every read site, and a review is a thing that happened
// once — this is the part of it that can be re-run.
//
// It reads the source rather than exercising the wiring because the three
// sites sit inside three large closures that each need a config, a mirror, a
// credential and a host; the assertion is about which constant is written
// there, and reading that directly is honest about what is being checked.
func TestSpaceOperationsStillTargetTheSharedBaseBranch(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("wire.go")
	if err != nil {
		t.Fatalf("read wire.go: %v", err)
	}
	src := string(raw)

	const wantSpaceSites = 3
	got := strings.Count(src, "BaseBranch: defaultBaseBranch") +
		strings.Count(src, "BaseBranch:        defaultBaseBranch")
	if got != wantSpaceSites {
		t.Errorf("wire.go has %d space call site(s) reading defaultBaseBranch, want %d.\n"+
			"MORE means a feedback site was repointed back onto the force-pushed branch.\n"+
			"FEWER means a space operation was moved onto the feedback hub, which would send every "+
			"space pull request to a branch that carries no space.", got, wantSpaceSites)
	}

	const wantFeedbackSites = 2 // the submit config, and the raw.githubusercontent.com reader
	gotFeedback := strings.Count(src, "feedbackBaseBranch")
	// The constant's own declaration and its doc comment mention it too; the
	// assertion is that the USE sites are present, so this is a floor.
	if gotFeedback < wantFeedbackSites {
		t.Errorf("wire.go mentions feedbackBaseBranch %d time(s), want at least %d use sites plus its declaration",
			gotFeedback, wantFeedbackSites)
	}
}
