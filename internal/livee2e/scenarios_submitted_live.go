//go:build livee2e

package livee2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// runSubmittedFamilyScenarios drives spec 38's AC-980.1 Layer-1 rows for the
// four kinds fold.go's postSubmissionState groups as the "submitted" family:
// question, work_request, handoff, response. It is this family's ONE
// exported-shape entry, matching runHappyScenarios/runContractIntegrity
// Scenarios' own convention (plan §"Wave-3 design notes": "the fan-out seam
// is four functions, not a registry" — this phase adds two more).
//
// Grouped in ONE file rather than four (the allowlist's own wording: "one
// [file] per lifecycle family") because question and work_request share
// IDENTICAL fold rows (internal/fold/table.go's exchangeRows is generated
// once for both kinds) and handoff/response are one hop away in the same
// closure model (§3.4.6) — keeping all four in one place is what "a kind's
// whole story stays readable" (spec 38 §T3) means for a family this small.
//
// Every helper below is prefixed subfam* — this phase's own scenario-family
// namespace, mirroring scenarios_happy_live.go's happy*/scenarios_contract_
// integrity_live.go's ac973* convention, so identifiers cannot collide
// across the package's scenario families.
//
// Layer 1 only (spec 38 §T2): author -> submit -> gated -> merged -> the
// counterpart actually sees it -> every legal transition through to a
// TERMINAL state. Layer 2 (an illegal transition refused live) and Layer 3
// (failure/recovery) are later waves' own files against this same
// allowlist; this file does not attempt either.
//
// KNOWN RISK, reported rather than routed around (this phase's own rule:
// "a row that cannot be driven because the product will not do the thing is
// a FINDING, never a harness workaround"): every row that calls `a2a
// respond` — question, work_request, and response, all three, since
// respond IS the shared "answer" step in each of their own lifecycles —
// authors a NEW response artifact whose `to:` field cmd_lifecycle.go's
// RespondCommand.Run never fills (respFields only ever sets parent/result/
// from; internal/template.Render's applyFills auto-fills a top-level field
// ONLY when it is a bare SCALAR pipe-alternated placeholder or has an
// explicit Fields override — `to: [<requester-system>]` is a YAML
// SEQUENCE, so neither path touches it). RespondCommand.Run also never
// calls internal/validate (unlike cmd_submit.go's SubmitCommand, which runs
// full V2 before its first commit) — only the fold-based legality check —
// so nothing local catches a literal `to: [<requester-system>]` before it
// is committed and pushed. internal/validate's own checkAddressees
// (authz.go, REF-006/CC-008) rejects a `to` entry that is not a real,
// registered space participant, at SeverityReject — if that check runs at
// CI (`a2a validate --ci`, the required check these rows all await), the
// respond PR's own check will conclude "failure", not "success". This is
// why subfamAwaitGreenAndLand exists below rather than reusing
// happyLandAndSync at the respond step specifically: a required check that
// concludes failure must render as a diagnosed VerdictFail naming the CI
// failure, never as a misleading VerdictTimedOut from a merge branch
// protection silently blocks. If this reads live as expected, it is a
// genuine product gap in `a2a respond` — not something this wave's harness
// can fix (respond collapses draft+submit into ONE call, D-026, so unlike
// ac973DraftContractExcludingPeer's staged-file edit there is no staging
// step here to patch), and not something this wave invents a fake
// "requester-system" participant to paper over.
func runSubmittedFamilyScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{
		subfamExchangeLifecycle(ctx, h, "question", subfamQuestionScenario),
		subfamExchangeLifecycle(ctx, h, "work_request", subfamWorkRequestScenario),
		subfamHandoffLifecycle(ctx, h),
		subfamResponseLifecycle(ctx, h),
	}

	// Same parking discipline runHappyScenarios/runContractIntegrityScenarios
	// already document: leave both checkouts on a clean main so whichever
	// family runs next does not inherit a dirty working tree that looks like
	// its own bug. Best-effort; a failure here does not change any
	// already-recorded result above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// This family's four catalogue names (catalogue.go carries the literal
// duplicates — catalogue.go is untagged and cannot reference these
// build-tag-gated constants, the same split ac973Scenario/"contract-
// integrity-registered-consumer" already lives with).
const (
	subfamQuestionScenario    = "question-lifecycle-to-closed"
	subfamWorkRequestScenario = "work-request-lifecycle-to-closed"
	subfamHandoffScenario     = "handoff-lifecycle-to-accepted"
	subfamResponseScenario    = "response-lifecycle-to-verified"
)

// subfamResultFromErr renders err into this family's Result, step-tagging
// Expected exactly as ac973ResultFromErr does — these rows are multi-step
// chains (submit -> ack -> respond/verify-pass -> close/read-back), and a
// bare "the row failed" would not say which leg broke. Filed under SystemA
// unconditionally, matching ac973's own precedent and this phase's brief
// ("one row per KIND, not per (kind x system)"): the row inherently uses
// BOTH systems, and which checkout authored the failing step is visible in
// Observed's own error text, not in this field.
func subfamResultFromErr(scenario, step string, err error, expected string) Result {
	verdict, _ := happyVerdictForErr(err)
	res := Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI,
		Verdict: verdict, Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: err.Error(),
	}
	if errors.Is(err, ErrNoPRForBranch) || errors.Is(err, ErrNoBranchMatch) || errors.Is(err, ErrAmbiguousBranchMatch) {
		res.Detail = err.Error()
	}
	return res
}

// subfamFail builds a VerdictFail row for an assertion this family observed
// directly (the CLI ran, but what it did was wrong) — same step-tagging as
// subfamResultFromErr.
func subfamFail(scenario, step, observed, expected, detail string) Result {
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// subfamShowDoc is this family's own minimal decode of `a2a show --json`'s
// output — only the one field every row here reads back to confirm a driven
// transition actually landed. Deliberately not internal/cache.ShowResult:
// this package never imports the product's cache layer, it only shells out
// to the CLI surface a real agent uses (the same reasoning
// lifecycleEventDoc/showOutput's siblings across this repo's own files
// apply to their own minimal projections).
type subfamShowDoc struct {
	State string `json:"state"`
}

// subfamShowState runs `a2a show <id> --json` from c and returns the folded
// state cache.Store.Show computed. For a response id specifically this is
// cache/mirror.go's own "Response closure-state overlay" pass (its own doc
// comment names it exactly), never a response's OWN independent Fold call —
// see subfamResponseLifecycle's doc comment for why that distinction is
// this row's whole point (spec 38 §6-Q3, plan D-H).
func subfamShowState(ctx context.Context, c *checkout, id string) (string, error) {
	stdout, stderr, err := c.Run(ctx, "show", id, "--json")
	if err != nil {
		return "", fmt.Errorf("livee2e: a2a show %s (%s): %w: %s", id, c.System, err, strings.TrimSpace(stderr))
	}
	var doc subfamShowDoc
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		return "", fmt.Errorf("livee2e: decode `a2a show %s --json` output (%s): %w", id, c.System, jerr)
	}
	return doc.State, nil
}

// subfamInboxContains runs `a2a inbox --json` from c and reports whether id
// appears — this is AC-980.1's own "the counterpart actually sees it" leg,
// the same evidence shape happyCrossSystemVisibility already exercises for
// announcement, reused here for the four kinds nothing else drives.
func subfamInboxContains(ctx context.Context, c *checkout, id string) (found bool, stdout string, err error) {
	stdout, stderr, runErr := c.Run(ctx, "inbox", "--json")
	if runErr != nil {
		return false, "", fmt.Errorf("livee2e: a2a inbox (%s): %w: %s", c.System, runErr, strings.TrimSpace(stderr))
	}
	return strings.Contains(stdout, id), stdout, nil
}

// subfamAwaitGreenAndLand is happyLandAndSync's (scenarios_happy_live.go)
// stricter sibling, used ONLY at this family's `respond` steps — see this
// file's own top-of-file "KNOWN RISK" comment for why.
// happyLandAndSync accepts AwaitCheck's (nil error, non-success conclusion)
// return silently and proceeds straight to awaiting the merge, which — on a
// required check that concluded "failure" — can only time out (branch
// protection blocks the merge on red), reporting VerdictTimedOut for what
// is actually a diagnosed content defect. This helper asserts the
// conclusion itself before ever waiting on the merge, so a CI rejection of
// `a2a respond`'s own `to:` gap (if it is real) surfaces as a step-tagged
// VerdictFail naming the check's own state/conclusion, not a timeout.
func subfamAwaitGreenAndLand(ctx context.Context, h *harness, c *checkout, prNumber int) error {
	res, err := h.AwaitCheck(ctx, c, prNumber)
	if err != nil {
		return fmt.Errorf("waiting for PR #%d's required check: %w", prNumber, err)
	}
	if res.Conclusion != "success" {
		return fmt.Errorf("PR #%d's required check concluded %q (state=%q), not success — see this file's own KNOWN RISK comment on `a2a respond`'s `to:` field",
			prNumber, res.Conclusion, res.State)
	}
	if _, err := happyAwaitMerged(ctx, h, prNumber); err != nil {
		return fmt.Errorf("waiting for PR #%d to merge: %w", prNumber, err)
	}
	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return fmt.Errorf("sync after PR #%d merged: %w: %s", prNumber, err, stderr)
	}
	return nil
}

// subfamExchangeLifecycle drives question's and work_request's IDENTICAL
// Layer-1 story (internal/fold/table.go's exchangeRows is generated once
// for both kinds, so their legal transitions are the same table): A submits
// -> B sees it (sync+inbox) -> B acknowledges -> B answers (`a2a respond`,
// D-026: draft+submit collapsed into the SAME PR as the parent's own
// `respond` event) -> A closes (RoleOwner, legal only from `responded`) ->
// terminal state `closed` (exchangeRows carries no transition OUT of
// closed). This is spec 38 §T2's own illustrative "question -> answer ->
// close", driven for real against both kinds that story applies to.
func subfamExchangeLifecycle(ctx context.Context, h *harness, kind, scenario string) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}

	sub, err := h.DraftAndSubmit(ctx, a, kind)
	if err != nil {
		return subfamResultFromErr(scenario, "draft-submit", err, fmt.Sprintf("draft+submit a %s opens its own PR", kind))
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return subfamResultFromErr(scenario, "publish-land-sync", err, "the submitted "+kind+" lands on main and reaches A's mirror")
	}

	// --- the counterpart actually sees it (AC-980.1) ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed "+kind)
	}
	seen, inboxOut, err := subfamInboxContains(ctx, b, sub.ID)
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-sees-it", err, "B's a2a inbox succeeds after sync")
	}
	if !seen {
		return subfamFail(scenario, "counterpart-sees-it", inboxOut,
			fmt.Sprintf("B's `a2a inbox --json` lists %s after sync", sub.ID), inboxOut)
	}

	// --- B acknowledges: submitted -> acknowledged ---
	if _, stderr, err := b.Run(ctx, "ack", sub.ID); err != nil {
		return subfamResultFromErr(scenario, "counterpart-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack succeeds and opens its own PR")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", sub.ID))
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return subfamResultFromErr(scenario, "ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- B answers: acknowledged -> responded. D6 keys this two-artifact
	// write by canonical intent, so its branch is opaque op-v1 data. ---
	respondKey, respondBranch := respondOperation(b.System, sub.ID, "answered")
	if _, stderr, err := b.Run(ctx, respondCommandArgs(sub.ID, "answered")...); err != nil {
		return subfamResultFromErr(scenario, "counterpart-respond", fmt.Errorf("%w: %s", err, stderr), "B's a2a respond succeeds and opens its own PR")
	}
	respondPR, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-respond", err, "respond's semantic-operation branch has a PR")
	}
	if _, err := operationArtifactID(respondPR.Body, respondKey, sub.ID, "XS-"); err != nil {
		return subfamResultFromErr(scenario, "counterpart-respond", err, "respond's PR metadata names the new XS response artifact")
	}
	if err := subfamAwaitGreenAndLand(ctx, h, b, respondPR.Number); err != nil {
		return subfamResultFromErr(scenario, "respond-land-sync", err, "the response lands on main and reaches B's mirror (required check concludes success)")
	}

	// --- A closes: responded -> closed (terminal, RoleOwner: env.From). ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "owner-sync-before-close", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's response before closing")
	}
	if _, stderr, err := a.Run(ctx, "close", sub.ID); err != nil {
		return subfamResultFromErr(scenario, "owner-close", fmt.Errorf("%w: %s", err, stderr), "A's a2a close succeeds and opens its own PR")
	}
	closePR, err := h.pullForBranch(ctx, space.BranchName(a.System, "close", sub.ID))
	if err != nil {
		return subfamResultFromErr(scenario, "owner-close", err, "close's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, closePR.Number); err != nil {
		return subfamResultFromErr(scenario, "close-land-sync", err, "the close lands on main and reaches A's mirror")
	}

	state, err := subfamShowState(ctx, a, sub.ID)
	if err != nil {
		return subfamResultFromErr(scenario, "terminal-state-read", err, "a2a show reads the closed "+kind+" back")
	}
	if state != "closed" {
		return subfamFail(scenario, "terminal-state-read", state, "the "+kind+" reaches the terminal `closed` state", state)
	}

	narrative := fmt.Sprintf("%s: submit PR #%d, ack PR #%d, respond PR #%d, close PR #%d — folded state %q",
		sub.ID, sub.PRNumber, ackPR.Number, respondPR.Number, closePR.Number, state)
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       narrative,
		PassEvidence: narrative,
	}
}

// subfamHandoffLifecycle drives handoff's Layer-1 story: A submits -> B
// sees it -> B acknowledges -> B verify-passes -> terminal state `accepted`
// (handoffRows carries no transition OUT of accepted — this is spec 38
// §T2's own illustrative "handoff -> accept", literally: verify-pass's own
// `to` state IS StateAccepted). Unlike the exchange kinds, `verify-pass`
// resolves to a SINGLE-id branch (it is a generic OP-211 table-driven verb,
// not `respond`'s own draft+submit collapse), so pullForBranch's plain
// exact-HeadRef lookup is enough — no composite-branch handling needed here.
func subfamHandoffLifecycle(ctx context.Context, h *harness) Result {
	const scenario = subfamHandoffScenario
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}

	sub, err := h.DraftAndSubmit(ctx, a, "handoff")
	if err != nil {
		return subfamResultFromErr(scenario, "draft-submit", err, "draft+submit a handoff opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return subfamResultFromErr(scenario, "publish-land-sync", err, "the submitted handoff lands on main and reaches A's mirror")
	}

	// --- the counterpart actually sees it (AC-980.1) ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed handoff")
	}
	seen, inboxOut, err := subfamInboxContains(ctx, b, sub.ID)
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-sees-it", err, "B's a2a inbox succeeds after sync")
	}
	if !seen {
		return subfamFail(scenario, "counterpart-sees-it", inboxOut,
			fmt.Sprintf("B's `a2a inbox --json` lists %s after sync", sub.ID), inboxOut)
	}

	// --- B acknowledges: submitted -> acknowledged ---
	if _, stderr, err := b.Run(ctx, "ack", sub.ID); err != nil {
		return subfamResultFromErr(scenario, "counterpart-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack succeeds and opens its own PR")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", sub.ID))
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return subfamResultFromErr(scenario, "ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- B verify-passes: acknowledged -> accepted (terminal). ---
	if _, stderr, err := b.Run(ctx, "verify-pass", sub.ID); err != nil {
		return subfamResultFromErr(scenario, "counterpart-verify-pass", fmt.Errorf("%w: %s", err, stderr), "B's a2a verify-pass succeeds and opens its own PR")
	}
	verifyPassPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "verify-pass", sub.ID))
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-verify-pass", err, "verify-pass's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, verifyPassPR.Number); err != nil {
		return subfamResultFromErr(scenario, "verify-pass-land-sync", err, "the verify-pass lands on main and reaches B's mirror")
	}

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "owner-sync-before-read", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's verify-pass before reading the terminal state")
	}
	state, err := subfamShowState(ctx, a, sub.ID)
	if err != nil {
		return subfamResultFromErr(scenario, "terminal-state-read", err, "a2a show reads the accepted handoff back")
	}
	if state != "accepted" {
		return subfamFail(scenario, "terminal-state-read", state,
			"the handoff reaches the terminal `accepted` state (handoffRows carries no transition out of accepted)", state)
	}

	narrative := fmt.Sprintf("%s: submit PR #%d, ack PR #%d, verify-pass PR #%d — folded state %q",
		sub.ID, sub.PRNumber, ackPR.Number, verifyPassPR.Number, state)
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       narrative,
		PassEvidence: narrative,
	}
}

// subfamResponseLifecycle drives response's Layer-1 story and IS spec 38
// §6-Q3/plan D-H's own answer, read live off internal/fold rather than
// decided here.
//
// The parent is a QUESTION, not a requirement, despite spec 38 §T2's own
// illustrative wording ("requirement -> response -> verify"): that wording
// is not table-legal. internal/fold/table.go's requirementRows() carries no
// TRespond row (only exchangeRows(question)/exchangeRows(work_request) do),
// so `a2a respond <requirement-id>` is refused by lifecycleCheckLegality
// (cmd_lifecycle.go) before it ever reaches the write funnel — there is no
// live path from a requirement to a response. This is a genuine spec/code
// discrepancy, reported in this wave's findings, not silently routed
// around: AC-980.1 only requires response's row to use A LEGAL parent, and
// the fold table names which kinds qualify. question is picked over
// work_request only because its scaffold template needs fewer filled
// fields (no interim_behavior/needed_by/acceptance_criteria), minimizing
// this already-long row's own risk surface — not because work_request would
// not equally work.
//
// The terminal-state read is this row's whole point, and it is MORE
// degenerate than a first read of fold.go suggests — traced through
// internal/cache/mirror.go's actual data flow, not asserted from the
// transition table alone:
//
//   - Zero events on the response's OWN subject (before `respond` has even
//     landed a `respond` event that names it): Fold's empty-slice branch
//     returns postSubmissionState(KindResponse) = `submitted` directly
//     (fold.go). This is the ONE state a response's own independent fold
//     can produce without ever touching applyResponseScoped.
//   - Once a verify/dispute event exists whose Subject IS the response's
//     own id (exactly what VerifyCommand/DisputeCommand author,
//     cmd_lifecycle.go), gatherEvents (mirror.go) folds it into the
//     response artifact's OWN Fold(KindResponse, responseEnv, ...) pass —
//     using the RESPONSE's own envelope, whose `from` is the RESPONDER
//     (B), not the parent's owner (A). applyResponseScoped's FIRST check is
//     `authorized(RoleOwner, env, event.Actor.System, membership)` against
//     THAT envelope — A (the verifier) is never B, so this fails, a
//     FlagUnauthorizedActor is appended, and the function returns before
//     ever touching result.State. State never leaves NewResult's own
//     `draft`. (Not FlagIllegalTransition — verified by tracing
//     applyResponseScoped's own early return, not assumed.)
//
// So a response artifact's OWN independent Fold call can reach `submitted`
// (zero events) or `draft`-plus-an-unauthorized-flag (once any verify/
// dispute event exists) — never `verified`/`disputed` on its own, either
// way. What `a2a show <response-id>` actually prints is ALWAYS
// mirror.go's own overlay pass (`for respID, parentID := range parentOf`),
// which fires UNCONDITIONALLY once `out[pIdx].Result.Responses[respID]`
// exists — and that entry is populated the moment the PARENT's own
// `respond` event folds (applyPrimaryScoped's TRespond handling seeds it to
// `submitted` immediately, before verify ever runs). So neither the
// pre-verify NOR the post-verify `a2a show` reading this row observes is
// ever the response's own independent value — both are the overlay. They
// happen to COINCIDE pre-verify (independent fold's zero-events fallback
// and the overlay both say `submitted`, by two unrelated code paths
// agreeing), and they DIVERGE post-verify (independent fold cannot reach
// `verified` at all; only the overlay can). response's own independent
// lifecycle is therefore degenerate exactly as §6-Q3 anticipated — this row
// drives what a real operator actually sees (the overlay, both times) and
// records the independent-fold distinction as prose, not as a second
// assertion this rig cannot make `a2a show` produce. See
// response_terminal_state in this wave's own report for the prose answer.
//
// `a2a verify`'s own D-024 convenience additionally closes the parent in
// the SAME PR when it is the response's only sibling on that parent
// (cmd_lifecycle.go VerifyCommand.Run: `if len(result.Responses) == 1`),
// so this row also reads the parent's own post-verify state as a bonus,
// non-asserted observation (recorded in Detail only — asserting on it would
// silently absorb the D-024 convenience into THIS row's own pass/fail,
// which is not what this row claims to test).
func subfamResponseLifecycle(ctx context.Context, h *harness) Result {
	const scenario = subfamResponseScenario
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}

	parent, err := h.DraftAndSubmit(ctx, a, "question")
	if err != nil {
		return subfamResultFromErr(scenario, "draft-submit-parent", err, "draft+submit a question (response's parent) opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, parent.PRNumber); err != nil {
		return subfamResultFromErr(scenario, "parent-land-sync", err, "the parent question lands on main and reaches A's mirror")
	}

	// --- the counterpart actually sees it (AC-980.1) ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed parent")
	}
	seen, inboxOut, err := subfamInboxContains(ctx, b, parent.ID)
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-sees-it", err, "B's a2a inbox succeeds after sync")
	}
	if !seen {
		return subfamFail(scenario, "counterpart-sees-it", inboxOut,
			fmt.Sprintf("B's `a2a inbox --json` lists %s after sync", parent.ID), inboxOut)
	}

	// --- B acknowledges: submitted -> acknowledged (respond's own legal
	// fromStates do not include submitted — acknowledge is a real
	// precondition, not ceremony). ---
	if _, stderr, err := b.Run(ctx, "ack", parent.ID); err != nil {
		return subfamResultFromErr(scenario, "counterpart-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack succeeds and opens its own PR")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", parent.ID))
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return subfamResultFromErr(scenario, "ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	// --- B responds: draft+submit collapsed (D-026) into one PR, keyed by
	// D6's stable semantic operation identity. ---
	respondKey, respondBranch := respondOperation(b.System, parent.ID, "answered")
	if _, stderr, err := b.Run(ctx, respondCommandArgs(parent.ID, "answered")...); err != nil {
		return subfamResultFromErr(scenario, "counterpart-respond", fmt.Errorf("%w: %s", err, stderr), "B's a2a respond succeeds and opens its own PR")
	}
	respondPR, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-respond", err, "respond's semantic-operation branch has a PR")
	}
	responseID, err := operationArtifactID(respondPR.Body, respondKey, parent.ID, "XS-")
	if err != nil {
		return subfamResultFromErr(scenario, "counterpart-respond", err, "respond's PR metadata names the new XS response artifact")
	}
	if err := subfamAwaitGreenAndLand(ctx, h, b, respondPR.Number); err != nil {
		return subfamResultFromErr(scenario, "respond-land-sync", err, "the response lands on main and reaches B's mirror (required check concludes success)")
	}

	// --- A (the parent's own owner) sees the response and reads its
	// PRE-verify state: `submitted`, via mirror.go's overlay — the parent's
	// own `respond` event already seeded Result.Responses[responseID] to
	// `submitted` the moment it landed, so the overlay is ALREADY active
	// here, not merely coincidentally equal to what a response's own
	// zero-events fold would separately say (this function's own doc
	// comment traces both paths and why they agree pre-verify and diverge
	// post-verify). ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return subfamResultFromErr(scenario, "owner-sync-sees-response", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's response before verifying it")
	}
	preVerifyState, err := subfamShowState(ctx, a, responseID)
	if err != nil {
		return subfamResultFromErr(scenario, "owner-sees-response", err, "a2a show reads the just-landed response back")
	}
	if preVerifyState != "submitted" {
		return subfamFail(scenario, "owner-sees-response", preVerifyState,
			"before verify, `a2a show` on the response reads `submitted` off the parent's own overlay (spec 38 §6-Q3/D-H)", preVerifyState)
	}

	// --- A records its verification rationale before verifying. This is the
	// exact live shape from fb-20260801-457629: a note on a submitted response
	// must pass the space's V2/V3 check, merge, and leave the response state
	// unchanged. Before the regression fix the client opened this PR and the
	// required check rejected it with LFC-001 forever. ---
	if _, stderr, err := a.Run(ctx, "note", "--note", "verification rationale recorded before verify", responseID); err != nil {
		return subfamResultFromErr(scenario, "owner-note", fmt.Errorf("%w: %s", err, stderr), "A's a2a note on the submitted response succeeds and opens its own PR")
	}
	notePR, err := h.pullForBranch(ctx, space.BranchName(a.System, "note", responseID))
	if err != nil {
		return subfamResultFromErr(scenario, "owner-note", err, "the note's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, notePR.Number); err != nil {
		return subfamResultFromErr(scenario, "note-land-sync", err, "the transition-free note passes the required check, lands, and reaches A's mirror")
	}
	postNoteState, err := subfamShowState(ctx, a, responseID)
	if err != nil {
		return subfamResultFromErr(scenario, "post-note-state-read", err, "a2a show reads the noted response back")
	}
	if postNoteState != "submitted" {
		return subfamFail(scenario, "post-note-state-read", postNoteState,
			"after note and before verify, the response remains `submitted` because note is transition-free", postNoteState)
	}

	// --- A verifies: submitted -> verified. Single-response exchange, so
	// D-024's convenience also closes the parent in the SAME PR — this
	// branch is ALSO composite. ---
	if _, stderr, err := a.Run(ctx, "verify", responseID); err != nil {
		return subfamResultFromErr(scenario, "owner-verify", fmt.Errorf("%w: %s", err, stderr), "A's a2a verify succeeds and opens its own PR")
	}
	verifyPR, err := h.pullForBranchContaining(ctx, a.System, "verify", responseID)
	if err != nil {
		return subfamResultFromErr(scenario, "owner-verify", err, "verify's own composite branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, verifyPR.Number); err != nil {
		return subfamResultFromErr(scenario, "verify-land-sync", err, "the verify (+ D-024 auto-close) lands on main and reaches A's mirror")
	}

	// --- Terminal state: `verified`, read through the SAME overlay
	// `a2a show` renders for a real operator — never a second, private fold
	// this row would have to keep faithful to internal/fold by hand. ---
	responseState, err := subfamShowState(ctx, a, responseID)
	if err != nil {
		return subfamResultFromErr(scenario, "terminal-state-read", err, "a2a show reads the verified response back")
	}
	if responseState != "verified" {
		return subfamFail(scenario, "terminal-state-read", responseState,
			"the response reaches the terminal `verified` state via cache/mirror.go's parent-overlay (spec 38 §6-Q3/D-H)", responseState)
	}
	// Non-asserted bonus observation — see this function's own doc comment
	// on why D-024's parent auto-close is reported, not asserted, here.
	parentState, parentErr := subfamShowState(ctx, a, parent.ID)
	if parentErr != nil {
		parentState = "unread: " + parentErr.Error()
	}

	narrative := fmt.Sprintf("%s (parent %s): ack PR #%d, respond PR #%d, note PR #%d, verify PR #%d — response state %q, parent state %q (D-024 bonus, unasserted)",
		responseID, parent.ID, ackPR.Number, respondPR.Number, notePR.Number, verifyPR.Number, responseState, parentState)
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       narrative,
		PassEvidence: narrative,
	}
}
