package cli_test

// cmd_contract_verify_published_test.go — answers-that-hold-2026-08 P7
// (spec 07-where-do-my-contracts-stand.md). Traces to ACs 1-7 (AC-8's own
// --json field-name handoff is documented in this phase's Deviations
// report; AC-9's roster assertion is LEAD-OWNED, run over
// cmd/a2a/mcp_parity_test.go).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)

// gitInitMirror turns dir into a minimal, HEAD-resolvable git repository —
// exactly what space.ResolveContractPublicationCandidateCommit needs to
// answer "is this mirror at least a real, synced clone" (this phase's own
// narrow discharge of AC-5, see its Deviations report). It never pushes or
// clones (contractReadDescriptor/os.ReadDir read the working tree directly,
// never a git object), so — unlike testkit/spacefixture — no bare origin is
// needed here; one local commit is enough.
func gitInitMirror(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("gitInitMirror: mkdir: %v", err)
	}
	run := func(args ...string) {
		cmd := exec.Command("git", gitfixture.Args(args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
			"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
		)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("gitInitMirror: git %v: %v\n%s", args, err, out.String())
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, ".seed"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("gitInitMirror: %v", err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")
}

// verifyPublishedInspectionFake is a per-Ref-keyed ContractInspectionOperations
// fake — p6InspectionFake (cmd_contract_p6_test.go) always returns the SAME
// result regardless of request, which cannot express "two contracts in one
// run, two different outcomes" (AC-1).
type verifyPublishedInspectionFake struct {
	results map[string]cli.ContractVerifyExportResult
	errs    map[string]error
	calls   []cli.ContractVerifyExportRequest
}

func (f *verifyPublishedInspectionFake) DiffContract(context.Context, cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
	return cli.ContractDiffResult{}, nil
}

func (f *verifyPublishedInspectionFake) VerifyContractExport(_ context.Context, request cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
	f.calls = append(f.calls, request)
	if err, ok := f.errs[request.Ref]; ok {
		return cli.ContractVerifyExportResult{}, err
	}
	return f.results[request.Ref], nil
}

func newVerifyPublishedContractCommand(t *testing.T, mirrorDir string, inspection cli.ContractInspectionOperations) *cli.ContractCommand {
	t.Helper()
	cmd := cli.NewContractCommand(nil, &fakeLifecycleFunnel{}, mirrorDir, "fixture-space", "axon", lifecycleManifest(), lifecycleHostConfig(), lifecycleActorResolver("agent", "bot"))
	cmd.SetP6Inspection(inspection)
	return cmd
}

// TestContractVerifyPublishedAggregatesRowsAndResolvesVersion is AC-1/AC-2:
// one row per provided contract, each carrying the version RESOLVED from
// the published descriptor — the invocation itself never names one.
func TestContractVerifyPublishedAggregatesRowsAndResolvesVersion(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitInitMirror(t, mirrorDir)
	writeContractDescriptor(t, mirrorDir, "alpha", "1.0.0")
	writeContractDescriptor(t, mirrorDir, "beta", "2.0.0")

	fake := &verifyPublishedInspectionFake{results: map[string]cli.ContractVerifyExportResult{
		"XC-axon-alpha@1.0.0": {ID: "XC-axon-alpha", Outcome: "matched"},
		"XC-axon-beta@2.0.0":  {ID: "XC-axon-beta", Outcome: "drifted"},
	}}
	cmd := newVerifyPublishedContractCommand(t, mirrorDir, fake)

	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"verify-published",
		"--local", "XC-axon-alpha=exports/alpha",
		"--local", "XC-axon-beta=exports/beta",
	}, stdio)
	// A drifted row exits 1 — the run itself is not refused (a REFUSAL is
	// reserved for an absent/stale mirror, ACs 4-5).
	if code != 1 {
		t.Fatalf("code = %d, want 1 (one drifted row); stdout=%s stderr=%s", code, out, errOut)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected exactly 2 VerifyContractExport calls, got %d: %+v", len(fake.calls), fake.calls)
	}
	// AC-2: the Ref built for each call carries the version RESOLVED from
	// the descriptor — never anything the invocation above named (it named
	// none at all).
	gotRefs := map[string]bool{}
	for _, call := range fake.calls {
		gotRefs[call.Ref] = true
	}
	for _, want := range []string{"XC-axon-alpha@1.0.0", "XC-axon-beta@2.0.0"} {
		if !gotRefs[want] {
			t.Errorf("expected a VerifyContractExport call with Ref %q, calls=%+v", want, fake.calls)
		}
	}
	if !strings.Contains(out.String(), "2 contracts published for axon") {
		t.Errorf("stdout missing denominator line: %s", out.String())
	}
	if !strings.Contains(out.String(), "XC-axon-alpha@1.0.0") || !strings.Contains(out.String(), "matched") {
		t.Errorf("stdout missing matched row: %s", out.String())
	}
	if !strings.Contains(out.String(), "XC-axon-beta@2.0.0") || !strings.Contains(out.String(), "drifted") {
		t.Errorf("stdout missing drifted row: %s", out.String())
	}

	// AC-8: --json carries the same rows.
	stdioJSON, outJSON, _ := newIO()
	codeJSON := cmd.Run(context.Background(), []string{
		"verify-published", "--json",
		"--local", "XC-axon-alpha=exports/alpha",
		"--local", "XC-axon-beta=exports/beta",
	}, stdioJSON)
	if codeJSON != 1 {
		t.Fatalf("json run code = %d, want 1", codeJSON)
	}
	var result cli.ContractVerifyPublishedResult
	if err := json.Unmarshal(outJSON.Bytes(), &result); err != nil {
		t.Fatalf("decode --json output: %v (%s)", err, outJSON.String())
	}
	if result.System != "axon" || result.Total != 2 || len(result.Rows) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestContractVerifyPublishedZeroRowsWarnsRatherThanRefuses is AC-3: a run
// examining zero contracts prints the denominator and exits 0 (a WARN,
// never a refusal — this verb is run by an agent mid-cycle, and "nothing
// published yet" is a state it cannot exit by any action).
func TestContractVerifyPublishedZeroRowsWarnsRatherThanRefuses(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitInitMirror(t, mirrorDir)
	// No axon/provides/* tree at all — the "moved path shape" case.

	cmd := newVerifyPublishedContractCommand(t, mirrorDir, &verifyPublishedInspectionFake{})
	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published"}, stdio)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (zero rows WARNS); stdout=%s stderr=%s", code, out, errOut)
	}
	if strings.TrimSpace(out.String()) != "0 contracts published for axon" {
		t.Fatalf("stdout = %q, want the exact denominator line", out.String())
	}
}

// TestContractVerifyPublishedNotPublishedYet: a descriptor with no
// recorded version is its own row status, never passed to
// VerifyContractExport (there is no version to compare against).
func TestContractVerifyPublishedNotPublishedYet(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitInitMirror(t, mirrorDir)
	writeContractDescriptor(t, mirrorDir, "draft", "")

	fake := &verifyPublishedInspectionFake{}
	cmd := newVerifyPublishedContractCommand(t, mirrorDir, fake)
	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published"}, stdio)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out, errOut)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected no VerifyContractExport call for an unpublished descriptor, got %+v", fake.calls)
	}
	if !strings.Contains(out.String(), "XC-axon-draft") || !strings.Contains(out.String(), "not-published-yet") {
		t.Fatalf("stdout missing not-published-yet row: %s", out.String())
	}
}

// TestContractVerifyPublishedNoLocalSubjectIsUnmeasured is AC-6's negative
// case: a provided, published contract with NO --local override is
// "unmeasured" (D9's shipped severity, never a silent skip and never an
// error) rather than defaulting onto `.a2a/staging` (untracked,
// publisher-rewritten — exactly what this phase exists to avoid).
func TestContractVerifyPublishedNoLocalSubjectIsUnmeasured(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitInitMirror(t, mirrorDir)
	writeContractDescriptor(t, mirrorDir, "alpha", "1.0.0")

	fake := &verifyPublishedInspectionFake{}
	cmd := newVerifyPublishedContractCommand(t, mirrorDir, fake)
	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published"}, stdio)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (unmeasured never fails a run by itself); stdout=%s stderr=%s", code, out, errOut)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected no VerifyContractExport call with no --local override, got %+v", fake.calls)
	}
	if !strings.Contains(out.String(), "unmeasured") || !strings.Contains(out.String(), "--local") {
		t.Fatalf("stdout missing an actionable unmeasured row: %s", out.String())
	}
}

// TestContractVerifyPublishedInspectionErrorIsUnmeasured: a comparison
// VerifyContractExport itself cannot complete (e.g. an unreadable local
// export) degrades that ONE row to unmeasured with the error as its
// detail — never a whole-run refusal (that is reserved for an absent/stale
// MIRROR, ACs 4-5, not a bad per-contract local subject).
func TestContractVerifyPublishedInspectionErrorIsUnmeasured(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitInitMirror(t, mirrorDir)
	writeContractDescriptor(t, mirrorDir, "alpha", "1.0.0")

	fake := &verifyPublishedInspectionFake{errs: map[string]error{
		"XC-axon-alpha@1.0.0": errors.New("boom: local export unreadable"),
	}}
	cmd := newVerifyPublishedContractCommand(t, mirrorDir, fake)
	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published", "--local", "XC-axon-alpha=exports/alpha"}, stdio)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out, errOut)
	}
	if !strings.Contains(out.String(), "unmeasured") || !strings.Contains(out.String(), "boom") {
		t.Fatalf("stdout missing the comparison's own error as detail: %s", out.String())
	}
}

// TestContractVerifyPublishedAbsentMirrorRefuses is AC-4: an absent mirror
// REFUSES the whole run and names `a2a sync` — never a zero-row pass.
func TestContractVerifyPublishedAbsentMirrorRefuses(t *testing.T) {
	t.Parallel()
	mirrorDir := filepath.Join(t.TempDir(), "never-synced")

	cmd := newVerifyPublishedContractCommand(t, mirrorDir, &verifyPublishedInspectionFake{})
	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published"}, stdio)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (refused); stdout=%s stderr=%s", code, out, errOut)
	}
	if !strings.Contains(errOut.String(), "a2a sync") {
		t.Fatalf("stderr missing the sync next-step: %s", errOut.String())
	}
}

// TestContractVerifyPublishedStaleMirrorRefuses is AC-5: a present but
// unresolvable-HEAD mirror (never actually synced into a real git
// checkout) refuses with the same shape as AC-4, naming `a2a sync`. This is
// this phase's own narrow discharge of "stale" — see its Deviations report
// for why no repo-wide sync-age oracle exists to reuse.
func TestContractVerifyPublishedStaleMirrorRefuses(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir() // exists, but is not a git repository at all

	cmd := newVerifyPublishedContractCommand(t, mirrorDir, &verifyPublishedInspectionFake{})
	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published"}, stdio)
	if code != 1 {
		t.Fatalf("code = %d, want 1 (refused); stdout=%s stderr=%s", code, out, errOut)
	}
	if !strings.Contains(errOut.String(), "a2a sync") {
		t.Fatalf("stderr missing the sync next-step: %s", errOut.String())
	}
}

// TestContractVerifyPublishedRejectsPositionalArgs: this verb aggregates
// every provided contract — it takes no id, no version, and no positional
// argument at all (US-2).
func TestContractVerifyPublishedRejectsPositionalArgs(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	gitInitMirror(t, mirrorDir)

	cmd := newVerifyPublishedContractCommand(t, mirrorDir, &verifyPublishedInspectionFake{})
	stdio, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"verify-published", "XC-axon-alpha"}, stdio)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error); stderr=%s", code, errOut)
	}
}

// TestContractVerifyPublishedTwoConnectedSpacesChecksBoth is AC-7, proven
// through ContractVerifyPublishedCommand's own constructor directly (spec
// 07 §11's 2026-08-28 amendment: this tier is IN-PROCESS in internal/cli,
// not the livee2e harness, which stands up exactly one space per run).
func TestContractVerifyPublishedTwoConnectedSpacesChecksBoth(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	projectConfigPath := filepath.Join(projectRoot, ".a2a", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgYAML := "system: axon\nspaces:\n" +
		"  - id: space-one\n    repo_url: https://example.invalid/org/space-one.git\n" +
		"  - id: space-two\n    repo_url: https://example.invalid/org/space-two.git\n"
	if err := os.WriteFile(projectConfigPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	machineConfigPath := filepath.Join(t.TempDir(), "machine.yaml") // deliberately absent

	// Mirror directories at EXACTLY the default location
	// space.ResolveMirrorLocation computes for each space id, so the
	// standalone command's own resolver (never overridden by this test)
	// finds real, HEAD-resolvable git repositories.
	mirrorOne := space.ResolveMirrorLocation(projectRoot, space.Ref{ID: "space-one"}, space.MachineConfig{})
	mirrorTwo := space.ResolveMirrorLocation(projectRoot, space.Ref{ID: "space-two"}, space.MachineConfig{})
	gitInitMirror(t, mirrorOne)
	gitInitMirror(t, mirrorTwo)
	writeContractDescriptor(t, mirrorOne, "alpha", "1.0.0")
	writeContractDescriptor(t, mirrorTwo, "gamma", "2.0.0")

	fakes := map[string]*verifyPublishedInspectionFake{
		"space-one": {results: map[string]cli.ContractVerifyExportResult{
			"XC-axon-alpha@1.0.0": {ID: "XC-axon-alpha", Outcome: "matched"},
		}},
		"space-two": {results: map[string]cli.ContractVerifyExportResult{
			"XC-axon-gamma@2.0.0": {ID: "XC-axon-gamma", Outcome: "matched"},
		}},
	}

	cmd := cli.NewContractVerifyPublishedCommand(projectConfigPath, machineConfigPath, projectRoot,
		func(ref space.Ref, _ string) (cli.ContractInspectionOperations, error) {
			fake, ok := fakes[ref.ID]
			if !ok {
				t.Fatalf("inspectorFor called for unexpected space %q", ref.ID)
			}
			return fake, nil
		},
	)

	stdio, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{
		"--local", "XC-axon-alpha=exports/alpha",
		"--local", "XC-axon-gamma=exports/gamma",
	}, stdio)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out, errOut)
	}
	if !strings.Contains(out.String(), "2 contracts published for axon") {
		t.Fatalf("stdout missing the two-space denominator: %s", out.String())
	}
	if !strings.Contains(out.String(), "[space-one]") || !strings.Contains(out.String(), "[space-two]") {
		t.Fatalf("stdout missing a row from one of the two connected spaces: %s", out.String())
	}
	if len(fakes["space-one"].calls) != 1 || len(fakes["space-two"].calls) != 1 {
		t.Fatalf("expected exactly one VerifyContractExport call per space, got space-one=%d space-two=%d",
			len(fakes["space-one"].calls), len(fakes["space-two"].calls))
	}
}
