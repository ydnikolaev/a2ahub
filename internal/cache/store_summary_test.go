package cache

import "testing"

// TestBodySummary covers the D-001/D-002 description extraction: first
// paragraph of an artifact body, whitespace-collapsed, marker-trimmed, capped.
func TestBodySummary(t *testing.T) {
	t.Parallel()
	const fm = "---\nschema: envelope/v1\nid: XQ-axon-x\n---\n"
	tests := []struct {
		name, body, want string
		max              int
	}{
		{"first paragraph only", "First line here.\nsame paragraph.\n\nSecond paragraph ignored.", "First line here. same paragraph.", 240},
		{"collapses whitespace", "a   b\tc\n d", "a b c d", 240},
		{"strips leading heading marker", "# Ingest contract\n\nrest", "Ingest contract", 240},
		{"strips multi-# heading", "### Deep heading\n\nrest", "Deep heading", 240},
		{"strips leading list marker", "- an item note", "an item note", 240},
		{"keeps a negative-number sign", "-50°C is the floor", "-50°C is the floor", 240},
		{"keeps bold prose (not a bullet)", "**bold** intro", "**bold** intro", 240},
		{"keeps a bare #hashtag", "#tag not a heading", "#tag not a heading", 240},
		{"CRLF paragraph split", "First para.\r\n\r\nSecond ignored.", "First para.", 240},
		{"empty body", "", "", 240},
		{"blank-only body", "   \n\n  \n", "", 240},
		{"caps long body with ellipsis", "abcdefghij", "abcd…", 5},
		{"max below one is clamped", "abc", "…", 0},
	}
	for _, tt := range tests {
		got := bodySummary([]byte(fm+tt.body), tt.max)
		if got != tt.want {
			t.Errorf("%s: bodySummary = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestBodySummary_Unparseable: a file without frontmatter degrades to "" rather
// than erroring the caller (inbox/outbox must never fail on one odd artifact).
func TestBodySummary_Unparseable(t *testing.T) {
	t.Parallel()
	if got := bodySummary([]byte("no frontmatter here"), 240); got != "" {
		t.Errorf("bodySummary(no frontmatter) = %q, want \"\"", got)
	}
}
