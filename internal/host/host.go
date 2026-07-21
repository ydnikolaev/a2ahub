// Package host implements the §T1 5-primitive host adapter interface plus
// its v1 GitHub implementation: push an ephemeral branch, open a PR with
// auto-merge always enabled, read the required-check and review-approval
// state, and locate an already-open/merged PR by its deterministic head
// branch (the idempotent-retry read path).
//
// This package is GitHub-specific mechanics ONLY — it never sees
// space.yaml, never orchestrates a multi-step flow, and never decides
// whether a path is gated (D-019, D-002; see spec 05 §T1 "Gating needs no
// OpenPR parameter"). internal/space is its only caller. Imports: stdlib +
// internal/artifact only (ADR-001) — this package does not import
// internal/artifact today, but the constraint still holds: no other
// a2ahub package.
package host

import "context"

// Repo identifies a GitHub-hosted space repository by owner/name. No
// space.yaml or a2ahub domain concept (system, artifact type, ...) appears
// here — that soft coupling is the point (D-019, spec 05 §9).
type Repo struct {
	Owner string
	Name  string
}

// Credential is a resolved write credential (a fine-grained PAT or GitHub
// App installation token) handed to a Host call at the point of use only.
// It deliberately carries no yaml/json struct tags so it can never be
// marshaled by accident; callers (internal/space's credential resolution)
// must never log it, persist it, or place it inside a struct that
// serializes (§10.5, CC-085's sibling secrecy guard).
type Credential struct {
	Token string
}

// PushBranchRequest describes an ephemeral-branch push (§4.2 D-002): push
// a local commit ref, already committed in RepoDir, to RemoteURL as
// Branch (the deterministic a2a/<system>/<id> name).
type PushBranchRequest struct {
	// RepoDir is the local git working directory (a mirror clone) that
	// already holds the commit to push.
	RepoDir string
	// LocalRef is the local commit-ish to push (e.g. "HEAD" or a branch
	// name already checked out in RepoDir).
	LocalRef string
	// Branch is the deterministic target branch name on the remote.
	Branch string
	// RemoteURL is the push target, e.g. "https://github.com/o/r.git" (or
	// a local filesystem path in tests — testkit/spacefixture).
	RemoteURL string
	// Credential authenticates the push. Zero value is valid for
	// credential-less remotes (local fixtures); real GitHub pushes always
	// supply one.
	Credential Credential
}

// PushBranchResult confirms the branch pushed.
type PushBranchResult struct {
	Branch string
}

// OpenPRRequest opens a PR from Head into Base with auto-merge enabled.
// OpenPR is UNIFORM (spec 05 §T1 "Gating needs no OpenPR parameter") — this
// request has no field that turns gating on or off; gate enforcement is
// CODEOWNERS + the V3 required check blocking auto-merge from firing, not
// an API parameter.
type OpenPRRequest struct {
	Repo       Repo
	Head       string
	Base       string
	Title      string
	Body       string
	Credential Credential
}

// PRInfo is the minimal PR handle other phases (P7's cache, P8's gated
// verbs) build against: number, URL, and the host-observed lifecycle
// state ("open" | "merged" | "closed"). This is deliberately NOT the same
// concept as WriteResult.State in internal/space (pending-merge / etc.) —
// PRInfo.State is what the host currently observes; WriteResult.State is
// the funnel's own claim about the outcome.
type PRInfo struct {
	Number int
	URL    string
	State  string
}

// StatusRequest identifies the PR whose check/review state is queried.
type StatusRequest struct {
	Repo       Repo
	PRNumber   int
	Credential Credential
}

// CheckStatusResult reports the `a2a-validate` required status check (V3)
// result for a PR (spec 05 §T1, §10.3 enforcement-layering note).
type CheckStatusResult struct {
	// State is the check's run state: "queued" | "in_progress" | "completed".
	State string
	// Conclusion is populated once State == "completed":
	// "success" | "failure" | "neutral" | ... (GitHub's check-run vocabulary).
	Conclusion string
}

// ReviewStatusResult reports the CODEOWNERS-required review approval state
// for a PR touching a gated path (spec 05 §T1, §4.2 CODEOWNERS rule).
type ReviewStatusResult struct {
	// Approved is true once every reviewer whose latest review this
	// package observed is an approval and at least one approval exists.
	// It does NOT know which logins CODEOWNERS actually requires — that
	// mapping lives in space.yaml (P3/P8's concern); this is the raw READ
	// surface only (soft coupling, spec 05 §9).
	Approved bool
	// Pending lists logins whose latest observed review is not an
	// approval (diagnostic only).
	Pending []string
}

// FindPRRequest identifies a PR by its deterministic head branch name.
type FindPRRequest struct {
	Repo       Repo
	Branch     string
	Credential Credential
}

// Host is the 5-primitive host adapter interface (spec 05 §T1). It is
// host-agnostic by design (D-019, Q-004 tracked): a GitLab/Gitea profile is
// a new implementation of this same interface, never a redesign.
// Implementations never see space.yaml and never orchestrate a multi-step
// flow — internal/space is the only caller.
type Host interface {
	// PushBranch pushes a local commit ref to the space repo's remote as
	// the deterministic branch a2a/<system>/<id>, authenticated with the
	// caller-supplied write credential. A rejected push (CC-061) returns
	// an error wrapping ErrPushRejected and leaves no partial state.
	PushBranch(ctx context.Context, req PushBranchRequest) (PushBranchResult, error)
	// OpenPR opens a PR from the pushed branch into Base with auto-merge
	// enabled — UNIFORM, always (see OpenPRRequest doc).
	OpenPR(ctx context.Context, req OpenPRRequest) (PRInfo, error)
	// CheckStatus reads the `a2a-validate` required status check result.
	CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error)
	// ReviewStatus reads the CODEOWNERS-required review approval state.
	ReviewStatus(ctx context.Context, req StatusRequest) (ReviewStatusResult, error)
	// FindPRByHeadBranch looks up an existing open or merged PR by its
	// deterministic head branch name — the idempotent-retry read path (no
	// dependency on internal/cache, P7). Returns (nil, nil) when no
	// matching open/merged PR exists.
	FindPRByHeadBranch(ctx context.Context, req FindPRRequest) (*PRInfo, error)
}
