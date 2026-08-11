//go:build livee2e

package livee2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
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
// function never dispatches directly). The 32 TierLogic rows this function
// dispatches are judged for real, against the fake, exactly as the live tier
// judges them against GitHub.
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
	// W3's conformance-path catalogue (plan 11, docs/features/active/
	// agent-ops-2026-07/plans/11-authoring-path-and-seam-verification.plan.md):
	// one subtest per declared Path, run over this SAME harness (D1: no
	// second rig) — BEFORE driveFamilies, not wrapped in its own t.Run
	// around that call. Both orderings were tried; this one is required,
	// not stylistic: the space-update family (scenarios_space_live.go)
	// legitimately and permanently raises the scaffolded space's
	// min_binary_version to the product's real current floor as part of
	// proving `a2a space update` durable, while this harness's own binary
	// stays stamped at buildLogicBinary's fixed
	// contract.ContractPublicationFloor for the run's whole lifetime. Run
	// after driveFamilies, every one of this file's writes was refused
	// ("local binary version older than space.yaml min_binary_version") —
	// observed empirically, not assumed; see this wave's own report for the
	// exact failure text. Run first, the space still matches the stamped
	// binary. Wrapping driveFamilies itself in its own t.Run (so `-run
	// '^TestLogicMatrix$/^group-[0-9]+$/^<path-id>$'` could skip the family
	// matrix too) was
	// considered and rejected regardless of ordering:
	// TestLogicTierWritesNothingOutsideItsOwnTempDirs and logicTierMarkerLine
	// below both read state driveFamilies populates directly off run/report
	// in this function's own body, and restructuring that call site is
	// outside this brief's granted scope — reported as a residual in the
	// wave's own report, not hidden. A path subtest failure still fails
	// TestLogicMatrix as a whole, same as a family-matrix row failure
	// already does.
	// P10 W1 — the paths get their OWN space. `h` above is the family
	// matrix's; this is a second, independent one, and D1's "no second rig"
	// is deliberately overturned here because the evidence that produced D1
	// has been superseded by evidence against it.
	//
	// The shared space COUPLED two unrelated suites, in two directions, both
	// observed rather than reasoned about:
	//
	//  1. The paragraph above records the first: the space-update family
	//     permanently raises the scaffolded space's min_binary_version, so
	//     every path write afterwards was refused. The fix was an ORDERING
	//     constraint — paths first — which works only while nothing else ever
	//     mutates the space durably.
	//  2. The second was found on 2026-08-07 and ordering cannot fix it. The
	//     71 paths leave hundreds of commits behind, and LE-OC-03/baseline's
	//     fixed 20-second conditional-snapshot budget then expires against the
	//     enlarged mirror — the loopback server answers 304 for the whole
	//     window, its revision never recomputing. Isolated by experiment: that
	//     cell passes with all eleven families and NO paths (exit 0, whole
	//     matrix green), passes alone in 44s, and fails whenever the paths ran
	//     first — on a quiet machine and a loaded one alike.
	//
	// Two suites that can break each other through shared state are not one
	// suite; making them separate is what stops the next such coupling from
	// being discovered as somebody else's red. It also retires the ordering
	// constraint in (1): with separate spaces neither suite can reach the
	// other's min_binary_version, so `paths first` is now a free choice rather
	// than a load-bearing one.
	//
	// The second harness pays a second binary build (seconds, warm cache)
	// against a tier that costs ~41 minutes. Sharing one build across both is
	// a real optimisation and deliberately NOT taken here: this wave changes
	// isolation only, so that a measurement of it measures isolation.
	// P10 W2 — how many path spaces to stand up. The bound is DERIVED from
	// the machine, never a literal tuned on one laptop (spec 10 §5), and is
	// overridable so a bisect can pin it: A2A_LOGIC_PATH_SPACES=1 reproduces
	// the serial behaviour exactly, which is the property that makes a
	// concurrency regression reversible by one constant instead of a revert.
	//
	// Capped at 4 rather than NumCPU outright: every path spawns real `a2a`
	// subprocesses doing real git, so the ceiling is IO and process churn well
	// before it is cores, and the spec's own §3 warns that the win is "four to
	// six fold, and the number is to be measured, not claimed".
	pathSpaces := runtime.NumCPU() / 2
	if pathSpaces > 4 {
		pathSpaces = 4
	}
	if pathSpaces < 1 {
		pathSpaces = 1
	}
	if raw := os.Getenv("A2A_LOGIC_PATH_SPACES"); raw != "" {
		n, convErr := strconv.Atoi(raw)
		if convErr != nil || n < 1 {
			t.Fatalf("A2A_LOGIC_PATH_SPACES=%q is not a positive integer — refusing to guess what was meant", raw)
		}
		pathSpaces = n
	}

	// Every harness is constructed HERE, on the test goroutine, before any
	// parallel subtest starts. fakegithub.New calls t.Fatalf, which is illegal
	// off the test goroutine (spec 10 §4), so the setup stays serial and only
	// the group bodies run concurrently. ~1s each, deliberately paid.
	pathHarnesses := make([]*harness, 0, pathSpaces)
	for i := 0; i < pathSpaces; i++ {
		ph, phCleanup, phErr := newLogicHarness(ctx, t)
		// t.Cleanup, NEVER defer, and the difference is not stylistic — it
		// was a real red. A deferred call registered in this function runs
		// when this function RETURNS, and Go releases parallel subtests only
		// after that. So `defer cleanup()` deleted all four path workspaces
		// BEFORE a single group ran, and the groups then failed with
		// `chdir .../livee2e-logic-NNN/A: no such file or directory`.
		// t.Cleanup runs after every subtest, parallel ones included, which
		// is the only correct hook once anything below calls t.Parallel().
		t.Cleanup(func() {
			if cleanupErr := phCleanup(); cleanupErr != nil {
				t.Errorf("conformance-path harness %d cleanup failed: %v", i+1, cleanupErr)
			}
		})
		if phErr != nil {
			t.Fatalf("newLogicHarness (conformance paths, space %d): %v", i+1, phErr)
		}
		if err := ph.Seam.Validate(); err != nil {
			t.Fatalf("conformance-path harness %d seam did not validate: %v", i+1, err)
		}
		if ph.Seam.IsRealGitHub() {
			t.Fatalf("conformance-path harness %d seam reports real GitHub — refusing to drive a write against it: %+v", i+1, ph.Seam)
		}
		pathHarnesses = append(pathHarnesses, ph)
	}
	t.Logf("conformance paths: %d space(s)", len(pathHarnesses))
	runConformancePaths(ctx, t, pathHarnesses)

	// P8 wave 31 (correcting wave 30B) — a FIFTH, dedicated space for
	// Family 15's own three departed-counterparty paths
	// (pathdriver_live.go's runDepartedCounterpartyPaths). Started with
	// the ORDINARY two-active-participant scaffold, unlike wave 30B's own
	// genesis-departed attempt: that attempt reds every one of these three
	// paths on REF-006 ("`to` includes a system marked `left`") the moment
	// the FIRST step tries to address the already-departed counterparty —
	// a scenario that must first ADDRESS a counterparty and only THEN
	// watch it leave can never be answered by seeding "left" before that
	// submission exists at all. Each driver now departs systemBravo
	// MID-PATH instead (SetParticipantStatusMidPath, provision_live.go),
	// after its own create+first-transition has already landed and before
	// its own final transition — see pathcatalogue_paths.go's own Family
	// 15 doc comment for the full finding.
	//
	// Still never one of the pathHarnesses above, and still its own
	// dedicated space: SetParticipantStatusMidPath is exactly as
	// irreversible as genesis departure was (no `a2a` CLI verb ever
	// writes this field, in either direction), so mixing
	// pathdrivability.go's own departedCounterpartyPathIDs() into the
	// ordinary round-robin split would still silently depart a
	// counterparty every OTHER path in that harness's group assumes is
	// active — runConformancePaths already excludes them from its own
	// split for exactly this reason; this harness is where they actually
	// run (runDepartedCounterpartyPaths itself, sequentially, restoring
	// systemBravo to active at the top of each driver so the three paths
	// stay independent of run order — ensureFamily15CounterpartyActive's
	// own doc comment).
	departedHarness, departedCleanup, departedErr := newLogicHarness(ctx, t)
	t.Cleanup(func() {
		if cleanupErr := departedCleanup(); cleanupErr != nil {
			t.Errorf("departed-counterparty harness cleanup failed: %v", cleanupErr)
		}
	})
	if departedErr != nil {
		t.Fatalf("newLogicHarness (departed counterparty, systemBravo): %v", departedErr)
	}
	if err := departedHarness.Seam.Validate(); err != nil {
		t.Fatalf("departed-counterparty harness seam did not validate: %v", err)
	}
	if departedHarness.Seam.IsRealGitHub() {
		t.Fatalf("departed-counterparty harness seam reports real GitHub — refusing to drive a write against it: %+v", departedHarness.Seam)
	}
	t.Run("departed-counterparty", func(t *testing.T) {
		t.Parallel()
		runDepartedCounterpartyPaths(ctx, t, departedHarness)
	})

	run := NewRunFor(logicOrg, logicRepo, Catalogue())
	run.Tier = TierLogic
	run.Preflight = h.Pre

	// Wrapped in its own subtest so
	// `-run '^TestLogicMatrix$/^group-[0-9]+$/^<path-id>$'` prunes the
	// family matrix too. Without this the filter pruned
	// only the path subtests and a "single path" run still paid the whole
	// matrix's wall clock — which fails the operator's own requirement that
	// every path be checkable independently.
	//
	// The naive wrap is unsafe, and that is why the escape below exists: with
	// the matrix filtered out, `run` collects no results, and the report plus
	// the D-7 marker would then be computed from a run that never happened —
	// a digest claiming complete coverage on the strength of zero executed
	// cells. That is precisely the defect the marker was built to catch (spec
	// 46's postmortem, a 59-minute live run spent discovering a row that never
	// ran), so a filtered run must emit NOTHING rather than something
	// reassuring.
	t.Run("family-matrix", func(t *testing.T) {
		driveFamilies(ctx, t, run, h)
	})

	report := run.Report()
	// "Nothing executed" is EVERY cell still at VerdictNotRun, never an empty
	// slice: NewRunFor pre-seeds one not-run cell per declared row, which is
	// the property that makes an un-driven row visible instead of silent
	// (Report's own doc comment). Reading emptiness here would have looked
	// right and never fired.
	executed := false
	for _, res := range report.Results {
		if res.Verdict != VerdictNotRun {
			executed = true
			break
		}
	}
	if !executed {
		t.Log("logic-e2e: the family matrix was not selected by -run, so no cell " +
			"executed — deliberately emitting no report and no D-7 marker rather " +
			"than a digest computed from an empty run. The conformance paths above " +
			"stand on their own assertions.")
		return
	}
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
