package livee2e

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -update rewrites the golden fixtures. Rendering is column-padded, so
// hand-authoring the fixtures would be transcription work with a real chance
// of encoding a typo as the expected output.
var updateGolden = flag.Bool("update", false, "rewrite testdata golden report fixtures")

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./internal/livee2e/... -update`)", path, err)
	}
	if got != string(want) {
		t.Errorf("report does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

func demoScenarios() ([]string, []string, []Surface) {
	return []string{"submit-gate-merge", "cross-section-refusal"},
		[]string{"A", "B"},
		[]Surface{SurfaceCLI, SurfaceMCP}
}

func newDemoRun() *Run {
	scenarios, systems, surfaces := demoScenarios()
	return NewRun("a2ahub-live-e2e", "space", scenarios, systems, surfaces)
}

// The matrix is the cross product, declared up front, every cell not-run.
func TestNewRunDeclaresTheWholeMatrixAsNotRun(t *testing.T) {
	t.Parallel()
	run := newDemoRun()

	report := run.Report()
	if len(report.Results) != 2*2*2 {
		t.Fatalf("declared %d cells, want 8", len(report.Results))
	}
	for _, res := range report.Results {
		if res.Verdict != VerdictNotRun {
			t.Errorf("cell %s/%s/%s starts at %v, want not-run", res.Scenario, res.System, res.Surface, res.Verdict)
		}
	}
	// Declaration order is the report order — scenario, then system, then
	// surface — so two runs of the same matrix diff cleanly.
	first := report.Results[0]
	if first.Scenario != "submit-gate-merge" || first.System != "A" || first.Surface != SurfaceCLI {
		t.Errorf("first cell is %+v, want submit-gate-merge/A/cli", first)
	}
}

// The property that makes a partial run honest: cells nobody reached stay
// not-run, so a crash halfway cannot render as a short all-green list.
func TestUnreachedCellsStayNotRun(t *testing.T) {
	t.Parallel()
	run := newDemoRun()
	if err := run.Record(Result{Scenario: "submit-gate-merge", System: "A", Surface: SurfaceCLI, Verdict: VerdictPass}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	report := run.Report()
	tally := report.Tally()
	if tally[VerdictPass] != 1 || tally[VerdictNotRun] != 7 {
		t.Fatalf("tally = %v, want 1 pass and 7 not-run", tally)
	}
	// One pass out of eight is not a green run.
	if code := report.ExitCode(); code != 1 {
		t.Errorf("ExitCode = %d, want 1", code)
	}
}

func TestRecordRefusesAnUndeclaredCell(t *testing.T) {
	t.Parallel()
	run := newDemoRun()

	err := run.Record(Result{Scenario: "invented-scenario", System: "A", Surface: SurfaceCLI, Verdict: VerdictPass})
	if !errors.Is(err, ErrUndeclaredCell) {
		t.Fatalf("want ErrUndeclaredCell, got %v", err)
	}
	if got := len(run.Report().Results); got != 8 {
		t.Errorf("refused record still grew the matrix to %d cells", got)
	}
}

// Recording the zero verdict would leave a cell indistinguishable from one
// that was never dispatched.
func TestRecordRefusesTheZeroVerdict(t *testing.T) {
	t.Parallel()
	run := newDemoRun()

	if err := run.Record(Result{Scenario: "submit-gate-merge", System: "A", Surface: SurfaceCLI}); err == nil {
		t.Fatal("recording VerdictNotRun was accepted")
	}
}

// AC-962.2 — the central fail-closed assertion: an unconfigured run exits
// non-zero, reports nothing as passed, and says why.
func TestAbortedRunReportsNotRunAndExitsNonZero(t *testing.T) {
	t.Parallel()
	run := newDemoRun()
	run.Abort("A2A_LIVE_E2E_ORG is unset")

	report := run.Report()
	if code := report.ExitCode(); code != 2 {
		t.Fatalf("ExitCode = %d, want 2", code)
	}
	if tally := report.Tally(); tally[VerdictPass] != 0 || tally[VerdictNotRun] != 8 {
		t.Fatalf("tally = %v, want no passes and 8 not-run", tally)
	}
	assertGolden(t, "report_not_run.txt", report.Render())
}

// The first abort reason is the cause; later ones are its consequences.
func TestAbortKeepsTheFirstReason(t *testing.T) {
	t.Parallel()
	run := newDemoRun()
	run.Abort("preflight refused: participant holds admin")
	run.Abort("github unreachable")

	if got := run.Report().NotRunReason; got != "preflight refused: participant holds admin" {
		t.Errorf("NotRunReason = %q, want the first reason", got)
	}
}

// An empty matrix must never exit green — "nothing declared" is not "nothing
// wrong".
func TestEmptyMatrixExitsNotRun(t *testing.T) {
	t.Parallel()
	report := NewRun("a2ahub-live-e2e", "space", nil, nil, nil).Report()
	if code := report.ExitCode(); code != 2 {
		t.Fatalf("ExitCode = %d, want 2", code)
	}
}

// A timed-out cell is neither a pass nor a silent omission: it must exit
// non-zero while staying distinguishable from a real failure in the report.
func TestTimedOutIsNotAPass(t *testing.T) {
	t.Parallel()
	run := NewRun("a2ahub-live-e2e", "space", []string{"s"}, []string{"A"}, []Surface{SurfaceCLI})
	if err := run.Record(Result{Scenario: "s", System: "A", Surface: SurfaceCLI, Verdict: VerdictTimedOut}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if code := run.Report().ExitCode(); code != 1 {
		t.Fatalf("ExitCode = %d, want 1", code)
	}
}

func TestAllPassExitsZero(t *testing.T) {
	t.Parallel()
	scenarios, systems, surfaces := demoScenarios()
	run := NewRun("a2ahub-live-e2e", "space", scenarios, systems, surfaces)
	for _, scenario := range scenarios {
		for _, system := range systems {
			for _, surface := range surfaces {
				if err := run.Record(Result{Scenario: scenario, System: system, Surface: surface, Verdict: VerdictPass}); err != nil {
					t.Fatalf("Record: %v", err)
				}
			}
		}
	}
	if code := run.Report().ExitCode(); code != 0 {
		t.Fatalf("ExitCode = %d, want 0", code)
	}
}

// A mixed run: the rendered shape a maintainer actually reads, including the
// identities the boundary assertions rest on and the per-row evidence.
func TestRenderMixedRun(t *testing.T) {
	t.Parallel()
	run := newDemoRun()
	run.Preflight = Preflight{ProvisionerLogin: "operator", ParticipantLogin: "a2ahub-e2e-bot"}

	records := []Result{
		{Scenario: "submit-gate-merge", System: "A", Surface: SurfaceCLI, Verdict: VerdictPass},
		{Scenario: "submit-gate-merge", System: "A", Surface: SurfaceMCP, Verdict: VerdictPass},
		{Scenario: "submit-gate-merge", System: "B", Surface: SurfaceCLI, Verdict: VerdictTimedOut,
			Expected: "check run reaches a terminal conclusion within 5m",
			Observed: "still queued after 5m",
			Detail:   "https://github.com/a2ahub-live-e2e/space/pull/12"},
		{Scenario: "cross-section-refusal", System: "B", Surface: SurfaceCLI, Verdict: VerdictFail,
			Expected: "check stays failure after the provisioner re-triggers it",
			Observed: "check flipped to success",
			Detail:   "executed ref a2a-validate-reusable.yml@v0.6.3 — stale merge commit? (spec 36 §T6-b)"},
	}
	for _, res := range records {
		if err := run.Record(res); err != nil {
			t.Fatalf("Record %s/%s/%s: %v", res.Scenario, res.System, res.Surface, err)
		}
	}

	assertGolden(t, "report_mixed.txt", run.Report().Render())
}

// TestRenderMarksAnInjectedFaultEvidenceClassOnItsFace is AC-982.1's own
// report contract (spec 38 §6-Q1, plan D-G): a proxied row must say so ON
// ITS FACE, not only in a code comment — rendered even when the row
// PASSES, so a green report can never be mistaken for "we observed a real,
// unscheduled 5xx".
func TestRenderMarksAnInjectedFaultEvidenceClassOnItsFace(t *testing.T) {
	t.Parallel()
	run := NewRun("a2ahub-live-e2e", "space", []string{"s"}, []string{"A"}, []Surface{SurfaceCLI})
	if err := run.Record(Result{
		Scenario: "s", System: "A", Surface: SurfaceCLI, Verdict: VerdictPass,
		EvidenceClass: EvidenceClassInjectedFault,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	rendered := run.Report().Render()
	if !strings.Contains(rendered, "evidence-class: "+EvidenceClassInjectedFault) {
		t.Fatalf("rendered report does not carry the evidence-class marker on a PASSING row:\n%s", rendered)
	}
}

// TestRenderOmitsEvidenceClassWhenUnset is the same guard's other half:
// every ordinary live row (EvidenceClass == "") must render exactly as it
// did before this field existed — the byte-identical-goldens half of the
// same change.
func TestRenderOmitsEvidenceClassWhenUnset(t *testing.T) {
	t.Parallel()
	run := NewRun("a2ahub-live-e2e", "space", []string{"s"}, []string{"A"}, []Surface{SurfaceCLI})
	if err := run.Record(Result{Scenario: "s", System: "A", Surface: SurfaceCLI, Verdict: VerdictPass}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rendered := run.Report().Render(); strings.Contains(rendered, "evidence-class:") {
		t.Fatalf("rendered report carries an evidence-class marker nobody set:\n%s", rendered)
	}
}
