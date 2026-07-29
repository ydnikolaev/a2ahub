package space

import (
	"context"

	"gopkg.in/yaml.v3"
)

// Manifest is the parsed structural shape of a space's space.yaml (P2
// schema, schemas/manifest/v1/space.schema.json) — a structural YAML
// decode only. internal/space does not re-implement JSON-Schema
// validation or policy/referential checks (D-011); those are the
// ManifestValidator seam, wired to the real P2/P3 engines at P6.
type Manifest struct {
	Schema           string        `yaml:"schema"`
	Space            string        `yaml:"space"`
	MinBinaryVersion string        `yaml:"min_binary_version"`
	Participants     []Participant `yaml:"participants"`
	Vendored         []string      `yaml:"vendored,omitempty"`

	// Raw holds the exact bytes ParseManifest was given, so a caller can
	// hand them, unmodified, to a ManifestValidator seam that needs the
	// full (possibly schema-permissive, §Open Q1) document rather than
	// just this struct's typed subset.
	Raw []byte `yaml:"-"`
}

// Participant is one space.yaml participant entry: a system's section,
// its human owners (GitHub logins), and membership status.
type Participant struct {
	System  string   `yaml:"system"`
	Org     string   `yaml:"org"`
	Section string   `yaml:"section"`
	Owners  []string `yaml:"owners"`
	Status  string   `yaml:"status"` // "active" | "left"
	Joined  string   `yaml:"joined"` // date, format per schema
}

// ParseManifest structurally parses raw space.yaml bytes. Malformed YAML
// is a typed error wrapping ErrManifestInvalid; this is a syntax check
// only — schema/policy validity is the ManifestValidator seam's job.
func ParseManifest(raw []byte) (Manifest, error) {
	const op = "ParseManifest"
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return Manifest{}, &Error{Op: op, Err: ErrManifestInvalid}
	}
	m.Raw = raw
	return m, nil
}

// SystemForLogin resolves a GitHub login to its owning system via the
// participant→owners map (§4.2's "github-login→system-id" mapping used by
// V3 diff-authz; read-only helper here, enforcement itself is
// internal/validate). A participant marked left grants no authority. An
// ambiguous active mapping fails closed even if a caller bypassed the
// canonical manifest-policy gate. Returns ("", false) when unmapped or
// ambiguous (CC-097).
func (m Manifest) SystemForLogin(login string) (string, bool) {
	system := ""
	for _, p := range m.Participants {
		if p.Status != "active" {
			continue
		}
		for _, owner := range p.Owners {
			if owner == login {
				if system != "" && system != p.System {
					return "", false
				}
				system = p.System
			}
		}
	}
	return system, system != ""
}

// ManifestValidator is the consumer-side seam (rails ISP/DI) for space.yaml
// schema and authority-map policy validation. The canonical implementation is
// internal/validate, wired through the CLI adapter; min_binary_version remains
// a separate write-funnel guard (CC-085).
type ManifestValidator interface {
	// ValidateManifest checks raw space.yaml bytes and returns a non-nil
	// error describing every schema/policy violation found (or nil).
	ValidateManifest(ctx context.Context, raw []byte) error
}

// LoadManifest is the composed manifest-load operation the "manifest
// load/validate" work item names: structural YAML parse (ParseManifest)
// followed by the ManifestValidator seam's schema/policy check over the
// SAME raw bytes. A structural parse failure short-circuits before the
// seam is ever called (there is nothing valid to check); a validator
// error is returned wrapped, never swallowed.
func LoadManifest(ctx context.Context, raw []byte, v ManifestValidator) (Manifest, error) {
	const op = "LoadManifest"
	m, err := ParseManifest(raw)
	if err != nil {
		return Manifest{}, err
	}
	if v != nil {
		if err := v.ValidateManifest(ctx, raw); err != nil {
			return Manifest{}, &Error{Op: op, Err: err}
		}
	}
	return m, nil
}
