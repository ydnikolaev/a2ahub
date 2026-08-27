package schema

import "testing"

// TestPlaceholderIsNotAFormatViolation pins the boundary
// dropPlaceholderFormatViolations draws: a still-templated `<...>` value must
// NOT surface as a `format` failure, while a real non-conforming value must.
//
// Written because the alternative fix — commenting `needed_by` out of the two
// shipped templates that carry it — passed every test that existed at the
// time and silently DELETED a real POL-010 refusal. Only
// TestPOL010ReachesEveryPlaceholderInTheShippedTemplates caught that. This is
// the other half of the guard, and it lives beside the rule rather than in
// internal/validate because TWO packages consume the rule (ADR-019).
func TestPlaceholderIsNotAFormatViolation(t *testing.T) {
	t.Parallel()

	instance := map[string]any{
		"needed_by":   "<YYYY-MM-DD>",
		"valid_until": "next tuesday",
		"expected_response": map[string]any{
			"by": "<YYYY-MM-DD>",
		},
	}
	in := []FieldViolation{
		{Path: "needed_by", Keyword: "format"},
		{Path: "valid_until", Keyword: "format"},
		{Path: "needed_by", Keyword: "type"},
		{Path: "expected_response.by", Keyword: "format"},
		{Path: "absent_field", Keyword: "format"},
	}

	got := dropPlaceholderFormatViolations(in, instance)

	has := func(path, keyword string) bool {
		for _, fv := range got {
			if fv.Path == path && fv.Keyword == keyword {
				return true
			}
		}
		return false
	}

	if has("needed_by", "format") {
		t.Errorf("a placeholder-shaped needed_by still produced a format violation; "+
			"POL-010 owns it at V2 and a violation here breaks `a2a new`. got=%+v", got)
	}
	if has("expected_response.by", "format") {
		t.Errorf("a NESTED placeholder must be resolved through the dot-path too; got=%+v", got)
	}
	if !has("valid_until", "format") {
		t.Errorf(`"next tuesday" is not placeholder-shaped and must still fail format; got=%+v`, got)
	}
	if !has("needed_by", "type") {
		t.Errorf("only the `format` keyword is dropped — another keyword on the same path must survive; got=%+v", got)
	}
	if !has("absent_field", "format") {
		t.Errorf("a path that does not resolve must FAIL CLOSED (violation kept), never suppress; got=%+v", got)
	}
}
