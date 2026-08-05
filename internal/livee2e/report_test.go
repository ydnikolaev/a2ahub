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

func TestReportRendersCandidateSourceAndBinaryAttestation(t *testing.T) {
	t.Parallel()
	run := newDemoRun()
	run.VerificationCandidate = CandidateAttestation{
		SourceRoot: "/tmp/verification", SourceSHA: strings.Repeat("a", 40),
		SourceTree: strings.Repeat("b", 40), IntendedTag: "v0.19.0",
		TemplateFloor: "0.19.0", CheckLog: "/tmp/check.log",
		SourceDetached: true, IndexClean: true, WorktreeClean: true, UntrackedClean: true,
	}
	run.ExecutionCandidate = CandidateAttestation{
		SourceRoot: "/tmp/execution", SourceSHA: strings.Repeat("a", 40),
		SourceTree: strings.Repeat("b", 40), IntendedTag: "v0.19.0",
		TemplateFloor:  "0.19.0",
		SourceDetached: true, IndexClean: true, WorktreeClean: true, UntrackedClean: true,
		BinarySHA256: strings.Repeat("c", 64), BuildRevision: strings.Repeat("a", 40),
		BuildModified: false, BinaryStamp: "a2a 0.19.0 (" + strings.Repeat("a", 40) + ")",
	}
	rendered := run.Report().Render()
	for _, want := range []string{
		"candidate:   " + strings.Repeat("a", 40),
		"tree=" + strings.Repeat("b", 40),
		"verification-source: /tmp/verification",
		"verification-state: detached=true index-clean=true worktree-clean=true untracked-clean=true",
		"execution-source: /tmp/execution",
		"execution-state: detached=true index-clean=true worktree-clean=true untracked-clean=true",
		"check-log:   /tmp/check.log",
		"sha256=" + strings.Repeat("c", 64),
		"vcs.modified=false",
		"binary-stamp: a2a 0.19.0 (" + strings.Repeat("a", 40) + ")",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered report missing %q:\n%s", want, rendered)
		}
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

// TestRenderPrintsEvidenceForAClaimingPass is the regression for a defect in
// this tier's own instrumentation, found by reading the first live run after the
// accounting shipped and noticing the line was simply not there.
//
// Render skips Expected/Observed/Detail for a passing row on purpose: a green
// matrix that printed evidence for all 39 rows is unreadable. But the space-CI
// health row's PASS is a claim — "these are the red runs this matrix owns up to
// having caused, by id" — and that claim went into Detail, which a passing row
// never prints. The ids were computed, carried, and dropped on the floor.
//
// That is the same mistake as a schema wired to no caller, at a smaller scale:
// something built, correct, and connected to nothing that reads it. It replaced
// a sentence that was an assumption dressed as a finding, so having it render
// nowhere left the row asserting less than the sentence it improved on.
func TestRenderPrintsEvidenceForAClaimingPass(t *testing.T) {
	t.Parallel()

	run := NewRun("a2ahub-live-e2e", "space", []string{"claiming-row"}, []string{SystemA}, []Surface{SurfaceCLI})
	if err := run.Record(Result{
		Scenario: "claiming-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail:       "detail that a passing row must NOT print",
		PassEvidence: "4 failure(s) claimed by the rows that caused them: run 2116, 2117",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	out := run.Report().Render()

	if !strings.Contains(out, "run 2116, 2117") {
		t.Errorf("a claiming pass did not print its evidence — the ids ARE the verdict, and without them "+
			"the row is back to asserting that the reds were deliberate:\n%s", out)
	}
	// And the ordinary Detail stays suppressed: the reason this needed its own
	// field rather than "print Detail on a pass too" is that most rows carry a
	// PR number there, and 39 of those is the noise this report avoids.
	if strings.Contains(out, "detail that a passing row must NOT print") {
		t.Errorf("Detail leaked onto a passing row — every row carries one, and printing them all is the "+
			"unreadable report this suppression exists to prevent:\n%s", out)
	}
}

// TestRecordRefusesPassOnAProviderRowInALogicRun is spec 09 §5's own point,
// made impossible to record rather than merely discouraged: a logic run
// recording a pass for a TierProvider row would be a fake deciding an
// answer only a provider can give.
func TestRecordRefusesPassOnAProviderRowInALogicRun(t *testing.T) {
	t.Parallel()
	run := NewRunFor("org", "repo", []Scenario{
		{Name: "provider-row", Tier: TierProvider, Systems: []string{SystemA}, Surfaces: cliOnly()},
	})
	run.Tier = TierLogic

	err := run.Record(Result{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass})
	if err == nil {
		t.Fatal("logic run recorded a pass for a provider-tier row")
	}
}

// TestRecordAllowsPassOnAProviderRowInALiveRun proves the refusal above is
// specific to the logic tier: recording a real pass for a provider-tier row
// is the ordinary, intended live-tier outcome and must stay unaffected.
func TestRecordAllowsPassOnAProviderRowInALiveRun(t *testing.T) {
	t.Parallel()
	run := NewRunFor("org", "repo", []Scenario{
		{Name: "provider-row", Tier: TierProvider, Systems: []string{SystemA}, Surfaces: cliOnly()},
	})
	// run.Tier left at its zero value — the live tier.

	if err := run.Record(Result{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// TestRecordRefusesSkippedProviderOnALogicRow is spec 09 §5's other
// direction: a logic-tier row dodging judgement in the logic tier is the
// same lie pointing the other way, and it is incoherent in ANY run — not
// conditioned on run.Tier.
func TestRecordRefusesSkippedProviderOnALogicRow(t *testing.T) {
	t.Parallel()
	run := NewRunFor("org", "repo", []Scenario{
		{Name: "logic-row", Tier: TierLogic, Systems: []string{SystemA}, Surfaces: cliOnly()},
	})

	err := run.Record(Result{Scenario: "logic-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider})
	if err == nil {
		t.Fatal("recorded skipped-provider for a logic-tier row")
	}
}

// TestRecordAllowsSkippedProviderOnAProviderRowInALogicRun is the one
// coherent use of VerdictSkippedProvider: the logic tier declining to judge
// a row only the provider tier may judge.
func TestRecordAllowsSkippedProviderOnAProviderRowInALogicRun(t *testing.T) {
	t.Parallel()
	run := NewRunFor("org", "repo", []Scenario{
		{Name: "provider-row", Tier: TierProvider, Systems: []string{SystemA}, Surfaces: cliOnly()},
	})
	run.Tier = TierLogic

	if err := run.Record(Result{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider}); err != nil {
		t.Fatalf("Record: %v", err)
	}
}

// TestLogicRunExitsZeroWithProviderRowsSkipped is the wave's central design
// problem (report.go:ExitCode doc): a logic run must be able to be green
// inside `make check` even though its provider rows are never judged there.
func TestLogicRunExitsZeroWithProviderRowsSkipped(t *testing.T) {
	t.Parallel()
	run := NewRunFor("org", "repo", []Scenario{
		{Name: "logic-row", Tier: TierLogic, Systems: []string{SystemA}, Surfaces: cliOnly()},
		{Name: "provider-row", Tier: TierProvider, Systems: []string{SystemA}, Surfaces: cliOnly()},
	})
	run.Tier = TierLogic

	if err := run.Record(Result{Scenario: "logic-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass}); err != nil {
		t.Fatalf("Record logic-row: %v", err)
	}
	if err := run.Record(Result{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider}); err != nil {
		t.Fatalf("Record provider-row: %v", err)
	}

	if code := run.Report().ExitCode(); code != 0 {
		t.Fatalf("ExitCode = %d, want 0 — a logic run must be able to be green inside make check", code)
	}
}

// TestExitCodeNeverTolerartesASkippedLogicRow is ExitCode's OWN rule proven
// directly against a hand-built Report, independent of Record's guard —
// belt and suspenders: even if a logic row's verdict were somehow forced to
// skipped-provider, ExitCode itself must never treat that as tolerated.
func TestExitCodeNeverToleratesASkippedLogicRow(t *testing.T) {
	t.Parallel()
	report := Report{
		Tier: TierLogic,
		Results: []Result{
			{Scenario: "logic-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider, Tier: TierLogic},
		},
	}
	if code := report.ExitCode(); code != 1 {
		t.Fatalf("ExitCode = %d, want 1 — a logic row skipped-provider must never be tolerated", code)
	}
}

// TestExitCodeTreatsSkippedProviderAsFailureInALiveReport is ExitCode's
// other guard: a LIVE report (Tier at its zero value) must never tolerate a
// skipped-provider row, even one declared TierProvider — the live tier must
// actually judge it.
func TestExitCodeTreatsSkippedProviderAsFailureInALiveReport(t *testing.T) {
	t.Parallel()
	report := Report{
		Results: []Result{
			{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider, Tier: TierProvider},
		},
	}
	if code := report.ExitCode(); code != 1 {
		t.Fatalf("ExitCode = %d, want 1 — a live report must never tolerate a skipped-provider row", code)
	}
}

// TestExitCodeTreatsSkippedProviderAsGreenOnlyInALogicReportForAProviderRow
// is the exact tolerated combination, proven directly against ExitCode.
func TestExitCodeTreatsSkippedProviderAsGreenOnlyInALogicReportForAProviderRow(t *testing.T) {
	t.Parallel()
	report := Report{
		Tier: TierLogic,
		Results: []Result{
			{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider, Tier: TierProvider},
		},
	}
	if code := report.ExitCode(); code != 0 {
		t.Fatalf("ExitCode = %d, want 0", code)
	}
}

// TestRenderShowsSkippedProviderRowVisibly is the D-2/D-3 report contract:
// a skipped-provider row must show up on its face, the same way a not-run
// row already does — never silently, never as absent.
func TestRenderShowsSkippedProviderRowVisibly(t *testing.T) {
	t.Parallel()
	run := NewRunFor("a2ahub-live-e2e", "space", []Scenario{
		{Name: "provider-row", Tier: TierProvider, Systems: []string{SystemA}, Surfaces: cliOnly()},
	})
	run.Tier = TierLogic
	if err := run.Record(Result{Scenario: "provider-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictSkippedProvider}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	rendered := run.Report().Render()
	if !strings.Contains(rendered, "skipped-provider") {
		t.Fatalf("skipped-provider row is not visible in the rendered report:\n%s", rendered)
	}
}

// TestRenderStaysQuietForAnOrdinaryPass is the other half: a pass with no claim
// prints nothing extra. Without this, "print the evidence" could be satisfied by
// printing something for every row, which is the failure mode the suppression
// exists to prevent.
func TestRenderStaysQuietForAnOrdinaryPass(t *testing.T) {
	t.Parallel()

	run := NewRun("a2ahub-live-e2e", "space", []string{"ordinary-row"}, []string{SystemA}, []Surface{SurfaceCLI})
	if err := run.Record(Result{
		Scenario: "ordinary-row", System: SystemA, Surface: SurfaceCLI, Verdict: VerdictPass,
		Detail: "PR #123",
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	out := run.Report().Render()

	if strings.Contains(out, "evidence:") {
		t.Errorf("an ordinary pass printed an evidence line: %s", out)
	}
}
