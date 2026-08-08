package schema

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func loadTable(t *testing.T) FillClassTable {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "fill-classes.yaml"))
	if err != nil {
		t.Fatalf("read fill-classes.yaml: %v", err)
	}
	table, err := ParseFillClasses(raw)
	if err != nil {
		t.Fatalf("parse fill-classes.yaml: %v", err)
	}
	return table
}

// TestFillClassesCoverEveryCorpusField is spec 03's AC1. Every field of every
// registered schema has a declared class, or its family is declared out of
// scope with a reason.
//
// A missing row is never defaulted to AUTHOR. That default is what the whole
// table exists to prevent: a newly added TOOL field would become silently
// fillable, which is exactly how `actor.name` ended up carrying an OS
// username in a slot meant for a detected agent identity.
//
// The universe is CorpusGroups(), not EnvelopeTypes(), and the difference
// decides whether this gate can see the fields three later phases add — see
// CorpusGroups' own doc comment.
func TestFillClassesCoverEveryCorpusField(t *testing.T) {
	t.Parallel()

	table := loadTable(t)
	exempt := map[string]bool{}
	for _, f := range table.ExemptFamilies() {
		exempt[f] = true
	}

	var missing []string
	checked := 0
	for _, group := range CorpusGroups() {
		if exempt[strings.SplitN(group, "/", 2)[0]] {
			continue
		}
		paths, err := FieldPaths(group)
		if err != nil {
			t.Fatalf("field paths for %s: %v", group, err)
		}
		if len(paths) == 0 {
			t.Errorf("%s contributed no field paths — a schema that reflects as empty would satisfy this gate while declaring nothing", group)
			continue
		}
		for _, p := range paths {
			checked++
			if _, ok := table.ClassOf(group, p); !ok {
				missing = append(missing, group+" · "+p)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no fields were checked at all — every family exempt, or the corpus reflected as empty; either way a green result here would mean nothing")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%d field(s) of the schema corpus have no fill-class row:\n  %s\n"+
			"Add a row to schemas/fill-classes.yaml. A field with no row is not AUTHOR by default — it is unclassified, and an unclassified TOOL field is fillable by anyone.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestFillClassesDeclareNoFieldTheCorpusLacks is the other direction. A row
// for a field no schema has reads as coverage and guards nothing — and worse,
// it survives a schema rename silently, so the field that replaced it is
// unclassified while the table still looks complete.
func TestFillClassesDeclareNoFieldTheCorpusLacks(t *testing.T) {
	t.Parallel()

	table := loadTable(t)
	corpus := map[string]map[string]bool{}
	for _, group := range CorpusGroups() {
		paths, err := FieldPaths(group)
		if err != nil {
			continue
		}
		set := map[string]bool{}
		for _, p := range paths {
			set[p] = true
		}
		corpus[group] = set
	}

	var surplus []string
	for group, rows := range table.Fields {
		known, ok := corpus[group]
		if !ok {
			surplus = append(surplus, group+" (whole group — no such registered schema)")
			continue
		}
		for field := range rows {
			if !known[field] {
				surplus = append(surplus, group+" · "+field)
			}
		}
	}

	if len(surplus) > 0 {
		sort.Strings(surplus)
		t.Fatalf("%d fill-class row(s) name a field the corpus does not have:\n  %s\n"+
			"Delete the row. A stale row reads as a considered classification forever while classifying nothing.",
			len(surplus), strings.Join(surplus, "\n  "))
	}
}

// TestExemptFamiliesAreRealAndReasoned keeps the out-of-scope list honest: it
// may only name families the corpus actually registers, so an exemption
// cannot outlive the thing it exempts.
func TestExemptFamiliesAreRealAndReasoned(t *testing.T) {
	t.Parallel()

	table := loadTable(t)
	families := map[string]bool{}
	for _, group := range CorpusGroups() {
		families[strings.SplitN(group, "/", 2)[0]] = true
	}
	for _, e := range table.OutOfScope {
		if !families[e.Family] {
			t.Errorf("out_of_scope names family %q, which the corpus does not register — delete the entry rather than leaving a dead exemption", e.Family)
		}
	}
}

// TestActorFieldsAreDetected pins the classification the whole table was
// built for. `actor.name` carrying an OS username, indistinguishable from a
// detected agent identity, is the defect Matrix A found; if these rows ever
// drift to AUTHOR the non-typeability rule stops applying to them and the
// defect is legal again.
func TestActorFieldsAreDetected(t *testing.T) {
	t.Parallel()

	table := loadTable(t)
	for _, group := range []string{"envelope/v1/base", "envelope/v2/base"} {
		for _, field := range []string{"actor.kind", "actor.name", "actor.model", "actor.session"} {
			class, ok := table.ClassOf(group, field)
			if !ok {
				t.Errorf("%s has no row for %s", group, field)
				continue
			}
			if class != FillDetected {
				t.Errorf("%s.%s is %q, want DETECTED — it is resolved from the environment, and classifying it otherwise makes an OS username a legal value again", group, field, class)
			}
		}
	}
}

// TestMintedFieldsAreTool pins the other half of the rule the table states:
// what Go code mints, an agent may not type.
func TestMintedFieldsAreTool(t *testing.T) {
	t.Parallel()

	table := loadTable(t)
	for _, tc := range []struct{ group, field string }{
		{"envelope/v1/base", "id"},
		{"envelope/v1/base", "schema"},
		{"envelope/v1/base", "type"},
		{"envelope/v1/base", "created"},
		{"event/v1/event", "event"},
		{"event/v1/event", "at"},
	} {
		class, ok := table.ClassOf(tc.group, tc.field)
		if !ok {
			t.Errorf("%s has no row for %s", tc.group, tc.field)
			continue
		}
		if class != FillTool {
			t.Errorf("%s.%s is %q, want TOOL — it is minted or stamped by the tool, never typed", tc.group, tc.field, class)
		}
	}
}
