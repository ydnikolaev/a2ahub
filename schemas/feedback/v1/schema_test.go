// Package feedback self-verifies the schemas/feedback/v1/ family (P25 13a):
// both schemas compile, every valid fixture validates green, every
// SCHEMA-LEVEL invalid fixture (FB-001/FB-002 sidecars) validates red, the
// SEMANTIC invalid fixtures (FB-003/004/005/006 — schema-valid by design,
// refused only by 13b's runtime validator) parse and validate green, and
// feedback/backlog.yaml validates green against backlog.schema.json.
//
// This is NOT the FB-### closure test (every code emitted >=1x by
// internal/feedback) — that lives in 13b, per the plan's allowlist split;
// this package only proves the schema shapes and the fixture corpus are
// internally consistent.
package feedback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

// resourceURLPrefix mirrors internal/schema/corpus.go's own synthetic,
// never-dereferenced resource-identity namespace (same convention, this
// package's own compiler instance — no import of internal/schema, which
// would invert the schemas -> internal dependency direction the corpus
// doc comment describes).
const resourceURLPrefix = "https://schemas.a2ahub.internal/"

// diskFixturesRoot is this package's own directory: fixtures/ is
// deliberately excluded from schemas.FS (schemas/embed.go's own
// convention), so fixtures are read straight from disk, same as every
// other schemas/**/fixtures/ consumer.
const diskFixturesRoot = "fixtures"

// backlogSeedPath is feedback/backlog.yaml relative to this package
// (schemas/feedback/v1 -> schemas -> repo root -> feedback/backlog.yaml).
const backlogSeedPath = "../../../feedback/backlog.yaml"

func compileFromFS(t *testing.T, relPath, seed string) *jsonschema.Schema {
	t.Helper()
	raw, err := schemas.FS.ReadFile(relPath)
	if err != nil {
		t.Fatalf("schemas.FS.ReadFile(%s): %v (is schemas/embed.go's go:embed line wired?)", relPath, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", relPath, err)
	}
	c := jsonschema.NewCompiler()
	key := resourceURLPrefix + seed
	if err := c.AddResource(key, doc); err != nil {
		t.Fatalf("AddResource(%s): %v", relPath, err)
	}
	sch, err := c.Compile(key)
	if err != nil {
		t.Fatalf("Compile(%s): %v", relPath, err)
	}
	return sch
}

// TestFeedbackSchemasCompile is acceptance item 1: both schemas compile as
// valid JSON Schema, loaded from the embedded schemas.FS (proves the A7 embed
// directives are wired, not just the files on disk).
func TestFeedbackSchemasCompile(t *testing.T) {
	t.Parallel()
	compileFromFS(t, "feedback/v1/feedback.schema.json", "feedback-report")
	compileFromFS(t, "feedback/v1/backlog.schema.json", "feedback-backlog")
}

// sidecar mirrors internal/validate/golden_test.go's own sidecar shape:
// {code, note} cited by every fixtures/invalid/*.yaml.expect.yaml.
type sidecar struct {
	Code string `yaml:"code"`
}

// schemaLevelCodes are the FB-### codes (codes.yaml) whose fixtures are
// genuine JSON-Schema violations — schema_test asserts these fixtures RED.
// Every other invalid fixture's code (FB-003/004/005/006) is a SEMANTIC
// gate 13b's runtime validator enforces; those fixtures are schema-VALID
// by design (brief's "do NOT assert them red" instruction) and are
// asserted GREEN below alongside fixtures/valid/*.yaml.
var schemaLevelCodes = map[string]bool{
	"FB-001": true,
	"FB-002": true,
}

// TestFeedbackFixtures is acceptance items 2-4: every fixtures/valid/*.yaml
// validates green; every schema-level invalid fixture (FB-001/FB-002)
// validates red; every semantic invalid fixture (FB-003/004/005/006)
// parses and validates green (schema-valid by design, per the brief).
func TestFeedbackFixtures(t *testing.T) {
	t.Parallel()
	sch := compileFromFS(t, "feedback/v1/feedback.schema.json", "feedback-report-fixtures")

	validFiles := globFixtures(t, filepath.Join(diskFixturesRoot, "valid/*.yaml"))
	if len(validFiles) == 0 {
		t.Fatal("expected at least one valid feedback fixture")
	}
	for _, f := range validFiles {
		t.Run("valid/"+filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			instance := decodeYAMLFile(t, f)
			if err := sch.Validate(instance); err != nil {
				t.Fatalf("expected %s to be schema-valid, got: %v", f, err)
			}
		})
	}

	invalidFiles := globFixtures(t, filepath.Join(diskFixturesRoot, "invalid/*.yaml"))
	if len(invalidFiles) == 0 {
		t.Fatal("expected at least one invalid feedback fixture")
	}
	for _, f := range invalidFiles {
		t.Run("invalid/"+filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			wantCode := sidecarCode(t, f+".expect.yaml")
			if !strings.HasPrefix(wantCode, "FB-") {
				t.Fatalf("%s: sidecar code %q is not an FB-### code (feedback codes are feedback-local, §11 A2 revised)", f, wantCode)
			}
			instance := decodeYAMLFile(t, f)

			err := sch.Validate(instance)
			if schemaLevelCodes[wantCode] {
				if err == nil {
					t.Fatalf("expected %s (code %s) to fail schema validation, got Valid", f, wantCode)
				}
				return
			}
			// Semantic class (FB-003/004/005/006): schema-valid by design —
			// 13b's runtime validator rejects these, not this schema.
			if err != nil {
				t.Fatalf("expected %s (code %s, semantic class) to be schema-valid so 13b can consume it, got: %v", f, wantCode, err)
			}
		})
	}
}

// TestFeedbackBacklogSeed is acceptance item 5: feedback/backlog.yaml
// validates green against backlog.schema.json.
func TestFeedbackBacklogSeed(t *testing.T) {
	t.Parallel()
	sch := compileFromFS(t, "feedback/v1/backlog.schema.json", "feedback-backlog-seed")
	instance := decodeYAMLFile(t, backlogSeedPath)
	if err := sch.Validate(instance); err != nil {
		t.Fatalf("expected %s to be schema-valid against backlog.schema.json, got: %v", backlogSeedPath, err)
	}
}

// globFixtures globs pattern and drops any `*.expect.yaml` sidecar that
// the glob's own "*.yaml" suffix incidentally also matches — same
// convention as internal/validate/golden_test.go's own globFixtures.
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
	instance, err := decodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("decodeYAMLInstance %s: %v", path, err)
	}
	return instance
}

// decodeYAMLInstance parses raw YAML bytes into a plain, JSON-Schema-
// validatable value (map[string]any / []any / string / float64/int64 /
// bool / nil) — mirrors internal/schema.DecodeYAMLInstance's node-walk
// approach (kept local rather than imported: this package sits under
// schemas/, the lower-level corpus internal/schema itself consumes, per
// schemas/embed.go's own doc comment — importing internal/schema from
// here would invert that direction). None of this family's fixtures use a
// `format`-bearing field, so yaml.v3's implicit !!timestamp resolution
// (the reason internal/schema's version keeps original scalar text) does
// not bite here, but the walk still preserves original text for both
// !!str and !!timestamp defensively.
func decodeYAMLInstance(raw []byte) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	return nodeToInstance(doc.Content[0])
}

func nodeToInstance(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			val, err := nodeToInstance(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[key] = val
		}
		return m, nil
	case yaml.SequenceNode:
		arr := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			val, err := nodeToInstance(c)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		return arr, nil
	case yaml.ScalarNode:
		return scalarToInstance(n)
	case yaml.AliasNode:
		return nodeToInstance(n.Alias)
	default:
		return nil, &unsupportedNodeKindError{kind: n.Kind}
	}
}

type unsupportedNodeKindError struct {
	kind yaml.Kind
}

func (e *unsupportedNodeKindError) Error() string {
	return "feedback: decodeYAMLInstance: unsupported yaml node kind"
}

func scalarToInstance(n *yaml.Node) (any, error) {
	switch n.Tag {
	case "!!str", "!!timestamp":
		return n.Value, nil
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, err
		}
		return b, nil
	case "!!int":
		var i int64
		if err := n.Decode(&i); err != nil {
			return nil, err
		}
		return i, nil
	case "!!float":
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, err
		}
		return f, nil
	default:
		return n.Value, nil
	}
}
