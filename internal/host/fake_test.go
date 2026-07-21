package host

import (
	"context"
	"testing"
)

func TestFakeHostIdempotentFindAfterOpen(t *testing.T) {
	t.Parallel()

	f := NewFakeHost()
	req := OpenPRRequest{Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main"}

	opened, err := f.OpenPR(context.Background(), req)
	if err != nil {
		t.Fatalf("OpenPR: %v", err)
	}

	found, err := f.FindPRByHeadBranch(context.Background(), FindPRRequest{
		Repo: req.Repo, Branch: req.Head,
	})
	if err != nil {
		t.Fatalf("FindPRByHeadBranch: %v", err)
	}
	if found == nil || found.Number != opened.Number {
		t.Fatalf("FindPRByHeadBranch = %+v, want the PR OpenPR minted (%+v)", found, opened)
	}
}

func TestFakeHostRecordsCalls(t *testing.T) {
	t.Parallel()

	f := NewFakeHost()
	ctx := context.Background()
	_, _ = f.PushBranch(ctx, PushBranchRequest{Branch: "a2a/axon/XQ-axon-1"})
	_, _ = f.OpenPR(ctx, OpenPRRequest{Head: "a2a/axon/XQ-axon-1"})

	if len(f.Pushes) != 1 || len(f.Opens) != 1 {
		t.Fatalf("expected 1 recorded push and 1 recorded open, got %d/%d", len(f.Pushes), len(f.Opens))
	}
}
