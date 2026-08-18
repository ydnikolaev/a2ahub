package spacenotify

import (
	"strings"
	"testing"
)

// TestBoundAndRedact_CredentialShapeIsGone is AC8/AC17: the shape is gone
// entirely and the FACT of redaction is carried as a structured code, never
// a baked-in English sentence (04-render-and-transport.md TASK1's repair) —
// checked against a real closed shape internal/sensitive matches (an AWS
// access key id), never a hand-picked pattern of this package's own
// invention.
func TestBoundAndRedact_CredentialShapeIsGone(t *testing.T) {
	t.Parallel()
	body := []byte("please rotate this key: AKIAABCDEFGHIJKLMNOP before Friday")
	got, truncated := boundAndRedact(body)
	if strings.Contains(got, "AKIAABCDEFGHIJKLMNOP") {
		t.Fatalf("description = %q, want the credential shape gone entirely", got)
	}
	if got != "" {
		t.Fatalf("description = %q, want empty on redaction — the sentence is the renderer's job, not a baked-in marker", got)
	}
	if !hasTruncationCode(truncated, TruncationDescriptionRedacted) {
		t.Fatalf("Truncated = %v, want %q recorded", truncated, TruncationDescriptionRedacted)
	}
}

func hasTruncationCode(reasons []TruncationReason, code TruncationCode) bool {
	for _, r := range reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestBoundAndRedact_SafeTextIsUnchanged(t *testing.T) {
	t.Parallel()
	body := []byte("please paginate the feed before Friday")
	got, truncated := boundAndRedact(body)
	if got != "please paginate the feed before Friday" {
		t.Fatalf("description = %q, want the body verbatim (trimmed)", got)
	}
	if len(truncated) != 0 {
		t.Fatalf("Truncated = %v, want none for a short safe body", truncated)
	}
}

func TestBoundAndRedact_LongBodyIsBounded(t *testing.T) {
	t.Parallel()
	body := []byte(strings.Repeat("a", maxDescriptionRunes+250))
	got, truncated := boundAndRedact(body)
	if len([]rune(got)) != maxDescriptionRunes {
		t.Fatalf("len(description) = %d, want exactly %d", len([]rune(got)), maxDescriptionRunes)
	}
	if !hasTruncationCode(truncated, TruncationDescriptionBounded) {
		t.Fatalf("Truncated = %v, want %q recorded", truncated, TruncationDescriptionBounded)
	}
	for _, r := range truncated {
		if r.Code == TruncationDescriptionBounded && r.Bound != maxDescriptionRunes {
			t.Fatalf("Truncated bound = %d, want %d", r.Bound, maxDescriptionRunes)
		}
	}
}
