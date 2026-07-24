// This file adds the §5.4b computed-compatibility check (D-010, CC-080):
// for a declared minor/patch bump, every fixture that validated under the
// PRIOR contract version's schema must still validate under the NEW
// version's schema. It is a pure function of caller-resolved bytes (rails
// "pure core", D-F): CheckComputedCompatibility does no I/O and never
// logs. The caller (spec 37 T2's two consumers — `contract publish` and
// `validate --ci`) is responsible for reading the prior version's
// fixtures/valid/** and the new version's schema/** off disk (or git) and
// handing the raw bytes in here, keyed by their repo-relative paths;
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

	compiled := map[string]*schema.ExternalSchema{}
	var failures []CompatFailure

	for _, fixturePath := range fixtureKeys {
		raw := in.PriorFixtures[fixturePath]

		var probe any
		if err := json.Unmarshal(raw, &probe); err != nil {
			return brokenBaseline(fixturePath, fmt.Sprintf("fixture is not valid JSON: %v", err))
		}

		schemaPath, candidates, ok := mapFixtureToSchema(fixturePath, in.NewSchemas, schemaKeys)
		if !ok {
			return brokenBaseline(fixturePath, ambiguousMappingDetail(candidates))
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
	return CompatResult{
		Computed: true,
		Failures: failures,
		Violation: &Violation{
			Code:  "POL-007",
			Class: ClassPolicy,
			Message: fmt.Sprintf(
				"declared %s bump (%s -> %s) contradicts computed compatibility: %s no longer validate against the new schema",
				in.DeclaredBump, orPlaceholder(in.PriorVersion), orPlaceholder(in.NewVersion), strings.Join(names, ", "),
			),
			CCRef:    "CC-080",
			Severity: SeverityReject,
		},
	}
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

// mapFixtureToSchema is decision D-E's fixture -> schema mapping, done on
// base names (not literal path concatenation) because NewSchemas'/
// PriorFixtures' keys are full repo-relative paths whose directory
// prefixes this package does not otherwise know or assume:
//
//  1. fixtures/valid/<stem>.json -> schema/<stem>.schema.json, matched by
//     base name, if such an entry exists in NewSchemas.
//  2. else, if NewSchemas holds exactly one entry, use it.
//  3. else, ambiguous: ok=false, candidates is every NewSchemas key
//     (sorted) for the caller to name in its refusal.
func mapFixtureToSchema(fixturePath string, newSchemas map[string][]byte, sortedSchemaKeys []string) (schemaPath string, candidates []string, ok bool) {
	stem := strings.TrimSuffix(pathBaseName(fixturePath), ".json")
	want := stem + ".schema.json"
	for _, k := range sortedSchemaKeys {
		if pathBaseName(k) == want {
			return k, nil, true
		}
	}
	if len(newSchemas) == 1 {
		return sortedSchemaKeys[0], nil, true
	}
	return "", sortedSchemaKeys, false
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
