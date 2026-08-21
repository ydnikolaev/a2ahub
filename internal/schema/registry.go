package schema

import (
	"fmt"
	"sort"

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
	// ModeScope is ADR-011 D3's declaration, read by the CI surface rather
	// than restated there: `v3-pr` means the code refuses at the write gate
	// and returns NO verdict at the post-merge audit, because a rule that
	// can only be satisfied by editing an immutable artifact must not judge
	// immutable history. `both` (or empty) means it judges everywhere.
	//
	// It exists so the suppression is DERIVED. cmd_validate_ci.go carried
	// one hardcoded `SuppressingCode("REF-017")` literal, spec 01's AC5
	// asked for it to become a declaration, and by the end of this epic
	// there were THREE such literals — the copy-paste the AC predicted.
	ModeScope string `yaml:"mode_scope"`
	// EmittedBy names the PACKAGE that raises this code, and exists because
	// two gates had been assuming the answer rather than reading it.
	//
	// Every code minted before 2026-08-21 is a validate.Violation raised from
	// internal/validate (internal/cli re-presents two of them at its own
	// severity, which check-error-codes.sh's scan order already accounts for).
	// Both guards were built on that: check-error-codes.sh's obligation 0
	// correlates a code's declared severity with the `Severity:` literal
	// nearest its `Code:` literal, and internal/validate's TestRegistryClosure
	// requires every referential/lifecycle code to be produced by a path that
	// test exercises. Neither statement was written down; both were true by
	// accident of there being one emitter.
	//
	// REF-024 broke the accident. It is a WRITE-FUNNEL refusal living in
	// internal/space so BOTH surfaces inherit it through funnel.Submit
	// (ADR-004: a shared concern moves DOWN into a package both import, never
	// ACROSS). It carries no `Code:` literal because a funnel refusal is not a
	// Violation, and it has no severity to correlate because it refuses BY
	// CONSTRUCTION — there is no warning variant of "the package has not
	// landed". Giving it a Code:/Severity: pair inside internal/cli would have
	// greened the gate and forced the MCP surface to duplicate the mapping:
	// B22 exactly, the shape this epic's AC5 exists to prevent.
	//
	// Empty means internal/validate, so the 23 codes that predate this field
	// need no migration. The registry keeps declaring the fact; the gates stop
	// inferring it.
	EmittedBy string `yaml:"emitted_by"`
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
		return nil, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrRegistryLoad, err)}
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

// CodesScopedToWriteGate returns every code whose declared mode_scope is
// `v3-pr` — the set a post-merge audit must suppress (ADR-011 D3). Sorted,
// so a caller's behaviour cannot depend on registry order.
func (r *Registry) CodesScopedToWriteGate() []string {
	var out []string
	for _, e := range r.entries {
		if e.ModeScope == "v3-pr" {
			out = append(out, e.Code)
		}
	}
	sort.Strings(out)
	return out
}

// EmitterFor returns the package that raises code, resolving the documented
// default: an entry with no `emitted_by` is raised from internal/validate.
// Callers must go through this rather than reading EmittedBy directly, so the
// default lives in exactly one place.
func (r *Registry) EmitterFor(code string) string {
	e, ok := r.byCode[code]
	if !ok || e.EmittedBy == "" {
		return "internal/validate"
	}
	return e.EmittedBy
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
