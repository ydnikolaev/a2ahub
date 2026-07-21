package validate

import "testing"

// TestResultIdentity_SchemaClassAgreement is AC-201.2's V1/V2 half: the
// same invalid content, run through ValidateDraft (V1) and
// ValidateForSubmit (V2), agrees on shared (schema-class) violations.
// V2 may additionally report referential/authz/lifecycle/policy
// violations V1's narrower scope never runs — this test asserts the
// SCHEMA-CLASS subset is identical, not the full violation set.
func TestResultIdentity_SchemaClassAgreement(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	// A schema-class-invalid work_request (bad category enum value) that
	// is ALSO section-mismatched, so V2 additionally reports an authz/
	// referential violation V1 never runs — proving V1/V2 identity is
	// about the SHARED class, not the full set.
	raw := []byte("---\n" + `
schema: envelope/v1
id: XW-axon-20260731-p9d3
type: work_request
title: A work request with a bad category
space: getvisa
from: axon
to: [seomatrix]
actor: {kind: agent, name: codex}
created: "2026-07-31T08:40:00Z"
category: not-a-real-category
priority: p3
blocking: false
interim_behavior: "n/a"
acceptance_criteria: ["x"]
classification: internal
` + "---\nBody.\n")

	draft := Draft{Path: "axon/exchanges/XW-axon-20260731-p9d3.md", Raw: raw}

	v1, err := engine.ValidateDraft(draft)
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	v2, err := engine.ValidateForSubmit(draft, nil, LocalContext{OwnSystem: "axon"})
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}

	v1Schema := schemaClassCodes(v1.Violations)
	v2Schema := schemaClassCodes(v2.Violations)

	if len(v1Schema) == 0 {
		t.Fatal("expected at least one schema-class violation from the bad category enum")
	}
	if len(v1Schema) != len(v2Schema) {
		t.Fatalf("V1 schema-class violations %v != V2 schema-class violations %v", v1Schema, v2Schema)
	}
	for i := range v1Schema {
		if v1Schema[i] != v2Schema[i] {
			t.Fatalf("V1 schema-class violations %v != V2 schema-class violations %v", v1Schema, v2Schema)
		}
	}
}

func schemaClassCodes(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		if v.Class == ClassSchema {
			out = append(out, v.Code)
		}
	}
	return out
}
