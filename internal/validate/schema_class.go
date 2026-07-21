package validate

import (
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// mapSchemaViolations turns internal/schema's library-agnostic
// []FieldViolation into this package's []Violation, attaching the
// registry SCH- code each JSON-Schema keyword failure corresponds to.
//
// The mapping is driven entirely by structural facts internal/schema
// already exposes (Keyword + SchemaPointer), never by re-parsing the
// schema documents here — that would be a second copy of schema
// knowledge (go-conventions.md anti-pattern #7, "second validation
// path"). An unrecognized (Keyword, SchemaPointer) combination is a
// genuine mapping gap: it returns an error rather than fabricating a
// code, so a future schema/keyword addition surfaces as a loud test
// failure (the registry cross-check, AC row 8) instead of a silently
// wrong machine code.
func mapSchemaViolations(fvs []schema.FieldViolation) ([]Violation, error) {
	out := make([]Violation, 0, len(fvs))
	for _, fv := range fvs {
		code, ccRef, err := schemaCode(fv)
		if err != nil {
			return nil, err
		}
		out = append(out, Violation{
			Code:     code,
			Class:    ClassSchema,
			Path:     fv.Path,
			Message:  schemaMessage(fv),
			CCRef:    ccRef,
			Severity: SeverityReject,
		})
	}
	return out, nil
}

func schemaCode(fv schema.FieldViolation) (code, ccRef string, err error) {
	switch fv.Keyword {
	case "required":
		switch {
		case fv.SchemaPointer == "/then":
			// The schema's OWN root-level if/then: blocking:false
			// requires interim_behavior (§5.2, CC-011) — the only
			// current use of a root-level (non-allOf) conditional.
			return "SCH-008", "CC-011", nil
		case strings.HasPrefix(fv.SchemaPointer, "/allOf/") && strings.HasSuffix(fv.SchemaPointer, "/then"):
			// A conditionally-required field nested inside an allOf
			// member (announcement's deprecates, work_request's
			// proposed_change).
			return "SCH-005", "", nil
		default:
			// Every other `required` failure — the document's own
			// root-level required[] ("" pointer) OR a nested object's
			// required[] (e.g. an array item shape, consumes.yaml's
			// dependencies[].major) — is a plain "required field
			// missing" (SCH-001).
			return "SCH-001", "", nil
		}
	case "enum", "const":
		return "SCH-002", "", nil
	case keywordFalseSchemaAlias:
		return "SCH-003", "", nil
	case "pattern":
		return "SCH-007", "", nil
	case "type":
		return "SCH-006", "", nil
	case "maxItems", "minItems":
		return "SCH-004", "", nil
	}
	// NOTE on "format": internal/schema's Load deliberately does NOT
	// enable format assertion (see its doc comment) precisely because no
	// SCH- code exists for a format failure and this phase may not
	// author a new one — so a "format" keyword should never reach this
	// function at runtime. If it ever does (a future schema.Load change
	// re-enables AssertFormat), this intentionally falls through to the
	// unmapped-keyword error below rather than silently inventing a
	// code.
	return "", "", fmt.Errorf(
		"validate: unmapped schema-class keyword %q at path %q (schemaPointer %q) — schema/registry drift, not a runtime content error",
		fv.Keyword, fv.Path, fv.SchemaPointer,
	)
}

// keywordFalseSchemaAlias mirrors internal/schema's own keywordFalseSchema
// constant (unexported there) — kept as a literal string here rather than
// importing an unexported symbol, since Keyword is a plain exported
// string field on FieldViolation this package is meant to read
// structurally, not couple to schema's internals.
const keywordFalseSchemaAlias = "falseSchema"

func schemaMessage(fv schema.FieldViolation) string {
	if fv.Path == "" {
		return fmt.Sprintf("schema violation (%s)", fv.Keyword)
	}
	return fmt.Sprintf("field %q fails schema validation (%s)", fv.Path, fv.Keyword)
}
