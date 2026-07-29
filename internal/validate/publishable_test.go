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
