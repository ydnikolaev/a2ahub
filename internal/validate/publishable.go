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
}

// CheckContractPublishable enforces decision D-D: a contract that declares
// a JSON-Schema dialect must actually publish a schema and at least one
// valid fixture, or it is refused (POL-009).
//
// Why a refusal and not a warning. Nothing in the product reads a
// contract's schema/** or fixtures/** today, and `contract new` scaffolds
// neither — so before this rule, every contract in existence published
// zero fixtures, and §5.4b's compatibility check would have answered
// "the prior version published no fixtures, nothing computed" for all of
// them, forever. A guarantee a producer has to opt into is not a
// guarantee, and in a fleet where the producer is an agent, nobody opts
// in. Requiring the baseline is what makes the check able to bite.
//
// A non-JSON-Schema contract is not subject to this: §5.4b hands deep
// compatibility for those formats to the owner's own CI, so demanding
// fixtures this engine would never read would be ceremony.
//
// Returns nil when the contract may be published.
func CheckContractPublishable(in PublishableInput) *Violation {
	if !IsJSONSchemaFormat(in.SchemaFormat) {
		return nil
	}
	if in.Schemas > 0 && in.ValidFixtures > 0 {
		return nil
	}

	var missing string
	switch {
	case in.Schemas == 0 && in.ValidFixtures == 0:
		missing = "no schema/** files and no fixtures/valid/** files"
	case in.Schemas == 0:
		missing = "no schema/** files"
	default:
		missing = "no fixtures/valid/** files"
	}

	return &Violation{
		Code:  "POL-009",
		Class: ClassPolicy,
		Message: "contract " + in.ContractID + " declares schema_format " + in.SchemaFormat +
			" but publishes " + missing +
			"; §5.4b's computed compatibility check has nothing to compute against, so a later breaking change could not be detected",
		Severity: SeverityReject,
	}
}
