package validate

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

func consumesEngine(t *testing.T) *Engine {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	return New(corpus)
}

func TestValidateConsumes(t *testing.T) {
	t.Parallel()
	engine := consumesEngine(t)

	t.Run("a well-formed registry is valid", func(t *testing.T) {
		t.Parallel()
		raw := []byte("schema: consumes/v1\nsystem: seomatrix\ndependencies:\n" +
			"  - contract: XC-axon-ingest\n    major: 1\n    since: \"2026-07-23\"\n")
		result, err := engine.ValidateConsumes(raw)
		if err != nil {
			t.Fatalf("ValidateConsumes: %v", err)
		}
		if !result.Valid {
			t.Fatalf("want valid, got %+v", result.Violations)
		}
		if result.ArtifactID != "seomatrix" {
			t.Fatalf("ArtifactID = %q, want the owning system", result.ArtifactID)
		}
	})

	t.Run("an empty dependency list is valid", func(t *testing.T) {
		t.Parallel()
		raw := []byte("schema: consumes/v1\nsystem: seomatrix\ndependencies: []\n")
		result, err := engine.ValidateConsumes(raw)
		if err != nil {
			t.Fatalf("ValidateConsumes: %v", err)
		}
		if !result.Valid {
			t.Fatalf("want valid (a system may consume nothing yet), got %+v", result.Violations)
		}
	})

	// The exact placeholder the first external consumer's space carried:
	// `consumes: []` is not the schema's shape at all, and nothing flagged
	// it — the file registered nobody, silently (fb-20260723-9ae145's
	// sibling finding).
	t.Run("the consumes: [] placeholder reds", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateConsumes([]byte("consumes: []\n"))
		if err != nil {
			t.Fatalf("ValidateConsumes: %v", err)
		}
		if result.Valid {
			t.Fatal("want invalid: the placeholder has neither schema, system, nor dependencies")
		}
	})

	t.Run("a missing required dependency field reds", func(t *testing.T) {
		t.Parallel()
		raw := []byte("schema: consumes/v1\nsystem: seomatrix\ndependencies:\n" +
			"  - contract: XC-axon-ingest\n    since: \"2026-07-23\"\n")
		result, err := engine.ValidateConsumes(raw)
		if err != nil {
			t.Fatalf("ValidateConsumes: %v", err)
		}
		if result.Valid {
			t.Fatal("want invalid: dependencies[].major is required")
		}
		if result.Violations[0].Code != "SCH-001" {
			t.Fatalf("code = %q, want SCH-001 (required field missing)", result.Violations[0].Code)
		}
	})

	t.Run("an unknown schema version is POL-005", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateConsumes([]byte("schema: consumes/v99\nsystem: x\ndependencies: []\n"))
		if err != nil {
			t.Fatalf("ValidateConsumes: %v", err)
		}
		if result.Valid || result.Violations[0].Code != "POL-005" {
			t.Fatalf("want POL-005, got %+v", result.Violations)
		}
	})

	t.Run("unparseable yaml is a single policy violation", func(t *testing.T) {
		t.Parallel()
		result, err := engine.ValidateConsumes([]byte("schema: [unclosed\n"))
		if err != nil {
			t.Fatalf("ValidateConsumes: %v", err)
		}
		if result.Valid || len(result.Violations) != 1 || result.Violations[0].Code != "POL-002" {
			t.Fatalf("want exactly one POL-002, got %+v", result.Violations)
		}
	})
}
