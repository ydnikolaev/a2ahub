package cli

import (
	"strings"
	"testing"
)

// TestNewRefusalRendersAllThreeParts is spec 04 §6's own testing
// requirement: "a refusal renders all three parts".
func TestNewRefusalRendersAllThreeParts(t *testing.T) {
	t.Parallel()
	r, err := NewRefusal("read space.yaml", "no manifest at this cwd", "run `a2a init` first")
	if err != nil {
		t.Fatalf("NewRefusal returned an error for a well-formed call: %v", err)
	}
	if r.Attempted() != "read space.yaml" {
		t.Fatalf("Attempted() = %q, want %q", r.Attempted(), "read space.yaml")
	}
	if r.Found() != "no manifest at this cwd" {
		t.Fatalf("Found() = %q, want %q", r.Found(), "no manifest at this cwd")
	}
	if r.NextStep() != "run `a2a init` first" {
		t.Fatalf("NextStep() = %q, want %q", r.NextStep(), "run `a2a init` first")
	}
	msg := r.Error()
	for _, part := range []string{"read space.yaml", "no manifest at this cwd", "run `a2a init` first"} {
		if !strings.Contains(msg, part) {
			t.Errorf("Error() = %q, missing part %q", msg, part)
		}
	}
}

// TestNewRefusalEmptyNextStepRefused is spec 04 §6's edge case: "a
// next-step that is itself empty must be refused at construction" — the
// arity forces the argument to be PRESENT, but only a runtime check can
// forbid it being the empty string.
func TestNewRefusalEmptyNextStepRefused(t *testing.T) {
	t.Parallel()
	cases := []string{"", "   ", "\t\n"}
	for _, nextStep := range cases {
		if _, err := NewRefusal("attempted", "found", nextStep); err == nil {
			t.Errorf("NewRefusal(_, _, %q) returned no error, want a refusal-at-construction error", nextStep)
		}
	}
}

// TestNewRefusalErrorMessageNamesEmptyNextStep proves the construction
// error itself is informative (it names what was attempted and found),
// rather than a bare "invalid argument".
func TestNewRefusalErrorMessageNamesEmptyNextStep(t *testing.T) {
	t.Parallel()
	_, err := NewRefusal("read space.yaml", "no manifest", "")
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if !strings.Contains(err.Error(), "read space.yaml") || !strings.Contains(err.Error(), "no manifest") {
		t.Errorf("construction error %q does not name attempted/found", err.Error())
	}
}

// TestRefusalImplementsError pins Refusal as usable everywhere an `error`
// is expected (fmt.Fprintln(stdio.Stderr, refusal), errors.As, %w-wrapping).
func TestRefusalImplementsError(t *testing.T) {
	t.Parallel()
	var _ error = Refusal{}
}
