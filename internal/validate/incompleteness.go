// P6 "incompleteness" (spec docs/features/active/agent-exchange-2026-08/
// specs/06-incompleteness.md, plan .../plans/06-incompleteness.plan.md):
// the schema already refuses a bare `partial`/`cannot` at the field-shape
// level (envelope/v2/response.schema.json's `then.anyOf` — SCH-class
// violations, mapped by schema_class.go, no new code needed for that
// half). What the schema CANNOT see is the PARENT this response answers,
// or the EVENTS accompanying a submission — both are cross-artifact facts
// only internal/validate's V2 (pre-write) invocation point ever holds.
// This file is that other half: two rules, both scoped to this wave's
// brief (AC1's index-range guard and AC8's residue guard — AC7's
// verdicts[]/cause_owner mirror is a separate file, not this wave's).
//
//   - REF-018 (reject): an `unmet[]` entry must be an INDEX that actually
//     resolves into the parent's `acceptance_criteria[]` — an
//     out-of-range index names a criterion that does not exist, which is
//     worse than prose (spec §7 AC4's own "never restate, never drift"
//     rationale, extended to bounds-checking the index itself).
//   - LFC-004 (reject): a `close`/`withdraw`/`cancel` event over this
//     response's own parent, submitted alongside a response that still
//     carries a non-met criterion (a non-empty `unmet[]`), requires a
//     `residue[]` entry naming where EACH such criterion went. Absent is
//     a refusal, never a default (D3: both live work_requests were closed
//     three weeks before their `needed_by` with open items named only in
//     prose, and a week later those items existed on no surface).
//
// # Deviation, CLOSED 2026-08-09 — REF-018 / LFC-004 are now in
// # schemas/errors/v1/registry.yaml
//
// Both codes are live: registry.yaml:166 (REF-018) and :186 (LFC-004), and
// schemas/errors/v1/history.tsv:20-21 lists both as `live` at commit
// 3c256c4f. The section below is the ORIGINAL deviation as written when
// this file shipped, before schemas/** carried the rows, kept because its
// reasoning (the registry's own numbering, the ceiling gate it was blocking)
// explains why the rows exist where they do. It is no longer a status
// report:
//
// # Deviation: REF-018 / LFC-004 are not yet in schemas/errors/v1/
// # registry.yaml
//
// schemas/** is off this wave's allowlist (per the brief), so this
// package cannot append the two rows itself. Both codes follow the
// registry's own numbering — REF-018 is the next free referential slot
// after REF-017 (P4, this same epic); LFC-004 is the next free lifecycle
// slot after LFC-003 (operational-confidence/P1) — and TestRegistryClosure
// (internal/validate/registry_test.go) stays green because it only checks
// codes on paths it itself exercises. scripts/check-operational-
// confidence.sh's check_history greps internal/validate/*.go (non-test)
// for literal "REF-###"/"LFC-###" codes and fails the whole ceiling until
// the registry and schemas/errors/v1/history.tsv both carry these two
// rows. Draft rows:
//
//	registry.yaml:
//	  - code: REF-018
//	    class: referential
//	    title: unmet[] index does not resolve to an entry in the parent's acceptance_criteria[]
//	    applies_to: envelope/v2 response `unmet[]` V2/V3 validation against the parent's declared acceptance_criteria (§7, AC1)
//	  - code: LFC-004
//	    class: lifecycle
//	    title: A terminal transition closes a parent carrying an unmet criterion with no residue naming where it went
//	    applies_to: internal/validate.checkResidue — a close/withdraw/cancel event over a response's parent, when that response's own unmet[] is non-empty and no residue[] entry covers every unmet index (§7, §11 "residue" amendment, AC8)
//
//	history.tsv (tab-separated):
//	  REF-018	live	agent-exchange-2026-08/P6	unmet[] index does not resolve to an entry in the parent's acceptance_criteria	<commit>	-
//	  LFC-004	live	agent-exchange-2026-08/P6	terminal transition closes a parent carrying an unmet criterion with no residue	<commit>	-
//
// # Deviation, CLOSED 2026-08-09 — both rules were dormant; both are now wired
//
// The section below is the ORIGINAL deviation as written when this file
// shipped, kept because its reasoning explains the shape of the fix. It is no
// longer a status report:
//
//   - REF-018 was wired through a Resolver that can read a parent's criteria
//     count (f6ce7df2).
//   - LFC-004 was wired through a submit adapter carrying a closing event over
//     the draft's own parent (9cd7f12a).
//
// The audit's decision — make criteria-counting a property of the resolver
// TYPE rather than of the construction site, so no site can get it wrong —
// SHIPPED on 2026-08-10. cli.MirrorResolver counts criteria itself and a
// compile-time assertion holds it to validate.ParentCriteriaCounter, so every
// site that constructs one inherits REF-018 rather than remembering it:
// cmd/a2a/wire.go's two paths, contract_p6_wiring.go, work_wiring.go, and
// internal/cli/cmd_validate_ci.go — the merge-time path a space actually pins.
// The cmd/a2a wrapper that used to carry the capability per-site is retired.
//
// The honest residue is now TWO sites this fix structurally cannot reach, and
// the paragraph above used to name only part of it. Both are filed:
//
//   - internal/mcp constructs its OWN resolver type (mcp/wire.go), so the MCP
//     surface keeps the gap the CLI just closed — epic-backlog B16. Porting the
//     method a second time would ship the rule twice, which is the shape
//     fold.go's own broadcastAckPermitted comment records as having already
//     caused a bug here; collapsing the two types is the real answer.
//   - `a2a data` gets dataNoopSubmitValidator, which runs NO V2 validation at
//     all — not a resolver missing a capability, a write path missing a
//     validator. Bigger and different — epic-backlog B15.
//
// Recorded because the two accountings of this residue that lived in this
// repository disagreed with each other, and the disagreement is what surfaced
// the sixth site nobody was counting.
//
// The original text follows.
//
// REF-018 needs to know how many acceptance_criteria[] entries the
// PARENT declares. internal/validate's Resolver (seam.go, off this wave's
// allowlist) exposes only KnownArtifact/Digest/System — no content
// lookup. Rather than widen Resolver (a seam.go edit) or import a fourth
// package (internal/space, forbidden by the brief's import rail), this
// file declares ParentCriteriaCounter below — a consumer-side OPTIONAL
// upgrade, the same pattern seam.go's own doc comment already establishes
// for this package ("internal/validate defines this interface... takes it
// via constructor DI") — and type-asserts ctx.Resolver against it. No
// concrete Resolver implements it today, so checkUnmetIndexRange's loop
// never runs in production; it is fully exercised only by this file's own
// tests, which supply a fake satisfying both interfaces. Fix: the lead
// adds an `AcceptanceCriteriaCount(parentID string) (int, bool)` method to
// seam.go's Resolver (or a concrete adapter implementing this file's
// narrower interface) plus a cmd/a2a implementation backed by the local
// mirror.
//
// LFC-004's trigger needs a CandidateEvent, in the SAME ValidateForSubmit
// call, whose Subject equals the response's own `parent` and whose
// Transition is close/withdraw/cancel. The engine-level signature
// supports arbitrary (Draft, []CandidateEvent) pairs, but today's only
// production caller — internal/cli/adapters.go's
// SubmitValidatorAdapter.ValidateSubmit — builds candidates per-draft by
// matching `event.Subject == draft's own probe.ID` (adapters.go:757).
// `close`/`withdraw`/`cancel` always target the PARENT's id, never the
// submitted response's own id, so that adapter can never attach one to a
// response draft's candidate list — this rule is provably reachable at
// the engine level (see incompleteness_test.go) but dormant through the
// one wired caller. Fix: a lead-level call, named here rather than
// silently assumed away — either the funnel batches a closing response
// together with its parent's close event under one Draft/events pair, or
// a dedicated close-command precheck (mirroring cmd/a2a's local V2
// precheck for other verbs) calls ValidateForSubmit directly with both.
package validate

import "fmt"

// ParentCriteriaCounter is validate's own consumer-side optional upgrade
// to Resolver (see this file's package-doc Deviation above for why it is
// declared here rather than widening seam.go's Resolver): a Resolver that
// can also report how many acceptance_criteria[] entries a parent
// artifact declares. A concrete Resolver that does not implement this
// interface simply never triggers checkUnmetIndexRange — degrade, never
// panic, matching this package's existing possession.go rail for
// type-assertion misses.
type ParentCriteriaCounter interface {
	// AcceptanceCriteriaCount reports how many entries parentID's own
	// `acceptance_criteria[]` declares, and whether parentID could be
	// resolved at all. ok=false (parent unresolvable) is deliberately
	// NOT a violation here — an unresolvable parent is REF-003/REF-004's
	// territory (referential.go's checkRefs-style rules), not this one's.
	AcceptanceCriteriaCount(parentID string) (count int, ok bool)
}

// terminalParentTransitions is D3's exact three-member set — close,
// withdraw, cancel — never widened to `supersede`, which the response
// kind's own table row (fold's responseRows(), Q3's 2026-08-08 amendment)
// uses for a DIFFERENT act (replacing a disputed response) with a
// different owner; conflating the two is the substitution this epic
// exists to end.
var terminalParentTransitions = map[string]bool{
	"close":    true,
	"withdraw": true,
	"cancel":   true,
}

// checkIncompleteness is this file's entry point: both AC1 (index range)
// and AC8 (residue) rules, run once per V2 submission. env/instance are
// the already-decoded response document; events are the candidates
// accompanying THIS submission (checkLifecycle's own input, reused here
// rather than re-threaded); resolver is ctx.Resolver, passed through
// unchanged so this file adds no new engine.go parameter.
func checkIncompleteness(env envelope, instance any, events []CandidateEvent, resolver Resolver) []Violation {
	var out []Violation
	out = append(out, checkUnmetIndexRange(env, instance, resolver)...)
	out = append(out, checkResidue(env, instance, events)...)
	return out
}

// checkUnmetIndexRange is AC1: every entry in a response's `unmet[]` must
// be a valid index into its parent's `acceptance_criteria[]`. Silently
// does nothing when resolver does not implement ParentCriteriaCounter (see
// this file's Deviation) or when the parent cannot be resolved at all —
// both are "cannot check", never "check passed".
//
// Bounds-checking itself is verdicts.go's resolveOutOfRangeIndices — the
// ONE implementation REF-018 (here) and REF-019 (verdicts.go's
// checkVerdictIndexRange) share as of defects-fix-2026-08 P4, closing the
// duplication this file's own comment used to name (env.Parent is passed
// as subjectID directly: this is the `close`/`verify`-response case where
// the response's OWN parent field already is the criteria-bearing id, so
// no ParentOf hop belongs here — that hop is checkVerdictIndexRange's own,
// never shared into this bounds check, per verdicts.go's package doc).
func checkUnmetIndexRange(env envelope, instance any, resolver Resolver) []Violation {
	unmet, present := responseUnmetIndices(instance)
	if !present || len(unmet) == 0 {
		return nil
	}
	outOfRange, count, checked := resolveOutOfRangeIndices(resolver, env.Parent, unmet)
	if !checked {
		return nil
	}
	var out []Violation
	for _, idx := range outOfRange {
		out = append(out, Violation{
			Code:     "REF-018",
			Class:    ClassReferential,
			Path:     "unmet",
			Message:  fmt.Sprintf("unmet[] names criterion index %d, which does not resolve to an entry in the parent's acceptance_criteria[] (%d declared)", idx, count),
			Severity: SeverityReject,
		})
	}
	return out
}

// checkResidue is AC8: a terminal transition (close/withdraw/cancel) over
// this response's own parent, submitted alongside a response that still
// carries a non-met criterion, requires a `residue[]` entry naming where
// EACH such criterion went. Scoped to what a single response instance can
// see (its own `unmet[]`) — see this file's package-doc Deviation for the
// cross-artifact aggregation (other responses, a verify event's
// `verdicts[]`) this narrower check does not attempt; that is AC7's
// territory, a different file.
func checkResidue(env envelope, instance any, events []CandidateEvent) []Violation {
	if env.Type != "response" {
		return nil
	}
	unmet, present := responseUnmetIndices(instance)
	if !present || len(unmet) == 0 {
		// Nothing this response itself marked non-met to carry forward.
		return nil
	}
	if !closesParent(env.Parent, events) {
		return nil
	}
	covered := residueCriterionIndices(instance)
	var out []Violation
	for _, idx := range unmet {
		if covered[idx] {
			continue
		}
		out = append(out, Violation{
			Code:     "LFC-004",
			Class:    ClassLifecycle,
			Path:     "residue",
			Message:  fmt.Sprintf("a terminal transition closes this response's parent while criterion %d is unmet and no residue names where it went", idx),
			Severity: SeverityReject,
		})
	}
	return out
}

// closesParent reports whether events contains a close/withdraw/cancel
// candidate whose Subject is parentID.
func closesParent(parentID string, events []CandidateEvent) bool {
	if parentID == "" {
		return false
	}
	for _, ev := range events {
		if terminalParentTransitions[ev.Transition] && ev.Subject == parentID {
			return true
		}
	}
	return false
}

// responseUnmetIndices reads instance's top-level `unmet[]` field
// (response.schema.json's shape: an array of non-negative integers).
// present=false means the field was absent or not the expected shape
// (degrade to "nothing to check", the same rail declaredAttachmentDigests
// uses in possession.go) — present=true with a zero-length result means
// the field WAS an (possibly empty) array, per D1's own "standing-only
// shortfall carries an empty unmet[]" case.
//
// schema.DecodeYAMLInstance (internal/schema/instance.go) decodes a YAML
// `!!int` scalar to Go int64, never float64 — this reads exactly that
// type, not a json.Unmarshal-shaped float64, because this package never
// re-parses frontmatter through a second decoder (rails: read `instance`
// as internal/schema already built it).
func responseUnmetIndices(instance any) (indices []int, present bool) {
	m, ok := instance.(map[string]any)
	if !ok {
		return nil, false
	}
	raw, ok := m["unmet"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]int, 0, len(raw))
	for _, v := range raw {
		n, ok := v.(int64)
		if !ok {
			// Not an integer entry — schema class (SCH-006/AC4's own
			// "restated in prose" refusal) already flags this shape; this
			// function only reads the entries it can, never errors.
			continue
		}
		out = append(out, int(n))
	}
	return out, true
}

// residueCriterionIndices reads instance's top-level `residue[]` field
// (response.schema.json's shape: `{criterion_index, carried_to}` objects)
// into the set of criterion_index values it names. A malformed or absent
// residue degrades to the empty set — "nothing named", which is exactly
// what checkResidue's refusal is watching for.
func residueCriterionIndices(instance any) map[int]bool {
	out := map[int]bool{}
	m, ok := instance.(map[string]any)
	if !ok {
		return out
	}
	raw, ok := m["residue"].([]any)
	if !ok {
		return out
	}
	for _, e := range raw {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		n, ok := em["criterion_index"].(int64)
		if !ok {
			continue
		}
		out[int(n)] = true
	}
	return out
}
