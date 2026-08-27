package cli

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// TestAttachAppendEntryPreservesEveryOtherField is the regression pin for a
// SHIPPED defect that was invisible for as long as `format` went unasserted.
//
// `attachAppendEntry` used to decode frontmatter into a `map[string]any`.
// gopkg.in/yaml.v3 resolves an unquoted `2026-12-31` to a **time.Time**, so
// marshalling it back wrote `2026-12-31T00:00:00Z` — and the envelope schema
// declares `needed_by` as `format: date`. `a2a attach` therefore silently
// rewrote a field the author never touched into a value that violates its own
// declared format, and nothing refused it because nothing asserted format.
//
// Found by the live matrix the day assertion was switched on
// (no-silent-yes-2026-08 P3): `bytes-round-trip` and
// `bytes-round-trip-corrupted-refused` both went red with
// `SCH-012 needed_by: fails schema validation (format)` at the `a2a submit`
// that followed the attach.
//
// The same round-trip also alphabetically REORDERED the whole frontmatter, so
// every attach rewrote lines nobody asked it to.
func TestAttachAppendEntryPreservesEveryOtherField(t *testing.T) {
	t.Parallel()

	const in = `id: XW-alpha-20260827-aaaa
type: work_request
needed_by: 2026-12-31
valid_until: 2027-01-15
created: 2026-08-27T09:00:00Z
title: keep me first
`
	out, err := attachAppendEntry(
		artifact.Frontmatter{YAML: []byte(in), Body: []byte("body\n")},
		datapackage.Attachment{Ref: "sha256:abc"},
		"",
	)
	if err != nil {
		t.Fatalf("attachAppendEntry: %v", err)
	}
	got := string(out)

	// The bug, pinned by value: a `format: date` field must survive verbatim.
	for _, want := range []string{"needed_by: 2026-12-31\n", "valid_until: 2027-01-15\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("a date field did not survive the attach round-trip.\n"+
				"want the line %q\ngot:\n%s", strings.TrimSpace(want), got)
		}
	}
	if strings.Contains(got, "2026-12-31T00:00:00Z") {
		t.Errorf("`needed_by` was widened from a date to a date-time — the exact "+
			"shipped defect this test exists for. got:\n%s", got)
	}

	// Key order: `id` was first going in and must still be first coming out.
	// The map round-trip sorted alphabetically, putting `created` first.
	idAt, titleAt := strings.Index(got, "id:"), strings.Index(got, "title:")
	if idAt < 0 || titleAt < 0 || idAt > titleAt {
		t.Errorf("attach reordered the frontmatter; `id` must still precede `title`.\ngot:\n%s", got)
	}

	// And it must actually have done its job.
	if !strings.Contains(got, "sha256:abc") {
		t.Errorf("the attachment entry itself is missing.\ngot:\n%s", got)
	}
}
