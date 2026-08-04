// Package datapackagev1 self-verifies the schemas/data-package/v1/ family
// (spec 05a §6.1): the schema compiles standalone (no network, no
// cross-document $ref — it is a flat, non-envelope schema per the release-
// notes/v1 and known-issues/v1 precedent, not the envelope one), and every
// fixture under fixtures/valid and fixtures/invalid closes the shape it
// claims to.
package datapackagev1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemaCompilesAndFixturesCloseTheShape(t *testing.T) {
	t.Parallel()
	schema := compileSchema(t, "data-package.schema.json", "https://schemas.a2ahub.internal/data-package/v1/data-package.schema.json")

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
