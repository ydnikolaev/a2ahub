package livee2e

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	Branch   string
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
	// PassEvidence is rendered even when the row PASSES, and exists because
	// Detail is not.
	//
	// Render skips Expected/Observed/Detail for a passing row on purpose: a green
	// matrix printing evidence for all 39 rows is unreadable, and a failing row
	// is READ while a passing one is scanned. But some rows' pass is itself a
	// CLAIM a reader has to be able to check, and for those, printing nothing is
	// the same failure as having no evidence.
	//
	// The space-CI health row is why this exists. It used to pass with the words
	// "every failure among them is one a scenario here caused on purpose" — an
	// assumption rendered as a finding, and the exact sentence an operator read as
	// green while their inbox filled with failure mail. Replacing it with the
	// actual claimed run ids was pointless while the field carrying them could
	// never reach the report: evidence nobody can read is not evidence, which is
	// the same mistake as a schema wired to no caller. Found by reading the first
	// live run after the accounting shipped and noticing the line was not there.
	//
	// Use it ONLY where the pass is a claim. A row whose pass means "the thing
	// worked" needs no line.
	PassEvidence string

	// EvidenceClass marks a Result that is NOT ordinary live evidence —
	// e.g. a write driven through the fault-injecting proxy
	// (injectproxy_live.go, EvidenceClassInjectedFault). Empty for every
	// ordinary live row.
	//
	// Rendered UNCONDITIONALLY (Render, below) — even on a pass — because
	// D-G's own condition for allowing an injected row at all is that a
	// reader can never mistake it for an unscheduled real 5xx (spec 38
	// §6-Q1: "a proxied row is no longer live, so it must be labelled as a
	// different class of evidence in the report, not silently mixed in").
	//
	// Deliberately NOT excluded from Tally/ExitCode: "never mixed into the
	// live rows' verdicts" is about a READER never confusing the two kinds
	// of evidence, not about a failing injected row being invisible to the
	// summary — a row that reds because its own recovery leg failed is
	// still a defect this report must not hide.
	EvidenceClass string

	// OperationalEvidence is populated only by P7 LE-OC rows. It contains
	// publish-safe facts required to produce the candidate-bound scenario log
	// and manifest row; provider payloads and credentials have no field here.
	OperationalEvidence *OperationalEvidence

	// Tier is this row's OWN declared tier (Scenario.Tier), not the tier of
	// the run that produced the result. It is set once, by Run itself, when
	// the matrix is declared (NewRunFor) and preserved by Record across
	// every subsequent write — scenario/driver code never sets it directly,
	// the same way it never sets Scenario/Branch/System/Surface to anything
	// but the cell's own declared identity. ExitCode and Record both read it
	// to decide whether a VerdictSkippedProvider is an honest logic-tier
	// report or an incoherent one (spec 09 §5).
	Tier Tier
}

// OperationalEvidence is the runtime-owned half of ScenarioEvidence. Exact
// candidate data is supplied by Run when the bundle is frozen.
type OperationalEvidence struct {
	StartedAt          time.Time
	EndedAt            time.Time
	Assertions         []AssertionEvidence
	SurfaceIDs         []string
	CommandFamily      string
	ArtifactRefs       []string
	PRRefs             []string
	CleanupStatus      string
	Limitations        []string
	LiveManifestFloor  string
	LiveManifestCommit string
}

// EvidenceClassInjectedFault marks a Result produced by a row that drove a
// write through injectingProxy (injectproxy_live.go, spec 38 §6-Q1, plan
// D-G) rather than by observing an unscheduled failure against real
// GitHub. The write itself still reaches the real GitHub API — the proxy
// genuinely forwards every request — but the CALLER never gets to see that
// it succeeded, which is the whole point of the row it marks. A real 5xx
// mid-write cannot be scheduled, so this is the only way to exercise
// AC-982.1 on demand, and D-G's own condition for allowing it is that the
// report never lets a reader mistake it for one.
const EvidenceClassInjectedFault = "injected-fault (proxied — not a live row; spec 38 §6-Q1/D-G)"

type cellKey struct {
	scenario string
	branch   string
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
	// Tier names which tier this run IS — TierLogic for the local/offline
	// entry point. Existing callers construct a Run without ever setting
	// this field (it did not exist before spec 09 §5), and the live path
	// must behave exactly as it did before this field existed — so the ZERO
	// VALUE, not TierProvider, is what "the live tier" means here. Every
	// check against it (Record, Report.ExitCode) therefore tests
	// `== TierLogic` specifically and treats everything else, including the
	// zero value, as the live/provider tier's strict behaviour.
	Tier Tier
	// Preflight names the identities every boundary assertion rests on. A
	// report that does not state them cannot be audited after the fact.
	Preflight Preflight
	// VerificationCandidate records the clean detached checkout that ran the
	// retained make-check transcript. ExecutionCandidate independently records
	// the second clean detached checkout used to build and drive live scenarios.
	VerificationCandidate CandidateAttestation
	ExecutionCandidate    CandidateAttestation
	// EvidencePath is the explicit manifest destination. Empty disables file
	// production for legacy/diagnostic callers; TestLiveMatrix sets it beside
	// the retained candidate check log.
	EvidencePath string

	order   []cellKey
	results map[cellKey]*Result
	aborted string
}

// NewRun declares the matrix as the cross product of scenarios × systems ×
// surfaces, in that nesting order — which is also the report's row order, so
// two runs of the same matrix diff cleanly.
func NewRun(org, repo string, scenarios, systems []string, surfaces []Surface) *Run {
	declared := make([]Scenario, 0, len(scenarios))
	for _, name := range scenarios {
		declared = append(declared, Scenario{Name: name, Systems: systems, Surfaces: surfaces})
	}
	return NewRunFor(org, repo, declared)
}

// NewRunFor declares the matrix from a scenario catalogue, where each
// scenario names the systems and surfaces it actually applies to.
//
// Not every scenario is a full cross product: "protection does not bind the
// provisioner" is meaningless for participant B, and declaring it anyway
// would pad the report with cells that can only ever be not-run — inflating
// apparent coverage with rows nobody intends to run.
func NewRunFor(org, repo string, scenarios []Scenario) *Run {
	r := &Run{Org: org, Repo: repo, results: map[cellKey]*Result{}}
	for _, scenario := range scenarios {
		for _, system := range scenario.Systems {
			for _, surface := range scenario.Surfaces {
				key := cellKey{scenario: scenario.Name, branch: scenario.Branch, system: system, surface: surface}
				if _, dup := r.results[key]; dup {
					continue
				}
				r.order = append(r.order, key)
				r.results[key] = &Result{
					Scenario: scenario.Name,
					Branch:   scenario.Branch,
					System:   system,
					Surface:  surface,
					Verdict:  VerdictNotRun,
					Tier:     scenario.Tier,
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
//
// Two further refusals are spec 09 §5's own gate, made impossible to record
// rather than merely discouraged:
//
//   - VerdictPass on a TierProvider row, when this Run itself is the logic
//     tier: that is a fake deciding an answer only a provider can give.
//   - VerdictSkippedProvider on a TierLogic row, in ANY run: a logic row
//     dodging judgement in the logic tier is the same lie pointing the
//     other way, and it is incoherent regardless of which tier recorded it.
func (r *Run) Record(res Result) error {
	key := cellKey{scenario: res.Scenario, branch: res.Branch, system: res.System, surface: res.Surface}
	cell, ok := r.results[key]
	if !ok {
		return fmt.Errorf("%w: %s/%s/%s/%s", ErrUndeclaredCell, res.Scenario, res.Branch, res.System, res.Surface)
	}
	if res.Verdict == VerdictNotRun {
		return fmt.Errorf("livee2e: %s/%s/%s/%s recorded VerdictNotRun — record the real outcome or leave the cell untouched",
			res.Scenario, res.Branch, res.System, res.Surface)
	}
	if res.Verdict == VerdictUnverified && !operationalOptionalBranch(res.Scenario, res.Branch) {
		return fmt.Errorf("livee2e: %s/%s is mandatory and cannot be unverified", res.Scenario, res.Branch)
	}
	if res.Verdict == VerdictPass && cell.Tier == TierProvider && r.Tier == TierLogic {
		return fmt.Errorf("livee2e: %s/%s/%s/%s is a provider-tier row — the logic tier cannot record it as a pass (spec 09 §5)",
			res.Scenario, res.Branch, res.System, res.Surface)
	}
	if res.Verdict == VerdictSkippedProvider && cell.Tier == TierLogic {
		return fmt.Errorf("livee2e: %s/%s/%s/%s is a logic-tier row — it cannot be recorded skipped-provider",
			res.Scenario, res.Branch, res.System, res.Surface)
	}
	// Tier is the cell's own declared identity, set once by NewRunFor —
	// never supplied by scenario/driver code — so it is preserved across
	// this overwrite exactly like the key fields (Scenario/Branch/System/
	// Surface) the caller already repeats faithfully.
	res.Tier = cell.Tier
	*cell = res
	return nil
}

func operationalOptionalBranch(id, branch string) bool {
	return id == "LE-OC-04" && branch == "provider-setting" || id == "LE-OC-08" && branch == "unsupported-provider"
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
		Org:                   r.Org,
		Repo:                  r.Repo,
		Tier:                  r.Tier,
		Preflight:             r.Preflight,
		VerificationCandidate: r.VerificationCandidate,
		ExecutionCandidate:    r.ExecutionCandidate,
		NotRunReason:          r.aborted,
		Results:               make([]Result, 0, len(r.order)),
	}
	for _, key := range r.order {
		out.Results = append(out.Results, *r.results[key])
	}
	return out
}

// Report is a rendered-ready snapshot of a run.
type Report struct {
	Org  string
	Repo string
	// Tier names which tier produced this report. The ZERO VALUE means the
	// live tier — see Run.Tier's own doc comment for why that is the
	// direction that keeps the existing live path byte-for-byte unchanged.
	Tier                  Tier
	Preflight             Preflight
	VerificationCandidate CandidateAttestation
	ExecutionCandidate    CandidateAttestation
	NotRunReason          string
	Results               []Result
}

// Tally counts results by verdict.
func (r Report) Tally() map[Verdict]int {
	counts := map[Verdict]int{}
	for _, res := range r.Results {
		counts[res.Verdict]++
	}
	return counts
}

// ExitCode classifies the run:
//
//	0 — every declared cell ran; mandatory cells passed, only the two
//	    explicitly optional provider branches may be unverified, and — in a
//	    LOGIC report only — a TierProvider row may be skipped-provider
//	    rather than passed
//	1 — the run happened and at least one cell did not pass
//	2 — the run did not happen (not configured, aborted, or an empty matrix)
//
// 2 is deliberately distinct from 1: "the product is wrong" and "we learned
// nothing" are different outcomes, and a tier that reported them identically
// would teach us to read a red exit as a known-flaky nuisance. Zero is
// unreachable without at least one cell, so an empty matrix can never exit
// green (AC-962.2).
//
// The skipped-provider tolerance is spec 09 §5's own design problem: a
// PROVIDER-tier row's own verdict must never be forced through the logic
// tier (Record already refuses that), so a logic run that reaches every row
// it declared MUST be able to exit 0 while its provider rows sit at
// VerdictSkippedProvider — otherwise the logic lane could never be green
// inside `make check`, which is the whole point of this wave. The tolerance
// is narrow on purpose: it fires only when BOTH r.Tier == TierLogic (this
// report is the logic tier's own) AND the row's OWN declared tier is
// TierProvider. A live report (r.Tier != TierLogic, which includes its zero
// value — see Report.Tier's own doc comment) or a row declared TierLogic
// stays a failure, exactly as today.
//
// NOTE on the process status: the live entry point is a `go test` target, and
// `go test` has only pass/fail, so `make live-e2e` reliably exits NON-ZERO on
// anything but a fully green run — but the 1-vs-2 distinction is carried in
// the rendered summary line, not in the process code. Making the process
// carry it means moving the entry point out of `go test`, which is a wave-3
// decision, not an accident to be discovered later.
func (r Report) ExitCode() int {
	if r.NotRunReason != "" || len(r.Results) == 0 {
		return 2
	}
	logicReport := r.Tier == TierLogic
	for _, res := range r.Results {
		if res.Verdict.IsPass() {
			continue
		}
		if res.Verdict == VerdictUnverified && operationalOptionalBranch(res.Scenario, res.Branch) {
			continue
		}
		if res.Verdict == VerdictSkippedProvider && logicReport && res.Tier == TierProvider {
			continue
		}
		return 1
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
	fmt.Fprintf(&b, "space:       %s/%s\n", orNone(r.Org), r.Repo)
	fmt.Fprintf(&b, "provisioner: %s\n", orNone(r.Preflight.ProvisionerLogin))
	fmt.Fprintf(&b, "participant: %s\n", orNone(r.Preflight.ParticipantLogin))
	if r.ExecutionCandidate.SourceSHA != "" {
		fmt.Fprintf(&b, "candidate:   %s tree=%s tag=%s floor=%s\n",
			r.ExecutionCandidate.SourceSHA, r.ExecutionCandidate.SourceTree, r.ExecutionCandidate.IntendedTag, r.ExecutionCandidate.TemplateFloor)
		fmt.Fprintf(&b, "verification-source: %s\n", r.VerificationCandidate.SourceRoot)
		fmt.Fprintf(&b, "verification-state: detached=%t index-clean=%t worktree-clean=%t untracked-clean=%t\n",
			r.VerificationCandidate.SourceDetached, r.VerificationCandidate.IndexClean, r.VerificationCandidate.WorktreeClean, r.VerificationCandidate.UntrackedClean)
		fmt.Fprintf(&b, "execution-source: %s\n", r.ExecutionCandidate.SourceRoot)
		fmt.Fprintf(&b, "execution-state: detached=%t index-clean=%t worktree-clean=%t untracked-clean=%t\n",
			r.ExecutionCandidate.SourceDetached, r.ExecutionCandidate.IndexClean, r.ExecutionCandidate.WorktreeClean, r.ExecutionCandidate.UntrackedClean)
		fmt.Fprintf(&b, "check-log:   %s\n", r.VerificationCandidate.CheckLog)
		fmt.Fprintf(&b, "binary:      sha256=%s vcs.revision=%s vcs.modified=%t\n",
			r.ExecutionCandidate.BinarySHA256, r.ExecutionCandidate.BuildRevision, r.ExecutionCandidate.BuildModified)
		fmt.Fprintf(&b, "binary-stamp: %s\n", r.ExecutionCandidate.BinaryStamp)
	}
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
		scenarioWidth = max(scenarioWidth, len(resultScenarioKey(res)))
		systemWidth = max(systemWidth, len(res.System))
		surfaceWidth = max(surfaceWidth, len(string(res.Surface)))
	}

	if len(r.Results) > 0 {
		fmt.Fprintf(&b, "%-*s  %-*s  %-*s  %s\n",
			scenarioWidth, "SCENARIO", systemWidth, "SYSTEM", surfaceWidth, "SURFACE", "VERDICT")
	}
	for _, res := range r.Results {
		fmt.Fprintf(&b, "%-*s  %-*s  %-*s  %s\n",
			scenarioWidth, resultScenarioKey(res), systemWidth, res.System, surfaceWidth, string(res.Surface), res.Verdict)
		// Rendered on EVERY row that carries it, pass or fail — see
		// Result.EvidenceClass's own doc comment for why this cannot wait
		// for the non-pass branch below.
		if res.EvidenceClass != "" {
			fmt.Fprintf(&b, "    evidence-class: %s\n", res.EvidenceClass)
		}
		// Evidence is indented under its row rather than crammed into
		// columns: a failing row is read, not scanned.
		if res.Verdict.IsPass() {
			// A pass that makes a CLAIM prints its evidence; a pass that
			// merely means "it worked" prints nothing. See
			// Result.PassEvidence.
			if res.PassEvidence != "" {
				fmt.Fprintf(&b, "    evidence: %s\n", res.PassEvidence)
			}
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

func resultScenarioKey(result Result) string {
	if result.Branch == "" {
		return result.Scenario
	}
	return result.Scenario + "/" + result.Branch
}

// DefaultEvidencePath reserves one unique retained-run directory beside the
// candidate-check transcript. Its manifest and logs therefore share one
// run-scoped root, while every file inside remains create-only.
func DefaultEvidencePath(candidateCheckLog string) (string, error) {
	dir, name := filepath.Split(candidateCheckLog)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = strings.TrimSuffix(name, "-check")
	runDir, err := os.MkdirTemp(dir, name+"-evidence-")
	if err != nil {
		return "", fmt.Errorf("livee2e: reserve retained evidence directory: %w", err)
	}
	return filepath.Join(runDir, "manifest.json"), nil
}

// WriteEvidenceBundle persists canonical scenario records first and the
// manifest last. Files are create-only: a retry must select a new run path,
// never overwrite evidence already attributed to a candidate execution.
func (r *Run) WriteEvidenceBundle(evidencePath string) (EvidenceManifest, error) {
	if r == nil || evidencePath == "" {
		return EvidenceManifest{}, errors.New("livee2e: evidence path is required")
	}
	if err := validateCandidateAttestationPair(r.VerificationCandidate, r.ExecutionCandidate); err != nil {
		return EvidenceManifest{}, err
	}
	dir := filepath.Dir(evidencePath)
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		return EvidenceManifest{}, fmt.Errorf("livee2e: create evidence logs directory: %w", err)
	}
	checkRaw, err := readBoundedRuntimeFile(r.VerificationCandidate.CheckLog, maxCandidateCheckLogBytes)
	if err != nil {
		return EvidenceManifest{}, fmt.Errorf("livee2e: read candidate check log: %w", err)
	}
	loggedVerification, err := candidateCheckAttestation(checkRaw, r.VerificationCandidate.CheckLog, CandidateExpectation{
		SHA: r.ExecutionCandidate.SourceSHA, Tree: r.ExecutionCandidate.SourceTree,
		Tag: r.ExecutionCandidate.IntendedTag, Floor: r.ExecutionCandidate.TemplateFloor,
	})
	if err != nil {
		return EvidenceManifest{}, fmt.Errorf("livee2e: bind retained candidate check log: %w", err)
	}
	if loggedVerification.SourceRoot != r.VerificationCandidate.SourceRoot ||
		loggedVerification.SourceSHA != r.VerificationCandidate.SourceSHA ||
		loggedVerification.SourceTree != r.VerificationCandidate.SourceTree ||
		loggedVerification.IntendedTag != r.VerificationCandidate.IntendedTag ||
		loggedVerification.SourceDetached != r.VerificationCandidate.SourceDetached ||
		loggedVerification.IndexClean != r.VerificationCandidate.IndexClean ||
		loggedVerification.WorktreeClean != r.VerificationCandidate.WorktreeClean ||
		loggedVerification.UntrackedClean != r.VerificationCandidate.UntrackedClean {
		return EvidenceManifest{}, errors.New("livee2e: retained candidate check log does not match the verification checkout attestation")
	}
	checkRel := "logs/candidate-check.log"
	if err := writeNewEvidenceFile(filepath.Join(dir, filepath.FromSlash(checkRel)), checkRaw); err != nil {
		return EvidenceManifest{}, err
	}

	truth := true
	verificationDetached := r.VerificationCandidate.SourceDetached
	verificationIndexClean := r.VerificationCandidate.IndexClean
	verificationWorktreeClean := r.VerificationCandidate.WorktreeClean
	verificationUntrackedClean := r.VerificationCandidate.UntrackedClean
	executionDetached := r.ExecutionCandidate.SourceDetached
	executionIndexClean := r.ExecutionCandidate.IndexClean
	executionWorktreeClean := r.ExecutionCandidate.WorktreeClean
	executionUntrackedClean := r.ExecutionCandidate.UntrackedClean
	buildModified := r.ExecutionCandidate.BuildModified
	manifest := EvidenceManifest{
		ManifestVersion: EvidenceManifestVersion,
		Candidate: CandidateEvidence{
			SHA: r.ExecutionCandidate.SourceSHA, Tree: r.ExecutionCandidate.SourceTree, Version: r.ExecutionCandidate.IntendedTag,
			VerificationCheckout: CheckoutEvidence{
				ID: checkoutEvidenceID(r.VerificationCandidate.SourceRoot), SHA: r.VerificationCandidate.SourceSHA, Tree: r.VerificationCandidate.SourceTree,
				Detached: &verificationDetached, IndexClean: &verificationIndexClean, WorktreeClean: &verificationWorktreeClean, UntrackedClean: &verificationUntrackedClean,
			},
			ExecutionCheckout: CheckoutEvidence{
				ID: checkoutEvidenceID(r.ExecutionCandidate.SourceRoot), SHA: r.ExecutionCandidate.SourceSHA, Tree: r.ExecutionCandidate.SourceTree,
				Detached: &executionDetached, IndexClean: &executionIndexClean, WorktreeClean: &executionWorktreeClean, UntrackedClean: &executionUntrackedClean,
			},
			BuiltSourceSHA: r.ExecutionCandidate.SourceSHA,
			Binary: BinaryEvidence{
				SHA256: "sha256:" + strings.TrimPrefix(r.ExecutionCandidate.BinarySHA256, "sha256:"), Version: r.ExecutionCandidate.TemplateFloor,
				BuildInfo: BinaryBuildInfo{Revision: r.ExecutionCandidate.BuildRevision, Modified: &buildModified},
			},
			EmbeddedTemplateFloor: r.ExecutionCandidate.TemplateFloor,
		},
		CandidateCheck: CandidateCheck{LogPath: checkRel, LogSHA256: runtimeDigest(checkRaw), Passed: &truth},
		Scenarios:      []ScenarioEvidence{},
	}

	for _, key := range r.order {
		result := r.results[key]
		if result == nil || result.OperationalEvidence == nil {
			continue
		}
		runtime := result.OperationalEvidence
		outcome, err := operationalOutcome(result.Verdict)
		if err != nil {
			return manifest, fmt.Errorf("livee2e: %s: %w", resultScenarioKey(*result), err)
		}
		row := ScenarioEvidence{
			ScenarioID: result.Scenario, Branch: result.Branch,
			StartedAt: runtime.StartedAt.UTC().Format(time.RFC3339), EndedAt: runtime.EndedAt.UTC().Format(time.RFC3339),
			SurfaceIDs: cloneRuntimeStrings(runtime.SurfaceIDs), RepositoryID: r.Org + "/" + r.Repo,
			CommandFamily: runtime.CommandFamily, Outcome: outcome,
			ArtifactRefs: cloneRuntimeStrings(runtime.ArtifactRefs), PRRefs: cloneRuntimeStrings(runtime.PRRefs),
			CleanupStatus: runtime.CleanupStatus, Limitations: cloneRuntimeStrings(runtime.Limitations),
			LiveManifestFloor: runtime.LiveManifestFloor, LiveManifestCommit: runtime.LiveManifestCommit,
			Assertions: append([]AssertionEvidence(nil), runtime.Assertions...),
		}
		row.LogPath = "logs/" + strings.ToLower(result.Scenario) + "-" + result.Branch + ".json"
		canonical, marshalErr := json.Marshal(scenarioLogEvidence{
			EvidenceVersion: ScenarioLogVersion,
			CandidateSHA:    manifest.Candidate.SHA, CandidateTree: manifest.Candidate.Tree, BinarySHA256: manifest.Candidate.Binary.SHA256,
			ScenarioID: row.ScenarioID, Branch: row.Branch, StartedAt: row.StartedAt, EndedAt: row.EndedAt,
			Outcome: row.Outcome, Assertions: row.Assertions,
		})
		if marshalErr != nil {
			return manifest, fmt.Errorf("livee2e: marshal %s scenario log: %w", resultScenarioKey(*result), marshalErr)
		}
		canonical = append(canonical, '\n')
		row.LogSHA256 = runtimeDigest(canonical)
		if err := writeNewEvidenceFile(filepath.Join(dir, filepath.FromSlash(row.LogPath)), canonical); err != nil {
			return manifest, err
		}
		manifest.Scenarios = append(manifest.Scenarios, row)
	}

	if err := ValidateEvidenceManifest(manifest); err != nil {
		return manifest, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, fmt.Errorf("livee2e: marshal evidence manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeNewEvidenceFile(evidencePath, raw); err != nil {
		return manifest, err
	}
	validated, err := ValidateEvidenceFile(evidencePath)
	if err != nil {
		return manifest, err
	}
	return validated, nil
}

func operationalOutcome(verdict Verdict) (string, error) {
	switch verdict {
	case VerdictPass:
		return "pass", nil
	case VerdictFail:
		return "fail", nil
	case VerdictUnverified:
		return "unverified", nil
	default:
		return "", fmt.Errorf("verdict %s cannot be persisted as scenario evidence", verdict)
	}
}

func cloneRuntimeStrings(values []string) []string {
	return append([]string{}, values...)
}

func validateCandidateAttestationPair(verification, execution CandidateAttestation) error {
	if verification.SourceRoot == "" || execution.SourceRoot == "" {
		return errors.New("livee2e: verification and execution checkout attestations are required")
	}
	if verification.SourceRoot == execution.SourceRoot {
		return fmt.Errorf("livee2e: verification and execution checkout roots must differ: %s", verification.SourceRoot)
	}
	if verification.SourceSHA != execution.SourceSHA || verification.SourceTree != execution.SourceTree ||
		verification.IntendedTag != execution.IntendedTag || verification.TemplateFloor != execution.TemplateFloor {
		return errors.New("livee2e: verification and execution checkout candidate identities differ")
	}
	for name, candidate := range map[string]CandidateAttestation{"verification": verification, "execution": execution} {
		if !candidate.SourceDetached || !candidate.IndexClean || !candidate.WorktreeClean || !candidate.UntrackedClean {
			return fmt.Errorf("livee2e: %s checkout attestation is not detached and clean", name)
		}
	}
	if verification.CheckLog == "" {
		return errors.New("livee2e: verification checkout check log is required")
	}
	return nil
}

func runtimeDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest)
}

func readBoundedRuntimeFile(name string, limit int64) ([]byte, error) {
	info, err := os.Lstat(name) //nolint:gosec // reason: callers provide explicit evidence/check paths; symlinks and size are rejected before reading.
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("must be a regular file no larger than %d bytes", limit)
	}
	file, err := os.OpenFile(name, os.O_RDONLY, 0) //nolint:gosec // reason: Lstat above rejects symlinks and bounds the explicit evidence/check path.
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("close %q after stat: %w", name, closeErr)
		}
		return nil, fmt.Errorf("stat %q: %w", name, err)
	}
	if !opened.Mode().IsRegular() || opened.Size() > limit {
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("close %q after stat: %w", name, closeErr)
		}
		return nil, fmt.Errorf("opened file must be regular and no larger than %d bytes", limit)
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close %q: %w", name, closeErr)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("file grew beyond the %d-byte limit while reading", limit)
	}
	return raw, nil
}

func writeNewEvidenceFile(name string, raw []byte) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // reason: name is beneath the run-scoped evidence root and O_EXCL preserves create-only evidence.
	if err != nil {
		return fmt.Errorf("livee2e: create evidence file %s: %w", name, err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("livee2e: write evidence file %s: %w", name, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("livee2e: sync evidence file %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("livee2e: close evidence file %s: %w", name, err)
	}
	return nil
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
