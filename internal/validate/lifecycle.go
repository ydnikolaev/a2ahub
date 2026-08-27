package validate

import "fmt"

// checkLifecycle is the V2 lifecycle class (§7 "V2 usage" row): the
// legality checker runs once per event accompanying the submit batch,
// using the manifest as staged locally (submit is pre-merge; V3
// re-derives against merged history post-merge, §5.5). A legal verdict
// produces no violation; illegal-transition maps to LFC-001; unauthorized-
// actor maps to LFC-002, UNLESS the event is a decision-supersede
// candidate — see isDecisionSupersedeCandidate's own doc comment for the
// LFC-005/LFC-006 pair it maps to instead.
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
			if isDecisionSupersedeCandidate(ev) {
				out = append(out, decisionSupersedePreconditionViolation(path))
				if ev.SuccessorEnvelope == nil {
					out = append(out, decisionSupersedeUnresolvedViolation(path))
				}
				continue
			}
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

// isDecisionSupersedeCandidate reports whether ev is a candidate this
// package's own §3.4.4 successor-precondition rule can apply to — a
// `supersede` transition over a `decision`-kind subject (internal/fold's
// two declared-precondition rows, table.go: from `rejected` and from
// `approved`). This is the ONE discrimination checkLifecycle needed to
// make the LFC-005/LFC-006 pair expressible (no-silent-yes-2026-08/P6,
// spec 06 §11's 2026-08-27 block) — read from the CandidateEvent it
// already holds (ev.Envelope.Kind, US-3's own addition), never from a
// widened Verdict (D9: "this enum stays exactly 3-valued").
//
// This signal is coarser than internal/fold's own row selection: fold's
// THIRD decision-supersede row (`proposed -> supersede`, RoleOwner, no
// declared Precondition) ALSO satisfies this predicate, so a plain
// wrong-owner refusal on a PROPOSED decision's supersede is relabelled
// LFC-005 here too, rather than staying LFC-002. checkLifecycle cannot
// tell the two apart from a 3-valued Verdict alone — see this phase's own
// Deviations report for why a stricter signal was not available within
// this package's own information boundary.
func isDecisionSupersedeCandidate(ev CandidateEvent) bool {
	return ev.Transition == "supersede" && ev.Envelope.Kind == "decision"
}

// DecisionSupersedePreconditionMessage and DecisionSupersedeUnresolvedMessage
// are the ONE wording of LFC-005 and LFC-006, exported because BOTH write
// surfaces need it and neither may import the other.
//
// They were duplicated three ways for the length of one wave — here, in
// internal/cli/cmd_lifecycle.go's local verb gate, and in
// internal/mcp/eventdoc.go's — and the only thing holding the three in
// agreement was cmd/a2a/mcp_equivalence_test.go comparing two of them for
// byte equality. That test is real and it stays; what it could NOT do is
// notice this file drifting from both, since it never reads this one.
//
// ADR-019 (2026-08-27), authored by the very epic that then shipped the third
// copy: a rule both surfaces need moves DOWN by default. internal/validate is
// below both by ADR-001's matrix ("core packages above", and mcp "never cli"),
// and both surfaces already import it — so the move-down costs an export and
// removes two copies. The refusal's own words are part of the rule: an agent
// that trips LFC-005 learns what would make the supersede legal FROM THE
// MESSAGE (spec 06's discoverability instrument), so two surfaces teaching it
// differently is the same defect as two surfaces deciding it differently.
const (
	// DecisionSupersedePreconditionMessage is LFC-005's own wording: it names
	// what WOULD make the supersede legal, per §3.4.4, so the refusal teaches
	// the rule rather than only reporting a verdict.
	DecisionSupersedePreconditionMessage = "supersede's successor does not satisfy the transition's declared precondition " +
		"(§3.4.4: a rejected decision's supersede must be authored by the successor's own author; " +
		"an approved decision's supersede must name an approved successor)"
	// DecisionSupersedeUnresolvedMessage is LFC-006's own wording — D9's
	// UNMEASURED half, which says the precondition was UNEVALUATED rather
	// than failed, and that absence refuses instead of granting.
	DecisionSupersedeUnresolvedMessage = "supersede's successor could not be resolved at all, " +
		"so the §3.4.4 precondition could not be evaluated — refusing rather than silently granting"
)

// decisionSupersedePreconditionViolation is LFC-005 — registered for BOTH
// the resolved-and-failing case (successor known, precondition not met)
// and the unresolved case (successor could not be resolved at all), per
// POL-024's own 2026-08-27 precedent D9 cites: UNMEASURED rides "alongside
// an ORDINARY reject", never founding a second reject code.
func decisionSupersedePreconditionViolation(path string) Violation {
	return Violation{
		Code:     "LFC-005",
		Class:    ClassLifecycle,
		Path:     path,
		Message:  DecisionSupersedePreconditionMessage,
		Severity: SeverityReject,
	}
}

// decisionSupersedeUnresolvedViolation is LFC-006 — D9's UNMEASURED half,
// emitted ALONGSIDE LFC-005 (never alone) exactly when the successor could
// not be resolved at all, so the precondition is UNEVALUATED rather than
// evaluated-and-failed. SeverityUnmeasured (P3, result.go) can never itself
// block a write (D9: "UNMEASURED can never itself block a write") —
// isReject's own allow-list already enforces that, this call site relies
// on it rather than re-deciding it.
func decisionSupersedeUnresolvedViolation(path string) Violation {
	return Violation{
		Code:     "LFC-006",
		Class:    ClassLifecycle,
		Path:     path,
		Message:  DecisionSupersedeUnresolvedMessage,
		Severity: SeverityUnmeasured,
	}
}
