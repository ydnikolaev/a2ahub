package livee2e

import (
	"strings"
	"testing"
)

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
		"confirmSpaceLocalPathIDs":                     confirmSpaceLocalPathIDs(),
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

// TestPathHostDeclarationsAreSound runs the host-declaration checker over the
// real catalogue, and requires the two dedicated-space families that push raw
// to a local space's default branch to say so: an undeclared one would be
// stood up on a host whose pre-receive hook refuses its first step, and the
// failure would read as a product bug.
func TestPathHostDeclarationsAreSound(t *testing.T) {
	t.Parallel()

	paths := ConformancePaths()
	for _, problem := range pathHostProblems(paths) {
		t.Error(problem)
	}

	byID := map[string]Path{}
	for _, p := range paths {
		byID[p.ID] = p
	}
	for name, ids := range map[string][]string{
		"departedCounterpartyPathIDs":                  departedCounterpartyPathIDs(),
		"classificationBilateralDedicatedSpacePathIDs": classificationBilateralDedicatedSpacePathIDs(),
	} {
		for _, id := range ids {
			if byID[id].RunsOn(HostKindBareRepo) {
				t.Errorf("%s() lists %q, which declares no exclusion on the bare-repo host: "+
					"it pushes raw to the default branch, which a local space refuses", name, id)
			}
		}
	}
	for _, id := range confirmSpaceLocalPathIDs() {
		if byID[id].RunsOn(HostKindGitHub) {
			t.Errorf("%q needs a confirm-gates local space but declares no exclusion on GitHub", id)
		}
	}
}

// TestPathHostProblemsSeededDefects proves the checker has teeth: each defect
// it claims to refuse, planted in a catalogue of its own, is named.
func TestPathHostProblemsSeededDefects(t *testing.T) {
	t.Parallel()

	why := "a fact about the host"
	cases := []struct {
		name  string
		paths []Path
		want  string
	}{
		{"an empty reason", []Path{{ID: "p", NotOn: []HostExclusion{{Host: HostKindBareRepo, Reason: " \t"}}}}, "with no reason"},
		{"an unknown host", []Path{{ID: "p", NotOn: []HostExclusion{{Host: HostKind(9), Reason: why}}}}, "unknown host unknown(9)"},
		{"a repeated host", []Path{{ID: "p", NotOn: []HostExclusion{{Host: HostKindBareRepo, Reason: why}, {Host: HostKindBareRepo, Reason: why}}}}, "twice"},
		{"excluded everywhere", []Path{{ID: "p", NotOn: []HostExclusion{{Host: HostKindBareRepo, Reason: why}, {Host: HostKindGitHub, Reason: why}}}}, "excluded on every host"},
		{"a precondition the host does not run", []Path{
			{ID: "pre", NotOn: []HostExclusion{{Host: HostKindBareRepo, Reason: why}}},
			{ID: "post", Precondition: "pre"},
		}, `runs on host bare-repo but its precondition "pre" does not`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(pathHostProblems(tc.paths), "\n")
			if !strings.Contains(got, tc.want) {
				t.Fatalf("pathHostProblems = %q, want a problem containing %q", got, tc.want)
			}
		})
	}

	if got := pathHostProblems([]Path{
		{ID: "pre"},
		{ID: "post", Precondition: "pre", NotOn: []HostExclusion{{Host: HostKindBareRepo, Reason: why}}},
	}); len(got) != 0 {
		t.Fatalf("a path excluded on a host its precondition still runs on is sound, got %v", got)
	}
}
