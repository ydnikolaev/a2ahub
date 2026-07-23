package fakegithub

import "testing"

// TestSplitHead pins the one piece of parsing the fake does on GitHub's
// behalf. Conflating "no owner" with "empty branch" produced an empty ref
// that git only rejected at merge time, several layers away from the
// mistake — the fake's own first bug.
func TestSplitHead(t *testing.T) {
	t.Parallel()

	cases := []struct {
		head        string
		owner       string
		branch      string
		description string
	}{
		{"a2a/axon/submit/XQ-1", "", "a2a/axon/submit/XQ-1", "same-repo head: no owner, branch is the whole string"},
		{"consumer:a2a/feedback/submit/fb-1", "consumer", "a2a/feedback/submit/fb-1", "cross-fork head"},
	}
	for _, c := range cases {
		owner, branch := splitHead(c.head)
		if owner != c.owner || branch != c.branch {
			t.Errorf("%s: splitHead(%q) = (%q, %q), want (%q, %q)",
				c.description, c.head, owner, branch, c.owner, c.branch)
		}
	}
}

// TestHeadRef pins where a PR's head lives inside the origin: a fork's head
// is mirrored under refs/fork/, a same-repo head is a plain branch.
func TestHeadRef(t *testing.T) {
	t.Parallel()

	if got := headRef(&PR{Head: "a2a/axon/submit/XQ-1"}); got != "refs/heads/a2a/axon/submit/XQ-1" {
		t.Errorf("same-repo headRef = %q", got)
	}
	if got := headRef(&PR{Head: "consumer:a2a/feedback/submit/fb-1"}); got != "refs/fork/a2a/feedback/submit/fb-1" {
		t.Errorf("fork headRef = %q", got)
	}
}

// TestPRAPIState pins GitHub's own shape: a merged PR reports state
// "closed" alongside merged:true, never a "merged" state string.
func TestPRAPIState(t *testing.T) {
	t.Parallel()

	if got := prAPIState(&PR{State: "open"}); got != "open" {
		t.Errorf("open PR state = %q", got)
	}
	if got := prAPIState(&PR{State: "merged", Merged: true}); got != "closed" {
		t.Errorf("merged PR state = %q, want closed (GitHub reports merged as closed+merged)", got)
	}
}
