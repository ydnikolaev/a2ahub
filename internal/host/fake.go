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
	CheckStatusFunc func(ctx context.Context, req StatusRequest) (CheckStatusResult, error)
	// TokenScopesFunc overrides TokenScopes; nil reports a workflow-capable
	// classic token (see the method).
	TokenScopesFunc  func(ctx context.Context, cred Credential) ([]string, bool, error)
	ReviewStatusFunc func(ctx context.Context, req StatusRequest) (ReviewStatusResult, error)
	// EnableAutoMergeFunc, when set, overrides the optional AutoMerger
	// capability. Default: records the call and succeeds, which is what a
	// real host does when auto-merge is already armed.
	EnableAutoMergeFunc func(ctx context.Context, req EnableAutoMergeRequest) error
	// EnsureForkFunc, when set, overrides the optional Forker capability.
	// Default: mints ForkLogin's fork of the same repo name, idempotently.
	EnsureForkFunc func(ctx context.Context, req EnsureForkRequest) (ForkInfo, error)
	// ForkLogin is the login EnsureFork's default behaviour forks as.
	ForkLogin string
	// MergePRFunc, when set, overrides the optional Merger capability.
	// Default: records the call and marks the recorded PR (found by number
	// across every branch OpenPR minted) merged, so a subsequent
	// FindPRByHeadBranch reads it back as merged — the same shape a real
	// merge produces, and what makes a funnel test's "landed" assertion
	// observable rather than merely "was called".
	MergePRFunc func(ctx context.Context, req MergePRRequest) error

	// Recorded calls, for test assertions.
	Pushes   []PushBranchRequest
	Opens    []OpenPRRequest
	Forks    []EnsureForkRequest
	AutoArms []EnableAutoMergeRequest
	MergePRs []MergePRRequest
	nextPR   int
	byBranch map[string]PRInfo
}

// NewFakeHost constructs a ready-to-use FakeHost.
func NewFakeHost() *FakeHost {
	return &FakeHost{byBranch: make(map[string]PRInfo), ForkLogin: "fork-owner"}
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

// TokenScopes delegates to TokenScopesFunc; by default it reports the scopes
// a workflow-capable classic token would carry, so a fake-backed test is not
// accidentally gated on a capability probe it never meant to exercise.
func (f *FakeHost) TokenScopes(ctx context.Context, cred Credential) ([]string, bool, error) {
	if f.TokenScopesFunc != nil {
		return f.TokenScopesFunc(ctx, cred)
	}
	return []string{"repo", "workflow"}, true, nil
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
	info, ok := f.byBranch[headRef(req.HeadOwner, req.Branch)]
	if !ok {
		return nil, nil
	}
	return &info, nil
}

// EnsureFork implements the optional Forker capability: it records the
// request and returns ForkLogin's fork of the same repo name, so the
// funnel's fork path is exercised without a network call.
func (f *FakeHost) EnsureFork(ctx context.Context, req EnsureForkRequest) (ForkInfo, error) {
	f.mu.Lock()
	f.Forks = append(f.Forks, req)
	fn := f.EnsureForkFunc
	login := f.ForkLogin
	f.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return ForkInfo{
		Repo:      Repo{Owner: login, Name: req.Repo.Name},
		RemoteURL: "https://example.invalid/" + login + "/" + req.Repo.Name + ".git",
	}, nil
}

// headRef renders GitHub's head filter (`<owner>:<branch>` for a fork,
// the bare branch when the head lives in the base repo) — the fake keys
// its PR table by exactly what OpenPR was given as Head, so a cross-fork
// PR is only found by a cross-fork lookup.
func headRef(owner, branch string) string {
	if owner == "" {
		return branch
	}
	return owner + ":" + branch
}

var (
	_ Host   = (*FakeHost)(nil)
	_ Forker = (*FakeHost)(nil)
	_ Forker = (*GitHubHost)(nil)
	_ Merger = (*FakeHost)(nil)
	_ Merger = (*GitHubHost)(nil)
)

// EnableAutoMerge implements the optional AutoMerger capability: it records
// the call and succeeds, unless EnableAutoMergeFunc overrides it.
//
// The fake satisfies AutoMerger deliberately. The funnel's idempotent-retry
// short-circuit re-arms auto-merge on the PR it finds, because OpenPR is not
// atomic on real GitHub — a fake that did NOT satisfy the capability would
// make the type assertion fail and leave that path untested.
func (f *FakeHost) EnableAutoMerge(ctx context.Context, req EnableAutoMergeRequest) error {
	f.mu.Lock()
	f.AutoArms = append(f.AutoArms, req)
	fn := f.EnableAutoMergeFunc
	f.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return nil
}

// MergePR implements the optional Merger capability: it records the request
// and, by default, marks the PR it names merged in byBranch (across
// whichever branch OpenPR minted it under) so a subsequent
// FindPRByHeadBranch observes the merge.
func (f *FakeHost) MergePR(ctx context.Context, req MergePRRequest) error {
	f.mu.Lock()
	f.MergePRs = append(f.MergePRs, req)
	fn := f.MergePRFunc
	f.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}

	f.mu.Lock()
	for branch, info := range f.byBranch {
		if info.Number == req.PRNumber {
			info.State = "merged"
			f.byBranch[branch] = info
		}
	}
	f.mu.Unlock()
	return nil
}
