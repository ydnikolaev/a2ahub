package validate

import (
	"fmt"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// ValidateContractConformance projects a self-suite contradiction into the
// canonical validation registry. Payload nonconformance and unsupported or
// unevaluated operations remain operation results, not policy findings.
//
//nolint:revive // Validate is the established package API verb used by V2/V3 callers.
func ValidateContractConformance(result contract.ConformanceResult) []Violation {
	if result.Mode != contract.ConformanceModeSuite || result.Outcome != contract.ConformanceSuiteInconsistent {
		return []Violation{}
	}
	paths := make([]string, 0, len(result.Results))
	for _, item := range result.Results {
		if item.Expected == contract.ConformanceExpectedConformant && item.Actual != contract.ConformanceActualConformant ||
			item.Expected == contract.ConformanceExpectedNonconformant && item.Actual != contract.ConformanceActualNonconformant ||
			item.Actual == contract.ConformanceActualInvalidJSON || item.Actual == contract.ConformanceActualInvalidSchema || item.Actual == contract.ConformanceActualUnevaluated {
			paths = append(paths, item.Path)
		}
	}
	sort.Strings(paths)
	path := "fixtures"
	if len(paths) != 0 {
		path = paths[0]
	}
	return []Violation{{
		Code: "POL-014", Class: ClassPolicy, Path: path, Severity: SeverityReject,
		Message:  fmt.Sprintf("declared conformance suite contradicts expected fixture outcomes (%d inconsistent result(s))", len(paths)),
		Subjects: paths,
	}}
}
