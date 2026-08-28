package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type mcpP6PublicationFake struct {
	preflight ContractPublicationRequest
	publish   ContractPublicationRequest
	result    space.ContractPublicationResult
	err       error
}

func (f *mcpP6PublicationFake) Preflight(_ context.Context, request ContractPublicationRequest) (space.ContractPublicationResult, error) {
	f.preflight = request
	return f.result, f.err
}
func (f *mcpP6PublicationFake) Publish(_ context.Context, request ContractPublicationRequest) (space.ContractPublicationResult, error) {
	f.publish = request
	return f.result, f.err
}

type mcpP6MaterializeFake struct {
	request ContractMaterializeRequest
	result  space.ContractMaterializeResult
}

func (f *mcpP6MaterializeFake) MaterializeContract(_ context.Context, request ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	f.request = request
	return f.result, nil
}

type mcpP6CheckFake struct {
	request ContractCheckRequest
	result  contract.ConformanceResult
}

type mcpP6InspectionFake struct {
	diffRequest   ContractDiffRequest
	verifyRequest ContractVerifyExportRequest
	diffResult    ContractDiffResult
	verifyResult  ContractVerifyExportResult
}

func (f *mcpP6InspectionFake) DiffContract(_ context.Context, request ContractDiffRequest) (ContractDiffResult, error) {
	f.diffRequest = request
	return f.diffResult, nil
}

func (f *mcpP6InspectionFake) VerifyContractExport(_ context.Context, request ContractVerifyExportRequest) (ContractVerifyExportResult, error) {
	f.verifyRequest = request
	return f.verifyResult, nil
}

func (f *mcpP6CheckFake) CheckContract(_ context.Context, request ContractCheckRequest) (contract.ConformanceResult, error) {
	f.request = request
	return f.result, nil
}

func TestContractP6MCPActionsUseSharedResults(t *testing.T) {
	t.Parallel()
	publication := &mcpP6PublicationFake{result: space.ContractPublicationResult{
		Status: space.ContractPublicationPlanned,
		Plan:   contract.PublicationPlan{Contract: "XC-axon-orders", TargetVersion: "1.1.0", PlanDigest: "sha256:plan"},
	}}
	materialize := &mcpP6MaterializeFake{result: space.ContractMaterializeResult{ContractID: "XC-axon-orders", Version: "1.0.0", Destination: "vendor/orders", Outcome: space.ContractAlreadyPresent}}
	check := &mcpP6CheckFake{result: contract.ConformanceResult{Ref: "XC-axon-orders@1.0.0", Mode: contract.ConformanceModeSuite, Supported: true, Passed: true, Outcome: contract.ConformanceSuiteConsistent, Results: []contract.ConformanceCaseResult{}}}
	deps := ContractDeps{Publication: publication, Materialize: materialize, Check: check}

	result, _, err := newContractPreflightHandler(deps)(t.Context(), json.RawMessage(`{"space":"orders","id":"XC-axon-orders","bump":"minor","staging":"candidate"}`))
	if err != nil || result.(space.ContractPublicationResult).Plan.PlanDigest != "sha256:plan" || publication.preflight.Staging != "candidate" || publication.preflight.Space != "orders" {
		t.Fatalf("preflight mismatch: result=%+v request=%+v err=%v", result, publication.preflight, err)
	}
	result, _, err = newP6ContractPublishHandler(deps)(t.Context(), json.RawMessage(`{"space":"orders","id":"XC-axon-orders","version":"1.1.0","expect_plan":"sha256:plan","staging":"candidate"}`))
	if err != nil || publication.publish.ExpectPlan != "sha256:plan" || publication.publish.Staging != "candidate" || publication.publish.Space != "orders" {
		t.Fatalf("publish mismatch: result=%+v request=%+v err=%v", result, publication.publish, err)
	}
	result, _, err = newContractMaterializeHandler(deps)(t.Context(), json.RawMessage(`{"space":"orders","ref":"XC-axon-orders@1.0.0","to":"vendor/orders"}`))
	if err != nil || result.(space.ContractMaterializeResult).Outcome != space.ContractAlreadyPresent || materialize.request.Destination != "vendor/orders" || materialize.request.Space != "orders" {
		t.Fatalf("materialize mismatch: result=%+v request=%+v err=%v", result, materialize.request, err)
	}
	result, _, err = newContractCheckHandler(deps)(t.Context(), json.RawMessage(`{"space":"orders","ref":"XC-axon-orders@1.0.0","mode":"suite"}`))
	if err != nil || result.(contract.ConformanceResult).Outcome != contract.ConformanceSuiteConsistent || !check.request.Suite || check.request.Space != "orders" {
		t.Fatalf("check mismatch: result=%+v request=%+v err=%v", result, check.request, err)
	}
}

// TestContractP6MCPAllowEmptyBumpThreads covers AC-4's MCP half: an agent
// that can only reach MCP must be able to satisfy the empty-bump refusal
// too (spec T1: "not a refusal it has no way to satisfy"). Both
// ContractPreflightInput (this package) and ContractPublishInput
// (tools_contract.go, decoded by newP6ContractPublishHandler which lives
// here) must decode `allow_empty_bump` and thread it into
// ContractPublicationRequest.AllowEmptyBump.
func TestContractP6MCPAllowEmptyBumpThreads(t *testing.T) {
	t.Parallel()
	publication := &mcpP6PublicationFake{result: space.ContractPublicationResult{
		Status: space.ContractPublicationPlanned,
		Plan:   contract.PublicationPlan{Contract: "XC-axon-orders", TargetVersion: "1.1.0"},
	}}
	deps := ContractDeps{Publication: publication}

	if _, _, err := newContractPreflightHandler(deps)(t.Context(), json.RawMessage(`{"id":"XC-axon-orders","bump":"minor","allow_empty_bump":true}`)); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !publication.preflight.AllowEmptyBump {
		t.Fatalf("preflight request = %+v, want AllowEmptyBump=true", publication.preflight)
	}

	if _, _, err := newP6ContractPublishHandler(deps)(t.Context(), json.RawMessage(`{"id":"XC-axon-orders","version":"1.1.0","allow_empty_bump":true}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !publication.publish.AllowEmptyBump {
		t.Fatalf("publish request = %+v, want AllowEmptyBump=true", publication.publish)
	}
}

func TestContractP6MCPMalformedAndPlanConflict(t *testing.T) {
	t.Parallel()
	publication := &mcpP6PublicationFake{err: space.ErrContractPublicationPlanChanged}
	deps := ContractDeps{Publication: publication, Materialize: &mcpP6MaterializeFake{}, Check: &mcpP6CheckFake{}}
	for name, test := range map[string]struct {
		handler HandlerFunc
		raw     string
	}{
		"both selectors": {newContractPreflightHandler(deps), `{"id":"XC-axon-orders","version":"1.0.0","bump":"patch"}`},
		"bad bump":       {newContractPreflightHandler(deps), `{"id":"XC-axon-orders","bump":"sideways"}`},
		"bad ref":        {newContractMaterializeHandler(deps), `{"ref":"XC-axon-orders","to":"vendor/orders"}`},
		"bad check mode": {newContractCheckHandler(deps), `{"ref":"XC-axon-orders@1.0.0","mode":"suite","payload":"payload.json"}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := test.handler(t.Context(), json.RawMessage(test.raw)); err == nil {
				t.Fatal("expected malformed input error")
			}
		})
	}
	_, _, err := newP6ContractPublishHandler(deps)(t.Context(), json.RawMessage(`{"id":"XC-axon-orders","version":"1.0.0","expect_plan":"sha256:old"}`))
	if err == nil || !strings.Contains(err.Error(), space.ErrContractPublicationPlanChanged.Error()) || !errors.Is(publication.err, space.ErrContractPublicationPlanChanged) {
		t.Fatalf("plan conflict mismatch: %v", err)
	}
}

func TestContractP6MCPInspectionDelegates(t *testing.T) {
	t.Parallel()
	inspection := &mcpP6InspectionFake{
		diffResult:   ContractDiffResult{Added: []string{"schema/new.json"}, Removed: []string{}, Changed: []string{}},
		verifyResult: ContractVerifyExportResult{ID: "XC-axon-orders", Outcome: "matched", LocalDigest: "sha256:same", WantDigest: "sha256:same"},
	}
	deps := ContractDeps{Inspection: inspection}
	result, _, err := newContractDiffHandler(deps)(t.Context(), json.RawMessage(`{"space":"orders","id":"XC-axon-orders","v1":"1.0.0","v2":"2.0.0"}`))
	if err != nil || inspection.diffRequest.V2 != "2.0.0" || inspection.diffRequest.Space != "orders" || result.(ContractDiffResult).Added[0] != "schema/new.json" {
		t.Fatalf("diff mismatch: result=%+v request=%+v err=%v", result, inspection.diffRequest, err)
	}
	result, _, err = newContractVerifyExportHandler(deps)(t.Context(), json.RawMessage(`{"space":"orders","local":"exports/orders","ref":"XC-axon-orders@1.0.0"}`))
	if err != nil || inspection.verifyRequest.Local != "exports/orders" || inspection.verifyRequest.Space != "orders" || result.(ContractVerifyExportResult).Outcome != "matched" {
		t.Fatalf("verify mismatch: result=%+v request=%+v err=%v", result, inspection.verifyRequest, err)
	}
}
