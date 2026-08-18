package spacenotify

import (
	"strings"
	"testing"
)

// TestBoundAndRedact_CredentialShapeIsGone is AC8/AC17: the redaction
// marker is visible, the shape is not — checked against a real closed
// shape internal/sensitive matches (an AWS access key id), never a
// hand-picked pattern of this package's own invention.
func TestBoundAndRedact_CredentialShapeIsGone(t *testing.T) {
	t.Parallel()
	body := []byte("please rotate this key: AKIAABCDEFGHIJKLMNOP before Friday")
	got, truncated := boundAndRedact(body)
	if strings.Contains(got, "AKIAABCDEFGHIJKLMNOP") {
		t.Fatalf("description = %q, want the credential shape gone entirely", got)
	}
	if got != redactedMarker {
		t.Fatalf("description = %q, want the fixed marker %q", got, redactedMarker)
	}
	if len(truncated) == 0 {
		t.Fatalf("want Truncated to record the redaction")
	}
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
	if len(truncated) == 0 {
		t.Fatalf("want Truncated to record the bound")
	}
}
