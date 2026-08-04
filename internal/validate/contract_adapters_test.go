package validate

import (
	"reflect"
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

// TestContractInstanceAdapterLocatesADocumentLevelViolationAtTheRoot pins the
// conformance report's location semantics for the exact contract LE-OC-08
// exercises live: `{}` against a root `required` keyword. The failure is the
// whole document, so both pointers are RFC 6901's empty root pointer — spec 06
// §5.4 exposes the pointers the approved library reports and never synthesizes
// one. The live scenario read that empty schema pointer as "no location" and
// failed a correct result; this fixes the semantics in place so a later change
// that starts inventing a pointer is caught here.
func TestContractInstanceAdapterLocatesADocumentLevelViolationAtTheRoot(t *testing.T) {
	t.Parallel()
	descriptor := contract.Descriptor{SchemaFormat: contract.ConformanceJSONSchema202012, Artifacts: []contract.Entry{
		{Path: "schema/order.schema.json", Role: contract.RoleSchema, Normative: true, MediaType: "application/schema+json"},
		{Path: "fixtures/valid/order.json", Role: contract.RoleValidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
		{Path: "fixtures/invalid/order.json", Role: contract.RoleInvalidFixture, Normative: true, MediaType: "application/json", ConformsTo: "schema/order.schema.json"},
	}}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte("descriptor"), descriptor, []contract.CandidateFile{
		{Path: "schema/order.schema.json", Kind: contract.CandidateRegular, Raw: []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`)},
		{Path: "fixtures/valid/order.json", Kind: contract.CandidateRegular, Raw: []byte(`{"id":"ok"}`)},
		{Path: "fixtures/invalid/order.json", Kind: contract.CandidateRegular, Raw: []byte(`{}`)},
	})
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	input := contract.ConformanceInput{
		ContractID: "XC-atlas-demo", Version: "1.0.0", Commit: "0123456789abcdef0123456789abcdef01234567",
		SchemaFormat: contract.ConformanceJSONSchema202012, PublishedDigest: set.AggregateDigest,
		Set: set, Mode: contract.ConformanceModePayload,
		SchemaPath: "schema/order.schema.json", PayloadPath: "payloads/invalid.json", Payload: []byte(`{}`),
	}
	first := contract.CheckConformance(input, ContractInstanceAdapter{})
	second := contract.CheckConformance(input, ContractInstanceAdapter{})
	if first.Passed || first.Outcome != contract.ConformanceNonconformant || len(first.Results) != 1 {
		t.Fatalf("known-invalid payload result = %+v", first)
	}
	if !reflect.DeepEqual(first.Results, second.Results) {
		t.Fatalf("results are unstable across identical checks:\nfirst=%+v\nsecond=%+v", first.Results, second.Results)
	}
	if len(first.Results[0].Violations) != 1 {
		t.Fatalf("violations = %+v, want exactly the document-level required failure", first.Results[0].Violations)
	}
	violation := first.Results[0].Violations[0]
	if violation.InstancePointer != "" || violation.SchemaPointer != "" || violation.Message == "" {
		t.Fatalf("document-level violation = %+v, want both pointers at the RFC 6901 root with a message", violation)
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
