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
