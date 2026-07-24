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
	// AutoMergeArmed reports whether auto-merge is actually armed on the PR.
	//
	// It exists because arming is a SECOND call that GitHub can decline for
	// reasons that are not failures of the write: the repository has
	// auto-merge turned off, or the PR is already mergeable so there is
	// nothing to wait for. Both leave a real, open PR — so failing the whole
	// submit would be wrong, and silently claiming it will merge unattended
	// (which is what swallowing the GraphQL error did) is worse.
	AutoMergeArmed bool
	// AutoMergeNote explains, in words the operator can act on, why arming
	// did not happen. Empty when AutoMergeArmed is true.
	AutoMergeNote string
}

// StatusRequest identifies the PR whose check/review state is queried.
type StatusRequest struct {
	Repo       Repo
	PRNumber   int
	Credential Credential
}

// CheckStatusResult reports the `a2a-validate` required status check (V3)
// result for a PR (spec 05 §T1, §10.3 enforcement-layering note).
//
// The check run is named `a2a-validate` on an un-migrated space and
// `a2a-validate / <reusable-job>` on a P33-migrated one (GitHub names a
// called workflow's run `<caller-job> / <reusable-job>`); both shapes resolve
// here — see selectRequiredCheckRun (spec 34 §2).
type CheckStatusResult struct {
	// State is the check's run state: "queued" | "in_progress" | "completed".
	State string
	// Conclusion is populated once State == "completed":
	// "success" | "failure" | "neutral" | ... (GitHub's check-run vocabulary).
	Conclusion string
	// Name is the selected check run's own name — the evidence of WHICH
	// shape answered (diagnostic only; empty when no run matched).
	Name string
	// Ambiguous lists every candidate name when more than one compound run
	// matched. The pick stays deterministic (lexicographically first) and
	// this field is how the caller learns the choice was not unique — a
	// silent pick is exactly what spec 34 §2.4 forbids. Empty otherwise.
	Ambiguous []string
	// HeadSHA is the commit the selected run actually executed against —
	// the evidence of WHICH REF answered, the sibling of Name's which-shape.
	//
	// It exists because a conclusion alone is not a verdict. GitHub can serve
	// a check run computed against a stale merge commit after the base moved
	// (observed twice during the 2026-07-24 getvisa migration), so a caller
	// that reads Conclusion without comparing this to the PR's current head
	// can report a green that was never tested, or a red already fixed. Any
	// consumer that waits for green — the `submit --wait` backlog row, and
	// P36's live tier today — needs it to be right rather than merely
	// plausible. Empty when no run matched.
	HeadSHA string
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
	Repo   Repo
	Branch string
	// HeadOwner qualifies the head reference when the branch lives in a
	// FORK rather than in Repo itself (P28): GitHub's head filter is
	// `<owner>:<branch>`, so a cross-fork PR is invisible to a lookup that
	// assumes Repo.Owner. Empty means "the branch is in Repo" — today's
	// behaviour, byte for byte.
	HeadOwner  string
	Credential Credential
}

// EnsureForkRequest asks for a fork of Repo owned by whoever Credential
// authenticates as. Idempotent by construction: an existing fork is
// returned, never duplicated.
type EnsureForkRequest struct {
	Repo       Repo
	Credential Credential
}

// ForkInfo is a fork the credential holder may push to: its owner/name and
// the push URL (RemoteURL feeds PushBranchRequest verbatim).
type ForkInfo struct {
	Repo      Repo
	RemoteURL string
}

// Forker is an OPTIONAL capability a Host MAY satisfy — deliberately not a
// 6th method on Host (ADR-003). The 5-primitive core stays the contract
// every host profile must meet; a profile whose platform has no fork
// concept simply does not implement this, and the caller
// (space.WriteFunnel) degrades to reporting the original push refusal.
//
// Callers must obtain it by type assertion on a Host, never by widening
// their own dependency to a concrete implementation.
type Forker interface {
	// EnsureFork returns the credential holder's fork of req.Repo,
	// creating it if it does not exist yet and waiting until the host
	// reports it usable. Returns an error wrapping ErrForkUnavailable when
	// the fork can neither be found nor created.
	EnsureFork(ctx context.Context, req EnsureForkRequest) (ForkInfo, error)
}

// EnableAutoMergeRequest identifies the PR whose auto-merge is (re-)armed.
type EnableAutoMergeRequest struct {
	Repo       Repo
	PRNumber   int
	Credential Credential
}

// AutoMerger is an OPTIONAL capability a Host MAY satisfy — deliberately not
// a 6th method on Host, the same shape as Forker (ADR-003). Obtain it by type
// assertion on a Host, never by widening a caller's dependency to a concrete
// implementation.
//
// It exists because OpenPR is NOT atomic on GitHub: creating the PR and
// arming auto-merge are two API calls (REST create, then a GraphQL
// `enablePullRequestAutoMerge` mutation). A failure between them — or a 5xx
// on the create whose write actually landed, which real GitHub does — leaves
// a PR that exists, passes its checks, and will never merge. The funnel's
// idempotent-retry short-circuit then finds that PR and reports success,
// so the write looks submitted and silently is not.
//
// Observed live on 2026-07-24 (P36's matrix): a 504 on OpenPR left PR #2 of
// the test space open with a green required check and `auto_merge: null`, and
// every subsequent `a2a submit` answered "already submitted … already-open".
//
// Implementations MUST be idempotent: arming auto-merge on a PR that already
// has it is a no-op success, because the retry path cannot know which state
// it is repairing.
type AutoMerger interface {
	EnableAutoMerge(ctx context.Context, req EnableAutoMergeRequest) error
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
	// CheckStatus reads the `a2a-validate` required status check result —
	// flat or compound-named (spec 34 §2).
	CheckStatus(ctx context.Context, req StatusRequest) (CheckStatusResult, error)
	// TokenScopes reports the OAuth scopes GitHub attributes to cred.
	//
	// reported=false means the credential TYPE does not advertise scopes at
	// all — a fine-grained PAT or a GitHub App installation token — which is
	// NOT the same as holding none. A caller that treats "not reported" as
	// "missing" would refuse exactly the credentials that are most narrowly
	// scoped, so the boolean is deliberately separate from the slice.
	TokenScopes(ctx context.Context, cred Credential) (scopes []string, reported bool, err error)
	// ReviewStatus reads the CODEOWNERS-required review approval state.
	ReviewStatus(ctx context.Context, req StatusRequest) (ReviewStatusResult, error)
	// FindPRByHeadBranch looks up an existing open or merged PR by its
	// deterministic head branch name — the idempotent-retry read path (no
	// dependency on internal/cache, P7). Returns (nil, nil) when no
	// matching open/merged PR exists.
	FindPRByHeadBranch(ctx context.Context, req FindPRRequest) (*PRInfo, error)
}
