package validate

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

func TestValidateContractCarriedSetAuthoritativeFloorMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     ContractCarriedSetInput
		wantValid bool
		wantCode  string
	}{
		{
			name:      "pre-floor current candidate retains legacy profile",
			input:     legacyContractInput(V2, ContractCandidateProposed, "0.18.9"),
			wantValid: true,
		},
		{
			name:      "active floor accepts exact v2 candidate",
			input:     validContractInput(V2),
			wantValid: true,
		},
		{
			name:     "active floor refuses legacy downgrade",
			input:    legacyContractInput(V2, ContractCandidateProposed, version.OperationalConfidenceFloor),
			wantCode: "POL-013",
		},
		{
			name:      "active floor does not reinterpret committed v1 history",
			input:     legacyContractInput(V3, ContractCandidateHistorical, version.OperationalConfidenceFloor),
			wantValid: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			set, result, err := ValidateContractCarriedSet(tc.input)
			if err != nil {
				t.Fatalf("ValidateContractCarriedSet: %v", err)
			}
			if result.Valid != tc.wantValid {
				t.Fatalf("valid = %v, want %v; violations=%+v", result.Valid, tc.wantValid, result.Violations)
			}
			if tc.wantCode != "" && !containsViolation(result.Violations, tc.wantCode, "") {
				t.Fatalf("violations = %+v, want %s", result.Violations, tc.wantCode)
			}
			if tc.wantValid && set.AggregateDigest == "" {
				t.Fatal("valid candidate returned no carried set")
			}
			if !tc.wantValid && set.AggregateDigest != "" {
				t.Fatal("invalid candidate exposed a carried set")
			}
		})
	}
}

func TestValidateContractCarriedSetV2V3Parity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ContractCarriedSetInput)
		code   string
		path   string
	}{
		{
			name: "valid exact set",
		},
		{
			name: "deleted declared sidecar is visible to V3",
			mutate: func(in *ContractCarriedSetInput) {
				in.Snapshot.Files = in.Snapshot.Files[:len(in.Snapshot.Files)-1]
			},
			code: "REF-014",
			path: "fixtures/invalid/example.json",
		},
		{
			name: "every resolved sidecar is secret scanned",
			mutate: func(in *ContractCarriedSetInput) {
				const path = "artifacts/operator-note.txt"
				in.Descriptor.Artifacts = append(in.Descriptor.Artifacts, contract.Entry{
					Path: path, Role: contract.RoleOther, Normative: false, MediaType: "text/plain",
				})
				in.Snapshot.Files = append(in.Snapshot.Files, contract.CandidateFile{
					Path: path, Kind: contract.CandidateRegular, Raw: []byte("token ghp_" + strings.Repeat("a", 36)),
				})
			},
			code: "POL-001",
			path: "artifacts/operator-note.txt",
		},
		{
			name: "per-file bound remains policy",
			mutate: func(in *ContractCarriedSetInput) {
				in.Snapshot.Files[0].Raw = []byte(strings.Repeat("x", contract.MaxFileBytes+1))
			},
			code: "POL-013",
			path: "schema/contract.schema.json",
		},
		{
			name: "unsafe resolved leaf is referential",
			mutate: func(in *ContractCarriedSetInput) {
				in.Snapshot.Files[0].Kind = contract.CandidateSymlink
			},
			code: "REF-014",
			path: "schema/contract.schema.json",
		},
		{
			name: "undeclared sidecar remains producer policy",
			mutate: func(in *ContractCarriedSetInput) {
				in.Snapshot.Files = append(in.Snapshot.Files, contract.CandidateFile{
					Path: "artifacts/hidden.txt", Kind: contract.CandidateRegular, Raw: []byte("hidden"),
				})
			},
			code: "POL-013",
			path: "artifacts/hidden.txt",
		},
		{
			name: "digest mismatch is referential",
			mutate: func(in *ContractCarriedSetInput) {
				in.ExpectedDigest = "sha256:" + strings.Repeat("0", 64)
			},
			code: "REF-014",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var baseline []Violation
			for _, point := range []InvocationPoint{V2, V3} {
				in := validContractInput(point)
				if tc.mutate != nil {
					tc.mutate(&in)
				}
				set, result, err := ValidateContractCarriedSet(in)
				if err != nil {
					t.Fatalf("%s: ValidateContractCarriedSet: %v", point, err)
				}
				if result.InvocationPoint != point {
					t.Fatalf("invocation point = %q, want %q", result.InvocationPoint, point)
				}
				if tc.code == "" {
					if !result.Valid || set.AggregateDigest == "" {
						t.Fatalf("%s: exact set invalid: %+v", point, result.Violations)
					}
				} else {
					if result.Valid || !containsViolation(result.Violations, tc.code, tc.path) {
						t.Fatalf("%s: violations = %+v, want %s at %q", point, result.Violations, tc.code, tc.path)
					}
					if set.AggregateDigest != "" {
						t.Fatalf("%s: invalid candidate exposed a carried set", point)
					}
				}
				if baseline == nil {
					baseline = result.Violations
					continue
				}
				if !reflect.DeepEqual(result.Violations, baseline) {
					t.Fatalf("V2/V3 violations differ:\nV2=%+v\nV3=%+v", baseline, result.Violations)
				}
			}
		})
	}
}

func TestValidateContractCarriedSetContextFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("invalid authoritative floor is operational error", func(t *testing.T) {
		t.Parallel()
		in := validContractInput(V2)
		in.SpaceMinBinaryVersion = "not-semver"
		_, result, err := ValidateContractCarriedSet(in)
		if !errors.Is(err, version.ErrInvalidVersion) {
			t.Fatalf("error = %v, want version.ErrInvalidVersion", err)
		}
		if result.Valid || result.ArtifactID != "" {
			t.Fatalf("invalid trusted context returned a content verdict: %+v", result)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(*ContractCarriedSetInput)
		path   string
	}{
		{
			name: "V1 is not a carried-set enforcement point",
			mutate: func(in *ContractCarriedSetInput) {
				in.InvocationPoint = V1
			},
			path: "invocation_point",
		},
		{
			name: "candidate mode must be explicit",
			mutate: func(in *ContractCarriedSetInput) {
				in.Mode = ""
			},
			path: "mode",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := validContractInput(V2)
			tc.mutate(&in)
			set, result, err := ValidateContractCarriedSet(in)
			if err != nil {
				t.Fatalf("ValidateContractCarriedSet: %v", err)
			}
			if result.Valid || !containsViolation(result.Violations, "POL-013", tc.path) {
				t.Fatalf("violations = %+v, want POL-013 at %q", result.Violations, tc.path)
			}
			if set.AggregateDigest != "" {
				t.Fatal("invalid context exposed a carried set")
			}
		})
	}
}

func TestValidateContractCarriedSetRequiredBaselineUsesPOL009(t *testing.T) {
	t.Parallel()

	in := validContractInput(V2)
	in.Descriptor.Artifacts = in.Descriptor.Artifacts[:1]
	in.Snapshot.Files = in.Snapshot.Files[:1]
	_, result, err := ValidateContractCarriedSet(in)
	if err != nil {
		t.Fatalf("ValidateContractCarriedSet: %v", err)
	}
	if !containsViolation(result.Violations, "POL-009", "artifacts") {
		t.Fatalf("violations = %+v, want POL-009", result.Violations)
	}
	if containsViolation(result.Violations, "POL-013", "artifacts") {
		t.Fatalf("required-role absence split across POL-009 and POL-013: %+v", result.Violations)
	}
}

func validContractInput(point InvocationPoint) ContractCarriedSetInput {
	descriptor := contract.Descriptor{
		SchemaFormat: "json-schema-2020-12",
		Artifacts: []contract.Entry{
			{Path: "schema/contract.schema.json", Role: contract.RoleSchema, Normative: true, MediaType: "application/schema+json"},
			{Path: "fixtures/valid/example.json", Role: contract.RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/contract.schema.json"},
			{Path: "fixtures/invalid/example.json", Role: contract.RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/contract.schema.json"},
		},
	}
	return ContractCarriedSetInput{
		InvocationPoint:       point,
		Mode:                  ContractCandidateProposed,
		SpaceMinBinaryVersion: version.OperationalConfidenceFloor,
		ContractID:            "XC-atlas-order-envelope",
		ContractSchema:        "envelope/v2",
		EventSchema:           "event/v2",
		DigestProfile:         string(contract.ProfileContractSetV2),
		Descriptor:            descriptor,
		Snapshot: contract.CandidateSnapshot{
			Descriptor: contract.CandidateFile{Path: contract.DescriptorPath, Kind: contract.CandidateRegular, Raw: []byte("---\nschema: envelope/v2\n---\n")},
			Files: []contract.CandidateFile{
				{Path: "schema/contract.schema.json", Kind: contract.CandidateRegular, Raw: []byte(`{"type":"object"}`)},
				{Path: "fixtures/valid/example.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
				{Path: "fixtures/invalid/example.json", Kind: contract.CandidateRegular, Raw: []byte(`null`)},
			},
		},
	}
}

func legacyContractInput(point InvocationPoint, mode ContractCandidateMode, floor string) ContractCarriedSetInput {
	return ContractCarriedSetInput{
		InvocationPoint:       point,
		Mode:                  mode,
		SpaceMinBinaryVersion: floor,
		ContractID:            "XC-atlas-order-envelope",
		ContractSchema:        "envelope/v1",
		EventSchema:           "event/v1",
		Snapshot: contract.CandidateSnapshot{Files: []contract.CandidateFile{
			{Path: "schema/contract.schema.json", Kind: contract.CandidateRegular, Raw: []byte(`{"type":"object"}`)},
			{Path: "fixtures/valid/example.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
			{Path: "fixtures/invalid/example.json", Kind: contract.CandidateRegular, Raw: []byte(`null`)},
		}},
	}
}

func containsViolation(violations []Violation, code, path string) bool {
	for _, violation := range violations {
		if violation.Code == code && (path == "" || violation.Path == path) {
			return true
		}
	}
	return false
}
