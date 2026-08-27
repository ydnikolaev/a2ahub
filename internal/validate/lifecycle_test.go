package validate

import "testing"

// TestCheckLifecycleDecisionSupersedePreconditions is no-silent-yes-2026-08/
// P6's AC 9(b): internal/validate's own half of D9's general rule, paired
// with internal/fold's AC 9(a) (legality_candidate_test.go's
// TestCheckCandidateWithSuccessorDecisionSupersedePreconditions). Driven
// directly against checkLifecycle with a fakeLegality double that always
// reports VerdictUnauthorizedActor — the discrimination under test is
// checkLifecycle's OWN mapping from that verdict to LFC-005/LFC-006, never
// the fold-side verdict itself (that is AC 9(a)'s job).
func TestCheckLifecycleDecisionSupersedePreconditions(t *testing.T) {
	t.Parallel()

	t.Run("resolved_and_failing_yields_LFC-005_alone", func(t *testing.T) {
		t.Parallel()
		events := []CandidateEvent{{
			Subject:    "XD-axon-20260827-e001",
			Transition: "supersede",
			Envelope:   Envelope{Kind: "decision"},
			// Resolved (non-nil) but still unauthorized — the successor
			// exists and was checked, and failed the precondition.
			SuccessorEnvelope: &SuccessorEnvelope{Author: "someone-else", State: "proposed"},
		}}
		got, err := checkLifecycle(events, &fakeLegality{verdict: VerdictUnauthorizedActor})
		if err != nil {
			t.Fatalf("checkLifecycle: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d violations, want exactly 1 (LFC-005 alone): %+v", len(got), got)
		}
		if got[0].Code != "LFC-005" {
			t.Fatalf("got code %q, want LFC-005", got[0].Code)
		}
		if got[0].Severity != SeverityReject {
			t.Fatalf("got severity %q, want reject", got[0].Severity)
		}
	})

	t.Run("unresolved_yields_LFC-005_plus_LFC-006", func(t *testing.T) {
		t.Parallel()
		events := []CandidateEvent{{
			Subject:    "XD-axon-20260827-e002",
			Transition: "supersede",
			Envelope:   Envelope{Kind: "decision"},
			// nil: the successor could not be resolved at all.
			SuccessorEnvelope: nil,
		}}
		got, err := checkLifecycle(events, &fakeLegality{verdict: VerdictUnauthorizedActor})
		if err != nil {
			t.Fatalf("checkLifecycle: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d violations, want exactly 2 (LFC-005 + LFC-006): %+v", len(got), got)
		}
		codes := map[string]Violation{got[0].Code: got[0], got[1].Code: got[1]}
		lfc5, ok := codes["LFC-005"]
		if !ok {
			t.Fatalf("LFC-005 not present: %+v", got)
		}
		if lfc5.Severity != SeverityReject {
			t.Fatalf("LFC-005 severity = %q, want reject", lfc5.Severity)
		}
		lfc6, ok := codes["LFC-006"]
		if !ok {
			t.Fatalf("LFC-006 not present: %+v", got)
		}
		if lfc6.Severity != SeverityUnmeasured {
			t.Fatalf("LFC-006 severity = %q, want unmeasured", lfc6.Severity)
		}
	})

	t.Run("legal_verdict_yields_no_violation_even_when_unresolved", func(t *testing.T) {
		t.Parallel()
		events := []CandidateEvent{{
			Subject:    "XD-axon-20260827-e003",
			Transition: "supersede",
			Envelope:   Envelope{Kind: "decision"},
		}}
		got, err := checkLifecycle(events, &fakeLegality{verdict: VerdictLegal})
		if err != nil {
			t.Fatalf("checkLifecycle: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %d violations, want 0: %+v", len(got), got)
		}
	})

	t.Run("non_decision_kind_supersede_still_maps_to_LFC-002", func(t *testing.T) {
		t.Parallel()
		// A requirement/question/handoff/announcement supersede: no
		// SuccessorPrecondition exists for any of those rows
		// (internal/fold/table.go), so an unauthorized-actor verdict must
		// stay LFC-002, never relabelled LFC-005.
		events := []CandidateEvent{{
			Subject:    "XW-axon-20260827-e004",
			Transition: "supersede",
			Envelope:   Envelope{Kind: "work_request"},
		}}
		got, err := checkLifecycle(events, &fakeLegality{verdict: VerdictUnauthorizedActor})
		if err != nil {
			t.Fatalf("checkLifecycle: %v", err)
		}
		if len(got) != 1 || got[0].Code != "LFC-002" {
			t.Fatalf("got %+v, want exactly one LFC-002 violation", got)
		}
	})

	t.Run("non_supersede_decision_transition_still_maps_to_LFC-002", func(t *testing.T) {
		t.Parallel()
		// A decision `reject`/`approve`/`withdraw` mismatch is an ordinary
		// LFC-002 — only `supersede` carries a successor precondition.
		events := []CandidateEvent{{
			Subject:    "XD-axon-20260827-e005",
			Transition: "reject",
			Envelope:   Envelope{Kind: "decision"},
		}}
		got, err := checkLifecycle(events, &fakeLegality{verdict: VerdictUnauthorizedActor})
		if err != nil {
			t.Fatalf("checkLifecycle: %v", err)
		}
		if len(got) != 1 || got[0].Code != "LFC-002" {
			t.Fatalf("got %+v, want exactly one LFC-002 violation", got)
		}
	})

	t.Run("illegal_transition_verdict_is_never_relabelled", func(t *testing.T) {
		t.Parallel()
		// VerdictIllegalTransition always maps to LFC-001, regardless of
		// transition/kind — isDecisionSupersedeCandidate is only consulted
		// on the VerdictUnauthorizedActor branch.
		events := []CandidateEvent{{
			Subject:    "XD-axon-20260827-e006",
			Transition: "supersede",
			Envelope:   Envelope{Kind: "decision"},
		}}
		got, err := checkLifecycle(events, &fakeLegality{verdict: VerdictIllegalTransition})
		if err != nil {
			t.Fatalf("checkLifecycle: %v", err)
		}
		if len(got) != 1 || got[0].Code != "LFC-001" {
			t.Fatalf("got %+v, want exactly one LFC-001 violation", got)
		}
	})
}
