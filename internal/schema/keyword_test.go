package schema

import "testing"

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

// TestFormatIsAnnotationOnly documents a deliberate, load-bearing
// decision: Load does NOT call the compiler's AssertFormat, so a
// malformed `created`/`needed_by`/`valid_until` value (format: date /
// date-time) is NOT a validation failure — draft 2020-12 treats "format"
// as annotation-only unless assertion is explicitly enabled, and
// schemas/errors/v1/registry.yaml has no SCH- row for a format failure
// (P2's authored set covers required/enum/forbidden-field/cardinality/
// conditional-required/type/pattern/interim_behavior only). Enabling
// AssertFormat without a matching registry code produced an UNMAPPABLE
// violation that surfaced as a hard error out of internal/validate's
// ValidateDraft instead of a reported violation — confirmed empirically
// before this test was written; see corpus.go's Load doc comment. This
// test pins the current (correct) behavior: a bad `created` value
// passes schema validation (format is not enforced), so a future
// accidental re-enable of AssertFormat fails this test loudly instead of
// silently reintroducing the bug.
func TestFormatIsAnnotationOnly(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: Bad created format
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "not-a-date-time"
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
	for _, v := range violations {
		if v.Keyword == "format" {
			t.Fatalf("expected format assertion to be OFF (no registry code exists for it), got a format violation: %+v", violations)
		}
	}
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
