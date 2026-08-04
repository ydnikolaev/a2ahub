// Package verificationreportv1 self-verifies the
// schemas/verification-report/v1/ family (spec 05a §6.1/§6.2): the schema
// compiles standalone (no network, no cross-document $ref), and every
// fixture under fixtures/valid and fixtures/invalid closes the shape it
// claims to — including the AC-15/§6.2 "result derivation" guard: a report
// whose result contradicts its own checks[] (fixtures/invalid/result-
// inconsistent-with-checks.json) must be rejected by this schema alone,
// with no Go code involved.
package verificationreportv1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemaCompilesAndFixturesCloseTheShape(t *testing.T) {
	t.Parallel()
	schema := compileSchema(t, "verification-report.schema.json", "https://schemas.a2ahub.internal/verification-report/v1/verification-report.schema.json")

	valid := fixturePaths(t, "fixtures/valid/*.json")
	if len(valid) == 0 {
		t.Fatal("valid fixture corpus is empty")
	}
	for _, path := range valid {
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(readInstance(t, path)); err != nil {
				t.Fatalf("expected valid: %v", err)
			}
		})
	}

	invalid := fixturePaths(t, "fixtures/invalid/*.json")
	if len(invalid) == 0 {
		t.Fatal("invalid fixture corpus is empty")
	}
	for _, path := range invalid {
		t.Run("invalid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(readInstance(t, path)); err == nil {
				t.Fatal("expected schema rejection")
			}
		})
	}
}

func compileSchema(t *testing.T, path, resource string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resource, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func fixturePaths(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func readInstance(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatal(err)
	}
	return instance
}
