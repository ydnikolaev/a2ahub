package schema

// contract_xprefix_test.go — no-silent-yes-2026-08 P5 (docs/features/active/
// no-silent-yes-2026-08/specs/05-a-contract-says-what-it-must.md), AC 5, per
// this spec's own 2026-08-28 amendment (§11 item 3): "A binary built before
// this phase" cannot be built inside any tier here. The claim that matters
// is the MECHANISM, and it is directly assertable against the CURRENT
// schema: a contract carrying an x_-prefixed key nothing declares must
// validate, because patternProperties: {"^x_": true} evaluates it and
// unevaluatedProperties: false therefore does not reject it -- precisely
// what a pre-phase binary does with x_identity/x_guarantees/
// x_schema_location. Exactly two assertions, no more, per the spec's own
// instrument decision -- plus one OPTIONAL, explicitly weaker companion.
//
// Every instance below is a literal Go map, following p5_ac1_discharge_
// test.go's own convention in this same package/file group: never a golden
// fixture file (this wave's allowlist grants only schemas/envelope/v2/
// fixtures/{valid,invalid}/, and none is needed here) and never a
// strings.Contains scan over rendered bytes.

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/ydnikolaev/a2ahub/schemas"
)

// TestUndeclaredXPrefixedKeyValidatesClean is AC5's (a): a contract carrying
// an x_-prefixed key NOTHING in this schema declares must still validate.
// This is exactly the shape a pre-phase binary sees x_identity (or
// x_guarantees, or x_schema_location) as, before this phase ever declared
// them -- the escape this phase's "no min_binary_version move" claim rests
// on.
func TestUndeclaredXPrefixedKeyValidatesClean(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Contract()
	instance["x_future_nothing_declares"] = map[string]any{"anything": "goes"}
	violations, err := c.ValidateEnvelope("contract", "v2", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected an undeclared x_-prefixed key to validate clean (the patternProperties/unevaluatedProperties escape), got %+v", violations)
	}
}

// TestXPrefixEscapeIsPinnedInTheSchemaFile is AC5's (b): the escape itself
// is pinned in the raw schema FILE -- patternProperties["^x_"] is exactly
// the literal `true` and unevaluatedProperties is exactly the literal
// `false` -- so (a) above is proven by a declared mechanism, not an
// accident of whatever the current property set happens to be.
func TestXPrefixEscapeIsPinnedInTheSchemaFile(t *testing.T) {
	t.Parallel()
	raw, err := schemas.FS.ReadFile("envelope/v2/contract.schema.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	patternProps, ok := doc["patternProperties"].(map[string]any)
	if !ok {
		t.Fatalf("patternProperties missing or not an object: %#v", doc["patternProperties"])
	}
	if xEscape, present := patternProps["^x_"]; !present || xEscape != true {
		t.Fatalf(`patternProperties["^x_"] = %#v (present=%v), want the literal true`, xEscape, present)
	}
	if unevaluated, present := doc["unevaluatedProperties"]; !present || unevaluated != false {
		t.Fatalf("unevaluatedProperties = %#v (present=%v), want the literal false", unevaluated, present)
	}
}

// TestXPrefixEscapeWithoutTheThreeNewProperties_WeakerLiteralReading is the
// OPTIONAL companion the spec's §11 amendment names and explicitly calls
// the WEAKER of the two required assertions above: it recompiles the schema
// FILE with exactly x_identity/x_guarantees/x_schema_location deleted from
// `properties`, and reproduces the pre-phase schema only if nothing else in
// the file moved between then and now -- unlike the two tests above, which
// hold regardless of what else the file has done. Kept as the literal
// reading of AC5's own wording ("a binary built before this phase").
//
// Mirrors corpus.go's own Load() mechanism exactly (readJSON +
// resourceURLPrefix + AddResource + Compile) rather than inventing a
// second one, isolated to just the two schemas contract.schema.json needs
// (itself + its allOf $ref target, base.schema.json).
func TestXPrefixEscapeWithoutTheThreeNewProperties_WeakerLiteralReading(t *testing.T) {
	t.Parallel()

	baseDoc, err := readJSON("envelope/v2/base.schema.json")
	if err != nil {
		t.Fatalf("readJSON base: %v", err)
	}
	contractDoc, err := readJSON("envelope/v2/contract.schema.json")
	if err != nil {
		t.Fatalf("readJSON contract: %v", err)
	}
	root, ok := contractDoc.(map[string]any)
	if !ok {
		t.Fatalf("contract schema root is not an object: %T", contractDoc)
	}
	props, ok := root["properties"].(map[string]any)
	if !ok {
		t.Fatalf("contract schema properties is not an object: %T", root["properties"])
	}
	for _, field := range []string{"x_identity", "x_guarantees", "x_schema_location"} {
		if _, present := props[field]; !present {
			t.Fatalf("expected %q to be present before deletion (test setup bug)", field)
		}
		delete(props, field)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resourceURLPrefix+"envelope/v2/base.schema.json", baseDoc); err != nil {
		t.Fatalf("AddResource base: %v", err)
	}
	const seedKey = resourceURLPrefix + "xprefix-companion-seed"
	if err := compiler.AddResource(seedKey, contractDoc); err != nil {
		t.Fatalf("AddResource contract: %v", err)
	}
	sch, err := compiler.Compile(seedKey)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	instance := baseV2Contract()
	instance["x_identity"] = map[string]any{"keys": []any{"url"}, "on_redelivery": "upsert"}
	instance["x_guarantees"] = []any{"deterministic_keys"}
	instance["x_schema_location"] = "provides/pages/schema/main.schema.json"

	if err := sch.Validate(instance); err != nil {
		t.Fatalf("expected the pre-phase-reconstructed schema to still accept all three x_ keys via the patternProperties/unevaluatedProperties escape, got: %v", err)
	}
}
