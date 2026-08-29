package livee2e

// coverage.go is P10's own AC-13 obligation (answers-that-hold-2026-08 spec
// 10 §8, "US-6: I want this phase's own coverage stated as a number, so
// nobody mistakes it for completeness"): a declared roster of the epic
// README's own EIGHT corrected C3 instances, which of them THIS PHASE'S OWN
// cells discharge, and the TWO records the same README explains are not
// this class at all — never a human-summarised total.
//
// The eight ids and the "corrected from ten" count are read from
// docs/features/active/answers-that-hold-2026-08/README.md's own C3 row,
// not re-derived here: unlike fold.BuildVocabulary()/TransitionRows(), a
// feedback record has no Go enumerator, and inventing one to avoid an
// eight-element literal would be exactly the "asserted total inside a
// derived gate" scenariocoverage_test.go's own doc comment already refuses
// one file over — this is a closed, spec-named set (the same shape
// TestIncidentReplayCount already pins for a different registry), not a
// vocabulary the domain publishes.

// verbAgreementInstanceIDs is the epic README's own corrected C3 row — the
// EIGHT instances this phase's gate exists for.
func verbAgreementInstanceIDs() []string {
	return []string{
		"fb-20260801-457629",
		"fb-20260806-3539ac",
		"fb-20260806-c6ad38",
		"fb-20260808-5c73a9",
		"fb-20260812-d31acb",
		"fb-20260812-f9cfac",
		"fb-20260820-d1e370",
		"fb-20260827-a84550",
	}
}

// verbAgreementNonInstance names a record the README's own C3 walk found
// and rejected, with the reason recorded there.
type verbAgreementNonInstance struct {
	ID     string
	Reason string
}

// verbAgreementNonInstances is the "corrected from ten" half: two records
// the README explicitly says are NOT this class, each with its own reason
// — never silently dropped from a ten-item list down to eight.
func verbAgreementNonInstances() []verbAgreementNonInstance {
	return []verbAgreementNonInstance{
		{
			ID: "fb-20260812-ee6dcd",
			Reason: "a check that did not EXIST — there was no second verdict to disagree with, and its fix " +
				"adds a capability rather than reconciling two answers",
		},
		{
			ID: "fb-20260820-02f576",
			Reason: "\"Not a malfunction — a second caller of a pattern that was retired hours earlier.\" Both " +
				"workflows ran green; nothing disagreed. Its own home is the notify-workflow gate's reusable-" +
				"workflow roster, not this phase",
		},
	}
}

// coveredVerbAgreementInstances reports, for every id verbAgreementInstanceIDs
// returns, whether THIS PHASE'S OWN registry (verbAgreementReplays,
// scenarios_incidents.go) carries a cell discharging it — by id, never by a
// padded or rounded count (AC-13: "an honest short number beats a padded
// one").
func coveredVerbAgreementInstances() (covered, uncovered []string) {
	discharged := map[string]bool{}
	for _, r := range verbAgreementReplays() {
		for _, id := range r.Instances {
			discharged[id] = true
		}
	}
	for _, id := range verbAgreementInstanceIDs() {
		if discharged[id] {
			covered = append(covered, id)
		} else {
			uncovered = append(uncovered, id)
		}
	}
	return covered, uncovered
}
