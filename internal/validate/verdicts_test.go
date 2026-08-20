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
// exact index and REF-019. The parent declares 2 criteria and this event
// names only the one out-of-range entry, so REF-023 (P4's completeness
// rule, riding the same call site) ALSO fires — an invalid entry does not
// count as "judged" (verdicts.go's own doc comment on checkVerdictCompleteness),
// so this is asserted by CODE PRESENCE, not by an exact violation count.
func TestCheckVerdictIndexRange_OutOfRangeProducesREF019(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(4), "verdict": "unmet", "cause_owner": "seomatrix"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 2}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	v := violationWithCode(violations, "REF-019")
	if v == nil {
		t.Fatalf("out-of-range verdict index produced no REF-019: %+v", violations)
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
		// The parent declares 2 criteria and this event names only the
		// one out-of-range entry, so REF-023 (completeness) also fires —
		// asserted by code presence, matching
		// TestCheckVerdictIndexRange_OutOfRangeProducesREF019's own reasoning.
		instance := map[string]any{
			"verdicts": []any{
				map[string]any{"index": int64(5), "verdict": "unmet", "cause_owner": "seomatrix"},
			},
		}
		violations := checkVerdictIndexRange(responseID, instance, resolver)
		v := violationWithCode(violations, "REF-019")
		if v == nil {
			t.Fatalf("hopped out-of-range verdict index produced no REF-019: %+v", violations)
		}
		if !strings.Contains(v.Message, parentID) {
			t.Errorf("message %q does not name the resolved parent %q", v.Message, parentID)
		}
	})

	t.Run("in_range_after_hop_produces_no_violation", func(t *testing.T) {
		t.Parallel()
		// Names BOTH of the parent's 2 declared criteria, so REF-023
		// (completeness) does not also fire alongside the in-range REF-019
		// happy path this subtest exists to prove.
		instance := map[string]any{
			"verdicts": []any{
				map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
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
// responseUnmetRefs' own "present=false, not a violation" case for
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

// --- REF-023: completeness (defects-fix-2026-08 P4) ---

// TestCheckVerdictIndexRange_EmptyVerdictsOverDeclaredCriteriaRefusesREF023
// is the measured defect itself (docs/inbox/defects/
// 04-the-verification-record-defaults-to-empty.md): a close/verify over a
// parent declaring N>0 criteria with `verdicts: []` used to return before
// ever asking the question this test pins. Reconstructed at N=3 for a
// tight fixture — the real getvisa closes were over 7 and 8.
func TestCheckVerdictIndexRange_EmptyVerdictsOverDeclaredCriteriaRefusesREF023(t *testing.T) {
	t.Parallel()
	instance := map[string]any{"verdicts": []any{}}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 3}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	v := violationWithCode(violations, "REF-023")
	if v == nil {
		t.Fatalf("empty verdicts[] over a 3-criteria parent produced no REF-023: %+v", violations)
	}
	if v.Severity != SeverityReject {
		t.Errorf("severity = %q, want reject", v.Severity)
	}
	for _, want := range []string{"0", "1", "2"} {
		if !strings.Contains(v.Message, want) {
			t.Errorf("message %q does not name missing criterion %q", v.Message, want)
		}
	}
}

// TestCheckVerdictIndexRange_NMinusOneVerdictsNamesTheMissingOne is the
// spec's own worded acceptance criterion: N criteria, N-1 verdicts, refused
// naming the missing one.
func TestCheckVerdictIndexRange_NMinusOneVerdictsNamesTheMissingOne(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
			map[string]any{"index": int64(1), "verdict": "unmet", "cause_owner": "seomatrix"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 3}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	v := violationWithCode(violations, "REF-023")
	if v == nil {
		t.Fatalf("2 of 3 criteria judged produced no REF-023: %+v", violations)
	}
	if !strings.Contains(v.Message, "2") {
		t.Errorf("message %q does not name the missing criterion (index 2)", v.Message)
	}
	if violationWithCode(violations, "REF-019") != nil {
		t.Errorf("two well-formed in-range indices also tripped REF-019: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_FullyJudgedProducesNoCompletenessViolation is
// the completeness rule's own happy path: every declared criterion named,
// no REF-023.
func TestCheckVerdictIndexRange_FullyJudgedProducesNoCompletenessViolation(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "met", "cause_owner": "axon"},
			map[string]any{"index": int64(1), "verdict": "unmet", "cause_owner": "seomatrix"},
			map[string]any{"index": int64(2), "verdict": "not_warranted", "cause_owner": "axon"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 3}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("all 3 declared criteria judged produced violations: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_ZeroDeclaredCriteriaStaysSilent is AC4: a
// parent with no acceptance_criteria[] at all (count 0) is the schema's own
// "no minItems floor" case and REF-023 must stay silent, even for an empty
// verdicts[].
func TestCheckVerdictIndexRange_ZeroDeclaredCriteriaStaysSilent(t *testing.T) {
	t.Parallel()
	instance := map[string]any{"verdicts": []any{}}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 0}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("a parent declaring zero acceptance_criteria[] produced violations for an empty verdicts[]: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_NotExercisedSatisfiesCompleteness is AC6: the
// enum's not_exercised member is still a JUDGEMENT for completeness
// purposes — the whole reason the strict form is affordable (this file's
// package doc).
func TestCheckVerdictIndexRange_NotExercisedSatisfiesCompleteness(t *testing.T) {
	t.Parallel()
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"index": int64(0), "verdict": "not_exercised", "cause_owner": "axon"},
		},
	}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 1}}

	violations := checkVerdictIndexRange("XW-axon-20260808-p9d3", instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("a not_exercised verdict was treated as unjudged: %+v", violations)
	}
}

// criteriaResolverWithIDs extends criteriaResolver with ParentCriteriaIDs —
// REF-023's own id-addressed completeness path (and REF-019's id-form range
// check), exercised only by this file's own tests since no concrete
// Resolver implements it yet (ParentCriteriaIDs' own doc comment).
type criteriaResolverWithIDs struct {
	criteriaResolver
	ids map[string][]string
}

func (r *criteriaResolverWithIDs) AcceptanceCriteriaIDs(parentID string) ([]string, bool) {
	ids, ok := r.ids[parentID]
	return ids, ok
}

var _ ParentCriteriaIDs = (*criteriaResolverWithIDs)(nil)

// TestCheckVerdictIndexRange_IDAddressedFullyJudgedPasses is the id-form
// mirror of TestCheckVerdictIndexRange_FullyJudgedProducesNoCompletenessViolation.
func TestCheckVerdictIndexRange_IDAddressedFullyJudgedPasses(t *testing.T) {
	t.Parallel()
	const parentID = "XW-axon-20260808-idpar"
	resolver := &criteriaResolverWithIDs{
		criteriaResolver: criteriaResolver{criteria: map[string]int{parentID: 2}},
		ids:              map[string][]string{parentID: {"ac1", "ac2"}},
	}
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"criterion": "ac1", "verdict": "met", "cause_owner": "axon"},
			map[string]any{"criterion": "ac2", "verdict": "met", "cause_owner": "axon"},
		},
	}

	violations := checkVerdictIndexRange(parentID, instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("an id-addressed close naming every declared criterion was refused: %+v", violations)
	}
}

// TestCheckVerdictIndexRange_IDAddressedMissingOneRefusedByID proves the
// message contract: "by id where the parent declares ids" — the missing
// criterion is named "ac2", never its ordinal position 1.
func TestCheckVerdictIndexRange_IDAddressedMissingOneRefusedByID(t *testing.T) {
	t.Parallel()
	const parentID = "XW-axon-20260808-idpar"
	resolver := &criteriaResolverWithIDs{
		criteriaResolver: criteriaResolver{criteria: map[string]int{parentID: 2}},
		ids:              map[string][]string{parentID: {"ac1", "ac2"}},
	}
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"criterion": "ac1", "verdict": "met", "cause_owner": "axon"},
		},
	}

	violations := checkVerdictIndexRange(parentID, instance, resolver)
	v := violationWithCode(violations, "REF-023")
	if v == nil {
		t.Fatalf("an id-addressed close missing one criterion produced no REF-023: %+v", violations)
	}
	if !strings.Contains(v.Message, "ac2") {
		t.Errorf("message %q does not name the missing criterion by id (ac2)", v.Message)
	}
	if strings.Contains(v.Message, "missing: 1") {
		t.Errorf("message %q named the missing criterion by ordinal position, want its id", v.Message)
	}
}

// TestCheckVerdictIndexRange_IDAddressedUnknownIDRefusedByREF019 is the
// id-form's own out-of-range case (wave 2's probe finding, this file's
// package doc): a criterion id that does not resolve into the parent's
// declared list is refused by REF-019, the same fact the index-form check
// already reports, addressed differently.
func TestCheckVerdictIndexRange_IDAddressedUnknownIDRefusedByREF019(t *testing.T) {
	t.Parallel()
	const parentID = "XW-axon-20260808-idpar"
	resolver := &criteriaResolverWithIDs{
		criteriaResolver: criteriaResolver{criteria: map[string]int{parentID: 2}},
		ids:              map[string][]string{parentID: {"ac1", "ac2"}},
	}
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"criterion": "ac9", "verdict": "met", "cause_owner": "axon"},
		},
	}

	violations := checkVerdictIndexRange(parentID, instance, resolver)
	v := violationWithCode(violations, "REF-019")
	if v == nil {
		t.Fatalf("an unresolvable criterion id produced no REF-019: %+v", violations)
	}
	if !strings.Contains(v.Message, "ac9") {
		t.Errorf("message %q does not name the offending id ac9", v.Message)
	}
}

// TestCheckVerdictIndexRange_IDFormWithoutParentCriteriaIDsDegradesSilently
// is ParentCriteriaIDs' own "cannot check is not check passed" rail: a
// Resolver that resolves the COUNT (ParentCriteriaCounter) but not the id
// LIST (ParentCriteriaIDs — every production Resolver today) must not
// guess at a criterion-form entry's completeness. Guessing either
// direction is worse than silence: it could wrongly clear a short
// id-addressed record or wrongly refuse a complete one.
func TestCheckVerdictIndexRange_IDFormWithoutParentCriteriaIDsDegradesSilently(t *testing.T) {
	t.Parallel()
	const parentID = "XW-axon-20260808-idpar"
	resolver := &criteriaResolver{criteria: map[string]int{parentID: 2}}
	instance := map[string]any{
		"verdicts": []any{
			map[string]any{"criterion": "ac1", "verdict": "met", "cause_owner": "axon"},
		},
	}

	violations := checkVerdictIndexRange(parentID, instance, resolver)
	if len(violations) != 0 {
		t.Fatalf("id-addressed verdicts without a ParentCriteriaIDs-capable resolver produced a false violation: %+v", violations)
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
