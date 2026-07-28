package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFeedbackIntakeCreatesLabelsBeforeApplyingThem guards the fresh-hub
// failure reported in fb-20260728-d33e33. The workflow may assume no
// repository labels exist: it must own both the issues permission and the
// idempotent creation step before `gh pr edit --add-label`.
func TestFeedbackIntakeCreatesLabelsBeforeApplyingThem(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRootForTest(t), ".github", "workflows", "feedback-intake.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read feedback intake workflow: %v", err)
	}
	workflow := string(raw)
	for _, required := range []string{
		"issues: write",
		`gh label create "feedback"`,
		`gh label create "feedback:${kind}"`,
		`gh pr edit "${PR_NUMBER}"`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("workflow does not contain %q", required)
		}
	}
	create := strings.Index(workflow, `gh label create "feedback"`)
	apply := strings.Index(workflow, `gh pr edit "${PR_NUMBER}"`)
	if create < 0 || apply < 0 || create > apply {
		t.Fatalf("workflow applies labels before ensuring them")
	}
}
