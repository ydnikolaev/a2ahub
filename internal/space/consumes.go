package space

import "gopkg.in/yaml.v3"

// Consumes is a system's `consumes.yaml` — its declared dependencies on other
// systems' contracts (the D-022 registry: space-visible artifacts, not
// project-local config). It is the source of the dashboard's contract-dependency
// edges (consumer→provider). Schema: schemas/consumes/v1.
type Consumes struct {
	Schema       string       `yaml:"schema"`
	System       string       `yaml:"system"`
	Dependencies []Dependency `yaml:"dependencies"`
}

// Dependency is one declared dependency on a contract at a pinned major.
type Dependency struct {
	// Contract is the depended-on contract id (XC-<provider>-<slug>).
	Contract string `yaml:"contract"`
	// Major is the pinned major version the consumer builds against.
	Major int `yaml:"major"`
	// Since is the ISO date the dependency was declared.
	Since string `yaml:"since"`
	// Note is an optional free-text rationale.
	Note string `yaml:"note,omitempty"`
}

// ParseConsumes structurally parses a consumes.yaml. A syntax failure is a
// typed error wrapping ErrConsumesInvalid; this is a parse check only, not a
// schema-class validation (that is the engine's job).
func ParseConsumes(raw []byte) (Consumes, error) {
	const op = "ParseConsumes"
	var c Consumes
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Consumes{}, &Error{Op: op, Err: ErrConsumesInvalid}
	}
	return c, nil
}
