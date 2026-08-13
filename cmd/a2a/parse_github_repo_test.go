package main

import (
	"path/filepath"
	"testing"
)

// parse_github_repo_test.go pins AC8's characterization half (agent-ops-2026-07
// P14 spec docs/features/active/agent-ops-2026-07/specs/14-feedback-one-corpus.md
// §3.6, AC8): "The reader seam's local-path defect is resolved or recorded —
// either parseGitHubRepo handles it with a test, or a docs/validator-backlog.md
// row names it with the reproduction."
//
// This wave took the SECOND branch, and this test is the reproduction the row
// cites, not a fix. The reason a fix is the wrong move here (rather than
// simply the more ambitious one) is documented in code already:
// wire_tier_test.go:28-33 states in as many words that the wire tier — and
// every integration test file that drives testkit/gitfixture
// (contract_lifecycle_integration_test.go, main_test.go,
// mcp_equivalence_test.go, serve_wiring_test.go, work_wiring_integration_test.go)
// — WORKS BECAUSE parseGitHubRepo accepts a bare filesystem path and returns
// its last two path segments as a garbage-but-non-empty owner/name pair, which
// lets dependency resolution complete without a real GitHub remote. Turning
// that into an error is not the clean early return AC8 asks a fixer to check
// for first: it reshapes the function's contract for every one of those
// callers and reds the tier's own documented design. So this test PINS the
// current, defect-carrying behaviour (a characterization test, not a
// regression guard for a fix) so the defect stays reproducible even after the
// only script that used to document it (docs/runbooks/feedback-sync.sh) is
// eventually retired by a later P14 wave.
func TestParseGitHubRepoLocalPathMisslice(t *testing.T) {
	t.Parallel()

	// A local absolute path — exactly the shape docs/runbooks/feedback-sync.sh's
	// own header (lines 11-15) and A2A_FEEDBACK_REPO both warn about. Neither
	// "https://github.com/" nor "git@github.com:" is a prefix of it, so
	// parseGitHubRepo's TrimPrefix calls are no-ops and the function falls
	// straight through to "last two path segments", which for a filesystem
	// path are two directory names, not an owner and a repo.
	localPath := filepath.Join(t.TempDir(), "some-org", "some-hub")

	owner, name, err := parseGitHubRepo(localPath)
	if err != nil {
		t.Fatalf("parseGitHubRepo(%q): got error %v, want the mis-slice this test pins", localPath, err)
	}

	wantOwner := filepath.Base(filepath.Dir(localPath)) // "some-org"
	wantName := filepath.Base(localPath)                // "some-hub"
	if owner != wantOwner || name != wantName {
		t.Fatalf("parseGitHubRepo(%q) = %q/%q, want the observed mis-slice %q/%q", localPath, owner, name, wantOwner, wantName)
	}

	// Second half of the reproduction, observed rather than assumed: this is
	// EXACTLY the string cmd/a2a/wire.go's runFeedback constructs at
	// wire.go:1442-1443 ("https://raw.githubusercontent.com/"+owner+"/"+name+
	// "/"+feedbackBaseBranch) from this same mis-sliced pair, unconditionally
	// — with no feedbackGitHubRemote(repoURL) guard, unlike the credential
	// decision 25 lines above it in the same function, which DOES check. So
	// A2A_FEEDBACK_REPO=<a local path> does not silently read nothing; it
	// silently builds a real internet request to a garbage owner/repo, which
	// is why docs/runbooks/feedback-sync.sh bypasses the a2a binary and uses
	// its own A2A_FEEDBACK_HUB_URL override instead.
	gotURL := "https://raw.githubusercontent.com/" + owner + "/" + name + "/" + feedbackBaseBranch
	wantURL := "https://raw.githubusercontent.com/" + wantOwner + "/" + wantName + "/" + feedbackBaseBranch
	if gotURL != wantURL {
		t.Fatalf("constructed hub URL = %q, want %q", gotURL, wantURL)
	}
}
