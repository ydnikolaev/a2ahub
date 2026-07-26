package livee2e

// probeenvelope.go holds the boundary family's probe artifact renderer.
//
// It lives in an UNTAGGED file on purpose. Every scenario file in this
// package is behind `//go:build livee2e`, which means `make check` cannot
// see any of it — and that is precisely how this function shipped for weeks
// rendering an artifact with NO frontmatter delimiter. Nothing hermetic
// could look at it. A pure string renderer has no reason to hide behind a
// build tag, and out here its own test runs on every commit.

import "time"

// boundaryProbeTitle is what the operator actually sees. GitHub renders a
// pull request's title as the workflow run's displayTitle, so THIS string is
// the line in the space's Actions tab and in its failure mail beside a red
// cross — the one place a red run this tier causes on purpose meets a human.
//
// It used to read "probe xsec". Nothing about that says the red is intended,
// so on 2026-07-26 the operator was reading failure notifications for a row
// that was passing, and the only way to learn otherwise was to go read this
// package. A deliberate failure that cannot be recognised as deliberate is
// indistinguishable from a defect, and the cost is not confusion once — it is
// an operator who learns to ignore red mail from this repository, which is
// the same repository that must be able to tell them about a real one.
//
// Kept to one line, front-loaded: the Actions list truncates, so the words
// that matter go first.
const boundaryProbeTitle = "EXPECTED RED — live-e2e cross-section probe (the red check IS the assertion)"

// boundaryProbeBody explains the probe to whoever clicked through from the red
// run. It names the scenario, what the red proves, and — the question a
// reader actually has — that nothing is wrong and nothing needs doing.
func boundaryProbeBody(org, repo string) string {
	return "**This pull request is supposed to fail its check. A green check here would be the bug.**\n\n" +
		"It is opened by the a2ahub live-e2e matrix, by the row\n" +
		"`cross-section-retrigger-stays-red` (internal/livee2e/scenarios_boundary_live.go).\n\n" +
		"What it proves: system `bravo` pushes a file into system `alpha`'s section, and the\n" +
		"space's validation gate must REFUSE it — and must keep refusing after the\n" +
		"provisioner re-triggers the check both ways available to them (the re-run API and\n" +
		"close/reopen). That is the regression net for the v0.6.4 bypass, where re-running a\n" +
		"check under a privileged actor turned another login's illegal write green.\n\n" +
		"It is pushed with raw git on purpose: the `a2a` write funnel refuses a cross-section\n" +
		"write client-side, so the only way to ask GitHub the question is to go around the\n" +
		"tool.\n\n" +
		"Nothing to do here. The branch is deleted when the row finishes, which closes this\n" +
		"pull request; the matrix accounts for the run ids it reddened, so a red run in\n" +
		"`" + org + "/" + repo + "` that is NOT one of these is a real finding and is reported as one.\n"
}

// The OPENING `---` is not cosmetic and its absence was a live defect. An
// artifact whose frontmatter has no opening delimiter parses as no
// frontmatter at all, so this probe used to render a file that fails POL-002
// ("artifact frontmatter is missing or is not valid YAML"). Two consequences,
// both found on 2026-07-26 from the space's own failure notifications rather
// than from any assertion in this tier:
//
//   - executed-ref-not-stale pushes this file straight to main, so every
//     later post-merge full-repo audit on that space failed. Nothing in the
//     matrix asserts that job (it is flag-only and never a required check by
//     design), so the tier stayed green while the space's CI was red.
//   - cross-section-retrigger-stays-red asserts only that the required check
//     is `failure`. It was — but for TWO reasons, POL-002 and the
//     section-authz violation the row exists to prove. Both fired, so the row
//     was not vacuous; it was, however, unable to tell the difference, which
//     means diff-authz could have regressed silently.
func boundaryProbeEnvelope(id string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: announcement\n" +
		"title: xsec\n" +
		"space: livee2e\n" +
		"from: alpha\n" +
		"to: [bravo]\n" +
		"actor: {kind: agent, name: probe}\n" +
		"created: " + time.Now().UTC().Format(time.RFC3339) + "\n" +
		"category: notice\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"---\n" +
		"body\n"
}
