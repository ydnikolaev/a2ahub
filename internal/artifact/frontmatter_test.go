package artifact

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrontmatterRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  []byte
	}{
		{
			name: "typical fixture",
			raw:  []byte("---\nschema: envelope/v1\nid: XC-axon-ingest\n---\n# Ingest contract\n\nBody text.\n"),
		},
		{
			name: "empty body",
			raw:  []byte("---\nschema: envelope/v1\n---\n"),
		},
		{
			name: "empty frontmatter block",
			raw:  []byte("---\n---\nBody only.\n"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fm, err := ParseFrontmatter(tc.raw)
			if err != nil {
				t.Fatalf("ParseFrontmatter unexpected error: %v", err)
			}
			got := SerializeFrontmatter(fm)
			if !bytes.Equal(got, tc.raw) {
				t.Fatalf("Serialize(Parse(x)) = %q, want %q", got, tc.raw)
			}
		})
	}
}

func TestFrontmatterCRLF_parseTolerant(t *testing.T) {
	// Interpretation note (reported to the lead): the canonical Serialize
	// layout is hardcoded LF (`---\n<yaml>\n---\n<body>` per spec T1b),
	// so CRLF input is NOT expected to byte-round-trip through Serialize.
	// This test asserts Parse tolerates CRLF delimiter lines and extracts
	// the expected YAML/body content; AC#6's byte-equality gate is
	// exercised on an LF fixture in TestFrontmatterRoundTrip.
	t.Parallel()

	raw := []byte("---\r\nschema: envelope/v1\r\n---\r\nBody line.\r\n")
	fm, err := ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter unexpected error: %v", err)
	}
	wantYAML := []byte("schema: envelope/v1\r\n")
	wantBody := []byte("Body line.\r\n")
	if !bytes.Equal(fm.YAML, wantYAML) {
		t.Fatalf("fm.YAML = %q, want %q", fm.YAML, wantYAML)
	}
	if !bytes.Equal(fm.Body, wantBody) {
		t.Fatalf("fm.Body = %q, want %q", fm.Body, wantBody)
	}
}

func TestParseFrontmatter_missingDelimiters(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  []byte
	}{
		{name: "no leading delimiter", raw: []byte("schema: envelope/v1\n---\nBody.\n")},
		{name: "no closing delimiter", raw: []byte("---\nschema: envelope/v1\nBody.\n")},
		{name: "empty input", raw: []byte("")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseFrontmatter(tc.raw)
			if !errors.Is(err, ErrNoFrontmatter) {
				t.Fatalf("ParseFrontmatter(%q) error = %v, want errors.Is ErrNoFrontmatter", tc.raw, err)
			}
		})
	}
}

func TestParseFrontmatter_malformedYAML(t *testing.T) {
	// Guard added beyond the literal spec (structural-split-only per
	// T1b): the extracted YAML block must itself be syntactically valid
	// YAML. Reported as an interpretation, not a spec requirement.
	t.Parallel()

	raw := []byte("---\nfoo: [1, 2\n---\nBody.\n")
	_, err := ParseFrontmatter(raw)
	if !errors.Is(err, ErrMalformedFrontmatter) {
		t.Fatalf("ParseFrontmatter(malformed yaml) error = %v, want errors.Is ErrMalformedFrontmatter", err)
	}
}
