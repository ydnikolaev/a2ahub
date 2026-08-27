package validate

import "testing"

// TestResult_UnmeasuredAloneLeavesValidTrue pins D9 (no-silent-yes-2026-08/P3,
// DECISIONS.md § D9 — "UNMEASURED can never itself block a write") and spec
// 03 §8 row 11: a single SeverityUnmeasured violation must never flip
// Result.Valid to false. Before the isReject fix (result.go:141-143's
// exclude-list, "!= SeverityWarning"), adding SeverityUnmeasured would have
// made this red — see TestIsReject_UnrecognisedSeverityNoLongerBlocks below
// for the mutation that proves it.
func TestResult_UnmeasuredAloneLeavesValidTrue(t *testing.T) {
	t.Parallel()
	r := newResult(V2, "XW-axon-20260827-abcd", []Violation{
		{Code: "REF-900", Class: ClassReferential, Severity: SeverityUnmeasured, Message: "capability could not be resolved"},
	})
	if !r.Valid {
		t.Fatalf("Result.Valid = false, want true: a lone SeverityUnmeasured violation must never block (D9)")
	}
	if len(r.Violations) != 1 {
		t.Fatalf("Violations = %+v, want exactly the one unmeasured violation preserved", r.Violations)
	}
}

// TestResult_RejectBesideUnmeasuredStillInvalid is D9's own paired case
// (DECISIONS.md § D9: "carried ... alongside an ordinary reject wherever the
// unchecked rule would otherwise grant") — the capability-miss shape spec 03
// AC row 13 names: a reject violation beside an unmeasured one must still
// leave Result.Valid false, so UNMEASURED never MASKS a real reject either.
func TestResult_RejectBesideUnmeasuredStillInvalid(t *testing.T) {
	t.Parallel()
	r := newResult(V2, "XW-axon-20260827-abcd", []Violation{
		{Code: "REF-899", Class: ClassReferential, Severity: SeverityReject, Message: "cannot confirm the space stays bilateral"},
		{Code: "REF-900", Class: ClassReferential, Severity: SeverityUnmeasured, Message: "capability could not be resolved"},
	})
	if r.Valid {
		t.Fatalf("Result.Valid = true, want false: a reject violation beside an unmeasured one must still block")
	}
	if len(r.Violations) != 2 {
		t.Fatalf("Violations = %+v, want both violations preserved", r.Violations)
	}
}

// TestIsReject_UnrecognisedSeverityNoLongerBlocks pins the deliberate
// behaviour change the plan (03-a-label-becomes-a-refusal.plan.md, step 1.c)
// and spec 03 §11 both call out by name: isReject flips from an EXCLUDE-list
// ("!= SeverityWarning" — everything blocks except an explicit warning) to
// an ALLOW-list ("== SeverityReject" — nothing blocks except an explicit
// reject). A Violation carrying a severity this package does not recognise
// (a typo, a future value, the zero value under a hypothetical future
// default) used to flip Result.Valid to false under the old exclude-list and
// no longer does.
//
// Watched failing before the fix (mutation evidence, quoted in the report):
// reverting isReject to `return v.Severity != SeverityWarning` reds this
// test with "Valid = false, want true", naming exactly the old default this
// change removes.
func TestIsReject_UnrecognisedSeverityNoLongerBlocks(t *testing.T) {
	t.Parallel()
	r := newResult(V1, "XW-axon-20260827-abcd", []Violation{
		{Code: "ZZZ-000", Class: ClassPolicy, Severity: Severity("bogus-unrecognised-severity"), Message: "an unrecognised severity"},
	})
	if !r.Valid {
		t.Fatalf("Result.Valid = false, want true: an unrecognised severity must not block under the allow-list isReject (only SeverityReject blocks)")
	}
}
