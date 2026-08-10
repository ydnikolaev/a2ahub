package validate

import (
	"strings"
	"testing"
)

func TestIsJSONSchemaFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		format string
		want   bool
	}{
		{"the first-class dialect", "json-schema-2020-12", true},
		{"a future draft is still JSON Schema", "json-schema-2029-01", true},
		{"case and surrounding space do not decide a guarantee", "  JSON-Schema-2020-12 ", true},
		{"openapi is the owner's own CI duty", "openapi-3.x", false},
		{"proto3 likewise", "proto3", false},
		{"other likewise", "other", false},
		{"an unset format is not a JSON-Schema claim", "", false},
		{"a lookalike that is not the dialect", "jsonschema-2020-12", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsJSONSchemaFormat(tc.format); got != tc.want {
				t.Fatalf("IsJSONSchemaFormat(%q) = %v, want %v", tc.format, got, tc.want)
			}
		})
	}
}

func TestCheckContractPublishable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		in          PublishableInput
		wantRefusal bool
		wantMention string
	}{
		{
			name: "a JSON-Schema contract with a schema and both fixture classes publishes",
			in:   PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-ingest", Schemas: 1, ValidFixtures: 1, InvalidFixtures: 1},
		},
		{
			name:        "no schema and no fixtures is refused, and says both are missing",
			in:          PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-ingest"},
			wantRefusal: true,
			wantMention: "no schema/** files and no fixtures/valid/** files and no fixtures/invalid/** files",
		},
		{
			name:        "a schema with no fixture leaves nothing to compute against",
			in:          PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-ingest", Schemas: 2, InvalidFixtures: 1},
			wantRefusal: true,
			wantMention: "no fixtures/valid/** files",
		},
		{
			name:        "fixtures with no schema are equally useless as a baseline",
			in:          PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-ingest", ValidFixtures: 3, InvalidFixtures: 1},
			wantRefusal: true,
			wantMention: "no schema/** files",
		},
		{
			name:        "a schema and valid fixture without an invalid fixture violate the plan contract",
			in:          PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-ingest", Schemas: 1, ValidFixtures: 1},
			wantRefusal: true,
			wantMention: "no fixtures/invalid/** files",
		},
		{
			name: "proto3 still carries the format-neutral §5.3 directory baseline",
			in: PublishableInput{
				SchemaFormat: "proto3", ContractID: "XC-axon-ingest",
				Schemas: 1, ValidFixtures: 1, InvalidFixtures: 1,
			},
		},
		{
			name:        "proto3 without the baseline is refused even though deep validation belongs to owner CI",
			in:          PublishableInput{SchemaFormat: "proto3", ContractID: "XC-axon-ingest"},
			wantRefusal: true,
			wantMention: "no schema/** files and no fixtures/valid/** files and no fixtures/invalid/** files",
		},
		// P5 US-1's relaxation (specs/05-declared-nature.md, 2026-08-10
		// amendment). Direction 1 (relaxation removed → still red) is the
		// "no schema and no fixtures is refused" case above, which carries
		// DeclaresNoCompatibilityClaim: false and stays refused — proving
		// the flag, not the empty counts alone, is what relaxes.
		{
			name: "a descriptor declaring no compatibility claim and publishing nothing is publishable",
			in:   PublishableInput{SchemaFormat: "other", ContractID: "XC-axon-review", DeclaresNoCompatibilityClaim: true},
		},
		{
			name: "the sentinel relaxation reaches json-schema-2020-12 too, not only non-JSON formats",
			in:   PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-review", DeclaresNoCompatibilityClaim: true},
		},
		// Direction 2: the relaxation must not become unconditional — a
		// PARTIALLY populated contract still refuses even when it declares
		// no compatibility claim, because declaring no claim does not
		// excuse an inconsistent one.
		{
			name:        "declaring no compatibility claim does not excuse a partially published contract",
			in:          PublishableInput{SchemaFormat: "json-schema-2020-12", ContractID: "XC-axon-review", DeclaresNoCompatibilityClaim: true, Schemas: 1},
			wantRefusal: true,
			wantMention: "no fixtures/valid/** files",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := CheckContractPublishable(tc.in)
			if !tc.wantRefusal {
				if v != nil {
					t.Fatalf("expected no refusal, got %+v", v)
				}
				return
			}
			if v == nil {
				t.Fatal("expected a POL-009 refusal, got nil")
			}
			if v.Code != "POL-009" {
				t.Fatalf("code = %q, want POL-009", v.Code)
			}
			if v.Class != ClassPolicy {
				t.Fatalf("class = %q, want %q", v.Class, ClassPolicy)
			}
			if !strings.Contains(v.Message, tc.wantMention) {
				t.Fatalf("message %q does not name what is missing (%q)", v.Message, tc.wantMention)
			}
			if !strings.Contains(v.Message, tc.in.ContractID) {
				t.Fatalf("message %q does not name the contract", v.Message)
			}
		})
	}
}

// TestCheckContractDescriptorShape covers the fb-20260806-3539ac defect from
// both sides of the floor. The rule is not "v2 is better": it is that the
// space's floor SELECTS one publication profile, and the descriptor that does
// not match the selected one can never publish in that space at all.
func TestCheckContractDescriptorShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		in       DescriptorShapeInput
		wantRed  bool
		wantSays []string
	}{
		{
			name: "above floor, envelope/v1 with no inventory — the reported defect",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.19.3", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: "json-schema-2020-12",
			},
			wantRed:  true,
			wantSays: []string{"envelope/v2", "artifacts:", "a2a template show contract"},
		},
		{
			name: "above floor, envelope/v2 but no inventory",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.19.3", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v2", DeclaresArtifacts: false, SchemaFormat: "json-schema-2020-12",
			},
			wantRed:  true,
			wantSays: []string{"declares no top-level `artifacts:` inventory"},
		},
		{
			name: "above floor, declared v2 — the shape the tool renders",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.19.3", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v2", DeclaresArtifacts: true, SchemaFormat: "json-schema-2020-12",
			},
		},
		{
			name: "exactly at the floor is AT or above, not below",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.19.0", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: "json-schema-2020-12",
			},
			wantRed:  true,
			wantSays: []string{"0.19.0"},
		},
		{
			name: "below floor, legacy v1 tree stays publishable",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.18.0", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: "json-schema-2020-12",
			},
		},
		{
			name: "below floor, a declared inventory the legacy profile refuses",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.18.0", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v2", DeclaresArtifacts: true, SchemaFormat: "json-schema-2020-12",
			},
			wantRed:  true,
			wantSays: []string{"contract-tree-v1", "min_binary_version"},
		},
		{
			name: "an unknown floor is not checked at all — never an invented one",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: "json-schema-2020-12",
			},
		},
		{
			name: "an uncomparable floor degrades to no verdict, not a refusal",
			in: DescriptorShapeInput{
				SpaceMinBinaryVersion: "not-a-version", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: "json-schema-2020-12",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CheckContractDescriptorShape(tc.in)
			if !tc.wantRed {
				if got != nil {
					t.Fatalf("expected no violation, got %+v", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected a violation, got nil")
			}
			if got.Code != "POL-013" || got.Severity != SeverityReject || got.Path != "artifacts" {
				t.Fatalf("violation shape = %+v, want POL-013/reject/artifacts", *got)
			}
			// Every refusal must carry its own remedy — the absence of one is
			// what the external report was actually about.
			if !strings.Contains(got.Message, tc.in.ContractID) {
				t.Fatalf("refusal does not name the contract: %s", got.Message)
			}
			for _, want := range tc.wantSays {
				if !strings.Contains(got.Message, want) {
					t.Fatalf("refusal does not say %q: %s", want, got.Message)
				}
			}
		})
	}
}

// TestCheckContractDescriptorShapeNonJSONSchemaNamesTheLimit: the whole point
// of this check is that a refusal must name a reachable remedy. A contract
// that carries no JSON Schema cannot supply the three roles contract-set-v2
// requires, so telling its author to "declare every carried file" would
// reproduce the reported defect one layer in. The refusal has to say the
// format has no publishable shape at this floor.
func TestCheckContractDescriptorShapeNonJSONSchemaNamesTheLimit(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"openapi-3.1", "proto3", "other"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			got := CheckContractDescriptorShape(DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.19.3", ContractID: "XC-axon-ingest",
				DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: format,
			})
			if got == nil {
				t.Fatal("expected a refusal: this format cannot publish at this floor")
			}
			if got.Path != "schema_format" {
				t.Fatalf("path = %q, want schema_format — the format is the blocker, not the inventory", got.Path)
			}
			if strings.Contains(got.Message, "a2a template show contract") {
				t.Fatalf("the refusal points at a remedy this format cannot reach: %s", got.Message)
			}
			for _, want := range []string{format, "no publishable shape"} {
				if !strings.Contains(got.Message, want) {
					t.Fatalf("refusal does not say %q: %s", want, got.Message)
				}
			}
		})
	}

	// Below the floor these formats still publish the legacy tree, unchanged.
	if got := CheckContractDescriptorShape(DescriptorShapeInput{
		SpaceMinBinaryVersion: "0.18.0", ContractID: "XC-axon-ingest",
		DescriptorSchema: "envelope/v1", DeclaresArtifacts: false, SchemaFormat: "openapi-3.1",
	}); got != nil {
		t.Fatalf("below the floor an openapi contract must stay publishable, got %+v", *got)
	}
}

// TestCheckContractDescriptorShapeAlreadySilentForANonBindingDescriptor is
// the negative finding specs/05-declared-nature.md's 2026-08-10 amendment
// asked to be verified empirically, not assumed: does
// CheckContractDescriptorShape (POL-013) ALSO refuse the shape a
// non-binding, no-artifacts-published contract takes, the way
// CheckContractPublishable (POL-009) does before its own relaxation?
//
// It does not, and DescriptorShapeInput therefore gains NO new field. The
// whole question is decided by DeclaresArtifacts alone
// (`probe.Artifacts != nil` at the caller) — role-agnostic and
// schema_format-agnostic — the moment DescriptorSchema is "envelope/v2":
// `if in.DescriptorSchema == "envelope/v2" && in.DeclaresArtifacts { return
// nil }` returns before schema_format is even read. And DeclaresArtifacts
// can never be false for a schema-valid v2 descriptor in the first place:
// envelope/v2/contract.schema.json requires `artifacts` (minItems: 1)
// UNCONDITIONALLY, x_binding or not — a non-binding contract still needs
// at least one non-schema/non-fixture entry (role "other", say) to be a
// legal document at all, and that alone satisfies DeclaresArtifacts. The
// 2026-08-08 finding that measured POL-013 firing alongside POL-009 used a
// descriptor that was envelope/v1 (no `artifacts` key exists on that
// schema at all), which is a different, unrelated refusal — the space's
// profile refusing the WRONG GENERATION, not this field's absence — and
// does not transfer to the v2-plus-x_binding shape this phase targets.
func TestCheckContractDescriptorShapeAlreadySilentForANonBindingDescriptor(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"json-schema-2020-12", "other", "openapi-3.1", "proto3"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			// DeclaresArtifacts: true models the real shape — the descriptor
			// carries a top-level `artifacts:` key with at least one entry
			// to satisfy the schema's unconditional `minItems: 1`, even
			// though none of those entries is a schema/valid-fixture/
			// invalid-fixture role (that fact lives in PublishableInput's
			// Schemas/ValidFixtures/InvalidFixtures counts, not here).
			if got := CheckContractDescriptorShape(DescriptorShapeInput{
				SpaceMinBinaryVersion: "0.19.3", ContractID: "XC-axon-review",
				DescriptorSchema: "envelope/v2", DeclaresArtifacts: true, SchemaFormat: format,
			}); got != nil {
				t.Fatalf("a v2 descriptor declaring an artifacts inventory must not be refused for its SHAPE regardless of schema_format, got %+v", *got)
			}
		})
	}
}
