package eventv2

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSchemaCompilesAndFixturesCloseTheV2Shape(t *testing.T) {
	t.Parallel()
	schema := compileSchema(t, "event.schema.json", "https://schemas.a2ahub.internal/event/v2/event.schema.json")

	valid := fixturePaths(t, "fixtures/valid/*.json")
	if len(valid) == 0 {
		t.Fatal("valid fixture corpus is empty")
	}
	for _, path := range valid {
		path := path
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
		path := path
		t.Run("invalid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			if err := schema.Validate(readInstance(t, path)); err == nil {
				t.Fatal("expected schema rejection")
			}
		})
	}
}

func TestV2PublishIsDistinctAndHistoricalV1RemainsReadable(t *testing.T) {
	t.Parallel()
	v1 := compileSchema(t, "../v1/event.schema.json", "https://schemas.a2ahub.internal/event/v1/event.schema.json")
	v2 := compileSchema(t, "event.schema.json", "https://schemas.a2ahub.internal/event/v2/event.schema.json")

	v2Publish := readInstance(t, "fixtures/valid/contract-publish.json")
	if err := v2.Validate(v2Publish); err != nil {
		t.Fatalf("v2 publish fixture rejected by v2: %v", err)
	}
	if err := v1.Validate(v2Publish); err == nil {
		t.Fatal("v2-only publish fixture was accepted by immutable v1 schema")
	}

	historical := map[string]any{
		"schema":     "event/v1",
		"event":      "01K1A2B3C4D5E6F7G8H9J0K1M7",
		"space":      "checkout-core",
		"subject":    "XC-atlas-order-envelope",
		"transition": "publish",
		"actor": map[string]any{
			"kind":   "agent",
			"name":   "legacy-publisher",
			"system": "atlas",
		},
		"at":      "2026-07-01T12:00:00Z",
		"version": "2.2.0",
		"digest":  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := v1.Validate(historical); err != nil {
		t.Fatalf("historical event/v1 rejected: %v", err)
	}
	if err := v2.Validate(historical); err == nil {
		t.Fatal("historical event/v1 was silently upgraded to v2")
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
