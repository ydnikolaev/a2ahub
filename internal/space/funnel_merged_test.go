package space

import (
	"context"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestFunnel_OpenPRMergedIsMerged is AC7 of the local-host spec: a host that
// lands the write inside OpenPR (the local host: validate, then merge, in one
// call) answers with a PR already in state "merged". The funnel must report
// that as WriteStateMerged at stage merged, with nothing left to do and no
// pending-merge or needs-attention note, and must not try to arm, merge or
// poll anything, because there is nothing open to act on.
func TestFunnel_OpenPRMergedIsMerged(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	l, err := NewLayout("axon")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	req := newTestSubmitRequest(fx, "axon", l)

	fake := host.NewFakeHost()
	fake.OpenPRFunc = func(_ context.Context, r host.OpenPRRequest) (host.PRInfo, error) {
		return host.PRInfo{
			Number: 7, URL: "file:///space.git#pr/7", State: "merged",
			Title: r.Title, Body: r.Body, BaseBranch: r.Base, HeadSHA: r.ExpectedHeadSHA,
			MergeMethod: host.MergeMethodMerge,
		}, nil
	}

	result, err := NewWriteFunnel(fake, nil, testBuild("0.1.0")).Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.State != WriteStateMerged {
		t.Fatalf("State = %q, want %q: a PR the host already merged is not needs-attention", result.State, WriteStateMerged)
	}
	if result.Stage != WriteStageMerged {
		t.Fatalf("Stage = %q, want %q", result.Stage, WriteStageMerged)
	}
	if result.RemainingAction != RemainingActionNone {
		t.Fatalf("RemainingAction = %q, want %q", result.RemainingAction, RemainingActionNone)
	}
	if result.Note != "" || result.AutoMergeNote != "" {
		t.Fatalf("Note = %q, AutoMergeNote = %q, want both empty: nothing is pending", result.Note, result.AutoMergeNote)
	}
	if result.PRNumber != 7 || result.MergeMethod != host.MergeMethodMerge {
		t.Fatalf("PRNumber = %d, MergeMethod = %q, want 7 and %q", result.PRNumber, result.MergeMethod, host.MergeMethodMerge)
	}
	if len(fake.AutoArms) != 0 || len(fake.MergePRs) != 0 {
		t.Fatalf("a merged PR was acted on: auto-arms=%d merges=%d, want none", len(fake.AutoArms), len(fake.MergePRs))
	}
}
