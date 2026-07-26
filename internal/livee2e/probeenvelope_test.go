package livee2e

import (
	"strings"
	"testing"
)

// The probe ARTIFACT's own guard is
// TestBoundaryProbeEnvelopeIsParseableFrontmatter in scaffold_test.go — it
// parses the frontmatter as YAML and checks every required field. This file
// covers the other half of the same probe: what it says to the human who sees
// its red check.

// TestBoundaryProbeSaysItIsExpectedToBeRed pins the operator-facing surface of
// a deliberately failing check.
//
// GitHub renders a pull request's title as the workflow run's displayTitle, so
// this string is what appears in the space's Actions tab and in its failure
// mail beside a red cross. When it read "probe xsec", the operator was reading
// failure notifications for a row that was passing, and the only way to learn
// otherwise was to read this package.
//
// Pinned in a test rather than left to a code comment because the audience is
// not a developer editing this file — it is somebody looking at a red cross in
// a browser, who will never see the comment. Shortening the title to something
// tidier is the natural change that would silently undo the fix.
func TestBoundaryProbeSaysItIsExpectedToBeRed(t *testing.T) {
	t.Parallel()

	upper := strings.ToUpper(boundaryProbeTitle)
	if !strings.Contains(upper, "EXPECTED") || !strings.Contains(upper, "RED") {
		t.Errorf("the probe PR title must say, in the Actions list itself, that the red is expected — "+
			"got %q. A deliberate failure that cannot be recognised as deliberate is "+
			"indistinguishable from a defect, and teaches the operator to ignore red mail from the "+
			"one repository that must be able to warn them", boundaryProbeTitle)
	}
	if len(boundaryProbeTitle) > 90 {
		t.Errorf("title is %d chars — the Actions list truncates, so the words that matter must come "+
			"first and the whole thing must stay readable there: %q",
			len(boundaryProbeTitle), boundaryProbeTitle)
	}

	body := boundaryProbeBody("a2ahub-live-e2e", "live-e2e-space")
	for _, want := range []struct{ substr, why string }{
		{"supposed to fail", "the first line must answer the reader's actual question"},
		{"cross-section-retrigger-stays-red", "name the row, so the claim is checkable in the source"},
		{"scenarios_boundary_live.go", "name the file, so the reader can get there without grepping"},
		{"a2ahub-live-e2e/live-e2e-space", "name the space, so a reader knows which repo this is about"},
		{"Nothing to do here", "say plainly that no action is needed — that is the decision the reader came to make"},
	} {
		if !strings.Contains(body, want.substr) {
			t.Errorf("probe body is missing %q — %s", want.substr, want.why)
		}
	}
}
