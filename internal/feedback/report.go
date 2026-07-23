// Package feedback is the P25 agent-feedback core: draft/validate/ledger/
// triage/submit for the consumer-agent -> product-repo feedback channel
// (docs/features/v1-min-2026-07/specs/25-agent-feedback.md). Feedback is
// NOT an envelope (I1) — this package never runs internal/validate's
// actors/threads/to/from V2 engine; it owns its own report shape, its own
// feedback-local FB-### code table, and its own authoring template.
package feedback

// Violation is one feedback-native finding (spec §11 A1 — deliberately
// NOT validate.Violation, which carries envelope-specific fields
// {Class, CCRef, Severity} this domain has no use for). Code is always
// one of codes.yaml's FB-### entries.
type Violation struct {
	// Code is the feedback-local FB-### code (schemas/feedback/v1/codes.yaml).
	Code string
	// Field is a best-effort field path the violation concerns ("checks",
	// "status", ...), or "" for a whole-document finding (e.g. a secret
	// hit, whose source scan has no field-level granularity — see §11 A3
	// mapping note in validate.go).
	Field string
	// Message is a human-readable, one-line explanation.
	Message string
}

// Report is feedback's own validate result shape (§11 A1): `a2a feedback
// validate` mirrors only the `--ci` exit-code contract of `a2a validate
// --ci` (0 valid / 1 invalid / 2 usage) — never validate.Result itself.
type Report struct {
	Valid      bool
	Violations []Violation
}
