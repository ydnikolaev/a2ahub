// Every fixture in this file whose name says "reconstructed" is a
// RECONSTRUCTION built from the shape the plan's own D1/D3 prose
// describes (docs/features/active/agent-exchange-2026-08/plans/
// 06-incompleteness.plan.md, "the fixtures D1 and D3 name are not in this
// repo" amendment). Neither XS-seomatrix-20260801-p1pf (D1) nor the two
// 2026-08-01 work_request closes (D3) is a file in this checkout — both
// live in a private space this work does not clone. What follows is NOT
// the committed artifact; it is this wave's own text, shaped to match
// what the plan says the real one contains, so what the ACs actually test
// (a refusal before a field exists, an acceptance after) survives without
// the original bytes.
package validate

import (
	"strings"
	"testing"
)

// criteriaResolver is this file's own Resolver fake — it additionally
// implements ParentCriteriaCounter (incompleteness.go's consumer-side
// optional upgrade), which no OTHER fake in this package needs, so it is
// not shared with registry_test.go's fakeResolver.
type criteriaResolver struct {
	member   map[string]bool
	left     map[string]bool
	criteria map[string]int
}

func (r *criteriaResolver) KnownArtifact(id string) bool { return true }
func (r *criteriaResolver) Digest(string) (string, bool) { return "", false }
func (r *criteriaResolver) System(system string) (member, left bool) {
	return r.member[system], r.left[system]
}
func (r *criteriaResolver) AcceptanceCriteriaCount(parentID string) (int, bool) {
	n, ok := r.criteria[parentID]
	return n, ok
}

var _ Resolver = (*criteriaResolver)(nil)
var _ ParentCriteriaCounter = (*criteriaResolver)(nil)

func hasCodePrefix(vs []Violation, prefix string) bool {
	for _, v := range vs {
		if strings.HasPrefix(v.Code, prefix) {
			return true
		}
	}
	return false
}

// --- AC1: unmet[] index must resolve into the parent's acceptance_criteria ---

// TestCheckUnmetIndexRange_InRangeProducesNoViolation is the unit-level
// half of AC1: an unmet index that fits inside the parent's declared
// acceptance_criteria count produces nothing.
func TestCheckUnmetIndexRange_InRangeProducesNoViolation(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260808-p9d3"}
	instance := map[string]any{"unmet": []any{int64(0)}}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 1}}

	got := checkUnmetIndexRange(env, instance, resolver)
	if len(got) != 0 {
		t.Fatalf("expected no violations for an in-range index, got %+v", got)
	}
}

// TestCheckUnmetIndexRange_OutOfRangeRefuses is AC1's refusal half: an
// unmet index the parent's acceptance_criteria[] does not have names a
// criterion that does not exist.
func TestCheckUnmetIndexRange_OutOfRangeRefuses(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260808-p9d3"}
	instance := map[string]any{"unmet": []any{int64(5)}}
	resolver := &criteriaResolver{criteria: map[string]int{"XW-axon-20260808-p9d3": 1}}

	got := checkUnmetIndexRange(env, instance, resolver)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}
	if got[0].Code != "REF-018" {
		t.Fatalf("expected REF-018, got %q", got[0].Code)
	}
	if got[0].Severity != SeverityReject {
		t.Fatalf("expected SeverityReject, got %q", got[0].Severity)
	}
}

// TestCheckUnmetIndexRange_DormantWithoutParentCriteriaCounter pins the
// documented degrade: a Resolver that does not implement
// ParentCriteriaCounter (every concrete Resolver in production today —
// see incompleteness.go's package-doc Deviation) never fires this rule,
// even for a wildly out-of-range index. This is "cannot check", proven
// rather than assumed.
func TestCheckUnmetIndexRange_DormantWithoutParentCriteriaCounter(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260808-p9d3"}
	instance := map[string]any{"unmet": []any{int64(99)}}
	resolver := &fakeResolver{known: map[string]bool{}}

	got := checkUnmetIndexRange(env, instance, resolver)
	if len(got) != 0 {
		t.Fatalf("expected no violations without a ParentCriteriaCounter, got %+v", got)
	}
}

// TestUnmetIndexRange_EngineWiring is the end-to-end proof that
// engine.go's ValidateForSubmit actually reaches checkIncompleteness: the
// SAME out-of-range index, through the real Engine, with a
// ParentCriteriaCounter-capable Resolver supplied via LocalContext.
func TestUnmetIndexRange_EngineWiring(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	resolver := &criteriaResolver{
		member:   map[string]bool{"seomatrix": true},
		criteria: map[string]int{"XW-axon-20260808-p9d3": 1},
	}

	raw := []byte("---\n" + responseV2Base + `result: partial
unmet: [5]
blocked_by:
  reason_code: out-of-scope
  owner: seomatrix
  needs: bytes
` + "---\nBody.\n")

	result, err := engine.ValidateForSubmit(
		Draft{Path: "axon/exchanges/XS-axon-20260808-p9d3.md", Raw: raw},
		nil,
		LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected refusal for an out-of-range unmet index, got Valid=true")
	}
	if !hasCode(result.Violations, "REF-018") {
		t.Fatalf("expected REF-018 among violations, got %+v", result.Violations)
	}
}

// responseV2Base is the shared frontmatter every fixture below builds on:
// everything base.schema.json requires, plus `parent` (response's own
// required field). Matches internal/schema/versioned_corpus_test.go's
// responseV2Header field-for-field, kept as this package's own copy
// (unexported to each package) rather than importing internal/schema's
// test-only const.
const responseV2Base = `
schema: envelope/v2
id: XS-axon-20260808-p9d3
type: response
title: A valid v2 response
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260808-k3f9
actor: {kind: agent, name: codex}
created: "2026-08-08T08:40:00Z"
priority: p3
blocking: true
classification: internal
parent: XW-axon-20260808-p9d3
`

// --- AC8: a terminal transition closing a parent with an unmet criterion needs residue ---

// TestCheckResidue_D3_ClosingUnmetCriterionWithoutResidueRefuses is D3
// reconstructed: a response whose own unmet[] names a still-open
// criterion, submitted alongside a `close` event over its parent, with no
// residue naming where that criterion went. Both live work_requests in
// the plan's account were closed three weeks before their needed_by with
// open items named only in the closing note's prose; this is that shape
// without the original bytes.
func TestCheckResidue_D3_ClosingUnmetCriterionWithoutResidueRefuses(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260801-close1"}
	instance := map[string]any{"unmet": []any{int64(0)}}
	events := []CandidateEvent{
		{Subject: "XW-axon-20260801-close1", Transition: "close", Actor: Actor{Kind: "agent", Name: "codex", System: "axon"}},
	}

	got := checkResidue(env, instance, events)
	if len(got) != 1 {
		t.Fatalf("expected exactly one violation, got %+v", got)
	}
	if got[0].Code != "LFC-004" {
		t.Fatalf("expected LFC-004, got %q", got[0].Code)
	}
	if got[0].Severity != SeverityReject {
		t.Fatalf("expected SeverityReject, got %q", got[0].Severity)
	}
}

// TestCheckResidue_ResidueNamingTheCriterionIsAccepted is D3's other
// cell: the SAME closing event, over the SAME unmet criterion, is
// accepted once residue names where it went.
func TestCheckResidue_ResidueNamingTheCriterionIsAccepted(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260801-close1"}
	instance := map[string]any{
		"unmet": []any{int64(0)},
		"residue": []any{
			map[string]any{"criterion_index": int64(0), "carried_to": "XS-axon-20260822-q7d2"},
		},
	}
	events := []CandidateEvent{
		{Subject: "XW-axon-20260801-close1", Transition: "close", Actor: Actor{Kind: "agent", Name: "codex", System: "axon"}},
	}

	got := checkResidue(env, instance, events)
	if len(got) != 0 {
		t.Fatalf("expected no violations once residue names the criterion, got %+v", got)
	}
}

// TestCheckResidue_NoClosingEventProducesNoViolation proves the trigger is
// the CLOSING event, not merely an unmet criterion on its own — an
// ordinary in-flight partial response (no accompanying terminal
// transition) never needs residue.
func TestCheckResidue_NoClosingEventProducesNoViolation(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260801-close1"}
	instance := map[string]any{"unmet": []any{int64(0)}}

	got := checkResidue(env, instance, nil)
	if len(got) != 0 {
		t.Fatalf("expected no violations with no accompanying closing event, got %+v", got)
	}
}

// TestCheckResidue_SupersedeIsNotATerminalCloseForThisRule pins the
// plan's own distinction (2026-08-08 "Q3 is no longer an argument"
// amendment): `supersede` replaces a disputed response, a different act
// with a different owner than close/withdraw/cancel, and must never
// trigger this rule.
func TestCheckResidue_SupersedeIsNotATerminalCloseForThisRule(t *testing.T) {
	t.Parallel()
	env := envelope{Type: "response", Parent: "XW-axon-20260801-close1"}
	instance := map[string]any{"unmet": []any{int64(0)}}
	events := []CandidateEvent{
		{Subject: "XW-axon-20260801-close1", Transition: "supersede", Actor: Actor{Kind: "agent", Name: "codex", System: "axon"}},
	}

	got := checkResidue(env, instance, events)
	if len(got) != 0 {
		t.Fatalf("expected no violations for supersede, got %+v", got)
	}
}

// TestResidue_D3_EngineWiring is the end-to-end proof that
// ValidateForSubmit actually reaches checkResidue through the real
// Engine, over a fully schema-valid response document.
func TestResidue_D3_EngineWiring(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	resolver := &criteriaResolver{member: map[string]bool{"seomatrix": true}}

	raw := []byte("---\n" + `
schema: envelope/v2
id: XS-axon-20260801-cwr9
type: response
title: Reviewer sign-off is a draft candidate, native-TR confirmation still open
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260801-cwr9
actor: {kind: agent, name: codex}
created: "2026-08-01T10:00:00Z"
priority: p3
blocking: true
classification: internal
parent: XW-axon-20260801-close1
result: partial
unmet: [0]
blocked_by:
  reason_code: out-of-scope
  owner: seomatrix
  needs: judgement
` + "---\nRECONSTRUCTED (D3): a native-TR confirmation gating a downstream release was still open when this parent closed.\n")

	events := []CandidateEvent{
		{Subject: "XW-axon-20260801-close1", Transition: "close", Actor: Actor{Kind: "agent", Name: "codex", System: "axon"}},
	}

	result, err := engine.ValidateForSubmit(
		Draft{Path: "axon/exchanges/XS-axon-20260801-cwr9.md", Raw: raw},
		events,
		LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected refusal for a close over an unmet criterion with no residue, got Valid=true")
	}
	if !hasCode(result.Violations, "LFC-004") {
		t.Fatalf("expected LFC-004 among violations, got %+v", result.Violations)
	}
}

// --- D1 (schema level, replayed through the Engine): standing vs incompleteness ---

// d1P1pfFrontmatter is RECONSTRUCTED from the plan's own D1 account: "a
// live, merged response in which every acceptance criterion was answered
// and the shortfall is that the supplier is not entitled to make the
// values binding: 'TR values are DRAFT candidates, not a native-speaker
// sign-off.'" This is NOT XS-seomatrix-20260801-p1pf's committed text.
const d1P1pfFrontmatterHeader = `
schema: envelope/v2
id: XS-axon-20260808-p1pf
type: response
title: TR values are DRAFT candidates, not a native-speaker sign-off
space: getvisa
from: axon
to: [seomatrix]
thread: thread:axon-20260808-p1pf
actor: {kind: agent, name: codex}
created: "2026-08-08T09:00:00Z"
priority: p3
blocking: true
classification: internal
parent: XW-axon-20260808-p1pf
result: partial
`

const d1Body = "RECONSTRUCTED (D1, from the plan's own account, not XS-seomatrix-20260801-p1pf's committed bytes): every acceptance criterion is answered; the TR values supplied are draft candidates pending native-speaker sign-off, so the shortfall is standing, not completeness.\n"

// TestD1_PartialWithoutStandingIsRefused is the pre-fix regression this
// spec exists to close: before `standing` exists, this response — which
// answered EVERY criterion — has no way to say so and is refused anyway,
// by the schema's own `then.anyOf` (SCH-class, not a code this file
// mints).
func TestD1_PartialWithoutStandingIsRefused(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	resolver := &criteriaResolver{member: map[string]bool{"seomatrix": true}}

	raw := []byte("---\n" + d1P1pfFrontmatterHeader + "---\n" + d1Body)
	result, err := engine.ValidateForSubmit(
		Draft{Path: "axon/exchanges/XS-axon-20260808-p1pf.md", Raw: raw},
		nil,
		LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected refusal for a bare partial with no standing/unmet/blocked_by, got Valid=true")
	}
	if !hasCodePrefix(result.Violations, "SCH-") {
		t.Fatalf("expected a schema-class (SCH-) violation, got %+v", result.Violations)
	}
}

// TestD1_PartialWithStandingIsAccepted is AC6: the SAME response, with
// `standing: provisional` and an empty `unmet: []` naming the shortfall
// as standing rather than completeness, validates.
func TestD1_PartialWithStandingIsAccepted(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)
	resolver := &criteriaResolver{member: map[string]bool{"seomatrix": true}}

	raw := []byte("---\n" + d1P1pfFrontmatterHeader + "standing: provisional\nunmet: []\n" + "---\n" + d1Body)
	result, err := engine.ValidateForSubmit(
		Draft{Path: "axon/exchanges/XS-axon-20260808-p1pf.md", Raw: raw},
		nil,
		LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected acceptance for standing=provisional with empty unmet, got violations: %+v", result.Violations)
	}
}
