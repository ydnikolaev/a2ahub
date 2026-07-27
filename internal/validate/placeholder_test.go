package validate

import (
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// TestCheckUnfilledPlaceholders_NestedInMapping is A2's headline case: a
// placeholder nested inside a mapping (`expected_response.shape`) is
// caught, with a DOTTED Path — the same vocabulary A1's `--field` now
// accepts.
func TestCheckUnfilledPlaceholders_NestedInMapping(t *testing.T) {
	t.Parallel()

	instance := map[string]any{
		"title": "A real title",
		"expected_response": map[string]any{
			"shape": "<what a good answer looks like>",
		},
	}
	got := checkUnfilledPlaceholders(instance)
	if len(got) != 1 {
		t.Fatalf("got %d violations, want 1: %+v", len(got), got)
	}
	v := got[0]
	if v.Code != "POL-010" {
		t.Errorf("Code = %q, want POL-010", v.Code)
	}
	if v.Class != ClassPolicy {
		t.Errorf("Class = %q, want %q", v.Class, ClassPolicy)
	}
	if v.Path != "expected_response.shape" {
		t.Errorf("Path = %q, want %q", v.Path, "expected_response.shape")
	}
	if v.Severity != SeverityReject {
		t.Errorf("Severity = %q, want %q (POL-002/POL-003's 'cannot admit' severity)", v.Severity, SeverityReject)
	}
}

// TestCheckUnfilledPlaceholders_NestedInSequence is the sequence-element
// half: a placeholder nested inside a sequence element (`refs[].note`) is
// caught, with an index-segmented dotted Path.
//
// This deliberately uses `note` (question.md's own commented example:
// `note: "<what this points at>"`), not `ref`: the template's `ref` value
// is `<id>#<digest>` — TWO bracketed spans in one string — which the
// mandated `^<[^<>]*>$` grammar does NOT catch (an inner `<` after the
// first `>` breaks the fully-anchored, no-nested-brackets match); see
// `deviations` for that named gap. `note`'s placeholder is a single
// bracketed span, so it is exactly the shape this test is for.
func TestCheckUnfilledPlaceholders_NestedInSequence(t *testing.T) {
	t.Parallel()

	instance := map[string]any{
		"refs": []any{
			map[string]any{"ref": "XC-axon-ingest#deadbeef", "note": "<what this points at>"},
		},
	}
	got := checkUnfilledPlaceholders(instance)
	if !hasPathCode(got, "refs.0.note", "POL-010") {
		t.Fatalf("expected a POL-010 violation at path %q, got %+v", "refs.0.note", got)
	}
}

// TestCheckUnfilledPlaceholders_ScriptTagTitleNotTripped is the deliberate
// narrowness guard: internal/e2e/testdata/sanitization/script-tag.md's
// title `<script>alert(1)</script> raw <b>HTML</b> && "quotes"` legitimately
// carries angle brackets (real sanitization-corpus content, not a
// placeholder) and must NOT trip POL-010.
func TestCheckUnfilledPlaceholders_ScriptTagTitleNotTripped(t *testing.T) {
	t.Parallel()

	instance := map[string]any{
		"title": `<script>alert(1)</script> raw <b>HTML</b> && "quotes"`,
	}
	got := checkUnfilledPlaceholders(instance)
	if len(got) != 0 {
		t.Fatalf("script-tag.md's own title must not trip POL-010, got %+v", got)
	}
}

// TestCheckUnfilledPlaceholders_FilledValueNotTripped is the negative
// control: an ordinary filled value never trips the check.
func TestCheckUnfilledPlaceholders_FilledValueNotTripped(t *testing.T) {
	t.Parallel()

	instance := map[string]any{
		"space":             "getvisa",
		"from":              "axon",
		"expected_response": map[string]any{"shape": "a yes/no answer with rationale"},
		"blocking":          true,
		"priority":          "p3",
		"category":          "clarification",
	}
	got := checkUnfilledPlaceholders(instance)
	if len(got) != 0 {
		t.Fatalf("expected no violations for fully filled values, got %+v", got)
	}
}

func hasPathCode(vs []Violation, path, code string) bool {
	for _, v := range vs {
		if v.Path == path && v.Code == code {
			return true
		}
	}
	return false
}

// TestValidateForSubmit_RendersButUnfilledQuestionTemplateIsRefused is A2's
// end-to-end case: `a2a new question` (internal/template.Render) followed
// by `a2a submit` (Engine.ValidateForSubmit) refuses a question draft
// whose `expected_response.shape` was left as the template's own
// placeholder — the exact defect filed 2026-07-27, verified on the
// released 0.10.0 binary.
//
// The companion half (this same test) is the AC-401.1 guard: the SAME
// rendered bytes, run through Engine.ValidateDraft (V1, `a2a validate`),
// must stay CLEAN — POL-010 is V2-only. Without this half, "fixing" A2 by
// running the placeholder check at V1 too would silently break a shipped
// acceptance criterion (a drafted-but-not-yet-filled artifact must still
// be inspectable via `a2a validate`).
func TestValidateForSubmit_RendersButUnfilledQuestionTemplateIsRefused(t *testing.T) {
	t.Parallel()

	raw, err := template.Render(template.Input{
		Type:    "question",
		ID:      "XQ-axon-20260727-p1ac",
		Actor:   template.Actor{Kind: "agent", Name: "test-bot", Model: "test-model"},
		Created: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		// thread is the one field template.Render cannot leave as a
		// placeholder-only fill (see template package's own doc comment
		// on TestRenderEveryTypeSchemaValid) — every OTHER field,
		// including expected_response.shape, is left exactly as the
		// canonical template author wrote it.
		Fields: map[string]string{"thread": "thread:axon-20260727-p1ac"},
	})
	if err != nil {
		t.Fatalf("template.Render: %v", err)
	}

	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	if !containsBytes(fm.YAML, []byte("<what a good answer looks like>")) {
		t.Fatalf("test fixture assumption broken: rendered draft no longer carries the unfilled expected_response.shape placeholder:\n%s", raw)
	}

	engine := mustEngine(t)
	draft := Draft{Path: "axon/exchanges/XQ-axon-20260727-p1ac.md", Raw: raw}

	v1, err := engine.ValidateDraft(draft)
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	if !v1.Valid || len(v1.Violations) != 0 {
		t.Fatalf("AC-401.1 broken: V1 (`a2a validate`) must stay clean on a placeholder-only fill, got %+v", v1)
	}

	v2, err := engine.ValidateForSubmit(draft, nil, LocalContext{OwnSystem: "axon"})
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if v2.Valid {
		t.Fatal("expected V2 (`a2a submit`) to refuse a draft whose expected_response.shape is still the template's own placeholder")
	}
	if !hasPathCode(v2.Violations, "expected_response.shape", "POL-010") {
		t.Fatalf("expected a POL-010 violation at path %q among V2's violations, got %+v", "expected_response.shape", v2.Violations)
	}
}

func containsBytes(haystack, needle []byte) bool {
	return len(needle) > 0 && indexOfBytes(haystack, needle) >= 0
}

func indexOfBytes(haystack, needle []byte) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
