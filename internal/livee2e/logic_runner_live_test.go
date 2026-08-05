//go:build livee2e

package livee2e

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestLogicMatrix is the logic tier's ONLY entry point (plan D-5: the tier
// is chosen by WHICH TEST RUNS, never by an env var a mis-set shell could
// flip). It never calls LoadConfig and never reads an A2A_LIVE_E2E_*_TOKEN
// variable — newLogicHarness stands the space up entirely on a filesystem
// path served by testkit/fakegithub, so this function literally cannot
// construct a live Config. (It DOES read A2A_LIVE_E2E_FAMILIES/_CELLS,
// through the same driveFamilies/selectedFamilies/selectedCells path
// TestLiveMatrix uses — a deliberate reuse of the one existing narrowing
// mechanism, not a second one; narrowing away a TierLogic row still leaves
// it not-run, which still reds ExitCode(), so this stays fail-closed the
// same way an accidentally-exported live token variable would not be.)
//
// It drives the REAL Catalogue() through driveFamilies — the identical
// dispatch table TestLiveMatrix uses (runner_live_test.go) — rather than a
// second, hand-maintained one: spec 46's postmortem is a row that shipped in
// the catalogue and in a scenario file but was never added to that
// dispatch table, and sat not-run for a full 59-minute live run before
// anyone noticed. A second table here would reintroduce exactly that,
// just for the tier meant to catch it in seconds instead.
//
// Every TierProvider row is recorded skipped-provider rather than judged —
// driveFamilies' own tier-narrowing (a family owning no TierLogic row is
// never entered; a mixed family's provider cells are overridden before
// Record sees them; a completion sweep backfills anything still not-run,
// including FamilySpaceCIHealth's one row, which — like the live tier — this
// function never dispatches directly). The 30 TierLogic rows are judged for
// real, against the fake, exactly as the live tier judges them against
// GitHub.
func TestLogicMatrix(t *testing.T) {
	start := time.Now()
	ctx := t.Context()

	h, cleanup, err := newLogicHarness(ctx, t)
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Errorf("logic harness cleanup failed: %v", cleanupErr)
		}
	}()
	if err != nil {
		t.Fatalf("newLogicHarness: %v", err)
	}

	// Asserted before anything else: a logic run must never be able to
	// reach a real API root (plan D-5's structural property
	// TestLiveTierIsNotAMergeGate depends on downstream).
	if err := h.Seam.Validate(); err != nil {
		t.Fatalf("logic harness seam did not validate: %v", err)
	}
	if h.Seam.IsRealGitHub() {
		t.Fatalf("logic harness seam reports real GitHub — refusing to drive a write against it: %+v", h.Seam)
	}

	// run.Tier = TierLogic is what makes this a LOGIC report rather than a
	// live one (Run.Tier's own doc, report.go): it is what lets driveFamilies
	// narrow by tier, what lets Record refuse a provider row's pass, and
	// what lets ExitCode() tolerate a skipped-provider verdict on a
	// TierProvider row. run.VerificationCandidate/ExecutionCandidate are
	// deliberately never assigned — see TestNewLogicHarnessLeavesExecutionCandidateZero
	// (logic_harness_live_test.go): this run can never write an evidence
	// bundle, because nothing here ever calls WriteEvidenceBundle, and it
	// never will as long as the only candidate this file constructs is the
	// zero value.
	run := NewRunFor(logicOrg, logicRepo, Catalogue())
	run.Tier = TierLogic
	run.Preflight = h.Pre

	driveFamilies(ctx, t, run, h)

	report := run.Report()
	rendered := report.Render()
	t.Log("\n" + rendered)

	if code := report.ExitCode(); code != 0 {
		t.Fatalf("logic-e2e: %d cell(s) declared, exit code %d, %s elapsed — see the report above",
			len(report.Results), code, time.Since(start))
	}

	// The D-7 marker. Emitted LAST, only after ExitCode() proved every cell
	// this tier may judge actually passed, and computed from THIS RUN's own
	// results — never from a static Catalogue() read.
	//
	// That distinction is the whole value of the marker. A digest computed
	// from the catalogue would claim complete coverage even when a `-run`
	// regex, a dispatch-table gap or a panicked family meant a row never
	// executed — which is precisely the defect spec 46's postmortem paid a
	// 59-minute live run to discover. Computed from results, a row that did
	// not run cannot appear here, the digest diverges from what
	// verifyCandidateCheckLog recomputes from the catalogue, and the RELEASE
	// gate reds naming the row.
	//
	// A row counts only when EVERY declared cell of it passed: a row is one
	// claim, and a claim half-proven is not proven. Printed to stdout rather
	// than through t.Logf so it lands in the transcript as its own line, and
	// note the lane must run with -v or `go test` discards a passing
	// package's output entirely (verified empirically — scripts/verify.sh's
	// run_logic_tests carries it for this reason).
	fmt.Println(logicTierMarkerLine(report))

	t.Logf("TestLogicMatrix: %d cell(s) judged (logic tier judged; provider tier's own rows recorded skipped-provider) in %s",
		len(report.Results), time.Since(start))
}

// logicTierMarkerLine renders the LOGIC_TIER_ROWS_SHA256 line for a finished
// logic report: the digest of every TierLogic row whose every declared cell
// passed in THIS run. See its one call site above for why it is derived from
// results rather than from the catalogue.
func logicTierMarkerLine(report Report) string {
	passed := map[string]bool{}
	for _, res := range report.Results {
		if res.Tier != TierLogic {
			continue
		}
		key := res.Scenario
		if res.Branch != "" {
			key = res.Scenario + "/" + res.Branch
		}
		if _, seen := passed[key]; !seen {
			passed[key] = true
		}
		if !res.Verdict.IsPass() {
			passed[key] = false
		}
	}
	keys := make([]string, 0, len(passed))
	for key, ok := range passed {
		if ok {
			keys = append(keys, key)
		}
	}
	return "LOGIC_TIER_ROWS_SHA256=" + logicTierRowsDigest(keys)
}

// snapshotDirNames lists the top-level entry names under dir, or an empty
// set when dir does not exist yet — the normal state on a machine that has
// never run a2a. It reads via os.UserHomeDir()-rooted paths ONLY, never a
// checkout's own (isolated) Home: this is the check on the REAL machine
// state a hermetic tier must never touch.
func snapshotDirNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

// newDirNames returns the names present in after but not before, sorted.
func newDirNames(before, after map[string]bool) []string {
	var out []string
	for name := range after {
		if !before[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TestLogicTierWritesNothingOutsideItsOwnTempDirs is the gate that makes the
// logic tier's hermeticity a property that is CHECKED, not one this wave's
// author fixed once and trusted to stay fixed.
//
// It exists because it very nearly wasn't written: five consecutive runs of
// this package's early tagged tests each left a directory in this
// operator's REAL `~/.cache/a2a/mirrors` — the same cache a real live-e2e
// run's mirror lives in — and the gap was noticed only by ACCIDENT, when two
// of those five runs happened to collide on the same cache key and one of
// them failed loudly. A run that did NOT collide would have polluted the
// operator's machine and reported a clean pass; that is the actual failure
// mode a hermeticity property defends against, and it is silent by
// construction unless something asserts it. The root cause (an ambient
// mirror_root machine config a logic checkout has no business reading) is
// fixed in newLogicHarness (each checkout gets its own isolated HOME,
// workspace_live.go's checkout.Home) — this test is what keeps that fix
// from being one refactor away from quietly regressing.
//
// Deliberately drives a REAL write (draft+submit+sync), not just
// construction: a hermeticity gap in the write path (a submit, a sync) is
// exactly as real as one in newLogicHarness itself, and a construction-only
// check would miss it.
func TestLogicTierWritesNothingOutsideItsOwnTempDirs(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	mirrorsDir := filepath.Join(realHome, ".cache", "a2a", "mirrors")
	configDir := filepath.Join(realHome, ".config", "a2a")

	beforeMirrors := snapshotDirNames(t, mirrorsDir)
	beforeConfig := snapshotDirNames(t, configDir)

	ctx := t.Context()
	h, cleanup, err := newLogicHarness(ctx, t)
	defer func() {
		if cleanupErr := cleanup(); cleanupErr != nil {
			t.Errorf("logic harness cleanup failed: %v", cleanupErr)
		}
	}()
	if err != nil {
		t.Fatalf("newLogicHarness: %v", err)
	}

	if _, err := h.DraftAndSubmit(ctx, h.A, "announcement"); err != nil {
		t.Fatalf("draft+submit an announcement from checkout A: %v", err)
	}
	if _, stderr, err := h.B.Run(ctx, "sync"); err != nil {
		t.Fatalf("a2a sync (checkout B): %v: %s", err, stderr)
	}

	if newNames := newDirNames(beforeMirrors, snapshotDirNames(t, mirrorsDir)); len(newNames) > 0 {
		t.Errorf("logic run wrote into the operator's real mirror cache %s: new entries %v", mirrorsDir, newNames)
	}
	if newNames := newDirNames(beforeConfig, snapshotDirNames(t, configDir)); len(newNames) > 0 {
		t.Errorf("logic run wrote into the operator's real machine config dir %s: new entries %v", configDir, newNames)
	}
}
