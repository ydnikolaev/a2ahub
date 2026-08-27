package schema

import (
	"fmt"
	"testing"
)

// TestClassifyKeyword_Const exercises the `const` branch: work_request.
// schema.json's own `"type": {"const": "work_request"}` fails when the
// envelope's declared `type` is a DIFFERENT-but-still-base-enum-valid
// type ("contract"), so base's own `type` enum passes while the
// type-specific const check fails.
func TestClassifyKeyword_Const(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: contract
title: Mismatched type const
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: data
priority: p3
blocking: false
interim_behavior: "n/a"
acceptance_criteria: ["x"]
classification: internal
`)
	violations, err := c.ValidateEnvelope("work_request", "v1", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	found := false
	for _, v := range violations {
		if v.Keyword == "const" && v.Path == "type" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a const violation at 'type', got %+v", violations)
	}
}

// TestFormatIsAsserted pins the NEW, deliberate behavior no-silent-yes-
// 2026-08/P3 stage 1 mints (spec 03 §8 AC1-2, DECISIONS.md § D3): Load DOES
// call the compiler's AssertFormat, so a malformed `created`/`needed_by`/
// `valid_until`/`expected_response.by` value (format: date / date-time) IS
// now a reported "format" FieldViolation — one table row per field, per
// AC-2's own wording ("the same holds for created, valid_until and
// expected_response.by").
//
// This test used to be TestFormatIsAnnotationOnly and pinned the OPPOSITE,
// on purpose: draft 2020-12 treats "format" as annotation-only unless
// assertion is explicitly enabled, and until this phase
// schemas/errors/v1/registry.yaml had no SCH- row for a format failure, so
// enabling AssertFormat produced an UNMAPPABLE violation that surfaced as a
// hard error out of internal/validate's ValidateDraft instead of a reported
// violation — confirmed empirically before that version of this test was
// written. SCH-012 (registered before AssertFormat was enabled — D3's own
// forced ordering, corpus.go's Load doc comment) closes that gap at the
// SCHEMA layer this test exercises; internal/validate's own mapping from
// the "format" keyword to SCH-012 is a separate, later step (see corpus.go's
// comment for why P3 stage 1 could not reach it).
//
// Each row also asserts a WELL-FORMED value on the SAME field validates
// clean — a table with only bad values could pass by asserting nothing at
// all if AssertFormat somehow stopped applying to that field, and this
// keeps the positive half honest per field, not just once for the type.
func TestFormatIsAsserted(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	base := map[string]string{
		"created":              `"2026-07-31T08:40:00Z"`,
		"needed_by":            `"2026-12-31"`,
		"valid_until":          `"2026-12-31"`,
		"expected_response.by": `"2026-12-31"`,
	}

	tests := []struct {
		name  string
		field string
		bad   string
	}{
		{"created (date-time)", "created", `"not-a-date-time"`},
		{"needed_by (date)", "needed_by", `"next tuesday"`},
		{"valid_until (date)", "valid_until", `"next tuesday"`},
		{"expected_response.by (date)", "expected_response.by", `"next tuesday"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fields := map[string]string{}
			for k, v := range base {
				fields[k] = v
			}
			fields[tt.field] = tt.bad

			instance := toInstance(t, workRequestYAML(fields))
			violations, err := c.ValidateEnvelope("work_request", "v1", instance)
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			found := false
			for _, v := range violations {
				if v.Keyword == "format" && v.Path == tt.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("field %q = %s: expected a format violation at path %q, got %+v", tt.field, tt.bad, tt.field, violations)
			}
		})
	}

	t.Run("all four fields well-formed validates clean of format violations", func(t *testing.T) {
		t.Parallel()
		instance := toInstance(t, workRequestYAML(base))
		violations, err := c.ValidateEnvelope("work_request", "v1", instance)
		if err != nil {
			t.Fatalf("ValidateEnvelope: %v", err)
		}
		for _, v := range violations {
			if v.Keyword == "format" {
				t.Fatalf("expected no format violation with well-formed dates, got %+v", violations)
			}
		}
	})
}

// workRequestYAML renders a well-formed work_request envelope, with
// fields["created"/"needed_by"/"valid_until"/"expected_response.by"]
// substituted in as raw YAML scalars (so a caller can supply a malformed
// string deliberately).
func workRequestYAML(fields map[string]string) string {
	return fmt.Sprintf(`
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: Format assertion table
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: %s
category: data
priority: p3
blocking: false
needed_by: %s
interim_behavior: "n/a"
acceptance_criteria: ["x"]
expected_response: {shape: "a short prose answer", by: %s}
classification: internal
valid_until: %s
`, fields["created"], fields["needed_by"], fields["expected_response.by"], fields["valid_until"])
}

// TestClassifyKeyword_AdditionalProperties exercises event.schema.json's
// flat `additionalProperties: false` (no allOf/$ref composition, so this
// hits *kind.AdditionalProperties directly, not the allOf-nested
// FalseSchema path envelope schemas use).
func TestClassifyKeyword_AdditionalProperties(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, `
schema: event/v1
event: 01J40A7M9P1S3V5W7Y9A1C3E5G
space: getvisa
subject: XW-axon-20260731-p9d3
transition: submit
actor: {kind: agent, name: codex, system: axon}
at: "2026-07-31T08:40:00Z"
stray_field: nope
`)
	violations, err := c.ValidateEvent("v1", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	found := false
	for _, v := range violations {
		if v.Keyword == keywordFalseSchema && v.Path == "stray_field" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a falseSchema violation at 'stray_field', got %+v", violations)
	}
}

// TestExtractFieldViolations_NonLibraryError exercises the defensive
// "other:<Go type>" fallback for an error that isn't a
// *jsonschema.ValidationError.
func TestExtractFieldViolations_NonLibraryError(t *testing.T) {
	t.Parallel()
	fvs := extractFieldViolations(errPlain{}, nil)
	if len(fvs) != 1 || fvs[0].Keyword != "other:schema.errPlain" {
		t.Fatalf("expected a single 'other:*' fallback violation, got %+v", fvs)
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "not a jsonschema.ValidationError" }
