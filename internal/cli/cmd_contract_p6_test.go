package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/skillcoverage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type p6PublicationFake struct {
	preflight cli.ContractPublicationRequest
	publish   cli.ContractPublicationRequest
	result    space.ContractPublicationResult
	err       error
}

func (f *p6PublicationFake) Preflight(_ context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	f.preflight = request
	return f.result, f.err
}

func (f *p6PublicationFake) Publish(_ context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	f.publish = request
	return f.result, f.err
}

type p6MaterializeFake struct {
	request cli.ContractMaterializeRequest
	result  space.ContractMaterializeResult
}

func (f *p6MaterializeFake) MaterializeContract(_ context.Context, request cli.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	f.request = request
	return f.result, nil
}

type p6CheckFake struct {
	request cli.ContractCheckRequest
	result  contract.ConformanceResult
}

type p6InspectionFake struct {
	diffRequest   cli.ContractDiffRequest
	verifyRequest cli.ContractVerifyExportRequest
	diffResult    cli.ContractDiffResult
	verifyResult  cli.ContractVerifyExportResult
}

func (f *p6InspectionFake) DiffContract(_ context.Context, request cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
	f.diffRequest = request
	return f.diffResult, nil
}

func (f *p6InspectionFake) VerifyContractExport(_ context.Context, request cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
	f.verifyRequest = request
	return f.verifyResult, nil
}

func (f *p6CheckFake) CheckContract(_ context.Context, request cli.ContractCheckRequest) (contract.ConformanceResult, error) {
	f.request = request
	return f.result, nil
}

func newP6ContractCommand(t *testing.T, publication cli.ContractPublicationOperations, materialize cli.ContractMaterializeOperation, check cli.ContractCheckOperation) *cli.ContractCommand {
	t.Helper()
	cmd := cli.NewContractCommand(nil, &fakeLifecycleFunnel{}, t.TempDir(), "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	cmd.SetP6Operations(publication, materialize, check)
	return cmd
}

func runP6Contract(t *testing.T, cmd *cli.ContractCommand, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cmd.Run(t.Context(), args, cli.IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	return code, stdout.String(), stderr.String()
}

func TestContractP6PreflightAndPublishAreThin(t *testing.T) {
	t.Parallel()
	result := space.ContractPublicationResult{Status: space.ContractPublicationPlanned, Plan: contract.PublicationPlan{Contract: "XC-axon-orders", TargetVersion: "2.0.0", PlanDigest: "sha256:plan"}}
	fake := &p6PublicationFake{result: result}
	cmd := newP6ContractCommand(t, fake, &p6MaterializeFake{}, &p6CheckFake{})

	code, output, stderr := runP6Contract(t, cmd, "preflight", "XC-axon-orders", "--bump", "major", "--staging", "candidate", "--json")
	if code != 0 || stderr != "" || fake.preflight.ID != "XC-axon-orders" || fake.preflight.Bump != "major" || fake.preflight.Staging != "candidate" {
		t.Fatalf("preflight mismatch: code=%d out=%q err=%q req=%+v", code, output, stderr, fake.preflight)
	}
	var got space.ContractPublicationResult
	if err := json.Unmarshal([]byte(output), &got); err != nil || got.Plan.PlanDigest != "sha256:plan" {
		t.Fatalf("preflight JSON mismatch: result=%+v err=%v", got, err)
	}

	code, _, stderr = runP6Contract(t, cmd, "publish", "XC-axon-orders", "--version", "2.0.0", "--expect-plan", "sha256:reviewed", "--staging", "candidate")
	if code != 0 || stderr != "" || fake.publish.Version != "2.0.0" || fake.publish.ExpectPlan != "sha256:reviewed" || fake.publish.Staging != "candidate" {
		t.Fatalf("publish mismatch: code=%d err=%q req=%+v", code, stderr, fake.publish)
	}
}

// TestContractP6AllowEmptyBumpFlagAndTextWarning covers AC-4's two halves on
// the CLI surface: the flag reaches the request (--allow-empty-bump), and
// the TEXT render prints the acknowledgement Plan.Warnings carries — not
// only under --json, where it was already visible as part of the encoded
// result.
func TestContractP6AllowEmptyBumpFlagAndTextWarning(t *testing.T) {
	t.Parallel()
	result := space.ContractPublicationResult{
		Status: space.ContractPublicationSubmitted,
		Plan: contract.PublicationPlan{
			Contract: "XC-axon-orders", TargetVersion: "1.1.0", PlanDigest: "sha256:plan",
			Warnings: []contract.Finding{{
				Code: "empty-bump-acknowledged", Path: "mutations",
				Message: "--allow-empty-bump acknowledged: this bump's 1 mutation(s) touch no normative artifact",
			}},
		},
	}
	fake := &p6PublicationFake{result: result}
	cmd := newP6ContractCommand(t, fake, &p6MaterializeFake{}, &p6CheckFake{})

	code, output, stderr := runP6Contract(t, cmd, "publish", "XC-axon-orders", "--version", "1.1.0", "--allow-empty-bump")
	if code != 0 || stderr != "" || !fake.publish.AllowEmptyBump {
		t.Fatalf("publish mismatch: code=%d err=%q req=%+v", code, stderr, fake.publish)
	}
	if !strings.Contains(output, "empty-bump-acknowledged") || !strings.Contains(output, "this bump's 1 mutation(s) touch no normative artifact") {
		t.Fatalf("text render dropped the empty-bump acknowledgement: output=%q", output)
	}
}

func TestContractP6MalformedAndPlanConflict(t *testing.T) {
	t.Parallel()
	fake := &p6PublicationFake{err: space.ErrContractPublicationPlanChanged}
	cmd := newP6ContractCommand(t, fake, &p6MaterializeFake{}, &p6CheckFake{})
	for _, args := range [][]string{
		{"preflight", "XC-axon-orders", "--version", "1.0.0", "--bump", "patch"},
		{"preflight", "XC-axon-orders", "--bump", "sideways"},
		{"materialize", "not-a-versioned-ref", "--to", "vendor/contract"},
		{"check", "XC-axon-orders@1.0.0", "--suite", "--payload", "payload.json"},
	} {
		code, _, _ := runP6Contract(t, cmd, args...)
		if code != 2 {
			t.Fatalf("args %v: got exit %d, want usage exit 2", args, code)
		}
	}
	code, _, stderr := runP6Contract(t, cmd, "publish", "XC-axon-orders", "--version", "1.0.0", "--expect-plan", "sha256:old")
	if code != 1 || !strings.Contains(stderr, space.ErrContractPublicationPlanChanged.Error()) || !errors.Is(fake.err, space.ErrContractPublicationPlanChanged) {
		t.Fatalf("plan conflict mismatch: code=%d stderr=%q", code, stderr)
	}
}

func TestContractP6MaterializeAndCheckProjectSharedShapes(t *testing.T) {
	t.Parallel()
	materialize := &p6MaterializeFake{result: space.ContractMaterializeResult{
		ContractID: "XC-axon-orders", Version: "1.0.0", Destination: "vendor/orders", Outcome: space.ContractMaterialized,
	}}
	check := &p6CheckFake{result: contract.ConformanceResult{
		Ref: "XC-axon-orders@1.0.0", Mode: contract.ConformanceModeSuite, Supported: true, Passed: true,
		Outcome: contract.ConformanceSuiteConsistent, Results: []contract.ConformanceCaseResult{},
	}}
	cmd := newP6ContractCommand(t, &p6PublicationFake{}, materialize, check)

	code, output, stderr := runP6Contract(t, cmd, "materialize", "XC-axon-orders@1.0.0", "--to", "vendor/orders", "--json")
	if code != 0 || stderr != "" || materialize.request.Destination != "vendor/orders" || !strings.Contains(output, `"outcome":"materialized"`) {
		t.Fatalf("materialize mismatch: code=%d out=%q err=%q req=%+v", code, output, stderr, materialize.request)
	}
	code, output, stderr = runP6Contract(t, cmd, "check", "XC-axon-orders@1.0.0", "--suite", "--json")
	if code != 0 || stderr != "" || !check.request.Suite || !strings.Contains(output, `"outcome":"suite-consistent"`) {
		t.Fatalf("suite mismatch: code=%d out=%q err=%q req=%+v", code, output, stderr, check.request)
	}

	check.result.Passed = false
	check.result.Outcome = contract.ConformanceNonconformant
	code, output, _ = runP6Contract(t, cmd, "check", "XC-axon-orders@1.0.0", "--payload", "payload.json", "--schema", "schema/main.json", "--json")
	if code != 1 || check.request.Suite || check.request.PayloadPath != "payload.json" || check.request.SchemaPath != "schema/main.json" || !strings.Contains(output, `"outcome":"nonconformant"`) {
		t.Fatalf("payload mismatch: code=%d out=%q req=%+v", code, output, check.request)
	}
}

func TestContractP6InspectionDelegatesWithoutGitOrDigestLogic(t *testing.T) {
	t.Parallel()
	inspection := &p6InspectionFake{
		diffResult:   cli.ContractDiffResult{Added: []string{"schema/new.json"}, Removed: []string{}, Changed: []string{}, FrontmatterChanged: []string{"compat_policy: old -> new"}},
		verifyResult: cli.ContractVerifyExportResult{ID: "XC-axon-orders", Matches: false, LocalDigest: "sha256:local", WantDigest: "sha256:published", Diff: cli.ContractDiffResult{Changed: []string{"schema/main.json"}}},
	}
	cmd := newP6ContractCommand(t, &p6PublicationFake{}, &p6MaterializeFake{}, &p6CheckFake{})
	cmd.SetP6Inspection(inspection)
	code, output, stderr := runP6Contract(t, cmd, "diff", "XC-axon-orders", "1.0.0", "2.0.0", "--json")
	if code != 0 || stderr != "" || inspection.diffRequest.V1 != "1.0.0" || !strings.Contains(output, `"schema/new.json"`) {
		t.Fatalf("diff mismatch: code=%d out=%q err=%q req=%+v", code, output, stderr, inspection.diffRequest)
	}
	code, output, stderr = runP6Contract(t, cmd, "verify-export", "XC-axon-orders@1.0.0", "--local", "exports/orders")
	if code != 1 || inspection.verifyRequest.Local != "exports/orders" || !strings.Contains(output, "changed schema/main.json") || !strings.Contains(stderr, "digest mismatch") {
		t.Fatalf("verify mismatch: code=%d out=%q err=%q req=%+v", code, output, stderr, inspection.verifyRequest)
	}
}

// TestVerifyExportThreeOutcomesAreDistinguishable is spec 03's carried AC-9
// (P2's own AC-6): matched/drifted/unmeasured must print DIFFERENT text on
// the human path, and only matched/drifted may emit a "matches" key under
// --json — an unmeasured run has nothing to compare, so `"matches":false`
// would read identically to a genuine drift.
func TestVerifyExportThreeOutcomesAreDistinguishable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		result        cli.ContractVerifyExportResult
		wantCode      int
		wantOut       []string
		wantOutAbsent []string
		wantErr       []string
		wantJSONHas   bool
	}{
		{
			name:        "matched",
			result:      cli.ContractVerifyExportResult{ID: "XC-axon-orders", Matches: true, Outcome: "matched", LocalDigest: "sha256:same"},
			wantCode:    0,
			wantOut:     []string{"XC-axon-orders matches (sha256:same)"},
			wantJSONHas: true,
		},
		{
			name:        "drifted",
			result:      cli.ContractVerifyExportResult{ID: "XC-axon-orders", Matches: false, Outcome: "drifted", LocalDigest: "sha256:local", WantDigest: "sha256:published", Diff: cli.ContractDiffResult{FrontmatterChanged: []string{"compat_policy: old -> new"}}},
			wantCode:    1,
			wantOut:     []string{"frontmatter compat_policy: old -> new"},
			wantErr:     []string{"digest mismatch"},
			wantJSONHas: true,
		},
		{
			name:          "unmeasured",
			result:        cli.ContractVerifyExportResult{ID: "XC-axon-orders", Matches: false, Outcome: "unmeasured", LocalDigest: "sha256:local"},
			wantCode:      0,
			wantOut:       []string{"nothing to compare"},
			wantOutAbsent: []string{"digest mismatch", "matches"},
			wantErr:       nil,
			wantJSONHas:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inspection := &p6InspectionFake{verifyResult: tc.result}
			cmd := newP6ContractCommand(t, &p6PublicationFake{}, &p6MaterializeFake{}, &p6CheckFake{})
			cmd.SetP6Inspection(inspection)

			code, output, stderr := runP6Contract(t, cmd, "verify-export", "XC-axon-orders@1.0.0", "--local", "exports/orders")
			if code != tc.wantCode {
				t.Fatalf("%s: code = %d, want %d (out=%q err=%q)", tc.name, code, tc.wantCode, output, stderr)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(output, want) {
					t.Fatalf("%s: stdout %q does not contain %q", tc.name, output, want)
				}
			}
			for _, absent := range tc.wantOutAbsent {
				if strings.Contains(output, absent) {
					t.Fatalf("%s: stdout %q unexpectedly contains %q", tc.name, output, absent)
				}
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(stderr, want) {
					t.Fatalf("%s: stderr %q does not contain %q", tc.name, stderr, want)
				}
			}
			if tc.wantErr == nil && stderr != "" {
				t.Fatalf("%s: expected empty stderr, got %q", tc.name, stderr)
			}

			_, jsonOut, jsonErr := runP6Contract(t, cmd, "verify-export", "XC-axon-orders@1.0.0", "--local", "exports/orders", "--json")
			if jsonErr != "" {
				t.Fatalf("%s: --json unexpected stderr %q", tc.name, jsonErr)
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(jsonOut), &decoded); err != nil {
				t.Fatalf("%s: --json output does not decode: %v (%q)", tc.name, err, jsonOut)
			}
			_, hasMatches := decoded["matches"]
			if hasMatches != tc.wantJSONHas {
				t.Fatalf("%s: --json \"matches\" key present=%v, want %v (%q)", tc.name, hasMatches, tc.wantJSONHas, jsonOut)
			}
		})
	}
}

// TestVerifyExportJSONMatchesMCPEncoding is spec 03 AC-2's table test: every
// key the MCP surface's ContractVerifyExportResult puts on the wire must
// also be on the CLI's --json wire, with the same value — mcp's own type
// carries no "matches" field at all (P2's three-outcome vocabulary shipped
// there first), so the CLI's own retained "matches" is the one deliberate,
// documented difference (present on matched/drifted, absent on unmeasured
// — TestVerifyExportThreeOutcomesAreDistinguishable's own case above).
func TestVerifyExportJSONMatchesMCPEncoding(t *testing.T) {
	t.Parallel()
	for _, outcome := range []string{"matched", "drifted", "unmeasured"} {
		t.Run(outcome, func(t *testing.T) {
			mcpResult := mcp.ContractVerifyExportResult{
				ID: "XC-axon-orders", Outcome: outcome,
				LocalDigest: "sha256:local", WantDigest: "sha256:published",
				Diff: mcp.ContractDiffResult{Changed: []string{"schema/main.json"}, FrontmatterChanged: []string{"compat_policy: old -> new"}},
			}
			cliResult := cli.ContractVerifyExportResult{
				ID: mcpResult.ID, Matches: outcome == "matched", Outcome: mcpResult.Outcome,
				LocalDigest: mcpResult.LocalDigest, WantDigest: mcpResult.WantDigest,
				Diff: cli.ContractDiffResult{Changed: mcpResult.Diff.Changed, FrontmatterChanged: mcpResult.Diff.FrontmatterChanged},
			}

			mcpRaw, err := json.Marshal(mcpResult)
			if err != nil {
				t.Fatalf("marshal mcp result: %v", err)
			}
			var mcpDecoded map[string]any
			if err := json.Unmarshal(mcpRaw, &mcpDecoded); err != nil {
				t.Fatalf("decode mcp result: %v", err)
			}

			cliRaw, err := json.Marshal(cliResult)
			if err != nil {
				t.Fatalf("marshal cli result: %v", err)
			}
			var cliDecoded map[string]any
			if err := json.Unmarshal(cliRaw, &cliDecoded); err != nil {
				t.Fatalf("decode cli result: %v", err)
			}

			for key, want := range mcpDecoded {
				got, ok := cliDecoded[key]
				if !ok {
					t.Fatalf("%s: cli encoding is missing key %q that mcp emits", outcome, key)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s: cli[%q] = %v, want %v (mcp's own value)", outcome, key, got, want)
				}
			}
			_, hasMatches := cliDecoded["matches"]
			if want := outcome != "unmeasured"; hasMatches != want {
				t.Fatalf("%s: cli \"matches\" presence = %v, want %v", outcome, hasMatches, want)
			}
		})
	}
}

// TestContractP6PublicationHumanLineNamesCandidateAndMutationCount is
// fb-20260827-47069c's own fix (US-2/AC-3): the preflight/publish human
// line must name the candidate source and the mutation count — both were
// computed already and reachable only under --json before this phase.
func TestContractP6PublicationHumanLineNamesCandidateAndMutationCount(t *testing.T) {
	t.Parallel()
	result := space.ContractPublicationResult{
		Status: space.ContractPublicationPlanned,
		Plan: contract.PublicationPlan{
			Contract: "XC-axon-orders", TargetVersion: "1.0.1", PlanDigest: "sha256:plan",
			CandidateSource: contract.CandidateSource{Kind: contract.CandidateSourceMirror, Location: "a1b2c3", Fingerprint: "sha256:fp"},
			Mutations:       []contract.PublicationMutation{{Action: contract.MutationWrite, Path: "contract.md"}},
		},
	}
	fake := &p6PublicationFake{result: result}
	cmd := newP6ContractCommand(t, fake, &p6MaterializeFake{}, &p6CheckFake{})

	code, output, stderr := runP6Contract(t, cmd, "preflight", "XC-axon-orders", "--version", "1.0.1")
	if code != 0 || stderr != "" {
		t.Fatalf("preflight mismatch: code=%d out=%q err=%q", code, output, stderr)
	}
	if !strings.Contains(output, "candidate mirror a1b2c3 (1 mutation(s))") {
		t.Fatalf("preflight text output did not name the candidate source and mutation count: %q", output)
	}
}

// TestContractP6PublicationHumanLineOmitsCandidateWhenUnset covers the
// zero-value case (e.g. a fixture result with no CandidateSource at all):
// the render must not print a garbled "candidate  (0 mutation(s))" line.
func TestContractP6PublicationHumanLineOmitsCandidateWhenUnset(t *testing.T) {
	t.Parallel()
	result := space.ContractPublicationResult{Status: space.ContractPublicationPlanned, Plan: contract.PublicationPlan{Contract: "XC-axon-orders", TargetVersion: "2.0.0", PlanDigest: "sha256:plan"}}
	fake := &p6PublicationFake{result: result}
	cmd := newP6ContractCommand(t, fake, &p6MaterializeFake{}, &p6CheckFake{})

	code, output, stderr := runP6Contract(t, cmd, "preflight", "XC-axon-orders", "--bump", "major")
	if code != 0 || stderr != "" {
		t.Fatalf("preflight mismatch: code=%d out=%q err=%q", code, output, stderr)
	}
	if strings.Contains(output, "candidate ") {
		t.Fatalf("preflight text output printed a candidate line with no candidate source set: %q", output)
	}
}

// --- render-ledger universe: scripts/check-render-ledger.sh's own driver ---
//
// renderLedgerCandidateSourceSurface and renderLedgerMutationsSurface are
// narrow, PURPOSE-BUILT wrappers around contract.CandidateSource /
// contract.PublicationMutation — never the full space.ContractPublicationResult
// (SurfaceKeys flattens it to ~65 keys, most already governed elsewhere and
// none of them this phase's business) or contract.PublicationPlan directly.
// Wrapping preserves BOTH the container field's own JSON name
// (candidate_source / mutations, exactly as contract.PublicationPlan tags
// them) and its element type's fields, via the SAME reflection primitive
// (SurfaceKeys) — never a hand-typed field list — scoped to exactly what
// this phase's two live fixes (fb-20260827-bc1f13, fb-20260827-47069c) and
// the render-ledger gate govern.
type renderLedgerCandidateSourceSurface struct {
	CandidateSource contract.CandidateSource `json:"candidate_source"`
}

type renderLedgerMutationsSurface struct {
	Mutations []contract.PublicationMutation `json:"mutations"`
}

// renderLedgerSurfaces is the render-ledger gate's OWN, independently
// derived universe: SurfaceKeys applied to exactly the result/wrapper types
// this phase's two live fixes and its carried P7 amendment cover.
// Deliberately NOT routed through internal/skillcoverage's shared
// Register/Registered — see cmd_contract_p6.go's own init() doc comment for
// why: that registry feeds cmd/a2a/catalog.go's `--surfaces --json`, which
// schemas/prose-coverage.yaml (outside this phase's allowlist) also
// consumes, and every field this function derives would need a prose row
// nobody could add there this wave.
func renderLedgerSurfaces() map[string][]string {
	return map[string][]string{
		"contract-diff":             skillcoverage.SurfaceKeys(reflect.TypeOf(cli.ContractDiffResult{})),
		"contract-verify-export":    skillcoverage.SurfaceKeys(reflect.TypeOf(cli.ContractVerifyExportResult{})),
		"contract-candidate-source": skillcoverage.SurfaceKeys(reflect.TypeOf(renderLedgerCandidateSourceSurface{})),
		"contract-mutations":        skillcoverage.SurfaceKeys(reflect.TypeOf(renderLedgerMutationsSurface{})),
		"contract-verify-published": skillcoverage.SurfaceKeys(reflect.TypeOf(cli.ContractVerifyPublishedResult{})),
	}
}

// TestRenderLedgerSurfaceDump is scripts/check-render-ledger.sh's own "ask
// the program" entry point, not only a conventional single-assertion test:
// internal/skillcoverage cannot import internal/cli/internal/contract/
// internal/space without becoming a domain package (see its own package
// doc comment), and a standalone `go run` of a file outside this module's
// tree cannot import an `internal/` package at all — Go's own
// internal-visibility rule, verified by hand: such a file fails to build
// with "use of internal package ... not allowed", regardless of the
// invoking shell's working directory. This file already imports every type
// the render ledger's universe needs, so it is where the derivation must
// live; the gate script runs `go test ./internal/cli/... -run
// TestRenderLedgerSurfaceDump` with RENDER_LEDGER_DUMP set to a file path
// and reads the JSON this test writes there.
//
// Every assertion below runs UNCONDITIONALLY, never behind a skip: setting
// RENDER_LEDGER_DUMP only adds an extra write, so a plain `go test
// ./internal/cli/...` (no gate involved) still exercises real coverage
// instead of a test that only means something when a gate calls it.
func TestRenderLedgerSurfaceDump(t *testing.T) {
	surfaces := renderLedgerSurfaces()
	for name, keys := range surfaces {
		if len(keys) == 0 {
			t.Fatalf("render-ledger surface %q derived zero keys — SurfaceKeys is failing closed on a real type", name)
		}
	}
	if !sameStringSet(surfaces["contract-diff"], []string{"added", "changed", "frontmatter_changed", "removed"}) {
		t.Fatalf("contract-diff = %v, want exactly {added,changed,frontmatter_changed,removed}", surfaces["contract-diff"])
	}
	if !containsAllStrings(surfaces["contract-verify-export"], "id", "matches", "outcome", "local_digest", "want_digest", "diff", "added", "removed", "changed", "frontmatter_changed") {
		t.Fatalf("contract-verify-export missing an expected key: %v", surfaces["contract-verify-export"])
	}
	if !sameStringSet(surfaces["contract-candidate-source"], []string{"candidate_source", "fingerprint", "kind", "location"}) {
		t.Fatalf("contract-candidate-source = %v, want exactly {candidate_source,fingerprint,kind,location}", surfaces["contract-candidate-source"])
	}
	if !containsAllStrings(surfaces["contract-mutations"], "mutations", "action", "path", "before_digest", "after_digest") {
		t.Fatalf("contract-mutations missing an expected key: %v", surfaces["contract-mutations"])
	}
	if !containsAllStrings(surfaces["contract-verify-published"], "system", "total", "id", "space_id", "version", "status", "local", "detail") {
		t.Fatalf("contract-verify-published missing an expected key (spec 03 §11's 2026-08-29 amendment): %v", surfaces["contract-verify-published"])
	}

	if path := os.Getenv("RENDER_LEDGER_DUMP"); path != "" {
		data, err := json.MarshalIndent(surfaces, "", "  ")
		if err != nil {
			t.Fatalf("marshal render-ledger surface dump: %v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatalf("write render-ledger surface dump: %v", err)
		}
	}
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	sortedGot := append([]string(nil), got...)
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)
	return reflect.DeepEqual(sortedGot, sortedWant)
}

func containsAllStrings(haystack []string, wants ...string) bool {
	set := make(map[string]bool, len(haystack))
	for _, h := range haystack {
		set[h] = true
	}
	for _, w := range wants {
		if !set[w] {
			return false
		}
	}
	return true
}
