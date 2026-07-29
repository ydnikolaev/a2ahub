//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// This file drives spec 38's AC-981.1 Layer-2 rows (§T2): internal/fold has
// excellent unit coverage of its own transition table
// (internal/fold/legality_test.go), but that proves only that the RULE is
// right, not that the refusal survives the WHOLE path — CLI -> V2's local
// fold.CheckLegality check -> the write funnel -> CI -> merge — rather than
// being enforced only where the unit test looks. Every row here picks one
// (kind, fromState, transition) combination that is genuinely ABSENT from
// internal/fold/table.go's own rows (or, for response, one where the row
// exists but the ACTOR is wrong — the brief's own "LFC-002/the
// unauthorized-actor code where that is the real rule" shape), drives it
// through the REAL `a2a` binary, and asserts BOTH halves the phase's brief
// calls "the whole point of Layer 2":
//
//   - the CLI refuses, citing the expected LFC- registry code
//     (verdictRefusalMessage, internal/cli/cmd_lifecycle.go); and
//   - NO PULL REQUEST was opened — the refusal happened BEFORE the write
//     funnel. lifecycleCheckLegality/lifecycleCheckResponseLegality both run
//     strictly before any funnel call in every command this file drives
//     (cmd_lifecycle.go's own Run() bodies: `if verdict != fold.VerdictLegal
//     { ...; return 1 }` precedes every write), so a refusal citing the
//     right code that STILL opened a PR would be a materially weaker
//     guarantee than the one this row claims — and the row must be able to
//     tell the two apart, not just assume the stronger one because the
//     weaker one would also look green.
//
// Every helper is prefixed illegalfam* — this wave's own scenario-family
// namespace, matching wave F's subfam*/draftfam* convention (scenarios_
// submitted_live.go / scenarios_decision_live.go) so identifiers cannot
// collide across the package's scenario families.
//
// FINDING, not a workaround (this phase's own rule): illegalfamResponse
// reuses subfamAwaitGreenAndLand (scenarios_submitted_live.go) at its own
// `a2a respond` step, which carries wave F's own unresolved KNOWN RISK —
// `a2a respond` never fills a new response's `to:` field, which
// internal/validate's checkAddressees may reject at CI. If that risk is
// real live, THIS row reds at "respond-land-sync" with the identical
// symptom response-lifecycle-to-verified's own Layer-1 row already
// documents — that is ONE product gap surfacing as two failing report
// rows, not two independent defects, and the report should be read that
// way (also recorded in this wave's own report under findings).
func runIllegalTransitionScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{
		illegalfamContract(ctx, h),
		illegalfamRequirement(ctx, h),
		illegalfamExchangeAcceptBeforeAck(ctx, h, "question", illegalfamQuestionScenario),
		illegalfamExchangeAcceptBeforeAck(ctx, h, "work_request", illegalfamWorkRequestScenario),
		illegalfamDecision(ctx, h),
		illegalfamHandoff(ctx, h),
		illegalfamAnnouncement(ctx, h),
		illegalfamResponse(ctx, h),
	}

	// Same parking discipline every other family's own tail documents: leave
	// both checkouts on a clean main so whichever family runs next does not
	// inherit a dirty working tree that looks like its own bug.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// This family's eight catalogue names (catalogue.go carries the literal
// duplicates — catalogue.go is untagged and cannot reference these
// build-tag-gated constants, the same split every other family's own
// scenario const already lives with).
const (
	illegalfamContractScenario     = "contract-retire-without-deprecation-refused"
	illegalfamRequirementScenario  = "requirement-satisfy-before-ack-refused"
	illegalfamQuestionScenario     = "question-accept-before-ack-refused"
	illegalfamWorkRequestScenario  = "work-request-accept-before-ack-refused"
	illegalfamDecisionScenario     = "decision-close-refused"
	illegalfamHandoffScenario      = "handoff-verify-pass-before-ack-refused"
	illegalfamAnnouncementScenario = "announcement-accept-refused"
	illegalfamResponseScenario     = "response-dispute-by-non-owner-refused"
)

// illegalfamContractSlug / illegalfamRequirementSlug: contract and
// requirement are BOTH artifact.ClassStanding (cmd_new.go), which MintStandingID
// refuses to mint without a slug ("--slug (or --field slug=<slug>) is
// required for standing types (contract, requirement)") — unlike every
// other kind this file drafts, whose ids are minted automatically
// (artifact.ClassExchangeBroadcast). Fixed, lowercase-kebab, this row's own
// (matching the other standing-id scenario bases). These refusal rows do not
// submit a semantic operation, so unlike deprecate scenarios they need no
// per-generation suffix.
const (
	illegalfamContractSlug    = "illegalfam-contract-retire"
	illegalfamRequirementSlug = "illegalfam-requirement-satisfy"
)

// illegalfamResultFromErr renders err into this family's Result, step-
// tagging Expected exactly as subfamResultFromErr/draftfamResultFromErr do.
// Filed under SystemA unconditionally, matching those two families' own
// precedent: which checkout actually drove the failing step is visible in
// Observed's own error text (and, for the illegal-transition step itself,
// in Detail — see illegalfamRefusalStep), not in this field.
func illegalfamResultFromErr(scenario, step string, err error, expected string) Result {
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

// illegalfamFail builds a VerdictFail row for an assertion this family
// observed directly (the CLI ran, but what it did was wrong) — same
// step-tagging as illegalfamResultFromErr.
func illegalfamFail(scenario, step, observed, expected, detail string) Result {
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// illegalfamRefusalStep drives ONE illegal-transition/unauthorized-actor
// probe through checkout c and asserts everything AC-981.1 needs: the CLI
// refuses citing wantCode, AND the write funnel never saw it — proven TWO
// ways, not one:
//
//   - branch is the transition's own deterministic head ref
//     (space.BranchName), checked before/after via h.countPRsForBranch — the
//     SAME before/after idiom scenarios_contract_integrity_live.go's own
//     "breaking-minor-no-pr-opened" step uses. This is the ONE assertion —
//     see the run-wide count below for why a second one is recorded but
//     deliberately NOT asserted on.
//   - a RUN-WIDE count via h.runPulls, before/after — recorded in Detail as
//     EVIDENCE, never asserted. It was tempting to assert on it too (every
//     row here drafts a BRAND-NEW artifact, so `branch` has never carried a
//     PR before this call — before=0, and branch alone only proves "still
//     0", which is also what a crashed lookup or a PR opened on some OTHER
//     branch would look like). It is deliberately NOT an assertion because
//     GitHub's pulls listing is eventually consistent: illegalfamResponse's
//     own row reads this total seconds after subfamAwaitGreenAndLand merged
//     TWO prior PRs (ack, respond) on the SAME repo, and an unconditional
//     `!=` would read as a false product defect if the second read simply
//     lagged the first by the listing's own propagation window — a false
//     red on this wave's newest family is worse than a slightly narrower
//     no-PR claim. The branch-specific assertion above already catches the
//     real failure mode this row exists to catch (the funnel actually
//     reached and opened a PR on the transition's OWN deterministic
//     branch); the total is kept only so a reader can SEE whether anything
//     else moved.
//
// Both counts go through h.runPulls (never h.Prov.ListPulls directly, per
// TestEveryPullLookupGoesThroughRunPulls), so this stays compliant with
// the same guard rather than needing a documented exemption.
func illegalfamRefusalStep(ctx context.Context, h *harness, scenario string, c *checkout, branch, wantCode, expected string, args ...string) Result {
	branchBefore, err := h.countPRsForBranch(ctx, branch)
	if err != nil {
		return illegalfamResultFromErr(scenario, "refused-before-count", err, "can list PRs on "+branch+" before the refused attempt")
	}
	totalBefore, err := h.runPulls(ctx)
	if err != nil {
		return illegalfamResultFromErr(scenario, "refused-before-count", err, "can list this run's PRs before the refused attempt")
	}

	_, stderr, cliErr := c.Run(ctx, args...)
	cmdText := "a2a " + strings.Join(args, " ")
	if cliErr == nil {
		return illegalfamFail(scenario, "illegal-refused", fmt.Sprintf("`%s` (%s) exited 0", cmdText, c.System), expected, "")
	}
	if !strings.Contains(stderr, wantCode) {
		return illegalfamFail(scenario, "illegal-refused",
			fmt.Sprintf("`%s` (%s) refused, but its stderr does not cite %s: %s", cmdText, c.System, wantCode, stderr),
			expected, stderr)
	}

	branchAfter, err := h.countPRsForBranch(ctx, branch)
	if err != nil {
		return illegalfamResultFromErr(scenario, "refused-no-pr-opened", err, "can list PRs on "+branch+" after the refused attempt")
	}
	if branchAfter != branchBefore {
		return illegalfamFail(scenario, "refused-no-pr-opened",
			fmt.Sprintf("PR count on %s went from %d to %d", branch, branchBefore, branchAfter),
			"a refused transition never reaches the write funnel, so it opens NO new PR on its own deterministic branch", "")
	}
	// Recorded, not asserted — see this function's own doc comment on why
	// an eventually-consistent run-wide listing cannot safely gate a row.
	totalAfterN := -1
	if totalAfter, totalErr := h.runPulls(ctx); totalErr == nil {
		totalAfterN = len(totalAfter)
	}

	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("`%s` (acted as %s) refused citing %s; PR count on %s stayed %d (asserted); run-wide total %d -> %d (recorded evidence, not asserted — see illegalfamRefusalStep's own doc comment)",
			cmdText, c.System, wantCode, branch, branchBefore, len(totalBefore), totalAfterN),
	}
}

// illegalfamContract drives spec 38 §T2's own "retiring without a
// deprecation" example: `a2a contract retire` is legal ONLY from
// `deprecated` (contractRows(), internal/fold/table.go) — a freshly
// published contract sits at `published`, one hop short.
func illegalfamContract(ctx context.Context, h *harness) Result {
	const scenario = illegalfamContractScenario
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	sub, err := h.DraftAndSubmit(ctx, a, "contract", "--slug", illegalfamContractSlug)
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit", err, "draft+submit a contract opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "publish-land-sync", err, "the published contract lands on main and reaches A's mirror")
	}

	branch := space.BranchName(a.System, "contract-retire", sub.ID)
	return illegalfamRefusalStep(ctx, h, scenario, a, branch, "LFC-001",
		"`a2a contract retire` is refused for a PUBLISHED contract — retire's own table row is legal only from `deprecated` (spec 38 §T2's own \"retiring without a deprecation\" example)",
		"contract", "retire", sub.ID)
}

// illegalfamRequirement: `a2a satisfy` is legal ONLY from `acknowledged`
// (requirementRows()) — a freshly published requirement sits at
// `published`, before any `ack` has run.
func illegalfamRequirement(ctx context.Context, h *harness) Result {
	const scenario = illegalfamRequirementScenario
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	sub, err := h.DraftAndSubmit(ctx, a, "requirement", "--slug", illegalfamRequirementSlug)
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit", err, "draft+submit a requirement opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "publish-land-sync", err, "the published requirement lands on main and reaches A's mirror")
	}

	branch := space.BranchName(a.System, "satisfy", sub.ID)
	return illegalfamRefusalStep(ctx, h, scenario, a, branch, "LFC-001",
		"`a2a satisfy` is refused for a PUBLISHED (not yet acknowledged) requirement — satisfy's own table row is legal only from `acknowledged`",
		"satisfy", "--refs", "n/a", sub.ID)
}

// illegalfamExchangeAcceptBeforeAck drives question's and work_request's
// IDENTICAL Layer-2 story (exchangeRows is generated once for both kinds,
// so their illegal-transition shape is identical too): `a2a accept` is
// legal ONLY from `acknowledged` — a freshly submitted exchange sits at
// `submitted`, before the counterpart's own `ack` has run. Driven by B (the
// target — accept's own Role), so the refusal is purely about STATE, never
// confoundable with an actor mismatch.
func illegalfamExchangeAcceptBeforeAck(ctx context.Context, h *harness, kind, scenario string) Result {
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	sub, err := h.DraftAndSubmit(ctx, a, kind)
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit", err, fmt.Sprintf("draft+submit a %s opens its own PR", kind))
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "publish-land-sync", err, "the submitted "+kind+" lands on main and reaches A's mirror")
	}
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed "+kind)
	}

	branch := space.BranchName(b.System, "accept", sub.ID)
	return illegalfamRefusalStep(ctx, h, scenario, b, branch, "LFC-001",
		"`a2a accept` is refused for a SUBMITTED (not yet acknowledged) "+kind+" — accept's own table row is legal only from `acknowledged`",
		"accept", sub.ID)
}

// illegalfamDecision: decisionRows() carries NO `close` transition row for
// ANY decision state at all (`close` only exists in exchangeRows), so
// `a2a close` on a decision is illegal from the moment it exists — no ack,
// no approve, nothing extra to set up.
func illegalfamDecision(ctx context.Context, h *harness) Result {
	const scenario = illegalfamDecisionScenario
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	sub, err := h.DraftAndSubmit(ctx, a, "decision")
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit", err, "draft+submit a decision (the `propose` entry transition) opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "propose-land-sync", err, "the proposed decision lands on main and reaches A's mirror")
	}

	branch := space.BranchName(a.System, "close", sub.ID)
	return illegalfamRefusalStep(ctx, h, scenario, a, branch, "LFC-001",
		"`a2a close` is refused for a decision — decisionRows() carries no `close` transition for any decision state at all",
		"close", sub.ID)
}

// illegalfamHandoff: `a2a verify-pass` is legal ONLY from `acknowledged`
// (handoffRows()) — a freshly submitted handoff sits at `submitted`, before
// the counterpart's own `ack` has run. Driven by B (the target).
func illegalfamHandoff(ctx context.Context, h *harness) Result {
	const scenario = illegalfamHandoffScenario
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	sub, err := h.DraftAndSubmit(ctx, a, "handoff")
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit", err, "draft+submit a handoff opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "publish-land-sync", err, "the submitted handoff lands on main and reaches A's mirror")
	}
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed handoff")
	}

	branch := space.BranchName(b.System, "verify-pass", sub.ID)
	return illegalfamRefusalStep(ctx, h, scenario, b, branch, "LFC-001",
		"`a2a verify-pass` is refused for a SUBMITTED (not yet acknowledged) handoff — verify-pass's own table row is legal only from `acknowledged`",
		"verify-pass", sub.ID)
}

// illegalfamAnnouncement: announcementRows() carries no `accept` transition
// for ANY announcement state — `accept` only exists in exchangeRows.
// Deliberately NOT built on `a2a ack`: this wave's own brief names exactly
// this trap — `a2a ack <announcement>` was refused LFC-001 unconditionally
// only until commit 325fd4a (CheckLegality's own D-025 broadcast-ack
// branch), and is legal for any space member now, addressed or not
// (legality.go's own broadcastAckPermitted branch, checked BEFORE the
// generic table lookup this row's `accept` case falls through to).
func illegalfamAnnouncement(ctx context.Context, h *harness) Result {
	const scenario = illegalfamAnnouncementScenario
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	sub, err := h.DraftAndSubmit(ctx, a, "announcement")
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit", err, "draft+submit an announcement opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "publish-land-sync", err, "the published announcement lands on main and reaches A's mirror")
	}

	branch := space.BranchName(a.System, "accept", sub.ID)
	return illegalfamRefusalStep(ctx, h, scenario, a, branch, "LFC-001",
		"`a2a accept` is refused for an announcement — announcementRows() carries no `accept` transition for any state at all (NOT `a2a ack`, which is legal for any member per D-025)",
		"accept", sub.ID)
}

// illegalfamResponse drives spec 38 §T2's brief-named alternative Layer-2
// shape: not an absent table row, but the WRONG ACTOR for a row that does
// exist. responseRows()'s TDispute row IS legal from `submitted` — but
// CheckLegality's own verify/dispute branch (legality.go) resolves
// RoleOwner against the PARENT's own envelope (applyResponseScoped's own
// rule, D-024), i.e. the question's owner A — never the responder. B
// disputing its OWN response is therefore refused UNAUTHORIZED-ACTOR
// (LFC-002), not illegal-transition — the exact "LFC-002/the
// unauthorized-actor code where that is the real rule" case the brief
// names.
//
// The minimum REAL path to an existing response id is the full submit -> ack
// -> respond chain (exchangeRows' own TRespond row's fromStates do not
// include `submitted` — ack is a genuine precondition of the funnel path,
// not ceremony this row could skip), so this reuses subfamAwaitGreenAndLand
// at the `respond` step exactly as subfamResponseLifecycle does — see this
// file's own top-of-file FINDING note for what that KNOWN RISK means if it
// fires here.
func illegalfamResponse(ctx context.Context, h *harness) Result {
	const scenario = illegalfamResponseScenario
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}
	parent, err := h.DraftAndSubmit(ctx, a, "question")
	if err != nil {
		return illegalfamResultFromErr(scenario, "draft-submit-parent", err, "draft+submit a question (response's parent) opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, parent.PRNumber); err != nil {
		return illegalfamResultFromErr(scenario, "parent-land-sync", err, "the parent question lands on main and reaches A's mirror")
	}
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed parent")
	}
	if _, stderr, err := b.Run(ctx, "ack", parent.ID); err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-ack", fmt.Errorf("%w: %s", err, stderr), "B's a2a ack succeeds and opens its own PR")
	}
	ackPR, err := h.pullForBranch(ctx, space.BranchName(b.System, "ack", parent.ID))
	if err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-ack", err, "the ack's own branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, ackPR.Number); err != nil {
		return illegalfamResultFromErr(scenario, "ack-land-sync", err, "the ack lands on main and reaches B's mirror")
	}

	respondKey, respondBranch := respondOperation(b.System, parent.ID, "answered")
	if _, stderr, err := b.Run(ctx, respondCommandArgs(parent.ID, "answered")...); err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-respond", fmt.Errorf("%w: %s", err, stderr), "B's a2a respond succeeds and opens its own PR")
	}
	respondPR, err := h.pullForBranch(ctx, respondBranch)
	if err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-respond", err, "respond's semantic-operation branch has a PR")
	}
	responseID, err := operationArtifactID(respondPR.Body, respondKey, parent.ID, "XS-")
	if err != nil {
		return illegalfamResultFromErr(scenario, "counterpart-respond", err, "respond's PR metadata names the new XS response artifact")
	}
	if err := subfamAwaitGreenAndLand(ctx, h, b, respondPR.Number); err != nil {
		return illegalfamResultFromErr(scenario, "respond-land-sync", err, "the response lands on main and reaches B's mirror (required check concludes success)")
	}

	branch := space.BranchName(b.System, "dispute", responseID)
	return illegalfamRefusalStep(ctx, h, scenario, b, branch, "LFC-002",
		"`a2a dispute` on B's OWN response is refused UNAUTHORIZED-ACTOR — dispute's RoleOwner resolves to the parent question's owner (A), never the responder (B, who is acting here)",
		"dispute", "--reason", "wrong actor probe", responseID)
}
