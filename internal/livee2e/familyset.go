package livee2e

import (
	"fmt"
	"sort"
)

// CanonicalFamilies is the hand-written twin of driveFamilies' own dispatch
// table (runner_live_test.go, `//go:build livee2e`) — the twelve family names,
// in the EXACT order that function's `families` slice declares them.
//
// Written out by hand rather than computed FROM driveFamilies' table on
// purpose: the whole point of this list is to be an INDEPENDENT source the
// tagged dispatch table is checked against (driveFamiliesMatchCanonical,
// runner_live_test.go). If this list were derived from that same table —
// a loop over `families`, a shared helper — the pre-flight assertion would
// compare the table to itself and always agree, and the filed defect (spec
// 46 postmortem, 2026-07-27: a family added to catalogue.go but never added
// to driveFamilies' table, discovered only after a 59-minute live run)
// would walk straight through it again.
//
// The order matters and must not be re-sorted: driveFamilies' own comment
// documents why it is cheapest-blast-radius-first (the happy paths run
// before anything that bends protection or the write floor) — this list is
// this package's readable RECORD of that order, so a future reordering of
// driveFamilies is visible as a diff here too, not just in the tagged file
// `make check` cannot type-check.
func CanonicalFamilies() []string {
	return []string{
		FamilyOperationalConfidence,
		"happy",
		"contract-integrity",
		"submitted-family",
		"draft-family",
		"illegal-transitions",
		"failure-recovery",
		"thread-chain",
		"data-loop",
		"boundary",
		"refusal",
		"space",
	}
}

// FamilySpaceCIHealth is the ONE documented exception to "every row's
// Family is a CanonicalFamilies() member": the
// space-ci-has-no-unexplained-failures row is not dispatched through
// driveFamilies' per-family loop at all. TestLiveMatrix
// (runner_live_test.go) calls runSpaceCIHealth directly, AFTER
// driveFamilies returns, deliberately — that row asks what else went red in
// the space while every other family was running, so it has to observe the
// run's WHOLE window and therefore has to run last, outside the loop that
// runs the other twelve families serially. Giving it a canonical-list entry
// instead would make driveFamiliesMatchCanonical's equality check fail on
// every single healthy run (the table legitimately never dispatches a
// 13th family), which is exactly the kind of gate-asserts-a-lie outcome
// this whole change exists to avoid. Naming it here, outside
// CanonicalFamilies(), keeps the exception a single visible fact instead of
// a silent carve-out inside a comparison function.
const FamilySpaceCIHealth = "space-ci-health"

// missingCoveredFamilies is the forward half of the family-coverage check,
// matching missingCoveredKinds' own shape (completeness.go) exactly: which
// of `families` (the canonical list) has NO Catalogue() row naming it in
// Family. Kept separate from CanonicalFamilies()/Catalogue() so it is
// unit-testable against a SYNTHETIC scenario slice, the same reason
// missingCoveredKinds is.
func missingCoveredFamilies(families []string, scenarios []Scenario) []string {
	covered := map[string]bool{}
	for _, sc := range scenarios {
		covered[sc.Family] = true
	}
	var missing []string
	for _, f := range families {
		if !covered[f] {
			missing = append(missing, f)
		}
	}
	sort.Strings(missing)
	return missing
}

// unknownDeclaredFamilies is the reverse half, matching unknownDeclaredKinds'
// own shape: a Scenario.Family value that is neither a CanonicalFamilies()
// member nor the one documented exception (FamilySpaceCIHealth) — a typo, a
// family renamed in driveFamilies out from under a stale Family entry, or a
// row that never had one filled in at all (the empty string is itself
// "unknown": Family has no legitimate nil-means-inapplicable reading the
// way Kinds does, because every row is produced by SOME code path).
// Reported as "scenario: family" pairs so a failing report names both the
// offending row and the bad value, matching unknownDeclaredKinds'
// `%s: %q` precedent.
func unknownDeclaredFamilies(families []string, scenarios []Scenario) []string {
	known := map[string]bool{FamilySpaceCIHealth: true}
	for _, f := range families {
		known[f] = true
	}
	var offenders []string
	for _, sc := range scenarios {
		if !known[sc.Family] {
			offenders = append(offenders, fmt.Sprintf("%s: %q", sc.Name, sc.Family))
		}
	}
	sort.Strings(offenders)
	return offenders
}
