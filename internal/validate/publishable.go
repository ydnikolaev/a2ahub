// P37 wave B (spec 37 §T2, decision D-D). Two rules that must hold at
// BOTH enforcement layers — `contract publish` locally and `validate --ci`
// at merge — and therefore live here, beside CheckComputedCompatibility,
// rather than in whichever caller happened to need them first. That is the
// same reasoning §T2 gives for the compat core itself, and P35's scar: a
// client-side guard and a CI-side guard shipped separately diverged, and
// nothing noticed until the e2e tier caught it.
package validate

import "strings"

// IsJSONSchemaFormat reports whether a contract's declared schema_format
// is a JSON-Schema dialect.
//
// This is the gate on the whole §5.4b computed-compatibility rule, and it
// is exported so the two callers cannot answer it differently. §5.4b
// splits by format on purpose: for a JSON-Schema contract a minor/patch
// bump REQUIRES that every prior valid fixture still validates against the
// new schema; for openapi-3.x, proto3 or other, V3 checks only the bump
// declaration and fixture self-consistency, and deep compatibility is the
// owner's own CI duty. Running the fixture-revalidation rule over a proto3
// contract's files would not be strictness — it would be a refusal the
// design never asked for, on evidence this engine cannot read.
//
// The match is on the dialect prefix, not the exact string: a future
// json-schema-<draft> is still a JSON-Schema contract, and reading it as
// "other" would silently drop the guarantee for a whole format.
func IsJSONSchemaFormat(schemaFormat string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(schemaFormat)), "json-schema")
}

// PublishableInput carries the facts a publishability check needs, all
// resolved by the caller — this package reads nothing from disk. Counts
// rather than contents: whether a contract carries a schema and fixtures
// is a question about presence, and handing this function the bytes would
// invite it to start validating them, which is CheckComputedCompatibility's
// job.
type PublishableInput struct {
	// SchemaFormat is the contract descriptor's declared schema_format.
	SchemaFormat string
	// ContractID is used only to name the contract in the refusal.
	ContractID string
	// Schemas is how many schema/** files the contract publishes.
	Schemas int
	// ValidFixtures is how many fixtures/valid/** files it publishes.
	ValidFixtures int
	// InvalidFixtures is how many fixtures/invalid/** files it publishes.
	InvalidFixtures int
}

// CheckContractPublishable enforces plan §5.3: every contract must actually
// publish a schema and at least one valid and invalid fixture, or it is
// refused (POL-009).
//
// Why a refusal and not a warning. A published contract without executable
// examples cannot satisfy §5.3, and for JSON Schema specifically leaves
// §5.4b with no compatibility baseline. A guarantee a producer has to opt
// into is not a guarantee, and in a fleet where the producer is an agent,
// nobody reliably opts in. Requiring the baseline is what makes the checks
// able to bite.
//
// This presence rule applies to every schema_format. What remains format
// specific is DEEP validation: §5.4b is computed here only for JSON Schema,
// while openapi/proto/other leave fixture semantics to the owner's CI. The
// binary can still enforce §5.3's format-neutral directory contract.
//
// Returns nil when the contract may be published.
func CheckContractPublishable(in PublishableInput) *Violation {
	if in.Schemas > 0 && in.ValidFixtures > 0 && in.InvalidFixtures > 0 {
		return nil
	}

	var missing []string
	if in.Schemas == 0 {
		missing = append(missing, "no schema/** files")
	}
	if in.ValidFixtures == 0 {
		missing = append(missing, "no fixtures/valid/** files")
	}
	if in.InvalidFixtures == 0 {
		missing = append(missing, "no fixtures/invalid/** files")
	}

	return &Violation{
		Code:  "POL-009",
		Class: ClassPolicy,
		Message: "contract " + in.ContractID + " declares schema_format " + in.SchemaFormat +
			" but publishes " + strings.Join(missing, " and ") +
			"; plan §5.3 requires a machine-validatable schema plus positive and negative executable examples before a version can be trusted",
		Severity: SeverityReject,
	}
}
