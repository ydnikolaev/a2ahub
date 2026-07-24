package livee2e

import (
	"strings"
	"testing"
)

// Every boundary scenario must be declared for SystemB ONLY. SystemA is
// backed by the provisioner, which holds admin: with `enforce_admins: false`
// (which the test space mirrors from production) an assertion authored by it
// passes without testing anything.
//
// This is the catalogue-level companion to RunPreflight's run-time refusal:
// preflight guarantees the participant is not an admin, and this guarantees
// nobody quietly declares a boundary row for the admin-backed system.
func TestBoundaryScenariosAreParticipantOnly(t *testing.T) {
	t.Parallel()

	// The scenarios whose whole point is that an authority pushes back.
	boundary := map[string]bool{
		"protection-blocks-until-green":     true,
		"direct-push-refused":               true,
		"cross-section-retrigger-stays-red": true,
		"protection-binds-participant":      true,
		"out-of-section-write-refused":      true,
		"stale-write-floor-refused":         true,
		"space-update-scope-preflight":      true,
	}

	seen := map[string]bool{}
	for _, scenario := range Catalogue() {
		seen[scenario.Name] = true
		if !boundary[scenario.Name] {
			continue
		}
		for _, system := range scenario.Systems {
			if system != SystemB {
				t.Errorf("boundary scenario %q is declared for system %q; only %q is non-admin, so any other system asserts nothing (spec 36 §T5)",
					scenario.Name, system, SystemB)
			}
		}
	}

	// A renamed scenario must not silently fall out of the boundary set.
	for name := range boundary {
		if !seen[name] {
			t.Errorf("boundary scenario %q is not in the catalogue — renamed without updating this guard?", name)
		}
	}
}

// The one scenario that deliberately asserts protection does NOT bind must be
// declared for the admin-backed system, or it tests the opposite of its name
// (AC-961.4's second half).
func TestProtectionSkipsProvisionerIsProvisionerOnly(t *testing.T) {
	t.Parallel()
	for _, scenario := range Catalogue() {
		if scenario.Name != "protection-skips-provisioner" {
			continue
		}
		if len(scenario.Systems) != 1 || scenario.Systems[0] != SystemA {
			t.Fatalf("declared for %v, want exactly [%s]", scenario.Systems, SystemA)
		}
		return
	}
	t.Fatal("protection-skips-provisioner is missing from the catalogue — AC-961.4 asserts protection's reach in BOTH directions")
}

func TestCatalogueIsWellFormed(t *testing.T) {
	t.Parallel()
	names := map[string]bool{}
	for _, scenario := range Catalogue() {
		switch {
		case scenario.Name == "":
			t.Error("catalogue holds an unnamed scenario")
		case names[scenario.Name]:
			t.Errorf("duplicate scenario %q — the matrix would silently collapse the rows", scenario.Name)
		case strings.TrimSpace(scenario.Name) != scenario.Name:
			t.Errorf("scenario %q has surrounding whitespace; report columns are padded from these", scenario.Name)
		case len(scenario.Systems) == 0:
			t.Errorf("scenario %q declares no systems, so it can never produce a row", scenario.Name)
		case len(scenario.Surfaces) == 0:
			t.Errorf("scenario %q declares no surfaces, so it can never produce a row", scenario.Name)
		}
		names[scenario.Name] = true
	}
	if len(names) == 0 {
		t.Fatal("the catalogue is empty")
	}
}

// NewRunFor must declare exactly the cells the catalogue asks for — not a
// full cross product, which would pad the report with rows nobody intends to
// run and inflate apparent coverage.
func TestNewRunForHonoursPerScenarioApplicability(t *testing.T) {
	t.Parallel()
	run := NewRunFor("org", DefaultRepo, []Scenario{
		{Name: "everywhere", Systems: []string{SystemA, SystemB}, Surfaces: bothSurfaces()},
		{Name: "participant-cli-only", Systems: []string{SystemB}, Surfaces: cliOnly()},
	})

	results := run.Report().Results
	if len(results) != 5 {
		t.Fatalf("declared %d cells, want 5 (4 + 1)", len(results))
	}
	for _, res := range results {
		if res.Scenario == "participant-cli-only" && (res.System != SystemB || res.Surface != SurfaceCLI) {
			t.Errorf("unexpected cell %s/%s/%s", res.Scenario, res.System, res.Surface)
		}
	}
}
