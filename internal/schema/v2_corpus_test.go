package schema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/schemas"
)

func TestContractV2GoldenFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	root := filepath.Join(corpusRoot, "envelope/v2/fixtures")

	validPaths, err := filepath.Glob(filepath.Join(root, "valid/XC-*.md"))
	if err != nil {
		t.Fatalf("glob valid contract/v2 fixtures: %v", err)
	}
	if len(validPaths) == 0 {
		t.Fatal("expected at least one valid contract/v2 fixture")
	}
	for _, path := range validPaths {
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEnvelope("contract", "v2", readEnvelopeFixture(t, path))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("expected valid contract/v2 fixture, got %+v", violations)
			}
		})
	}

	wantKeywords := map[string]string{
		"XC-axon-invalid-additional-entry-field.md": keywordFalseSchema,
		"XC-axon-invalid-empty-artifacts.md":        "minItems",
		"XC-axon-invalid-extra-conforms.md":         keywordFalseSchema,
		"XC-axon-invalid-json-media-type.md":        "enum",
		"XC-axon-invalid-missing-artifacts.md":      "required",
		"XC-axon-invalid-missing-conforms.md":       "required",
		"XC-axon-invalid-too-many-artifacts.md":     "maxItems",
		"XC-axon-invalid-unknown-role.md":           "enum",
		"XC-axon-invalid-unsafe-path.md":            "pattern",
		"XC-axon-invalid-wrong-root.md":             "pattern",
	}
	invalidPaths, err := filepath.Glob(filepath.Join(root, "invalid/XC-*.md"))
	if err != nil {
		t.Fatalf("glob invalid contract/v2 fixtures: %v", err)
	}
	if len(invalidPaths) != len(wantKeywords) {
		t.Fatalf("invalid contract/v2 fixture count = %d, want %d", len(invalidPaths), len(wantKeywords))
	}
	for _, path := range invalidPaths {
		name := filepath.Base(path)
		wantKeyword, ok := wantKeywords[name]
		if !ok {
			t.Fatalf("invalid contract/v2 fixture %q has no keyword expectation", name)
		}
		if _, err := os.Stat(path + ".expect.yaml"); err != nil {
			t.Fatalf("invalid contract/v2 fixture %q has no sidecar: %v", name, err)
		}
		t.Run("invalid/"+name, func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEnvelope("contract", "v2", readEnvelopeFixture(t, path))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if len(violations) != 1 {
				t.Fatalf("expected exactly one violation, got %+v", violations)
			}
			if violations[0].Keyword != wantKeyword {
				t.Fatalf("keyword = %q, want %q: %+v", violations[0].Keyword, wantKeyword, violations[0])
			}
		})
	}
}

func TestContractV2FixtureIsRejectedByDistinctV1Decoder(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	v1 := c.schemas[corpusKey{family: familyEnvelope, version: 1, typ: "contract"}]
	v2 := c.schemas[corpusKey{family: familyEnvelope, version: 2, typ: "contract"}]
	if v1 == nil || v2 == nil || v1 == v2 {
		t.Fatalf("contract decoder identity v1=%p v2=%p; want two concrete objects", v1, v2)
	}

	paths, err := filepath.Glob(filepath.Join(corpusRoot, "envelope/v2/fixtures/valid/XC-*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob valid contract/v2 fixture: paths=%v err=%v", paths, err)
	}
	instance := readEnvelopeFixture(t, paths[0])
	violations, err := c.ValidateEnvelope("contract", "v2", instance)
	if err != nil || len(violations) != 0 {
		t.Fatalf("contract/v2 decoder rejected its fixture: violations=%+v err=%v", violations, err)
	}
	violations, err = c.ValidateEnvelope("contract", "v1", instance)
	if err != nil {
		t.Fatalf("contract/v1 decoder: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("contract/v1 decoder accepted a concrete contract/v2 fixture")
	}
}

func TestAnnouncementV2GoldenFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	root := filepath.Join(corpusRoot, "envelope/v2/fixtures")

	validPaths, err := filepath.Glob(filepath.Join(root, "valid/XA-*.md"))
	if err != nil {
		t.Fatalf("glob valid announcement/v2 fixtures: %v", err)
	}
	if len(validPaths) == 0 {
		t.Fatal("expected at least one valid announcement/v2 fixture")
	}
	for _, path := range validPaths {
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEnvelope("announcement", "v2", readEnvelopeFixture(t, path))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("expected valid announcement/v2 fixture, got %+v", violations)
			}
		})
	}

	invalidPaths, err := filepath.Glob(filepath.Join(root, "invalid/XA-*.md"))
	if err != nil {
		t.Fatalf("glob invalid announcement/v2 fixtures: %v", err)
	}
	if len(invalidPaths) == 0 {
		t.Fatal("expected at least one invalid announcement/v2 fixture")
	}
	for _, path := range invalidPaths {
		name := filepath.Base(path)
		if _, err := os.Stat(path + ".expect.yaml"); err != nil {
			t.Fatalf("invalid announcement/v2 fixture %q has no sidecar: %v", name, err)
		}
		t.Run("invalid/"+name, func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEnvelope("announcement", "v2", readEnvelopeFixture(t, path))
			if err != nil {
				t.Fatalf("ValidateEnvelope: %v", err)
			}
			if len(violations) == 0 {
				t.Fatal("expected announcement/v2 fixture rejection")
			}
		})
	}
}

func TestEventV2CorpusFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)
	root := filepath.Join(corpusRoot, "event/v2/fixtures")

	validPaths, err := filepath.Glob(filepath.Join(root, "valid/*.json"))
	if err != nil {
		t.Fatalf("glob valid event/v2 fixtures: %v", err)
	}
	if len(validPaths) == 0 {
		t.Fatal("expected at least one valid event/v2 fixture")
	}
	for _, path := range validPaths {
		t.Run("valid/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEvent("v2", readJSONFixture(t, path))
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if len(violations) != 0 {
				t.Fatalf("expected valid event/v2 fixture, got %+v", violations)
			}
		})
	}

	type expectation struct {
		path    string
		keyword string
	}
	want := map[string]expectation{
		"additional-publication-field.json":      {path: "publication.extra", keyword: keywordFalseSchema},
		// defects-fix-2026-08 P3 widened verdicts[] to an ORDINAL-or-ID item
		// shape, which necessarily LOOSENED `required` from
		// [index, verdict, cause_owner] to [verdict, cause_owner]. These two
		// fixtures are the only thing that proves the anyOf/if-then still
		// refuses the two shapes that loosening opened: an entry naming BOTH
		// addressing modes (no single referent) and an entry naming NEITHER
		// (no referent at all). The epic's own auditor found the widening had
		// shipped with zero fixture delta.
		"verdicts-names-both-index-and-criterion.json": {path: "verdicts.0.criterion", keyword: keywordFalseSchema},
		"malformed-candidate-intent-digest.json": {path: "publication.candidate_intent_digest", keyword: "pattern"},
		"malformed-digest.json":                  {path: "digest", keyword: "pattern"},
		"malformed-intent-key.json":              {path: "publication.intent_key", keyword: "pattern"},
		"malformed-operation-key.json":           {path: "publication.operation_key", keyword: "pattern"},
		"malformed-version-selector.json":        {path: "publication.version_selector", keyword: "pattern"},
		"malformed-version.json":                 {path: "version", keyword: "pattern"},
		"missing-candidate-intent-digest.json":   {path: "publication", keyword: "required"},
		"missing-digest.json":                    {keyword: "required"},
		"missing-intent-key.json":                {path: "publication", keyword: "required"},
		"missing-operation-key.json":             {path: "publication", keyword: "required"},
		"missing-profile.json":                   {keyword: "required"},
		"missing-publication.json":               {keyword: "required"},
		"missing-version-selector.json":          {path: "publication", keyword: "required"},
		"missing-version.json":                   {keyword: "required"},
		"unknown-profile.json":                   {path: "digest_profile", keyword: "enum"},
	}
	invalidPaths, err := filepath.Glob(filepath.Join(root, "invalid/*.json"))
	if err != nil {
		t.Fatalf("glob invalid event/v2 fixtures: %v", err)
	}
	if len(invalidPaths) != len(want) {
		t.Fatalf("invalid event/v2 fixture count = %d, want %d", len(invalidPaths), len(want))
	}
	for _, path := range invalidPaths {
		name := filepath.Base(path)
		expected, ok := want[name]
		if !ok {
			t.Fatalf("invalid event/v2 fixture %q has no isolated expectation", name)
		}
		if _, err := os.Stat(path + ".expect.yaml"); err != nil {
			t.Fatalf("invalid event/v2 fixture %q has no sidecar: %v", name, err)
		}
		t.Run("invalid/"+name, func(t *testing.T) {
			t.Parallel()
			violations, err := c.ValidateEvent("v2", readJSONFixture(t, path))
			if err != nil {
				t.Fatalf("ValidateEvent: %v", err)
			}
			if len(violations) != 1 {
				t.Fatalf("expected exactly one isolated violation, got %+v", violations)
			}
			if violations[0].Path != expected.path || violations[0].Keyword != expected.keyword {
				t.Fatalf("violation = %+v, want path=%q keyword=%q", violations[0], expected.path, expected.keyword)
			}
		})
	}
}

func TestContractV2TemplateIsEmbedded(t *testing.T) {
	t.Parallel()
	raw, err := schemas.FS.ReadFile("templates/v2/contract.md")
	if err != nil {
		t.Fatalf("read embedded contract/v2 template: %v", err)
	}
	for _, want := range []string{"schema: envelope/v2", "artifacts:", "role: valid-fixture", "role: invalid-fixture"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("embedded contract/v2 template does not contain %q", want)
		}
	}
}

func readEnvelopeFixture(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	parts := strings.SplitN(string(raw), "---\n", 3)
	if len(parts) != 3 {
		t.Fatalf("%s: fixture is not frontmatter-shaped", path)
	}
	instance, err := DecodeYAMLInstance([]byte(parts[1]))
	if err != nil {
		t.Fatalf("DecodeYAMLInstance %s: %v", path, err)
	}
	return instance
}

func readJSONFixture(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return instance
}
