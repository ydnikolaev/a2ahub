package livee2e

import (
	"errors"
	"strings"
	"testing"
)

// TestMatchCompositeBranchFindsTheComposite is the P36 defect's own repro:
// `contract deprecate` opens a PR on a2a/<sys>/contract-deprecate/<XA>+<XC>
// (BranchName keyed on buildRequest's sorted, "+"-joined ArtifactID), and a
// caller that only knows the bare contract id must still find it.
func TestMatchCompositeBranchFindsTheComposite(t *testing.T) {
	t.Parallel()
	pulls := []branchPull{
		{HeadRef: "a2a/alpha/contract-deprecate/XA-alpha-20260101aaaa+XC-alpha-widget", State: "open", Number: 7},
	}
	got, err := matchCompositeBranch(pulls, "alpha", "contract-deprecate", "XC-alpha-widget")
	if err != nil {
		t.Fatalf("matchCompositeBranch: %v", err)
	}
	if got.Number != 7 {
		t.Fatalf("matched PR #%d, want #7", got.Number)
	}
}

// TestMatchCompositeBranchRefusesAmbiguity: two DISTINCT branches under the
// same prefix both carry the artifact id as one of their composite
// components — this must be a refusal naming both candidates, never a
// silent pick (spec 36 §T4: "a live tier that guesses is worse than one
// that fails").
func TestMatchCompositeBranchRefusesAmbiguity(t *testing.T) {
	t.Parallel()
	pulls := []branchPull{
		{HeadRef: "a2a/alpha/contract-deprecate/XA-alpha-1111+XC-alpha-widget", State: "closed", Number: 1},
		{HeadRef: "a2a/alpha/contract-deprecate/XA-alpha-2222+XC-alpha-widget", State: "open", Number: 2},
	}
	_, err := matchCompositeBranch(pulls, "alpha", "contract-deprecate", "XC-alpha-widget")
	if !errors.Is(err, ErrAmbiguousBranchMatch) {
		t.Fatalf("err = %v, want wrapping ErrAmbiguousBranchMatch", err)
	}
	for _, want := range []string{
		"a2a/alpha/contract-deprecate/XA-alpha-1111+XC-alpha-widget",
		"a2a/alpha/contract-deprecate/XA-alpha-2222+XC-alpha-widget",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name candidate branch %q", err.Error(), want)
		}
	}
}

// TestMatchCompositeBranchDoesNotMatchAnotherContractsBranch: a component
// must match EXACTLY, never as a substring — XC-alpha-widget must not match
// a branch whose composite instead carries XC-alpha-widget-pro (textually
// containing the shorter id, but a DIFFERENT contract).
func TestMatchCompositeBranchDoesNotMatchAnotherContractsBranch(t *testing.T) {
	t.Parallel()
	pulls := []branchPull{
		{HeadRef: "a2a/alpha/contract-deprecate/XA-alpha-1111+XC-alpha-widget-pro", State: "open", Number: 3},
	}
	_, err := matchCompositeBranch(pulls, "alpha", "contract-deprecate", "XC-alpha-widget")
	if !errors.Is(err, ErrNoBranchMatch) {
		t.Fatalf("err = %v, want wrapping ErrNoBranchMatch (must not match a longer, different contract id as a substring)", err)
	}
}

// TestMatchCompositeBranchDoesNotMatchAnotherVerbsBranch: the same
// artifact id, published under a DIFFERENT verb's branch, must not match —
// the prefix scopes by verb, not just by artifact id.
func TestMatchCompositeBranchDoesNotMatchAnotherVerbsBranch(t *testing.T) {
	t.Parallel()
	pulls := []branchPull{
		{HeadRef: "a2a/alpha/contract-publish/XC-alpha-widget", State: "open", Number: 4},
	}
	_, err := matchCompositeBranch(pulls, "alpha", "contract-deprecate", "XC-alpha-widget")
	if !errors.Is(err, ErrNoBranchMatch) {
		t.Fatalf("err = %v, want wrapping ErrNoBranchMatch (a different verb's branch must not match)", err)
	}
}

// TestMatchCompositeBranchDoesNotMatchAnotherSystemsBranch: the same verb
// and artifact id, under a DIFFERENT system's prefix, must not match either
// — the prefix scopes by system too.
func TestMatchCompositeBranchDoesNotMatchAnotherSystemsBranch(t *testing.T) {
	t.Parallel()
	pulls := []branchPull{
		{HeadRef: "a2a/bravo/contract-deprecate/XA-bravo-1111+XC-alpha-widget", State: "open", Number: 5},
	}
	_, err := matchCompositeBranch(pulls, "alpha", "contract-deprecate", "XC-alpha-widget")
	if !errors.Is(err, ErrNoBranchMatch) {
		t.Fatalf("err = %v, want wrapping ErrNoBranchMatch (a different system's branch must not match)", err)
	}
}

// TestMatchCompositeBranchPrefersOpenThenHighestNumber mirrors
// pullForBranch's own selection rule among multiple PRs on the ONE matching
// branch (fa3ee82: this space auto-merges, so a fast-green write is merged
// and closed before a caller looks — OPEN still wins when one exists, and
// the highest number wins among closed candidates).
func TestMatchCompositeBranchPrefersOpenThenHighestNumber(t *testing.T) {
	t.Parallel()
	branch := "a2a/alpha/contract-deprecate/XA-alpha-1111+XC-alpha-widget"
	pulls := []branchPull{
		{HeadRef: branch, State: "closed", Number: 1},
		{HeadRef: branch, State: "closed", Number: 9},
		{HeadRef: branch, State: "open", Number: 5},
	}
	got, err := matchCompositeBranch(pulls, "alpha", "contract-deprecate", "XC-alpha-widget")
	if err != nil {
		t.Fatalf("matchCompositeBranch: %v", err)
	}
	if got.State != "open" || got.Number != 5 {
		t.Fatalf("got State=%q Number=%d, want the OPEN PR (#5)", got.State, got.Number)
	}

	allClosed := []branchPull{
		{HeadRef: branch, State: "closed", Number: 1},
		{HeadRef: branch, State: "closed", Number: 9},
	}
	got, err = matchCompositeBranch(allClosed, "alpha", "contract-deprecate", "XC-alpha-widget")
	if err != nil {
		t.Fatalf("matchCompositeBranch: %v", err)
	}
	if got.Number != 9 {
		t.Fatalf("got #%d, want the highest closed PR number (#9)", got.Number)
	}
}
