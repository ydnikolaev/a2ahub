package space

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	gopath "path"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/operation"
)

// WriteState is the funnel's own claim about a Submit outcome — the
// contract P7's cache persistence (out of this phase's footprint) is
// built against. Kept stable across phases per spec 05 §9.
type WriteState string

const (
	// WriteStatePendingMerge is returned after a fresh push+PR-open: the
	// PR is open and auto-merge is armed, but not yet merged.
	WriteStatePendingMerge WriteState = "pending-merge"
	// WriteStateAlreadyOpen is returned by the idempotent short-circuit
	// (step 0) when a re-run finds an already-open PR for the
	// deterministic branch.
	WriteStateAlreadyOpen WriteState = "already-open"
	// WriteStateAlreadyMerged is returned by the idempotent short-circuit
	// when the deterministic branch's PR was ALREADY merged before this
	// call — including the repair path, where a re-run found the PR open and
	// landed it: the artifact was submitted by an earlier invocation either
	// way, which is what the caller's "already submitted" message says.
	//
	// The go-auditor read that repair case as the same lie WriteStateMerged
	// exists to prevent (2026-07-25). Adjudicated: it is not. The short-circuit
	// only fires when FindPRByHeadBranch already found a PR, which means a
	// PREVIOUS invocation pushed and opened it — so "already submitted" is
	// literally true, and the only imprecision left is that the caller cannot
	// tell "it was merged before I ran" from "I landed what a prior call
	// opened". That distinction changes nothing a caller does (nothing is
	// pending either way), so it stays one state rather than three.
	WriteStateAlreadyMerged WriteState = "already-merged"
	// WriteStateMerged is returned when THIS call did the whole thing:
	// a fresh push + PR-open, GitHub declined to arm auto-merge because the
	// PR was already mergeable, the required check read explicitly green,
	// and the funnel merged it directly (WAVE M4).
	//
	// It is a SEPARATE value from WriteStateAlreadyMerged, and that
	// distinction is not bookkeeping: `a2a submit` prints "already
	// submitted" for already-merged, which would be a plain lie about a
	// write this call had just performed for the first time. Every consumer
	// treats it like the already-* states for the purpose that matters —
	// there is nothing pending and no marker to track — but the sentence
	// the user reads is true. (M4 first shipped this as a reuse of
	// already-merged, because the consumers switching on WriteState were off
	// that wave's allowlist; that was an allowlist artifact, not a design
	// conclusion, and the lead split it.)
	WriteStateMerged WriteState = "merged"
	// WriteStateNeedsAttention means a PR is confirmed but unattended merge
	// is not armed. Note names the operator-visible unfinished condition.
	WriteStateNeedsAttention WriteState = "needs-attention"
	// WriteStateOutcomeUnknown means a later provider side effect may have
	// happened but could not be observed. Stage remains the last proven fact.
	WriteStateOutcomeUnknown WriteState = "outcome-unknown"
)

// WriteStage is monotonic evidence about the furthest durable boundary a
// funnel invocation proved.
type WriteStage string

const (
	WriteStageNone           WriteStage = "none"
	WriteStageCommitted      WriteStage = "committed"
	WriteStagePushed         WriteStage = "pushed"
	WriteStagePRCreated      WriteStage = "pr-created"
	WriteStageAutoMergeArmed WriteStage = "auto-merge-armed"
	WriteStageMerged         WriteStage = "merged"
)

// RemainingAction is the closed machine-readable next step derived from
// Stage and State. It is never reconstructed from Note.
type RemainingAction string

const (
	RemainingActionNone                   RemainingAction = "none"
	RemainingActionRetryPush              RemainingAction = "retry-push"
	RemainingActionEnsurePR               RemainingAction = "ensure-pr"
	RemainingActionArmAutoMerge           RemainingAction = "arm-auto-merge"
	RemainingActionWaitForGates           RemainingAction = "wait-for-gates"
	RemainingActionResolvePR              RemainingAction = "resolve-pr"
	RemainingActionObserveProviderOutcome RemainingAction = "observe-provider-outcome"
)

var (
	ErrInvalidWriteOutcome     = errors.New("space: invalid write stage/state combination")
	ErrExpectedHeadUnavailable = errors.New("space: host did not return the pull request head checked by CI")
	ErrSubmitValidatorRequired = errors.New("space: final submission validator is required at the operational-confidence floor")
)

// FileWrite is one file the write funnel commits — a path (relative to
// the mirror clone's working directory) and its full content.
type FileWrite struct {
	Path    string
	Content []byte
}

// SubmitRequest is one write-funnel invocation (§4.2 D-002, §7): commit
// Files as ONE commit (an artifact file + its first lifecycle event, per
// D-026) on the deterministic branch a2a/<System>/<ArtifactID>, push, open
// a PR with auto-merge enabled.
type SubmitRequest struct {
	// RepoDir is the local mirror clone's working directory (already
	// cloned/fetched via CloneOrFetch) that the commit is made in.
	RepoDir string
	// System is the authoring system (branch name + section guard).
	System string
	// Verb names the WRITE this request is — "submit", "ack", "accept",
	// "contract-publish", … It is part of the branch name, which is what
	// makes two different transitions on one artifact two different
	// branches (see BranchName). Required: a write that cannot say which
	// write it is cannot be told apart from the previous one.
	Verb string
	// ArtifactID is the artifact's §3.3 id (branch name suffix).
	ArtifactID string
	// ArtifactIDs names every public protocol id produced by this write.
	// Operation-key retries return the IDs recorded on the original PR rather
	// than newly minted date-bearing IDs that were never submitted.
	ArtifactIDs []string
	// OperationKey opts this request into semantic idempotency across public
	// ID/date changes. It is internal branch identity, never protocol data.
	OperationKey string
	// Files are committed together, exactly once. Every Path must be
	// under System's own section (or decisions/, the funnel-level
	// exception) — checked BEFORE any git action.
	Files []FileWrite
	// Mutations is the mixed write/delete form. FileWrite remains the
	// compatibility adapter for write-only callers; duplicate paths across
	// the two collections are refused.
	Mutations []Mutation
	// CommitMessage, CommitAuthorName/Email: P6 supplies these (the exact
	// "a2a(<type>): <id>" convention, OP-205, is a CLI-layer concern).
	CommitMessage     string
	CommitAuthorName  string
	CommitAuthorEmail string

	// RemoteURL is the push target (real GitHub URL, or a local fixture
	// path in tests).
	RemoteURL string
	// Repo identifies the GitHub repo for the OpenPR/FindPRByHeadBranch
	// calls (owner/name) — distinct from RemoteURL, which is what `git
	// push` uses.
	Repo host.Repo
	// BaseBranch is the PR's target branch (normatively "main", §4.2).
	BaseBranch string
	// ExpectedBaseSHA freezes the exact parent tree for a prepared write.
	// Compatibility one-shot callers may leave it empty and use origin/base.
	ExpectedBaseSHA string
	// PRTitle/PRBody are passed through to host.OpenPR verbatim.
	PRTitle string
	PRBody  string

	Credential host.Credential

	// AllowForkFallback opts THIS write into the P28 fork path: if the
	// push is refused because the credential may not write to Repo, the
	// funnel ensures the submitter's own fork, pushes there, and opens a
	// cross-fork PR. Off by default — a space write is authored by a
	// system that owns its section and is expected to have write access,
	// so a refusal there is a credential fault to report, not to route
	// around. `a2a feedback submit` is the one caller that sets it: its
	// audience is precisely the non-collaborator.
	AllowForkFallback bool
	// ReplaceOrphanBranch allows this deterministic, tool-owned branch to
	// replace a prior remote head only with an exact force-with-lease. The
	// funnel performs this after its no-PR check. Feedback submission is the
	// sole caller; ordinary space writes remain fast-forward-only.
	ReplaceOrphanBranch bool

	// MinBinaryVersion is space.yaml's pin for the CC-085 guard (caller
	// already parsed the manifest; the funnel does not parse YAML).
	MinBinaryVersion string

	// AllowSpaceInfrastructure opts THIS write into admitting the fixed set
	// of space-infrastructure paths ("space.yaml", "CODEOWNERS",
	// "BRANCH-PROTECTION.md", anything under ".github/") past the section
	// guard, IN ADDITION TO the caller's own section. Off by default — an
	// ordinary system write has no business touching the space's shared
	// infrastructure, and admitting it unconditionally would let any writer
	// edit CODEOWNERS or the CI workflow through the funnel meant to enforce
	// the single-writer boundary. V3 diff-authz (internal/cli/cmd_validate_ci.go)
	// already carves these exact paths out of per-author section review and
	// treats them as governed instead by CODEOWNERS review + branch
	// protection — so this flag lets the two guards agree on what
	// "space infrastructure" means rather than silently disagreeing. The one
	// caller that sets it is spec 35's `a2a space update` migration path.
	AllowSpaceInfrastructure bool
}

// WriteResult is what Submit returns: the contract P7's cache persistence
// (pending-merge marker) and P8's gated verbs are built against (spec 05
// §7, §9 — "keep that return contract stable across phases").
type WriteResult struct {
	Branch    string
	PRNumber  int
	PRURL     string
	CommitSHA string
	State     WriteState
	Stage     WriteStage
	// ArtifactIDs are the IDs actually present on the operation's PR. They
	// may differ from this invocation's candidates on a cross-midnight retry.
	ArtifactIDs []string
	// MergeMethod is the method selected by the host. The funnel never
	// guesses or reconstructs it.
	MergeMethod host.MergeMethod
	// RemainingAction is derived solely from Stage and State.
	RemainingAction RemainingAction
	// Note is the actionable explanation for needs-attention and related
	// partial outcomes.
	Note string
	// AutoMergeNote is the deprecated compatibility alias for Note. New
	// consumers branch on State and RemainingAction and render Note only as
	// operator-facing context.
	AutoMergeNote string
}

// RemainingActionFor is the total stage/state mapping defined by P4. Invalid
// combinations return ErrInvalidWriteOutcome rather than guessing.
func RemainingActionFor(stage WriteStage, state WriteState) (RemainingAction, error) {
	switch state {
	case WriteStateMerged, WriteStateAlreadyMerged:
		if stage != WriteStageMerged {
			return "", ErrInvalidWriteOutcome
		}
		return RemainingActionNone, nil
	}
	if stage == WriteStageMerged {
		if state == "" {
			return RemainingActionNone, nil
		}
		return "", ErrInvalidWriteOutcome
	}
	if !validNonMergedStage(stage) {
		return "", ErrInvalidWriteOutcome
	}
	if state == WriteStateOutcomeUnknown {
		return RemainingActionObserveProviderOutcome, nil
	}
	switch state {
	case WriteStateNeedsAttention:
		if stage != WriteStagePRCreated {
			return "", ErrInvalidWriteOutcome
		}
		return RemainingActionResolvePR, nil
	case WriteStatePendingMerge:
		if stage != WriteStageAutoMergeArmed {
			return "", ErrInvalidWriteOutcome
		}
		return RemainingActionWaitForGates, nil
	case WriteStateAlreadyOpen:
		switch stage {
		case WriteStagePRCreated:
			return RemainingActionArmAutoMerge, nil
		case WriteStageAutoMergeArmed:
			return RemainingActionWaitForGates, nil
		default:
			return "", ErrInvalidWriteOutcome
		}
	case "":
		switch stage {
		case WriteStageNone, WriteStageCommitted:
			return RemainingActionRetryPush, nil
		case WriteStagePushed:
			return RemainingActionEnsurePR, nil
		case WriteStagePRCreated:
			return RemainingActionArmAutoMerge, nil
		case WriteStageAutoMergeArmed:
			return RemainingActionWaitForGates, nil
		default:
			return "", ErrInvalidWriteOutcome
		}
	default:
		return "", ErrInvalidWriteOutcome
	}
}

func validNonMergedStage(stage WriteStage) bool {
	switch stage {
	case WriteStageNone, WriteStageCommitted, WriteStagePushed, WriteStagePRCreated, WriteStageAutoMergeArmed:
		return true
	default:
		return false
	}
}

func finalizeWriteResult(result WriteResult) (WriteResult, error) {
	action, err := RemainingActionFor(result.Stage, result.State)
	if err != nil {
		return result, err
	}
	if result.State == WriteStateNeedsAttention && result.Note == "" {
		return result, ErrInvalidWriteOutcome
	}
	result.RemainingAction = action
	result.AutoMergeNote = result.Note // deprecated compatibility alias
	return result, nil
}

func baseWriteResult(branch string, artifactIDs []string) WriteResult {
	return WriteResult{
		Branch: branch, Stage: WriteStageNone,
		RemainingAction: RemainingActionRetryPush,
		ArtifactIDs:     append([]string(nil), artifactIDs...),
	}
}

func completeWriteResult(op, input string, result WriteResult) (WriteResult, error) {
	result, err := finalizeWriteResult(result)
	if err != nil {
		return result, &Error{Op: op, Input: input, Err: err}
	}
	return result, nil
}

func failWriteResult(op, input string, result WriteResult, cause error) (WriteResult, error) {
	result, err := finalizeWriteResult(result)
	if err != nil {
		cause = errors.Join(cause, err)
	}
	return result, &Error{Op: op, Input: input, Err: cause}
}

// SubmitValidator is the consumer-side seam (rails ISP/DI) for V2
// validation of the artifact+event pair before it enters the write funnel
// (P3 internal/validate, wired for real at P6). internal/space depends on
// this interface only — never a concrete validate.Engine (ADR-001's
// import grant is a ceiling, not a mandate; plan 05 Placement decisions).
type SubmitValidator interface {
	// ValidateSubmit validates files about to be committed and returns a
	// non-nil error describing every violation found (or nil).
	ValidateSubmit(ctx context.Context, files []FileWrite) error
}

// WriteFunnel implements the D-002/D-026 single write funnel: the ONLY
// code path internal/space exposes for mutating a space (rails: "one
// write shape"). It is the sole caller of internal/host.
type WriteFunnel struct {
	host              host.Host
	validator         SubmitValidator
	mutationAuthority *mutationAuthority
	// binaryVersion is injected via constructor DI (plan 05 Placement
	// decision: "the version stamp lives in cmd/a2a; space never reads
	// build info itself") — used only for the CC-085 guard.
	binaryVersion string
}

// NewWriteFunnel constructs a WriteFunnel. New-floor writes fail closed when
// validator is nil. The temporary nil allowance exists only so pre-v0.19
// compatibility tests and old embedding callers remain readable while they
// migrate to an explicit domain validator.
func NewWriteFunnel(h host.Host, validator SubmitValidator, binaryVersion string) *WriteFunnel {
	return &WriteFunnel{
		host: h, validator: validator, binaryVersion: binaryVersion,
		mutationAuthority: newMutationAuthority(),
	}
}

// submitPreparedRequest runs an already-prepared request end to end. The
// exported Submit and SubmitPrepared entry points in prepared.go are the only
// callers: stamping and validation happen before this function and are never
// repeated here.
//
//	(0) FindPRByHeadBranch short-circuit for a2a/<system>/<id> — an
//	    existing open/merged PR returns immediately, no second
//	    push/open cycle (AC-301.1 idempotency).
//	(1) section guard (wrong-section files refused before any git
//	    action) + the min_binary_version guard (CC-085) + the
//	    SubmitValidator seam (V2).
//	(2) ONE commit = every req.Files entry.
//	(3) host.PushBranch to a2a/<system>/<id>.
//	(4) host.OpenPR with auto-merge enabled (uniform, D-002).
//	(5) return the write-result.
func (f *WriteFunnel) submitPreparedRequest(ctx context.Context, req SubmitRequest, prior WriteResult, prepared *PreparedSubmission) (WriteResult, error) {
	const op = "Submit"
	if req.Verb == "" {
		return baseWriteResult("", req.ArtifactIDs), &Error{Op: op, Input: req.ArtifactID, Err: ErrMissingVerb}
	}
	branchID := req.ArtifactID
	if req.OperationKey != "" {
		if !operation.Valid(req.OperationKey) {
			return baseWriteResult("", req.ArtifactIDs), &Error{Op: op, Input: req.OperationKey, Err: ErrInvalidOperationKey}
		}
		branchID = req.OperationKey
	}
	if err := validateBranchSegments(req.System, req.Verb, branchID); err != nil {
		return baseWriteResult("", req.ArtifactIDs), &Error{Op: op, Input: req.ArtifactID, Err: err}
	}
	branch := BranchName(req.System, req.Verb, branchID)
	result := prior
	if result.Stage == "" {
		result = baseWriteResult(branch, req.ArtifactIDs)
	} else {
		// PriorResult is invocation-local evidence, not an alternate target.
		// Keep its proven stage while restoring only immutable identity fields
		// omitted by older journals.
		if result.Branch == "" {
			result.Branch = branch
		}
		if len(result.ArtifactIDs) == 0 {
			result.ArtifactIDs = append([]string(nil), req.ArtifactIDs...)
		}
	}

	// Step 0: idempotent-retry short-circuit — before ANY other check or
	// git action (spec 05 §7 idempotency note). Only an OPEN PR
	// short-circuits: it is the interrupted run's own PR, still in flight.
	// A MERGED one is a write that already completed, and the next write on
	// the same branch is a NEW transition, not a repeat — treating it as a
	// repeat is how `ack` then `accept` used to lose the accept.
	existing, err := f.host.FindPRByHeadBranch(ctx, host.FindPRRequest{
		Repo: req.Repo, Branch: branch, Credential: req.Credential,
	})
	if err != nil {
		return result, &Error{Op: op, Input: branch, Err: err}
	}
	if existing != nil && (existing.State != "merged" || req.OperationKey != "") {
		existingIDs := req.ArtifactIDs
		if req.OperationKey != "" {
			key, ids, ok := ParseOperationMetadata(existing.Body)
			if !ok || key != req.OperationKey {
				return result, &Error{Op: op, Input: branch, Err: ErrOperationMismatch}
			}
			existingIDs = ids
		}
		if prepared != nil && writeStageRank(result.Stage) >= writeStageRank(WriteStagePushed) {
			if err := verifyPriorPRIdentity(result, existing, req.BaseBranch); err != nil {
				return result, &Error{Op: op, Input: branch, Err: err}
			}
		}
		existingResult, existingErr := existingPRResult(branch, existing, existingIDs)
		err = existingErr
		if writeStageRank(existingResult.Stage) >= writeStageRank(result.Stage) {
			result = existingResult
		}
		if err != nil {
			return result, &Error{Op: op, Input: branch, Err: err}
		}
		// Re-arm auto-merge before reporting success. OpenPR is NOT atomic
		// on GitHub — creating the PR and arming auto-merge are two calls —
		// so the interrupted run this short-circuit exists for may have
		// created a PR and died before arming it. Reporting "already
		// submitted" over a PR that will never merge on its own is exactly
		// the silent stall this path is meant to recover from.
		//
		// Observed live 2026-07-24 (P36's matrix): a 504 on OpenPR left a PR
		// open with a green required check and `auto_merge: null`, and every
		// retry answered "already-open". Arming is idempotent, so the common
		// case — the PR was fully configured — costs one no-op call.
		if existing.State != "merged" {
			if am, isAutoMerger := f.host.(host.AutoMerger); isAutoMerger {
				status, statusErr := f.host.CheckStatus(ctx, host.StatusRequest{
					Repo: req.Repo, PRNumber: existing.Number, Credential: req.Credential,
				})
				if statusErr != nil {
					result.State = WriteStateNeedsAttention
					result.Note = "PR exists, but its current head could not be checked before re-arming auto-merge"
					return failWriteResult(op, branch, result, statusErr)
				}
				if status.HeadSHA == "" {
					result.State = WriteStateNeedsAttention
					result.Note = "PR exists, but the checked head is unavailable; auto-merge was not armed"
					return failWriteResult(op, branch, result, ErrExpectedHeadUnavailable)
				}
				if writeStageRank(prior.Stage) >= writeStageRank(WriteStagePushed) &&
					(existing.HeadSHA == "" || status.HeadSHA != existing.HeadSHA || status.HeadSHA != prior.CommitSHA) {
					result.State = WriteStateNeedsAttention
					result.Note = "PR head does not match the prepared commit; auto-merge was not armed"
					return failWriteResult(op, branch, result, remoteRecoveryConflict("pr-head-mismatch"))
				}
				method, err := am.EnableAutoMerge(ctx, host.EnableAutoMergeRequest{
					Repo: req.Repo, PRNumber: existing.Number, ExpectedHeadSHA: status.HeadSHA,
					Credential: req.Credential,
				})
				result.MergeMethod = method
				// "Already clean" is GitHub declining because the PR can be
				// merged right now — nothing to wait for. That is not a failed
				// repair; it means the write must LAND the PR itself instead
				// (WAVE M4), guarded by tryLandCleanPR's explicit-green check —
				// never merge, either way, must not fail the retry.
				if err != nil {
					if !host.IsAutoMergeAlreadyClean(err) {
						result.State = WriteStateNeedsAttention
						result.Note = "PR exists, but auto-merge could not be armed; retry after observing its current head"
						return failWriteResult(op, branch, result, err)
					}
					merged, mergeMethod, merr := f.tryLandCleanPRWithStatus(ctx, req, existing.Number, status)
					if mergeMethod != "" {
						result.MergeMethod = mergeMethod
					}
					if merr != nil {
						result.State = WriteStateNeedsAttention
						result.Note = "PR exists and is green, but direct merge did not complete"
						return failWriteResult(op, branch, result, merr)
					}
					if merged {
						result.State = WriteStateAlreadyMerged
						result.Stage = furthestWriteStage(result.Stage, WriteStageMerged)
						result.Note = ""
						return completeWriteResult(op, branch, result)
					}
					result.State = WriteStateNeedsAttention
					result.Note = "PR exists, but unattended merge is not armed"
					return completeWriteResult(op, branch, result)
				}
				result.Stage = furthestWriteStage(result.Stage, WriteStageAutoMergeArmed)
				result.State = WriteStateAlreadyOpen
				return completeWriteResult(op, branch, result)
			}
			result.State = WriteStateNeedsAttention
			result.Note = "PR exists, but this host cannot arm auto-merge"
			return completeWriteResult(op, branch, result)
		}
		return result, nil
	}
	if writeStageRank(result.Stage) >= writeStageRank(WriteStagePRCreated) {
		// A prior result proving that a PR existed is monotonic external
		// evidence. Provider lookup absence cannot authorize a replacement PR;
		// the caller must reconcile that exact provider outcome.
		return result, &Error{Op: op, Input: branch, Err: remoteRecoveryConflict("prior-pr-not-observed")}
	}

	var openRequest host.OpenPRRequest
	if prepared != nil && len(prepared.data.recoveryJSON) > 0 {
		reader, ok := f.host.(remoteRecoveryReader)
		if !ok {
			return failWriteResult(op, branch, result, remoteRecoveryConflict("reader-unavailable"))
		}
		repair, found, recoveryErr := probePreparedRemoteRecovery(ctx, reader, *prepared, remoteRecoveryRuntime{
			RepoDir: req.RepoDir, RemoteURL: req.RemoteURL, Credential: req.Credential,
		})
		if recoveryErr != nil {
			return failWriteResult(op, branch, result, recoveryErr)
		}
		if found {
			result.CommitSHA = repair.HeadSHA
			result.Stage = furthestWriteStage(result.Stage, WriteStagePushed)
			openRequest = repair.OpenPR
		}
	}

	// Steps 2-3: commit + push, under ONE hold of the mirror's advisory
	// lock spanning the whole span — this mirror directory is shared
	// across every project/system on this machine that connects the same
	// space (mirror_root), and this is the only part of Submit that
	// mutates it. See commitAndPush's own doc for the exact hold window.
	if openRequest.Head == "" {
		outcome, err := f.commitAndPush(ctx, req, branch)
		result.CommitSHA = outcome.sha
		result.Stage = furthestWriteStage(result.Stage, outcome.stage)
		if err != nil {
			return failWriteResult(op, branch, result, err)
		}
		if outcome.done != nil {
			return *outcome.done, nil
		}
		openRequest = host.OpenPRRequest{
			Repo: req.Repo, Head: outcome.head, Base: req.BaseBranch,
			Title: req.PRTitle, Body: req.PRBody, ExpectedHeadSHA: outcome.sha, Credential: req.Credential,
		}
	}

	// Step 4: open the PR — UNIFORM, auto-merge always (D-002; spec 05
	// §T1 "Gating needs no OpenPR parameter"). Head is owner-qualified on
	// the fork path; the PR still targets req.Repo's base branch. Runs
	// UNLOCKED: commitAndPush already released the mirror lock once the
	// push completed, so this network round trip does not hold up any
	// other writer on the mirror.
	pr, err := f.host.OpenPR(ctx, openRequest)
	if err != nil {
		if pr.Number > 0 && pr.URL != "" {
			result.PRNumber, result.PRURL = pr.Number, pr.URL
			result.Stage, result.MergeMethod = furthestWriteStage(result.Stage, WriteStagePRCreated), pr.MergeMethod
			if hostOutcomeUnknown(err) {
				result.State = WriteStateOutcomeUnknown
				result.Note = "PR was created, but the auto-merge outcome could not be confirmed"
			} else {
				result.State = WriteStateNeedsAttention
				result.Note = "PR was created, but auto-merge could not be armed"
			}
		} else if hostOutcomeUnknown(err) {
			result.State = WriteStateOutcomeUnknown
			result.Note = "The PR creation outcome could not be observed; retry is required"
		}
		return failWriteResult(op, branch, result, err)
	}
	result.PRNumber, result.PRURL = pr.Number, pr.URL
	result.Stage, result.MergeMethod = furthestWriteStage(result.Stage, WriteStagePRCreated), pr.MergeMethod

	// Step 4b (WAVE M4): OpenPR's own auto-merge arming can be refused
	// because the PR is ALREADY mergeable — nothing to wait for. That is
	// not a failure (armed stays false, a note is carried), but reporting
	// "pending-merge" over it and stopping there is exactly the defect this
	// wave closes: nothing else will ever merge that PR. Try to land it
	// directly, gated by tryLandCleanPR's explicit-green guard.
	state := WriteStateNeedsAttention
	note := pr.AutoMergeNote
	if !pr.AutoMergeArmed && host.AutoMergeNoteIsAlreadyClean(pr.AutoMergeNote) {
		merged, mergeMethod, merr := f.tryLandCleanPR(ctx, req, pr.Number)
		if mergeMethod != "" {
			result.MergeMethod = mergeMethod
		}
		if merr != nil {
			result.State = WriteStateNeedsAttention
			result.Note = "PR exists and is green, but direct merge did not complete"
			return failWriteResult(op, branch, result, merr)
		}
		if merged {
			state, note = WriteStateMerged, ""
		}
	} else if pr.AutoMergeArmed {
		state, note = WriteStatePendingMerge, ""
		result.Stage = furthestWriteStage(result.Stage, WriteStageAutoMergeArmed)
	} else if note == "" {
		note = "PR exists, but unattended merge is not armed"
	}

	// Step 5: return the write-result (cache persistence is P7's, not
	// this phase's — spec 05 §7).
	result.State, result.Note = state, note
	if state == WriteStateMerged {
		result.Stage = furthestWriteStage(result.Stage, WriteStageMerged)
	}
	return completeWriteResult(op, branch, result)
}

func furthestWriteStage(left, right WriteStage) WriteStage {
	if writeStageRank(right) > writeStageRank(left) {
		return right
	}
	return left
}

func writeStageRank(stage WriteStage) int {
	switch stage {
	case WriteStageNone:
		return 1
	case WriteStageCommitted:
		return 2
	case WriteStagePushed:
		return 3
	case WriteStagePRCreated:
		return 4
	case WriteStageAutoMergeArmed:
		return 5
	case WriteStageMerged:
		return 6
	default:
		return 0
	}
}

func hostOutcomeUnknown(err error) bool {
	return errors.Is(err, host.ErrTransient) || errors.Is(err, host.ErrRetriedWriteWasDuplicate)
}

// tryLandCleanPR attempts to merge a PR GitHub has already told us is
// mergeable right now (the "clean status" auto-merge refusal — see
// host.Merger's doc). Both places that arm auto-merge — the fresh-OpenPR
// path above and the idempotent-retry repair path — route through this ONE
// function so they cannot drift (WAVE M4 / AC-1050.12).
//
// merged=true, err=nil: MergePR succeeded — the PR is now merged.
// merged=false, err=nil: the guard was not satisfied, OR the host implements
//
//	no Merger — the caller reports needs-attention and preserves the note.
//
// err != nil: MergePR (or the CheckStatus read) itself failed — the caller
//
//	must surface it, never swallow a real failure as "not merged".
//
// The guard is AC-1050.13's entire safety argument, and is deliberately
// strict: merge ONLY when CheckStatus reports the required context PRESENT
// and EXPLICITLY green — State == "completed" AND Conclusion == "success"
// AND no ambiguity. Every other shape falls through to false:
//   - never reported at all: CheckStatus's own "no check matched" answer is
//     {State: "queued", Conclusion: "", Name: ""} (github.go's CheckStatus,
//     the not-ok branch of selectRequiredCheckRun) — State != "completed",
//     so it is indistinguishable here from "still running", which is
//     exactly the point: this function does not need a separate "absent"
//     case because it never treats anything but a completed success as
//     green in the first place.
//   - pending: State == "in_progress" or "queued" — same, not "completed".
//   - ambiguous: len(Ambiguous) > 0 — P34's own signal that the pick was
//     not unique; a merge decided on an uncertain pick is not "explicitly"
//     anything.
//   - failing: Conclusion != "success" once State == "completed".
//
// a2a must never merge something CI has not passed; this is the one place
// that decision is made, and it is made by requiring a positive answer, not
// by excluding known-bad ones.
func (f *WriteFunnel) tryLandCleanPR(ctx context.Context, req SubmitRequest, prNumber int) (merged bool, method host.MergeMethod, err error) {
	_, ok := f.host.(host.Merger)
	if !ok {
		return false, "", nil
	}
	// Both failures below are returned, not swallowed — an unevaluable or
	// failed landing must never be reported as a completed write, which is
	// this whole wave's point. But the message has to say WHAT ALREADY
	// HAPPENED: the branch is pushed and the PR is open by the time this runs,
	// so a bare wrapped transport error reads to an agent as "the write
	// failed" and invites it to give up rather than re-run. Naming the PR and
	// the safe next move is the difference between an actionable error and a
	// confusing one; re-running is safe because the funnel is idempotent by
	// head branch (verified live during the 2026-07-24 GitHub outage: the
	// re-run produced no duplicate).
	status, err := f.host.CheckStatus(ctx, host.StatusRequest{
		Repo: req.Repo, PRNumber: prNumber, Credential: req.Credential,
	})
	if err != nil {
		return false, "", fmt.Errorf("the write landed and PR #%d is open, but its required check could not be read, "+
			"so a2a did not merge it — re-running is safe and will retry the merge: %w", prNumber, err)
	}
	return f.tryLandCleanPRWithStatus(ctx, req, prNumber, status)
}

func (f *WriteFunnel) tryLandCleanPRWithStatus(ctx context.Context, req SubmitRequest, prNumber int, status host.CheckStatusResult) (merged bool, method host.MergeMethod, err error) {
	merger, ok := f.host.(host.Merger)
	if !ok {
		return false, "", nil
	}
	if !checkStatusExplicitlyGreen(status) {
		return false, "", nil
	}
	method, err = merger.MergePR(ctx, host.MergePRRequest{
		Repo: req.Repo, PRNumber: prNumber, ExpectedHeadSHA: status.HeadSHA, Credential: req.Credential,
	})
	if err != nil {
		return false, method, fmt.Errorf("the write landed and PR #%d is green, but merging it failed, "+
			"so the artifact is not on the base branch yet — re-running is safe and will retry: %w", prNumber, err)
	}
	return true, method, nil
}

// checkStatusExplicitlyGreen is AC-1050.13's guard condition in one place:
// present AND explicitly successful, nothing looser. See tryLandCleanPR's
// doc for why each of absent/pending/ambiguous/failing falls through false.
func checkStatusExplicitlyGreen(s host.CheckStatusResult) bool {
	return s.State == "completed" && s.Conclusion == "success" && len(s.Ambiguous) == 0 && s.HeadSHA != ""
}

// BranchName renders the funnel's deterministic branch:
// a2a/<system>/<verb>/<artifact-id>.
//
// The VERB segment is load-bearing. Keyed on the artifact alone, the branch
// identified WHAT was written about rather than WHICH write it was, so once
// any write by a system on an artifact had merged, every later write by
// that system on that artifact matched a merged PR and short-circuited —
// `ack` then `accept` lost the accept, and a contract's publish/deprecate/
// retire all collapsed into its submit. Silently, with exit 0.
// BranchName IS PROTOCOL, not an implementation detail. Read
// validateBranchSegments and TestBranchNameGrammarIsProtocol before changing
// the format string.
func BranchName(system, verb, artifactID string) string {
	return operation.BranchName(system, verb, artifactID)
}

const (
	operationMetadataPrefix = "<!-- a2a-operation: "
	operationMetadataSuffix = " -->"
)

type operationMetadata struct {
	Key string   `json:"key"`
	IDs []string `json:"ids"`
}

func appendOperationMetadata(body, key string, ids []string) string {
	raw, err := json.Marshal(operationMetadata{Key: key, IDs: ids})
	if err != nil {
		// The shape contains strings only, so encoding cannot fail. Keep the
		// funnel API error-free here while refusing adoption later if that
		// invariant is ever broken.
		return body
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body + operationMetadataPrefix + string(raw) + operationMetadataSuffix
}

// ParseOperationMetadata reads the funnel-owned recovery marker from a pull
// request body. It is exported only inside this repository's internal tree so
// recovery observers (including the live release harness) use the same parser
// as Submit instead of re-deriving the private comment format.
func ParseOperationMetadata(body string) (key string, ids []string, ok bool) {
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, operationMetadataPrefix) || !strings.HasSuffix(line, operationMetadataSuffix) {
			continue
		}
		raw := strings.TrimSuffix(strings.TrimPrefix(line, operationMetadataPrefix), operationMetadataSuffix)
		var metadata operationMetadata
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil || !operation.Valid(metadata.Key) || len(metadata.IDs) == 0 {
			return "", nil, false
		}
		return metadata.Key, append([]string(nil), metadata.IDs...), true
	}
	return "", nil, false
}

// validateBranchSegments refuses a system/verb/artifactID that would make
// BranchName render something other than the four-segment ref it promises.
//
// The failure this prevents is quiet, not loud. A slash in an artifact id
// renders `a2a/<sys>/<verb>/a/b` — still a legal git ref, so the push
// succeeds and a PR opens, but the branch no longer round-trips through the
// funnel's own step-0 lookup the way its sibling writes do, and the space's
// tooling parses one segment where two arrived. The same goes for the ref
// characters git itself rejects (`~ ^ : ? * [ \` and whitespace), a segment
// ending in `.lock`, and `..`: those fail at the push, after the commit, in
// git's own words rather than the product's.
//
// This validates the SEGMENTS rather than the rendered branch on purpose:
// the rendered string cannot tell an embedded slash from a real separator,
// which is exactly the ambiguity being refused.
func validateBranchSegments(system, verb, artifactID string) error {
	for _, seg := range []struct{ name, value string }{
		{"system", system},
		{"verb", verb},
		{"artifact id", artifactID},
	} {
		if seg.value == "" {
			return fmt.Errorf("%w: %s is empty", ErrInvalidBranchSegment, seg.name)
		}
		if strings.ContainsAny(seg.value, "/") {
			return fmt.Errorf("%w: %s %q contains a slash, which would silently nest the write branch "+
				"and break the funnel's idempotency lookup", ErrInvalidBranchSegment, seg.name, seg.value)
		}
		if strings.ContainsAny(seg.value, " \t\n~^:?*[\\") {
			return fmt.Errorf("%w: %s %q contains a character git refuses in a ref name",
				ErrInvalidBranchSegment, seg.name, seg.value)
		}
		if strings.Contains(seg.value, "..") || strings.HasSuffix(seg.value, ".lock") ||
			strings.HasPrefix(seg.value, ".") || strings.HasSuffix(seg.value, ".") {
			return fmt.Errorf("%w: %s %q is not a legal git ref component", ErrInvalidBranchSegment, seg.name, seg.value)
		}
	}
	return nil
}

// resolvedBaseBranch returns req.BaseBranch, defaulting to §4.2's
// normative "main" when unset. The one place this default lives, shared by
// commitOne's checkout start-point and commitIsOnBase's ancestor check so
// the two can never name a different base out from under each other.
func resolvedBaseBranch(req SubmitRequest) string {
	if req.BaseBranch == "" {
		return "main"
	}
	return req.BaseBranch
}

// restoreTreeToBase moves the mirror's working tree off the ephemeral
// write branch and back onto the base branch as the mirror last fetched
// it, so the funnel leaves the mirror the way it found it: a view of the
// SPACE, not of one in-flight write.
//
// It deliberately resets to `origin/<base>` rather than to the local base
// ref. The local ref can lag or have been moved by a previous write; the
// remote-tracking ref is what "the space, as of the last fetch" means, and
// it is the same anchor commitOne's own start point uses — so a write and
// the cleanup after it can never disagree about which commit base is.
//
// What this does NOT do, stated so nobody reads a stronger guarantee into
// it: it does not fetch. The tree is restored to the last-fetched base,
// not to whatever the remote holds right now. Freshness is the reader's
// concern (internal/cache's SyncIfStale, and CloneOrFetch behind it); this
// function's only job is that a write does not leave the mirror pinned to
// its own unmerged branch.
func (f *WriteFunnel) restoreTreeToBase(ctx context.Context, lock *MirrorLock, req SubmitRequest) error {
	if err := mutateTree(lock, req.RepoDir); err != nil {
		return err
	}
	base := resolvedBaseBranch(req)
	if err := runGit(ctx, req.RepoDir, "rev-parse", "--verify", "origin/"+base); err != nil {
		// No remote-tracking base to restore to (a fixture with no origin,
		// a base that has never been fetched). Leaving the tree where it is
		// beats resetting it to something invented.
		return nil //nolint:nilerr // reason: an unresolvable base means there is nothing to restore TO, which is not a failure of the write
	}
	deadline := time.Now().Add(indexLockWaitBudget)
	if err := runGitRetryLocked(ctx, req.RepoDir, deadline, "checkout", "-B", base, "origin/"+base); err != nil {
		return err
	}
	return runGitRetryLocked(ctx, req.RepoDir, deadline, "reset", "--hard", "origin/"+base)
}

// commitIsOnBase reports whether sha is already contained in the base
// branch as the mirror last fetched it — i.e. this write's content is
// already in the space. An unresolvable base ref answers "no": writing a
// duplicate is recoverable, silently dropping a write is not.
func (f *WriteFunnel) commitIsOnBase(ctx context.Context, req SubmitRequest, sha string) (bool, error) {
	base := resolvedBaseBranch(req)
	if err := runGit(ctx, req.RepoDir, "rev-parse", "--verify", "origin/"+base); err != nil {
		return false, nil //nolint:nilerr // reason: an unknown base is "not on base", not a failure
	}
	err := runGit(ctx, req.RepoDir, "merge-base", "--is-ancestor", sha, "origin/"+base)
	return err == nil, nil
}

// commitAndPushOutcome is what commitAndPush found: either steps 2-3
// completed with a commit SHA and push head ready for Submit's step 4
// (OpenPR), or the write is already fully accounted for — this write's
// content already merged, or a fork PR already exists for this head — and
// Submit should return the carried result immediately without opening
// anything.
type commitAndPushOutcome struct {
	sha, head string
	stage     WriteStage
	// done, when non-nil, is Submit's final result: nothing left to do.
	done *WriteResult
}

// commitAndPush is Submit's steps 2-3 (commit, then push — into req.Repo
// itself, or the P28 fork fallback), run under ONE hold of the mirror's
// advisory lock (AcquireMirrorLock) spanning the whole mutate-commit-push
// sequence — the exact live-e2e regression this exists to close: two
// systems writing the SAME shared mirror (mirror_root puts every
// project's clone of a space in one place) interleaved their own
// checkout/write/add/commit on ONE git index, and a branch ended up
// carrying the other author's files.
//
// The lock is acquired before the first git action that touches
// req.RepoDir and released as soon as the push (ordinary or via fork)
// completes — success OR failure, so a refused write never leaks the lock
// and poisons the next writer's attempt (asserted directly by this
// package's tests). Everything after this function returns — OpenPR,
// tryLandCleanPR's CheckStatus/MergePR — is a HOST network call, not a
// mutation of req.RepoDir, and deliberately runs UNLOCKED: holding the
// mirror lock across those round trips would stall every other writer on
// the mirror for the duration of a call that never touches req.RepoDir
// again.
func (f *WriteFunnel) commitAndPush(ctx context.Context, req SubmitRequest, branch string) (commitAndPushOutcome, error) {
	const op = "Submit"

	lock, err := AcquireMirrorLock(ctx, req.RepoDir)
	if err != nil {
		return commitAndPushOutcome{}, &Error{Op: op, Input: branch, Err: err}
	}

	// Leaving the tree on the ephemeral branch and releasing is what made a
	// long-lived process read a mirror that is not the space. commitOne
	// checks out a2a/<system>/<verb>/<id> and never leaves; the only thing
	// that ever moved it back was checkoutRemoteHead, reachable solely
	// through CloneOrFetch. The CLI got away with it because it re-syncs on
	// every invocation. `a2a mcp` does not: it clones once at server
	// construction and then serves N writes, so write #2's legality read
	// walked a tree standing on write #1's UNMERGED branch — validating a
	// transition against a state the space had never agreed to.
	//
	// So the restore belongs here, at the writer, not at every reader: the
	// invariant is that the funnel leaves the mirror as it found it.
	//
	// Ordering is the whole trick and it is why this is one closure rather
	// than two defers. The restore is itself a mutation, so it must happen
	// while the lock is still HELD; the success path below used to release
	// early on purpose (nothing after it touches RepoDir, and every other
	// writer should be unblocked the moment the push lands). Both paths now
	// go through this, and it runs once.
	restored := false
	restoreAndRelease := func() {
		if !restored {
			restored = true
			// Best-effort by design: the write has already succeeded or
			// already failed, and neither verdict should change because
			// tidying the tree afterwards did not work. A failed restore
			// degrades to exactly the old behaviour — a mirror parked on a
			// branch until the next CloneOrFetch — rather than losing a
			// write or leaking the lock.
			_ = f.restoreTreeToBase(ctx, lock, req)
		}
		_ = lock.Release() // reason: best-effort — a failed release self-heals via mirrorLockStaleAfter and must not turn a completed/refused write into a different error
	}
	defer restoreAndRelease()

	// A semantic-operation branch may already have been pushed by an earlier
	// invocation that failed before OpenPR. Resume it only when the remote SHA
	// is still exactly the locally preserved tool-owned commit and that commit
	// carries this operation key. No force push and no branch-name-only trust.
	if req.OperationKey != "" {
		reader, ok := f.host.(host.RemoteBranchReader)
		if !ok {
			return commitAndPushOutcome{}, &Error{Op: op, Input: branch, Err: ErrOperationMismatch}
		}
		remote, readErr := reader.ReadRemoteBranch(ctx, host.RemoteBranchRequest{
			RepoDir: req.RepoDir, RemoteURL: req.RemoteURL, Branch: branch, Credential: req.Credential,
		})
		if readErr != nil {
			return commitAndPushOutcome{}, &Error{Op: op, Input: branch, Err: readErr}
		}
		if remote.Exists {
			matches, matchErr := operationCommitMatches(ctx, req.RepoDir, branch, remote.SHA, req.OperationKey)
			if matchErr != nil {
				return commitAndPushOutcome{}, &Error{Op: op, Input: branch, Err: matchErr}
			}
			if !matches {
				return commitAndPushOutcome{}, &Error{Op: op, Input: branch, Err: ErrOperationMismatch}
			}
			restoreAndRelease()
			return commitAndPushOutcome{sha: remote.SHA, head: branch, stage: WriteStagePushed}, nil
		}
	}

	sha, fresh, err := f.commitOne(ctx, lock, req, branch)
	if err != nil {
		return commitAndPushOutcome{}, &Error{Op: op, Err: err}
	}
	if !fresh {
		// Nothing to commit: the branch already carries exactly this
		// content. Either it is already IN the space (a genuine repeat of
		// the same write — nothing left to do), or a previous attempt made
		// the commit but never got it merged, in which case the push/PR
		// steps still have work.
		onBase, berr := f.commitIsOnBase(ctx, req, sha)
		if berr != nil {
			return commitAndPushOutcome{}, &Error{Op: op, Err: berr}
		}
		if onBase {
			done, derr := finalizeWriteResult(WriteResult{
				Branch: branch, CommitSHA: sha, State: WriteStateAlreadyMerged,
				Stage:       WriteStageMerged,
				ArtifactIDs: append([]string(nil), req.ArtifactIDs...),
			})
			if derr != nil {
				return commitAndPushOutcome{}, &Error{Op: op, Input: branch, Err: derr}
			}
			return commitAndPushOutcome{done: &done}, nil
		}
	}
	committed := commitAndPushOutcome{sha: sha, head: branch, stage: WriteStageCommitted}

	// Push the ephemeral branch — into req.Repo itself, or (when the
	// caller allowed it and the push was refused for ACCESS) into the
	// submitter's own fork.
	head := branch
	if err := f.push(ctx, req, branch, req.RemoteURL); err != nil {
		if !req.AllowForkFallback || !errors.Is(err, host.ErrPushForbidden) {
			return committed, &Error{Op: op, Input: branch, Err: err}
		}
		forkHead, forked, ferr := f.pushViaFork(ctx, req, branch)
		if ferr != nil {
			return committed, ferr
		}
		if forked != nil {
			// The fork already carries this branch's PR — the fork-head
			// idempotency check step 0 could not make, because the fork's
			// owner was unknown until now.
			done, derr := existingPRResult(branch, forked, req.ArtifactIDs)
			if derr != nil {
				return committed, &Error{Op: op, Input: branch, Err: derr}
			}
			return commitAndPushOutcome{done: &done}, nil
		}
		head = forkHead
	}

	// Restore and release NOW, explicitly, rather than waiting for the
	// deferred safety net: nothing left in this function touches
	// req.RepoDir, and every other writer waiting on this mirror should be
	// unblocked the moment this one's push lands, not after this function
	// merely returns.
	restoreAndRelease()

	return commitAndPushOutcome{sha: sha, head: head, stage: WriteStagePushed}, nil
}

// existingPRResult renders the idempotent short-circuit's result for a PR
// the host already has open or merged for this branch.
func existingPRResult(branch string, pr *host.PRInfo, artifactIDs []string) (WriteResult, error) {
	state := WriteStateAlreadyOpen
	stage := WriteStagePRCreated
	if pr.State == "merged" {
		state = WriteStateAlreadyMerged
		stage = WriteStageMerged
	}
	return finalizeWriteResult(WriteResult{
		Branch: branch, PRNumber: pr.Number, PRURL: pr.URL, State: state,
		Stage: stage, CommitSHA: pr.HeadSHA, MergeMethod: pr.MergeMethod,
		ArtifactIDs: append([]string(nil), artifactIDs...),
	})
}

func verifyPriorPRIdentity(prior WriteResult, observed *host.PRInfo, expectedBase string) error {
	if observed == nil || prior.CommitSHA == "" || observed.BaseBranch != expectedBase || observed.HeadSHA != prior.CommitSHA {
		return remoteRecoveryConflict("prior-pr-identity-mismatch")
	}
	if writeStageRank(prior.Stage) >= writeStageRank(WriteStagePRCreated) &&
		(prior.PRNumber <= 0 || observed.Number != prior.PRNumber) {
		return remoteRecoveryConflict("prior-pr-identity-mismatch")
	}
	return nil
}

func operationCommitMatches(ctx context.Context, repoDir, branch, remoteSHA, key string) (bool, error) {
	localSHA, err := runGitOutput(ctx, repoDir, nil, "rev-parse", "--verify", branch)
	if err != nil {
		return false, nil //nolint:nilerr // absent local provenance means "not proven", not an engine failure
	}
	if localSHA != remoteSHA {
		return false, nil
	}
	message, err := runGitOutput(ctx, repoDir, nil, "show", "-s", "--format=%B", remoteSHA)
	if err != nil {
		return false, err
	}
	return strings.Contains("\n"+message+"\n", "\nA2A-Operation-Key: "+key+"\n"), nil
}

// push sends the committed branch to remoteURL.
func (f *WriteFunnel) push(ctx context.Context, req SubmitRequest, branch, remoteURL string) error {
	pushReq := host.PushBranchRequest{
		RepoDir: req.RepoDir, LocalRef: branch, Branch: branch,
		RemoteURL: remoteURL, Credential: req.Credential,
	}
	if req.ReplaceOrphanBranch {
		if reader, ok := f.host.(host.RemoteBranchReader); ok {
			remote, err := reader.ReadRemoteBranch(ctx, host.RemoteBranchRequest{
				RepoDir: req.RepoDir, RemoteURL: remoteURL, Branch: branch,
				Credential: req.Credential,
			})
			if err != nil {
				return err
			}
			if remote.Exists {
				pushReq.ForceWithLeaseSHA = remote.SHA
			}
		}
	}
	_, err := f.host.PushBranch(ctx, pushReq)
	return err
}

// pushViaFork is the P28 fallback, reached only when a push into req.Repo
// was refused for ACCESS and req.AllowForkFallback is set. It ensures the
// submitter's fork, re-checks idempotency against the now-known fork head
// (step 0 could not: it did not know the fork's owner), and pushes there.
//
// Returns either the owner-qualified head to open the PR from, or the PR
// that already exists for that head — never both.
func (f *WriteFunnel) pushViaFork(ctx context.Context, req SubmitRequest, branch string) (string, *host.PRInfo, error) {
	const op = "Submit"

	forker, ok := f.host.(host.Forker)
	if !ok {
		return "", nil, &Error{Op: op, Input: manualForkAdvice, Err: ErrForkFallbackUnavailable}
	}
	fork, err := forker.EnsureFork(ctx, host.EnsureForkRequest{Repo: req.Repo, Credential: req.Credential})
	if err != nil {
		return "", nil, &Error{Op: op, Input: manualForkAdvice, Err: fmt.Errorf("%w: %w", ErrForkFallbackUnavailable, err)}
	}

	existing, err := f.host.FindPRByHeadBranch(ctx, host.FindPRRequest{
		Repo: req.Repo, Branch: branch, HeadOwner: fork.Repo.Owner, Credential: req.Credential,
	})
	if err != nil {
		return "", nil, &Error{Op: op, Input: branch, Err: err}
	}
	if existing != nil {
		return "", existing, nil
	}

	if err := f.push(ctx, req, branch, fork.RemoteURL); err != nil {
		return "", nil, &Error{Op: op, Input: branch, Err: err}
	}
	return fork.Repo.Owner + ":" + branch, nil, nil
}

// manualForkAdvice is the one path that always works when the automatic
// fork flow cannot run: the operator forks and opens the PR by hand (the
// intake accepts fork PRs by design — feedback-intake.yml is
// pull_request_target).
const manualForkAdvice = "no write access and no automatic fork — fork the repo and open the pull request by hand"

// sectionOK reports whether path is inside system's own section, or under
// the space-level decisions/ exception (the one path the single-writer
// rule does not enforce per-system, §4.2 decision flow).
//
// The path must be a clean, relative, forward-slash space path: any
// absolute path, any `..` segment, or any non-canonical form (e.g.
// `axon/../other/evil.md`) is rejected outright — otherwise a crafted
// FileWrite.Path could collapse into a sibling system's section, or
// outside the repo entirely, while still passing the guard whose whole
// job is to enforce the single-writer boundary (D-002's single write funnel
// + section ownership — plan §4.2; NOT D-014, which is the unrelated
// data-never-instructions stance).
func sectionOK(system, path string) bool {
	if !isCleanRelativePath(path) {
		return false
	}
	if path == "decisions" || hasPathPrefix(path, "decisions/") {
		return true
	}
	return path == system || hasPathPrefix(path, system+"/")
}

// spaceInfraOK reports whether path is one of the fixed space-infrastructure
// paths — "space.yaml", "CODEOWNERS", "BRANCH-PROTECTION.md", or anything
// under ".github/" — mirroring what internal/cli/cmd_validate_ci.go already
// excludes from author-diff-authz (both guards must agree on this set,
// §"AllowSpaceInfrastructure" on SubmitRequest).
//
// Only reachable when the caller opted in via
// SubmitRequest.AllowSpaceInfrastructure; sectionOK's own admission (e.g.
// system "axon" writing "axon/CODEOWNERS", a file merely NAMED CODEOWNERS
// inside axon's own section) is unaffected and unrelated — this function
// checks the repo-root infrastructure files/dir ONLY, anchored (not
// substring-matched): "CODEOWNERS" matches, "axon/CODEOWNERS" does not;
// ".github/x" matches, ".githubfoo/x" does not.
//
// Shares isCleanRelativePath with sectionOK so a path admitted via this
// route is held to the exact same cleanliness bar as the section route —
// otherwise a crafted ".github/../axon/evil.md" could pass in claiming to
// be infrastructure while actually escaping into another system's section.
func spaceInfraOK(path string) bool { return IsInfrastructurePath(path) }

// IsInfrastructurePath is the EXPORTED form of the same predicate, and the
// single source of truth for "what counts as space infrastructure".
//
// It is exported because a caller that PLANS an infrastructure write (spec
// 35's `a2a space update`) has to know the exact set the funnel will admit.
// A second, hand-maintained copy on the planning side would drift, and the
// failure mode is nasty: the planner proposes a file, the funnel refuses it
// with ErrWrongSection, and the whole write fails on a path nobody asked
// for. One predicate, both sides.
func IsInfrastructurePath(path string) bool {
	if !isCleanRelativePath(path) {
		return false
	}
	switch path {
	case "space.yaml", "CODEOWNERS", "BRANCH-PROTECTION.md":
		return true
	}
	return hasPathPrefix(path, ".github/")
}

// isCleanRelativePath reports whether path is already in cleaned,
// non-escaping, relative form: not empty, not absolute, and identical to
// its own path.Clean result (which collapses `..`/`.`/double-slashes) with
// that cleaned form not itself escaping the root. Shared by every route
// through the section guard (sectionOK and spaceInfraOK) so no route can
// admit a path the other would have rejected as unclean.
func isCleanRelativePath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") {
		return false
	}
	cleaned := gopath.Clean(path)
	return cleaned == path && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func hasPathPrefix(path, prefix string) bool {
	return len(path) > len(prefix) && path[:len(prefix)] == prefix
}

// commitOne builds the desired commit from origin/<base> through a private
// Git index. It never checks out the ephemeral branch and never reads or
// mutates a worktree path: symlinks and concurrent directory renames in the
// shared mirror therefore cannot redirect protocol bytes. The explicit base
// tree also prevents one writer from inheriting another writer's branch.
func (f *WriteFunnel) commitOne(ctx context.Context, lock *MirrorLock, req SubmitRequest, branch string) (sha string, fresh bool, err error) {
	mutations, err := normalizeMutations(req.Files, req.Mutations)
	if err != nil {
		return "", false, err
	}
	if err := f.validateMutationSet(req.OperationKey, mutations); err != nil {
		return "", false, err
	}

	authorName := req.CommitAuthorName
	if authorName == "" {
		authorName = "a2a"
	}
	authorEmail := req.CommitAuthorEmail
	if authorEmail == "" {
		authorEmail = "a2a@a2ahub.invalid"
	}
	msg := req.CommitMessage
	if msg == "" {
		msg = "a2a: submit " + req.ArtifactID
	}
	if req.OperationKey != "" && !hasExactCommitTrailer(msg, "A2A-Operation-Key", req.OperationKey) {
		msg += "\n\nA2A-Operation-Key: " + req.OperationKey
	}
	baseRef := "origin/" + resolvedBaseBranch(req)
	if req.ExpectedBaseSHA != "" {
		if !validRemoteRecoveryOID(req.ExpectedBaseSHA) {
			return "", false, ErrPreparedInvalid
		}
		baseRef = req.ExpectedBaseSHA
	}
	return commitTreeFromBase(ctx, lock, treeCommitRequest{
		RepoDir: req.RepoDir, BaseRef: baseRef,
		UpdateRef: "refs/heads/" + branch, Mutations: mutations,
		Message: msg, AuthorName: authorName, AuthorMail: authorEmail,
	})
}

func hasExactCommitTrailer(message, name, value string) bool {
	want := name + ": " + value
	for _, line := range strings.Split(message, "\n") {
		if line == want {
			return true
		}
	}
	return false
}
