package validate

import (
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"gopkg.in/yaml.v3"
)

// ValidateConsumes validates one `<system>/consumes.yaml` — the D-022
// registered-consumer registry (§5.2.3) — against the embedded
// `consumes/v1` schema.
//
// It is a NON-artifact file: no envelope, no id, no frontmatter, so it
// never goes through ValidateDraft/ValidateForSubmit's envelope path. It
// still reports the same Result/Violation shape as everything else, because
// every consumer of this engine (the CLI verb, the V3 CI check, the hub)
// parses exactly one wire contract (D-011).
//
// Until this existed, nothing validated the file at all: the CI check only
// looked at `*.md` under a participant section, so a `consumes.yaml` that
// the schema rejects outright merged silently and simply failed to register
// anyone as a consumer — the exact failure mode the first external
// consumer's space was in (fb-20260723-9ae145's sibling finding).
func (e *Engine) ValidateConsumes(raw []byte) (Result, error) {
	const op = "ValidateConsumes"

	var instance any
	if err := yaml.Unmarshal(raw, &instance); err != nil {
		return newResult(V2, "", []Violation{malformedConsumesViolation()}), nil
	}

	var probe struct {
		Schema string `yaml:"schema"`
		System string `yaml:"system"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return newResult(V2, "", []Violation{malformedConsumesViolation()}), nil
	}

	// A DECLARED version this binary does not understand is POL-005,
	// exactly as it is for an envelope — the one-cycle overlap window is a
	// property of the binary, not of the family being validated. A file
	// with NO `schema:` field at all is a different case: it is checked
	// against the current schema, so the operator gets the real,
	// actionable errors (the missing `schema` field among them) rather
	// than an opaque "unknown version".
	against := schema.CurrentConsumesSchema()
	if probe.Schema != "" {
		n, ok := schema.ParseVersion(probe.Schema)
		if !ok || !schema.AcceptsConsumesVersion(n) {
			return newResult(V2, probe.System, []Violation{{
				Code:     "POL-005",
				Class:    ClassPolicy,
				Path:     "schema",
				Message:  "consumes.yaml schema version is outside the one-cycle overlap window this binary understands",
				CCRef:    "CC-005",
				Severity: SeverityReject,
			}}), nil
		}
		against = probe.Schema
	}

	fieldViolations, serr := e.corpus.ValidateConsumes(against, instance)
	if serr != nil {
		return Result{}, &Error{Op: op, Err: serr}
	}
	violations, merr := mapSchemaViolations(fieldViolations)
	if merr != nil {
		return Result{}, &Error{Op: op, Err: merr}
	}
	return newResult(V2, probe.System, violations), nil
}

// malformedConsumesViolation is the non-artifact twin of
// malformedFrontmatterViolation: the file is not parseable YAML at all, so
// no schema check can say anything more specific. It reuses POL-002 — the
// registry's one "this document is not valid YAML" code (its title names
// frontmatter because artifacts were the only YAML the engine saw when the
// registry was authored; the substance is identical) rather than minting a
// code this phase is not authorised to add.
func malformedConsumesViolation() Violation {
	return Violation{
		Code:     "POL-002",
		Class:    ClassPolicy,
		Path:     "",
		Message:  "consumes.yaml is not valid YAML",
		CCRef:    "CC-001",
		Severity: SeverityReject,
	}
}
