package schema

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/schemas"
	"gopkg.in/yaml.v3"
)

// FillClass says who is allowed to put a value in a field.
//
// The rule the classification exists to enforce is "what the TOOL
// establishes, an agent may not type". A field the environment resolves is
// not a field an agent gets to assert — and the defect that motivated it is
// `actor.name`, where an OS username reached a slot meant for a detected
// agent identity with nothing recording which one it was.
type FillClass string

const (
	// FillTool is minted by Go code. No authoring surface may accept it.
	FillTool FillClass = "TOOL"
	// FillDetected is resolved from the environment. It may be overridden
	// ONLY with the record saying it was — the rule is "may not be typed
	// SILENTLY", never "may not be typed" (P3 D5). Removing the override
	// would reverse a documented, test-pinned precedence order.
	FillDetected FillClass = "DETECTED"
	// FillAuthor is intent the agent must supply. The only class where an
	// empty value is the agent's problem rather than the tool's.
	FillAuthor FillClass = "AUTHOR"
	// FillConfig comes from the manifest, project config or harness, with an
	// explicit override available.
	FillConfig FillClass = "CONFIG"
	// FillDefaulted is a template or schema default that stands unless
	// overridden.
	//
	// This is the fifth class, and it is DEFAULTED rather than the
	// NEGOTIATED spec 03 §T2 lists. The spec declares itself to be Matrix A
	// "made enforceable"; Matrix A defines DEFAULTED, uses it, and contains
	// no NEGOTIATED at all. The named source wins over the summary of it.
	FillDefaulted FillClass = "DEFAULTED"
)

// fillClasses is every class a row may declare. A value outside this set is
// a typo that would otherwise read as a considered decision.
var fillClasses = map[FillClass]bool{
	FillTool: true, FillDetected: true, FillAuthor: true, FillConfig: true, FillDefaulted: true,
}

// FillClassTable is schemas/fill-classes.yaml, parsed.
type FillClassTable struct {
	Schema     string                          `yaml:"schema"`
	OutOfScope []FillClassExemption            `yaml:"out_of_scope"`
	Fields     map[string]map[string]FillClass `yaml:"fields"`
}

// FillClassExemption names a schema family with no authoring path, and why.
// Classifying its fields AUTHOR wholesale would be coverage without meaning:
// no agent ever fills them one at a time.
type FillClassExemption struct {
	Family string `yaml:"family"`
	Reason string `yaml:"reason"`
}

// ParseFillClasses decodes the table and refuses anything structurally
// unusable. It validates SHAPE only — whether the table is COMPLETE against
// the schema corpus is the gate's question, because completeness is a
// property of two files rather than of this one.
func ParseFillClasses(raw []byte) (FillClassTable, error) {
	var t FillClassTable
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return FillClassTable{}, fmt.Errorf("schema: parse fill-classes: %w", err)
	}
	if t.Schema != "fill-classes/v1" {
		return FillClassTable{}, fmt.Errorf("schema: fill-classes: schema is %q, want fill-classes/v1", t.Schema)
	}
	if len(t.Fields) == 0 {
		return FillClassTable{}, fmt.Errorf("schema: fill-classes: no field groups — an empty table would let the completeness gate pass while policing nothing")
	}
	for group, rows := range t.Fields {
		if len(rows) == 0 {
			return FillClassTable{}, fmt.Errorf("schema: fill-classes: group %q has no rows", group)
		}
		for field, class := range rows {
			if !fillClasses[class] {
				return FillClassTable{}, fmt.Errorf("schema: fill-classes: %s.%s declares class %q, which is not one of TOOL, DETECTED, AUTHOR, CONFIG, DEFAULTED", group, field, class)
			}
		}
	}
	for _, e := range t.OutOfScope {
		if e.Family == "" || strings.TrimSpace(e.Reason) == "" {
			return FillClassTable{}, fmt.Errorf("schema: fill-classes: an out_of_scope entry is missing its family or its reason — an exemption without a reason is a hole that reads as a decision")
		}
	}
	return t, nil
}

// ClassOf returns the declared class for one field of one schema, and
// whether a row exists. A missing row is NOT defaulted to AUTHOR: that would
// make a newly added TOOL field silently fillable, which is the exact defect
// the table exists to prevent.
func (t FillClassTable) ClassOf(group, field string) (FillClass, bool) {
	rows, ok := t.Fields[group]
	if !ok {
		return "", false
	}
	class, ok := rows[field]
	return class, ok
}

// ExemptFamilies returns the families declared out of scope, sorted.
func (t FillClassTable) ExemptFamilies() []string {
	out := make([]string, 0, len(t.OutOfScope))
	for _, e := range t.OutOfScope {
		out = append(out, e.Family)
	}
	sort.Strings(out)
	return out
}

// CorpusGroups returns every (family/vN/type) group key the schema corpus
// registers — the universe the completeness gate checks the table against.
//
// It is derived from corpusDefinitions rather than from EnvelopeTypes(),
// and the difference is load-bearing. EnvelopeTypes returns eight envelope
// type NAMES and is version-agnostic: keyed on those, this table could not
// see manifest/v1/space (where P5 puts `capabilities`), nor event/v* (where
// P6 puts `verdicts[]`), nor tell v1 `work_request` from the v2 one P4
// creates — which is the entire point of P4's own D1. All three phases
// declare they consume rows from here; keyed wrongly it would silently
// exempt exactly their fields.
func CorpusGroups() []string {
	out := make([]string, 0, len(corpusDefinitions))
	for _, d := range corpusDefinitions {
		out = append(out, fmt.Sprintf("%s/v%d/%s", d.key.family, d.key.version, d.key.typ))
	}
	sort.Strings(out)
	return out
}

// FieldPaths returns every field path in one registered schema, in the dotted
// form the table uses: nested objects join with ".", and an array's items
// contribute "[]" before their own properties (refs[].ref).
func FieldPaths(group string) ([]string, error) {
	for _, d := range corpusDefinitions {
		if fmt.Sprintf("%s/v%d/%s", d.key.family, d.key.version, d.key.typ) != group {
			continue
		}
		raw, err := schemas.FS.ReadFile(d.path)
		if err != nil {
			return nil, fmt.Errorf("schema: read %s: %w", d.path, err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("schema: decode %s: %w", d.path, err)
		}
		var out []string
		collectFieldPaths(doc, "", &out)
		sort.Strings(out)
		return out, nil
	}
	return nil, fmt.Errorf("schema: no registered schema for group %q", group)
}

func collectFieldPaths(node map[string]any, prefix string, out *[]string) {
	if props, ok := node["properties"].(map[string]any); ok {
		for name, child := range props {
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			*out = append(*out, path)
			if sub, ok := child.(map[string]any); ok {
				collectFieldPaths(sub, path, out)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		collectFieldPaths(items, prefix+"[]", out)
	}
}

// EnvelopeFieldPaths returns every top-level field an envelope of this
// generation and type may legally carry — the union of the shared base and
// the per-type schema.
//
// The union is the point. A per-type schema reaches its shared fields
// through `$ref`, so reflecting over its own `properties` alone yields four
// names for `question` and misses the thirty-one on the base. A caller
// asking "may this artifact carry `origin`?" and reading only the per-type
// schema gets "no" for every type, which is how five schema-legal fields
// came to be unreachable from any authoring surface.
func EnvelopeFieldPaths(version int, typ string) ([]string, error) {
	base, err := FieldPaths(fmt.Sprintf("envelope/v%d/%s", version, typeBase))
	if err != nil {
		return nil, err
	}
	own, err := FieldPaths(fmt.Sprintf("envelope/v%d/%s", version, typ))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(base)+len(own))
	out := make([]string, 0, len(base)+len(own))
	for _, p := range append(base, own...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// LoadFillClasses parses the embedded schemas/fill-classes.yaml. It is the
// accessor an authoring surface uses to ask "may a caller type this field",
// so the classification is enforced from the same bytes the completeness
// gate checks rather than from a second copy.
func LoadFillClasses() (FillClassTable, error) {
	raw, err := schemas.FS.ReadFile("fill-classes.yaml")
	if err != nil {
		return FillClassTable{}, fmt.Errorf("schema: read fill-classes.yaml: %w", err)
	}
	return ParseFillClasses(raw)
}

// Typeable reports whether a caller may supply this field's value directly.
//
// TOOL and DETECTED are refused: those are the two classes the epic's rule
// names — what the tool establishes, an agent may not type. Everything else
// is the agent's to state, including a field with no row, because refusing an
// unclassified field would make the completeness gate's own failure mode
// "authoring stops working" rather than "the gate goes red".
func (t FillClassTable) Typeable(group, field string) bool {
	class, ok := t.ClassOf(group, field)
	if !ok {
		return true
	}
	return class != FillTool && class != FillDetected
}

// EnvelopeFieldType returns the JSON-Schema `type` declared for a top-level
// envelope field, or "" when the field is absent or declares none.
//
// It exists because "the schema has this field" and "a caller may supply it
// as one scalar" are different questions. `origin` is an array,
// `expected_response` an object, and appending a bare scalar for either
// writes a draft the schema refuses — with nothing between render and disk
// to say so, since `a2a new` writes the file straight after rendering.
func EnvelopeFieldType(version int, typ, field string) (string, error) {
	for _, group := range []string{
		fmt.Sprintf("envelope/v%d/%s", version, typ),
		fmt.Sprintf("envelope/v%d/%s", version, typeBase),
	} {
		for _, d := range corpusDefinitions {
			if fmt.Sprintf("%s/v%d/%s", d.key.family, d.key.version, d.key.typ) != group {
				continue
			}
			raw, err := schemas.FS.ReadFile(d.path)
			if err != nil {
				return "", fmt.Errorf("schema: read %s: %w", d.path, err)
			}
			var doc struct {
				Properties map[string]struct {
					Type string `yaml:"type"`
				} `yaml:"properties"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				return "", fmt.Errorf("schema: decode %s: %w", d.path, err)
			}
			if p, ok := doc.Properties[field]; ok && p.Type != "" {
				return p.Type, nil
			}
		}
	}
	return "", nil
}
