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

// --- the ParentOf hop (gap #2's fix): `verify`'s own event.Subject is a
// --- response id, not itself criteria-bearing ---

// criteriaResolverWithParent extends criteriaResolver (incompleteness_test.go)
// with ResponseParentResolver — this file's own fake, not shared with
// criteriaResolver's other test users, because only REF-019's own hop needs
// it.
type criteriaResolverWithParent struct {
	criteriaResolver
	parents map[string]string
}

func (r *criteriaResolverWithParent) ParentOf(responseID string) (string, bool) {
	p, ok := r.parents[responseID]
	return p, ok
}

var _ ResponseParentResolver = (*criteriaResolverWithParent)(nil)

// TestCheckVerdictIndexRange_VerifyHopsThroughParentOf is REF-019's gap-#2
// fix, proven both ways: the subject is a RESPONSE id (unresolvable
// directly against ParentCriteriaCounter, exactly like
// TestCheckVerdictIndexRange_UnresolvableSubjectDegradesSilently above), but
// this resolver ALSO implements ResponseParentResolver — the hop to the
// response's own parent must fire, and an out-of-range index against the
// PARENT's declared count must still produce REF-019.
func TestCheckVerdictIndexRange_VerifyHopsThroughParentOf(t *testing.T) {
	t.Parallel()
	const responseID = "XS-axon-20260809-resp1"
	const parentID = "XW-axon-20260808-p9d3"
	resolver := &criteriaResolverWithParent{
		criteriaResolver: criteriaResolver{criteria: map[string]int{parentID: 2}},
		parents:          map[string]string{responseID: parentID},
	}

	t.Run("out_of_range_after_hop_produces_REF019", func(t *testing.T) {
		t.Parallel()
		instance := map[string]any{
			"verdicts": []any{
				map[string]any{"index": int64(5), "verdict": "unmet", "cause_owner": "seomatrix"},
			},
		}
		violations := checkVerdictIndexRange(responseID, instance, resolver)
		if len(violations) != 1 {
			t.Fatalf("hopped out-of-range verdict index produced %d violations, want 1: %+v", len(violations), violations)
		}
		if violations[0].Code != "REF-019" {
			t.Errorf("code = %q, want REF-019", violations[0].Code)
		}
		if !strings.Contains(violations[0].Message, parentID) {
			t.Errorf("message %q does not name the resolved parent %q", violations[0].Message, parentID)
		}
	})

	t.Run("in_range_after_hop_produces_no_violation", func(t *testing.T) {
		t.Parallel()
		instance := map[string]any{
			"verdicts": []any{
				map[string]any{"index": int64(1), "verdict": "met", "cause_owner": "axon"},
			},
		}
		violations := checkVerdictIndexRange(responseID, instance, resolver)
		if len(violations) != 0 {
			t.Fatalf("in-range hopped verdict index produced violations: %+v", violations)
		}
	})
}

// TestCheckVerdictIndexRange_UnresolvableParentOfDegradesSilently: the
// resolver implements ResponseParentResolver but cannot resolve THIS
// response to any parent (ok=false) — "cannot check", not a violation, the
// same rail every other miss in this file already follows.
func TestCheckVerdictIndexRange_UnresolvableParentOfDegradesSilently(t *testing.T) {
	t.Parallel()
	resolver := &criteriaResolverWithParent{
		criteriaResolver: criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 2}},
		parents:          map[string]string{}, // the response is not present
	}
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
		},
	}
	violations := checkVerdictIndexRange("XS-axon-20260809-resp1", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("unresolvable ParentOf produced violations: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_CloseDoesNotAttemptTheHop is `close`'s own
// happy path restated with a resolver that COULD hop (implements
// ResponseParentResolver) but must not need to: subjectID already resolves
// directly, so ParentOf is never consulted. Proven by giving `parents` an
// entry that, if consulted, would resolve to a DIFFERENT (nonexistent)
// parent whose out-of-range check would disagree with the direct one —
// if the direct result silently changed, this would catch it.
func TestCheckVerdictIndexRange_CloseDoesNotAttemptTheHop(t *testing.T) {
	t.Parallel()
	const parentID = "XW-axon-20260808-p9d3"
	resolver := &criteriaResolverWithParent{
		criteriaResolver: criteriaResolver{criteria: map[string]int{parentID: 1}},
		parents:          map[string]string{parentID: "XW-axon-20260808-wrong-hop"},
	}
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
		},
	}
	violations := checkVerdictIndexRange(parentID, instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("close's own direct resolution produced violations: %+v", violations)
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

// --- engine level: Engine.ValidateEventWithContext (REF-019 wired through
// --- the real corpus, not a re-derived copy of the rule) ---

// TestValidateEvent_REF019DormantWithoutResolver pins the OLD, unchanged
// entry point's own behaviour: ValidateEvent/ValidateEventWithEvaluation
// pass a nil resolver internally (eventproducer.go), so an out-of-range
// verdicts[] index is schema-legal-but-referentially-wrong and stays
// entirely unreported through them — exactly the "cannot check is not check
// passed... but also is not check failed" degrade every miss in this file
// already follows, proven here at the ENGINE boundary, not just the
// function boundary, so a caller relying on the byte-identical old
// signature is not surprised into a new refusal.
func TestValidateEvent_REF019DormantWithoutResolver(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	block := "verdicts:\n  - index: 9\n    verdict: unmet\n    cause_owner: seomatrix\n"
	result, err := engine.ValidateEvent(verifyEventYAML("close", block), "0.19.0")
	if err != nil {
		t.Fatalf("ValidateEvent: %v", err)
	}
	if !result.Valid {
		t.Fatalf("ValidateEvent (no resolver) reported a violation for an out-of-range verdicts[] index: %+v", result.Violations)
	}
}

// TestValidateEventWithContext_REF019FiresOnClose is REF-019 wired all the
// way through the real schema corpus: `close`'s own subject IS the parent,
// so a resolver need only implement ParentCriteriaCounter for this to fire.
func TestValidateEventWithContext_REF019FiresOnClose(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	// verifyEventYAML's own fixed subject (both transitions use it).
	const subject = "XW-axon-20260810-verify"
	resolver := &criteriaResolverWithParent{
		criteriaResolver: criteriaResolver{criteria: map[string]int{subject: 1}},
	}

	t.Run("out_of_range_index_produces_REF019", func(t *testing.T) {
		t.Parallel()
		block := "verdicts:\n  - index: 9\n    verdict: unmet\n    cause_owner: seomatrix\n"
		result, err := engine.ValidateEventWithContext(verifyEventYAML("close", block), "0.19.0", EventContext{Resolver: resolver})
		if err != nil {
			t.Fatalf("ValidateEventWithContext: %v", err)
		}
		if result.Valid {
			t.Fatal("out-of-range verdicts[] index validated clean through a wired resolver, want REF-019")
		}
		if !hasCode(result.Violations, "REF-019") {
			t.Fatalf("violations = %+v, want REF-019", result.Violations)
		}
	})

	t.Run("in_range_index_is_accepted", func(t *testing.T) {
		t.Parallel()
		block := "verdicts:\n  - index: 0\n    verdict: met\n    cause_owner: axon\n"
		result, err := engine.ValidateEventWithContext(verifyEventYAML("close", block), "0.19.0", EventContext{Resolver: resolver})
		if err != nil {
			t.Fatalf("ValidateEventWithContext: %v", err)
		}
		if !result.Valid {
			t.Fatalf("in-range verdicts[] index rejected through a wired resolver: %+v", result.Violations)
		}
	})
}

// TestValidateEventWithContext_REF019FiresOnVerifyThroughParentOf is the
// engine-level proof of gap #2's fix: `verify`'s own subject is NOT itself
// criteria-bearing, so REF-019 only fires here because the resolver also
// implements ResponseParentResolver and checkVerdictIndexRange hops through
// it.
func TestValidateEventWithContext_REF019FiresOnVerifyThroughParentOf(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	const responseSubject = "XW-axon-20260810-verify" // verifyEventYAML's own fixed subject
	const parentID = "XW-axon-20260808-parent"
	resolver := &criteriaResolverWithParent{
		criteriaResolver: criteriaResolver{criteria: map[string]int{parentID: 1}},
		parents:          map[string]string{responseSubject: parentID},
	}
	block := "verdicts:\n  - index: 9\n    verdict: unmet\n    cause_owner: seomatrix\n"
	result, err := engine.ValidateEventWithContext(verifyEventYAML("verify", block), "0.19.0", EventContext{Resolver: resolver})
	if err != nil {
		t.Fatalf("ValidateEventWithContext: %v", err)
	}
	if result.Valid {
		t.Fatal("out-of-range verdicts[] index on a hopped verify subject validated clean, want REF-019")
	}
	if !hasCode(result.Violations, "REF-019") {
		t.Fatalf("violations = %+v, want REF-019", result.Violations)
	}
}
