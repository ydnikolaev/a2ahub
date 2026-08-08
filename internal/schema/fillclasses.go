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
