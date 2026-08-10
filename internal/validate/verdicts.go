// P6 wave C "verdicts[]" (spec docs/features/active/agent-exchange-2026-08/
// specs/06-incompleteness.md §7/§11's "the verifier has no word" amendment,
// threat-model.md T5, readiness audit row 25): the verifier's own mirror of
// AC1's `unmet[]` — a per-criterion `{index, verdict, cause_owner}` on the
// `verify`/`close` EVENT itself, indexed into the same parent
// `acceptance_criteria[]` unmet[] already addresses, so a false verdict has
// a named author rather than living only in a transition-free note.
//
// This file is deliberately separate from incompleteness.go — that file's
// own package doc says AC7's verdicts[]/cause_owner mirror "is a separate
// file, not this one," and this is it.
//
// # REF-019 shares REF-018's range rule; it does not copy it
//
// unmet[] and verdicts[] both index the same parent acceptance_criteria[]
// array. resolveOutOfRangeIndices below is the ONE bounds check both codes
// need — resolve a count via ParentCriteriaCounter, then flag every index
// outside [0, count). checkVerdictIndexRange (REF-019) calls it.
// checkUnmetIndexRange (REF-018, incompleteness.go) does NOT — that file is
// off this wave's allowlist, so its own inline loop is untouched. That is a
// real duplication this wave could not close: incompleteness.go:202-212's
// loop and this file's resolveOutOfRangeIndices are the same rule, twice,
// exactly the "one guarantee, two implementations" shape this repo's own
// fold.go (broadcastAckPermitted's comment) records as having already
// caused a bug. The fix is a two-line edit at incompleteness.go — replace
// checkUnmetIndexRange's loop body with a call to resolveOutOfRangeIndices
// — named here rather than silently left for someone to rediscover.
//
// # Dormant in three separate ways, and none of them is hidden behind a
// # silent "degrades to no violation"
//
//  1. Not wired at all. verdicts[] lives on the EVENT's own frontmatter, so
//     its natural validation home is ValidateEvent/validateEvent
//     (eventproducer.go) — which takes no Resolver today. Wiring this rule
//     in needs a Resolver parameter threaded through ValidateEvent and
//     ValidateEventWithEvaluation (engine.go, eventproducer.go), both off
//     this wave's allowlist. checkVerdictIndexRange below is therefore
//     exercised only by this file's own tests until the lead makes that
//     change — the same shape checkUnmetIndexRange shipped in before its
//     own Resolver gained AcceptanceCriteriaCount.
//  2. Asymmetric even once wired. `close`'s event.Subject IS the criteria-
//     bearing artifact (fold's exchangeRows: close departs from the
//     work_request/question itself), so AcceptanceCriteriaCount(subject)
//     resolves directly. `verify`'s event.Subject is the RESPONSE's own id
//     (fold's applyResponseScoped: "Subject is the response (XS) id, not
//     the primary artifact's own id" — cmd_lifecycle.go's VerifyCommand
//     writes Subject: responseID). A response carries no
//     acceptance_criteria of its own, so
//     AcceptanceCriteriaCount(responseID) degrades to ok=false — "cannot
//     check", per ParentCriteriaCounter's own doc rail — every time, not
//     as a bug but because nothing in today's Resolver seam can hop from a
//     response id to ITS OWN parent id (MirrorResolver's index carries only
//     Path/Thread/Digest per artifact — see cli/adapters.go). So this rule
//     can only ever fire on `close`, never on `verify`, until a
//     ParentOf(responseID)-shaped capability exists — a cli/adapters.go
//     change, off this wave's allowlist. Recorded here rather than left to
//     look like an oversight.
//  3. TestRegistryClosure will name REF-019 an orphan. internal/validate/
//     registry_test.go (off this wave's allowlist) requires every
//     referential/lifecycle/policy registry code to be produced by some
//     exercised path within that one test function's own local `produced`
//     map — the same mechanism that already carries REF-018/LFC-004 as
//     deliberately-dormant-but-reachable stubs. Adding REF-019 to
//     registry.yaml without a matching `record(checkVerdictIndexRange(...))`
//     line in registry_test.go reds that test. The fix is the same shape as
//     the REF-018/LFC-004 stubs already there (a criteriaResolver literal
//     with an out-of-range index) — a small, mechanical addition this
//     package's own allowlist does not extend to.
//
// Even more unreachable than the above: `a2a verify`/`a2a close` write
// `event/v1` today, never `event/v2` (cmd_lifecycle.go's VerifyCommand.Run
// and the close-authoring branch both set `Schema: "event/v1"`), so no
// event the shipped binary produces can carry `verdicts[]` at all yet — the
// schema's own conditional-required clause binds nothing until a future
// wave moves lifecycle event authoring to event/v2 for these two
// transitions. That is a product finding for this wave's report, not
// something this file can fix: cmd_lifecycle.go is off this wave's
// allowlist.
package validate

import "fmt"

// resolveOutOfRangeIndices resolves subjectID's declared
// acceptance_criteria[] count via resolver (only when resolver also
// implements ParentCriteriaCounter — the same consumer-side optional
// upgrade incompleteness.go declares) and reports which of indices fall
// outside [0, count).
//
// checked=false covers every "cannot check" case alike: resolver does not
// implement ParentCriteriaCounter, or subjectID's count could not be
// resolved at all (unknown artifact, a kind that carries no
// acceptance_criteria, or a read/parse failure). Per ParentCriteriaCounter's
// own doc comment, "cannot check" is never treated as "check passed" — a
// caller that gets checked=false must not report a false negative by
// assuming zero violations means the index was verified in range.
func resolveOutOfRangeIndices(resolver Resolver, subjectID string, indices []int) (outOfRange []int, checked bool) {
	counter, ok := resolver.(ParentCriteriaCounter)
	if !ok {
		return nil, false
	}
	count, ok := counter.AcceptanceCriteriaCount(subjectID)
	if !ok {
		return nil, false
	}
	for _, idx := range indices {
		if idx < 0 || idx >= count {
			outOfRange = append(outOfRange, idx)
		}
	}
	return outOfRange, true
}

// checkVerdictIndexRange is REF-019 (AC7's index-integrity half): every
// entry in an EVENT's `verdicts[].index` must be a valid index into the
// declared parent's `acceptance_criteria[]` — subjectID is the event's own
// `subject` field (the caller's job to supply; see this file's package doc,
// point 2, for why that resolves correctly only for `close`).
//
// Not yet called by any production path (package doc, point 1) — exercised
// today only by this file's own tests, which supply a fake resolver
// satisfying both Resolver and ParentCriteriaCounter (criteriaResolver,
// incompleteness_test.go, reused rather than redeclared in this package).
func checkVerdictIndexRange(subjectID string, instance any, resolver Resolver) []Violation {
	indices, present := eventVerdictIndices(instance)
	if !present || len(indices) == 0 {
		return nil
	}
	outOfRange, checked := resolveOutOfRangeIndices(resolver, subjectID, indices)
	if !checked {
		return nil
	}
	var out []Violation
	for _, idx := range outOfRange {
		out = append(out, Violation{
			Code:  "REF-019",
			Class: ClassReferential,
			Path:  "verdicts",
			Message: fmt.Sprintf(
				"verdicts[] names criterion index %d, which does not resolve to an entry in %s's acceptance_criteria[]",
				idx, subjectID,
			),
			Severity: SeverityReject,
		})
	}
	return out
}

// eventVerdictIndices reads an EVENT instance's top-level `verdicts[]`
// field (event/v2/event.schema.json's shape: an array of
// `{index, verdict, cause_owner}` objects) into the `index` values it
// names — the same decode rail incompleteness.go's responseUnmetIndices
// uses for `unmet[]`: schema.DecodeYAMLInstance decodes a YAML `!!int`
// scalar to Go int64, never float64, so this reads exactly that type.
//
// present=false means the field was absent or not the expected shape
// (degrade to "nothing to check", never an error) — present=true with a
// zero-length result means the field WAS an (possibly empty) array, which
// the schema's own "no minItems floor" comment permits (a close over a
// parent with no acceptance_criteria[] at all).
func eventVerdictIndices(instance any) (indices []int, present bool) {
	m, ok := instance.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := m["verdicts"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		entry, ok := v.(map[string]any)
		if !ok {
			// Not an object entry — the schema class already flags this
			// shape; this function only reads the entries it can.
			continue
		}
		n, ok := entry["index"].(int64)
		if !ok {
			continue
		}
		out = append(out, int(n))
	}
	return out, true
}
