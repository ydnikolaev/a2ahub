package datapackage

import (
	"encoding/json"
	"fmt"
)

// Result is verification-report/v1's verdict (§T2.3): pass, fail or error,
// derived from checks[], never authored directly.
type Result string

// Result values, in the precedence NewReport derives them by: error beats
// fail beats pass — one erroring check makes the whole report an error
// even if others merely failed, and any failing check makes it a fail even
// if the rest passed.
const (
	ResultPass  Result = "pass"
	ResultFail  Result = "fail"
	ResultError Result = "error"
)

// CheckStatus is one checks[] entry's own outcome (§T2.3).
type CheckStatus string

// The three check outcomes, deliberately the same vocabulary the shipped
// conformance core already uses: a consumer reading a verification report and
// a `contract check` result should not have to learn two words for one thing.
const (
	CheckPass  CheckStatus = "pass"
	CheckFail  CheckStatus = "fail"
	CheckError CheckStatus = "error"
)

// InstanceViolation mirrors internal/contract's InstanceViolation field
// names and RFC 6901 pointer semantics — an empty root pointer ("") is
// legitimate and is never rejected here — without importing
// internal/contract: this package owns no contract typing (spec 05a §5,
// plan D-3). A caller that has an internal/contract.InstanceViolation
// converts at its own boundary; this package's own callers stay free of
// that dependency.
type InstanceViolation struct {
	InstancePointer string `json:"instance_pointer"`
	SchemaPointer   string `json:"schema_pointer"`
	Message         string `json:"message"`
}

// Check is one checks[] entry (§T2.3): a stable check id, the target entry
// path, its own pass|fail|error outcome, and its bounded violations.
type Check struct {
	ID         string              `json:"id"`
	Path       string              `json:"path"`
	Status     CheckStatus         `json:"status"`
	Violations []InstanceViolation `json:"violations"`
}

// NewCheck builds a Check, defaulting a nil violations slice to an empty,
// non-nil one. checks[].violations is always a required array in the wire
// shape, never null, even for a passing check with nothing to report — a
// bare Check{} literal with a nil Violations field would otherwise
// marshal to `"violations":null`, which the schema's `"type":"array"`
// rejects.
func NewCheck(id, path string, status CheckStatus, violations []InstanceViolation) Check {
	if violations == nil {
		violations = []InstanceViolation{}
	}
	return Check{ID: id, Path: path, Status: status, Violations: violations}
}

// PackageRef is the report's own local record of which package it judged
// and what it verified that package's bytes to be (§T2.3 "package").
type PackageRef struct {
	ID              string `json:"id"`
	AggregateDigest string `json:"aggregate_digest"`
}

// Observed is the consumer's own re-counted numbers (§T2.3) — never the
// producer's claim copied. ContractSupersededBy is L-2's observation: set
// when a newer version of the same contract existed at verify time; the
// verdict itself (Result) is unaffected by it.
type Observed struct {
	RecordCount          int64  `json:"record_count"`
	SizeBytes            int64  `json:"size_bytes"`
	DurationMS           int64  `json:"duration_ms"`
	ContractSupersededBy string `json:"contract_superseded_by,omitempty"`
}

// reportSchema is verification-report/v1's own literal schema const.
const reportSchema = "verification-report/v1"

// Report is the Go shape of verification-report/v1 (§T2.3): a verdict
// bound to exact bytes. Every field is exported and settable except
// Result, which has no exported setter anywhere in this package — NewReport
// is the only exported way to attach one to a set of Checks, and it always
// derives it FROM those Checks. There is no exported way to construct a
// Report whose Result contradicts its own Checks using this package's Go
// API.
//
// That guarantee is about THIS package's own construction path, not about
// every byte that could ever claim to be a verification-report/v1
// document: UnmarshalJSON below deliberately reads a declared "result"
// verbatim rather than recomputing it, so a report.json forged or
// corrupted outside this package's API still decodes — and its
// self-contradiction is then caught by the schema's own result-derivation
// conditional (schemas/verification-report/v1/verification-report.schema.
// json), not silently repaired by this type. See UnmarshalJSON's own doc
// comment for why recomputing on decode would be the wrong fix.
type Report struct {
	Schema     string     `json:"schema"`
	ID         string     `json:"id"`
	Thread     string     `json:"thread"`
	Package    PackageRef `json:"package"`
	Contract   string     `json:"contract"`
	Consumer   string     `json:"consumer"`
	Checks     []Check    `json:"checks"`
	Observed   Observed   `json:"observed"`
	StartedAt  string     `json:"started_at"`
	FinishedAt string     `json:"finished_at"`

	// result is unexported: see the Report doc comment above. Result()
	// reads it; nothing outside this file writes it except NewReport.
	result Result
}

// NewReport builds a Report and derives its Result from checks (error >
// fail > pass — see ResultError/ResultFail/ResultPass). checks must be
// non-empty: a verdict with nothing checked is not a verdict, which
// mirrors the schema's own checks minItems:1.
func NewReport(id, thread string, pkg PackageRef, contract, consumer string, checks []Check, observed Observed, startedAt, finishedAt string) (Report, error) {
	if len(checks) == 0 {
		return Report{}, fmt.Errorf("datapackage: NewReport requires at least one check")
	}
	return Report{
		Schema:     reportSchema,
		ID:         id,
		Thread:     thread,
		Package:    pkg,
		Contract:   contract,
		Consumer:   consumer,
		Checks:     append([]Check(nil), checks...),
		Observed:   observed,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		result:     deriveResult(checks),
	}, nil
}

// deriveResult is the ONLY place a Result is ever computed from Checks —
// spec 05a §6.2's "result derivation" property.
func deriveResult(checks []Check) Result {
	hasFail := false
	for _, c := range checks {
		if c.Status == CheckError {
			return ResultError
		}
		if c.Status == CheckFail {
			hasFail = true
		}
	}
	if hasFail {
		return ResultFail
	}
	return ResultPass
}

// Result returns the report's verdict. There is no corresponding setter.
func (r Report) Result() Result { return r.result }

// MarshalJSON emits Result under its wire name "result" alongside every
// exported field, using the standard unexported-field-via-type-alias
// idiom: `alias` is a distinct named type with Report's exact field set
// but none of Report's own methods (so embedding it here cannot recurse
// back into this MarshalJSON), and the anonymous wrapper struct adds the
// one field that alias's own automatic marshaling cannot see.
func (r Report) MarshalJSON() ([]byte, error) {
	type alias Report
	return json.Marshal(struct {
		alias
		Result Result `json:"result"`
	}{alias: alias(r), Result: r.result})
}

// UnmarshalJSON reads every field, including "result", exactly as
// declared in the input — it deliberately does NOT recompute Result from
// Checks. Recomputing on decode would silently repair a report.json this
// package's own API could never have produced (one hand-authored, or
// corrupted, or forged outside NewReport) into a self-consistent one,
// destroying the property spec 05a §6.2 requires: that such a document is
// rejected by the schema's own result-derivation conditional. A report
// this package produced round-trips unaffected either way, because
// NewReport already made Result consistent before the first encode.
func (r *Report) UnmarshalJSON(data []byte) error {
	type alias Report
	aux := struct {
		*alias
		Result Result `json:"result"`
	}{alias: (*alias)(r)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.result = aux.Result
	return nil
}
