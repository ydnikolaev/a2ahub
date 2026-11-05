// This file adds the §5.4b computed-compatibility check (D-010, CC-080):
// for a declared minor/patch bump, every fixture that validated under the
// PRIOR contract version's schema must still validate under the NEW
// version's schema. It is a pure function of caller-resolved bytes (rails
// "pure core", D-F): CheckComputedCompatibility does no I/O and never
// logs. The caller (spec 37 T2's two consumers — `contract publish` and
// `validate --ci`) is responsible for reading the prior version's
// fixtures/valid/** and the new version's schema/** off disk (or git),
// plus the prior descriptor's conforms_to declarations, and handing them
// in here, keyed by their repo-relative paths;
// this package never opens a file or resolves a version itself.
//
// The only place that may compile an arbitrary, caller-supplied JSON
// Schema is internal/schema (D-F, the repo's sole jsonschema/v6 importer)
// — this file calls schema.CompileExternal, never the library directly.
package validate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// CompatInput is CheckComputedCompatibility's caller-resolved input.
type CompatInput struct {
	// DeclaredBump is the version bump the publish declares: "major",
	// "minor", or "patch".
	DeclaredBump string
	// PriorVersion is the contract version being compared FROM (for
	// diagnostics only).
	PriorVersion string
	// NewVersion is the contract version being published (for
	// diagnostics only).
	NewVersion string
	// NewSchemas is the NEW version's schema/** files: repo-relative path
	// -> raw file bytes.
	NewSchemas map[string][]byte
	// PriorFixtures is the PRIOR version's fixtures/valid/** files:
	// repo-relative path -> raw file bytes.
	PriorFixtures map[string][]byte
	// PriorConformsTo is the PRIOR version's descriptor-declared
	// fixture -> schema mapping (contract-set-v2's `conforms_to`): a
	// PriorFixtures key -> the NewSchemas key that fixture was declared to
	// conform to, in exactly the key spaces those two maps already use.
	// A fixture with an entry is mapped by it instead of by file stem; if
	// NewSchemas no longer holds the named schema, that schema was removed
	// (POL-007). A fixture without an entry (or with an empty value) falls
	// back to the stem rule. nil/empty means no fixture declared one, which
	// leaves every verdict exactly as the stem rule alone gives it.
	PriorConformsTo map[string]string
}

// CompatFailure is one prior-version fixture that no longer validates
// against its mapped new-version schema.
type CompatFailure struct {
	// Fixture is the offending fixture's repo-relative path (a
	// PriorFixtures key).
	Fixture string
	// Schema is the new-version schema's repo-relative path (a
	// NewSchemas key) the fixture was validated against.
	Schema string
	// Detail is a human-readable explanation of why the fixture no
	// longer validates.
	Detail string
}

// CompatResult is CheckComputedCompatibility's return value.
type CompatResult struct {
	// Computed reports whether a compatibility verdict was actually
	// reached. false means one of two operationally OPPOSITE things,
	// discriminated by Violation:
	//   - Violation == nil: nothing to check (a major bump, D-A/AC-970.3)
	//     or nothing WAS checked (no prior fixtures published, D-B) —
	//     the publish proceeds, and the caller prints Reason.
	//   - Violation != nil (always POL-008): the baseline itself is
	//     broken (unparseable fixture, uncompilable schema, ambiguous
	//     mapping, D-B's fail-closed half) — the publish is REFUSED.
	// Never treat Computed==false as a pass; always check Violation
	// before deciding whether to proceed.
	Computed bool
	// Reason is set when Computed == false: a complete sentence naming
	// why nothing was computed, for the caller to print verbatim (D-B).
	Reason string
	// Failures lists every prior fixture that failed to validate against
	// its mapped new schema. Non-nil only when Computed == true and at
	// least one fixture failed.
	Failures []CompatFailure
	// Violation is the policy-class finding a caller folds into its own
	// result: POL-007 when Computed == true and Failures is non-empty,
	// POL-008 when the baseline itself could not be evaluated (an
	// unparseable prior fixture, an uncompilable mapped schema, or an
	// ambiguous fixture->schema mapping — D-B's fail-closed half). nil
	// otherwise.
	Violation *Violation
}

// CheckComputedCompatibility is the §5.4b compat core (D-010, T2). See
// this file's header comment for what the caller must resolve before
// calling it, and CompatResult's doc comments for how to read Computed/
// Reason/Failures/Violation.
func CheckComputedCompatibility(in CompatInput) CompatResult {
	if in.DeclaredBump == "major" {
		return CompatResult{
			Computed: false,
			Reason:   "declared bump is major, which declares the change incompatible; computed compatibility is not checked for a major bump (§5.4b).",
		}
	}

	if len(in.PriorFixtures) == 0 {
		return CompatResult{
			Computed: false,
			Reason: fmt.Sprintf(
				"the prior version (%s) published no fixtures/valid/** files, so nothing was computed for the %s -> %s bump; this is not a pass.",
				orPlaceholder(in.PriorVersion), orPlaceholder(in.PriorVersion), orPlaceholder(in.NewVersion),
			),
		}
	}

	fixtureKeys := sortedKeys(in.PriorFixtures)
	schemaKeys := sortedKeys(in.NewSchemas)

	plan, planErr := planMapping(fixtureKeys, in.NewSchemas, schemaKeys, in.PriorConformsTo)
	if planErr != nil {
		return brokenBaseline(planErr.fixture, planErr.detail)
	}

	compiled := map[string]*schema.ExternalSchema{}
	var failures []CompatFailure

	// A fixture whose named schema is gone from the new version is not an
	// unevaluatable baseline — it is the breaking change itself, reported
	// as such, with an empty Schema because there is no longer one to name.
	for _, fixturePath := range plan.schemaRemoved {
		detail := "the schema this fixture validated against in the prior version is not published by the new version"
		if declared := in.PriorConformsTo[fixturePath]; declared != "" {
			detail = fmt.Sprintf("the schema this fixture declared conformance to in the prior version (%s) is not published by the new version", declared)
		}
		failures = append(failures, CompatFailure{Fixture: fixturePath, Detail: detail})
	}

	for _, fixturePath := range fixtureKeys {
		raw := in.PriorFixtures[fixturePath]

		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return brokenBaseline(fixturePath, fmt.Sprintf("fixture is not valid JSON: %v", err))
		}

		schemaPath, mapped := plan.schemaFor[fixturePath]
		if !mapped {
			continue // already reported above as a removed schema
		}

		sch, ok := compiled[schemaPath]
		if !ok {
			var err error
			sch, err = schema.CompileExternal(schemaPath, in.NewSchemas[schemaPath])
			if err != nil {
				return brokenBaseline(fixturePath, fmt.Sprintf("mapped schema %s failed to compile: %v", schemaPath, err))
			}
			compiled[schemaPath] = sch
		}

		if err := sch.ValidateInstance(raw); err != nil {
			failures = append(failures, CompatFailure{
				Fixture: fixturePath,
				Schema:  schemaPath,
				Detail:  err.Error(),
			})
		}
	}

	if len(failures) == 0 {
		return CompatResult{Computed: true}
	}

	names := make([]string, len(failures))
	for i, f := range failures {
		names[i] = f.Fixture
	}
	// A schema a fixture DECLARED (conforms_to) and the new version dropped is
	// named in the message, since the failure's Detail does not reach every
	// caller. Stem-mapped removals have no declared name and add nothing.
	removedNote := ""
	if removed := declaredRemovedSchemas(plan.schemaRemoved, in.PriorConformsTo); len(removed) > 0 {
		removedNote = fmt.Sprintf(" (schemas no longer published: %s)", strings.Join(removed, ", "))
	}
	return CompatResult{
		Computed: true,
		Failures: failures,
		Violation: &Violation{
			Code:  "POL-007",
			Class: ClassPolicy,
			Message: fmt.Sprintf(
				"declared %s bump (%s -> %s) contradicts computed compatibility: %s no longer validate against the new version's schemas%s",
				in.DeclaredBump, orPlaceholder(in.PriorVersion), orPlaceholder(in.NewVersion), strings.Join(names, ", "), removedNote,
			),
			CCRef:    "CC-080",
			Severity: SeverityReject,
		},
	}
}

// declaredRemovedSchemas lists, sorted and de-duplicated, the schemas the
// removed fixtures declared via conforms_to. Fixtures that went to
// schemaRemoved through the stem rule contribute nothing.
func declaredRemovedSchemas(removedFixtures []string, priorConformsTo map[string]string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, fixturePath := range removedFixtures {
		name := priorConformsTo[fixturePath]
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// brokenBaseline builds the CompatResult D-B's fail-closed half requires:
// the baseline itself (a prior fixture, or the schema it maps to) could
// not be evaluated at all, so nothing was computed and the publish is
// refused (POL-008) rather than silently skipped.
//
// No CCRef is set: CC-080 (registry's applies_to for both POL-007 and
// POL-008) is specifically "breaking change published as minor" — the
// case POL-007 reports. POL-008 is a different failure (the baseline
// itself is unusable, D-B), with no corner-case ID of its own in §12 —
// leaving CCRef empty here is deliberate, not an oversight.
func brokenBaseline(fixturePath, detail string) CompatResult {
	return CompatResult{
		Computed: false,
		Reason:   fmt.Sprintf("computed compatibility could not be evaluated: %s (%s)", detail, fixturePath),
		Violation: &Violation{
			Code:     "POL-008",
			Class:    ClassPolicy,
			Message:  fmt.Sprintf("computed compatibility could not be evaluated against the prior version's baseline: %s (%s)", detail, fixturePath),
			Severity: SeverityReject,
		},
	}
}

// mappingPlan is the fixture -> schema assignment for a WHOLE batch,
// decided once before any fixture is validated.
type mappingPlan struct {
	// schemaFor maps a fixture path to the NewSchemas key it validates
	// against. A fixture absent from this map is in schemaRemoved.
	schemaFor map[string]string
	// schemaRemoved lists fixtures whose schema the new version no longer
	// publishes (sorted): one declared by conforms_to and absent from
	// NewSchemas, or, under the stem convention, one with no stem match.
	schemaRemoved []string
}

// mappingRefusal is planMapping's fail-closed answer: the batch cannot be
// mapped at all, so nothing can be computed (D-B, POL-008).
type mappingRefusal struct{ fixture, detail string }

// planMapping is decision D-E's fixture -> schema mapping. It is a
// property of the BATCH, not of one fixture, and that is the whole point.
//
// Deciding per fixture — "no stem match? then fall back to the single
// schema" — produces a FALSE PASS, which the wave-A early audit
// reproduced: one schema `widget-1.schema.json`, two prior fixtures
// `widget-1.json` and `gadget-1.json`, where the new version DELETED
// gadget's schema. widget matched by stem; gadget fell through to "there
// is exactly one schema, use it", validated green against a schema that
// was never its own, and the whole publish reported Computed:true with
// zero failures — while a schema had in fact been removed in a minor bump.
//
// The descriptor's own declaration comes first: a fixture with a
// priorConformsTo entry names its schema outright, so it is mapped to that
// schema when NewSchemas has it and is a removed schema (POL-007) when it
// does not. File names play no part for such a fixture, which is what lets a
// multi-schema contract name its fixtures `<schema>--<case>.json`. Only the
// fixtures WITHOUT a declaration are left to the stem rule below, and the
// batch modes are chosen over that remaining set alone — a declared fixture
// neither counts as a stem match nor as an unmatched one.
//
// For that fallback set the mode is chosen once, from what the set as a
// whole looks like:
//
//  1. EVERY fixture matches a schema by stem (fixtures/valid/<stem>.json ->
//     schema/<stem>.schema.json, compared on base names since these keys
//     are full paths whose prefixes this package does not assume) — the
//     contract uses the stem convention; map each accordingly.
//  2. NO fixture matches by stem and there is exactly ONE schema — the
//     contract is single-schema and names its fixtures freely; every
//     fixture maps to that schema. This is the shape of the repo's own
//     compat corpus.
//  3. SOME match and some do not — the contract IS using the stem
//     convention, so a fixture with no match has lost its schema. That is
//     a breaking change, not an unmappable input: those fixtures go to
//     schemaRemoved and are reported as POL-007 failures.
//  4. Anything else (no stem matches, and zero or several schemas) —
//     genuinely unmappable, refuse with POL-008 naming the candidates.
//
// A stem that matches TWO schemas at different directory depths (nothing
// constrains schema/** to be flat) is ambiguous under every mode and
// refuses rather than silently taking the first — case 4. That refusal is
// raised for the fallback set only; a declared fixture never reaches it.
func planMapping(sortedFixtureKeys []string, newSchemas map[string][]byte, sortedSchemaKeys []string, priorConformsTo map[string]string) (mappingPlan, *mappingRefusal) {
	declared := map[string]string{}
	var removed, fallback []string
	for _, fixturePath := range sortedFixtureKeys {
		want := priorConformsTo[fixturePath]
		if want == "" {
			fallback = append(fallback, fixturePath)
			continue
		}
		if _, ok := newSchemas[want]; ok {
			declared[fixturePath] = want
		} else {
			removed = append(removed, fixturePath)
		}
	}

	matched := map[string]string{}
	var unmatched []string

	for _, fixturePath := range fallback {
		want := strings.TrimSuffix(pathBaseName(fixturePath), ".json") + ".schema.json"
		var hits []string
		for _, k := range sortedSchemaKeys {
			if pathBaseName(k) == want {
				hits = append(hits, k)
			}
		}
		switch len(hits) {
		case 0:
			unmatched = append(unmatched, fixturePath)
		case 1:
			matched[fixturePath] = hits[0]
		default:
			return mappingPlan{}, &mappingRefusal{
				fixture: fixturePath,
				detail: fmt.Sprintf(
					"fixture->schema mapping is ambiguous: %d schemas share the base name %s (%s)",
					len(hits), want, strings.Join(hits, ", "),
				),
			}
		}
	}

	schemaFor := declared // owned here; merge the stem matches into it
	for fixturePath, schemaPath := range matched {
		schemaFor[fixturePath] = schemaPath
	}

	switch {
	case len(unmatched) == 0: // case 1 (or nothing left to fall back on)
		sort.Strings(removed)
		return mappingPlan{schemaFor: schemaFor, schemaRemoved: removed}, nil

	case len(matched) == 0 && len(newSchemas) == 1: // case 2
		only := sortedSchemaKeys[0]
		for _, fixturePath := range unmatched {
			schemaFor[fixturePath] = only
		}
		sort.Strings(removed)
		return mappingPlan{schemaFor: schemaFor, schemaRemoved: removed}, nil

	case len(matched) > 0: // case 3
		removed = append(removed, unmatched...)
		sort.Strings(removed)
		return mappingPlan{schemaFor: schemaFor, schemaRemoved: removed}, nil

	default: // case 4
		return mappingPlan{}, &mappingRefusal{
			fixture: unmatched[0],
			detail:  ambiguousMappingDetail(sortedSchemaKeys),
		}
	}
}

// ambiguousMappingDetail renders mapFixtureToSchema's candidate list into
// the human-readable clause brokenBaseline's message embeds.
func ambiguousMappingDetail(candidates []string) string {
	if len(candidates) == 0 {
		return "no fixture->schema mapping: NewSchemas published no schema/** files at all"
	}
	return fmt.Sprintf("ambiguous fixture->schema mapping: candidates are [%s]", strings.Join(candidates, ", "))
}

// pathBaseName returns the final "/"-separated segment of a repo-relative
// path, without assuming any particular directory depth.
func pathBaseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// sortedKeys returns m's keys in sorted order — every map walk in this
// file uses it so a refusal message's fixture/candidate ordering never
// varies between runs (Go's own map iteration order is randomized).
func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// orPlaceholder returns s, or "(unspecified)" when s is empty — keeps
// Reason/Message strings readable even if a caller left a version field
// blank rather than producing "... ( -> ) ...".
func orPlaceholder(s string) string {
	if s == "" {
		return "(unspecified)"
	}
	return s
}
