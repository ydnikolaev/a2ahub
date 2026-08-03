package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
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
