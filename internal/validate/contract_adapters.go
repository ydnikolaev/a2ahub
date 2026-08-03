package validate

import (
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// ContractCompatibilityAdapter is the production implementation of the
// contract core's consumer-owned compatibility seam. It projects only the
// exact normative schema/valid-fixture carried bytes into the canonical
// computed-compatibility validator.
type ContractCompatibilityAdapter struct{}

// CheckCompatibility compares the candidate schemas with the prior fixtures.
func (ContractCompatibilityAdapter) CheckCompatibility(input contract.CompatibilityCheckInput) contract.CompatibilityResult {
	newSchemas := make(map[string][]byte)
	for _, entry := range input.NewEntries {
		if entry.Role == contract.RoleSchema && entry.Normative {
			newSchemas[entry.Path] = append([]byte(nil), input.NewBytes[entry.Path]...)
		}
	}
	priorFixtures := make(map[string][]byte)
	for _, entry := range input.BaselineEntries {
		if entry.Role == contract.RoleValidFixture && entry.Normative {
			priorFixtures[entry.Path] = append([]byte(nil), input.BaselineBytes[entry.Path]...)
		}
	}
	computed := CheckComputedCompatibility(CompatInput{
		DeclaredBump: input.DeclaredBump, PriorVersion: input.PriorVersion, NewVersion: input.NewVersion,
		NewSchemas: newSchemas, PriorFixtures: priorFixtures,
	})
	result := contract.CompatibilityResult{Failures: make([]contract.CompatibilityFailure, 0, len(computed.Failures))}
	for _, failure := range computed.Failures {
		result.Failures = append(result.Failures, contract.CompatibilityFailure{Path: failure.Fixture, Schema: failure.Schema, Reason: failure.Detail})
	}
	switch {
	case computed.Violation != nil && computed.Violation.Code == "POL-008":
		result.Verdict = contract.CompatibilityRefused
	case computed.Violation != nil:
		result.Verdict = contract.CompatibilityBreaking
	case !computed.Computed:
		result.Verdict, result.Reason = contract.CompatibilityNotEvaluated, computed.Reason
	default:
		result.Verdict = contract.CompatibilityCompatible
	}
	if computed.Violation != nil {
		result.PolicyViolation = &contract.Finding{
			Code: computed.Violation.Code, Path: computed.Violation.Path, Message: computed.Violation.Message,
		}
	}
	return result
}

// ContractInstanceAdapter is the production implementation of contract
// conformance's instance-check seam. Arbitrary schemas are compiled only by
// internal/schema; jsonschema implementation types never cross this adapter.
type ContractInstanceAdapter struct{}

// CheckInstance validates the supplied instance through internal/schema.
func (ContractInstanceAdapter) CheckInstance(input contract.InstanceCheckInput) contract.InstanceCheckResult {
	compiled, err := schema.CompileExternal(input.SchemaPath, input.Schema)
	if err != nil {
		return instanceAdapterError(input, err)
	}
	violations, err := compiled.ValidateInstanceViolations(input.Instance)
	if err != nil {
		return instanceAdapterError(input, err)
	}
	result := contract.InstanceCheckResult{Passed: len(violations) == 0, Violations: make([]contract.InstanceViolation, 0, len(violations))}
	for _, violation := range violations {
		result.Violations = append(result.Violations, contract.InstanceViolation{
			InstancePointer: fieldPathPointer(violation.Path), SchemaPointer: violation.SchemaPointer,
			Message: fmt.Sprintf("JSON Schema keyword %s rejected the instance", violation.Keyword),
		})
	}
	return result
}

func instanceAdapterError(_ contract.InstanceCheckInput, _ error) contract.InstanceCheckResult {
	// The contract core reserves Passed=false + zero violations for an
	// unevaluated result. Returning a synthetic validation violation here would
	// let an uncompilable schema satisfy an invalid-fixture expectation.
	return contract.InstanceCheckResult{Passed: false, Violations: []contract.InstanceViolation{}}
}

func fieldPathPointer(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	if strings.HasPrefix(field, "/") {
		return field
	}
	parts := strings.Split(field, ".")
	for index := range parts {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(parts[index], "~", "~0"), "/", "~1")
	}
	return "/" + strings.Join(parts, "/")
}

var _ contract.CompatibilityChecker = ContractCompatibilityAdapter{}
var _ contract.InstanceChecker = ContractInstanceAdapter{}
