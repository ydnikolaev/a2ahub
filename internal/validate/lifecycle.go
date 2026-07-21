package validate

import "fmt"

// checkLifecycle is the V2 lifecycle class (§7 "V2 usage" row): the
// legality checker runs once per event accompanying the submit batch,
// using the manifest as staged locally (submit is pre-merge; V3
// re-derives against merged history post-merge, §5.5). A legal verdict
// produces no violation; illegal-transition / unauthorized-actor map to
// LFC-001 / LFC-002.
func checkLifecycle(events []CandidateEvent, checker LegalityChecker) ([]Violation, error) {
	if checker == nil {
		return nil, nil
	}
	var out []Violation
	for i, ev := range events {
		verdict, err := checker.CheckLegality(ev)
		if err != nil {
			return nil, fmt.Errorf("validate: lifecycle check for event[%d]: %w", i, err)
		}
		path := fmt.Sprintf("event[%d]", i)
		switch verdict {
		case VerdictLegal:
			// no violation
		case VerdictIllegalTransition:
			out = append(out, Violation{
				Code:     "LFC-001",
				Class:    ClassLifecycle,
				Path:     path,
				Message:  "event encodes an illegal transition for the subject's current folded state",
				CCRef:    "CC-020",
				Severity: SeverityReject,
			})
		case VerdictUnauthorizedActor:
			out = append(out, Violation{
				Code:     "LFC-002",
				Class:    ClassLifecycle,
				Path:     path,
				Message:  "event's actor is not authorized for this transition",
				CCRef:    "CC-021",
				Severity: SeverityReject,
			})
		}
	}
	return out, nil
}
