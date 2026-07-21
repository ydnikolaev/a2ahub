package schema

import (
	"errors"
	"testing"
)

func mustLoad(t *testing.T) *Corpus {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

// toInstance decodes yamlDoc via DecodeYAMLInstance — the same path
// production code uses (internal/validate's decodeEnvelope) — never a
// naive `yaml.Unmarshal` into `map[string]any` + JSON round-trip, which
// would silently corrupt date/date-time field values (see
// DecodeYAMLInstance's doc comment; TestValidateConsumes's regression is
// exactly this footgun).
func toInstance(t *testing.T, yamlDoc string) any {
	t.Helper()
	instance, err := DecodeYAMLInstance([]byte(yamlDoc))
	if err != nil {
		t.Fatalf("DecodeYAMLInstance: %v", err)
	}
	return instance
}

const validWorkRequest = `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: A valid work request
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: data
priority: p3
blocking: false
interim_behavior: "Fees rendered without normalization."
acceptance_criteria:
  - "Every code exists in the registry."
classification: internal
`

// FIRST TEST (brief step 2): allOf + base-$ref + unevaluatedProperties
// composition actually rejects a stray field, and a decision+category
// fixture is rejected too. If santhosh-tekuri/jsonschema/v6 did not
// enforce this, the brief requires STOPPING — it does enforce it, so this
// test documents the confirmation rather than a stop.
func TestUnevaluatedPropertiesComposition_RejectsStrayField(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, validWorkRequest+"stray_field: nope\n")
	violations, err := c.ValidateEnvelope("work_request", "v1", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("expected a violation for the stray field; got none — allOf+base-$ref+unevaluatedProperties composition is NOT enforced")
	}
	found := false
	for _, v := range violations {
		if v.Keyword == keywordFalseSchema && v.Path == "stray_field" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a falseSchema violation at path 'stray_field', got %+v", violations)
	}
}

func TestUnevaluatedPropertiesComposition_ValidInstancePasses(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, validWorkRequest)
	violations, err := c.ValidateEnvelope("work_request", "v1", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected a valid instance to produce zero violations, got %+v", violations)
	}
}

// TestUnevaluatedPropertiesComposition_DecisionRejectsCategory is the
// brief's second FIRST-TEST assertion: decision has no `category` field in
// either base or decision.schema.json, so unevaluatedProperties:false must
// structurally reject one if present.
func TestUnevaluatedPropertiesComposition_DecisionRejectsCategory(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, `
schema: envelope/v1
id: XD-axon-20260731-p9d3
type: decision
title: A decision with a forbidden category field
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: not-allowed
priority: p3
blocking: false
interim_behavior: "n/a"
required_approvers: [seomatrix]
context: "why"
options_considered: ["a", "b"]
classification: internal
`)
	violations, err := c.ValidateEnvelope("decision", "v1", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	found := false
	for _, v := range violations {
		if v.Keyword == keywordFalseSchema && v.Path == "category" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected decision to reject a `category` field, got %+v", violations)
	}
}

// TestAnnotationPropagationEdge_BaseFailureDoesNotCascade is the brief's
// second FIRST-TEST verification: when the base $ref branch fails on one
// field (a bad `id` pattern), the corpus must report exactly that one
// schema violation, matching ajv's (spec-conformant) behavior, not
// cascade a second wave of spurious unevaluatedProperties errors on every
// other base-only field.
//
// Empirically, santhosh-tekuri/jsonschema/v6 v6.0.2 DOES cascade here (11
// leaves instead of 1) — confirmed against ajv 8.20 (draft 2020-12, same
// two schema files, same instance) returning exactly one error. This
// test asserts the CORRECTED behavior after this package's
// BaseEnvelopeFields-driven suppression (see ValidateEnvelope /
// BaseEnvelopeFields doc comments) — the workaround, not the raw library
// output. See this phase's Deviations report for the full empirical
// trail (ajv comparison, blast-radius scope across the P2 corpus: exactly
// one existing fixture — XR-axon-invalid-bad-id-grammar.md — hits this
// edge).
func TestAnnotationPropagationEdge_BaseFailureDoesNotCascade(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	instance := toInstance(t, `
schema: envelope/v1
id: XR-axon
type: requirement
title: Canonical country vocabulary — malformed id (missing slug)
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: claude-code, model: claude-fable-5}
created: "2026-07-29T09:12:00Z"
category: vocabulary
priority: p2
blocking: false
interim_behavior: "Axon renders English names from ISO-3166 fallback table until delivered."
acceptance_criteria:
  - "Every destination row carries iso2 from the real ISO-3166 registry."
classification: internal
`)
	violations, err := c.ValidateEnvelope("requirement", "v1", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation (the base id-pattern failure), got %d: %+v", len(violations), violations)
	}
	if violations[0].Keyword != "pattern" || violations[0].Path != "id" {
		t.Fatalf("expected the single violation to be a pattern failure at 'id', got %+v", violations[0])
	}
}

func TestVersionSeam(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    int
		want bool
	}{
		{0, false},
		{1, true},
		{2, false}, // CC-005: unknown newer version refused
	}
	for _, tc := range cases {
		if got := AcceptsEnvelopeVersion(tc.v); got != tc.want {
			t.Errorf("AcceptsEnvelopeVersion(%d) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// TestVersionSeam_CC005Fixture is CC-005's own unit test (no shared P2
// fixture exists for this — it's a version-seam probe, constructed
// inline): an artifact declaring an unknown, newer envelope version must
// be refused, never silently validated or downgraded.
func TestVersionSeam_CC005Fixture(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	_, err := c.ValidateEnvelope("work_request", "v2", toInstance(t, validWorkRequest))
	if err == nil {
		t.Fatal("expected ValidateEnvelope to refuse envelope/v2 (CC-005: unknown newer version), got nil error")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
}
