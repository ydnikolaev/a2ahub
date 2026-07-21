package host

import (
	"context"
	"sync"
)

// FakeHost is a hand-written, in-memory Host test double (rails: "hand-
// written mocks, no codegen"). It is exported so internal/space's tests
// (and any other consumer-side test) can exercise the write funnel without
// a live GitHub call, per spec 05 §6 "interface-level fake". Safe for
// concurrent/parallel test use (t.Parallel()).
type FakeHost struct {
	mu sync.Mutex

	// PushBranchFunc, when set, overrides PushBranch's behavior (e.g. to
	// simulate CC-061 push rejection). Default: records the call and
	// succeeds.
	PushBranchFunc func(ctx context.Context, req PushBranchRequest) (PushBranchResult, error)
	// OpenPRFunc, when set, overrides OpenPR's behavior. Default: mints a
	// deterministic incrementing PR number/URL and marks the PR open.
	OpenPRFunc func(ctx context.Context, req OpenPRRequest) (PRInfo, error)
	// FindPRFunc, when set, overrides FindPRByHeadBranch. Default: looks
	// up prior OpenPR calls by branch name (the idempotency short-circuit
	// path under test).
	FindPRFunc func(ctx context.Context, req FindPRRequest) (*PRInfo, error)
	// CheckStatusFunc/ReviewStatusFunc override the corresponding method;
	// unset returns a zero-value success result.
	CheckStatusFunc  func(ctx context.Context, req StatusRequest) (CheckStatusResult, error)
	ReviewStatusFunc func(ctx context.Context, req StatusRequest) (ReviewStatusResult, error)

	// Recorded calls, for test assertions.
	Pushes   []PushBranchRequest
	Opens    []OpenPRRequest
	nextPR   int
	byBranch map[string]PRInfo
}

// NewFakeHost constructs a ready-to-use FakeHost.
func NewFakeHost() *FakeHost {
	return &FakeHost{byBranch: make(map[string]PRInfo)}
}

// PushBranch records the request and delegates to PushBranchFunc, or
// succeeds by default.
func (f *FakeHost) PushBranch(ctx context.Context, req PushBranchRequest) (PushBranchResult, error) {
	f.mu.Lock()
	f.Pushes = append(f.Pushes, req)
	fn := f.PushBranchFunc
	f.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return PushBranchResult{Branch: req.Branch}, nil
}

// OpenPR records the request and delegates to OpenPRFunc, or mints a
// deterministic open PR by default.
func (f *FakeHost) OpenPR(ctx context.Context, req OpenPRRequest) (PRInfo, error) {
	f.mu.Lock()
	f.Opens = append(f.Opens, req)
	fn := f.OpenPRFunc
	f.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}

	f.mu.Lock()
	f.nextPR++
	info := PRInfo{Number: f.nextPR, URL: "https://example.invalid/pr/" + req.Head, State: "open"}
	f.byBranch[req.Head] = info
	f.mu.Unlock()
	return info, nil
}

// CheckStatus delegates to CheckStatusFunc, or reports a green check by
// default.
func (f *FakeHost) CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error) {
	if f.CheckStatusFunc != nil {
		return f.CheckStatusFunc(ctx, req)
	}
	return CheckStatusResult{State: "completed", Conclusion: "success"}, nil
}

// ReviewStatus delegates to ReviewStatusFunc, or reports "approved" by
// default.
func (f *FakeHost) ReviewStatus(ctx context.Context, req StatusRequest) (ReviewStatusResult, error) {
	if f.ReviewStatusFunc != nil {
		return f.ReviewStatusFunc(ctx, req)
	}
	return ReviewStatusResult{Approved: true}, nil
}

// FindPRByHeadBranch delegates to FindPRFunc, or (default) returns any PR
// previously minted by OpenPR for that branch — this is what makes the
// idempotency short-circuit testable without a custom func.
func (f *FakeHost) FindPRByHeadBranch(ctx context.Context, req FindPRRequest) (*PRInfo, error) {
	if f.FindPRFunc != nil {
		return f.FindPRFunc(ctx, req)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	info, ok := f.byBranch[req.Branch]
	if !ok {
		return nil, nil
	}
	return &info, nil
}

var _ Host = (*FakeHost)(nil)
