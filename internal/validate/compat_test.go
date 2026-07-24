package validate

import (
	"os"
	"path/filepath"
	"testing"
)

// compatFixtureDir is the schemas/fixtures/compat/ corpus (§5.4b, CC-080)
// this test reuses — the same golden fixtures internal/e2e's
// compatFixtureValidates test drove before this phase promoted the check
// out of a _test.go (see this phase's Deviations report).
const compatFixtureDir = corpusRoot + "/fixtures/compat"

func mustReadCompatFixture(t *testing.T, rel string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(compatFixtureDir, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return raw
}

func TestCheckComputedCompatibility_MajorBumpNotChecked(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump:  "major",
		PriorVersion:  "1.0.0",
		NewVersion:    "2.0.0",
		PriorFixtures: map[string][]byte{"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`)},
		NewSchemas:    map[string][]byte{"schema/widget.schema.json": []byte(`{"type":"object"}`)},
	})

	if res.Computed {
		t.Fatal("expected Computed=false for a major bump (AC-970.3)")
	}
	if res.Reason == "" {
		t.Error("expected a non-empty Reason explaining why nothing was computed")
	}
	if res.Violation != nil {
		t.Errorf("expected no violation for a major bump, got %+v", res.Violation)
	}
	if len(res.Failures) != 0 {
		t.Errorf("expected no failures for a major bump, got %+v", res.Failures)
	}
}

func TestCheckComputedCompatibility_NoPriorFixturesIsNotAPass(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas:   map[string][]byte{"schema/widget.schema.json": []byte(`{"type":"object"}`)},
	})

	if res.Computed {
		t.Fatal("expected Computed=false when the prior version published no fixtures (D-B)")
	}
	if res.Reason == "" {
		t.Error("expected a non-empty, printable Reason (D-B: 'nothing to check' must never read as 'checked and compatible')")
	}
	if res.Violation != nil {
		t.Errorf("expected no violation when nothing was computed, got %+v", res.Violation)
	}
}

func TestCheckComputedCompatibility_AdditiveMinorPasses(t *testing.T) {
	t.Parallel()

	newSchema := mustReadCompatFixture(t, "additive-minor/new.schema.json")
	fixture := mustReadCompatFixture(t, "additive-minor/fixtures/valid/widget-1.json")

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas:   map[string][]byte{"schema/widget.schema.json": newSchema},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": fixture,
		},
	})

	if !res.Computed {
		t.Fatalf("expected Computed=true, got Reason=%q", res.Reason)
	}
	if len(res.Failures) != 0 {
		t.Errorf("expected zero failures for an additive minor bump, got %+v", res.Failures)
	}
	if res.Violation != nil {
		t.Errorf("expected no violation for an additive minor bump, got %+v", res.Violation)
	}
}

func TestCheckComputedCompatibility_MislabeledMinorFails(t *testing.T) {
	t.Parallel()

	newSchema := mustReadCompatFixture(t, "mislabeled-minor/new.schema.json")
	fixture := mustReadCompatFixture(t, "mislabeled-minor/fixtures/valid/widget-1.json")

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas:   map[string][]byte{"schema/widget.schema.json": newSchema},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": fixture,
		},
	})

	if !res.Computed {
		t.Fatalf("expected Computed=true (it WAS computed, and it failed), got Reason=%q", res.Reason)
	}
	if len(res.Failures) != 1 || res.Failures[0].Fixture != "fixtures/valid/widget-1.json" {
		t.Fatalf("expected exactly one failure naming the fixture, got %+v", res.Failures)
	}
	if res.Violation == nil || res.Violation.Code != "POL-007" {
		t.Fatalf("expected a POL-007 violation, got %+v", res.Violation)
	}
	if res.Violation.CCRef != "CC-080" {
		t.Errorf("expected CCRef CC-080, got %q", res.Violation.CCRef)
	}
	if res.Violation.Message == "" {
		t.Error("expected a non-empty Message naming the offending fixture")
	}
}

func TestCheckComputedCompatibility_PatchBumpAlsoChecked(t *testing.T) {
	t.Parallel()

	newSchema := mustReadCompatFixture(t, "mislabeled-minor/new.schema.json")
	fixture := mustReadCompatFixture(t, "mislabeled-minor/fixtures/valid/widget-1.json")

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump:  "patch",
		PriorVersion:  "1.0.0",
		NewVersion:    "1.0.1",
		NewSchemas:    map[string][]byte{"schema/widget.schema.json": newSchema},
		PriorFixtures: map[string][]byte{"fixtures/valid/widget-1.json": fixture},
	})

	if !res.Computed || res.Violation == nil || res.Violation.Code != "POL-007" {
		t.Fatalf("expected a patch bump to also be compat-checked and refused, got %+v", res)
	}
}

func TestCheckComputedCompatibility_MappingByExactStem(t *testing.T) {
	t.Parallel()

	newSchema := mustReadCompatFixture(t, "additive-minor/new.schema.json")
	other := []byte(`{"type": "object"}`) // an unrelated schema that must NOT be picked
	fixture := mustReadCompatFixture(t, "additive-minor/fixtures/valid/widget-1.json")

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas: map[string][]byte{
			"schema/widget-1.schema.json": newSchema,
			"schema/other.schema.json":    other,
		},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": fixture,
		},
	})

	if !res.Computed {
		t.Fatalf("expected Computed=true, got Reason=%q", res.Reason)
	}
	if len(res.Failures) != 0 {
		t.Errorf("expected the exact-stem match (schema/widget-1.schema.json) to be used, got failures %+v", res.Failures)
	}
}

func TestCheckComputedCompatibility_AmbiguousMappingIsPOL008(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas: map[string][]byte{
			"schema/a.schema.json": []byte(`{"type":"object"}`),
			"schema/b.schema.json": []byte(`{"type":"object"}`),
		},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`),
		},
	})

	if res.Computed {
		t.Fatal("expected Computed=false for an ambiguous fixture->schema mapping (D-E fail-closed)")
	}
	if res.Violation == nil || res.Violation.Code != "POL-008" {
		t.Fatalf("expected a POL-008 violation, got %+v", res.Violation)
	}
	if res.Reason == "" {
		t.Error("expected a non-empty Reason")
	}
}

// TestCheckComputedCompatibility_NoSchemasPublishedIsPOL008 covers the
// zero-candidates edge of mapFixtureToSchema/ambiguousMappingDetail: prior
// fixtures exist but NewSchemas is empty, so there is nothing to map to.
// This is fail-closed (D-B) at the compat-core level; the case of a
// contract that never published schema/**+fixtures/valid/** AT ALL is
// POL-009's territory (B1, `contract publish` — not this package, see
// this phase's Deviations report).
func TestCheckComputedCompatibility_NoSchemasPublishedIsPOL008(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump:  "minor",
		PriorVersion:  "1.0.0",
		NewVersion:    "1.1.0",
		NewSchemas:    map[string][]byte{},
		PriorFixtures: map[string][]byte{"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`)},
	})

	if res.Computed {
		t.Fatal("expected Computed=false when NewSchemas has no entries at all")
	}
	if res.Violation == nil || res.Violation.Code != "POL-008" {
		t.Fatalf("expected a POL-008 violation, got %+v", res.Violation)
	}
}

// TestCheckComputedCompatibility_DeletedSchemaIsNotASilentPass is the
// wave-A early audit's reproduction, kept as a regression.
//
// The prior version published two fixtures under the stem convention; the
// new version DELETED gadget's schema — a breaking change in a declared
// minor bump. Deciding the fixture->schema mapping per fixture, gadget-1
// found no stem match, fell through to "there is exactly one schema, use
// it", validated green against widget's schema (which it was never written
// against), and the whole publish reported Computed:true with no failures.
//
// TEETH: restore the per-fixture single-schema fallback in planMapping and
// this test reds — Computed:true, zero failures, no violation.
func TestCheckComputedCompatibility_DeletedSchemaIsNotASilentPass(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas: map[string][]byte{
			"schema/widget-1.schema.json": []byte(`{"type":"object"}`),
		},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`),
			"fixtures/valid/gadget-1.json": []byte(`{"totally":"unrelated","shape":true}`),
		},
	})

	if !res.Computed {
		t.Fatalf("expected the check to be COMPUTED (the batch is mappable — one fixture lost its schema, which is a verdict, not an unevaluatable baseline); got Reason=%q", res.Reason)
	}
	if res.Violation == nil || res.Violation.Code != "POL-007" {
		t.Fatalf("removing a schema in a minor bump must contradict the declared bump (POL-007); got %+v", res.Violation)
	}
	if len(res.Failures) != 1 || res.Failures[0].Fixture != "fixtures/valid/gadget-1.json" {
		t.Fatalf("expected exactly one failure naming gadget-1, got %+v", res.Failures)
	}
	if res.Failures[0].Schema != "" {
		t.Fatalf("a removed schema has no path to name; Schema = %q", res.Failures[0].Schema)
	}
}

// TestCheckComputedCompatibility_DuplicateSchemaBaseNameIsPOL008 covers
// the other half of the same audit finding: nothing constrains schema/**
// to be flat (internal/space/layout.go only names the directory), so two
// schemas can share a base name at different depths. Taking whichever
// sorts first would make the verdict depend on a directory name.
func TestCheckComputedCompatibility_DuplicateSchemaBaseNameIsPOL008(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas: map[string][]byte{
			"schema/legacy/widget-1.schema.json": []byte(`{"type":"object"}`),
			"schema/v2/widget-1.schema.json":     []byte(`{"type":"object","required":["name"]}`),
		},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`),
		},
	})

	if res.Computed {
		t.Fatalf("an ambiguous stem must refuse, not resolve to whichever schema sorts first; got Computed=true, failures=%+v", res.Failures)
	}
	if res.Violation == nil || res.Violation.Code != "POL-008" {
		t.Fatalf("expected POL-008, got %+v", res.Violation)
	}
}

// TestCheckComputedCompatibility_SingleSchemaFreelyNamedFixtures keeps
// case 2 of the mapping alive: a single-schema contract may name its
// fixtures anything, and every one of them validates against that schema.
// This is the shape of the repo's own schemas/fixtures/compat corpus, and
// the batch rule must not have broken it.
func TestCheckComputedCompatibility_SingleSchemaFreelyNamedFixtures(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas: map[string][]byte{
			"schema/widget.schema.json": []byte(`{"type":"object"}`),
		},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/example.json": []byte(`{"name":"Widget"}`),
			"fixtures/valid/another.json": []byte(`{"name":"Gadget"}`),
		},
	})

	if !res.Computed {
		t.Fatalf("a single-schema contract with freely-named fixtures must still be computed; Reason=%q", res.Reason)
	}
	if res.Violation != nil || len(res.Failures) != 0 {
		t.Fatalf("both fixtures validate against the one schema; got %+v / %+v", res.Violation, res.Failures)
	}
}

func TestCheckComputedCompatibility_UnparseableFixtureIsPOL008(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas:   map[string][]byte{"schema/widget.schema.json": []byte(`{"type":"object"}`)},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": []byte(`{not valid json`),
		},
	})

	if res.Computed {
		t.Fatal("expected Computed=false for an unparseable prior fixture (D-B fail-closed)")
	}
	if res.Violation == nil || res.Violation.Code != "POL-008" {
		t.Fatalf("expected a POL-008 violation, got %+v", res.Violation)
	}
}

func TestCheckComputedCompatibility_UncompilableSchemaIsPOL008(t *testing.T) {
	t.Parallel()

	res := CheckComputedCompatibility(CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas:   map[string][]byte{"schema/widget.schema.json": []byte(`{"type": `)},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": []byte(`{"name":"Widget"}`),
		},
	})

	if res.Computed {
		t.Fatal("expected Computed=false when the mapped schema fails to compile (D-B fail-closed)")
	}
	if res.Violation == nil || res.Violation.Code != "POL-008" {
		t.Fatalf("expected a POL-008 violation, got %+v", res.Violation)
	}
}

// TestCheckComputedCompatibility_Deterministic asserts the same input
// produces byte-identical output across repeated calls — a refusal
// message that reorders between runs is not diffable.
func TestCheckComputedCompatibility_Deterministic(t *testing.T) {
	t.Parallel()

	newSchema := mustReadCompatFixture(t, "mislabeled-minor/new.schema.json")
	fixtureA := mustReadCompatFixture(t, "mislabeled-minor/fixtures/valid/widget-1.json")

	in := CompatInput{
		DeclaredBump: "minor",
		PriorVersion: "1.0.0",
		NewVersion:   "1.1.0",
		NewSchemas:   map[string][]byte{"schema/widget.schema.json": newSchema},
		PriorFixtures: map[string][]byte{
			"fixtures/valid/widget-1.json": fixtureA,
			"fixtures/valid/widget-2.json": fixtureA,
			"fixtures/valid/widget-0.json": fixtureA,
		},
	}

	first := CheckComputedCompatibility(in)
	for i := 0; i < 5; i++ {
		got := CheckComputedCompatibility(in)
		if len(got.Failures) != len(first.Failures) {
			t.Fatalf("run %d: got %d failures, want %d", i, len(got.Failures), len(first.Failures))
		}
		for j := range first.Failures {
			if got.Failures[j] != first.Failures[j] {
				t.Fatalf("run %d: failures differ at %d: got %+v, want %+v", i, j, got.Failures[j], first.Failures[j])
			}
		}
		if got.Violation == nil || first.Violation == nil || got.Violation.Message != first.Violation.Message {
			t.Fatalf("run %d: violation message differs: got %+v, want %+v", i, got.Violation, first.Violation)
		}
	}

	want := []string{"fixtures/valid/widget-0.json", "fixtures/valid/widget-1.json", "fixtures/valid/widget-2.json"}
	if len(first.Failures) != len(want) {
		t.Fatalf("got %d failures, want %d", len(first.Failures), len(want))
	}
	for i, w := range want {
		if first.Failures[i].Fixture != w {
			t.Fatalf("failures not sorted: got %v, want fixture order %v", first.Failures, want)
		}
	}
}
