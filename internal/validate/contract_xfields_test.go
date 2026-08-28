package validate

// contract_xfields_test.go — no-silent-yes-2026-08 P5 (docs/features/active/
// no-silent-yes-2026-08/specs/05-a-contract-says-what-it-must.md), AC 1, 3,
// 4 and 9: envelope/v2/contract.schema.json's three new x_ fields
// (x_identity, x_guarantees, x_schema_location), driven through the SAME
// production path `a2a validate` itself uses -- Engine.ValidateDraft, which
// runs artifact.ParseFrontmatter -> decodeEnvelope -> corpus.ValidateEnvelope
// -> mapSchemaViolations (schema_class.go) -- rather than a hand-built
// map[string]any fed straight to internal/schema. Every case decodes a full
// frontmatter document, following operational_claim_test.go's own
// production-shaped-path convention in this package.
//
// Mutation-tested by hand: each assertion below was watched failing (wrong
// violation count, or the schema edit reverted) before the schema change
// that makes it pass, then restored — see this wave's own report.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/schemas"
)

func xfieldsEngine(t *testing.T) *Engine {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return New(corpus)
}

// contractXFieldsFrontmatter builds a schema-legal envelope/v2 contract
// document (every base + contract-own required field, one JSON-Schema-
// format artifact, mirroring internal/schema/p5_ac1_discharge_test.go's
// baseV2Contract() shape in YAML form since this package validates raw
// bytes, not a pre-decoded map) with extra spliced in verbatim as the raw
// YAML text of whichever x_ fields a case wants to add.
func contractXFieldsFrontmatter(extra string) string {
	return "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-axon-xfields\n" +
		"type: contract\n" +
		"title: x-fields contract\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-08-28T10:00:00Z\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"thread: thread:axon-20260828-ab12\n" +
		"category: api\n" +
		"version: \"1.0.0\"\n" +
		"compat_policy: additive\n" +
		"schema_format: json-schema-2020-12\n" +
		"artifacts:\n" +
		"  - path: schema/widget.schema.json\n" +
		"    role: schema\n" +
		"    normative: true\n" +
		"    media_type: application/schema+json\n" +
		extra +
		"---\n" +
		"Body.\n"
}

func xfieldsResult(t *testing.T, extra string) Result {
	t.Helper()
	engine := xfieldsEngine(t)
	result, err := engine.ValidateDraft(Draft{Path: "axon/exchanges/XC-axon-xfields.md", Raw: []byte(contractXFieldsFrontmatter(extra))})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	return result
}

// schemaViolationsAt filters result.Violations to ClassSchema violations
// whose Path equals path.
func schemaViolationsAt(result Result, path string) []Violation {
	var out []Violation
	for _, v := range result.Violations {
		if v.Class == ClassSchema && v.Path == path {
			out = append(out, v)
		}
	}
	return out
}

// TestXFieldsWellFormedAccepted is AC 1/3/4's positive case: a document
// carrying all three new fields, each well-formed, validates clean.
func TestXFieldsWellFormedAccepted(t *testing.T) {
	t.Parallel()
	extra := "x_identity:\n" +
		"  keys: [url]\n" +
		"  dynamic_keys_from: variant\n" +
		"  on_redelivery: upsert\n" +
		"x_guarantees: [deterministic_keys, required_non_null]\n" +
		"x_schema_location: provides/pages/schema/main.schema.json\n"
	result := xfieldsResult(t, extra)
	if !result.Valid {
		t.Fatalf("expected a well-formed x_identity/x_guarantees/x_schema_location document to be valid, got violations %+v", result.Violations)
	}
}

// TestEachXFieldAbsentIsLegal is AC 1/3/4's absence case: a document
// declaring none of the three new fields at all still validates — absence
// is a live, legal state, never a required default.
func TestEachXFieldAbsentIsLegal(t *testing.T) {
	t.Parallel()
	result := xfieldsResult(t, "")
	if !result.Valid {
		t.Fatalf("expected a document with none of the three new x_ fields to be valid, got violations %+v", result.Violations)
	}
}

// TestXIdentityOnRedeliveryOutsideEnumIsRefused is AC 1: on_redelivery
// accepts only the two declared literals; anything else is refused.
func TestXIdentityOnRedeliveryOutsideEnumIsRefused(t *testing.T) {
	t.Parallel()
	extra := "x_identity:\n" +
		"  keys: [url]\n" +
		"  on_redelivery: replace\n"
	result := xfieldsResult(t, extra)
	if result.Valid {
		t.Fatalf("expected on_redelivery: replace to be refused, got a valid result")
	}
	got := schemaViolationsAt(result, "x_identity.on_redelivery")
	if len(got) != 1 {
		t.Fatalf("schema violations at x_identity.on_redelivery = %+v, want exactly 1", got)
	}
	if got[0].Code != "SCH-002" {
		t.Fatalf("violation code = %q, want SCH-002 (enum)", got[0].Code)
	}
}

// TestXGuaranteesUnlistedValueIsRefused is AC 3: x_guarantees is a closed
// enum — an unlisted value is refused rather than silently carried.
func TestXGuaranteesUnlistedValueIsRefused(t *testing.T) {
	t.Parallel()
	extra := "x_guarantees: [not_a_real_guarantee]\n"
	result := xfieldsResult(t, extra)
	if result.Valid {
		t.Fatalf("expected an unlisted x_guarantees value to be refused, got a valid result")
	}
	got := schemaViolationsAt(result, "x_guarantees.0")
	if len(got) != 1 {
		t.Fatalf("schema violations at x_guarantees.0 = %+v, want exactly 1", got)
	}
	if got[0].Code != "SCH-002" {
		t.Fatalf("violation code = %q, want SCH-002 (enum)", got[0].Code)
	}
}

// TestXGuaranteesEmptyArrayIsLegal is AC 3's own named edge case: an empty
// array is a legal, deliberate claim of no guarantees — never refused.
func TestXGuaranteesEmptyArrayIsLegal(t *testing.T) {
	t.Parallel()
	extra := "x_guarantees: []\n"
	result := xfieldsResult(t, extra)
	if !result.Valid {
		t.Fatalf("expected an empty x_guarantees array to be valid, got violations %+v", result.Violations)
	}
}

// TestXSchemaLocationRepoRelativeAccepted is AC 4's positive case.
func TestXSchemaLocationRepoRelativeAccepted(t *testing.T) {
	t.Parallel()
	extra := "x_schema_location: provides/pages/schema/main.schema.json\n"
	result := xfieldsResult(t, extra)
	if !result.Valid {
		t.Fatalf("expected a repo-relative x_schema_location to be valid, got violations %+v", result.Violations)
	}
}

// TestXSchemaLocationAbsolutePathIsRefused is AC 4: an absolute path is
// refused, not accepted as "repo-relative".
func TestXSchemaLocationAbsolutePathIsRefused(t *testing.T) {
	t.Parallel()
	extra := "x_schema_location: /etc/passwd\n"
	result := xfieldsResult(t, extra)
	if result.Valid {
		t.Fatalf("expected an absolute x_schema_location to be refused, got a valid result")
	}
	got := schemaViolationsAt(result, "x_schema_location")
	if len(got) != 1 {
		t.Fatalf("schema violations at x_schema_location = %+v, want exactly 1", got)
	}
	if got[0].Code != "SCH-007" {
		t.Fatalf("violation code = %q, want SCH-007 (pattern)", got[0].Code)
	}
}

// TestXSchemaLocationTraversalIsRefused is AC 4: a `..` traversal segment
// is refused.
func TestXSchemaLocationTraversalIsRefused(t *testing.T) {
	t.Parallel()
	extra := "x_schema_location: ../secret/schema.json\n"
	result := xfieldsResult(t, extra)
	if result.Valid {
		t.Fatalf("expected a `..`-traversal x_schema_location to be refused, got a valid result")
	}
	got := schemaViolationsAt(result, "x_schema_location")
	if len(got) != 1 {
		t.Fatalf("schema violations at x_schema_location = %+v, want exactly 1", got)
	}
	if got[0].Code != "SCH-007" {
		t.Fatalf("violation code = %q, want SCH-007 (pattern)", got[0].Code)
	}
}

// TestXFieldDescriptionsStateTheirAbsenceReading is AC 9: each of the three
// new fields' own schema `description` states that its ABSENCE reads as
// undeclared downstream — never a default — and that this reading is the
// CALLER's, never the schema's, to make (x_operational's own wording, the
// convention these three fields copy). Reads the live descriptions off the
// schema FILE rather than hardcoding a copy of them, so the assertion tracks
// whatever the schema actually says instead of a frozen expectation.
func TestXFieldDescriptionsStateTheirAbsenceReading(t *testing.T) {
	t.Parallel()
	raw, err := schemas.FS.ReadFile("envelope/v2/contract.schema.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		t.Fatalf("contract schema properties is not an object: %T", doc["properties"])
	}
	for _, field := range []string{"x_identity", "x_guarantees", "x_schema_location"} {
		fieldSchema, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("properties[%q] is not an object: %#v", field, props[field])
		}
		desc, ok := fieldSchema["description"].(string)
		if !ok || desc == "" {
			t.Fatalf("properties[%q].description is missing or empty", field)
		}
		if !strings.Contains(desc, "undeclared") {
			t.Fatalf("properties[%q].description = %q, want it to state that absence reads as undeclared", field, desc)
		}
		if !strings.Contains(desc, "CALLER's") {
			t.Fatalf("properties[%q].description = %q, want it to state that the absence reading is the CALLER's, never this schema's, to make", field, desc)
		}
	}
}
