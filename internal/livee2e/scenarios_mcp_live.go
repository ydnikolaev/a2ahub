//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"strings"
)

func mcpFail(scenario, system, expected string, err error) Result {
	verdict, _ := happyVerdictForErr(err)
	return Result{
		Scenario: scenario, System: system, Surface: SurfaceMCP,
		Verdict: verdict, Expected: expected, Observed: err.Error(),
	}
}

func mcpSubmitAndVisibility(ctx context.Context, h *harness, system string, author, observer *checkout) []Result {
	const submitScenario = "submit-gate-merge"
	const visibilityScenario = "cross-system-visibility"
	if _, stderr, err := author.Run(ctx, "sync"); err != nil {
		wrapped := fmt.Errorf("sync before MCP write: %w: %s", err, stderr)
		return []Result{
			mcpFail(submitScenario, system, "MCP submit reaches a green auto-merged PR", wrapped),
			mcpFail(visibilityScenario, system, "peer MCP read sees the merged artifact", wrapped),
		}
	}
	session, err := startMCPSession(ctx, author)
	if err != nil {
		return []Result{
			mcpFail(submitScenario, system, "MCP initialize succeeds", err),
			mcpFail(visibilityScenario, system, "MCP initialize succeeds", err),
		}
	}
	defer session.Close()
	write, err := mcpDraftAndSubmit(session, author, "announcement", "", nil)
	if err != nil {
		return []Result{
			mcpFail(submitScenario, system, "a2a_new + a2a_submit return structured success", err),
			mcpFail(visibilityScenario, system, "the MCP-authored announcement reaches main", err),
		}
	}
	pr, err := write.PRNumber()
	if err != nil {
		return []Result{mcpFail(submitScenario, system, "structured pr_url names a PR", err), mcpFail(visibilityScenario, system, "structured pr_url names a PR", err)}
	}
	check, err := h.AwaitCheck(ctx, author, pr)
	if err != nil || check.Conclusion != "success" {
		if err == nil {
			err = fmt.Errorf("required check conclusion=%q", check.Conclusion)
		}
		return []Result{mcpFail(submitScenario, system, "required check succeeds", err), mcpFail(visibilityScenario, system, "MCP-authored write lands", err)}
	}
	if _, err := happyAwaitMerged(ctx, h, pr); err != nil {
		return []Result{mcpFail(submitScenario, system, "PR auto-merges", err), mcpFail(visibilityScenario, system, "MCP-authored write lands", err)}
	}
	submitResult := Result{
		Scenario: submitScenario, System: system, Surface: SurfaceMCP, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: JSON-RPC initialize + a2a_new + a2a_submit, PR #%d green and merged", write.IDs[0], pr),
	}

	observerSession, err := startMCPSession(ctx, observer)
	if err != nil {
		return []Result{submitResult, mcpFail(visibilityScenario, system, "observer MCP initializes", err)}
	}
	defer observerSession.Close()
	call, err := observerSession.Call("a2a_read", map[string]any{"view": "inbox"})
	if err != nil || call.IsError {
		if err == nil {
			err = fmt.Errorf("a2a_read tool error: %s", mcpText(call))
		}
		return []Result{submitResult, mcpFail(visibilityScenario, system, "observer a2a_read inbox succeeds", err)}
	}
	evidence := string(call.Structured) + mcpText(call)
	if !strings.Contains(evidence, write.IDs[0]) {
		return []Result{submitResult, Result{
			Scenario: visibilityScenario, System: system, Surface: SurfaceMCP, Verdict: VerdictFail,
			Expected: "observer MCP inbox contains " + write.IDs[0], Observed: evidence,
		}}
	}
	return []Result{submitResult, Result{
		Scenario: visibilityScenario, System: system, Surface: SurfaceMCP, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s authored through MCP by %s and observed through MCP by %s", write.IDs[0], author.System, observer.System),
	}}
}

func mcpLifecycle(ctx context.Context, h *harness, system string, c *checkout) Result {
	const scenario = "lifecycle-transitions"
	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return mcpFail(scenario, system, "sync before MCP lifecycle", fmt.Errorf("%w: %s", err, stderr))
	}
	session, err := startMCPSession(ctx, c)
	if err != nil {
		return mcpFail(scenario, system, "MCP initializes", err)
	}
	defer session.Close()
	slug := "matrix-mcp-lifecycle-" + c.System
	publish, err := mcpDraftAndSubmit(session, c, "requirement", slug, nil)
	if err != nil {
		return mcpFail(scenario, system, "MCP publishes requirement", err)
	}
	publishPR, err := publish.PRNumber()
	if err != nil {
		return mcpFail(scenario, system, "publish returns PR", err)
	}
	if err := happyLandAndSync(ctx, h, c, publishPR); err != nil {
		return mcpFail(scenario, system, "MCP publish lands", err)
	}
	var withdraw mcpWriteResult
	if err := mcpCallOK(session, "a2a_lifecycle", map[string]any{
		"action": "withdraw", "ids": publish.IDs,
	}, &withdraw); err != nil {
		return mcpFail(scenario, system, "MCP withdraw succeeds", err)
	}
	withdrawPR, err := withdraw.PRNumber()
	if err != nil {
		return mcpFail(scenario, system, "withdraw returns PR", err)
	}
	if withdrawPR == publishPR {
		return Result{Scenario: scenario, System: system, Surface: SurfaceMCP, Verdict: VerdictFail,
			Expected: "publish and withdraw use distinct PRs", Observed: fmt.Sprintf("both PR #%d", publishPR)}
	}
	if err := happyLandAndSync(ctx, h, c, withdrawPR); err != nil {
		return mcpFail(scenario, system, "MCP withdraw lands with a green required check", err)
	}
	return Result{Scenario: scenario, System: system, Surface: SurfaceMCP, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: publish PR #%d, withdraw PR #%d via JSON-RPC", publish.IDs[0], publishPR, withdrawPR)}
}

func mcpContractLifecycle(ctx context.Context, h *harness, system string, c *checkout) Result {
	const scenario = "contract-publish-deprecate-retire"
	if _, stderr, err := c.Run(ctx, "sync"); err != nil {
		return mcpFail(scenario, system, "sync before MCP contract lifecycle", fmt.Errorf("%w: %s", err, stderr))
	}
	session, err := startMCPSession(ctx, c)
	if err != nil {
		return mcpFail(scenario, system, "MCP initializes", err)
	}
	defer session.Close()
	slug := liveRunSlug("matrix-mcp-contract-"+c.System, h.PRFloor)
	publish, err := mcpDraftAndSubmit(session, c, "contract", slug, nil)
	if err != nil {
		return mcpFail(scenario, system, "MCP publishes contract", err)
	}
	id := publish.IDs[0]
	publishPR, err := publish.PRNumber()
	if err != nil {
		return mcpFail(scenario, system, "publish returns PR", err)
	}
	if err := happyLandAndSync(ctx, h, c, publishPR); err != nil {
		return mcpFail(scenario, system, "publish lands", err)
	}
	var deprecate mcpWriteResult
	if err := mcpCallOK(session, "a2a_contract", map[string]any{
		"action": "deprecate", "id": id,
		"successor": id + "-successor@2.0.0", "sunset": "2020-01-01",
	}, &deprecate); err != nil {
		return mcpFail(scenario, system, "MCP deprecates contract", err)
	}
	deprecatePR, err := deprecate.PRNumber()
	if err != nil {
		return mcpFail(scenario, system, "deprecate returns PR", err)
	}
	if err := happyLandAndSync(ctx, h, c, deprecatePR); err != nil {
		return mcpFail(scenario, system, "deprecation lands", err)
	}
	var retire mcpWriteResult
	if err := mcpCallOK(session, "a2a_contract", map[string]any{
		"action": "retire", "id": id,
	}, &retire); err != nil {
		return mcpFail(scenario, system, "MCP retires contract", err)
	}
	retirePR, err := retire.PRNumber()
	if err != nil {
		return mcpFail(scenario, system, "retire returns PR", err)
	}
	if publishPR == deprecatePR || publishPR == retirePR || deprecatePR == retirePR {
		return Result{Scenario: scenario, System: system, Surface: SurfaceMCP, Verdict: VerdictFail,
			Expected: "publish, deprecate and retire use three PRs",
			Observed: fmt.Sprintf("publish=%d deprecate=%d retire=%d", publishPR, deprecatePR, retirePR)}
	}
	if err := happyLandAndSync(ctx, h, c, retirePR); err != nil {
		return mcpFail(scenario, system, "MCP retirement lands with a green required check", err)
	}
	return Result{Scenario: scenario, System: system, Surface: SurfaceMCP, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: publish #%d, deprecate #%d, retire #%d via JSON-RPC", id, publishPR, deprecatePR, retirePR)}
}

func mcpOutOfSectionRefused(ctx context.Context, h *harness) Result {
	const scenario = "out-of-section-write-refused"
	res := Result{Scenario: scenario, System: SystemB, Surface: SurfaceMCP}
	branchesBefore, err := h.Prov.ListBranches(ctx, h.Org, h.Repo)
	if err != nil {
		return mcpFail(scenario, SystemB, "read branches before refusal", err)
	}
	pullsBefore, err := h.runPulls(ctx)
	if err != nil {
		return mcpFail(scenario, SystemB, "read PRs before refusal", err)
	}
	session, err := startMCPSession(ctx, h.B)
	if err != nil {
		return mcpFail(scenario, SystemB, "MCP initializes", err)
	}
	defer session.Close()
	id, err := mcpDraft(session, h.B, "announcement", "", map[string]string{"from": h.A.System})
	if err != nil {
		return mcpFail(scenario, SystemB, "MCP drafts foreign-from artifact", err)
	}
	call, err := session.Call("a2a_submit", map[string]any{"ids": []string{id}})
	if err != nil {
		return mcpFail(scenario, SystemB, "MCP returns a tool-level refusal", err)
	}
	branchesAfter, berr := h.Prov.ListBranches(ctx, h.Org, h.Repo)
	pullsAfter, perr := h.runPulls(ctx)
	if berr != nil || perr != nil {
		return mcpFail(scenario, SystemB, "read remote state after refusal", fmt.Errorf("branches=%v pulls=%v", berr, perr))
	}
	newBranches := refusalNewElements(branchesBefore, branchesAfter)
	newPulls := refusalNewElements(refusalPullNumberStrings(pullsBefore), refusalPullNumberStrings(pullsAfter))
	text := mcpText(call)
	switch {
	case !call.IsError:
		res.Verdict, res.Expected, res.Observed = VerdictFail, "tools/call result.isError=true", string(call.Structured)
	case !strings.Contains(text, "CC-002"):
		res.Verdict, res.Expected, res.Observed = VerdictFail, "structured MCP refusal names CC-002", text
	case len(newBranches) > 0 || len(newPulls) > 0:
		res.Verdict, res.Expected, res.Observed = VerdictFail, "refusal creates no remote state", fmt.Sprintf("branches=%v pulls=%v", newBranches, newPulls)
	default:
		res.Verdict = VerdictPass
		res.Detail = fmt.Sprintf("%s refused with JSON-RPC result.isError=true and CC-002; no branch/PR", id)
	}
	return res
}
