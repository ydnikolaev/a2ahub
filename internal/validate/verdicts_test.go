package validate

import (
	"strings"
	"testing"
)

// --- unit level: checkVerdictIndexRange / resolveOutOfRangeIndices ---

// TestCheckVerdictIndexRange_InRangeProducesNoViolation is REF-019's happy
// path: every verdicts[].index resolves inside the subject's declared
// acceptance_criteria count.
func TestCheckVerdictIndexRange_InRangeProducesNoViolation(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 1}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("in-range verdict index produced violations: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_OutOfRangeProducesREF019 is the mutation
// case: an index the parent does not declare must be refused, naming the
// exact index and REF-019.
func TestCheckVerdictIndexRange_OutOfRangeProducesREF019(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(4), "verdict": "unmet", "cause_owner": "seomatrix"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 2}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	if len(violations) != 1 {
		t.Fatalf("out-of-range verdict index produced %d violations, want 1: %+v", len(violations), violations)
	}
	v := violations[0]
	if v.Code != "REF-019" {
		t.Errorf("code = %q, want REF-019", v.Code)
	}
	if v.Class != ClassReferential {
		t.Errorf("class = %q, want referential", v.Class)
	}
	if v.Severity != SeverityReject {
		t.Errorf("severity = %q, want reject", v.Severity)
	}
	if !strings.Contains(v.Message, "4") {
		t.Errorf("message %q does not name the offending index 4", v.Message)
	}
}

// TestCheckVerdictIndexRange_UnresolvableSubjectDegradesSilently is
// ParentCriteriaCounter's own "cannot check is not check passed" rail,
// exercised for verdicts[]: an unresolvable subject (this file's package
// doc, point 2 — a `verify` event whose subject is the response id, not a
// criteria-bearing artifact) must produce nothing, not a false positive.
func TestCheckVerdictIndexRange_UnresolvableSubjectDegradesSilently(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{}} // XS-... not present

	violations := checkVerdictIndexRange("XS-axon-20260809-resp1", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("unresolvable subject produced violations: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_NoParentCriteriaCounterDegradesSilently is the
// other "cannot check" branch: a Resolver that does not implement
// ParentCriteriaCounter at all (every production Resolver until the lead
// wires this file in, per its own package doc) must not panic and must not
// synthesize a violation.
func TestCheckVerdictIndexRange_NoParentCriteriaCounterDegradesSilently(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
		},
	}
	resolver := &fakeResolver{} // registry_test.go's fake: no AcceptanceCriteriaCount

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("resolver without ParentCriteriaCounter produced violations: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_AbsentVerdictsIsNotChecked mirrors
// responseUnmetIndices' own "present=false, not a violation" case for
// verdicts[]: an event carrying no verdicts field at all (e.g. every
// transition other than verify/close) is nothing this check has an
// opinion about.
func TestCheckVerdictIndexRange_AbsentVerdictsIsNotChecked(t *testing.T) {
	t.Parallel()
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 1}}
	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", map[string]any{}, resolver)
	if len(violations) != 0 {
		t.Fatalf("absent verdicts[] produced violations: %+v", violations)
	}
}

// --- schema level: the real corpus, via ValidateEvent ---

// verifyEventYAML builds a minimal, otherwise-valid event/v2 document for
// the given transition, with or without a verdicts[] block, to drive the
// real shipped schema (not a re-derived copy of its rule) through
// Engine.ValidateEvent.
func verifyEventYAML(transition string, verdictsBlock string) []byte {
	var b strings.Builder
	b.WriteString("schema: event/v2\n")
	b.WriteString("event: 01K1A2B3C4D5E6F7G8H9J0K1M9\n")
	b.WriteString("space: getvisa\n")
	b.WriteString("subject: XW-axon-20260810-verify\n")
	b.WriteString("transition: " + transition + "\n")
	b.WriteString("actor:\n  kind: agent\n  name: verifier\n  system: axon\n")
	b.WriteString("at: 2026-08-10T12:00:00Z\n")
	b.WriteString("produced_by:\n  tool: a2a\n  version: 0.19.0\n")
	b.WriteString(verdictsBlock)
	return []byte(b.String())
}

const validVerdictsBlock = "verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n"

// TestValidateEvent_VerdictsRequiredOnVerifyAndClose is the schema-level
// half of T5's discharge: the real corpus, not a re-derived copy of its
// rule, refuses a verify/close event with no verdicts[] and accepts one
// that carries it.
func TestValidateEvent_VerdictsRequiredOnVerifyAndClose(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	for _, transition := range []string{"verify", "close"} {
		t.Run(transition+"_missing_verdicts_is_rejected", func(t *testing.T) {
			t.Parallel()
			result, err := engine.ValidateEvent(verifyEventYAML(transition, ""), "0.19.0")
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if result.Valid {
				t.Fatalf("%s event with no verdicts[] validated clean, want a rejection naming the missing field", transition)
			}
			// A conditionally-required field's own violation is
			// document-level (schema.FieldViolation's Path is "" for a
			// `required` failure — schema/violation.go's own doc
			// comment), so this asserts by CODE (SCH-005, the allOf/then
			// conditional-required mapping schema_class.go already uses
			// for the schema's OTHER allOf/then clause), not by path.
			if !hasCode(result.Violations, "SCH-005") {
				t.Fatalf("%s event with no verdicts[] = %+v, want SCH-005 (conditionally-required field missing)", transition, result.Violations)
			}
		})

		t.Run(transition+"_with_verdicts_is_accepted", func(t *testing.T) {
			t.Parallel()
			result, err := engine.ValidateEvent(verifyEventYAML(transition, validVerdictsBlock), "0.19.0")
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if !result.Valid {
				t.Fatalf("%s event with a valid verdicts[] rejected: %+v", transition, result.Violations)
			}
		})
	}

	t.Run("note_transition_does_not_require_verdicts", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateEvent(verifyEventYAML("note", ""), "0.19.0")
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if !result.Valid {
			t.Fatalf("note event with no verdicts[] rejected: %+v", result.Violations)
		}
	})
}

// TestValidateEvent_VerdictsEntryShapeIsEnforced pins the item schema
// itself: an unknown verdict enum value and an unknown property inside one
// entry are both refused by the real corpus.
func TestValidateEvent_VerdictsEntryShapeIsEnforced(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	t.Run("unknown_verdict_enum_value_is_rejected", func(t *testing.T) {
		t.Parallel()
		block := "verdicts:\n  - index: 0\n    verdict: maybe\n    cause_owner: axon\n"
		result, err := engine.ValidateEvent(verifyEventYAML("close", block), "0.19.0")
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if result.Valid {
			t.Fatal("verdict: maybe validated clean, want an enum rejection")
		}
	})

	t.Run("unknown_property_is_rejected", func(t *testing.T) {
		t.Parallel()
		block := "verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n    extra: nope\n"
		result, err := engine.ValidateEvent(verifyEventYAML("close", block), "0.19.0")
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if result.Valid {
			t.Fatal("verdicts[] entry with an unknown property validated clean, want additionalProperties rejection")
		}
	})

	t.Run("missing_cause_owner_is_rejected", func(t *testing.T) {
		t.Parallel()
		block := "verdicts:\n  - index: 0\n    verdict: met\n"
		result, err := engine.ValidateEvent(verifyEventYAML("close", block), "0.19.0")
		if err != nil {
			t.Fatalf("ValidateEvent: %v", err)
		}
		if result.Valid {
			t.Fatal("verdicts[] entry with no cause_owner validated clean, want a required-field rejection")
		}
	})
}
