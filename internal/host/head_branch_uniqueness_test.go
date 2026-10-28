package host

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// assertOneOpenPRPerHeadBranch is computed-not-listed-2026-08 P6 §8 row 9's
// own property, stated over the Host INTERFACE rather than any provider:
// opening a PR twice for the SAME head branch into the SAME base must leave
// AT MOST ONE open PR behind — the second OpenPR call must either fail or
// return the identical PR FindPRByHeadBranch already resolves. It names no
// provider (the operator's own recorded decision: a first-party SaaS host
// is coming, and a test naming GitHub's head-branch uniqueness would keep
// passing right up until a permissive second backend silently duplicated a
// PR into a counterparty's repository).
//
// Returns nil when the property holds, a descriptive error when it does
// not — a checker, not a self-failing subtest, so it can be run against
// both a correct implementation and a deliberately permissive one without
// either becoming a false test failure in the wrong place.
func assertOneOpenPRPerHeadBranch(ctx context.Context, h Host, open OpenPRRequest, find FindPRRequest) error {
	first, err := h.OpenPR(ctx, open)
	if err != nil {
		return fmt.Errorf("first OpenPR: %w", err)
	}
	second, err := h.OpenPR(ctx, open)
	if err == nil && second != first {
		return fmt.Errorf(
			"second OpenPR for head branch %q returned a DIFFERENT PR (#%d) than the first (#%d) instead of failing or returning the identical one — the head-branch-uniqueness property does not hold",
			open.Head, second.Number, first.Number,
		)
	}
	resolved, err := h.FindPRByHeadBranch(ctx, find)
	if err != nil {
		return fmt.Errorf("FindPRByHeadBranch: %w", err)
	}
	if resolved == nil {
		return fmt.Errorf("FindPRByHeadBranch found no PR for head branch %q after OpenPR", open.Head)
	}
	if resolved.Number != first.Number {
		return fmt.Errorf("FindPRByHeadBranch resolved PR #%d, not the first OpenPR's #%d — more than one PR exists for head branch %q", resolved.Number, first.Number, open.Head)
	}
	return nil
}

// TestOneOpenPRPerHeadBranch_CorrectImplementationHolds is the property's
// GREEN case: a Host whose OpenPR checks FindPRByHeadBranch first — the
// idempotent-retry shape every Host implementation must provide, and what
// real GitHub's own duplicate-PR refusal (spec §0.5: "the TOCTOU is closed
// by GitHub's own duplicate refusal") gives for free — never leaves a
// second, distinct open PR behind.
func TestOneOpenPRPerHeadBranch_CorrectImplementationHolds(t *testing.T) {
	t.Parallel()

	f := NewFakeHost()
	f.OpenPRFunc = func(ctx context.Context, req OpenPRRequest) (PRInfo, error) {
		if existing, _ := f.FindPRByHeadBranch(ctx, FindPRRequest{Repo: req.Repo, Branch: req.Head}); existing != nil {
			return *existing, nil
		}
		f.mu.Lock()
		f.nextPR++
		info := PRInfo{Number: f.nextPR, URL: "https://example.invalid/pr/" + req.Head, State: "open", Title: req.Title, Body: req.Body, BaseBranch: req.Base}
		f.byBranch[req.Head] = info
		f.mu.Unlock()
		return info, nil
	}

	req := OpenPRRequest{Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main"}
	if err := assertOneOpenPRPerHeadBranch(context.Background(), f, req, FindPRRequest{Repo: req.Repo, Branch: req.Head}); err != nil {
		t.Fatalf("a correct (idempotent-retry) Host implementation failed the property: %v", err)
	}
}

// TestOneOpenPRPerHeadBranch_PermissiveFakeReds is AC row 9's own "reds
// against a Host fake that permits duplicates" — and the fake it reds
// against is FakeHost's OWN default OpenPR behaviour: unconfigured, it
// mints a NEW, distinct PR number on every call regardless of whether the
// head branch already has one open (fake.go's own OpenPR: "mints a
// deterministic incrementing PR number" — unconditionally, never
// consulting byBranch first). That is a real, standing finding, not a
// fixture built to fail on purpose: every existing internal/space funnel
// test that relies on FakeHost's zero-value OpenPR behaviour is NOT
// exercising this property today, because nothing asserted it before this
// phase. FakeHost's own behaviour is left unchanged here (internal/space's
// tests depend on it and are outside this phase's footprint) — this test
// only proves the checker itself is not vacuous.
func TestOneOpenPRPerHeadBranch_PermissiveFakeReds(t *testing.T) {
	t.Parallel()

	f := NewFakeHost() // no OpenPRFunc override — the permissive default.
	req := OpenPRRequest{Repo: Repo{Owner: "acme", Name: "space"}, Head: "a2a/axon/XQ-axon-1", Base: "main"}

	err := assertOneOpenPRPerHeadBranch(context.Background(), f, req, FindPRRequest{Repo: req.Repo, Branch: req.Head})
	if err == nil {
		t.Fatal("expected the property to red against FakeHost's permissive default OpenPR, got nil")
	}
	const wantSubstr = "head-branch-uniqueness property does not hold"
	if got := err.Error(); !strings.Contains(got, wantSubstr) {
		t.Fatalf("error = %q, want it to name the property (substring %q)", got, wantSubstr)
	}
}

// TestOneOpenPRPerHeadBranch_LocalHost runs the property against the local host:
// a second OpenPR for the same head returns the identical landed PR, and
// FindPRByHeadBranch resolves that one.
func TestOneOpenPRPerHeadBranch_LocalHost(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-1"
	r.pushHead(t, h, r.clone("axon"), head, "axon/docs/a.md", "a\n", "feat: a\n")
	req := OpenPRRequest{Repo: testRepo, Head: head, Base: "main", Title: "t", Body: "b"}
	if err := assertOneOpenPRPerHeadBranch(context.Background(), h, req, FindPRRequest{Repo: testRepo, Branch: head}); err != nil {
		t.Fatalf("LocalHost fails the head-branch-uniqueness property: %v", err)
	}
}

// TestOneOpenPRPerHeadBranch_LocalHostNewHeadOnReusedName is the reuse half of
// the property: a branch name carries many writes over time, and a new head on a
// name that already landed must land as a new record, not answer with the old one.
func TestOneOpenPRPerHeadBranch_LocalHostNewHeadOnReusedName(t *testing.T) {
	t.Parallel()
	r := newLocalRig(t)
	h := r.host(t, nil, "local:axon")
	head := "a2a/axon/submit/XQ-axon-1"
	ctx := context.Background()
	var numbers []int
	for i, file := range []string{"axon/docs/a.md", "axon/docs/b.md"} {
		sha := r.pushHead(t, h, r.clone("axon"), head, file, "x\n", "feat: write\n")
		req := OpenPRRequest{Repo: testRepo, Head: head, Base: "main", Title: fmt.Sprintf("write %d", i), Body: "b"}
		if err := assertOneOpenPRPerHeadBranch(ctx, h, req, FindPRRequest{Repo: testRepo, Branch: head}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		got, err := h.FindPRByHeadBranch(ctx, FindPRRequest{Repo: testRepo, Branch: head})
		if err != nil || got == nil || got.HeadSHA != sha || got.Title != req.Title {
			t.Fatalf("write %d: FindPRByHeadBranch = %+v, %v; want the new write %s", i, got, err, sha)
		}
		numbers = append(numbers, got.Number)
	}
	if numbers[0] == numbers[1] {
		t.Errorf("both writes got number %d", numbers[0])
	}
}
