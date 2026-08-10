package schema

// P5's AC1 discharge half (docs/features/active/agent-exchange-2026-08/
// specs/05-declared-nature.md, 2026-08-10 amendments): schema-level pins for
// envelope/v2/contract's `x_operational[]` and event/v2's `activation` +
// `activate` conditional. Every instance below is a literal Go map — never a
// golden fixture file (this wave's allowlist does not grant
// schemas/**/fixtures/**) and never a strings.Contains scan over rendered
// bytes (this epic's own paid-for trap: a rendered TEMPLATE COMMENT matched a
// substring check once and stayed green). Mutation-tested by hand: each
// assertion below was watched failing before the schema change that makes it
// pass, by temporarily reverting the schema edit under test.

import (
	"testing"
)

// baseV2Contract is a minimal, schema-legal envelope/v2/contract instance
// (every base + contract-own required field, one JSON-Schema-format
// artifact) that every x_operational[] sub-test starts from and mutates.
func baseV2Contract() map[string]any {
	return map[string]any{
		"schema":         "envelope/v2",
		"id":             "XC-axon-widget",
		"type":           "contract",
		"title":          "t",
		"space":          "fixture-space",
		"from":           "axon",
		"to":             []any{"beta"},
		"actor":          map[string]any{"kind": "agent", "name": "bot"},
		"created":        "2026-08-10T10:00:00Z",
		"priority":       "p3",
		"blocking":       false,
		"classification": "internal",
		"thread":         "thread:axon-20260810-ab12",
		"category":       "api",
		"version":        "1.0.0",
		"compat_policy":  "strict-semver",
		"schema_format":  "json-schema-2020-12",
		"artifacts": []any{
			map[string]any{
				"path":       "schema/widget.schema.json",
				"role":       "schema",
				"normative":  true,
				"media_type": "application/schema+json",
			},
		},
	}
}

// TestXOperationalIsOptionalAndAbsenceIsNeverRequired is the P-1 assertion
// this phase's own entry point names: the field carries no default, and a
// contract declaring nothing about x_operational at all is still schema-
// legal — absence must remain a live state, never a validation failure.
func TestXOperationalIsOptionalAndAbsenceIsNeverRequired(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Contract()
	violations, err := c.ValidateEnvelope("contract", "v2", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected a contract with no x_operational at all to be valid, got %+v", violations)
	}
}

// TestXOperationalDeclaredItemValidates is the positive case: a declared
// item with a legal state and an eta validates cleanly.
func TestXOperationalDeclaredItemValidates(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Contract()
	instance["x_operational"] = []any{
		map[string]any{"name": "endpoint", "state": "absent", "eta": "2026-09-01"},
		map[string]any{"name": "registration", "state": "ready"},
	}
	violations, err := c.ValidateEnvelope("contract", "v2", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected a valid x_operational[] to validate, got %+v", violations)
	}
}

// TestXOperationalStateIsClosedVocabulary pins the design decision this
// wave's brief itself calls out: `undeclared` is a READER's computed
// verdict (P-1's own words: "the declaration must change what those facts
// MEAN, not whether they exist"), never a literal an author may write. If
// the schema's `state` enum were ever widened to accept "undeclared"
// directly, a producer could type the very silence P-1 forbids — this test
// reds the moment that widening happens.
func TestXOperationalStateIsClosedVocabulary(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	for _, bad := range []string{"undeclared", "in-progress", ""} {
		instance := baseV2Contract()
		instance["x_operational"] = []any{map[string]any{"name": "endpoint", "state": bad}}
		violations, err := c.ValidateEnvelope("contract", "v2", instance)
		if err != nil {
			t.Fatalf("ValidateEnvelope: %v", err)
		}
		if len(violations) != 1 {
			t.Fatalf("state=%q: expected exactly one violation, got %+v", bad, violations)
		}
		if violations[0].Keyword != "enum" {
			t.Fatalf("state=%q: violation keyword = %q, want enum: %+v", bad, violations[0].Keyword, violations[0])
		}
	}
}

// TestXOperationalItemRequiresNameAndState pins the two required leaves —
// an item naming nothing, or naming a status with no name, is refused.
func TestXOperationalItemRequiresNameAndState(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	cases := map[string]map[string]any{
		"missing name":  {"state": "ready"},
		"missing state": {"name": "endpoint"},
	}
	for label, item := range cases {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			instance := baseV2Contract()
			instance["x_operational"] = []any{item}
			violations, err := c.ValidateEnvelope("contract", "v2", instance)
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if len(violations) != 1 || violations[0].Keyword != "required" {
				t.Fatalf("expected exactly one `required` violation, got %+v", violations)
			}
		})
	}
}

// TestXOperationalItemRefusesUnknownKeys pins that a hand-rolled extra key
// on one item (the exact defect class US-4 exists for, one level down) is
// refused, not silently carried.
func TestXOperationalItemRefusesUnknownKeys(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Contract()
	instance["x_operational"] = []any{
		map[string]any{"name": "endpoint", "state": "ready", "owner": "someone"},
	}
	violations, err := c.ValidateEnvelope("contract", "v2", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != keywordFalseSchema {
		t.Fatalf("expected exactly one falseSchema violation, got %+v", violations)
	}
}

// --- event/v2 `activate` / `activation` ------------------------------------

// baseV2Event is a minimal, schema-legal event/v2 instance for subject and
// transition, with no optional fields — every conditional-block test below
// adds only what its own case needs.
func baseV2Event(subject, transition string) map[string]any {
	return map[string]any{
		"schema":     "event/v2",
		"event":      "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"space":      "fixture-space",
		"subject":    subject,
		"transition": transition,
		"actor":      map[string]any{"kind": "agent", "name": "bot", "system": "axon"},
		"at":         "2026-08-10T10:00:00Z",
	}
}

func validActivation() map[string]any {
	return map[string]any{
		"version":   "1.0.0",
		"status":    "live",
		"satisfies": []any{"endpoint"},
	}
}

// TestActivateOnAContractRequiresActivation is the guard itself: an
// `activate` transition against an XC- subject with NO `activation` block
// is refused. This is the assertion that reds the moment the conditional
// (schemas/event/v2/event.schema.json's second allOf entry) is REMOVED —
// removing it makes this exact instance schema-legal, and the test below
// starts failing.
func TestActivateOnAContractRequiresActivation(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Event("XC-axon-widget", "activate")
	violations, err := c.ValidateEvent("v2", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != "required" {
		t.Fatalf("expected exactly one `required` violation for the missing activation block, got %+v", violations)
	}
}

// TestActivateOnAContractWithActivationValidates is the positive half: the
// SAME instance, now carrying a legal `activation` block, validates.
func TestActivateOnAContractWithActivationValidates(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Event("XC-axon-widget", "activate")
	instance["activation"] = validActivation()
	violations, err := c.ValidateEvent("v2", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected a legal activation block to validate, got %+v", violations)
	}
}

// TestActivationConditionalDoesNotFireOutsideActivate is the OTHER mutation
// this rule needs a test for, per this wave's own brief: "a rule needs a
// test that reds when it is removed AND one that reds when it is made
// unconditional". An ordinary, unrelated transition on an ordinary subject,
// with none of event/v2's optional fields, must stay legal — if the
// conditional were ever widened to require `activation` unconditionally
// (or fire on the subject pattern alone), this exact minimal instance would
// start failing.
func TestActivationConditionalDoesNotFireOutsideActivate(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	for _, tc := range []struct {
		name, subject, transition string
	}{
		{"non-activate transition on a contract subject", "XC-axon-widget", "acknowledge"},
		{"activate transition on a non-contract subject", "XQ-axon-1", "activate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			instance := baseV2Event(tc.subject, tc.transition)
			violations, err := c.ValidateEvent("v2", instance)
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("expected no activation requirement here, got %+v", violations)
			}
		})
	}
}

// TestActivationRequiresVersionStatusSatisfies pins the object's own three
// required leaves.
func TestActivationRequiresVersionStatusSatisfies(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	for label, mutate := range map[string]func(map[string]any){
		"missing version":   func(a map[string]any) { delete(a, "version") },
		"missing status":    func(a map[string]any) { delete(a, "status") },
		"missing satisfies": func(a map[string]any) { delete(a, "satisfies") },
	} {
		t.Run(label, func(t *testing.T) {
			t.Parallel()
			instance := baseV2Event("XC-axon-widget", "activate")
			activation := validActivation()
			mutate(activation)
			instance["activation"] = activation
			violations, err := c.ValidateEvent("v2", instance)
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if len(violations) != 1 || violations[0].Keyword != "required" {
				t.Fatalf("expected exactly one `required` violation, got %+v", violations)
			}
		})
	}
}

// TestActivationStatusIsClosedToLive pins that `status` accepts only the
// literal `live` the corpus names — the field this wave's fill-classes.yaml
// classifies TOOL for exactly this reason (no CLI flag exposes a choice
// because the schema offers none yet).
func TestActivationStatusIsClosedToLive(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Event("XC-axon-widget", "activate")
	activation := validActivation()
	activation["status"] = "pending"
	instance["activation"] = activation
	violations, err := c.ValidateEvent("v2", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != "enum" {
		t.Fatalf("expected exactly one enum violation, got %+v", violations)
	}
}

// TestActivationSatisfiesRequiresAtLeastOneItem pins the minItems floor: an
// activation that satisfies nothing is not an activation.
func TestActivationSatisfiesRequiresAtLeastOneItem(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Event("XC-axon-widget", "activate")
	activation := validActivation()
	activation["satisfies"] = []any{}
	instance["activation"] = activation
	violations, err := c.ValidateEvent("v2", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 1 || violations[0].Keyword != "minItems" {
		t.Fatalf("expected exactly one minItems violation, got %+v", violations)
	}
}

// TestActivateIsAcceptedTransitionEnumMember pins that "activate" itself is
// a legal `transition` value — a plain event/v1-style event on a non-XC
// subject with the transition alone (no activation needed there) must not
// be refused for the transition value itself.
func TestActivateIsAcceptedTransitionEnumMember(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	instance := baseV2Event("XQ-axon-1", "activate")
	violations, err := c.ValidateEvent("v2", instance)
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected `activate` to be a legal transition value, got %+v", violations)
	}
}
