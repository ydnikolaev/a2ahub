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
