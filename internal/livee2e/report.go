package livee2e

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Surface is the entry point a scenario is driven through. P15 claims CLI/MCP
// parity; the live tier is where that claim is actually exercised end to end
// (spec 36 §T3).
type Surface string

const (
	// SurfaceCLI drives a scenario through the `a2a` binary.
	SurfaceCLI Surface = "cli"
	// SurfaceMCP drives the same scenario through the MCP twin, which is
	// where P15's parity claim stops being a claim.
	SurfaceMCP Surface = "mcp"
)

// ErrUndeclaredCell is returned by Record for a (scenario, system, surface)
// the matrix does not contain. Recording into an undeclared cell is refused
// rather than appended: a row nobody declared cannot be missed when it is
// absent, so it would be a result the report can gain but never lack.
var ErrUndeclaredCell = errors.New("livee2e: result recorded for an undeclared matrix cell")

// Result is one cell of the matrix.
type Result struct {
	Scenario string
	System   string
	Surface  Surface
	Verdict  Verdict
	// Expected and Observed are only meaningful on a non-pass. They are what
	// makes a failing row actionable instead of a name and a red word.
	Expected string
	Observed string
	// Detail carries the row's evidence — a PR URL, a check-run name, the
	// workflow ref that actually executed (§T6-b). Free text, rendered as-is.
	Detail string
}

type cellKey struct {
	scenario string
	system   string
	surface  Surface
}

// Run is one live-tier execution. The full matrix is DECLARED UP FRONT and
// every cell starts at VerdictNotRun, which is the property that makes the
// report honest: a run that dies after three scenarios reports the remaining
// cells as not-run rather than emitting a short, all-green list that reads
// like a complete pass (spec 36 §T3).
type Run struct {
	Org  string
	Repo string
	// Preflight names the identities every boundary assertion rests on. A
	// report that does not state them cannot be audited after the fact.
	Preflight Preflight

	order   []cellKey
	results map[cellKey]*Result
	aborted string
}

// NewRun declares the matrix as the cross product of scenarios × systems ×
// surfaces, in that nesting order — which is also the report's row order, so
// two runs of the same matrix diff cleanly.
func NewRun(org, repo string, scenarios, systems []string, surfaces []Surface) *Run {
	r := &Run{Org: org, Repo: repo, results: map[cellKey]*Result{}}
	for _, scenario := range scenarios {
		for _, system := range systems {
			for _, surface := range surfaces {
				key := cellKey{scenario, system, surface}
				if _, dup := r.results[key]; dup {
					continue
				}
				r.order = append(r.order, key)
				r.results[key] = &Result{
					Scenario: scenario,
					System:   system,
					Surface:  surface,
					Verdict:  VerdictNotRun,
				}
			}
		}
	}
	return r
}

// Record sets a declared cell's outcome. The zero Verdict is rejected: a
// scenario that ran must say what happened, and silently leaving a cell at
// not-run would hide the one state the report cannot distinguish from
// "never dispatched".
func (r *Run) Record(res Result) error {
	key := cellKey{res.Scenario, res.System, res.Surface}
	cell, ok := r.results[key]
	if !ok {
		return fmt.Errorf("%w: %s/%s/%s", ErrUndeclaredCell, res.Scenario, res.System, res.Surface)
	}
	if res.Verdict == VerdictNotRun {
		return fmt.Errorf("livee2e: %s/%s/%s recorded VerdictNotRun — record the real outcome or leave the cell untouched",
			res.Scenario, res.System, res.Surface)
	}
	*cell = res
	return nil
}

// Abort marks the whole run as not run — no credentials, preflight refused,
// GitHub unreachable. Every cell keeps its not-run verdict and the reason is
// carried into the report, so "we could not test" never renders as "nothing
// was wrong" (AC-962.2).
//
// The FIRST reason wins: it is the one that caused everything after it.
func (r *Run) Abort(reason string) {
	if r.aborted == "" {
		r.aborted = reason
	}
}

// Report freezes the run for rendering.
func (r *Run) Report() Report {
	out := Report{
		Org:          r.Org,
		Repo:         r.Repo,
		Preflight:    r.Preflight,
		NotRunReason: r.aborted,
		Results:      make([]Result, 0, len(r.order)),
	}
	for _, key := range r.order {
		out.Results = append(out.Results, *r.results[key])
	}
	return out
}

// Report is a rendered-ready snapshot of a run.
type Report struct {
	Org          string
	Repo         string
	Preflight    Preflight
	NotRunReason string
	Results      []Result
}

// Tally counts results by verdict.
func (r Report) Tally() map[Verdict]int {
	counts := map[Verdict]int{}
	for _, res := range r.Results {
		counts[res.Verdict]++
	}
	return counts
}

// ExitCode is the process status `make live-e2e` exits with.
//
//	0 — every declared cell ran and passed
//	1 — the run happened and at least one cell did not pass
//	2 — the run did not happen (not configured, aborted, or an empty matrix)
//
// 2 is deliberately distinct from 1: "the product is wrong" and "we learned
// nothing" are different outcomes, and a tier that reported them identically
// would teach us to read a red exit as a known-flaky nuisance. Zero is
// unreachable without at least one cell, so an empty matrix can never exit
// green (AC-962.2).
func (r Report) ExitCode() int {
	if r.NotRunReason != "" || len(r.Results) == 0 {
		return 2
	}
	for _, res := range r.Results {
		if !res.Verdict.IsPass() {
			return 1
		}
	}
	return 0
}

// Render writes the matrix report: a header naming the space and the
// identities the assertions rest on, one line per declared cell, then a
// summary. Deterministic — row order is declaration order and the tally is
// sorted — so golden fixtures compare byte for byte and two runs diff.
func (r Report) Render() string {
	var b strings.Builder

	b.WriteString("a2a live-e2e report\n")
	fmt.Fprintf(&b, "space:       %s/%s\n", r.Org, r.Repo)
	fmt.Fprintf(&b, "provisioner: %s\n", orNone(r.Preflight.ProvisionerLogin))
	fmt.Fprintf(&b, "participant: %s\n", orNone(r.Preflight.ParticipantLogin))
	if r.NotRunReason != "" {
		fmt.Fprintf(&b, "NOT RUN:     %s\n", r.NotRunReason)
	}
	b.WriteString("\n")

	if len(r.Results) == 0 {
		b.WriteString("(no scenarios declared)\n\n")
	}

	// Column widths from the data, so the table stays readable as scenario
	// names grow without a hand-tuned constant going stale.
	scenarioWidth, systemWidth, surfaceWidth := len("SCENARIO"), len("SYSTEM"), len("SURFACE")
	for _, res := range r.Results {
		scenarioWidth = max(scenarioWidth, len(res.Scenario))
		systemWidth = max(systemWidth, len(res.System))
		surfaceWidth = max(surfaceWidth, len(string(res.Surface)))
	}

	if len(r.Results) > 0 {
		fmt.Fprintf(&b, "%-*s  %-*s  %-*s  %s\n",
			scenarioWidth, "SCENARIO", systemWidth, "SYSTEM", surfaceWidth, "SURFACE", "VERDICT")
	}
	for _, res := range r.Results {
		fmt.Fprintf(&b, "%-*s  %-*s  %-*s  %s\n",
			scenarioWidth, res.Scenario, systemWidth, res.System, surfaceWidth, string(res.Surface), res.Verdict)
		// Evidence is indented under its row rather than crammed into
		// columns: a failing row is read, not scanned.
		if res.Verdict.IsPass() {
			continue
		}
		if res.Expected != "" {
			fmt.Fprintf(&b, "    expected: %s\n", res.Expected)
		}
		if res.Observed != "" {
			fmt.Fprintf(&b, "    observed: %s\n", res.Observed)
		}
		if res.Detail != "" {
			fmt.Fprintf(&b, "    detail:   %s\n", res.Detail)
		}
	}

	b.WriteString("\n")
	tally := r.Tally()
	verdicts := make([]Verdict, 0, len(tally))
	for v := range tally {
		verdicts = append(verdicts, v)
	}
	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i] < verdicts[j] })
	parts := make([]string, 0, len(verdicts))
	for _, v := range verdicts {
		parts = append(parts, fmt.Sprintf("%s=%d", v, tally[v]))
	}
	if len(parts) == 0 {
		parts = append(parts, "no cells")
	}
	fmt.Fprintf(&b, "summary: %s (exit %d)\n", strings.Join(parts, " "), r.ExitCode())

	return b.String()
}

// orNone keeps an unestablished identity legible in the header. Rendering an
// empty string there would read as a formatting slip; "(none)" reads as the
// fact that preflight never resolved it.
func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
