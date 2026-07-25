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

// runDraftFamilyScenarios drives spec 38's AC-980.1 Layer-1 row for the one
// kind fold.go's postSubmissionState groups as the "draft" family: decision.
// It is this family's ONE exported-shape entry, matching
// runSubmittedFamilyScenarios' own convention.
//
// decision gets its own file rather than folding into
// scenarios_submitted_live.go: its §3.4.4 entry transition is `propose`
// (cmd_submit.go's submitFirstTransition), not `submit`/`publish`, its
// terminal transition needs BOTH systems as `required_approvers` (a quorum,
// not a single counterpart's ack), and its own G3 gate marker
// (lifecycleVerbTable's approve/reject rows) is a shape none of the
// submitted-family kinds carry — different enough a story that folding it
// in would blur, not clarify, "a kind's whole story stays readable" (spec
// 38 §T3).
//
// Every helper below is prefixed draftfam* — this phase's own second
// scenario-family namespace (subfam* is scenarios_submitted_live.go's own),
// so identifiers cannot collide across the package's scenario families.
func runDraftFamilyScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{draftfamDecisionLifecycle(ctx, h)}

	// Same parking discipline every other family's own tail documents.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// draftfamDecisionScenario is this family's one catalogue name — catalogue.go
// carries the literal duplicate (untagged code cannot reference this
// build-tag-gated constant, the same split ac973Scenario/subfam* consts
// already live with).
const draftfamDecisionScenario = "decision-lifecycle-to-approved"

// draftfamResultFromErr/draftfamFail/draftfamShowDoc/draftfamShowState/
// draftfamInboxContains are this file's own copies of
// scenarios_submitted_live.go's subfam* siblings — same tiny logic, kept
// file-private per this repo's own "disjoint files" idiom
// (cmd_lifecycle.go's lifecycleMembership doc comment: "kept file-private
// per the Placement decision rather than exported cross-file") rather than
// exported and shared across a package boundary that is otherwise scenario-
// family-disjoint by construction.

func draftfamResultFromErr(scenario, step string, err error, expected string) Result {
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

func draftfamFail(scenario, step, observed, expected, detail string) Result {
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

type draftfamShowDoc struct {
	State string `json:"state"`
}

func draftfamShowState(ctx context.Context, c *checkout, id string) (string, error) {
	stdout, stderr, err := c.Run(ctx, "show", id, "--json")
	if err != nil {
		return "", fmt.Errorf("livee2e: a2a show %s (%s): %w: %s", id, c.System, err, strings.TrimSpace(stderr))
	}
	var doc draftfamShowDoc
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		return "", fmt.Errorf("livee2e: decode `a2a show %s --json` output (%s): %w", id, c.System, jerr)
	}
	return doc.State, nil
}

func draftfamInboxContains(ctx context.Context, c *checkout, id string) (found bool, stdout string, err error) {
	stdout, stderr, runErr := c.Run(ctx, "inbox", "--json")
	if runErr != nil {
		return false, "", fmt.Errorf("livee2e: a2a inbox (%s): %w: %s", c.System, runErr, strings.TrimSpace(stderr))
	}
	return strings.Contains(stdout, id), stdout, nil
}

// draftfamDecisionLifecycle drives decision's Layer-1 story — spec 38 §T2's
// own illustrative "decision propose -> approve", driven with a REAL
// quorum rather than a single approver: draftfill.go fills
// `required_approvers: [<Me>, <Peer>]` for every decision draft (both
// systems), and internal/fold's quorumReached (fold.go) requires EVERY
// entry in env.RequiredApprovers to have approved before the dynamic
// `approve` row moves the decision out of `proposed` — so this row has B
// approve first (quorum 1 of 2, state stays `proposed`) and A approve
// second (quorum 2 of 2, state -> `approved`, terminal: decisionRows
// carries no ordinary transition out of approved besides the exceptional
// `supersede`).
//
// `a2a approve` is ALWAYS G3-gated (lifecycleVerbTable's GateMarker flag,
// cmd_lifecycle.go), but that gate is ADVISORY ONLY in this space:
// provision_live.go arms branch protection with
// `required_approving_review_count: 0`, so the PR body carries the G3
// notice but nothing blocks auto-merge on it — approve behaves like every
// other write funnel call in this matrix, no human review step to fake.
func draftfamDecisionLifecycle(ctx context.Context, h *harness) Result {
	const scenario = draftfamDecisionScenario
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return draftfamResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh submit")
	}

	sub, err := h.DraftAndSubmit(ctx, a, "decision")
	if err != nil {
		return draftfamResultFromErr(scenario, "draft-submit", err, "draft+submit a decision (the `propose` entry transition) opens its own PR")
	}
	if err := happyLandAndSync(ctx, h, a, sub.PRNumber); err != nil {
		return draftfamResultFromErr(scenario, "propose-land-sync", err, "the proposed decision lands on main and reaches A's mirror")
	}

	// --- the counterpart actually sees it (AC-980.1) ---
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return draftfamResultFromErr(scenario, "counterpart-sync", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches the just-landed decision")
	}
	seen, inboxOut, err := draftfamInboxContains(ctx, b, sub.ID)
	if err != nil {
		return draftfamResultFromErr(scenario, "counterpart-sees-it", err, "B's a2a inbox succeeds after sync")
	}
	if !seen {
		return draftfamFail(scenario, "counterpart-sees-it", inboxOut,
			fmt.Sprintf("B's `a2a inbox --json` lists %s after sync", sub.ID), inboxOut)
	}

	// --- B approves: 1 of 2 required_approvers. Quorum not yet reached, so
	// fold's dynamic `approve` row (applyApprove) keeps the decision at
	// `proposed` — not asserted here (the assertion that matters is the
	// FINAL terminal read below), but recorded as the reason approval order
	// is not symmetric with the exchange-family rows' single ack. ---
	if _, stderr, err := b.Run(ctx, "approve", sub.ID); err != nil {
		return draftfamResultFromErr(scenario, "counterpart-approve", fmt.Errorf("%w: %s", err, stderr), "B's a2a approve succeeds and opens its own PR")
	}
	bApprovePR, err := h.pullForBranch(ctx, space.BranchName(b.System, "approve", sub.ID))
	if err != nil {
		return draftfamResultFromErr(scenario, "counterpart-approve", err, "B's approve branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, b, bApprovePR.Number); err != nil {
		return draftfamResultFromErr(scenario, "counterpart-approve-land-sync", err, "B's approval lands on main and reaches B's mirror")
	}

	// --- A approves: 2 of 2 — quorum reached, State -> approved (terminal). ---
	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return draftfamResultFromErr(scenario, "owner-sync-before-approve", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches B's approval before A's own")
	}
	if _, stderr, err := a.Run(ctx, "approve", sub.ID); err != nil {
		return draftfamResultFromErr(scenario, "owner-approve", fmt.Errorf("%w: %s", err, stderr), "A's a2a approve succeeds and opens its own PR")
	}
	aApprovePR, err := h.pullForBranch(ctx, space.BranchName(a.System, "approve", sub.ID))
	if err != nil {
		return draftfamResultFromErr(scenario, "owner-approve", err, "A's approve branch has an open PR")
	}
	if err := happyLandAndSync(ctx, h, a, aApprovePR.Number); err != nil {
		return draftfamResultFromErr(scenario, "owner-approve-land-sync", err, "A's approval lands on main and reaches A's mirror")
	}

	state, err := draftfamShowState(ctx, a, sub.ID)
	if err != nil {
		return draftfamResultFromErr(scenario, "terminal-state-read", err, "a2a show reads the approved decision back")
	}
	if state != "approved" {
		return draftfamFail(scenario, "terminal-state-read", state,
			"the decision reaches the terminal `approved` state once BOTH required_approvers have approved (quorum)", state)
	}

	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: propose PR #%d, B-approve PR #%d, A-approve PR #%d — folded state %q",
			sub.ID, sub.PRNumber, bApprovePR.Number, aApprovePR.Number, state),
	}
}
