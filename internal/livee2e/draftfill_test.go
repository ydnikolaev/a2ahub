package livee2e

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func stdDC() DraftContext {
	return DraftContext{Space: "livee2e", Me: "alpha", Peer: "bravo"}
}

// TestFillDraftSubstitutions covers one table case per fill.py substitution,
// checking each pattern fires on its own line in isolation.
func TestFillDraftSubstitutions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
		dc   DraftContext
		want string
	}{
		{
			"space id",
			"space: <space-id>          # the space this draft targets",
			stdDC(),
			"space: livee2e",
		},
		{
			"to recipient",
			"to: [<system-id>]          # who receives this",
			stdDC(),
			"to: [bravo]",
		},
		{
			"actor agent",
			"actor: {kind: agent, name: '<system-id>'}",
			stdDC(),
			"actor: {kind: agent, name: 'live-e2e'}",
		},
		{
			"actor human",
			"actor: {kind: human, name: '<your name>'}",
			stdDC(),
			"actor: {kind: human, name: 'live-e2e-operator'}",
		},
		{
			"category",
			"category: <one of: a, b, c>",
			stdDC(),
			"category: other",
		},
		{
			"interim_behavior",
			"interim_behavior: <what happens while this is open>",
			stdDC(),
			`interim_behavior: "we proceed with the current shape"`,
		},
		{
			"needed_by",
			"needed_by: <date>",
			stdDC(),
			"needed_by: 2026-12-31",
		},
		{
			"context angle-bracket form",
			"context: <why this question exists>",
			stdDC(),
			`context: "the live matrix needs this artifact"`,
		},
		{
			"context quoted form",
			`context: "<why this question exists>"`,
			stdDC(),
			`context: "the live matrix needs this artifact"`,
		},
		{
			"options_considered",
			"options_considered: [<option a>, <option b>]",
			stdDC(),
			`options_considered: ["option A", "option B"]`,
		},
		{
			"required_approvers",
			"required_approvers: [<system a>, <system b>]",
			stdDC(),
			"required_approvers: [alpha, bravo]",
		},
		{
			"verification",
			"verification: <how it was checked>",
			stdDC(),
			`verification: "the matrix re-ran the suite; all green"`,
		},
		{
			"acceptance_criteria inline form",
			`acceptance_criteria: ["<measurable AC 1>"]`,
			stdDC(),
			`acceptance_criteria: ["the artifact validates"]`,
		},
		{
			"fulfills default",
			"fulfills: [<requirement id>]",
			stdDC(),
			"fulfills: [XR-alpha-matrix-req]",
		},
		{
			"fulfills override",
			"fulfills: [<requirement id>]",
			DraftContext{Space: "livee2e", Me: "alpha", Peer: "bravo", Extra: map[string]string{"fulfills": "XR-custom"}},
			"fulfills: [XR-custom]",
		},
		{
			"parent default is an explicit YAML null (trailing space, ported as-is from fill.py)",
			"parent: <parent id or empty>",
			stdDC(),
			"parent: ",
		},
		{
			"parent override",
			"parent: <parent id or empty>",
			DraftContext{Space: "livee2e", Me: "alpha", Peer: "bravo", Extra: map[string]string{"parent": "XR-parent-1"}},
			"parent: XR-parent-1",
		},
		{
			"result",
			"result: <answered|open>",
			stdDC(),
			"result: answered",
		},
		{
			"version",
			"version: <semver>",
			stdDC(),
			"version: 1.0.0",
		},
		{
			"status",
			"status: <draft|active|deprecated>",
			stdDC(),
			"status: active",
		},
		{
			"block-valued acceptance criterion",
			`  - "<measurable AC 1>"`,
			stdDC(),
			`  - "the artifact validates"`,
		},
		{
			"block-valued shape",
			`  shape: "<a short prose answer>"`,
			stdDC(),
			`  shape: "a short prose answer"`,
		},
		{
			"refs list with only a placeholder becomes an explicit empty list",
			"refs:\n  - {ref: \"<some ref>\", kind: doc}\n",
			stdDC(),
			"refs: []\n",
		},
		{
			"refs artifact placeholder",
			`  - {name: "<artifact name>", ref: "<artifact ref>", kind: doc}`,
			stdDC(),
			`  - {name: "matrix artifact", ref: "live-e2e/matrix@1.0.0", kind: doc}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _ := FillDraft(tc.src, tc.dc)
			got = strings.TrimRight(got, "\n")
			want := strings.TrimRight(tc.want, "\n")
			if got != want {
				t.Fatalf("FillDraft(%q) = %q, want %q", tc.src, got, want)
			}
		})
	}
}

// TestFillDraftReplacementIsLiteral proves substitution values are inserted
// literally, never interpreted as a Go regexp replacement template — a
// caller-supplied peer/space value containing "$1" or "${x}" must survive
// unchanged.
func TestFillDraftReplacementIsLiteral(t *testing.T) {
	t.Parallel()
	dc := DraftContext{Space: "livee2e", Me: "alpha", Peer: "bravo$1${oops}"}
	got, _ := FillDraft("to: [<system-id>]", dc)
	want := "to: [bravo$1${oops}]"
	if got != want {
		t.Fatalf("FillDraft literal replacement: got %q, want %q", got, want)
	}
}

// TestFillDraftOrderMattersForContext proves the two context: patterns are
// applied in fill.py's declared order, not as an unordered set — the
// angle-bracket pattern must not consume what only the quoted pattern should
// match, and vice versa, when both appear in one draft.
func TestFillDraftOrderMattersForContext(t *testing.T) {
	t.Parallel()
	src := "context: <why>\ncontext: \"<why>\"\n"
	got, _ := FillDraft(src, stdDC())
	wantLine := `context: "the live matrix needs this artifact"`
	for _, ln := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if ln != wantLine {
			t.Fatalf("FillDraft context ordering: line %q, want %q", ln, wantLine)
		}
	}
}

// TestFillDraftUnfilledDetection covers the three cases the report is meant
// to distinguish: an active line still holding a placeholder (reported), a
// fully commented-out line (not reported), and a placeholder that only
// appears inside a trailing comment on an otherwise-filled active line (not
// reported — that is prose, not a field).
func TestFillDraftUnfilledDetection(t *testing.T) {
	t.Parallel()

	src := strings.Join([]string{
		"---",
		"schema: doc/v1",
		"still_placeholder: <not filled>",
		"# commented_placeholder: <also not filled>",
		`filled_field: "value"  # example: <not a field, just prose>`,
		"---",
		"body text follows, not frontmatter: <ignored>",
	}, "\n")

	_, unfilled := FillDraft(src, stdDC())

	if len(unfilled) != 1 {
		t.Fatalf("unfilled = %v, want exactly one entry", unfilled)
	}
	if !strings.Contains(unfilled[0], "still_placeholder") {
		t.Fatalf("unfilled[0] = %q, want the still_placeholder line", unfilled[0])
	}
	for _, u := range unfilled {
		if strings.Contains(u, "commented_placeholder") {
			t.Fatalf("a commented-out line must not be reported: %v", unfilled)
		}
		if strings.Contains(u, "not a field, just prose") {
			t.Fatalf("a placeholder inside a trailing comment must not be reported: %v", unfilled)
		}
		if strings.Contains(u, "ignored") {
			t.Fatalf("a placeholder outside frontmatter must not be reported: %v", unfilled)
		}
	}
}

// TestFillDraftUnfilledEmptyWhenAllFilled proves a fully-substituted draft
// reports no unfilled lines at all.
func TestFillDraftUnfilledEmptyWhenAllFilled(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"---",
		"schema: doc/v1",
		"space: <space-id>",
		"---",
		"body",
	}, "\n")
	_, unfilled := FillDraft(src, stdDC())
	if len(unfilled) != 0 {
		t.Fatalf("unfilled = %v, want none", unfilled)
	}
}

// TestFillDraftUnfilledTruncatesByRune proves the 110-char cap slices on
// rune boundaries, not bytes — a multi-byte rune sitting at the cut point
// must not produce invalid UTF-8.
func TestFillDraftUnfilledTruncatesByRune(t *testing.T) {
	t.Parallel()
	// Build a line whose byte-110 offset lands inside a multi-byte rune
	// ("§" is 2 bytes in UTF-8) if truncation were byte-based.
	long := "still_open: <" + strings.Repeat("a", 95) + "§§§§§§§§§§§§§§§§§§§§>"
	src := "---\n" + long + "\n---\n"
	_, unfilled := FillDraft(src, stdDC())
	if len(unfilled) != 1 {
		t.Fatalf("unfilled = %v, want exactly one entry", unfilled)
	}
	if !utf8.ValidString(unfilled[0]) {
		t.Fatalf("unfilled[0] is not valid UTF-8: %q", unfilled[0])
	}
	if len([]rune(unfilled[0])) > 110 {
		t.Fatalf("unfilled[0] has %d runes, want at most 110", len([]rune(unfilled[0])))
	}
}
