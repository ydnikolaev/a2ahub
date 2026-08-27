package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

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

const (
	familyConsumes           = "consumes"
	familyDataPackage        = "data-package"
	familyEnvelope           = "envelope"
	familyEvent              = "event"
	familyKnownIssues        = "known-issues"
	familyManifest           = "manifest"
	familyReleaseNotes       = "release-notes"
	familyVerificationReport = "verification-report"

	typeBase               = "base"
	typeConsumes           = "consumes"
	typeDataPackage        = "data-package"
	typeEvent              = "event"
	typeKnownIssues        = "known-issues"
	typeReleaseNotes       = "release-notes"
	typeSpace              = "space"
	typeVerificationReport = "verification-report"
)

// corpusKey is the complete identity of one shipped product schema. Family
// versions evolve independently, and concrete envelope types do not silently
// inherit a newer base: every supported (family, version, type) tuple must have
// its own explicit definition below.
type corpusKey struct {
	family  string
	version int
	typ     string
}

type corpusDefinition struct {
	key       corpusKey
	path      string
	refTarget bool
}

// corpusDefinitions is the explicit shipped-decoder registry. Historical
// entries remain here even after they leave the N/N-1 authoring window. A
// refTarget is registered at its exact $id-derived resource URL so concrete
// schemas in that same family/version can resolve a bare filename $ref to it.
var corpusDefinitions = []corpusDefinition{
	{key: corpusKey{family: familyEnvelope, version: 1, typ: typeBase}, path: "envelope/v1/base.schema.json", refTarget: true},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "contract"}, path: "envelope/v1/contract.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "requirement"}, path: "envelope/v1/requirement.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "question"}, path: "envelope/v1/question.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "work_request"}, path: "envelope/v1/work_request.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "decision"}, path: "envelope/v1/decision.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "response"}, path: "envelope/v1/response.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "handoff"}, path: "envelope/v1/handoff.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 1, typ: "announcement"}, path: "envelope/v1/announcement.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 2, typ: typeBase}, path: "envelope/v2/base.schema.json", refTarget: true},
	{key: corpusKey{family: familyEnvelope, version: 2, typ: "contract"}, path: "envelope/v2/contract.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 2, typ: "announcement"}, path: "envelope/v2/announcement.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 2, typ: "work_request"}, path: "envelope/v2/work_request.schema.json"},
	{key: corpusKey{family: familyEnvelope, version: 2, typ: "response"}, path: "envelope/v2/response.schema.json"},
	{key: corpusKey{family: familyEvent, version: 1, typ: typeEvent}, path: "event/v1/event.schema.json"},
	{key: corpusKey{family: familyEvent, version: 2, typ: typeEvent}, path: "event/v2/event.schema.json"},
	{key: corpusKey{family: familyManifest, version: 1, typ: typeSpace}, path: "manifest/v1/space.schema.json"},
	{key: corpusKey{family: familyConsumes, version: 1, typ: typeConsumes}, path: "consumes/v1/consumes.schema.json"},
	{key: corpusKey{family: familyReleaseNotes, version: 1, typ: typeReleaseNotes}, path: "release-notes/v1/release-notes.schema.json"},
	{key: corpusKey{family: familyKnownIssues, version: 1, typ: typeKnownIssues}, path: "known-issues/v1/known-issues.schema.json"},
	{key: corpusKey{family: familyDataPackage, version: 1, typ: typeDataPackage}, path: "data-package/v1/data-package.schema.json"},
	{key: corpusKey{family: familyVerificationReport, version: 1, typ: typeVerificationReport}, path: "verification-report/v1/verification-report.schema.json"},
}

// resourceURLPrefix is a synthetic, non-dereferenced URI namespace used
// only as the jsonschema/v6 resource-identity space for this corpus (see
// the "seed key" doc comment on addSeeded below). It is never fetched
// over the network — every resource is added via Compiler.AddResource
// from the embedded FS; UseLoader is never called, so an unresolved $ref
// fails compilation instead of reaching out.
const resourceURLPrefix = "https://schemas.a2ahub.internal/"

// Corpus is the compiled, embedded product schema corpus plus the parsed
// error-code registry. Every compiled schema has an explicit (family,
// version, type) identity. Build one with Load(); it is safe for concurrent
// read-only use (jsonschema.Schema.Validate does not mutate the schema).
type Corpus struct {
	schemas   map[corpusKey]*jsonschema.Schema
	baseProps map[int]map[string]bool // envelope/vN/base.schema.json top-level "properties" keys, by N
	registry  *Registry
}

// Load compiles the embedded schema corpus (schemas.FS) and loads the
// error-code registry. It is deterministic and side-effect free (no
// network, no disk beyond the embedded FS); call it once and share the
// result, or call it per-test — either is safe.
func Load() (*Corpus, error) {
	const op = "Load"

	// TWO compilers, not one (no-silent-yes-2026-08/P3 stage 1, spec 03 §8
	// AC1-3, DECISIONS.md § D3/§ D9). AssertFormat is a single bool on
	// *jsonschema.Compiler — compiler-WIDE, with no per-schema or
	// per-family scope — so calling it on the one shared compiler this
	// function used to build would have turned format assertion on for
	// EVERY corpus family, not just envelope's four date fields the brief
	// names (`created`, `needed_by`, `valid_until`,
	// `expected_response.by`). Confirmed empirically, not assumed: a
	// single-compiler version of this change reds
	// schemas/release-notes/v1/fixtures/valid/release-notes-unreleased.yaml
	// — `released: unreleased` is a DELIBERATE non-date sentinel
	// scripts/check-release-record.sh accepts for a version authored ahead
	// of its own git tag (the fixture's own comment says so), and it is a
	// shipped, currently-valid document this phase's own scope (the four
	// envelope fields) never asked to touch. So envelopeCompiler carries
	// AssertFormat; everyCompiler (every other family: event, manifest,
	// consumes, release-notes, known-issues, data-package,
	// verification-report) does not, exactly as before this phase.
	//
	// The order AssertFormat is turned on in was FORCED, not chosen:
	// enabling assertion BEFORE a registry code existed for a format
	// failure produced an UNMAPPABLE violation that hard-errored out of
	// internal/validate's ValidateDraft instead of surfacing as a reported
	// violation — confirmed empirically (internal/schema/keyword_test.go's
	// TestFormatIsAnnotationOnly used to pin exactly that absence; it now
	// pins the opposite, a reported "format" FieldViolation). SCH-012
	// (schemas/errors/v1/registry.yaml) was registered FIRST so
	// internal/validate/schema_class.go's mapper has a code to attach —
	// see that file's own comment for the one piece of this ordering P3
	// stage 1's allowlist could not reach (reported as a deviation).
	envelopeCompiler := jsonschema.NewCompiler()
	envelopeCompiler.AssertFormat()
	everyCompiler := jsonschema.NewCompiler()
	compilerFor := func(family string) *jsonschema.Compiler {
		if family == familyEnvelope {
			return envelopeCompiler
		}
		return everyCompiler
	}

	baseProps := make(map[int]map[string]bool)
	for _, definition := range corpusDefinitions {
		if !definition.refTarget {
			continue
		}
		doc, err := readJSON(definition.path)
		if err != nil {
			return nil, &Error{Op: op, Input: definition.path, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
		}
		if err := compilerFor(definition.key.family).AddResource(resourceURLPrefix+definition.path, doc); err != nil {
			return nil, &Error{Op: op, Input: definition.path, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
		}
		if definition.key.family == familyEnvelope && definition.key.typ == typeBase {
			baseProps[definition.key.version] = topLevelProperties(doc)
		}
	}

	compiled := make(map[corpusKey]*jsonschema.Schema, len(corpusDefinitions))
	for i, definition := range corpusDefinitions {
		if _, duplicate := compiled[definition.key]; duplicate {
			return nil, &Error{Op: op, Input: definition.path, Err: fmt.Errorf("%w: duplicate corpus key %+v", ErrCorpusLoad, definition.key)}
		}

		c := compilerFor(definition.key.family)
		var sch *jsonschema.Schema
		var err error
		if definition.refTarget {
			sch, err = c.Compile(resourceURLPrefix + definition.path)
		} else {
			sch, err = addSeeded(c, fmt.Sprintf("corpus-%d", i), definition.path)
		}
		if err != nil {
			return nil, &Error{Op: op, Input: definition.path, Err: fmt.Errorf("%w: %w", ErrCorpusLoad, err)}
		}
		compiled[definition.key] = sch
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
		schemas:   compiled,
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
// each version's base.schema.json (a $ref TARGET, never a $ref SOURCE) is
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

func (c *Corpus) schemaFor(key corpusKey) (*jsonschema.Schema, bool) {
	sch, ok := c.schemas[key]
	return sch, ok
}

func (c *Corpus) hasFamilyVersion(family string, version int) bool {
	for key := range c.schemas {
		if key.family == family && key.version == version {
			return true
		}
	}
	return false
}

// ValidateEnvelope validates instance (a decoded frontmatter map — JSON-
// or YAML-sourced, either way passed in as plain Go maps/slices/scalars)
// against typ's explicitly registered decoder at the given version. A
// schema-structural failure is reported via the returned []FieldViolation
// (nil + nil on a valid instance); err is non-nil only for an operational
// failure (bad type name, or no registered decoder for that version) —
// log-or-return, this package never decides what "invalid" means for a caller,
// it only reports facts.
func (c *Corpus) ValidateEnvelope(typ, version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateEnvelope"
	n, ok := ParseVersion(version)
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	sch, ok := c.schemaFor(corpusKey{family: familyEnvelope, version: n, typ: typ})
	if !ok {
		if !c.hasFamilyVersion(familyEnvelope, n) {
			return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
		}
		return nil, &Error{Op: op, Input: typ, Err: ErrUnknownType}
	}
	fvs := extractFieldViolations(sch.Validate(instance), c.baseProps[n])
	return dropPlaceholderFormatViolations(fvs, instance), nil
}

// dropPlaceholderFormatViolations removes `format` violations whose value is
// still, verbatim, a template's own placeholder token (`<...>`).
//
// It lives HERE, beside the assertion it qualifies, because TWO consumers
// need the same answer — internal/validate's V1/V2 engine and
// internal/template's own TestRenderEveryTypeSchemaValid — and ADR-019
// (2026-08-27) makes a package below both the default for exactly that.
// It was written in internal/validate first and moved down within the hour,
// when the second consumer appeared. That is the ADR working on the commit
// that introduced it.
//
// THE RULE, and the collision that forced it. Enabling format assertion
// (no-silent-yes-2026-08 P3) collided with a decision this repo had already
// taken: internal/validate/placeholder.go's POL-010 is deliberately V2-ONLY —
// an unfilled placeholder is legal while a document is a draft and refused at
// the boundary where the draft becomes a shared record. Two shipped templates
// carry `needed_by: <YYYY-MM-DD>`, and POL-010's own corpus test asserts BY
// NAME that it reaches that field.
//
// Without this, `a2a new work_request` drafts a document its own binary
// immediately calls invalid, and a rendered template stops being
// schema-valid. The tempting fix — comment `needed_by` out of the templates —
// passes every test that existed at the time and silently DELETES a real
// POL-010 refusal: it trades a format check for a lost placeholder check,
// which is the exact silent acceptance the epic that enabled format assertion
// exists to abolish. Only POL-010'"'"'s own reach test catches it.
//
// So: a placeholder is not a format violation. POL-010 owns placeholders, at
// V2, alone. A value that is NOT placeholder-shaped — `needed_by: "next
// tuesday"` — still fails format at every tier, which is the point.
func dropPlaceholderFormatViolations(fvs []FieldViolation, instance any) []FieldViolation {
	if len(fvs) == 0 {
		return fvs
	}
	out := fvs[:0:0]
	for _, fv := range fvs {
		if fv.Keyword == "format" && valueAtPathIsPlaceholder(instance, fv.Path) {
			continue
		}
		out = append(out, fv)
	}
	return out
}

// placeholderToken matches a frontmatter scalar that is still, verbatim, a
// template placeholder. Deliberately the SAME shape internal/validate/
// placeholder.go'"'"'s own placeholderPattern uses; that package walks the whole
// instance to REPORT them as POL-010, this one only asks about one path.
var placeholderToken = regexp.MustCompile(`^<.*>$`)

// valueAtPathIsPlaceholder resolves a dot-joined FieldViolation.Path against
// the decoded instance and reports whether the value there is a placeholder
// token. A path that does not resolve, or resolves to a non-string, is not a
// placeholder — fail closed, so an unresolvable path never suppresses a real
// violation.
func valueAtPathIsPlaceholder(instance any, path string) bool {
	if path == "" {
		return false
	}
	node := instance
	for _, seg := range strings.Split(path, ".") {
		switch n := node.(type) {
		case map[string]any:
			v, ok := n[seg]
			if !ok {
				return false
			}
			node = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(n) {
				return false
			}
			node = n[i]
		default:
			return false
		}
	}
	s, ok := node.(string)
	return ok && placeholderToken.MatchString(s)
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
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	sch, ok := c.schemaFor(corpusKey{family: familyEvent, version: n, typ: typeEvent})
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	return extractFieldViolations(sch.Validate(instance), nil), nil
}

// ValidateManifest validates an instance against the manifest schema for the given version.
func (c *Corpus) ValidateManifest(version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateManifest"
	n, ok := ParseVersion(version)
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	sch, ok := c.schemaFor(corpusKey{family: familyManifest, version: n, typ: typeSpace})
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	return extractFieldViolations(sch.Validate(instance), nil), nil
}

// ValidateConsumes validates an instance against the consumes schema for the given version.
func (c *Corpus) ValidateConsumes(version string, instance any) ([]FieldViolation, error) {
	const op = "ValidateConsumes"
	n, ok := ParseVersion(version)
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	sch, ok := c.schemaFor(corpusKey{family: familyConsumes, version: n, typ: typeConsumes})
	if !ok {
		return nil, &Error{Op: op, Input: version, Err: ErrUnsupportedVersion}
	}
	return extractFieldViolations(sch.Validate(instance), nil), nil
}

// ValidateReleaseNotes validates instance against the release-notes/v1
// schema. Unlike ValidateEnvelope/ValidateEvent/ValidateManifest/
// ValidateConsumes, there is no N-1-cycle version-overlap window here (no
// AcceptsReleaseNotesVersion): release-notes/v1 is the schema field's own
// literal const, not a per-instance version selector — the corpus is
// authored (releasenotes/*.yaml, one file per shipped a2a version) and
// internal/notes' gate test validates every embedded file against this
// one schema directly.
func (c *Corpus) ValidateReleaseNotes(instance any) ([]FieldViolation, error) {
	sch, _ := c.schemaFor(corpusKey{family: familyReleaseNotes, version: 1, typ: typeReleaseNotes})
	return extractFieldViolations(sch.Validate(instance), nil), nil
}

// ValidateKnownIssues validates the single current known-issues document.
func (c *Corpus) ValidateKnownIssues(instance any) ([]FieldViolation, error) {
	sch, _ := c.schemaFor(corpusKey{family: familyKnownIssues, version: 1, typ: typeKnownIssues})
	return extractFieldViolations(sch.Validate(instance), nil), nil
}

// ValidateDataPackage validates instance against the data-package/v1
// schema (spec 05a §T2.1). Same shape as ValidateReleaseNotes/
// ValidateKnownIssues: a flat, non-envelope wire document with no
// version-overlap window — "data-package/v1" is the schema field's own
// literal const, not a per-instance version selector.
func (c *Corpus) ValidateDataPackage(instance any) ([]FieldViolation, error) {
	sch, _ := c.schemaFor(corpusKey{family: familyDataPackage, version: 1, typ: typeDataPackage})
	return extractFieldViolations(sch.Validate(instance), nil), nil
}

// ValidateVerificationReport validates instance against the
// verification-report/v1 schema (spec 05a §T2.3), including its
// result-derivation conditional: a report whose result contradicts its own
// checks[] is rejected here, not merely by Go-side construction discipline
// (internal/datapackage.NewReport) — see that package's report.go for why
// both layers exist.
func (c *Corpus) ValidateVerificationReport(instance any) ([]FieldViolation, error) {
	sch, _ := c.schemaFor(corpusKey{family: familyVerificationReport, version: 1, typ: typeVerificationReport})
	return extractFieldViolations(sch.Validate(instance), nil), nil
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
func (c *Corpus) BaseEnvelopeFields() map[string]bool { return c.baseProps[1] }

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
