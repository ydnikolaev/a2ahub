package schema_test

// This file lives in package schema_test (an external test package), not
// package schema, for one structural reason: the reachability question can
// only be answered by asking internal/template's Render — the actual code
// path behind `a2a new --field` — and internal/template imports
// internal/schema (for the canonical type list and the field-type/fill-class
// lookups schemaAllowsField needs). A test inside package schema therefore
// cannot import internal/template without creating an import cycle. Placing
// the test in the same directory's external test package is legal Go and
// keeps the assertion honest: it asks the shipped surface rather than
// re-deriving its rule.

import (
	"errors"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"gopkg.in/yaml.v3"
)

// probeTime is a fixed, arbitrary "now" — Render never calls time.Now()
// itself (package doc, internal/template/errors.go), so any fixed value is
// fine here.
var probeTime = time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)

const probeValue = "reachability-probe-value"

// probeID returns an ID Render will accept for typ/generation. Render only
// interprets the ID's shape for one case — a v2 contract, which must parse
// as a Standing ID with prefix "XC" so the `<slug>` token can be resolved
// (template.go's Render, the `in.Type == "contract" && ... "envelope/v2"`
// branch). Every other (type, generation) pair treats the ID as an opaque
// string.
func probeID(typ, generation string) string {
	if typ == "contract" && generation == "envelope/v2" {
		return "XC-axon-probe"
	}
	return "XQ-axon-probe"
}

func renderPlain(typ, generation string) ([]byte, error) {
	return template.Render(template.Input{
		Type:           typ,
		EnvelopeSchema: generation,
		ID:             probeID(typ, generation),
		Actor:          template.Actor{Kind: "agent", KindClaimed: true, Name: "reachability-probe"},
		Created:        probeTime,
	})
}

func renderWithField(typ, generation, field string) ([]byte, error) {
	return template.Render(template.Input{
		Type:           typ,
		EnvelopeSchema: generation,
		ID:             probeID(typ, generation),
		Actor:          template.Actor{Kind: "agent", KindClaimed: true, Name: "reachability-probe"},
		Created:        probeTime,
		Fields:         map[string]string{field: probeValue},
	})
}

// frontmatterRoot parses a rendered draft's frontmatter into its root
// mapping node, the same shape Render's own applyFills walks.
func frontmatterRoot(raw []byte) (*yaml.Node, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(fm.YAML, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter root is not a YAML mapping")
	}
	return doc.Content[0], nil
}

// pathPresent reports whether the fill-classes.yaml dotted/bracketed field
// path already exists as a structural node in a freshly rendered (unfilled)
// draft. That existence is itself the second authoring surface AC5 names —
// "a template placeholder": the key is already there, with the template's
// own placeholder literal, ready for a human or agent to hand-edit. This is
// the only way an array-item path (`refs[].ref`, `deliverables[].name`, ...)
// can ever be reached at all: applyFills's dotted-path pass is map-only by
// its own documented grammar and can never descend into a sequence index, so
// `--field refs[].ref=...` is refused unconditionally regardless of what the
// template carries.
func pathPresent(node *yaml.Node, path string) bool {
	if path == "" {
		return true
	}
	seg, rest, _ := strings.Cut(path, ".")
	isSeq := strings.HasSuffix(seg, "[]")
	key := strings.TrimSuffix(seg, "[]")

	if node.Kind != yaml.MappingNode {
		return false
	}
	var val *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			val = node.Content[i+1]
			break
		}
	}
	if val == nil {
		return false
	}
	if !isSeq {
		if rest == "" {
			return true
		}
		return pathPresent(val, rest)
	}
	if val.Kind != yaml.SequenceNode || len(val.Content) == 0 {
		return false
	}
	if rest == "" {
		return true
	}
	for _, item := range val.Content {
		if pathPresent(item, rest) {
			return true
		}
	}
	return false
}

// envelopeCandidates maps a fill-classes.yaml group to the envelope
// generation string Render expects and the candidate type(s) to probe.
//
// A "base" group is shared by every envelope type of that generation, so
// reachability for a base-group field is an EXISTENTIAL question — reachable
// if ANY type's shipped template accepts it — not a universal one; a field
// only some drafts want cannot live in every type's template (spec 03 §11,
// 2026-08-08 amendment), so requiring every type to carry it would make the
// gate red on exactly the fields the phase's own mechanism was built to
// rescue.
//
// ok is false for anything that is not an `envelope/vN/...` group: `a2a new`
// and `--field` are envelope-only mechanisms (they render one of the 8
// schema.EnvelopeTypes() types), so a non-envelope group (event, manifest,
// consumes) is never addressable by either surface named in AC5 at all —
// that is a distinct fact from "reachable but refused", and is reported
// separately rather than forced through this predicate.
func envelopeCandidates(group string) (generation string, types []string, ok bool) {
	parts := strings.Split(group, "/")
	if len(parts) != 3 || parts[0] != "envelope" {
		return "", nil, false
	}
	switch parts[1] {
	case "v1":
		generation = ""
	case "v2":
		generation = "envelope/v2"
	default:
		return "", nil, false
	}
	if parts[2] == "base" {
		return generation, schema.EnvelopeTypes(), true
	}
	return generation, []string{parts[2]}, true
}

// shippedTemplate caches, per (generation, type), whether a template file is
// actually embedded, and its plain-rendered root node when it is.
type shippedTemplate struct {
	exists bool
	root   *yaml.Node
}

// TestEveryAuthorFieldIsReachableOrDeclaredUnreachable is spec 03 §8 AC5.
//
// Every AUTHOR field in an envelope group (the only groups either named
// authoring surface can reach at all — see envelopeCandidates) must be
// reachable through a real probe against internal/template.Render — the
// exact function `a2a new --field` calls — via either mechanism AC5 names:
// a `--field` override, or the field already existing as a template
// placeholder in the plain-rendered draft. A field that is neither must
// carry a non-blank reason in fill-classes.yaml's `unreachable:` map; a
// field that IS reachable must NOT also carry one — an exemption must not
// outlive the thing it exempts (TestExemptFamiliesAreRealAndReasoned's own
// rule, mirrored here for this second exemption list).
//
// Non-envelope groups (event/*, manifest/*, consumes/*), and an envelope
// group with no shipped template at all for a generation (envelope/v2/response
// ships no templates/v2/response.md and `a2a new` never selects envelope/v2
// for that type — see template.go's AuthoringEnvelopeSchema), are excluded
// from this gate's universe rather than forced into a required
// `unreachable:` entry: AC5's own two named mechanisms do not exist for
// those groups at all, which is a different, group-level fact from "this
// field is refused by an authoring surface that does exist". t.Logf reports
// the excluded counts so the exclusion is visible rather than silent.
//
// envelope/v2/work_request left this universe on 2026-08-10
// (specs/04-possession.md §11): schemas/templates/v2/work_request.md now
// ships, so its AUTHOR fields are judged like any other envelope group's —
// reachable, or carrying a real `unreachable:` reason (schemas/fill-
// classes.yaml).

// dedicatedFlagReaches is this gate's THIRD reachability mechanism, added
// 2026-08-19 (defects-fix-2026-08 P2/P3) because a fourth kind of authoring
// surface shipped and the gate modelled only two.
//
// AC5's two named mechanisms are `--field <key>=...` and an already-present
// template placeholder. Neither reaches a field whose only door is a
// PURPOSE-BUILT FLAG: `--field` writes one scalar node and is refused for an
// array or a mapping (its own comment says so, and that refusal is why `--ref`
// exists), while a live template placeholder for an optional block would make
// every fresh draft author a declaration nobody made (P-1).
//
// So `a2a respond --unmet` / `--blocked-by` DO reach these fields, and calling
// them "unreachable" in fill-classes.yaml would have been a lie in the one
// place this repo keeps honest — the alternative this gate offered, and the
// reason it is widened instead.
//
// Keyed group/field. A row here is a claim that a shipped flag writes that
// field; the flag's own test is what proves it.
var dedicatedFlagReaches = map[string]bool{
	"envelope/v2/response/unmet":                  true, // a2a respond --unmet
	"envelope/v2/response/blocked_by":             true, // a2a respond --blocked-by
	"envelope/v2/response/blocked_by.reason_code": true,
	"envelope/v2/response/blocked_by.owner":       true,
	"envelope/v2/response/blocked_by.needs":       true,
}

func TestEveryAuthorFieldIsReachableOrDeclaredUnreachable(t *testing.T) {
	table, err := schema.LoadFillClasses()
	if err != nil {
		t.Fatalf("load fill-classes.yaml: %v", err)
	}

	cache := map[string]shippedTemplate{}
	shipped := func(generation, typ string) shippedTemplate {
		key := generation + "|" + typ
		if c, ok := cache[key]; ok {
			return c
		}
		raw, rerr := renderPlain(typ, generation)
		var c shippedTemplate
		switch {
		case rerr == nil:
			root, perr := frontmatterRoot(raw)
			if perr != nil {
				t.Fatalf("parse plain-rendered %s/%s frontmatter: %v", generation, typ, perr)
			}
			c = shippedTemplate{exists: true, root: root}
		case errors.Is(rerr, fs.ErrNotExist):
			c = shippedTemplate{exists: false}
		default:
			// A shipped template exists but something else about this
			// (type, generation) pair is wrong (e.g. the v2 contract's ID
			// shape check). Treat as shipped so its fields are judged
			// rather than silently excluded — the failure surfaces on the
			// specific field probe instead.
			c = shippedTemplate{exists: true}
		}
		cache[key] = c
		return c
	}

	var groups []string
	for g := range table.Fields {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	excluded := map[string]int{}
	checked := 0

	for _, group := range groups {
		var authorFields []string
		for field, class := range table.Fields[group] {
			if class == schema.FillAuthor {
				authorFields = append(authorFields, field)
			}
		}
		if len(authorFields) == 0 {
			continue
		}
		sort.Strings(authorFields)

		generation, types, ok := envelopeCandidates(group)
		if !ok {
			excluded[group] += len(authorFields)
			continue
		}

		var shippedTypes []string
		for _, typ := range types {
			if shipped(generation, typ).exists {
				shippedTypes = append(shippedTypes, typ)
			}
		}
		if len(shippedTypes) == 0 {
			excluded[group] += len(authorFields)
			continue
		}

		for _, field := range authorFields {
			checked++
			// The third mechanism first — see dedicatedFlagReaches' own comment.
			reachable := dedicatedFlagReaches[group+"/"+field]
			for _, typ := range shippedTypes {
				if reachable {
					break
				}
				if _, ferr := renderWithField(typ, generation, field); ferr == nil {
					reachable = true
					break
				}
				if c := shipped(generation, typ); c.root != nil && pathPresent(c.root, field) {
					reachable = true
					break
				}
			}

			reason, declared := table.UnreachableReason(group, field)
			switch {
			case reachable && declared:
				t.Errorf("%s/%s: a probe (--field %s=... or an already-present template placeholder) reached it, but it is ALSO declared in fill-classes.yaml's unreachable map with reason %q — the exemption has outlived the thing it exempts; delete the entry", group, field, field, reason)
			case !reachable && !declared:
				t.Errorf("%s/%s is AUTHOR and neither `--field %s=...` nor an existing template placeholder reaches it, and fill-classes.yaml declares no unreachable reason for it — either make it reachable, or add %s/%s to the unreachable map with a real reason", group, field, field, group, field)
			}
		}
	}

	if checked == 0 {
		t.Fatal("no AUTHOR envelope field was checked — every envelope group excluded or empty; a green result here would mean nothing")
	}
	for group, n := range excluded {
		t.Logf("excluded from this gate's universe (no envelope authoring surface exists at all for this group): %s (%d AUTHOR field(s))", group, n)
	}

	// The exclusion set is PINNED, not merely logged, and this is the half
	// that keeps the gate honest rather than merely green.
	//
	// Exclusion is derived (a group whose template does not render has no
	// authoring surface to judge against), which is right — but a derived
	// exclusion can also GROW silently. Delete a template, or add a fill-class
	// group for a generation `a2a new` never selects, and 14 more AUTHOR
	// fields quietly leave the universe while this test stays green and says
	// so only in a log line nobody reads on a passing run. That is the exact
	// defect P8's resting-state gate was written to remove — "a gate green
	// because its universe is short" — so the universe's edge is a committed
	// expectation here, and widening it is a deliberate edit with a reason.
	//
	// The five below have no authoring surface AT ALL, which is a different
	// fact from `unreachable:`'s "a surface exists and refuses this field":
	// the first four ship no templates/** file and are authored by dedicated
	// verbs (`a2a note`, `a2a verify`, a hand-edited manifest); the one
	// envelope/v2 group has no templates/v2/<type>.md and `a2a new` never
	// selects envelope/v2 for that type (template.go's
	// AuthoringEnvelopeSchema). `envelope/v2/work_request` left this map on
	// 2026-08-10 when schemas/templates/v2/work_request.md shipped (specs/
	// 04-possession.md §11) — deleting the entry rather than leaving it is
	// this same rule applied to itself: an exemption must not outlive the
	// thing it exempts. Forcing per-field `unreachable:` rows for the
	// remaining five groups would state a group-level absence as a
	// field-by-field refusal, which is a stronger claim than is true.
	wantExcluded := map[string]bool{
		"consumes/v1/consumes": true,
		"event/v1/event":       true,
		"event/v2/event":       true,
		"manifest/v1/space":    true,
	}
	for group := range excluded {
		if !wantExcluded[group] {
			t.Errorf("%s left this gate's universe and nothing declared that it may — %d AUTHOR field(s) stopped being checked. "+
				"Either restore its authoring surface, or add it to wantExcluded with the reason it has none.", group, excluded[group])
		}
	}
	for group := range wantExcluded {
		if _, stillExcluded := excluded[group]; !stillExcluded {
			t.Errorf("%s is declared as having no authoring surface, but this gate now checks it — delete the wantExcluded entry rather than leaving an exemption that exempts nothing", group)
		}
	}

	// Every declared unreachable entry must name a real AUTHOR field —
	// mirroring TestExemptFamiliesAreRealAndReasoned's rule for out_of_scope
	// families: an exemption naming nothing real is a hole that reads as a
	// decision.
	for _, e := range table.UnreachableEntries() {
		class, ok := table.ClassOf(e.Group, e.Field)
		switch {
		case !ok:
			t.Errorf("unreachable entry %s/%s names a field schemas/fill-classes.yaml does not classify at all — delete the entry, it exempts nothing", e.Group, e.Field)
		case class != schema.FillAuthor:
			t.Errorf("unreachable entry %s/%s is classified %s, not AUTHOR — only an AUTHOR field can be structurally unreachable from an authoring surface; a %s field is refused by a different, unrelated mechanism", e.Group, e.Field, class, class)
		}
	}
}
