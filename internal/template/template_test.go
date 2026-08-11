package template_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

func fixedInput(typ, id string) template.Input {
	return template.Input{
		Type:    typ,
		ID:      id,
		Actor:   template.Actor{Kind: "agent", Name: "test-bot", Model: "test-model"},
		Created: time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC),
	}
}

// TestExactlyOneTemplatePerType is AC row 7's first half: every P2
// envelope type has exactly one embedded template, and there are no stray
// extra template files.
func TestExactlyOneTemplatePerType(t *testing.T) {
	t.Parallel()
	types := schema.EnvelopeTypes()
	if len(types) == 0 {
		t.Fatal("schema.EnvelopeTypes() returned no types")
	}

	entries, err := fs.Glob(schemas.FS, "templates/v1/*.md")
	if err != nil {
		t.Fatalf("glob templates: %v", err)
	}
	if len(entries) != len(types) {
		t.Fatalf("expected exactly %d template files (one per type), found %d: %v", len(types), len(entries), entries)
	}

	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			if _, err := template.Show(typ); err != nil {
				t.Fatalf("template.Show(%q): %v", typ, err)
			}
		})
	}
}

// TestTemplateFieldsSubsetOfSchema is AC row 7's second half: every
// top-level frontmatter field name a canonical template declares is a
// member of that type's own schema field set (base ∪ type-specific
// properties) — one direction only (a template may legitimately omit an
// optional schema field; the reverse would make every optional field
// mandatory in every template).
func TestTemplateFieldsSubsetOfSchema(t *testing.T) {
	t.Parallel()
	baseProps := schemaProperties(t, "envelope/v1/base.schema.json")

	for _, typ := range schema.EnvelopeTypes() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			typeProps := schemaProperties(t, "envelope/v1/"+typ+".schema.json")

			raw, err := template.Show(typ)
			if err != nil {
				t.Fatalf("template.Show(%q): %v", typ, err)
			}
			fm, err := artifact.ParseFrontmatter(raw)
			if err != nil {
				t.Fatalf("ParseFrontmatter(%q): %v", typ, err)
			}
			var doc map[string]any
			if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
				t.Fatalf("yaml.Unmarshal(%q): %v", typ, err)
			}

			for field := range doc {
				if !baseProps[field] && !typeProps[field] {
					t.Errorf("template %q declares field %q which is in neither base nor %q's own schema properties", typ, field, typ)
				}
			}
		})
	}
}

func schemaProperties(t *testing.T, relPath string) map[string]bool {
	t.Helper()
	raw, err := schemas.FS.ReadFile(relPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", relPath, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", relPath, err)
	}
	out := map[string]bool{}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return out
	}
	for k := range props {
		out[k] = true
	}
	return out
}

// TestRenderFillsCallerSuppliedValues checks id/created/actor are filled
// from Input, never left as template placeholder text.
func TestRenderFillsCallerSuppliedValues(t *testing.T) {
	t.Parallel()
	out, err := template.Render(fixedInput("question", "XQ-axon-20260721-k3f9"))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"id: XQ-axon-20260721-k3f9",
		"created: 2026-07-21T10:00:00Z",
		"name: test-bot",
		"model: test-model",
	} {
		if !containsLine(got, want) {
			t.Errorf("rendered draft missing expected content %q; got:\n%s", want, got)
		}
	}
}

// TestRenderOmitsEmptyModel checks the actor block drops `model` entirely
// when Input.Actor.Model is empty, rather than emitting an empty value.
func TestRenderOmitsEmptyModel(t *testing.T) {
	t.Parallel()
	in := fixedInput("question", "XQ-axon-20260721-k3f9")
	in.Actor.Model = ""
	out, err := template.Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if containsLine(string(out), "model:") {
		t.Errorf("expected no model: line when Actor.Model is empty; got:\n%s", out)
	}
}

// TestRenderEnumPlaceholderSurvivesUnlessOverridden: category (an
// enum-placeholder field) is NOT defaulted to its first alternative absent
// an override — the unreplaced pipe-alternatives token survives to the
// rendered draft verbatim — and an explicit Fields override still wins.
//
// Renamed from TestRenderEnumDefaultAndFieldOverride: that name and its
// "default" half asserted the exact fabrication agent-exchange-2026-08 B3
// (spec 03-fill-classes.md §8 AC4) removed — Render used to silently fill
// an unreplaced enum placeholder with its first alternative, so a fresh
// `question` draft claimed `category: clarification` before any agent had
// chosen a category. The tool must not invent a choice the author never
// made; POL-010 refuses the surviving placeholder token at V2 (`a2a
// submit` / `a2a validate --ci`) instead.
func TestRenderEnumPlaceholderSurvivesUnlessOverridden(t *testing.T) {
	t.Parallel()

	in := fixedInput("question", "XQ-axon-20260721-k3f9")
	out, err := template.Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !containsLine(string(out), "category: <clarification|defect|choice>") {
		t.Errorf("expected the unreplaced enum placeholder to survive verbatim; got:\n%s", out)
	}

	in.Fields = map[string]string{"category": "defect"}
	out, err = template.Render(in)
	if err != nil {
		t.Fatalf("Render with override: %v", err)
	}
	if !containsLine(string(out), "category: defect") {
		t.Errorf("expected overridden category=defect; got:\n%s", out)
	}
}

// TestRenderEveryTypeSchemaValid runs Render for every type with only
// caller-supplied id/actor/date (no --field overrides — "placeholder-only
// fills") and asserts the result is schema-class valid (V1 scope), driving
// this package's own copy of AC-401.1's guarantee (the full V1/`a2a new`
// integration test lives at the cli layer, wired to the real
// validate.Engine).
//
// `thread` is the ONE field this test cannot leave as a placeholder-only fill,
// and the reason is worth stating rather than working around: unlike
// priority/blocking/classification, the template cannot carry a generic
// LITERAL default for it. Any fixed string would be a real, schema-valid
// looking thread id — that is, a fake conversation, which is exactly the
// authoritative-looking wrong answer spec 46 exists to kill. So the templates'
// placeholder for `thread` is deliberately free prose that can never match the
// §3.8 pattern, and the value is supplied by the MINTER at the `a2a new` call
// site, not by this package.
//
// The cost, named rather than hidden: AC-401.1's "placeholder-only fills are
// V1-valid" guarantee is narrowed for this one field, and the coverage that
// replaces it lives at the cli layer where the minting actually happens.
//
// `category` (announcement, contract, question, requirement, work_request)
// and `result` (response) join `thread` in enumFieldOverrides for the same
// reason: agent-exchange-2026-08 B3 (spec 03-fill-classes.md §8 AC4) deleted
// applyFills' first-alternative enum fill, so an unreplaced `<a|b|c>`
// placeholder now survives rendering verbatim and fails schema validation —
// exactly like `thread`, the alternative to a placeholder is a fabricated
// value, not a schema-valid default this test can leave unfilled.
// `decision` and `handoff` carry no top-level enum placeholder and need no
// entry. Values match internal/livee2e/draftfields.go's own choices for the
// same fields, kept in sync deliberately rather than coincidentally.
func TestRenderEveryTypeSchemaValid(t *testing.T) {
	t.Parallel()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	prefixes := map[string]string{
		"contract":     "XC-axon-ingest",
		"requirement":  "XR-axon-ingest",
		"question":     "XQ-axon-20260721-k3f9",
		"work_request": "XW-axon-20260721-k3f9",
		"decision":     "XD-axon-20260721-k3f9",
		"response":     "XS-axon-20260721-k3f9",
		"handoff":      "XH-axon-20260721-k3f9",
		"announcement": "XA-axon-20260721-k3f9",
	}

	enumFieldOverrides := map[string]map[string]string{
		"announcement": {"category": "notice"},
		"contract":     {"category": "other"},
		"question":     {"category": "clarification"},
		"requirement":  {"category": "other"},
		"work_request": {"category": "data"},
		"response":     {"result": "answered"},
	}

	for _, typ := range schema.EnvelopeTypes() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			in := fixedInput(typ, prefixes[typ])
			in.Fields = map[string]string{"thread": "thread:axon-20260721-k3f9"}
			for field, value := range enumFieldOverrides[typ] {
				in.Fields[field] = value
			}
			raw, err := template.Render(in)
			if err != nil {
				t.Fatalf("Render(%q): %v", typ, err)
			}

			fm, err := artifact.ParseFrontmatter(raw)
			if err != nil {
				t.Fatalf("ParseFrontmatter(%q): %v", typ, err)
			}
			instance, err := schema.DecodeYAMLInstance(fm.YAML)
			if err != nil {
				t.Fatalf("DecodeYAMLInstance(%q): %v", typ, err)
			}
			violations, err := corpus.ValidateEnvelope(typ, "envelope/v1", instance)
			if err != nil {
				t.Fatalf("ValidateEnvelope(%q): %v", typ, err)
			}
			if len(violations) != 0 {
				t.Errorf("Render(%q) produced a schema-invalid instance: %+v\n---\n%s", typ, violations, raw)
			}
		})
	}
}

func TestRenderUnknownType(t *testing.T) {
	t.Parallel()
	if _, err := template.Render(fixedInput("bogus", "X-axon-y")); err == nil {
		t.Fatal("expected an error for an unknown type")
	}
}

// TestRenderV2WorkRequestIsCompletableThroughFieldOverrides is the other
// half of TestRenderEveryTypeSchemaValid's V1-pass proof, which only ever
// renders envelope/v1 (Render's default EnvelopeSchema). It proves the thing
// that would actually matter to an author: the sanctioned completion surface
// (`--field`, the same dotted-path pass `a2a new --field` drives) reaches a
// FULLY VALID envelope/v2 work_request — the generation is not merely
// selectable but usable.
//
// NARROWED, and the narrowing is the finding rather than a retreat. This test
// first drove `binding.adoptable=false` and `binding.runtime_pinnable=false`
// against a LIVE `binding:` block in templates/v2/work_request.md, and the
// block is now commented out, so those two overrides no longer apply. The
// live block was reversed for two measured reasons — AC-401.1
// (internal/cli's TestNewDraftsEveryTypeV1Valid) went red, because no
// placeholder literal for a boolean is schema-valid; and the schema's own
// text makes absence mean `undeclared`, DISTINCT from a declared `none`
// (P-1), so emitting the block would have `a2a new` author a declaration
// nobody made, which is B3's defect one level out. See
// schemas/fill-classes.yaml's header.
//
// What remains provable is stronger than it looks: `binding` is
// oneOf(the literal `none`, an object of four required leaves), and the
// SENTINEL branch is reachable through the append pass. So an author who
// wants to declare non-binding does it in one flag, the long form is a
// hand-edit of the commented block, and — the part worth pinning — a fresh
// v2 draft completed only through `--field` validates clean.
func TestRenderV2WorkRequestIsCompletableThroughFieldOverrides(t *testing.T) {
	t.Parallel()

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	in := fixedInput("work_request", "XW-axon-20260721-k3f9")
	in.EnvelopeSchema = "envelope/v2"
	in.Fields = map[string]string{
		"category": "data",
		"thread":   "thread:axon-20260721-k3f9",
		"binding":  "none",
	}
	raw, err := template.Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	instance, err := schema.DecodeYAMLInstance(fm.YAML)
	if err != nil {
		t.Fatalf("DecodeYAMLInstance: %v", err)
	}
	violations, err := corpus.ValidateEnvelope("work_request", "envelope/v2", instance)
	if err != nil {
		t.Fatalf("ValidateEnvelope: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("a fully field-overridden v2 work_request draft is still schema-invalid: %+v\n---\n%s", violations, raw)
	}
	// Assert the sentinel LANDED, not merely that the draft is valid — a
	// draft with no `binding:` key at all is also valid (the field is
	// optional, and absence is `undeclared`), so validity alone would pass
	// even if the override had been silently dropped.
	//
	// Read it off the DECODED instance, never off the raw bytes. The first
	// draft of this assertion was `strings.Contains(raw, "binding: none")`,
	// and mutating the override away left it GREEN: templates/v2/
	// work_request.md's commented-out guidance block contains the literal
	// text "binding: none" as prose, so the substring check was matching a
	// comment. The mutation is the only reason that is known.
	obj, ok := instance.(map[string]any)
	if !ok {
		t.Fatalf("decoded v2 work_request instance is %T, not a mapping", instance)
	}
	if got := obj["binding"]; got != "none" {
		t.Fatalf("`--field binding=none` did not reach the draft's `binding` key (got %#v); the sentinel branch is the only route an author has to declare non-binding through the sanctioned surface:\n%s", got, raw)
	}
}

func TestRenderSelectsExplicitEnvelopeTemplateGeneration(t *testing.T) {
	t.Parallel()

	legacy, err := template.Render(fixedInput("contract", "XC-axon-contract"))
	if err != nil {
		t.Fatalf("render default contract template: %v", err)
	}
	if !strings.Contains(string(legacy), "schema: envelope/v1") {
		t.Fatalf("empty EnvelopeSchema no longer preserves the v1 default:\n%s", legacy)
	}

	v2Input := fixedInput("contract", "XC-axon-contract")
	v2Input.EnvelopeSchema = "envelope/v2"
	v2, err := template.Render(v2Input)
	if err != nil {
		t.Fatalf("render explicit contract/v2 template: %v", err)
	}
	if !strings.Contains(string(v2), "schema: envelope/v2") || !strings.Contains(string(v2), "artifacts:") {
		t.Fatalf("explicit EnvelopeSchema did not select contract/v2:\n%s", v2)
	}
	for _, want := range []string{
		"schema/contract.schema.json",
		"fixtures/valid/contract.json",
		"fixtures/invalid/contract.json",
	} {
		if !strings.Contains(string(v2), want) {
			t.Fatalf("explicit contract/v2 did not bind %q to the standing slug:\n%s", want, v2)
		}
	}
	if strings.Contains(string(v2), "<slug>") {
		t.Fatalf("explicit contract/v2 leaked an unresolved slug token:\n%s", v2)
	}

	unsupported := fixedInput("contract", "XC-axon-contract")
	unsupported.EnvelopeSchema = "envelope/v3"
	if _, err := template.Render(unsupported); !errors.Is(err, template.ErrUnsupportedEnvelopeSchema) {
		t.Fatalf("unsupported envelope generation error = %v, want ErrUnsupportedEnvelopeSchema", err)
	}
}

func TestRenderNewMovesOnlyJSONSchemaContractsToV2(t *testing.T) {
	t.Parallel()
	jsonInput := fixedInput("contract", "XC-axon-contract")
	jsonDraft, err := template.RenderNew(jsonInput, func(format string) bool { return strings.HasPrefix(format, "json-schema") })
	if err != nil || !strings.Contains(string(jsonDraft), "schema: envelope/v2") {
		t.Fatalf("JSON-Schema contract did not select envelope/v2: err=%v\n%s", err, jsonDraft)
	}
	protoInput := fixedInput("contract", "XC-axon-contract")
	protoInput.Fields = map[string]string{}
	protoInput.Fields["schema_format"] = "proto3"
	protoDraft, err := template.RenderNew(protoInput, func(format string) bool { return strings.HasPrefix(format, "json-schema") })
	if err != nil || !strings.Contains(string(protoDraft), "schema: envelope/v1") {
		t.Fatalf("non-JSON contract did not preserve envelope/v1 authoring: err=%v\n%s", err, protoDraft)
	}
	question, err := template.RenderNew(fixedInput("question", "XQ-axon-20260804-a2b3"), func(string) bool { return true })
	if err != nil || !strings.Contains(string(question), "schema: envelope/v1") {
		t.Fatalf("question did not preserve historical generation: err=%v\n%s", err, question)
	}
}

func TestShowUnknownType(t *testing.T) {
	t.Parallel()
	if _, err := template.Show("bogus"); err == nil {
		t.Fatal("expected an error for an unknown type")
	}
}

// TestRenderAppliesADottedFieldOverride is A1's fix: `--field expected_
// response.shape=...` reaches question.md's NESTED `expected_response:
// {shape: ...}` placeholder — before this fix applyFills only ever walked
// TOP-LEVEL keys, so a dotted key matched none of them and (after the D0
// fix made unmatched keys an error rather than silence) tripped
// ErrUnappliableField even though the field genuinely exists, just
// nested.
func TestRenderAppliesADottedFieldOverride(t *testing.T) {
	t.Parallel()

	out, err := template.Render(template.Input{
		Type:    "question",
		ID:      "XQ-axon-20260727-d0t1",
		Actor:   template.Actor{Kind: "agent", Name: "bot"},
		Created: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		Fields: map[string]string{
			"thread":                  "thread:axon-20260727-d0t1",
			"expected_response.shape": "a yes/no answer with one sentence of rationale",
		},
	})
	if err != nil {
		t.Fatalf("Render with --field expected_response.shape=...: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "<what a good answer looks like>") {
		t.Fatalf("the nested placeholder survived a dotted --field override:\n%s", got)
	}
	if !strings.Contains(got, "a yes/no answer with one sentence of rationale") {
		t.Fatalf("the dotted override never reached the document:\n%s", got)
	}
}

// TestRenderDottedFieldOverrideMissingIntermediateKey is A1's error path:
// a dotted key whose intermediate segment names no key anywhere on the
// path is refused (ErrUnappliableField) naming the FULL dotted path —
// never silence, and never a partial/confusing error that only names the
// last segment.
func TestRenderDottedFieldOverrideMissingIntermediateKey(t *testing.T) {
	t.Parallel()

	_, err := template.Render(template.Input{
		Type:    "question",
		ID:      "XQ-axon-20260727-d0t2",
		Actor:   template.Actor{Kind: "agent", Name: "bot"},
		Created: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		// "expected_response" exists, but it has no "nope" key.
		Fields: map[string]string{"expected_response.nope": "whatever"},
	})
	if err == nil {
		t.Fatal("expected an error for a dotted --field whose intermediate segment resolves to nothing")
	}
	if !errors.Is(err, template.ErrUnappliableField) {
		t.Fatalf("err = %v, want ErrUnappliableField", err)
	}
	if !strings.Contains(err.Error(), "expected_response.nope") {
		t.Errorf("err must name the FULL dotted path %q: %v", "expected_response.nope", err)
	}
}

// TestRenderDottedFieldOverrideThroughSequenceIndexUnsupported is the
// explicit "must stay unsupported" guard: `refs[0].ref` is deliberately
// NOT a supported grammar (A1 is map-only) — it must be refused, never
// panic and never silently drop.
func TestRenderDottedFieldOverrideThroughSequenceIndexUnsupported(t *testing.T) {
	t.Parallel()

	_, err := template.Render(template.Input{
		Type:    "question",
		ID:      "XQ-axon-20260727-d0t3",
		Actor:   template.Actor{Kind: "agent", Name: "bot"},
		Created: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		Fields:  map[string]string{"refs[0].ref": "XC-axon-ingest#deadbeef"},
	})
	if err == nil {
		t.Fatal("expected an error: refs[0].ref is a sequence-index grammar, which A1 deliberately does not support")
	}
	if !errors.Is(err, template.ErrUnappliableField) {
		t.Fatalf("err = %v, want ErrUnappliableField", err)
	}
}

// TestRenderDottedFieldOverrideThroughNonMapping proves descending
// through a SCALAR node with more path remaining is a refusal, not a
// panic — e.g. `title.shape` where `title` itself is a plain scalar in
// every template.
func TestRenderDottedFieldOverrideThroughNonMapping(t *testing.T) {
	t.Parallel()

	_, err := template.Render(template.Input{
		Type:    "question",
		ID:      "XQ-axon-20260727-d0t4",
		Actor:   template.Actor{Kind: "agent", Name: "bot"},
		Created: time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC),
		Fields:  map[string]string{"title.shape": "whatever"},
	})
	if err == nil {
		t.Fatal("expected an error descending through a scalar node (title), not a panic and not silence")
	}
	if !errors.Is(err, template.ErrUnappliableField) {
		t.Fatalf("err = %v, want ErrUnappliableField", err)
	}
	if !strings.Contains(err.Error(), "title.shape") {
		t.Errorf("err must name the FULL dotted path %q: %v", "title.shape", err)
	}
}

func containsLine(haystack, needle string) bool {
	return len(needle) > 0 && (indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// TestRenderAppliesAListValuedFieldOverride is the regression for a defect found
// by drafting an announcement on a real space and reading the refusal that came
// back minutes later.
//
// `--field to=alpha` was ACCEPTED and silently dropped. setScalar writes
// node.Value, and for a sequence node the encoder emits the node's children and
// never looks at Value — so the template's `<recipient-system>` placeholder
// survived, and `submit` refused the write with
// "REF-006 to: `to` includes an unknown system: <recipient-system>": a complaint
// about a placeholder the author believed they had replaced, with nothing
// anywhere saying the flag had been ignored.
//
// Three syntaxes were verified as broken before this was called a defect —
// `to=alpha`, `to=[alpha]`, `to=- alpha` — because "my syntax was wrong" is the
// likelier explanation and had to be ruled out first.
//
// The fix lives in internal/template, which means it covers BOTH surfaces: MCP's
// a2a_new renders through the same template.Render. The deferral note that filed
// this originally called it "cross-surface with MCP a2a_new" and parked it; the
// shared door is exactly why it is not.
func TestRenderAppliesAListValuedFieldOverride(t *testing.T) {
	t.Parallel()

	forms := map[string]string{
		"bare scalar is the one-element reading": "beta",
		"flow list":                              "[beta]",
		"block list":                             "- beta",
	}
	for name, override := range forms {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := template.Render(template.Input{
				Type:    "announcement",
				ID:      "XA-axon-20260726-ab12",
				Actor:   template.Actor{Kind: "agent", Name: "bot"},
				Created: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
				Fields:  map[string]string{"to": override},
			})
			if err != nil {
				t.Fatalf("Render with --field to=%q: %v", override, err)
			}
			got := string(out)
			if strings.Contains(got, "<recipient-system>") {
				t.Fatalf("the placeholder survived a --field override of %q — this is the silent drop, and "+
					"the author only finds out when `submit` refuses the write:\n%s", override, got)
			}
			if !strings.Contains(got, "beta") {
				t.Fatalf("the override %q did not reach the document:\n%s", override, got)
			}
		})
	}
}

// TestRenderRefusesAFieldOverrideItCannotApply is the other half, and the one
// that matters more than the fix.
//
// The old behaviour was not "lists are unsupported" — it was unsupported AND
// SILENT. Anything the renderer cannot apply must now be refused where it is
// given, naming the field and both kinds, so the failure is never again
// discovered as a validation complaint about something else entirely.
func TestRenderRefusesAFieldOverrideItCannotApply(t *testing.T) {
	t.Parallel()

	_, err := template.Render(template.Input{
		Type:    "announcement",
		ID:      "XA-axon-20260726-ab12",
		Actor:   template.Actor{Kind: "agent", Name: "bot"},
		Created: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
		// `to` is a list in the template; a mapping cannot become one.
		Fields: map[string]string{"to": "{beta: yes}"},
	})
	if err == nil {
		t.Fatal("a --field override that cannot be applied must be refused, not dropped — dropping it is " +
			"how the original defect stayed invisible until a later, unrelated-looking refusal")
	}
	if !errors.Is(err, template.ErrUnappliableField) {
		t.Fatalf("err = %v, want ErrUnappliableField so a caller can recognise the class", err)
	}
	for _, want := range []string{"to", "mapping", "list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err is missing %q — it must name the field and both kinds: %v", want, err)
		}
	}
}

// TestRenderThreadFieldLands is the D0 regression (spec 46, defect D0):
// `a2a new --thread <id>` had never worked, because applyFills only ever
// visited keys ALREADY IN the template's mapping, and no template carried a
// `thread:` key at all — so `Fields{"thread": ...}` was silently dropped for
// every one of the 8 types. This is the test whose absence let the flag
// ship inert (spec 46 §T7): it must fail on the pre-fix template corpus and
// pass once both the mechanism (applyFills) and the corpus (schemas/
// templates/v1/*.md) carry a real `thread:` key.
func TestRenderThreadFieldLands(t *testing.T) {
	t.Parallel()

	prefixes := map[string]string{
		"contract":     "XC-axon-ingest",
		"requirement":  "XR-axon-ingest",
		"question":     "XQ-axon-20260726-a1b2",
		"work_request": "XW-axon-20260726-a1b2",
		"decision":     "XD-axon-20260726-a1b2",
		"response":     "XS-axon-20260726-a1b2",
		"handoff":      "XH-axon-20260726-a1b2",
		"announcement": "XA-axon-20260726-a1b2",
	}
	const wantThread = "thread:axon-20260726-a1b2"

	for _, typ := range schema.EnvelopeTypes() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			in := fixedInput(typ, prefixes[typ])
			in.Fields = map[string]string{"thread": wantThread}
			out, err := template.Render(in)
			if err != nil {
				t.Fatalf("Render(%q) with Fields{\"thread\": %q}: %v", typ, wantThread, err)
			}
			if !containsLine(string(out), "thread: "+wantThread) {
				t.Errorf("Render(%q): the --thread override never reached the document — this is D0 all "+
					"over again — got:\n%s", typ, out)
			}
		})
	}
}

// TestRenderUnknownFieldOverrideRefused is applyFills' generic guarantee,
// underneath the thread-specific D0 fix: an in.Fields key that names no key
// in the template's own mapping is refused with ErrUnappliableField, never
// silently dropped. Fixing only `thread` and leaving every other field able
// to vanish silently would be treating the symptom, not the root cause
// (spec 46 §T1).
func TestRenderUnknownFieldOverrideRefused(t *testing.T) {
	t.Parallel()

	_, err := template.Render(template.Input{
		Type:    "question",
		ID:      "XQ-axon-20260726-a1b2",
		Actor:   template.Actor{Kind: "agent", Name: "bot"},
		Created: time.Date(2026, 7, 26, 10, 0, 0, 0, time.UTC),
		// "no_such_field" is not a key question.md's template carries.
		Fields: map[string]string{"no_such_field": "whatever"},
	})
	if err == nil {
		t.Fatal("a --field override naming a key the template does not carry must be refused, not dropped — " +
			"a silently discarded --field is the same class of lie as the D0 --thread drop")
	}
	if !errors.Is(err, template.ErrUnappliableField) {
		t.Fatalf("err = %v, want ErrUnappliableField so a caller can recognise the class", err)
	}
	if !strings.Contains(err.Error(), "no_such_field") {
		t.Errorf("err is missing the offending field name: %v", err)
	}
}

// TestRenderDottedActorFieldWinsOverTheResolvedActor pins the one behaviour
// the dotted-path pass changes beyond "a nested field can now be filled":
// `--field actor.name=X` overrides the actor Render already wrote from
// in.Actor.
//
// This is the ordering plan §7.4 mandates — "Actor identity resolution
// order (for `a2a new`/events): explicit flags > `A2A_ACTOR_*` env vars >
// harness adapter defaults > config" — and a dotted `--field` is an
// explicit flag. in.Actor arrives already resolved from the lower tiers of
// that order, so a flag that lost to it would invert the rule.
//
// Pinned rather than left implicit because the opposite reading is
// plausible enough that someone will "fix" it: making the resolved actor
// authoritative would mean silently dropping a flag the caller passed,
// which is the exact class of silence this pass was written to end.
func TestRenderDottedActorFieldWinsOverTheResolvedActor(t *testing.T) {
	t.Parallel()

	in := fixedInput("question", "XQ-axon-20260727-k3f9")
	in.Fields = map[string]string{"actor.name": "opus-explicit"}

	out, err := template.Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "opus-explicit") {
		t.Errorf("rendered draft does not carry the explicit --field actor.name; §7.4 puts an explicit flag ABOVE the resolved actor:\n%s", got)
	}
	if strings.Contains(got, "test-bot") {
		t.Errorf("rendered draft still carries in.Actor's resolved name %q alongside the explicit override:\n%s", "test-bot", got)
	}
}

// isJSONSchemaFixture mirrors validate.IsJSONSchemaFormat's dialect-prefix
// rule without importing internal/validate — this package deliberately never
// depends on it (§9 "Coupling: soft"), which is exactly why the classifier is
// a caller-supplied function in the first place.
func isJSONSchemaFixture(format string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(format)), "json-schema")
}

// TestShowGenerationMatchesWhatFreshAuthoringRenders is the regression for
// fb-20260806-3539ac. `a2a template show contract` returned the envelope/v1
// template while `a2a new contract` wrote envelope/v2, so a hand-authored
// contract carried no `artifacts:` inventory, merged, and could never
// publish. The inspection surface must show the bytes the authoring surface
// writes — for EVERY type, not just the one that was reported.
func TestShowGenerationMatchesWhatFreshAuthoringRenders(t *testing.T) {
	t.Parallel()
	for _, typ := range template.Types() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			shown, err := template.ShowGeneration(typ, "", isJSONSchemaFixture)
			if err != nil {
				t.Fatalf("ShowGeneration(%q): %v", typ, err)
			}
			rendered, err := template.RenderNew(fixedInput(typ, idFor(typ)), isJSONSchemaFixture)
			if err != nil {
				t.Fatalf("RenderNew(%q): %v", typ, err)
			}
			shownSchema, renderedSchema := envelopeSchemaOf(t, shown), envelopeSchemaOf(t, rendered)
			if shownSchema != renderedSchema {
				t.Fatalf("template show %s prints %s but a fresh draft is %s — an author following the shown template writes a shape the tool itself does not",
					typ, shownSchema, renderedSchema)
			}
		})
	}
}

// TestShowGenerationContractIsDeclaredV2 pins the specific shape the
// publication planner requires at or above contract.ContractPublicationFloor:
// envelope/v2 with a top-level `artifacts:` inventory. Presence of the key is
// what selects declared-v2 in contract.BuildCandidateIntent, so its absence
// here is the whole defect.
func TestShowGenerationContractIsDeclaredV2(t *testing.T) {
	t.Parallel()
	shown, err := template.ShowGeneration("contract", "", isJSONSchemaFixture)
	if err != nil {
		t.Fatalf("ShowGeneration(contract): %v", err)
	}
	if got := envelopeSchemaOf(t, shown); got != "envelope/v2" {
		t.Fatalf("schema = %q, want envelope/v2", got)
	}
	if !strings.Contains(string(shown), "\nartifacts:\n") {
		t.Fatalf("the shown contract template declares no top-level artifacts inventory:\n%s", shown)
	}
}

// TestShowGenerationExplicitV1 keeps the older shape reachable on purpose: a
// space below the floor still publishes the fixed v1 tree, and an author
// there has to be able to see the template that space accepts.
func TestShowGenerationExplicitV1(t *testing.T) {
	t.Parallel()
	shown, err := template.ShowGeneration("contract", "envelope/v1", isJSONSchemaFixture)
	if err != nil {
		t.Fatalf("ShowGeneration(contract, envelope/v1): %v", err)
	}
	if got := envelopeSchemaOf(t, shown); got != "envelope/v1" {
		t.Fatalf("schema = %q, want envelope/v1", got)
	}
	if strings.Contains(string(shown), "\nartifacts:\n") {
		t.Fatalf("the v1 template must NOT declare an artifacts inventory — the legacy profile refuses one:\n%s", shown)
	}
}

// TestShowGenerationUnknownGenerationRefused: an unknown generation is an
// error, never a silent fall back to v1.
func TestShowGenerationUnknownGenerationRefused(t *testing.T) {
	t.Parallel()
	if _, err := template.ShowGeneration("contract", "envelope/v9", isJSONSchemaFixture); err == nil {
		t.Fatal("expected an error for an unknown envelope generation, got nil")
	}
}

// TestAuthoringEnvelopeSchemaKnowsEveryType proves the reported answer is
// never empty — a caller (the `template list` column, the drift gate) that
// got "" would print a blank generation and prove nothing.
func TestAuthoringEnvelopeSchemaKnowsEveryType(t *testing.T) {
	t.Parallel()
	for _, typ := range template.Types() {
		got, err := template.AuthoringEnvelopeSchema(typ, isJSONSchemaFixture)
		if err != nil {
			t.Fatalf("AuthoringEnvelopeSchema(%q): %v", typ, err)
		}
		if got != "envelope/v1" && got != "envelope/v2" {
			t.Fatalf("AuthoringEnvelopeSchema(%q) = %q, want a known generation", typ, got)
		}
	}
}

// wantGeneration is the pinned expectation table for "what envelope
// generation does FRESH authoring render for typ", mirroring the shape of
// internal/schema/fillclasses_reachability_test.go's own wantExcluded block:
// a derived answer that could otherwise change silently is committed here so
// widening it is a deliberate edit with a reason, not a side effect of
// touching template.go's selector.
//
// `contract` is envelope/v2 because isJSONSchemaFixture (this file) reports
// its default schema_format (json-schema-2020-12) as JSON-Schema — the one
// row where the answer is a sniff rather than a fixed fact.
// `work_request` is envelope/v2 UNCONDITIONALLY, per specs/04-possession.md
// §11's 2026-08-10 amendment: attachments[] and binding are both optional,
// so a v2 draft is a strict superset of v1 and there is no bifurcation to
// sniff. `announcement` stays envelope/v1 and carries "B20" in its reason:
// schemas/templates/v2/announcement.md ships, but it is a work-checkpoint
// template (hardcoded `category: status`, a required `work:` block), not a
// general v2 announcement, so selecting v2 for it here would misrender every
// fresh `a2a new announcement` — filed as epic-backlog B20 rather than a
// silent omission (see template.go's generationTable doc comment).
var wantGeneration = map[string]string{
	"contract":     "envelope/v2",
	"requirement":  "envelope/v1",
	"question":     "envelope/v1",
	"work_request": "envelope/v2",
	"decision":     "envelope/v1",
	"response":     "envelope/v1",
	"handoff":      "envelope/v1",
	"announcement": "envelope/v1", // B20
}

// TestFreshAuthoringGenerationIsPinned is spec 04-possession.md §11's
// 2026-08-10 amendment turned into a gate: for EVERY type
// schema.EnvelopeTypes() names, the selector's answer (AuthoringEnvelopeSchema,
// the `a2a template list/show` reporting path) must match wantGeneration's
// declared expectation, AND RenderNew's actual rendered `schema:` line (the
// `a2a new` authoring path) must agree with what AuthoringEnvelopeSchema
// reported — the exact disagreement between those two functions that this
// wave's own selectGeneration extraction was built to make impossible again
// (see AuthoringEnvelopeSchema's doc comment for the 2026-08-06 incident).
func TestFreshAuthoringGenerationIsPinned(t *testing.T) {
	t.Parallel()

	types := template.Types()
	if len(wantGeneration) != len(types) {
		t.Fatalf("wantGeneration has %d entries, template.Types() has %d — every envelope type needs an explicit, named row (no derived default)", len(wantGeneration), len(types))
	}

	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			want, declared := wantGeneration[typ]
			if !declared {
				t.Fatalf("wantGeneration has no row for %q", typ)
			}

			got, err := template.AuthoringEnvelopeSchema(typ, isJSONSchemaFixture)
			if err != nil {
				t.Fatalf("AuthoringEnvelopeSchema(%q): %v", typ, err)
			}
			if got != want {
				t.Errorf("AuthoringEnvelopeSchema(%q) = %q, want %q (pinned) — either the selection changed for a real reason (update wantGeneration and say why) or template.go's generationTable drifted", typ, got, want)
			}

			rendered, err := template.RenderNew(fixedInput(typ, idFor(typ)), isJSONSchemaFixture)
			if err != nil {
				t.Fatalf("RenderNew(%q): %v", typ, err)
			}
			renderedSchema := envelopeSchemaOf(t, rendered)
			if renderedSchema != got {
				t.Errorf("RenderNew(%q) rendered %q but AuthoringEnvelopeSchema(%q) reports %q — the two authoring surfaces disagree, exactly the 2026-08-06 incident this wave's selectGeneration extraction exists to prevent", typ, renderedSchema, typ, got)
			}
		})
	}
}

// envelopeSchemaOf decodes a draft or template's own `schema:` field.
func envelopeSchemaOf(t *testing.T, raw []byte) string {
	t.Helper()
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		t.Fatalf("ParseFrontmatter: %v", err)
	}
	var probe struct {
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		t.Fatalf("unmarshal frontmatter: %v", err)
	}
	return probe.Schema
}

// idFor is the same per-type minted-ID table the type-coverage test above
// uses, lifted to a helper so the two tests cannot disagree about which id
// grammar a type takes.
func idFor(typ string) string {
	switch typ {
	case "contract":
		return "XC-axon-ingest"
	case "requirement":
		return "XR-axon-ingest"
	case "question":
		return "XQ-axon-20260721-k3f9"
	case "work_request":
		return "XW-axon-20260721-k3f9"
	case "decision":
		return "XD-axon-20260721-k3f9"
	case "response":
		return "XS-axon-20260721-k3f9"
	case "handoff":
		return "XH-axon-20260721-k3f9"
	case "announcement":
		return "XA-axon-20260721-k3f9"
	}
	return ""
}

// TestRenderNewAcceptsAFieldOnlyTheSelectedGenerationHas is the gate the
// reachability suite could not be: it drives RenderNew — the function
// `a2a new` actually calls — rather than Render with a generation already
// chosen.
//
// The distinction is not academic. RenderNew's FIRST pass renders v1 in order
// to sniff `schema_format`, and it used to apply the caller's --field
// overrides to that pass. A field that exists only on the v2 schema therefore
// failed with "the envelope/v1 schema for <type> has no <field> key" — before
// the generation was even decided, and even for `work_request`, which is
// unconditionally v2.
//
// Two shipped documents asserted the opposite. fill-classes.yaml's header and
// specs/04 §11 both state that `--field binding=none` "is a real, valid
// authoring act", measured against
// internal/schema/fillclasses_reachability_test.go — which calls Render with
// EnvelopeSchema already set and so never walks the path the product walks.
// A gate proving something the product does not do is the defect this epic
// exists to remove, and it was sitting in the epic's own evidence. Found by
// running the verb by hand on 2026-08-11.
func TestRenderNewAcceptsAFieldOnlyTheSelectedGenerationHas(t *testing.T) {
	t.Parallel()

	isJSONSchema := func(format string) bool { return format == "json-schema-2020-12" }

	for _, tc := range []struct {
		typ, field, want string
	}{
		// Unconditionally v2, so the sniff pass is pure overhead — and it was
		// the thing failing.
		{typ: "work_request", field: "binding", want: "envelope/v2"},
		// Sniffed to v2 off the template's own schema_format, and the field
		// lives only there.
		{typ: "contract", field: "x_binding", want: "envelope/v2"},
	} {
		t.Run(tc.typ+"/"+tc.field, func(t *testing.T) {
			t.Parallel()

			in := fixedInput(tc.typ, renderNewProbeID(tc.typ))
			in.Fields = map[string]string{tc.field: "none"}
			raw, err := template.RenderNew(in, isJSONSchema)
			if err != nil {
				t.Fatalf("RenderNew refused --field %s=none: %v\n"+
					"The field exists on %s's %s schema. If the sniff pass is applying caller fields again, "+
					"every v2-only field is unreachable from `a2a new` while the reachability gate stays green.",
					tc.field, tc.typ, tc.want, err)
			}
			fm, perr := artifact.ParseFrontmatter(raw)
			if perr != nil {
				t.Fatalf("ParseFrontmatter: %v", perr)
			}
			instance, derr := schema.DecodeYAMLInstance(fm.YAML)
			if derr != nil {
				t.Fatalf("DecodeYAMLInstance: %v", derr)
			}
			obj, ok := instance.(map[string]any)
			if !ok {
				t.Fatalf("decoded %s draft is %T, not a mapping", tc.typ, instance)
			}
			if got := obj["schema"]; got != tc.want {
				t.Fatalf("%s rendered %v, want %s — the generation decision changed", tc.typ, got, tc.want)
			}
			if got := obj[tc.field]; got != "none" {
				t.Fatalf("%s: %s = %#v, want \"none\" — the override reached neither pass", tc.typ, tc.field, got)
			}
		})
	}
}

// renderNewProbeID returns an id whose prefix the given type's own schema
// accepts — a contract is a STANDING id (XC-<system>-<slug>), an exchange is
// date-bearing, and a mismatched prefix would red the probe above for the
// wrong reason.
func renderNewProbeID(typ string) string {
	if typ == "contract" {
		return "XC-axon-renderprobe"
	}
	return "XW-axon-20260721-k3f9"
}
