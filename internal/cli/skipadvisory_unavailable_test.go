package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// skipadvisory_unavailable_test.go covers computed-not-listed-2026-08 P6
// AC-8 / §8 row 8: a read verb whose skipped-file report could not itself be
// COMPUTED must say so, rather than dropping two facts at once — that the
// output might be missing rows, and that nothing could find out which.
//
// It lives in `package cli` and drives skipAdvisoryUnavailable directly,
// for the reason actorrefusal_test.go's own header gives about a different
// helper: the alternative is fabricating a store whose AllSkippedFiles
// fails, and a test that can only be written by breaking the world tends to
// end up skipped — which reads as coverage on the only machine anyone runs
// it on.
//
// The behaviour shipped with NO test at all. That is the epic's own second
// thesis (a fact an agent must act on is asserted where a machine checks it)
// missing from the phase that carries it, and this file is the correction.

func TestSkipAdvisoryUnavailableNamesTheReasonAndTheNextStep(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	stdio := IO{Stdout: &stdout, Stderr: &stderr}

	skipAdvisoryUnavailable(stdio, "inbox", errors.New("mirror index unreadable"), false)

	// STDOUT MUST STAY EMPTY. skipAdvisory's own contract is that an
	// existing consumer's stdout bytes are byte-identical whether or not an
	// advisory fires; this half inherits it, and a note that reached stdout
	// would corrupt every `a2a inbox | ...` pipeline in existence.
	if stdout.Len() != 0 {
		t.Fatalf("the advisory must never touch stdout, got %q", stdout.String())
	}

	got := stderr.String()
	// All three of NewRefusal's parts, because a symptom with no action is
	// the exact defect spec 04 exists to close — and it is what the four
	// per-verb copies this helper replaced actually shipped.
	for _, want := range []string{
		"inbox",
		"could not determine which files, if any, were skipped",
		"mirror index unreadable",
		skipAdvisoryNextStep,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("stderr note is missing %q, got %q", want, got)
		}
	}
}

func TestSkipAdvisoryUnavailableEmitsJSONInJSONMode(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	stdio := IO{Stdout: &stdout, Stderr: &stderr}

	skipAdvisoryUnavailable(stdio, "search", errors.New("boom"), true)

	if stdout.Len() != 0 {
		t.Fatalf("the advisory must never touch stdout, got %q", stdout.String())
	}
	// A --json consumer parses this channel already, because skipAdvisory
	// itself emits one JSON line here in the same mode. Prose in the middle
	// of that stream is a decoder error at the caller, so the shape is
	// asserted rather than assumed.
	var payload struct {
		SkippedUnavailable string `json:"skipped_unavailable"`
		Reason             string `json:"reason"`
		NextStep           string `json:"next_step"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr.String())), &payload); err != nil {
		t.Fatalf("--json mode must emit one JSON object on stderr, got %q (%v)", stderr.String(), err)
	}
	if payload.SkippedUnavailable != "search" || payload.Reason != "boom" {
		t.Fatalf("payload lost the verb or the reason: %+v", payload)
	}
	if payload.NextStep != skipAdvisoryNextStep {
		t.Fatalf("payload's next step drifted from the constant: %q", payload.NextStep)
	}
}

// TestSkipAdvisoryNextStepIsNeverEmpty pins the one thing that would make
// NewRefusal refuse to build this note at all — at which point the helper
// falls back to a fixed internal-problem line and the reader loses both the
// reason and the action. The constant is shared by the prose and the JSON
// path, so one empty value would silently degrade both.
func TestSkipAdvisoryNextStepIsNeverEmpty(t *testing.T) {
	t.Parallel()
	if strings.TrimSpace(skipAdvisoryNextStep) == "" {
		t.Fatal("skipAdvisoryNextStep is empty — NewRefusal would refuse to build the note")
	}
}
