package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// reviewedHostDeps is the write dependency set of a space on a host that
// reviews writes (the GitHub column): a publish judges its gate from the
// host's declaration, so a test that is not about gates names the common one.
func reviewedHostDeps() WriteDeps {
	return WriteDeps{HostCaps: &host.Capabilities{NeedsCredential: true, ReviewsWrites: true}}
}

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
	deps := ContractDeps{WriteDeps: reviewedHostDeps(), Publication: publication, Materialize: materialize, Check: check}

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
	deps := ContractDeps{WriteDeps: reviewedHostDeps(), Publication: publication}

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
	deps := ContractDeps{WriteDeps: reviewedHostDeps(), Publication: publication, Materialize: &mcpP6MaterializeFake{}, Check: &mcpP6CheckFake{}}
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

// gatedPublication is a publication fake whose plan sits behind gate.
func gatedPublication(gate contract.PublicationGate) *mcpP6PublicationFake {
	return &mcpP6PublicationFake{result: space.ContractPublicationResult{
		Status: space.ContractPublicationPlanned,
		Plan:   contract.PublicationPlan{Contract: "XC-axon-orders", TargetVersion: "1.0.0", Gate: gate},
	}}
}

// gateWriteDeps is the write dependency set of a space whose host declares caps
// and whose manifest carries gates.
func gateWriteDeps(caps *host.Capabilities, gates any) WriteDeps {
	deps := WriteDeps{HostCaps: caps}
	deps.Manifest.Gates = gates
	return deps
}

const publishGateArgs = `{"id":"XC-axon-orders","version":"1.0.0"}`
const publishGateArgsApproved = `{"id":"XC-axon-orders","version":"1.0.0","approve":true}`

// TestContractPublishGateApprovalByMode is spec 04 §T1 rows 52 and 89 for
// a2a_contract publish: the plan's own gate (the planner's, asked through a
// preflight of the same request) is judged against the space's effective mode.
// A confirm space refuses a gated publish without approve before anything is
// written, records the gate when approve was given, and a host that reviews
// writes or an autonomous space asks nothing, neither of them paying for the
// preflight whose answer they would not read.
func TestContractPublishGateApprovalByMode(t *testing.T) {
	t.Parallel()
	local := &host.Capabilities{ReviewsWrites: false}
	reviewed := &host.Capabilities{ReviewsWrites: true}
	const noEffect = "--approve has no effect here: this space's host reviews gated writes in a pull request"

	cases := []struct {
		name          string
		gate          contract.PublicationGate
		caps          *host.Capabilities
		gates         any
		args          string
		wantErr       string
		wantPreflight bool
		wantPublish   bool
		wantGates     []string
	}{
		{name: "G1 in a confirm space without approve is refused", gate: contract.GateG1, caps: local, args: publishGateArgs,
			wantErr: "POL-029: G1 requires --approve in a confirm space", wantPreflight: true},
		{name: "G2 in a confirm space without approve is refused", gate: contract.GateG2, caps: local, args: publishGateArgs,
			wantErr: "POL-029: G2 requires --approve in a confirm space", wantPreflight: true},
		{name: "G1 with approve is recorded", gate: contract.GateG1, caps: local, args: publishGateArgsApproved,
			wantPreflight: true, wantPublish: true, wantGates: []string{"G1"}},
		{name: "G2 with approve is recorded", gate: contract.GateG2, caps: local, args: publishGateArgsApproved,
			wantPreflight: true, wantPublish: true, wantGates: []string{"G2"}},
		{name: "no gate in a confirm space asks nothing", gate: contract.GateNone, caps: local, args: publishGateArgs,
			wantPreflight: true, wantPublish: true},
		{name: "no gate with approve records nothing", gate: contract.GateNone, caps: local, args: publishGateArgsApproved,
			wantPreflight: true, wantPublish: true},
		{name: "an empty gate asks nothing either", gate: "", caps: local, args: publishGateArgs,
			wantPreflight: true, wantPublish: true},
		{name: "an autonomous space publishes unrecorded and unpreflighted", gate: contract.GateG1, caps: local, gates: "autonomous",
			args: publishGateArgs, wantPublish: true},
		{name: "an autonomous space records nothing even with approve", gate: contract.GateG1, caps: local, gates: "autonomous",
			args: publishGateArgsApproved, wantPublish: true},
		{name: "a reviewed host publishes unpreflighted", gate: contract.GateG1, caps: reviewed,
			args: publishGateArgs, wantPublish: true},
		{name: "a reviewed host ignores autonomous", gate: contract.GateG1, caps: reviewed, gates: "autonomous",
			args: publishGateArgs, wantPublish: true},
		{name: "approve on a reviewed host is refused before any preflight", gate: contract.GateG1, caps: reviewed,
			args: publishGateArgsApproved, wantErr: noEffect},
		{name: "a session that never opened the host cannot judge", gate: contract.GateG1, caps: nil,
			args: publishGateArgs, wantErr: "gate mode cannot be judged"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			publication := gatedPublication(tc.gate)
			deps := ContractDeps{WriteDeps: gateWriteDeps(tc.caps, tc.gates), Publication: publication}
			_, _, err := newP6ContractPublishHandler(deps)(t.Context(), json.RawMessage(tc.args))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatalf("publish returned error: %v", err)
			}
			if got := publication.preflight.ID != ""; got != tc.wantPreflight {
				t.Fatalf("preflight called = %t, want %t", got, tc.wantPreflight)
			}
			if got := publication.publish.ID != ""; got != tc.wantPublish {
				t.Fatalf("publish called = %t, want %t (a refusal writes nothing)", got, tc.wantPublish)
			}
			if tc.wantPublish && !equalStrings(publication.publish.ApprovedGates, tc.wantGates) {
				t.Fatalf("ApprovedGates = %v, want %v", publication.publish.ApprovedGates, tc.wantGates)
			}
		})
	}
}

// TestContractPublishGateRefusalIsThreePartAndNamesTheGate pins the whole line.
func TestContractPublishGateRefusalIsThreePartAndNamesTheGate(t *testing.T) {
	t.Parallel()
	deps := ContractDeps{WriteDeps: gateWriteDeps(&host.Capabilities{}, nil), Publication: gatedPublication(contract.GateG1)}
	_, _, err := newP6ContractPublishHandler(deps)(t.Context(), json.RawMessage(publishGateArgs))
	want := "contract publish: POL-029: G1 requires --approve in a confirm space — re-run with approve: true if you, the operator, approve this write (G1)"
	if err == nil || err.Error() != want {
		t.Fatalf("refusal = %v\nwant     %s", err, want)
	}
}

// TestContractPublishGateJudgesTheTargetSpaceNotThePrimary: a multi-space
// session's WriteDeps describes its primary space, so the publish resolves the
// target first. A local confirm space is refused even though the primary is a
// reviewed one, and the reviewed one is untouched even though the other is not.
func TestContractPublishGateJudgesTheTargetSpaceNotThePrimary(t *testing.T) {
	t.Parallel()
	reviewedSpace := gateWriteDeps(&host.Capabilities{ReviewsWrites: true}, nil)
	localSpace := gateWriteDeps(&host.Capabilities{ReviewsWrites: false}, nil)
	primary := reviewedSpace
	primary.ResolveSpace = func(id string) (WriteDeps, error) {
		switch id {
		case "reviewed":
			return reviewedSpace, nil
		case "local":
			return localSpace, nil
		}
		return WriteDeps{}, errors.New("not connected: " + id)
	}

	local := gatedPublication(contract.GateG1)
	_, _, err := newP6ContractPublishHandler(ContractDeps{WriteDeps: primary, Publication: local})(t.Context(),
		json.RawMessage(`{"space":"local","id":"XC-axon-orders","version":"1.0.0"}`))
	if err == nil || !strings.Contains(err.Error(), "POL-029: G1 requires --approve in a confirm space") {
		t.Fatalf("a local target under a reviewed primary: error = %v, want POL-029", err)
	}
	if local.publish.ID != "" {
		t.Fatal("the refused publish still wrote")
	}

	reviewedPub := gatedPublication(contract.GateG1)
	if _, _, err := newP6ContractPublishHandler(ContractDeps{WriteDeps: primary, Publication: reviewedPub})(t.Context(),
		json.RawMessage(`{"space":"reviewed","id":"XC-axon-orders","version":"1.0.0"}`)); err != nil {
		t.Fatalf("a reviewed target: %v", err)
	}
	if reviewedPub.preflight.ID != "" {
		t.Fatal("a reviewed target paid for a preflight")
	}

	if _, _, err := newP6ContractPublishHandler(ContractDeps{WriteDeps: primary, Publication: gatedPublication(contract.GateG1)})(t.Context(),
		json.RawMessage(`{"space":"nowhere","id":"XC-axon-orders","version":"1.0.0"}`)); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("an unknown target: error = %v, want the resolver's refusal", err)
	}
}

// TestContractPublishGateStopsOnAFailedPreflight: when the plan cannot be
// computed the gate cannot be judged, so nothing is written.
func TestContractPublishGateStopsOnAFailedPreflight(t *testing.T) {
	t.Parallel()
	publication := &mcpP6PublicationFake{err: errors.New("mirror is stale")}
	deps := ContractDeps{WriteDeps: gateWriteDeps(&host.Capabilities{}, nil), Publication: publication}
	_, _, err := newP6ContractPublishHandler(deps)(t.Context(), json.RawMessage(publishGateArgsApproved))
	if err == nil || !strings.Contains(err.Error(), "mirror is stale") {
		t.Fatalf("error = %v, want the preflight's own", err)
	}
	if publication.publish.ID != "" {
		t.Fatal("a publish ran after its preflight failed")
	}
}

// TestContractPreflightRefusesApprove: preflight writes nothing, so an input
// that would read as an approval given is refused as an unknown field.
func TestContractPreflightRefusesApprove(t *testing.T) {
	t.Parallel()
	deps := ContractDeps{WriteDeps: reviewedHostDeps(), Publication: gatedPublication(contract.GateG1)}
	_, _, err := newContractPreflightHandler(deps)(t.Context(), json.RawMessage(`{"id":"XC-axon-orders","version":"1.0.0","approve":true}`))
	if err == nil || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error = %v, want a refusal naming approve", err)
	}
}
