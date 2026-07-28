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
		`gh label create "feedback:${KIND}"`,
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

// TestFeedbackIntakeHasAUniversalReadOnlyMergePolicy protects the P48
// branch-wide required-check design. A path-filtered required check wedges
// unrelated PRs in "Expected", while a write-capable job that reads PR-head
// content crosses the quarantine boundary.
func TestFeedbackIntakeHasAUniversalReadOnlyMergePolicy(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRootForTest(t), ".github", "workflows", "feedback-intake.yml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read feedback intake workflow: %v", err)
	}
	workflow := string(raw)
	for _, required := range []string{
		"pull_request_target:",
		"name: merge-policy",
		"contents: read",
		"pull-requests: read",
		"needs: policy",
		"if: needs.policy.outputs.is-feedback == 'true'",
		"--paginate --slurp",
		"expected exactly one top-level kind field",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("workflow does not contain %q", required)
		}
	}
	if strings.Contains(workflow, "actions/checkout") {
		t.Fatal("pull_request_target workflow must never checkout PR-head content")
	}
	trigger := workflow[strings.Index(workflow, "on:"):strings.Index(workflow, "permissions:")]
	if strings.Contains(trigger, "paths:") {
		t.Fatal("merge-policy must be universal; a path-filtered required check wedges unrelated PRs")
	}
}
