package main

import (
	"os"
	"strings"
	"testing"
)

// The feedback hub of record must NOT be the branch a release force-pushes.
//
// That sentence is the whole of agent-ops-2026-07 P15, and it is one edit away
// from being false at any time. ADR-010 decision 3 records why P15 added
// `feedbackBaseBranch` rather than repointing the shared space constant: that
// constant also served three SPACE operations (lifecycle, submit, space
// update), so collapsing them would have moved where every space pull request
// targets.
//
// # Why this file changed on 2026-08-28, and what survived
//
// P15's guarantee held by COMPARISON: `feedbackBaseBranch != defaultBaseBranch`,
// two constants in one file. `no-silent-yes-2026-08` P2b then DELETED
// `defaultBaseBranch` — a space's base branch is DERIVED from its own mirror's
// `refs/remotes/origin/HEAD` (space.ResolveBaseBranch), because a team whose
// repo defaults to `master` could not submit at all while the push target was a
// literal. P15's AC4 was amended in its own spec the same day: it guards that
// P15'S OWN DIFF did not repoint the shared constant, which remains true and
// diff-reviewable.
//
// So the comparison has no second operand any more — and the guarantee it stood
// for does not depend on one. What it actually requires is that the feedback
// hub is a NAMED, FIXED branch of its own, and that no space operation reads it.
// Both are asserted below, and the second is now the stronger of the two: with
// space branches derived per space, "a space operation reading the feedback
// constant" is the only remaining way to send space pull requests to a branch
// that carries no space.
func TestFeedbackBaseBranchIsNotTheForcePushedBranch(t *testing.T) {
	t.Parallel()

	// `main` is what docs/runbooks/publish-to-public.sh force-pushes on every
	// release. Named as a literal HERE deliberately: this test is about the
	// PUBLISHER's branch, which is a fact about the public repository, not
	// about any space's derived default — the two used to be the same word and
	// that coincidence is exactly what made the old comparison readable.
	const forcePushedByThePublisher = "main"

	if feedbackBaseBranch == forcePushedByThePublisher {
		t.Fatalf("feedbackBaseBranch == %q: inbound reports would land on the branch "+
			"docs/runbooks/publish-to-public.sh force-pushes on every release, which is the defect P15 "+
			"exists to remove. See ADR-010 decision 3 before changing this.", forcePushedByThePublisher)
	}
	if feedbackBaseBranch == "" {
		t.Fatal("feedbackBaseBranch is empty; internal/feedback refuses an empty base branch rather than guessing")
	}
}

// No space operation may read the feedback constant.
//
// This replaces a count of `BaseBranch: defaultBaseBranch` sites, which counted
// a symbol that no longer exists. The direction that mattered is the one kept:
// FEWER space sites on the shared constant used to mean "a space operation was
// moved onto the feedback hub, which would send every space pull request to a
// branch that carries no space." That failure is still reachable, and this is
// what still catches it.
//
// It reads the source rather than exercising the wiring for the same reason the
// original did: the sites sit inside large closures that each need a config, a
// mirror, a credential and a host, and the assertion is about which value is
// written there.
func TestSpaceOperationsDoNotReadTheFeedbackBaseBranch(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("wire.go")
	if err != nil {
		t.Fatalf("read wire.go: %v", err)
	}
	src := string(raw)

	// Every space write path resolves its own branch. A `BaseBranch:` field set
	// from feedbackBaseBranch is only legal in the feedback submit config, which
	// is not a space operation at all.
	for _, illegal := range []string{
		"BaseBranch: feedbackBaseBranch,\n\t\tCredential",
		"BaseBranch:        feedbackBaseBranch,\n\t\tExpectedBaseSHA",
	} {
		if strings.Contains(src, illegal) {
			t.Errorf("a SPACE operation in wire.go reads feedbackBaseBranch:\n%s\n"+
				"That sends space pull requests to the feedback hub, a branch that carries no space. "+
				"Space operations resolve their own base branch via space.ResolveBaseBranch.", illegal)
		}
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
