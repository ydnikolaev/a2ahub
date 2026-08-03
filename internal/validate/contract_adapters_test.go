package validate

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

func TestContractCompatibilityAdapterUsesCanonicalCompatibilityCore(t *testing.T) {
	t.Parallel()
	adapter := ContractCompatibilityAdapter{}
	input := contract.CompatibilityCheckInput{
		DeclaredBump: "minor", PriorVersion: "1.0.0", NewVersion: "1.1.0",
		NewEntries:      []contract.Entry{{Path: "schema/order.json", Role: contract.RoleSchema, Normative: true}},
		BaselineEntries: []contract.Entry{{Path: "fixtures/valid/order.json", Role: contract.RoleValidFixture, Normative: true}},
		NewBytes:        map[string][]byte{"schema/order.json": []byte(`{"type":"object","required":["id"]}`)},
		BaselineBytes:   map[string][]byte{"fixtures/valid/order.json": []byte(`{}`)},
	}
	result := adapter.CheckCompatibility(input)
	if result.Verdict != contract.CompatibilityBreaking || result.PolicyViolation == nil || result.PolicyViolation.Code != "POL-007" || len(result.Failures) != 1 {
		t.Fatalf("breaking result = %+v", result)
	}
	input.NewBytes["schema/order.json"] = []byte(`{"type":"object"}`)
	result = adapter.CheckCompatibility(input)
	if result.Verdict != contract.CompatibilityCompatible || result.PolicyViolation != nil || len(result.Failures) != 0 {
		t.Fatalf("compatible result = %+v", result)
	}
	input.NewBytes["schema/order.json"] = []byte(`{"$ref":"missing.json"}`)
	result = adapter.CheckCompatibility(input)
	if result.Verdict != contract.CompatibilityRefused || result.PolicyViolation == nil || result.PolicyViolation.Code != "POL-008" {
		t.Fatalf("refused result = %+v", result)
	}
}

func TestContractInstanceAdapterReturnsStructuredCanonicalViolations(t *testing.T) {
	t.Parallel()
	adapter := ContractInstanceAdapter{}
	input := contract.InstanceCheckInput{
		SchemaPath: "schema/order.json", Schema: []byte(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`),
		InstancePath: "payload", Instance: []byte(`{"id":3}`),
	}
	result := adapter.CheckInstance(input)
	if result.Passed || len(result.Violations) != 1 || result.Violations[0].InstancePointer != "/id" || result.Violations[0].SchemaPointer == "" {
		t.Fatalf("invalid result = %+v", result)
	}
	input.Instance = []byte(`{"id":"ok"}`)
	result = adapter.CheckInstance(input)
	if !result.Passed || result.Violations == nil || len(result.Violations) != 0 {
		t.Fatalf("valid result = %+v", result)
	}
	input.Instance = []byte(`{`)
	result = adapter.CheckInstance(input)
	if result.Passed || result.Violations == nil || len(result.Violations) != 0 {
		t.Fatalf("malformed result = %+v", result)
	}
}

func TestContractInstanceAdapterCannotMakeUnevaluatedInvalidFixturePass(t *testing.T) {
	t.Parallel()
	descriptor := contract.Descriptor{SchemaFormat: contract.ConformanceJSONSchema202012, Artifacts: []contract.Entry{
		{Path: "schema/main.json", Role: contract.RoleSchema, Normative: true, MediaType: "application/schema+json"},
		{Path: "fixtures/valid/ok.json", Role: contract.RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/main.json"},
		{Path: "fixtures/invalid/bad.json", Role: contract.RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/main.json"},
	}}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte("descriptor"), descriptor, []contract.CandidateFile{
		{Path: "schema/main.json", Kind: contract.CandidateRegular, Raw: []byte(`{"type":17}`)},
		{Path: "fixtures/valid/ok.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
		{Path: "fixtures/invalid/bad.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
	})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	result := contract.CheckConformance(contract.ConformanceInput{
		ContractID: "XC-atlas-demo", Version: "1.0.0", Commit: "0123456789abcdef0123456789abcdef01234567",
		SchemaFormat: contract.ConformanceJSONSchema202012, PublishedDigest: set.AggregateDigest,
		Set: set, Mode: contract.ConformanceModeSuite,
	}, ContractInstanceAdapter{})
	if result.Passed || result.Outcome != contract.ConformanceSuiteInconsistent {
		t.Fatalf("unevaluated invalid fixture suite = %+v", result)
	}
	foundUnevaluated := false
	for _, item := range result.Results {
		foundUnevaluated = foundUnevaluated || item.Actual == contract.ConformanceActualUnevaluated
	}
	if !foundUnevaluated {
		t.Fatalf("suite has no unevaluated result: %+v", result.Results)
	}
}
