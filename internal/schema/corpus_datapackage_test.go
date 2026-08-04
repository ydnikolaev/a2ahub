package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDataPackageAndVerificationReportLoadFromEmbed is the spec 05a §6.1
// "loader closure" requirement: both new families must resolve from the
// versioned embed with no network call and no cross-document $ref. Load()
// succeeding already proves "no network" (every resource in this corpus is
// added via Compiler.AddResource from schemas.FS; UseLoader is never
// called, so an unresolved $ref fails compilation instead of reaching out
// — see corpus.go's resourceURLPrefix doc comment). The cross-document-$ref
// half is checked explicitly below, because a same-document "#/$defs/..."
// $ref (which both schemas use, for their result-derivation and shared
// pattern defs) is legitimate and must NOT trip this guard — only a $ref
// naming another file would.
func TestDataPackageAndVerificationReportLoadFromEmbed(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	if _, ok := c.schemaFor(corpusKey{family: familyDataPackage, version: 1, typ: typeDataPackage}); !ok {
		t.Fatal("data-package/v1 did not compile into the corpus")
	}
	if _, ok := c.schemaFor(corpusKey{family: familyVerificationReport, version: 1, typ: typeVerificationReport}); !ok {
		t.Fatal("verification-report/v1 did not compile into the corpus")
	}
}

func TestDataPackageAndVerificationReportHaveNoCrossDocumentRef(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		filepath.Join(corpusRoot, "data-package", "v1", "data-package.schema.json"),
		filepath.Join(corpusRoot, "verification-report", "v1", "verification-report.schema.json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		for _, ref := range collectRefs(doc) {
			// A same-document reference is a JSON pointer fragment: it
			// starts with "#". Anything else names another file (or a
			// network URI), which is exactly what the loader-closure
			// requirement forbids for these two flat families.
			if len(ref) == 0 || ref[0] != '#' {
				t.Errorf("%s: cross-document $ref %q is forbidden for a flat, non-envelope family", path, ref)
			}
		}
	}
}

// collectRefs walks a decoded JSON document and returns every string value
// found under a "$ref" key.
func collectRefs(node any) []string {
	var refs []string
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "$ref" {
					if s, ok := child.(string); ok {
						refs = append(refs, s)
						continue
					}
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(node)
	return refs
}

// TestDataPackageFixtures makes the data-package/v1 fixture tree
// load-bearing through the corpus's own ValidateDataPackage entry point
// (schemas/data-package/v1/schema_test.go already proves the schema
// compiles standalone; this proves the corpus wiring — the
// corpusDefinitions row and embed.go's go:embed directive — actually
// reaches the same file).
func TestDataPackageFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	fixtureRoot := filepath.Join(corpusRoot, "data-package", "v1", "fixtures")

	valid, err := filepath.Glob(filepath.Join(fixtureRoot, "valid", "*.json"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(valid) == 0 {
		t.Fatal("no valid data-package fixtures found")
	}
	for _, f := range valid {
		violations, vErr := c.ValidateDataPackage(jsonFixtureInstance(t, f))
		if vErr != nil {
			t.Fatalf("%s: ValidateDataPackage: %v", f, vErr)
		}
		if len(violations) != 0 {
			t.Errorf("%s: expected a valid fixture, got %+v", filepath.Base(f), violations)
		}
	}

	invalid, err := filepath.Glob(filepath.Join(fixtureRoot, "invalid", "*.json"))
	if err != nil {
		t.Fatalf("glob invalid fixtures: %v", err)
	}
	if len(invalid) == 0 {
		t.Fatal("no invalid data-package fixtures found")
	}
	for _, f := range invalid {
		violations, vErr := c.ValidateDataPackage(jsonFixtureInstance(t, f))
		if vErr != nil {
			t.Fatalf("%s: ValidateDataPackage: %v", f, vErr)
		}
		if len(violations) == 0 {
			t.Errorf("%s: expected at least one violation, got none", filepath.Base(f))
		}
	}
}

// TestVerificationReportFixtures is TestDataPackageFixtures's sibling for
// verification-report/v1, including the AC-15/§6.2 result-derivation
// guard fixture (fixtures/invalid/result-inconsistent-with-checks.json)
// reached through the corpus this time, not the standalone schema test.
func TestVerificationReportFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	fixtureRoot := filepath.Join(corpusRoot, "verification-report", "v1", "fixtures")

	valid, err := filepath.Glob(filepath.Join(fixtureRoot, "valid", "*.json"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(valid) == 0 {
		t.Fatal("no valid verification-report fixtures found")
	}
	for _, f := range valid {
		violations, vErr := c.ValidateVerificationReport(jsonFixtureInstance(t, f))
		if vErr != nil {
			t.Fatalf("%s: ValidateVerificationReport: %v", f, vErr)
		}
		if len(violations) != 0 {
			t.Errorf("%s: expected a valid fixture, got %+v", filepath.Base(f), violations)
		}
	}

	invalid, err := filepath.Glob(filepath.Join(fixtureRoot, "invalid", "*.json"))
	if err != nil {
		t.Fatalf("glob invalid fixtures: %v", err)
	}
	if len(invalid) == 0 {
		t.Fatal("no invalid verification-report fixtures found")
	}
	for _, f := range invalid {
		violations, vErr := c.ValidateVerificationReport(jsonFixtureInstance(t, f))
		if vErr != nil {
			t.Fatalf("%s: ValidateVerificationReport: %v", f, vErr)
		}
		if len(violations) == 0 {
			t.Errorf("%s: expected at least one violation, got none", filepath.Base(f))
		}
	}
}

// jsonFixtureInstance reads a JSON fixture off disk and decodes it as a
// plain Go value — data-package/v1 and verification-report/v1 are wire
// documents (like event/v1 and event/v2), not YAML frontmatter artifacts,
// so this uses encoding/json directly rather than DecodeYAMLInstance.
func jsonFixtureInstance(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	return instance
}
