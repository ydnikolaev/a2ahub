package schema

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// corpusRoot is the schemas/ directory relative to this package (two
// levels up: internal/schema -> internal -> repo root -> schemas).
// Fixtures are deliberately excluded from the embedded schemas.FS
// (schemas/embed.go's doc comment), so this AC-401.2 gate test — and only
// this test — reads them straight from disk, path-relative, exactly as
// the plan Amendment (2026-07-21) specifies.
const corpusRoot = "../../schemas"

// TestAC401_2_SchemaTemplatePairing is the shift-left MED-3 gate (spec 03
// Amendment 2026-07-21, spec 02 Deferred/follow-ups): the 8 envelope type
// schemas must pair 1:1 with schemas/templates/v1/*.md.
func TestAC401_2_SchemaTemplatePairing(t *testing.T) {
	t.Parallel()

	schemaFiles, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v1/*.schema.json"))
	if err != nil {
		t.Fatalf("glob schemas: %v", err)
	}
	var schemaTypes []string
	for _, f := range schemaFiles {
		base := strings.TrimSuffix(filepath.Base(f), ".schema.json")
		if base == "base" {
			continue // base.schema.json is not a per-type schema
		}
		schemaTypes = append(schemaTypes, base)
	}
	sort.Strings(schemaTypes)

	templateFiles, err := filepath.Glob(filepath.Join(corpusRoot, "templates/v1/*.md"))
	if err != nil {
		t.Fatalf("glob templates: %v", err)
	}
	var templateTypes []string
	for _, f := range templateFiles {
		templateTypes = append(templateTypes, strings.TrimSuffix(filepath.Base(f), ".md"))
	}
	sort.Strings(templateTypes)

	if len(schemaTypes) != 8 {
		t.Fatalf("expected 8 envelope type schemas, got %d: %v", len(schemaTypes), schemaTypes)
	}
	if len(templateTypes) != 8 {
		t.Fatalf("expected 8 templates, got %d: %v", len(templateTypes), templateTypes)
	}

	want := EnvelopeTypes()
	if !equalSlices(schemaTypes, want) {
		t.Errorf("schema types = %v, want %v", schemaTypes, want)
	}
	if !equalSlices(templateTypes, want) {
		t.Errorf("template types = %v, want %v", templateTypes, want)
	}
}

// sidecarExpect mirrors the <fixture-name>.expect.yaml sidecar shape (spec
// 02: "each invalid fixture gets a SIDECAR file ... with at least code:
// SCH-###").
type sidecarExpect struct {
	Code string `yaml:"code"`
}

// TestAC401_2_FixtureRegistryClosure is the AC-401.2 gate's second half:
// every invalid-fixture sidecar across the whole schemas/** corpus cites a
// known registry code, AND every SCH- registry code is cited by at least
// one sidecar — both directions, no orphans either way (AC-201.7 per P2's
// registry.yaml header comment).
func TestAC401_2_FixtureRegistryClosure(t *testing.T) {
	t.Parallel()

	registryRaw, err := os.ReadFile(filepath.Join(corpusRoot, "errors/v1/registry.yaml"))
	if err != nil {
		t.Fatalf("read registry.yaml: %v", err)
	}
	registry, err := LoadRegistry(registryRaw)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}

	allSidecars, err := filepath.Glob(filepath.Join(corpusRoot, "*/v1/fixtures/invalid/*.expect.yaml"))
	if err != nil {
		t.Fatalf("glob sidecars: %v", err)
	}
	// The feedback family (schemas/feedback/v1, P25) is a SEPARATE code domain:
	// it is not an envelope (I1), owns its own feedback-local FB-### table
	// (schemas/feedback/v1/codes.yaml, spec 25 §11 A2), and is closure-checked
	// by internal/feedback's own test — NOT by this envelope-registry gate.
	// Excluding it here keeps this gate envelope-scoped, exactly as the registry
	// it reads is envelope-scoped.
	var sidecars []string
	for _, p := range allSidecars {
		if strings.Contains(filepath.ToSlash(p), "/feedback/") {
			continue
		}
		sidecars = append(sidecars, p)
	}
	if len(sidecars) == 0 {
		t.Fatal("expected at least one invalid-fixture sidecar under schemas/**/fixtures/invalid/")
	}

	cited := map[string]bool{}
	for _, path := range sidecars {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var sc sidecarExpect
		if err := yaml.Unmarshal(raw, &sc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if sc.Code == "" {
			t.Errorf("%s: sidecar has no `code:`", path)
			continue
		}
		if !registry.Has(sc.Code) {
			t.Errorf("%s: cites unknown registry code %q", path, sc.Code)
			continue
		}
		cited[sc.Code] = true
	}

	for _, code := range registry.CodesInClass("schema") {
		if !cited[code] {
			t.Errorf("registry code %q (schema class) is not cited by any invalid-fixture sidecar", code)
		}
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
