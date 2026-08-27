package livee2e

import "testing"

// TestPathDrivabilityCoversEveryPath is the union gate pathdrivability.go's
// own doc comment promises: every ConformancePaths() id is either in
// drivenPathIDs() or undrivablePaths() (with a reason), never both, never
// neither, and neither list names an id ConformancePaths() does not
// actually have. Runs untagged, so it is part of the plain
// `go test ./internal/livee2e/...` suite `make check` already executes —
// the same "runs on every commit" property pathcoverage_test.go's own
// TestPathCatalogueCoversEveryTransition has.
func TestPathDrivabilityCoversEveryPath(t *testing.T) {
	real := map[string]bool{}
	for _, p := range ConformancePaths() {
		real[p.ID] = true
	}

	driven := map[string]bool{}
	for _, id := range drivenPathIDs() {
		if driven[id] {
			t.Errorf("drivenPathIDs() lists %q twice", id)
		}
		driven[id] = true
		if !real[id] {
			t.Errorf("drivenPathIDs() names %q, which ConformancePaths() does not have", id)
		}
	}

	undriven := map[string]bool{}
	for _, u := range undrivablePaths() {
		if undriven[u.ID] {
			t.Errorf("undrivablePaths() lists %q twice", u.ID)
		}
		undriven[u.ID] = true
		if u.Reason == "" {
			t.Errorf("undrivablePaths() entry %q carries an empty reason", u.ID)
		}
		if !real[u.ID] {
			t.Errorf("undrivablePaths() names %q, which ConformancePaths() does not have", u.ID)
		}
		if driven[u.ID] {
			t.Errorf("%q is in BOTH drivenPathIDs() and undrivablePaths() — remove it from one", u.ID)
		}
	}

	var missing []string
	for id := range real {
		if !driven[id] && !undriven[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d ConformancePaths() id(s) are neither driven nor declared undrivable: %v", len(missing), missing)
	}
}

// TestDedicatedSpacePathIDsAreDrivenAndReal holds the invariant the
// dedicated-harness lists exist for: an id that runConformancePaths SUBTRACTS
// from its round-robin split must still be a real, driven path — otherwise
// subtracting it silently removes it from the matrix altogether, and the
// matrix goes green by not running it.
//
// This is the untagged consumer classificationBilateralDedicatedSpacePathIDs()
// needs, exactly as pathdrivability_test.go is already drivenPathIDs()' own.
// It is NOT a lint appeasement: the same shape covers departedCounterpartyPathIDs(),
// and the failure it guards — a subtracted id nothing else runs — is precisely
// how a conformance path stops being evidence without anyone noticing.
func TestDedicatedSpacePathIDsAreDrivenAndReal(t *testing.T) {
	t.Parallel()

	real := map[string]bool{}
	for _, p := range ConformancePaths() {
		real[p.ID] = true
	}
	driven := map[string]bool{}
	for _, id := range drivenPathIDs() {
		driven[id] = true
	}

	lists := map[string][]string{
		"classificationBilateralDedicatedSpacePathIDs": classificationBilateralDedicatedSpacePathIDs(),
		"departedCounterpartyPathIDs":                  departedCounterpartyPathIDs(),
	}

	seen := map[string]string{}
	for name, ids := range lists {
		if len(ids) == 0 {
			t.Errorf("%s() is empty — a dedicated-harness list with no ids means "+
				"runConformancePaths subtracts nothing and the harness stands up for no path", name)
		}
		for _, id := range ids {
			if !real[id] {
				t.Errorf("%s() names %q, which ConformancePaths() does not have", name, id)
			}
			if !driven[id] {
				t.Errorf("%s() names %q, which drivenPathIDs() does NOT list — "+
					"runConformancePaths would subtract it from the split and nothing "+
					"would run it, so the matrix greens by skipping it", name, id)
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("%q is in BOTH %s() and %s() — two harnesses would each "+
					"claim to run it", id, prev, name)
			}
			seen[id] = name
		}
	}
}
