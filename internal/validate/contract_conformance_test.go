package validate

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

func TestValidateContractConformanceEmitsPOL014OnlyForSuiteContradiction(t *testing.T) {
	t.Parallel()
	result := contract.ConformanceResult{
		Mode: contract.ConformanceModeSuite, Outcome: contract.ConformanceSuiteInconsistent,
		Results: []contract.ConformanceCaseResult{
			{Path: "fixtures/invalid/b.json", Expected: contract.ConformanceExpectedNonconformant, Actual: contract.ConformanceActualConformant},
			{Path: "fixtures/valid/a.json", Expected: contract.ConformanceExpectedConformant, Actual: contract.ConformanceActualUnevaluated},
		},
	}
	violations := ValidateContractConformance(result)
	if len(violations) != 1 || violations[0].Code != "POL-014" || violations[0].Path != "fixtures/invalid/b.json" || len(violations[0].Subjects) != 2 {
		t.Fatalf("violations = %+v", violations)
	}
	result.Mode, result.Outcome = contract.ConformanceModePayload, contract.ConformanceNonconformant
	if got := ValidateContractConformance(result); got == nil || len(got) != 0 {
		t.Fatalf("payload nonconformance emitted policy finding: %+v", got)
	}
}
