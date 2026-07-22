package schema

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/ydnikolaev/a2ahub/schemas"
)

// envelopeTypes are the 8 §3.1 object types, in the order §5.2.1 lists
// them. Every one of these has a schemas/envelope/v1/<type>.schema.json
// file and a schemas/templates/v1/<type>.md template (AC-401.2).
var envelopeTypes = []string{
	"contract", "requirement", "question", "work_request",
	"decision", "response", "handoff", "announcement",
}

// resourceURLPrefix is a synthetic, non-dereferenced URI namespace used
// only as the jsonschema/v6 resource-identity space for this corpus (see
// the "seed key" doc comment on addFamily below). It is never fetched
// over the network — every resource is added via Compiler.AddResource
// from the embedded FS; UseLoader is never called, so an unresolved $ref
// fails compilation instead of reaching out.
const resourceURLPrefix = "https://schemas.a2ahub.internal/"

// Corpus is the compiled, embedded product schema corpus plus the parsed
// error-code registry. Build one with Load(); it is safe for concurrent
// read-only use (jsonschema.Schema.Validate does not mutate the schema).
type Corpus struct {
	envelope  map[string]*jsonschema.Schema // by type name
	event     *jsonschema.Schema
	manifest  *jsonschema.Schema
	consumes  *jsonschema.Schema
	baseProps map[string]bool // envelope/v1/base.schema.json's own top-level "properties" keys
	registry  *Registry
}

// Load compiles the embedded schema corpus (schemas.FS) and loads the
// error-code registry. It is deterministic and side-effect free (no
// network, no disk beyond the embedded FS); call it once and share the
// result, or call it per-test — either is safe.
func Load() (*Corpus, error) {
	const op = "Load"

	c := jsonschema.NewCompiler()
	// AssertFormat is deliberately NOT called: draft 2020-12 treats
	// "format" as annotation-only unless the implementation opts into
	// assertion, and this registry has no code for a format failure
	// (schemas/errors/v1/registry.yaml's SCH- rows cover required/enum/
	// forbidden-field/cardinality/conditional-required/type/pattern/
	// interim_behavior only — no "date/date-time format" row, and this
	// phase may not author a new SCH- row, only P2 does). Turning format
	// assertion on without a matching code would surface as an
	// unmappable violation in internal/validate's schema_class.go
	// mapper — confirmed by a probe: a bad `created` value produced a
	// hard error out of ValidateDraft instead of a reported violation.
	// If format enforcement is wanted later, it needs a P2-authored
	// SCH- row first; until then this corpus stays consistent with what
	// the registry actually catalogues.

	baseDoc, err := readJSON("envelope/v1/base.schema.json")
	if err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	// base.schema.json is $ref'd BY the 8 type extensions but never
	// itself has a $ref — safe to register directly under its own full
	// $id-derived key (see addFamily's doc comment for why that
	// distinction matters).
	baseKey := resourceURLPrefix + "envelope/v1/base.schema.json"
	if err := c.AddResource(baseKey, baseDoc); err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	baseProps := topLevelProperties(baseDoc)

	envelope := make(map[string]*jsonschema.Schema, len(envelopeTypes))
	for i, typ := range envelopeTypes {
		sch, err := addSeeded(c, fmt.Sprintf("envelope-%d", i), "envelope/v1/"+typ+".schema.json")
		if err != nil {
			return nil, &Error{Op: op, Input: typ, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
		}
		envelope[typ] = sch
	}

	event, err := addSeeded(c, "event", "event/v1/event.schema.json")
	if err != nil {
		return nil, &Error{Op: op, Input: "event", Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	manifest, err := addSeeded(c, "manifest", "manifest/v1/space.schema.json")
	if err != nil {
		return nil, &Error{Op: op, Input: "manifest", Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	consumes, err := addSeeded(c, "consumes", "consumes/v1/consumes.schema.json")
	if err != nil {
		return nil, &Error{Op: op, Input: "consumes", Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}

	registryRaw, err := schemas.FS.ReadFile("errors/v1/registry.yaml")
	if err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
	}
	registry, err := LoadRegistry(registryRaw)
	if err != nil {
		return nil, &Error{Op: op, Err: err}
	}

	return &Corpus{
		envelope:  envelope,
		event:     event,
		manifest:  manifest,
		consumes:  consumes,
		baseProps: baseProps,
		registry:  registry,
	}, nil
}

// addSeeded loads relPath and registers it under a throwaway single
// path-segment "seed key" (resourceURLPrefix + seed), then compiles it.
//
// WHY a seed key and not relPath itself: every schema file's own "$id" is
// its full corpus-relative path (e.g. "envelope/v1/work_request.schema.
// json" — a P2 authoring decision, off-limits to change here). jsonschema/
// v6 resolves a document's internal resource identity as
// retrievalURL.join($id) (RFC 3986 §5.3 relative-reference merge). If the
// retrieval URL (what we pass to AddResource/Compile) were ALSO the
// literal multi-segment $id string, that join re-appends the same
// directory onto itself (e.g. ".../envelope/v1/envelope/v1/work_request.
// schema.json") — every $ref inside the document then resolves via that
// doubled base and never finds its target. Seeding the retrieval URL with
// a single path segment (no internal "/") makes the join a clean,
// single-occurrence rewrite: retrievalURL.join($id) == resourceURLPrefix +
// $id, exactly. That clean, deduped value is what OTHER documents' bare-
// filename $refs (e.g. "base.schema.json") resolve against — which is why
// base.schema.json itself (a $ref TARGET, never a $ref SOURCE) is
// registered directly under resourceURLPrefix + its own $id instead: nothing
// ever joins against a "seeded" alias for it, so it must live at the exact
// key other documents' clean joins compute.
func addSeeded(c *jsonschema.Compiler, seed, relPath string) (*jsonschema.Schema, error) {
	doc, err := readJSON(relPath)
	if err != nil {
		return nil, err
	}
	key := resourceURLPrefix + seed
	if err := c.AddResource(key, doc); err != nil {
		return nil, err
	}
	return c.Compile(key)
}

func readJSON(relPath string) (any, error) {
	raw, err := schemas.FS.ReadFile(relPath)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// topLevelProperties returns the keys of doc's own top-level "properties"
// object (empty map if absent or doc isn't a JSON object).
func topLevelProperties(doc any) map[string]bool {
	out := map[string]bool{}
	obj, ok := doc.(map[string]any)
	if !ok {
		return out
	}
	props, ok := obj["properties"].(map[string]any)
	if !ok {
		return out
	}
	for k := range props {
		out[k] = true
	}
	return out
}

// ValidateEnvelope validates instance (a decoded frontmatter map — JSON-
// or YAML-sourced, either way passed in as plain Go maps/slices/scalars)
// against typ's compiled schema at the given version. A schema-structural
// failure is reported via the returned []FieldViolation (nil + nil on a
// valid instance); err is non-nil only for an operational failure (bad
// type name, unsupported version per CC-005) — log-or-return, this
// package never decides what "invalid" means for a caller, it only
// reports facts.
func (c *Corpus) ValidateEnvelope(typ, version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateEnvelope"
	n, ok := ParseVersion(version)
	if !ok || !AcceptsEnvelopeVersion(n) {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	sch, ok := c.envelope[typ]
	if !ok {
		return nil, &Error{Op: op, Input: typ, Err: ErrUnknownType}
	}
	return extractFieldViolations(sch.Validate(instance), c.baseProps), nil
}

// ValidateEvent / ValidateManifest / ValidateConsumes: same contract as
// ValidateEnvelope for the other three single-shape product schemas. None
// of these three schemas compose via allOf+$ref (they are flat objects),
// so the BaseEnvelopeFields annotation-propagation workaround does not
// apply — passing an empty suppression set here is intentional, not an
// oversight (see ValidateEnvelope / BaseEnvelopeFields doc comments).
func (c *Corpus) ValidateEvent(version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateEvent"
	n, ok := ParseVersion(version)
	if !ok || !AcceptsEventVersion(n) {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	return extractFieldViolations(c.event.Validate(instance), nil), nil
}

// ValidateManifest validates an instance against the manifest schema for the given version.
func (c *Corpus) ValidateManifest(version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateManifest"
	n, ok := ParseVersion(version)
	if !ok || !AcceptsManifestVersion(n) {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	return extractFieldViolations(c.manifest.Validate(instance), nil), nil
}

// ValidateConsumes validates an instance against the consumes schema for the given version.
func (c *Corpus) ValidateConsumes(version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateConsumes"
	n, ok := ParseVersion(version)
	if !ok || !AcceptsConsumesVersion(n) {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	return extractFieldViolations(c.consumes.Validate(instance), nil), nil
}

// BaseEnvelopeFields returns the set of field names declared directly on
// envelope/v1/base.schema.json's own top-level "properties" object.
//
// internal/validate uses this to correct a santhosh-tekuri/jsonschema/v6
// annotation-propagation gap: when the allOf branch that $refs
// base.schema.json fails validation (e.g. a bad `id` pattern), v6 does
// NOT propagate that branch's "properties" annotations to the type
// schema's own sibling `unevaluatedProperties: false`, and spuriously
// flags every base-only field as an unevaluated (forbidden) property —
// even though every field WAS declared by an applicable subschema (the
// allOf/$ref member), just not one that fully validated. A
// spec-conformant validator (verified here against ajv 8.20, draft
// 2020-12: exactly one violation, the base branch's own pattern failure)
// does not cascade this way. See the Deviations note in this phase's
// report for the empirical confirmation and blast-radius scope.
func (c *Corpus) BaseEnvelopeFields() map[string]bool { return c.baseProps }

// Registry returns the parsed schemas/errors/v1/registry.yaml.
func (c *Corpus) Registry() *Registry { return c.registry }

// EnvelopeTypes returns the 8 §3.1 type names this corpus compiles, in a
// stable (sorted) order.
func EnvelopeTypes() []string {
	out := make([]string, len(envelopeTypes))
	copy(out, envelopeTypes)
	sort.Strings(out)
	return out
}
