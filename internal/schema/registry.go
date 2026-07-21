package schema

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RegistryEntry is one {code, class, title, applies_to} row of the SSOT
// data file schemas/errors/v1/registry.yaml (§5, ADR-001 Open Q2
// resolution). Go code never carries a second, hand-written copy of this
// table — it loads the embedded file and mirrors it.
type RegistryEntry struct {
	Code      string `yaml:"code"`
	Class     string `yaml:"class"`
	Title     string `yaml:"title"`
	AppliesTo string `yaml:"applies_to"`
}

// Registry is the parsed, embedded schemas/errors/v1/registry.yaml.
type Registry struct {
	entries []RegistryEntry
	byCode  map[string]RegistryEntry
}

type registryDoc struct {
	Entries []RegistryEntry `yaml:"entries"`
}

// LoadRegistry parses raw (the exact bytes of registry.yaml) into a
// Registry. It never mutates the input and never carries derived state
// beyond a code -> entry index.
func LoadRegistry(raw []byte) (*Registry, error) {
	const op = "LoadRegistry"
	var doc registryDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %v", ErrRegistryLoad, err)}
	}
	byCode := make(map[string]RegistryEntry, len(doc.Entries))
	for _, e := range doc.Entries {
		if e.Code == "" {
			return nil, &Error{Op: op, Err: fmt.Errorf("%w: entry with empty code", ErrRegistryLoad)}
		}
		if _, dup := byCode[e.Code]; dup {
			return nil, &Error{Op: op, Input: e.Code, Err: fmt.Errorf("%w: duplicate code", ErrRegistryLoad)}
		}
		byCode[e.Code] = e
	}
	return &Registry{entries: doc.Entries, byCode: byCode}, nil
}

// Has reports whether code is a known registry entry.
func (r *Registry) Has(code string) bool {
	_, ok := r.byCode[code]
	return ok
}

// Entry returns the registry row for code, if any.
func (r *Registry) Entry(code string) (RegistryEntry, bool) {
	e, ok := r.byCode[code]
	return e, ok
}

// Codes returns every code in the registry, in file order.
func (r *Registry) Codes() []string {
	out := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Code)
	}
	return out
}

// CodesInClass returns every code in the registry belonging to class
// ("schema", "referential", "lifecycle", or "policy").
func (r *Registry) CodesInClass(class string) []string {
	var out []string
	for _, e := range r.entries {
		if e.Class == class {
			out = append(out, e.Code)
		}
	}
	return out
}
