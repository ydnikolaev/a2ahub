package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"gopkg.in/yaml.v3"
)

// corpusRoot is the schemas/ directory relative to this package (two
// levels up: internal/validate -> internal -> repo root -> schemas).
// Fixtures are excluded from the embedded schemas.FS by design (schemas/
// embed.go), so every test in this file reads them straight from disk,
// path-relative — same convention as internal/schema's AC-401.2 test.
const corpusRoot = "../../schemas"

func mustEngine(t *testing.T) *Engine {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return New(corpus)
}

type sidecar struct {
	Code string `yaml:"code"`
}

// draftPathFor builds a synthetic repo-relative path that satisfies
// checkIDForm's filename/section guard for id, so the golden-fixture test
// below is isolated to schema-class violations (its purpose) and not
// coupled to how P2's fixture corpus happens to be laid out on disk
// (schemas/**/fixtures/**, which is not itself a "section" per §3.3/
// §4.2 — it's test data, not a real space).
func draftPathFor(id string) (string, error) {
	parsed, err := parseSystemFromID(id)
	if err != nil {
		return "", err
	}
	return parsed + "/exchanges/" + id + ".md", nil
}

// parseSystemFromID extracts the <system> token from a §3.3 ID
// (<PREFIX>-<system>-...) without re-implementing artifact.ParseID's
// full grammar — used only to build a matching synthetic path above.
func parseSystemFromID(id string) (string, error) {
	parts := strings.SplitN(id, "-", 3)
	if len(parts) < 2 {
		return "", fmt.Errorf("id %q has no system segment", id)
	}
	return parts[1], nil
}

// TestGoldenFixtures_Envelope is AC-201.1's V1 half: every P2 envelope
// golden fixture, run through Engine.ValidateDraft, is valid (zero
// violations) when it lives under fixtures/valid/, and fails with
// EXACTLY its sidecar's registry code when it lives under
// fixtures/invalid/.
func TestGoldenFixtures_Envelope(t *testing.T) {
	t.Parallel()
	engine := mustEngine(t)

	validFiles, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v1/fixtures/valid/*.md"))
	if err != nil {
		t.Fatalf("glob valid: %v", err)
	}
	if len(validFiles) == 0 {
		t.Fatal("expected at least one valid envelope fixture")
	}
	for _, f := range validFiles {
		f := f
		t.Run("valid/"+filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			id := idFromFrontmatter(t, raw)
			path, err := draftPathFor(id)
			if err != nil {
				t.Fatalf("draftPathFor(%s): %v", id, err)
			}
			result, err := engine.ValidateDraft(Draft{Path: path, Raw: raw})
			if err != nil {
				t.Fatalf("ValidateDraft: %v", err)
			}
			if !result.Valid {
				t.Fatalf("expected a valid fixture to pass, got violations: %+v", result.Violations)
			}
		})
	}

	invalidFiles, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v1/fixtures/invalid/*.md"))
	if err != nil {
		t.Fatalf("glob invalid: %v", err)
	}
	if len(invalidFiles) == 0 {
		t.Fatal("expected at least one invalid envelope fixture")
	}
	for _, f := range invalidFiles {
		f := f
		t.Run("invalid/"+filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			wantCode := sidecarCode(t, f+".expect.yaml")

			id := idFromFrontmatter(t, raw)
			path, err := draftPathFor(id)
			if err != nil {
				t.Fatalf("draftPathFor(%s): %v", id, err)
			}
			result, err := engine.ValidateDraft(Draft{Path: path, Raw: raw})
			if err != nil {
				t.Fatalf("ValidateDraft: %v", err)
			}
			if result.Valid {
				t.Fatalf("expected fixture to be invalid (code %s), got Valid=true", wantCode)
			}
			var codes []string
			for _, v := range result.Violations {
				codes = append(codes, v.Code)
			}
			if len(codes) != 1 || codes[0] != wantCode {
				t.Fatalf("expected EXACTLY [%s], got %v", wantCode, codes)
			}
		})
	}
}

// TestGoldenFixtures_EventManifestConsumes covers the three non-envelope
// product schemas (event/v1, manifest/v1, consumes/v1). These are not
// §3.3-IDed artifacts (no frontmatter wrapper, no filename/section
// guard), so they are exercised directly against internal/schema's
// per-family ValidateXxx + this package's own mapSchemaViolations —
// proving the same registry-code mapper is family-agnostic, without
// forcing them through the artifact-shaped Engine.ValidateDraft entry
// point (out of scope for how P6 ultimately wires event/manifest/
// consumes into the submit funnel).
func TestGoldenFixtures_EventManifestConsumes(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	families := []struct {
		name     string
		validate func(instance any) ([]schema.FieldViolation, error)
	}{
		{"event", func(i any) ([]schema.FieldViolation, error) { return corpus.ValidateEvent("v1", i) }},
		{"manifest", func(i any) ([]schema.FieldViolation, error) { return corpus.ValidateManifest("v1", i) }},
		{"consumes", func(i any) ([]schema.FieldViolation, error) { return corpus.ValidateConsumes("v1", i) }},
	}

	for _, fam := range families {
		fam := fam
		t.Run(fam.name, func(t *testing.T) {
			t.Parallel()

			validFiles := globFixtures(t, filepath.Join(corpusRoot, fam.name+"/v1/fixtures/valid/*.yaml"))
			if len(validFiles) == 0 {
				t.Fatalf("expected at least one valid %s fixture", fam.name)
			}
			for _, f := range validFiles {
				instance := decodeYAMLFile(t, f)
				fvs, err := fam.validate(instance)
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				if len(fvs) != 0 {
					t.Errorf("%s: expected valid, got field violations %+v", f, fvs)
				}
			}

			invalidFiles := globFixtures(t, filepath.Join(corpusRoot, fam.name+"/v1/fixtures/invalid/*.yaml"))
			for _, f := range invalidFiles {
				wantCode := sidecarCode(t, f+".expect.yaml")
				instance := decodeYAMLFile(t, f)
				fvs, err := fam.validate(instance)
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				violations, err := mapSchemaViolations(fvs)
				if err != nil {
					t.Fatalf("%s: mapSchemaViolations: %v", f, err)
				}
				var codes []string
				for _, v := range violations {
					codes = append(codes, v.Code)
				}
				if len(codes) != 1 || codes[0] != wantCode {
					t.Fatalf("%s: expected EXACTLY [%s], got %v", f, wantCode, codes)
				}
			}
		})
	}
}

func idFromFrontmatter(t *testing.T, raw []byte) string {
	t.Helper()
	body := string(raw)
	parts := strings.SplitN(body, "---\n", 3)
	if len(parts) < 3 {
		t.Fatalf("fixture is not frontmatter-shaped")
	}
	var m map[string]any
	if err := yaml.Unmarshal([]byte(parts[1]), &m); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("fixture has no `id` field")
	}
	return id
}

func sidecarCode(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sidecar %s: %v", path, err)
	}
	var sc sidecar
	if err := yaml.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("parse sidecar %s: %v", path, err)
	}
	if sc.Code == "" {
		t.Fatalf("sidecar %s has no code", path)
	}
	return sc.Code
}

func decodeYAMLFile(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("DecodeYAMLInstance %s: %v", path, err)
	}
	return instance
}

// globFixtures globs pattern and drops any `*.expect.yaml` sidecar that
// the glob's own "*.yaml" suffix incidentally also matches (a sidecar
// filename ends in ".yaml" too).
func globFixtures(t *testing.T, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	out := matches[:0]
	for _, m := range matches {
		if strings.HasSuffix(m, ".expect.yaml") {
			continue
		}
		out = append(out, m)
	}
	return out
}
