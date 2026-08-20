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

// TestPRNumberFromVerbOutput pins the two output shapes a lifecycle verb
// actually prints and the two host grammars this repo runs against. It exists
// because the function replaced a branch-NAME lookup that stopped working the
// moment `a2a verify --verdict` switched the funnel's dedup key from the
// batch's artifact id to operation.Verify's content-derived key — so a reader
// that guesses at either the URL grammar or the sentence shape reintroduces
// exactly the fragility the function was written to remove.
func TestPRNumberFromVerbOutput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		out  string
		want int
	}{
		{
			name: "opened, github grammar",
			out:  "verify: opened PR https://github.com/o/r/pull/41 for XS-a, XW-b (pending-merge)",
			want: 41,
		},
		{
			name: "opened, logic-tier local host grammar",
			out:  "verify: opened PR https://example.invalid/pr/6 for XS-a, XW-b (pending-merge)",
			want: 6,
		},
		{
			name: "already submitted, PR in parentheses",
			out:  "verify: already submitted for XS-a (PR https://example.invalid/pr/7, merged)",
			want: 7,
		},
		{
			name: "the verdict echo precedes the success line",
			out:  "XS-a:\n  0 -> \"the artifact validates\"  met  (alpha)\nverify: opened PR https://example.invalid/pr/9 for XS-a (pending-merge)",
			want: 9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := prNumberFromVerbOutput(tc.out)
			if err != nil {
				t.Fatalf("prNumberFromVerbOutput(%q) = error %v, want %d", tc.out, err, tc.want)
			}
			if got != tc.want {
				t.Fatalf("prNumberFromVerbOutput(%q) = %d, want %d", tc.out, got, tc.want)
			}
		})
	}

	t.Run("no URL is an error, never a zero", func(t *testing.T) {
		t.Parallel()
		if n, err := prNumberFromVerbOutput("verify: nothing to do"); err == nil {
			t.Fatalf("want an error for output carrying no PR URL, got %d", n)
		}
	})
	t.Run("a non-numeric last segment is not a PR number", func(t *testing.T) {
		t.Parallel()
		if n, err := prNumberFromVerbOutput("see https://example.invalid/pr/latest"); err == nil {
			t.Fatalf("want an error for a non-numeric PR segment, got %d", n)
		}
	})
}

// TestKindDeclaresAcceptanceCriteria pins the predicate against the ONE place
// the answer actually comes from — draftFieldArgs' own field lists. The two
// must not drift: a kind that starts declaring acceptance_criteria without
// this predicate learning about it makes a generic family scenario submit a
// verdict-free close, which REF-023 refuses; a kind that stops declaring them
// makes it submit a verdict against a criterion that no longer exists.
func TestKindDeclaresAcceptanceCriteria(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"work_request", "requirement", "question", "decision", "handoff", "announcement", "contract", "response"} {
		args, ok := draftFieldArgs(kind, "space", "alpha", "bravo")
		if !ok {
			continue
		}
		declared := false
		for _, a := range args {
			if strings.Contains(a, "acceptance_criteria=") {
				declared = true
				break
			}
		}
		if got := kindDeclaresAcceptanceCriteria(kind); got != declared {
			t.Errorf("kindDeclaresAcceptanceCriteria(%q) = %v, but draftFieldArgs %s declare acceptance_criteria",
				kind, got, map[bool]string{true: "DOES", false: "does NOT"}[declared])
		}
	}
}
