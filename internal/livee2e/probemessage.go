package livee2e

// probemessage.go holds the human-facing title and explanation for the
// boundary family's deliberately red pull request. The artifact itself is
// authored by the shipped `a2a new` command in the live-tagged driver.

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
