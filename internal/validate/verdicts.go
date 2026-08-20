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
// outside [0, count). checkVerdictIndexRange (REF-019) calls it, and — as
// of defects-fix-2026-08 P4 — so does incompleteness.go's
// checkUnmetIndexRange (REF-018): that file's own inline loop is gone, and
// the "one guarantee, two implementations" duplication this comment used to
// warn about (this repo's own fold.go, broadcastAckPermitted's comment,
// records having already caused a bug from exactly this shape) is closed.
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
//
// # defects-fix-2026-08 P4 — REF-023, the completeness half
//
// docs/inbox/defects/04-the-verification-record-defaults-to-empty.md
// measured the real getvisa space: 21 acceptance criteria declared across
// three work_requests closed by binaries that HAD this file's REF-019
// range check, and only 6 were ever judged. Two closes carried
// `verdicts: []` over parents declaring 7 and 8 criteria — legal under the
// schema's own "no minItems floor" comment, and legal under
// checkVerdictIndexRange's own OLD short-circuit
// (`if !present || len(indices) == 0 { return nil }`), which returned
// before it ever asked whether the count it had just resolved was
// satisfied. checkVerdictCompleteness below (REF-023) is that question,
// asked: given the SAME resolved count this file already had, does
// verdicts[] name every one of them?
//
// It lives INSIDE checkVerdictIndexRange (called from it, not from a
// second engine.go hookpoint) for the same reason the ParentOf hop lives
// here rather than in the shared bounds function: every caller of this
// file — eventproducer.go, engine.go — is off this wave's allowlist, so a
// rule that needed a NEW call site could not ship at all. Riding the one
// call site REF-019 already has means REF-023 is reachable everywhere
// REF-019 already is, on day one, with zero new wiring.
//
// The id-addressed form (P3, base.schema.json's `{id, text}` shape) was
// enhancement-only here at the time this paragraph was written: no
// concrete Resolver implemented ParentCriteriaIDs below yet. That gap is
// CLOSED — cli.MirrorResolver (internal/cli/adapters.go:726) now asserts
// itself against ParentCriteriaIDs at compile time, so an id-addressed
// parent's completeness IS judged in production wherever a MirrorResolver
// is the Resolver in play. Kept here as history: the rule reached the
// MEASURED defect (docs/inbox/defects/04-the-verification-record-defaults-
// to-empty.md) before this closed too — both getvisa closes are over
// ordinal (bare-string) parents, and an ordinal parent's completeness needs
// nothing but the count this file already resolves.
package validate

import (
	"fmt"
	"strconv"
	"strings"
)

// resolveOutOfRangeIndices resolves subjectID's declared
// acceptance_criteria[] count via resolver (only when resolver also
// implements ParentCriteriaCounter — the same consumer-side optional
// upgrade incompleteness.go declares) and reports which of indices fall
// outside [0, count), alongside the resolved count itself — REF-023's
// completeness question (checkVerdictCompleteness below) needs the exact
// same count this function already resolves, and a second resolver
// round-trip to fetch it again would cost a real file read in production
// (cli.MirrorResolver re-reads and re-parses the parent's frontmatter per
// call, per its own AcceptanceCriteriaCount doc comment).
//
// checked=false covers every "cannot check" case alike: resolver does not
// implement ParentCriteriaCounter, or subjectID's count could not be
// resolved at all (unknown artifact, a kind that carries no
// acceptance_criteria, or a read/parse failure). Per ParentCriteriaCounter's
// own doc comment, "cannot check" is never treated as "check passed" — a
// caller that gets checked=false must not report a false negative by
// assuming zero violations means the index was verified in range.
//
// incompleteness.go's checkUnmetIndexRange (REF-018) and this file's
// checkVerdictIndexRange (REF-019) both call this — the ONE bounds
// implementation the two ranges share, per this file's own package doc.
func resolveOutOfRangeIndices(resolver Resolver, subjectID string, indices []int) (outOfRange []int, count int, checked bool) {
	counter, ok := resolver.(ParentCriteriaCounter)
	if !ok {
		return nil, 0, false
	}
	count, ok = counter.AcceptanceCriteriaCount(subjectID)
	if !ok {
		return nil, 0, false
	}
	for _, idx := range indices {
		if idx < 0 || idx >= count {
			outOfRange = append(outOfRange, idx)
		}
	}
	return outOfRange, count, true
}

// ParentCriteriaIDs is validate's own consumer-side optional upgrade,
// alongside ParentCriteriaCounter (incompleteness.go) and
// ResponseParentResolver (below): a Resolver that can also report the
// ORDERED list of criterion identifiers parentID's own
// acceptance_criteria[] declares, when that array carries P3's
// id-addressed form ({id, text} objects, base.schema.json) rather than the
// original ordinal form (bare strings). len(ids) equals the same count
// AcceptanceCriteriaCount would report for the same parentID; ids[i] is the
// id at position i.
//
// checkVerdictIndexRange uses it for two questions a bare count cannot
// answer for an id-addressed parent: resolving a `criterion`-keyed
// verdicts[] entry to its position (REF-019's own out-of-range question,
// extended to the id form — wave 2's probe found this shape entirely
// unchecked) and naming a missing criterion BY ID rather than by an
// ordinal position an id-addressed parent's own author never used
// (REF-023's message contract).
//
// ok=false covers every "cannot resolve" case alike — unresolvable
// parentID, a parse failure, or an ordinal-only acceptance_criteria[] that
// carries no ids at all — the same "cannot check is not check passed" rail
// ParentCriteriaCounter's own doc comment already establishes; never
// degrading to a slice of empty strings.
//
// cli.MirrorResolver (internal/cli/adapters.go:726) implements this —
// asserted at compile time via `var _ validate.ParentCriteriaIDs =
// (*MirrorResolver)(nil)` — so an id-addressed parent's verdicts[] DOES get
// a REF-019 id-range check and a REF-023 completeness verdict wherever a
// MirrorResolver is the Resolver in play (see this file's package doc,
// "P4 — REF-023" section, corrected).
type ParentCriteriaIDs interface {
	AcceptanceCriteriaIDs(parentID string) (ids []string, ok bool)
}

// resolveParentCriteriaIDs is ParentCriteriaIDs' own consumer-side type
// assertion, isolated here the same way resolveOutOfRangeIndices isolates
// ParentCriteriaCounter's — every caller degrades through this one place
// rather than repeating the assertion.
func resolveParentCriteriaIDs(resolver Resolver, parentID string) (ids []string, ok bool) {
	lister, ok := resolver.(ParentCriteriaIDs)
	if !ok {
		return nil, false
	}
	return lister.AcceptanceCriteriaIDs(parentID)
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

// verdictRef is one verdicts[] entry's own addressing — a bare index (P3's
// ordinal form) or a criterion id (P3's id-addressed form). Mutually
// exclusive per entry, per event.schema.json's own allOf ("naming BOTH
// index and criterion on one entry is refused"), so exactly one of
// hasIndex/criterion is meaningful.
type verdictRef struct {
	index    int
	hasIndex bool
	// criterion is non-empty exactly when hasIndex is false.
	criterion string
}

// checkVerdictIndexRange is REF-019 (AC7's index-integrity half) AND
// REF-023 (defects-fix-2026-08 P4's completeness half, riding this same
// call site — see this file's package doc): every entry in an EVENT's
// `verdicts[]` must resolve into the declared parent's
// `acceptance_criteria[]`, and every declared criterion must be named by
// at least one entry — subjectID is the event's own `subject` field (the
// caller's job to supply).
//
// Tries subjectID directly first (the `close` case: the event's subject IS
// the criteria-bearing parent). When that fails to resolve AND resolver also
// implements ResponseParentResolver, hops once from subjectID (a response
// id, the `verify` case) to its own parent and retries — see this file's
// package doc, point 2, for why this makes REF-019 reachable on both
// transitions rather than `close` alone.
func checkVerdictIndexRange(subjectID string, instance any, resolver Resolver) []Violation {
	refs, present := eventVerdictRefs(instance)
	if !present {
		return nil
	}

	var indices []int
	for _, r := range refs {
		if r.hasIndex {
			indices = append(indices, r.index)
		}
	}

	// countedID is whichever id actually resolved a count — subjectID
	// itself (`close`) or, after the hop, its own parent (`verify`). Every
	// violation below must name THIS id, never subjectID unconditionally:
	// a hopped `verify` event's subject is a response id, and reporting
	// "does not resolve to an entry in <response id>'s acceptance_criteria[]"
	// would misname the artifact whose array was actually bounds-checked.
	countedID := subjectID
	outOfRange, count, checked := resolveOutOfRangeIndices(resolver, subjectID, indices)
	if !checked {
		if parentResolver, ok := resolver.(ResponseParentResolver); ok {
			if parentID, found := parentResolver.ParentOf(subjectID); found {
				outOfRange, count, checked = resolveOutOfRangeIndices(resolver, parentID, indices)
				countedID = parentID
			}
		}
	}
	if !checked {
		return nil
	}

	var out []Violation
	invalidIdx := make(map[int]bool, len(outOfRange))
	for _, idx := range outOfRange {
		invalidIdx[idx] = true
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

	// Criterion (id) form: REF-019's own out-of-range question, extended to
	// the id-addressed shape wave 2's probe found entirely unchecked — an
	// id that does not resolve into countedID's own declared list names a
	// criterion that does not exist, exactly the fact the index-form check
	// above already reports, addressed differently. Only attempted when
	// resolver offers the ordered id list at all (ParentCriteriaIDs) —
	// cli.MirrorResolver does (adapters.go:726), so this is the live path
	// for that Resolver, not a dormant one.
	ids, haveIDs := resolveParentCriteriaIDs(resolver, countedID)
	unresolvedCriteria := map[string]bool{}
	if haveIDs {
		known := make(map[string]bool, len(ids))
		for _, id := range ids {
			known[id] = true
		}
		for _, r := range refs {
			if r.hasIndex || known[r.criterion] {
				continue
			}
			unresolvedCriteria[r.criterion] = true
			out = append(out, Violation{
				Code:  "REF-019",
				Class: ClassReferential,
				Path:  "verdicts",
				Message: fmt.Sprintf(
					"verdicts[] names criterion %q, which does not resolve to an entry in %s's acceptance_criteria[]",
					r.criterion, countedID,
				),
				Severity: SeverityReject,
			})
		}
	}

	out = append(out, checkVerdictCompleteness(countedID, count, refs, invalidIdx, ids, haveIDs, unresolvedCriteria)...)
	return out
}

// checkVerdictCompleteness is REF-023 (defects-fix-2026-08 P4, the next
// free referential code — see this file's package doc, "P4 — REF-023"):
// when count (already resolved by checkVerdictIndexRange above — resolving
// it a second time here would repeat exactly the round-trip
// resolveOutOfRangeIndices' own doc comment says this file avoids) is
// greater than zero and refs names fewer than count DISTINCT, VALID
// criteria, refuse and name every criterion left unjudged.
//
// count == 0 is the schema's own "no minItems floor" case — a parent that
// declares no acceptance_criteria[] at all stays expressible with an empty
// verdicts[] — and stays silent here, deliberately, matching AC4.
//
// An entry whose own index/criterion did not resolve (present in invalidIdx
// or unresolvedCriteria) does not count as "judged": REF-019 already names
// it wrong above, and letting a bogus entry also satisfy completeness would
// let it silently stand in for a real judgement.
//
// When refs carries a criterion-form entry and resolver offers no
// ParentCriteriaIDs (haveIDs=false — every Resolver that is not a
// cli.MirrorResolver, see that interface's own doc comment), completeness
// cannot be judged for
// THIS event at all: without the parent's own id list there is no way to
// tell which of its criteria a `criterion`-keyed entry actually names, so
// guessing would either wrongly clear a short id-addressed record or
// wrongly refuse a complete one. Both are worse than the "cannot check is
// not check passed" degrade this file already follows everywhere else, so
// this function returns nil rather than approximate. A verdicts[] array
// carrying ONLY index-form entries (including the empty array — zero
// entries of any form) is unaffected by this guard and is judged as
// before.
func checkVerdictCompleteness(parentID string, count int, refs []verdictRef, invalidIdx map[int]bool, ids []string, haveIDs bool, unresolvedCriteria map[string]bool) []Violation {
	if count == 0 {
		return nil
	}
	if !haveIDs {
		for _, r := range refs {
			if !r.hasIndex {
				return nil
			}
		}
	}

	judged := make(map[int]bool, count)
	for _, r := range refs {
		if r.hasIndex {
			if r.index >= 0 && r.index < count && !invalidIdx[r.index] {
				judged[r.index] = true
			}
			continue
		}
		if unresolvedCriteria[r.criterion] {
			continue
		}
		for i, id := range ids {
			if id == r.criterion {
				judged[i] = true
				break
			}
		}
	}

	if len(judged) >= count {
		return nil
	}

	var missing []string
	for i := 0; i < count; i++ {
		if judged[i] {
			continue
		}
		if haveIDs && i < len(ids) && ids[i] != "" {
			missing = append(missing, ids[i])
		} else {
			missing = append(missing, strconv.Itoa(i))
		}
	}

	return []Violation{{
		Code:  "REF-023",
		Class: ClassReferential,
		Path:  "verdicts",
		Message: fmt.Sprintf(
			"verdicts[] names %d of %d declared acceptance criteria in %s; missing: %s",
			len(judged), count, parentID, strings.Join(missing, ", "),
		),
		Severity: SeverityReject,
	}}
}

// eventVerdictRefs reads an EVENT instance's top-level `verdicts[]` field
// (event/v2/event.schema.json's shape: an array of `{index|criterion,
// verdict, cause_owner}` objects) into the addressing each entry names —
// the same decode rail incompleteness.go's responseUnmetRefs uses for
// `unmet[]` (both call parseVerdictRefs below, this file's ONE per-entry
// dual-form decode — see this file's package doc, "unmet[] needs the same
// shape; extract it rather than writing a second one"):
// schema.DecodeYAMLInstance decodes a YAML `!!int` scalar to Go int64,
// never float64, so this reads exactly that type for `index`, and a YAML
// `!!str` scalar as Go string for `criterion`.
//
// present=false means the field was absent or not the expected shape
// (degrade to "nothing to check", never an error) — present=true with a
// zero-length result means the field WAS an (possibly empty) array, which
// the schema's own "no minItems floor" comment permits (a close over a
// parent with no acceptance_criteria[] at all).
//
// Widened by defects-fix-2026-08 P4 (wave 2's own probe finding): the OLD
// eventVerdictIndices this replaces read only `entry["index"].(int64)` —
// an id-form entry (`{criterion: "ac3", ...}`, no `index` key) failed that
// type assertion and was silently `continue`d, so REF-019 never
// range-checked it and a completeness rule built on the old reader would
// have counted zero for a fully-judged id-addressed close. Every entry
// this function CAN read (either form) is now returned; an entry with
// neither key readable is still skipped — the schema class already flags
// that shape, and this function only reads the entries it can.
func eventVerdictRefs(instance any) (refs []verdictRef, present bool) {
	m, ok := instance.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := m["verdicts"].([]any)
	if !ok {
		return nil, false
	}
	return parseVerdictRefs(raw), true
}

// parseVerdictRefs is eventVerdictRefs' and responseUnmetRefs' shared
// per-entry decode: each raw entry names an index OR a criterion id, never
// neither. The two fields' WIRE shapes are not identical at the entry
// level — event.schema.json's `verdicts[]` entries are always objects
// (`{index|criterion, verdict, cause_owner}`), while response.schema.json's
// `unmet[]` is `anyOf` a bare-integer array OR an array of `{criterion}`
// objects (no `{index: N}` object form exists for `unmet[]` at all) — so
// this function accepts a raw entry that is EITHER a bare int64 (unmet[]'s
// index form) OR an object carrying "index" or "criterion" (verdicts[]'s
// both forms, and unmet[]'s/residue[]'s own id form). An entry that is
// neither is skipped — the schema class already flags that shape, and this
// function only reads the entries it can.
//
// Extracted from eventVerdictRefs' own former inline loop (P3 "one reader
// for both wire forms") so `unmet[]` reads the SAME dual-form decode
// `verdicts[]` already established, rather than a second parser this
// package's own rails would then have to keep in step by hand.
func parseVerdictRefs(raw []any) []verdictRef {
	out := make([]verdictRef, 0, len(raw))
	for _, v := range raw {
		if n, ok := v.(int64); ok {
			out = append(out, verdictRef{index: int(n), hasIndex: true})
			continue
		}
		entry, ok := v.(map[string]any)
		if !ok {
			// Neither a bare integer nor an object entry — the schema class
			// already flags this shape; this function only reads the
			// entries it can.
			continue
		}
		if n, ok := entry["index"].(int64); ok {
			out = append(out, verdictRef{index: int(n), hasIndex: true})
			continue
		}
		if s, ok := entry["criterion"].(string); ok && s != "" {
			out = append(out, verdictRef{criterion: s})
			continue
		}
	}
	return out
}
