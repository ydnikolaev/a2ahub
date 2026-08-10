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
// Deliberately NOT closed by giving resolveOutOfRangeIndices the ParentOf
// hop this file adds below for verify's own sake (point 2 was): env.Parent,
// checkUnmetIndexRange's own subjectID, IS ALREADY the parent id — a shared
// hop would make REF-018 resolve the GRANDPARENT's criteria count instead
// and bounds-check against the wrong array the moment the two functions
// ever share it. The hop lives only in checkVerdictIndexRange, one level
// up, never inside the shared bounds-check itself.
//
// # Wave 25B (2026-08-10): wired, and REF-019's own asymmetry (point 2
// # below) closed with a real ParentOf, not just declared
//
// The three-way "dormant" account this comment used to carry is now
// out of date in every branch — recorded here as history, not status:
//
//  1. Was: "not wired at all". `Engine.validateEvent` now takes an optional
//     Resolver (eventproducer.go's `EventContext`, threaded through the new
//     `ValidateEventWithContext`) and calls checkVerdictIndexRange with
//     `probe.Subject`, the event's own `subject` field, and whatever
//     Resolver the caller supplied. `ValidateEvent`/`ValidateEventWithEvaluation`
//     keep their exact original two/three-argument signatures.
//
//     The wave that added this left REF-019 reachable from its own tests and
//     firing on NO production path — "a rule that is true and inert", the
//     exact defect T5 names, one layer further out than the one this file
//     was written to fix. The lead closed it rather than filing it, and the
//     honest inventory is short:
//
//     `internal/cli/cmd_validate_ci.go` — the merge-time path a space
//     actually pins, and the same caller P6 already had to reach for
//     REF-018 — now calls `ValidateEventWithContext` and offers the
//     `MirrorResolver` it had already constructed two hundred lines above.
//     `TestValidateCI_REF019FiresOnAnOutOfRangeVerdictIndex` proves it in
//     both directions and was watched failing with the resolver removed.
//
//     `internal/workcheckpoint/validator.go` keeps passing none, and that is
//     correct rather than pending: it validates work-checkpoint publish
//     events, never a `verify` or a `close`, so REF-019's conditional cannot
//     apply to anything it sees. Wiring a resolver there would buy a
//     capability with no reachable subject.
//
//  2. Was: "asymmetric even once wired... until a ParentOf(responseID)-shaped
//     capability exists". `cli.MirrorResolver` (adapters.go, this wave)
//     now implements `ResponseParentResolver` below, resolving a response id
//     to its own `parent` field by the SAME re-read pattern
//     `AcceptanceCriteriaCount` already established (index lookup ->
//     bounded re-read -> ParseFrontmatter -> decode). checkVerdictIndexRange
//     tries AcceptanceCriteriaCount(subjectID) first (the `close` case,
//     where subjectID already IS the parent); when that fails it tries the
//     ParentOf hop and retries once (the `verify` case). REF-019 can now
//     fire on BOTH transitions, given a Resolver that carries both
//     capabilities — no longer scoped to `close` only.
//
//  3. Was: "TestRegistryClosure will name REF-019 an orphan". Already false
//     when this wave started: registry_test.go:334 stubs
//     checkVerdictIndexRange directly, and schemas/errors/v1/registry.yaml
//     already carries the REF-019 row (both off this wave's allowlist,
//     verified present rather than re-added).
//
// `a2a verify`/`a2a close` also no longer write `event/v1` unconditionally —
// wave 25A (this same tree) floor-gated both to `event/v2` (with a
// `--verdict` flag) once the space's `min_binary_version` has crossed
// contract.ContractPublicationFloor (cmd_lifecycle.go's
// lifecycleEventSchema). Below that floor they still author event/v1, where
// `verdicts[]` cannot exist at all — additionalProperties:false refuses it
// structurally, independent of anything this file does.
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

// ResponseParentResolver is validate's own consumer-side optional upgrade
// (the same pattern ParentCriteriaCounter above already establishes),
// distinct from it and NOT layered underneath resolveOutOfRangeIndices —
// see this file's package doc for why the hop lives in checkVerdictIndexRange
// alone rather than in the shared bounds-check both REF-018 and REF-019
// could otherwise share.
//
// It resolves a RESPONSE id (a `verify` event's own `subject`, per fold's
// applyResponseScoped) to the parent it answers, so checkVerdictIndexRange
// can hop from a subject that is not itself criteria-bearing to the one
// that is. cli.MirrorResolver (adapters.go) is the one shipped
// implementation.
type ResponseParentResolver interface {
	// ParentOf reports responseID's own `parent` field and whether
	// responseID could be resolved to one at all. ok=false covers "unknown
	// id", "not a response", and "response carries no parent" alike —
	// exactly resolveOutOfRangeIndices' own "cannot check is not check
	// passed" rail, extended one hop earlier.
	ParentOf(responseID string) (parentID string, ok bool)
}

// checkVerdictIndexRange is REF-019 (AC7's index-integrity half): every
// entry in an EVENT's `verdicts[].index` must be a valid index into the
// declared parent's `acceptance_criteria[]` — subjectID is the event's own
// `subject` field (the caller's job to supply).
//
// Tries subjectID directly first (the `close` case: the event's subject IS
// the criteria-bearing parent). When that fails to resolve AND resolver also
// implements ResponseParentResolver, hops once from subjectID (a response
// id, the `verify` case) to its own parent and retries — see this file's
// package doc, point 2, for why this makes REF-019 reachable on both
// transitions rather than `close` alone.
func checkVerdictIndexRange(subjectID string, instance any, resolver Resolver) []Violation {
	indices, present := eventVerdictIndices(instance)
	if !present || len(indices) == 0 {
		return nil
	}
	// countedID is whichever id actually resolved a count — subjectID
	// itself (`close`) or, after the hop, its own parent (`verify`). The
	// violation below must name THIS id, never subjectID unconditionally:
	// a hopped `verify` event's subject is a response id, and reporting
	// "does not resolve to an entry in <response id>'s acceptance_criteria[]"
	// would misname the artifact whose array was actually bounds-checked.
	countedID := subjectID
	outOfRange, checked := resolveOutOfRangeIndices(resolver, subjectID, indices)
	if !checked {
		if parentResolver, ok := resolver.(ResponseParentResolver); ok {
			if parentID, found := parentResolver.ParentOf(subjectID); found {
				outOfRange, checked = resolveOutOfRangeIndices(resolver, parentID, indices)
				countedID = parentID
			}
		}
	}
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
				idx, countedID,
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
