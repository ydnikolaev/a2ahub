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

// TestRenderEnumDefaultAndFieldOverride: category (an enum-placeholder
// field) defaults to its first alternative absent an override, and an
// explicit Fields override wins.
func TestRenderEnumDefaultAndFieldOverride(t *testing.T) {
	t.Parallel()

	in := fixedInput("question", "XQ-axon-20260721-k3f9")
	out, err := template.Render(in)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !containsLine(string(out), "category: clarification") {
		t.Errorf("expected default category=clarification (first enum alt); got:\n%s", out)
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

	for _, typ := range schema.EnvelopeTypes() {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()
			in := fixedInput(typ, prefixes[typ])
			in.Fields = map[string]string{"thread": "thread:axon-20260721-k3f9"}
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
