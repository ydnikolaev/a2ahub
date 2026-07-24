package schema

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Sentinel errors for CompileExternal/ExternalSchema.ValidateInstance, kept
// local to this file rather than errors.go (this file's own allowlisted
// addition) — same {Op, Input, Err} *Error wrapping idiom the rest of the
// package uses.
var (
	// ErrExternalSchemaCompile is returned when raw is not valid JSON, or
	// is syntactically-valid JSON that jsonschema/v6 refuses to compile
	// (including an unresolvable $ref — see CompileExternal's doc
	// comment).
	ErrExternalSchemaCompile = errors.New("schema: external schema failed to compile")

	// ErrExternalInstanceDecode is returned by ValidateInstance when its
	// raw argument is not valid JSON — distinct from a schema-validation
	// failure (ErrExternalSchemaViolation) so a caller can tell "this
	// wasn't even parseable" from "this parsed but the schema rejects it".
	ErrExternalInstanceDecode = errors.New("schema: external schema instance is not valid JSON")

	// ErrExternalSchemaViolation is returned by ValidateInstance when raw
	// decodes but does not validate against the compiled schema.
	ErrExternalSchemaViolation = errors.New("schema: instance failed external schema validation")
)

// externalResourceBase is the synthetic, non-dereferenced resource-identity
// namespace CompileExternal seeds each external schema's resource key
// under — the same role corpus.go's resourceURLPrefix plays for the
// embedded corpus, kept as a distinct value here only for readability (the
// two never share a *Compiler, so there is no key-collision risk either
// way). It is never fetched: see refusingLoader below for why an
// unresolved $ref must never reach jsonschema/v6's own default loader.
const externalResourceBase = "https://schemas.a2ahub.internal/external/"

// refusingLoader is a jsonschema.URLLoader that refuses every URL. It
// exists because jsonschema/v6's OWN default loader is NOT a no-op:
// *Compiler starts with roots.loader.loader == FileLoader{} (v6's
// roots.go newRoots), which happily reads local disk paths. Because
// AddResource's key is normalized through v6's own absolute() helper, a
// document's unresolved *relative* $ref (or a caller-supplied name that
// is not already an absolute URI) gets turned into a "file://" URL built
// from the process's current working directory and handed straight to
// FileLoader.Load — an actual local-disk read, not merely a compile
// error. A "https://" (or any other non-"file") $ref does not reach the
// network either way (FileLoader.ToFile rejects every scheme but "file"
// with an error before any I/O happens), but relying on that scheme
// mismatch as the sole guard would leave the local-disk path open. Wiring
// UseLoader(refusingLoader{}) here makes EVERY unresolved reference —
// relative or absolute, local or remote — a compile error, unconditionally,
// which is what CompileExternal promises callers ("does NO I/O").
type refusingLoader struct{}

func (refusingLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema: unresolved reference %q — external schemas may not fetch or read additional resources", url)
}

// ExternalSchema is a compiled JSON Schema handed in by a caller — a
// contract's own producer-authored schema/**.json, NOT part of the
// embedded corpus (schemas.FS). It is safe for concurrent read-only use
// (jsonschema.Schema.Validate does not mutate the schema).
type ExternalSchema struct {
	name   string
	schema *jsonschema.Schema
}

// CompileExternal compiles raw — a JSON Schema document's bytes — under
// the resource identity name. name is used only for $ref resolution
// within raw and for error messages; it is NOT a filesystem path and
// CompileExternal performs no I/O of any kind (no disk, no network — see
// refusingLoader).
//
// A $ref inside raw that cannot be resolved from raw itself (nothing else
// is ever registered against this compiler) is always a compile error,
// never a fetch: this is v6's DEFAULT for a same-document/registered-only
// $ref, but is NOT true in general (see refusingLoader) — hence the
// explicit UseLoader call below rather than relying on the library's
// out-of-the-box behavior.
func CompileExternal(name string, raw []byte) (*ExternalSchema, error) {
	const op = "CompileExternal"

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, &Error{Op: op, Input: name, Err: fmt.Errorf("%w: %w", ErrExternalSchemaCompile, err)}
	}

	c := jsonschema.NewCompiler()
	c.UseLoader(refusingLoader{})

	key := externalResourceBase + name
	if err := c.AddResource(key, doc); err != nil {
		return nil, &Error{Op: op, Input: name, Err: fmt.Errorf("%w: %w", ErrExternalSchemaCompile, err)}
	}
	sch, err := c.Compile(key)
	if err != nil {
		return nil, &Error{Op: op, Input: name, Err: fmt.Errorf("%w: %w", ErrExternalSchemaCompile, err)}
	}
	return &ExternalSchema{name: name, schema: sch}, nil
}

// ValidateInstance decodes raw as JSON and validates it against s. A nil
// return means raw validates. A non-nil return wraps either
// ErrExternalInstanceDecode (raw was not valid JSON) or
// ErrExternalSchemaViolation (raw decoded but the schema rejects it) —
// callers that only need pass/fail can test for nil; callers that need to
// tell the two apart use errors.Is.
func (s *ExternalSchema) ValidateInstance(raw []byte) error {
	const op = "ValidateInstance"

	var inst any
	if err := json.Unmarshal(raw, &inst); err != nil {
		return &Error{Op: op, Input: s.name, Err: fmt.Errorf("%w: %w", ErrExternalInstanceDecode, err)}
	}
	if err := s.schema.Validate(inst); err != nil {
		return &Error{Op: op, Input: s.name, Err: fmt.Errorf("%w: %w", ErrExternalSchemaViolation, err)}
	}
	return nil
}
