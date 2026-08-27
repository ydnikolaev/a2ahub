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
		case isConditionalRequiredPointer(fv.SchemaPointer):
			// A conditionally-required field missing "for the declared
			// category" — SCH-005's own registry title, decided by the
			// SHAPE of the schema construct (an allOf-member if/then
			// gating a required[]), not by which member happens to carry
			// it. Covers three pointer shapes this corpus produces (P3,
			// spec §T2 + its post-dispatch correction):
			//   - /allOf/N/then                    the conditional's own
			//     object (announcement's deprecates, work_request's
			//     proposed_change)
			//   - /allOf/N/then/properties/<field>  one level deeper: the
			//     conditional requires a field ON a nested property
			//     object it itself requires (work's actor.session)
			//   - /$defs/<name>/allOf/N/then[/properties/<field>]  either
			//     shape above, reached through a $ref hop into a $defs
			//     schema (work's waiting_on, contract's artifactEntry
			//     conforms_to)
			// The third shape once looked ambiguous against
			// XC-axon-invalid-missing-conforms.md, whose sidecar declared
			// SCH-001 for the identical construct
			// ($defs.artifactEntry.allOf[4]: if role ∈ {valid-fixture,
			// invalid-fixture} then required ["conforms_to"]) — but that
			// sidecar was fitted to this function's own prior bug, not a
			// second category of "required" failure: its own note already
			// read "fixture roles require `conforms_to`", SCH-005's title
			// in words while declaring SCH-001 in code. Corrected
			// alongside this fix.
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
	case "minLength":
		return "SCH-009", "", nil
	case "minimum", "maximum", "maxLength":
		return "SCH-010", "", nil
	case "uniqueItems":
		return "SCH-011", "", nil
	case "format":
		// no-silent-yes-2026-08/P3 stage 2: internal/schema's Load now
		// enables AssertFormat on the envelope family (corpus.go), so a
		// malformed `created`/`needed_by`/`valid_until`/
		// `expected_response.by` value reaches this function as a real
		// "format" FieldViolation instead of never occurring. SCH-012
		// (schemas/errors/v1/registry.yaml) is the registered code for
		// exactly this — registered ahead of AssertFormat being turned on
		// (that file's own comment records the forced ordering).
		return "SCH-012", "", nil
	}
	return "", "", fmt.Errorf(
		"validate: unmapped schema-class keyword %q at path %q (schemaPointer %q) — schema/registry drift, not a runtime content error",
		fv.Keyword, fv.Path, fv.SchemaPointer,
	)
}

// isConditionalRequiredPointer reports whether a "required" failure's
// SchemaPointer names an allOf/then conditional — SCH-005's shape — in any
// of the three forms this corpus actually produces (P3, spec §T2: two
// pointer shapes in one function, not one $defs bug; see schemaCode's own
// comment for the third shape's history):
//
//   - /allOf/N/then                       the conditional's own object
//   - /allOf/N/then/properties/<field>    one level deeper: the conditional
//     requires a field ON a nested property object, not on itself
//   - /$defs/<name>/allOf/N/then[/properties/<field>]   either shape above,
//     reached through a $ref hop into a $defs schema
//
// Deliberately narrow beyond that: only one $ref hop is stripped, and the
// properties/<field> form matches exactly one trailing segment — a
// required[] failure nested any deeper than that is a distinct shape this
// function has never been asked to classify, and stays unmapped (falls to
// SCH-001) until it is.
func isConditionalRequiredPointer(pointer string) bool {
	if rest, ok := strings.CutPrefix(pointer, "/$defs/"); ok {
		if idx := strings.Index(rest, "/"); idx >= 0 {
			pointer = rest[idx:]
		}
	}
	if !strings.HasPrefix(pointer, "/allOf/") {
		return false
	}
	if strings.HasSuffix(pointer, "/then") {
		return true
	}
	const marker = "/then/properties/"
	if idx := strings.Index(pointer, marker); idx >= 0 {
		field := pointer[idx+len(marker):]
		return field != "" && !strings.Contains(field, "/")
	}
	return false
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
