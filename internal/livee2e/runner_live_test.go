//go:build livee2e

package livee2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// EnvReportPath overrides where the rendered report is written.
//
// Unset, the report lands beside the manifest in this run's retained evidence
// directory — never in the working tree, which is what the old "stdout only"
// default was protecting. Stdout alone turned out not to be durable: the
// evidence bundle carries scenario logs for the P7 rows only, so a run driven
// outside `detached.sh` (which redirects stdout into a timestamped log) left
// every other family's verdict, and every failure's text, in a terminal
// buffer nobody could read afterwards. A candidate whose report cannot be
// re-read is a candidate that has to be run again, so this is no longer
// something the operator has to remember to ask for.
const EnvReportPath = "A2A_LIVE_E2E_REPORT"

// DefaultReportName is the report's file name inside the evidence directory.
const DefaultReportName = "report.txt"

// liveRunCeiling bounds the entire matrix. `make live-e2e` must pass a
// -timeout at least this large, or `go test`'s own 10-minute default kills the
// run first and the report never renders — the one failure mode that costs a
// full matrix's Actions latency and returns nothing. The value itself lives in
// the untagged build (runceiling.go) so TestLiveTimeoutCoversTheRunCeiling can
// hold that relationship under `make check` instead of under the live run it
// protects; the 2026-08-05 run is what made writing the gate non-optional.
//
// Sized from that run's measured throughput, not guessed. The matrix is
// serialized on pull-request round trips: 64 consecutive PRs (#2153–#2217)
// took 85.3 minutes, a steady ~80s each, and the run reached only 45 of 60
// declared cells before the previous 110-minute ceiling expired mid
// failure-recovery — taking thread-chain, data-loop, boundary, refusal and
// space with it, including this release's headline feature. The ~38 remaining
// round trips are ~51 minutes, putting a complete matrix near 160. 195 leaves
// real headroom for Actions latency without pretending the tier is cheaper
// than it is.
const liveRunCeiling = LiveRunCeiling

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
			defer func() {
				if cleanupErr := cleanup(); cleanupErr != nil {
					t.Errorf("candidate/harness cleanup failed: %v", cleanupErr)
				}
			}()
			if h != nil {
				run.VerificationCandidate = h.VerificationCandidate
				run.ExecutionCandidate = h.ExecutionCandidate
			}
			switch {
			case hErr != nil:
				run.Abort("harness construction failed: " + hErr.Error())
			default:
				// Stamped BEFORE the first write, so the health probe below
				// asks about this run's own window and not about whatever a
				// previous run left behind. Taken here rather than at the
				// top of TestLiveMatrix because harness construction itself
				// re-provisions the space and pushes to main — runs from
				// that step belong to this run and must be inside the
				// window.
				since := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
				driveFamilies(ctx, t, run, h)
				// Last, deliberately: it asks what ELSE went red in the
				// space while everything above was running, so it has to run
				// after everything above.
				if err := run.Record(runSpaceCIHealth(ctx, h, since)); err != nil {
					t.Errorf("record %s: %v", scenarioSpaceCIHealthy, err)
				}
			}
		}
	}

	report := run.Report()
	rendered := report.Render()
	if _, writeErr := fmt.Fprint(os.Stdout, "\n"+rendered); writeErr != nil {
		t.Errorf("write live report to stdout: %v", writeErr)
	}
	if run.ExecutionCandidate.SourceSHA != "" {
		evidencePath, pathErr := DefaultEvidencePath(run.VerificationCandidate.CheckLog)
		if pathErr != nil {
			t.Errorf("create P7 retained evidence run: %v", pathErr)
		}
		run.EvidencePath = evidencePath
		if pathErr == nil {
			if _, evidenceErr := run.WriteEvidenceBundle(evidencePath); evidenceErr != nil {
				t.Errorf("write P7 evidence bundle %s: %v", evidencePath, evidenceErr)
			} else {
				if _, writeErr := fmt.Fprintf(os.Stdout, "evidence: %s\n", evidencePath); writeErr != nil {
					t.Errorf("write evidence path to stdout: %v", writeErr)
				}
			}
		}
	}

	// Deliberately AFTER the bundle and keyed on the evidence directory rather
	// than on the bundle succeeding: a run whose evidence write is refused is
	// exactly the run whose report is most worth keeping. The directory is
	// created by DefaultEvidencePath and is unique per run, so a rerun of the
	// same candidate cannot overwrite an earlier report.
	reportPath := os.Getenv(EnvReportPath)
	if reportPath == "" && run.EvidencePath != "" {
		reportPath = filepath.Join(filepath.Dir(run.EvidencePath), DefaultReportName)
	}
	if reportPath != "" {
		if writeErr := os.WriteFile(reportPath, []byte(rendered), 0o644); writeErr != nil {
			t.Errorf("write report to %s: %v", reportPath, writeErr)
		} else if _, writeErr := fmt.Fprintf(os.Stdout, "report: %s\n", reportPath); writeErr != nil {
			t.Errorf("write report path to stdout: %v", writeErr)
		}
	}

	if code := report.ExitCode(); code != 0 {
		t.Fatalf("live-e2e: %d cell(s) declared, exit code %d — see the report above", len(report.Results), code)
	}
}

// driveFamilies runs the twelve scenario families and records what they return.
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
		// P7 §6.2 owns its own ordered sequence (visibility first, then
		// receipt/work/server/contracts). Run it before legacy families mutate
		// repository settings or the write floor.
		{FamilyOperationalConfidence, runOperationalConfidenceScenarios},
		{"happy", runHappyScenarios},
		// contract-integrity (AC-973.1) writes only to its own contract's
		// section, the same "no shared-state mutation" property the happy
		// family has — placed right after it, still ahead of boundary/
		// refusal/space, which deliberately bend protection and the write
		// floor.
		{"contract-integrity", runContractIntegrityScenarios},
		// AC-980.1 (spec 38 wave F) — Layer-1 rows for the five envelope
		// kinds nothing else drives. Same "no shared-state mutation"
		// property as happy/contract-integrity (every row authors its OWN
		// fresh artifacts), so placed right after them, still ahead of
		// boundary/refusal/space.
		{"submitted-family", runSubmittedFamilyScenarios},
		{"draft-family", runDraftFamilyScenarios},
		// AC-981.1 (spec 38 wave G) — Layer-2 illegal-transition rows, one
		// per kind. Same "no shared-state mutation" property as the three
		// families above (every row authors its OWN fresh artifacts and
		// the refused call never reaches the funnel at all), so placed
		// right after them, still ahead of boundary/refusal/space, which
		// deliberately bend protection and the write floor.
		{"illegal-transitions", runIllegalTransitionScenarios},
		// AC-982.1/982.2/982.3 (spec 38 wave H) — Layer-3 failure/recovery
		// rows. Same "no shared-state mutation" property as the four
		// families above (every row authors its OWN fresh artifacts; the
		// fault-injection row's proxy touches only its own one write), so
		// placed right after them, still ahead of boundary/refusal/space,
		// which deliberately bend protection and the write floor.
		{"failure-recovery", runFailureRecoveryScenarios},
		// P46 (spec 46 §T3/§T4) — one row: a chain across two real logins,
		// read back from BOTH sides. Same "no shared-state mutation"
		// property as the families above (it authors its own fresh
		// question and response), so it sits with them, ahead of the
		// families that bend protection and the write floor.
		{"thread-chain", runThreadChainScenarios},
		// Spec 05a — the contract data exchange loop. Same "no
		// shared-state mutation" property as the families above (its one
		// row authors its own fresh contract, work_request and data
		// packages), so placed right after them, still ahead of
		// boundary/refusal/space, which deliberately bend protection and
		// the write floor.
		{"data-loop", runDataLoopScenarios},
		{"boundary", runBoundaryScenarios},
		{"refusal", runRefusalScenarios},
		{"space", runSpaceScenarios},
	}

	declared := make([]string, 0, len(families))
	for _, f := range families {
		declared = append(declared, f.name)
	}

	// Pre-flight (spec 46 postmortem, 2026-07-27): the dispatch table above
	// is the ONE place a family goes from "declared in catalogue.go" to
	// "actually runs" — spec 46 shipped "thread-chain-reads-identically-
	// from-both-sides" in the catalogue and in scenarios_thread_live.go
	// without adding it here, and it sat not-run for the full 59-minute
	// live run before anyone noticed. Checked here, before this function's
	// own first GitHub call (below, in selectedFamilies/runFamily) and
	// before selectedFamilies can narrow anything: a mismatch fails in
	// milliseconds, naming the exact family, instead of at the report an
	// hour later. Compared against CanonicalFamilies() (catalogue.go/
	// familyset.go) — hand-written, INDEPENDENT of `declared` above — never
	// against `selected` below: selectedFamilies deliberately narrows a run
	// via EnvFamilies, and comparing against ITS output would fire on every
	// intentionally narrowed run.
	if missing, extra := diffFamilyNames(CanonicalFamilies(), declared); len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("driveFamilies' dispatch table does not match CanonicalFamilies() (catalogue.go): missing %v, extra %v — a family declared in the catalogue with no dispatch entry stays not-run for the whole run (spec 46 postmortem, 2026-07-27)",
			missing, extra)
	}

	// A typo here must be loud. Silently matching nothing would run zero
	// families and, because every row then stays not-run, produce a report
	// that looks exactly like a catastrophic matrix failure — the operator
	// would go debugging the product instead of their own env var.
	selected, selErr := selectedFamilies(os.Getenv(EnvFamilies), declared)
	if selErr != nil {
		t.Fatalf("%s: %v", EnvFamilies, selErr)
	}
	cells, cellErr := selectedCells(os.Getenv(EnvCells), Catalogue())
	if cellErr != nil {
		t.Fatalf("%s: %v", EnvCells, cellErr)
	}
	if cells != nil {
		cellFamilies := map[string]bool{}
		for key := range cells {
			var family string
			for _, scenario := range Catalogue() {
				if scenario.Name == key.scenario {
					family = scenario.Family
					break
				}
			}
			if family != "happy" {
				t.Fatalf("%s: exact-cell execution currently supports the happy family; %s belongs to %q",
					EnvCells, renderCellKey(key), family)
			}
			if !selected[family] {
				t.Fatalf("%s selects %s, but %s excludes its family %q",
					EnvCells, renderCellKey(key), EnvFamilies, family)
			}
			cellFamilies[family] = true
		}
		selected = cellFamilies
		h.SelectedCells = cells
		t.Logf("=== %s NARROWS this run to exact cells: %s — every other cell stays not-run, so this run CANNOT exit 0 and is not a release verdict ===",
			EnvCells, strings.Join(sortedCellKeys(cells), ", "))
	}
	if len(selected) != len(families) {
		t.Logf("=== %s NARROWS this run to: %s — every other family's rows stay not-run, so this run CANNOT exit 0 and is not a release verdict ===",
			EnvFamilies, strings.Join(sortedKeys(selected), ", "))
	}

	for _, family := range families {
		if !selected[family.name] {
			t.Logf("=== family %s SKIPPED (%s) — its rows stay not-run ===", family.name, EnvFamilies)
			continue
		}
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

func sortedCellKeys(cells map[cellKey]bool) []string {
	out := make([]string, 0, len(cells))
	for key := range cells {
		out = append(out, renderCellKey(key))
	}
	sort.Strings(out)
	return out
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

// diffFamilyNames reports the SET difference between CanonicalFamilies()
// (untagged data, catalogue.go/familyset.go) and driveFamilies' own dispatch
// table (`declared`, built from the `families` slice a few lines above its
// call site). Written directly against the two plain []string slices rather
// than reusing missingCoveredFamilies/unknownDeclaredFamilies (familyset.go):
// those compare a Scenario's Family field against the canonical list, a
// different comparison (catalogue rows vs. canonical) from this one
// (dispatch table vs. canonical) — sharing a helper between the two would
// blur which comparison a failure came from.
func diffFamilyNames(canonical, declared []string) (missing, extra []string) {
	declaredSet := make(map[string]bool, len(declared))
	for _, d := range declared {
		declaredSet[d] = true
	}
	canonicalSet := make(map[string]bool, len(canonical))
	for _, c := range canonical {
		canonicalSet[c] = true
	}
	for _, c := range canonical {
		if !declaredSet[c] {
			missing = append(missing, c)
		}
	}
	for _, d := range declared {
		if !canonicalSet[d] {
			extra = append(extra, d)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
