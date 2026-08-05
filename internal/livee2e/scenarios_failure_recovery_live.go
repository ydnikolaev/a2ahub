//go:build livee2e

package livee2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// This file drives spec 38's AC-982.1/982.2/982.3 (§T2 Layer 3): the layer
// an automated fleet actually lives in and the one nothing else in this
// matrix covers — a 5xx mid-write, an interrupted submit retried, two
// systems writing concurrently. Every helper is prefixed layer3* — this
// wave's own scenario-family namespace, matching subfam*/illegalfam*'s own
// convention (scenarios_submitted_live.go / scenarios_illegal_live.go), so
// identifiers cannot collide across the package's scenario families.
//
// This phase adds NO product code (plan's own rule). Every row here drives
// the REAL `a2a` binary and the REAL space; the only harness-owned
// machinery is injectingProxy (injectproxy_live.go), which sits IN FRONT of
// the real GitHub API for exactly one row and is its own labelled evidence
// class (Result.EvidenceClass, report.go) — never a substitute for driving
// the other two rows for real.
func runFailureRecoveryScenarios(ctx context.Context, h *harness) []Result {
	results := []Result{
		layer3FiveXXInjected(ctx, h),
		layer3InterruptedSubmitRetried(ctx, h),
		layer3ConcurrentWrites(ctx, h),
	}

	// Same parking discipline every other family's own tail documents:
	// leave both checkouts on a clean main so whichever family runs next
	// does not inherit a dirty working tree that looks like its own bug.
	// Best-effort; a failure here does not change any already-recorded
	// result above.
	_, _, _ = h.A.Run(ctx, "sync")
	_, _, _ = h.B.Run(ctx, "sync")

	return results
}

// This family's three catalogue names (catalogue.go carries the literal
// duplicates — catalogue.go is untagged and cannot reference these
// build-tag-gated constants, the same split every other family's own
// scenario const already lives with).
const (
	layer3FiveXXInjectedScenario           = "five-xx-mid-write-injected-unknown-then-recovered"
	layer3InterruptedSubmitRetriedScenario = "interrupted-submit-retried-one-pr"
	layer3ConcurrentWritesScenario         = "concurrent-writes-no-lost-write"
)

// layer3ResultFromErr renders err into this family's Result, step-tagging
// Expected exactly as subfamResultFromErr/illegalfamResultFromErr do. Filed
// under SystemA unconditionally, matching every other Layer-1/Layer-2 row's
// own precedent: which checkout actually drove the failing step is visible
// in Observed's own error text, not in this field.
func layer3ResultFromErr(scenario, step string, err error, expected string) Result {
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

// layer3Fail builds a VerdictFail row for an assertion this family observed
// directly (the CLI ran, but what it did was wrong) — same step-tagging as
// layer3ResultFromErr.
func layer3Fail(scenario, step, observed, expected, detail string) Result {
	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictFail,
		Expected: fmt.Sprintf("[%s] %s", step, expected), Observed: observed, Detail: detail,
	}
}

// layer3InjectedResultFromErr/layer3InjectedFail are layer3ResultFromErr's/
// layer3Fail's own AC-982.1 variants: every Result this row returns —
// PASS or FAIL — carries EvidenceClassInjectedFault, because the row's
// premise (a write driven through injectingProxy) does not stop being true
// just because a leg of it failed. D-G's own condition for allowing this
// row at all is that the report never lets a reader mistake ANY of its
// outcomes for an unscheduled real 5xx (report.go, Result.EvidenceClass's
// own doc comment).
func layer3InjectedResultFromErr(step string, err error, expected string) Result {
	res := layer3ResultFromErr(layer3FiveXXInjectedScenario, step, err, expected)
	res.EvidenceClass = EvidenceClassInjectedFault
	return res
}

func layer3InjectedFail(step, observed, expected, detail string) Result {
	res := layer3Fail(layer3FiveXXInjectedScenario, step, observed, expected, detail)
	res.EvidenceClass = EvidenceClassInjectedFault
	return res
}

// layer3FiveXXInjected drives AC-982.1: a 5xx mid-write, injected via
// injectingProxy (spec 38 §6-Q1, plan D-G), must be reported as UNKNOWN
// with a successful re-read — never as a product failure.
//
// `a2a submit` performs exactly ONE write call through A2A_GITHUB_API — the
// push itself is `git push` straight to github.com, never through this
// seam (internal/host.GitHubHost.PushBranch shells to `git`) — so injecting
// on POST .../pulls (injectOnCreatePR) targets internal/host.GitHubHost.
// OpenPR's create call: the funnel has already committed and pushed the
// branch for real by the time this fires (space/funnel.go step 3, BEFORE
// step 4's OpenPR), so "the write may or may not have landed" (spec 38
// §T2) resolves, here, to "the branch landed for certain; whether the PR
// exists is exactly what this row cannot see and must re-read for".
//
// Recovery is the product's OWN idempotent-retry path (space.WriteFunnel.
// Submit's step 0, FindPRByHeadBranch) — never a harness workaround — run a
// SECOND time with no proxy, proving the real operator's actual recourse
// (retry the same `a2a submit`) works. This row deliberately does NOT wait
// for the recovered PR to merge: the claim AC-982.1 makes is "UNKNOWN, then
// a successful re-read", not "and it eventually merges" — a merge-timing
// race is a different claim (AC-982.3's own row already carries that risk
// honestly), and folding it in here would let an unrelated merge stall
// misreport a row that already proved what it set out to prove.
func layer3FiveXXInjected(ctx context.Context, h *harness) Result {
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return layer3InjectedResultFromErr("sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh draft")
	}

	id, _, err := a.Draft(ctx, "announcement")
	if err != nil {
		return layer3InjectedResultFromErr("draft", err, "draft+fill an announcement locally, before any write")
	}

	proxy, err := newInjectingProxy(h.Seam.APIRoot, injectOnCreatePR, http.StatusGatewayTimeout)
	if err != nil {
		return layer3InjectedResultFromErr("proxy-setup", err, "the fault-injecting proxy starts in front of the real GitHub API")
	}
	defer proxy.Close()

	branch := space.BranchName(a.System, "submit", id)
	stdout, stderr, submitErr := a.RunWithAPIRoot(ctx, proxy.URL(), "submit", id)

	if !proxy.Fired() {
		return layer3InjectedFail("injected-write",
			fmt.Sprintf("the proxy's own OpenPR match never fired — stdout=%q stderr=%q err=%v", stdout, stderr, submitErr),
			"the proxy injects its ONE 5xx on `a2a submit`'s POST .../pulls call — if it never fires, this row proved nothing about AC-982.1", "")
	}
	if submitErr == nil {
		return layer3InjectedFail("injected-write",
			"`a2a submit` exited 0 despite the proxy injecting a 5xx on its OpenPR call",
			"a write whose OpenPR call comes back 5xx must surface to the caller as an error, not a silent success", stdout)
	}
	// TWO shapes are correct here, and which one appears depends on the
	// product's own bounded retry — which did not exist when this row was
	// written.
	//
	// The row used to require the injected 5xx verbatim in stderr, citing
	// "`send` issues OpenPR exactly once, no internal retry". P44 deliberately
	// changed that: a 5xx is now retried, because during GitHub's real outage on
	// 2026-07-24 every `a2a submit` died instantly on a bare `status 500:` and an
	// agent could not tell a platform outage from a refusal.
	//
	// So with the retry in place, this proxy — which FORWARDS the request for
	// real and only then discards the response (see injectingProxy) — has already
	// created the pull request on attempt 1. The retry's second POST therefore
	// hits a duplicate, and the product reports THAT: "refused as a duplicate,
	// which means the FIRST attempt landed". Which is strictly better than the
	// bare 504 this row used to demand, and is the very truth the injected 5xx
	// withheld from the caller.
	//
	// Both are accepted; neither is optional. What must never happen is a
	// failure that names NEITHER — that would be an unrelated error wearing this
	// row's clothes, and the row would pass having proved nothing.
	//
	// Matched against the sentinel's OWN text rather than a copied literal, so
	// rewording the product's message cannot silently un-anchor this assertion.
	// The sentinel's text carries its own "host: " prefix, and (*Error).Error()
	// TRIMS that from the inner message when it renders — so the rendered stderr
	// reads "host: OpenPR: a retry after …", and matching the sentinel's FULL
	// text would find nothing.
	//
	// This is not hypothetical: matching the full text is what this row did for
	// about an hour on 2026-07-26, between the row's fix and the prefix fix that
	// followed it. The run in flight at the time passed because it predated the
	// prefix change; the next one would have failed on an assertion about a
	// product that was working. Trimming here matches either rendering, so the
	// tier cannot be broken again by a change to how the prefix is composed.
	dupMarker := strings.TrimPrefix(host.ErrRetriedWriteWasDuplicate.Error(), "host: ")
	sawInjected := strings.Contains(stderr, injectedFaultMarker)
	sawDuplicate := strings.Contains(stderr, dupMarker)
	if !sawInjected && !sawDuplicate {
		return layer3InjectedFail("injected-write",
			fmt.Sprintf("submit failed, but its stderr names neither this row's injected fault nor the "+
				"retried-write-was-duplicate class: %s", stderr),
			"the CLI's error must carry EITHER the 5xx this row injected, or — since P44's bounded retry "+
				"re-issues it and the proxy forwards for real — the duplicate refusal that says the first "+
				"attempt landed (spec 36 §T6-d, spec 44)", stderr)
	}

	// --- UNKNOWN, never FAIL: everything observed so far is exactly the
	// shape spec 36 §T6-d and this package's own verdictForError treat as
	// "we do not know what happened" for every OTHER 5xx-shaped failure —
	// a raw CLI exec error carries no Go error value to route through that
	// function directly, so this row makes the identical call by hand: the
	// three checks above are this row's OWN "reported as UNKNOWN" leg. ---

	// --- Re-read the REAL GitHub API (h.Prov, never the proxy) to
	// establish the truth the injected 5xx withheld: the proxy genuinely
	// forwarded the request, so the PR the CLI never got to see IS there. ---
	pr, err := h.pullForBranch(ctx, branch)
	if err != nil {
		return layer3InjectedResultFromErr("re-read", err,
			"re-reading the REAL GitHub API (not the proxy) finds the PR the injected 5xx withheld from the CLI — the write landed even though the caller could not see it (spec 36 §T6-d)")
	}

	// --- Recovery: the product's OWN idempotent-retry path (see this
	// function's own doc comment) re-discovers the PR by its deterministic
	// head branch and reports success. ---
	retryStdout, retryStderr, retryErr := a.Run(ctx, "submit", id)
	if retryErr != nil {
		return layer3InjectedResultFromErr("retry-recovers", fmt.Errorf("%w: %s", retryErr, retryStderr),
			"retrying the SAME `a2a submit` (no proxy this time) finds the already-open PR by its deterministic head branch and succeeds (AC-301.1/space.WriteFunnel step 0)")
	}
	if !strings.Contains(retryStdout, "already submitted") {
		return layer3InjectedFail("retry-recovers", retryStdout,
			"the retry's own stdout says `already submitted`, naming the PR the injected 5xx had withheld", retryStdout)
	}

	return Result{
		Scenario: layer3FiveXXInjectedScenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		EvidenceClass: EvidenceClassInjectedFault,
		Detail: fmt.Sprintf("%s: injected %d on PR #%d's OpenPR call (proxy %s); the branch and PR landed for real anyway (spec 36 §T6-d); a plain retry (no proxy) found it via the funnel's own idempotent-retry path and reported %q — deliberately left OPEN, not merged, by this row (see this function's own doc comment on why a merge-timing race is not this row's claim)",
			id, http.StatusGatewayTimeout, pr.Number, proxy.URL(), strings.TrimSpace(retryStdout)),
	}
}

// layer3InterruptedSubmitRetried drives AC-982.2: an interrupted submit,
// retried, produces exactly ONE PR — the funnel's OWN idempotency by head
// branch (space.WriteFunnel.Submit's step 0, FindPRByHeadBranch), driven
// live rather than only in internal/space's unit tests.
//
// The retry runs while the FIRST PR is still OPEN and the mirror has never
// synced it — deliberately. `a2a submit` is ALSO a no-op for an id with
// COMMITTED history (partitionByHistory -> LegalityAdapter.HasCommitted-
// History, internal/cli/adapters.go), but that check reads the local
// MIRROR's own committed events, which only a2a sync ever populates — so a
// retry issued before any sync can only be short-circuited by the funnel's
// branch-level check, never by the history one. Retrying AFTER a merge
// WITHOUT syncing would NOT prove funnel-level idempotency either: step 0's
// own short-circuit only fires for a PR whose State is `!= "merged"`
// (space/funnel.go) — a merged PR falls through to commitOne/push/OpenPR
// again, on a branch with nothing new to push, a different (and
// untested-by-this-row) path this row does not attempt. So THIS row proves
// specifically: retrying `a2a submit` on the SAME staged draft, while its
// PR is still open, is a no-op success that opens no second PR — the
// narrower, precise claim the brief asked this row to name.
func layer3InterruptedSubmitRetried(ctx context.Context, h *harness) Result {
	const scenario = layer3InterruptedSubmitRetriedScenario
	a := h.A

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return layer3ResultFromErr(scenario, "sync-before-draft", fmt.Errorf("%w: %s", err, stderr), "a2a sync succeeds before a fresh draft")
	}

	sub, err := h.DraftAndSubmit(ctx, a, "announcement")
	if err != nil {
		return layer3ResultFromErr(scenario, "first-submit", err, "the first `a2a submit` opens its own PR")
	}

	beforeCount, err := h.countPRsForBranch(ctx, sub.Branch)
	if err != nil {
		return layer3ResultFromErr(scenario, "retry-before-count", err, "can list PRs on "+sub.Branch+" before the retry")
	}
	if beforeCount != 1 {
		return layer3Fail(scenario, "retry-before-count", fmt.Sprintf("%d", beforeCount),
			"exactly one PR exists on "+sub.Branch+" after the first submit, before any retry", "")
	}

	retryStdout, retryStderr, retryErr := a.Run(ctx, "submit", sub.ID)
	if retryErr != nil {
		return layer3ResultFromErr(scenario, "retry-submit", fmt.Errorf("%w: %s", retryErr, retryStderr),
			"retrying `a2a submit` on the SAME staged draft, while its PR is still open and never synced, is a no-op success (AC-301.1's funnel-level idempotency by head branch — see this function's own doc comment on why this is NOT partitionByHistory's local-history check)")
	}
	if !strings.Contains(retryStdout, "already submitted") {
		return layer3Fail(scenario, "retry-submit", retryStdout,
			"the retry's own stdout says `already submitted`, naming the SAME PR the first submit opened", retryStdout)
	}

	afterCount, err := h.countPRsForBranch(ctx, sub.Branch)
	if err != nil {
		return layer3ResultFromErr(scenario, "retry-after-count", err, "can list PRs on "+sub.Branch+" after the retry")
	}
	if afterCount != 1 {
		return layer3Fail(scenario, "retry-after-count", fmt.Sprintf("%d", afterCount),
			"exactly ONE PR exists on "+sub.Branch+" after the retry — the funnel's step 0 (FindPRByHeadBranch) short-circuits before ever pushing or opening a second PR", "")
	}

	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("%s: PR #%d — %d PR on %s before the retry, %d after; retry's own stdout: %q (left OPEN on purpose — merging is orthogonal to this row's own claim)",
			sub.ID, sub.PRNumber, beforeCount, sub.Branch, afterCount, strings.TrimSpace(retryStdout)),
	}
}

// layer3ShowDocument is this file's own minimal decode of `a2a show --json`
// — the same "each family owns its own minimal projection" convention
// subfamShowDoc documents (scenarios_submitted_live.go), extended with
// Title because this row's own claim ("no lost write") is about CONTENT,
// not merely state.
type layer3ShowDocument struct {
	Title string `json:"title"`
	State string `json:"state"`
}

func layer3ShowDoc(ctx context.Context, c *checkout, id string) (layer3ShowDocument, error) {
	stdout, stderr, err := c.Run(ctx, "show", id, "--json")
	if err != nil {
		return layer3ShowDocument{}, fmt.Errorf("livee2e: a2a show %s (%s): %w: %s", id, c.System, err, strings.TrimSpace(stderr))
	}
	var doc layer3ShowDocument
	if jerr := json.Unmarshal([]byte(stdout), &doc); jerr != nil {
		return layer3ShowDocument{}, fmt.Errorf("livee2e: decode `a2a show %s --json` output (%s): %w", id, c.System, jerr)
	}
	return doc, nil
}

// layer3AuthorAndLand drafts+submits+gates+lands ONE artifact in c's own
// section, with title overriding the draft's default fill — the "own
// artifacts in their own sections" premise AC-982.3's row rests its "no
// lost write" claim on.
func layer3AuthorAndLand(ctx context.Context, h *harness, c *checkout, kind, title string) (submitted, error) {
	sub, err := h.DraftAndSubmit(ctx, c, kind, "--field", "title="+title)
	if err != nil {
		return sub, err
	}
	if err := happyLandAndSync(ctx, h, c, sub.PRNumber); err != nil {
		return sub, err
	}
	return sub, nil
}

// layer3ConcurrentWrites drives AC-982.3: two systems writing concurrently
// both land, with no lost write (the P30(a) class at the space level).
//
// Both checkouts author, submit, gate and land their OWN announcement in
// their OWN section — genuinely CONCURRENTLY (two goroutines, not
// sequenced), so their pushes/OpenPR calls/required checks/auto-merges all
// race against real GitHub around the same time. The claim is "no lost
// write", so the assertion is on CONTENT: after both land, BOTH checkouts
// sync and read BACK both artifacts' Title via `a2a show --json` — never
// merely that two PRs existed (the brief's own wording).
//
// If ONE PR's merge stalls (e.g. a required-review/up-to-date interaction
// this rig has not seen before), this row reports that as VerdictTimedOut
// via layer3ResultFromErr, exactly like any other bounded-wait expiry in
// this package — it is never serialized, retried, or rebased into passing:
// spec 38's own rule is "a row that cannot be driven because the product
// will not do the thing is a FINDING", and a stalled second merge under
// real concurrency IS the finding this row exists to surface, not a defect
// in the row.
func layer3ConcurrentWrites(ctx context.Context, h *harness) Result {
	const scenario = layer3ConcurrentWritesScenario
	a, b := h.A, h.B

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return layer3ResultFromErr(scenario, "sync-before-draft-a", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync succeeds before a fresh draft")
	}
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return layer3ResultFromErr(scenario, "sync-before-draft-b", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync succeeds before a fresh draft")
	}

	const titleA = "layer3 concurrent write A"
	const titleB = "layer3 concurrent write B"

	var subA, subB submitted
	var errA, errB error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		subA, errA = layer3AuthorAndLand(ctx, h, a, "announcement", titleA)
	}()
	go func() {
		defer wg.Done()
		subB, errB = layer3AuthorAndLand(ctx, h, b, "announcement", titleB)
	}()
	wg.Wait()

	if errA != nil {
		return layer3ResultFromErr(scenario, "system-a-author-land", errA,
			"A's own announcement authors, submits, gates and lands while B writes concurrently — no lost write (spec 38 §T2 Layer 3, the P30(a) class at the space level)")
	}
	if errB != nil {
		return layer3ResultFromErr(scenario, "system-b-author-land", errB,
			"B's own announcement authors, submits, gates and lands while A writes concurrently — no lost write (spec 38 §T2 Layer 3, the P30(a) class at the space level)")
	}

	if _, stderr, err := a.Run(ctx, "sync"); err != nil {
		return layer3ResultFromErr(scenario, "sync-after-a", fmt.Errorf("%w: %s", err, stderr), "A's a2a sync fetches both landed writes")
	}
	if _, stderr, err := b.Run(ctx, "sync"); err != nil {
		return layer3ResultFromErr(scenario, "sync-after-b", fmt.Errorf("%w: %s", err, stderr), "B's a2a sync fetches both landed writes")
	}

	checks := []struct {
		c     *checkout
		id    string
		want  string
		label string
	}{
		{a, subA.ID, titleA, "A-sees-own-write"},
		{a, subB.ID, titleB, "A-sees-B-write"},
		{b, subA.ID, titleA, "B-sees-A-write"},
		{b, subB.ID, titleB, "B-sees-own-write"},
	}
	for _, chk := range checks {
		doc, err := layer3ShowDoc(ctx, chk.c, chk.id)
		if err != nil {
			return layer3ResultFromErr(scenario, "content-"+chk.label, err,
				fmt.Sprintf("`a2a show %s --json` (%s) succeeds after sync", chk.id, chk.c.System))
		}
		if doc.Title != chk.want {
			return layer3Fail(scenario, "content-"+chk.label, doc.Title,
				fmt.Sprintf("%s's own `a2a show %s` reads back Title %q — a lost write would show stale or empty content instead", chk.c.System, chk.id, chk.want),
				doc.Title)
		}
	}

	return Result{
		Scenario: scenario, System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: fmt.Sprintf("A: %s (PR #%d, title %q); B: %s (PR #%d, title %q) — both landed, both present with correct content on BOTH checkouts after sync",
			subA.ID, subA.PRNumber, titleA, subB.ID, subB.PRNumber, titleB),
	}
}
