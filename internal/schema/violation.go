package schema

import (
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// FieldViolation is this package's own jsonschema-library-agnostic
// representation of a single leaf JSON-Schema validation failure.
// santhosh-tekuri/jsonschema/v6 types never leak past this package
// (go-conventions.md "Schema validation" stack row: behind internal/
// schema, never imported elsewhere) — internal/validate consumes only
// this struct and maps it to a registry code; it carries no knowledge of
// the underlying library.
type FieldViolation struct {
	// Path is the failing instance field, dot-joined (e.g. "id",
	// "actor.kind", "refs.0.ref"); "" means the whole document (a
	// document-level `required` failure).
	Path string
	// Keyword names the JSON-Schema keyword that failed: one of
	// "required", "enum", "pattern", "type", "falseSchema" (additional/
	// unevaluatedProperties), "maxItems", "minItems", or an "other:<Go
	// type>" fallback for anything this package doesn't classify — the
	// fallback is deliberate: an unrecognized keyword should surface as
	// an unmapped code in internal/validate's own tests, never be
	// silently absorbed.
	Keyword string
	// SchemaPointer is the JSON pointer (schema-side) of the failing
	// keyword within the compiled schema document, e.g. "" (root),
	// "/then" (a schema-root-level if/then), "/allOf/1/then" (an
	// allOf-member-level if/then) — internal/validate uses this to tell
	// apart same-keyword failures with different registry codes (e.g.
	// root `required` vs a conditional `required`).
	SchemaPointer string
}

const keywordFalseSchema = "falseSchema"

// extractFieldViolations walks err's *jsonschema.ValidationError leaf
// tree into a flat []FieldViolation, and applies the BaseEnvelopeFields
// annotation-propagation workaround (see that doc comment): a
// "falseSchema" (additionalProperties/unevaluatedProperties) leaf whose
// flagged field is a KNOWN base-envelope field name is dropped as
// cascade noise from a sibling allOf/$ref branch failure, never a
// genuine "field not permitted" violation.
//
// baseProps may be nil (event/manifest/consumes: no allOf+$ref
// composition, so there is nothing to suppress).
func extractFieldViolations(err error, baseProps map[string]bool) []FieldViolation {
	if err == nil {
		return nil
	}
	ve := &jsonschema.ValidationError{}
	ok := errors.As(err, &ve)
	if !ok {
		// Not the library's own error type — an operational failure
		// (e.g. the instance wasn't JSON-marshalable), never silently
		// dropped.
		return []FieldViolation{{Keyword: fmt.Sprintf("other:%T", err)}}
	}

	var leaves []*jsonschema.ValidationError
	collectLeaves(ve, &leaves)

	out := make([]FieldViolation, 0, len(leaves))
	for _, leaf := range leaves {
		base := strings.Join(leaf.InstanceLocation, ".")
		ptr := schemaPointer(leaf.SchemaURL)

		// *kind.AdditionalProperties (a flat `additionalProperties:
		// false` object, e.g. event/manifest/consumes schemas — no
		// allOf+$ref composition) reports ALL of an object's extra keys
		// in one leaf's own Properties field, rather than one leaf per
		// key the way *kind.FalseSchema (unevaluatedProperties, the
		// envelope schemas' composition pattern) does — expand it here
		// so both shapes produce one FieldViolation per extra field,
		// consistently.
		if ap, ok := leaf.ErrorKind.(*kind.AdditionalProperties); ok {
			for _, prop := range ap.Properties {
				path := prop
				if base != "" {
					path = base + "." + prop
				}
				if baseProps[prop] {
					continue
				}
				out = append(out, FieldViolation{Path: path, Keyword: keywordFalseSchema, SchemaPointer: ptr})
			}
			continue
		}

		fv := FieldViolation{
			Path:          base,
			Keyword:       classifyKeyword(leaf.ErrorKind),
			SchemaPointer: ptr,
		}
		if fv.Keyword == keywordFalseSchema && baseProps[lastSegment(fv.Path)] {
			continue
		}
		out = append(out, fv)
	}
	return out
}

func collectLeaves(ve *jsonschema.ValidationError, out *[]*jsonschema.ValidationError) {
	if len(ve.Causes) == 0 {
		*out = append(*out, ve)
		return
	}
	for _, c := range ve.Causes {
		collectLeaves(c, out)
	}
}

func classifyKeyword(k jsonschema.ErrorKind) string {
	switch k.(type) {
	case *kind.Required:
		return "required"
	case *kind.Enum:
		return "enum"
	case *kind.Const:
		return "const"
	case *kind.Pattern:
		return "pattern"
	case *kind.Type:
		return "type"
	case *kind.FalseSchema:
		// *kind.AdditionalProperties is handled separately in
		// extractFieldViolations (it carries multiple property names
		// per leaf, unlike FalseSchema's one-leaf-per-field shape) and
		// never reaches this classifier.
		return keywordFalseSchema
	case *kind.MaxItems:
		return "maxItems"
	case *kind.MinItems:
		return "minItems"
	case *kind.Format:
		return "format"
	default:
		return fmt.Sprintf("other:%T", k)
	}
}

// schemaPointer returns the fragment (JSON pointer) portion of an
// absolute schema URL, e.g. "https://x/y#/allOf/1/then" -> "/allOf/1/then".
func schemaPointer(schemaURL string) string {
	if i := strings.IndexByte(schemaURL, '#'); i >= 0 {
		return schemaURL[i+1:]
	}
	return ""
}

// lastSegment returns the final dot-separated token of a FieldViolation
// Path (the property name a falseSchema violation flagged).
func lastSegment(path string) string {
	if path == "" {
		return ""
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:]
	}
	return path
}
