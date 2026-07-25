//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// EnvReportPath optionally names a file the rendered report is also written
// to. Unset means stdout only — no default path, so a run can never quietly
// drop an artifact into the working tree.
const EnvReportPath = "A2A_LIVE_E2E_REPORT"

// liveRunCeiling bounds the entire matrix. `make live-e2e` must pass a
// -timeout at least this large, or `go test`'s own 10-minute default kills the
// run first and the report never renders — the one failure mode that costs a
// full matrix's Actions latency and returns nothing.
const liveRunCeiling = 75 * time.Minute

// TestLiveMatrix is the live tier's ONLY entry point, reachable solely
// through `make live-e2e` (which supplies -tags=livee2e). It exists in the
// tagged build so that `go test ./...` — what `make check` runs — cannot
// compile it, let alone execute it (AC-962.1).
//
// It is fail-closed end to end (AC-962.2): the matrix is declared before
// anything is read, so every cell already carries VerdictNotRun; a
// configuration failure aborts with the reason and the report exits non-zero
// having reported nothing as passed. There is no path through this function
// that reaches a zero exit without a passing cell.
func TestLiveMatrix(t *testing.T) {
	run := NewRunFor(os.Getenv(EnvOrg), DefaultRepo, Catalogue())

	cfg, err := LoadConfig(os.Getenv)
	if err != nil {
		run.Abort(err.Error())
	} else {
		// One deadline for the WHOLE run, not just preflight: seventeen
		// scenarios, each of which may wait out a bounded Actions latency,
		// add up far past any single step's ceiling. It is deliberately
		// generous — expiring here aborts mid-matrix, and a run that dies
		// half-way reports fewer rows than it actually knew. The per-step
		// ceilings (requiredCheckWaitCeiling) are what keep any ONE scenario
		// from eating it.
		ctx, cancel := context.WithTimeout(t.Context(), liveRunCeiling)
		defer cancel()

		pre, preErr := RunPreflight(ctx, &GitHubProber{}, cfg, cfg.Org, DefaultRepo)
		switch {
		case preErr != nil:
			run.Abort("preflight refused: " + preErr.Error())
		default:
			// Carried into the report so it states the identities its
			// assertions rest on — a report that does not name them cannot
			// be audited afterwards.
			run.Preflight = pre

			h, cleanup, hErr := newHarness(ctx, cfg, pre)
			defer cleanup()
			switch {
			case hErr != nil:
				run.Abort("harness construction failed: " + hErr.Error())
			default:
				driveFamilies(ctx, t, run, h)
			}
		}
	}

	report := run.Report()
	rendered := report.Render()
	fmt.Fprint(os.Stdout, "\n"+rendered)

	if path := os.Getenv(EnvReportPath); path != "" {
		if writeErr := os.WriteFile(path, []byte(rendered), 0o644); writeErr != nil {
			t.Errorf("write report to %s: %v", path, writeErr)
		}
	}

	if code := report.ExitCode(); code != 0 {
		t.Fatalf("live-e2e: %d cell(s) declared, exit code %d — see the report above", len(report.Results), code)
	}
}

// driveFamilies runs the four scenario families and records what they return.
//
// SERIALLY, and that is a correctness requirement rather than a simplification:
// the families are file-disjoint but they all drive ONE real space, and two of
// them deliberately mutate shared state (a merge with the gate not green, a
// raised min_binary_version). Run concurrently, one family's restore window
// would break every write another family has in flight, and the failure would
// surface as somebody else's bug.
//
// Order is cheapest-blast-radius first: the happy paths establish that the
// ordinary flow works before the boundary and refusal families start bending
// protection and the write floor. A family that panics is contained here — the
// remaining families still run, and its rows stay not-run rather than taking
// the whole matrix down with them.
func driveFamilies(ctx context.Context, t *testing.T, run *Run, h *harness) {
	families := []struct {
		name string
		fn   func(context.Context, *harness) []Result
	}{
		{"happy", runHappyScenarios},
		// contract-integrity (AC-973.1) writes only to its own contract's
		// section, the same "no shared-state mutation" property the happy
		// family has — placed right after it, still ahead of boundary/
		// refusal/space, which deliberately bend protection and the write
		// floor.
		{"contract-integrity", runContractIntegrityScenarios},
		{"boundary", runBoundaryScenarios},
		{"refusal", runRefusalScenarios},
		{"space", runSpaceScenarios},
	}

	for _, family := range families {
		t.Logf("=== family %s ===", family.name)
		for _, res := range runFamily(ctx, t, family.name, family.fn, h) {
			if err := run.Record(res); err != nil {
				// An undeclared cell or a zero verdict is a family/catalogue
				// mismatch. Surfaced loudly: silently dropping it would leave
				// the row not-run in a report that otherwise reads complete.
				t.Errorf("record %s/%s/%s from family %s: %v",
					res.Scenario, res.System, res.Surface, family.name, err)
				continue
			}
			t.Logf("  %-34s %-2s %s", res.Scenario, res.System, res.Verdict)
		}
	}
}

// runFamily isolates one family's panic. A live tier that loses fifteen rows
// because the sixteenth dereferenced a nil PR would report less than it knew.
func runFamily(ctx context.Context, t *testing.T, name string, fn func(context.Context, *harness) []Result, h *harness) (out []Result) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("family %s panicked: %v — its rows stay not-run", name, r)
		}
	}()
	return fn(ctx, h)
}
