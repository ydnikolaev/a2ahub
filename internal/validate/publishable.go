// P37 wave B (spec 37 §T2, decision D-D). Two rules that must hold at
// BOTH enforcement layers — `contract publish` locally and `validate --ci`
// at merge — and therefore live here, beside CheckComputedCompatibility,
// rather than in whichever caller happened to need them first. That is the
// same reasoning §T2 gives for the compat core itself, and P35's scar: a
// client-side guard and a CI-side guard shipped separately diverged, and
// nothing noticed until the e2e tier caught it.
package validate

import (
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

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

// DescriptorShapeInput carries the three facts a floor-aware descriptor-shape
// verdict needs. All are resolved by the caller: the space's authoritative
// write floor, the descriptor's own declared envelope generation, and whether
// it carries a top-level `artifacts:` key at all. Presence, not contents —
// the inventory's CONTENTS are contract.ValidateCarriedSet's job, and handing
// this function the entries would invite a second, divergent copy of it.
type DescriptorShapeInput struct {
	// SpaceMinBinaryVersion is the space manifest's authoritative floor.
	SpaceMinBinaryVersion string
	// ContractID names the contract in the refusal.
	ContractID string
	// DescriptorSchema is the descriptor's own `schema:` value.
	DescriptorSchema string
	// DeclaresArtifacts reports whether the descriptor carries a top-level
	// `artifacts:` key — the one fact that selects the candidate's inventory
	// mode in contract.BuildCandidateIntent.
	DeclaresArtifacts bool
	// SchemaFormat is the descriptor's declared schema_format. It does not
	// change the RULE — the floor alone selects the profile — but it changes
	// what the refusal may honestly tell the author to do, because the
	// contract-set-v2 profile requires the schema/valid-fixture/invalid-fixture
	// roles unconditionally (contract.ValidateCarriedSet) and a contract that
	// carries no JSON Schema cannot supply them.
	SchemaFormat string
}

// CheckContractDescriptorShape refuses a contract descriptor that the space's
// own publication planner can never publish, at the gate that runs BEFORE the
// merge rather than after it.
//
// contract.BuildPublicationPlan selects the publication profile from the
// space's authoring floor alone: at or above contract.ContractPublicationFloor
// it is contract-set-v2, which requires a declared-v2 candidate — a descriptor
// that is `schema: envelope/v2` and carries a top-level `artifacts:`
// inventory. Below the floor it is the legacy fixed v1 tree, which requires
// the descriptor NOT to carry one. Either mismatch is terminal for that
// candidate: no flag, retry or later version rescues it, only re-authoring the
// descriptor does.
//
// Found on 2026-08-06 by an external report. A hand-authored envelope/v1
// contract in a space at floor 0.19.x validated clean, submitted, passed the
// space's own `a2a validate --ci`, auto-merged to main — and only then was
// refused at `a2a contract preflight`. Every gate before the irreversible step
// said yes to a document that could never publish. This check is the one that
// had to say no, and it belongs here, beside the other two rules that must
// hold identically at `contract publish` and at merge.
//
// A caller that cannot read an authoritative floor passes an empty
// SpaceMinBinaryVersion and gets nil: this rule refuses on a KNOWN floor, and
// inventing one would be worse than not checking.
//
// Returns nil when the descriptor's shape matches the profile its space's
// floor selects.
func CheckContractDescriptorShape(in DescriptorShapeInput) *Violation {
	if in.SpaceMinBinaryVersion == "" {
		return nil
	}
	belowFloor, err := version.OlderThan(in.SpaceMinBinaryVersion, contract.ContractPublicationFloor)
	if err != nil {
		return nil
	}

	if belowFloor {
		if !in.DeclaresArtifacts {
			return nil
		}
		return &Violation{
			Code:  "POL-013",
			Class: ClassPolicy,
			Path:  "artifacts",
			Message: fmt.Sprintf(
				"contract %s declares a top-level `artifacts:` inventory, but this space's floor %s is below %s, so publication uses the %s profile over the fixed schema/** + fixtures/** tree and refuses a declared inventory. Remove the key, or raise the space's min_binary_version to %s. `a2a template show contract --envelope-schema envelope/v1` prints the shape this floor expects.",
				in.ContractID, in.SpaceMinBinaryVersion, contract.ContractPublicationFloor,
				contract.ProfileContractTreeV1, contract.ContractPublicationFloor),
			Severity: SeverityReject,
		}
	}

	if in.DescriptorSchema == "envelope/v2" && in.DeclaresArtifacts {
		return nil
	}

	// A non-JSON-Schema contract gets a DIFFERENT refusal, because the one
	// above would name a remedy it cannot reach: contract-set-v2 requires the
	// schema, valid-fixture and invalid-fixture roles unconditionally, and
	// `a2a new contract` still authors these formats at envelope/v1 for
	// exactly that reason. Telling such an author to "declare every carried
	// file under artifacts:" would be the same defect this whole check exists
	// to remove — a precondition with no means of satisfaction — one layer
	// further in. Naming the limitation is the honest answer until
	// fb-20260802-e6d436 lands a carried-set shape these formats can satisfy.
	if !IsJSONSchemaFormat(in.SchemaFormat) {
		return &Violation{
			Code:  "POL-013",
			Class: ClassPolicy,
			Path:  "schema_format",
			Message: fmt.Sprintf(
				"contract %s declares schema_format %q, and this space's floor %s is at or above %s, where publication uses the %s profile — which requires the schema, valid-fixture and invalid-fixture roles that a non-JSON-Schema contract has no way to supply. There is no publishable shape for this format at this floor yet. Publish it from a space still below %s, or carry the interface as a json-schema-2020-12 contract, until the declared carried set admits other formats.",
				in.ContractID, in.SchemaFormat, in.SpaceMinBinaryVersion, contract.ContractPublicationFloor,
				contract.ProfileContractSetV2, contract.ContractPublicationFloor),
			Severity: SeverityReject,
		}
	}

	var missing []string
	if in.DescriptorSchema != "envelope/v2" {
		missing = append(missing, fmt.Sprintf("its schema is %q, not envelope/v2", in.DescriptorSchema))
	}
	if !in.DeclaresArtifacts {
		missing = append(missing, "it declares no top-level `artifacts:` inventory")
	}
	return &Violation{
		Code:  "POL-013",
		Class: ClassPolicy,
		Path:  "artifacts",
		Message: fmt.Sprintf(
			"contract %s can never be published in this space: the floor %s is at or above %s, so publication uses the %s profile, which requires a declared-v2 descriptor — but %s. Run `a2a template show contract` for the exact shape, declare every carried file exactly once under `artifacts:`, and correct the descriptor before it merges.",
			in.ContractID, in.SpaceMinBinaryVersion, contract.ContractPublicationFloor,
			contract.ProfileContractSetV2, strings.Join(missing, " and ")),
		Severity: SeverityReject,
	}
}
