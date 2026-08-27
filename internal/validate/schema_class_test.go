package validate

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// TestMapSchemaViolations_Format pins schema_class.go's "format" arm
// (no-silent-yes-2026-08/P3 stage 2, fix wave): a "format" keyword
// FieldViolation — the shape internal/schema now produces once
// AssertFormat is enabled on the envelope family (corpus.go) — maps to
// SCH-012 (schemas/errors/v1/registry.yaml), not to the "unmapped
// schema-class keyword" hard error mapSchemaViolations returned for it
// before this arm existed (the gap internal/schema/keyword_test.go's
// TestFormatIsAsserted predicted, and internal/cli's own
// TestNewDraftsEveryTypeV1Valid hit at runtime, before this fix).
func TestMapSchemaViolations_Format(t *testing.T) {
	t.Parallel()

	fvs := []schema.FieldViolation{
		{Path: "needed_by", Keyword: "format", SchemaPointer: "/properties/needed_by"},
	}
	violations, err := mapSchemaViolations(fvs)
	if err != nil {
		t.Fatalf("mapSchemaViolations: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %+v", violations)
	}
	got := violations[0]
	if got.Code != "SCH-012" {
		t.Errorf("Code = %q, want SCH-012", got.Code)
	}
	if got.Class != ClassSchema {
		t.Errorf("Class = %q, want %q", got.Class, ClassSchema)
	}
	if got.Path != "needed_by" {
		t.Errorf("Path = %q, want %q", got.Path, "needed_by")
	}
	if got.Severity != SeverityReject {
		t.Errorf("Severity = %q, want %q", got.Severity, SeverityReject)
	}
}

// TestSchemaCode_UnmappedKeywordStillErrors guards the OTHER half of
// schemaCode's contract: adding the "format" arm above must not turn
// schemaCode into something that fabricates a code for every keyword. A
// genuinely unrecognised keyword still returns an error — the same
// schema/registry-drift guard the rest of this file's own comment
// documents — rather than a code invented on the spot.
func TestSchemaCode_UnmappedKeywordStillErrors(t *testing.T) {
	t.Parallel()

	_, _, err := schemaCode(schema.FieldViolation{Path: "x", Keyword: "not-a-real-keyword"})
	if err == nil {
		t.Fatal("expected an error for an unrecognised keyword, got nil")
	}
}
