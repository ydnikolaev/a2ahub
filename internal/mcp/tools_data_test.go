package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

type fakeMCPDataBackend struct {
	calls        []string
	packReq      DataPackRequest
	deliverReq   DataDeliverRequest
	fetchReq     DataFetchRequest
	verifyReq    DataVerifyRequest
	fetchBlobReq DataFetchBlobRequest
	result       DataResult
	fetchBlobRes DataFetchBlobResult
	err          error
}

func (f *fakeMCPDataBackend) Pack(_ context.Context, req DataPackRequest) (DataResult, error) {
	f.calls = append(f.calls, "pack")
	f.packReq = req
	return f.result, f.err
}

func (f *fakeMCPDataBackend) Deliver(_ context.Context, req DataDeliverRequest) (DataResult, error) {
	f.calls = append(f.calls, "deliver")
	f.deliverReq = req
	return f.result, f.err
}

func (f *fakeMCPDataBackend) Fetch(_ context.Context, req DataFetchRequest) (DataResult, error) {
	f.calls = append(f.calls, "fetch")
	f.fetchReq = req
	return f.result, f.err
}

func (f *fakeMCPDataBackend) Verify(_ context.Context, req DataVerifyRequest) (DataResult, error) {
	f.calls = append(f.calls, "verify")
	f.verifyReq = req
	return f.result, f.err
}

// FetchBlob implements DataFetchBlobOperations — fakeMCPDataBackend
// satisfies this optional capability too, so tests exercising action=
// fetch-blob's happy path can reuse the same fake every other action does.
func (f *fakeMCPDataBackend) FetchBlob(_ context.Context, req DataFetchBlobRequest) (DataFetchBlobResult, error) {
	f.calls = append(f.calls, "fetch-blob")
	f.fetchBlobReq = req
	if f.err != nil {
		return DataFetchBlobResult{}, f.err
	}
	return f.fetchBlobRes, nil
}

var _ DataOperations = (*fakeMCPDataBackend)(nil)
var _ DataFetchBlobOperations = (*fakeMCPDataBackend)(nil)

// fakeMCPDataBackendNoFetchBlob implements ONLY DataOperations, with NO
// FetchBlob method at the type level at all — used to prove action=
// fetch-blob degrades to "not configured" when the concrete ops value has
// not (yet) grown that capability, mirroring DataAttachOperations' own
// optional-capability discipline (tools_data.go's doc comment). A type
// embedding fakeMCPDataBackend would inherit ITS FetchBlob method and
// satisfy DataFetchBlobOperations anyway, defeating the point — so this is
// a deliberately separate, minimal type instead.
type fakeMCPDataBackendNoFetchBlob struct{ result DataResult }

func (f *fakeMCPDataBackendNoFetchBlob) Pack(context.Context, DataPackRequest) (DataResult, error) {
	return f.result, nil
}
func (f *fakeMCPDataBackendNoFetchBlob) Deliver(context.Context, DataDeliverRequest) (DataResult, error) {
	return f.result, nil
}
func (f *fakeMCPDataBackendNoFetchBlob) Fetch(context.Context, DataFetchRequest) (DataResult, error) {
	return f.result, nil
}
func (f *fakeMCPDataBackendNoFetchBlob) Verify(context.Context, DataVerifyRequest) (DataResult, error) {
	return f.result, nil
}

var _ DataOperations = (*fakeMCPDataBackendNoFetchBlob)(nil)

func testDataTool(t *testing.T, backend *fakeMCPDataBackend) ToolSpec {
	t.Helper()
	spec, err := NewDataTool(DataToolDeps{Operations: backend})
	if err != nil {
		t.Fatalf("NewDataTool: %v", err)
	}
	return spec
}

func callDataTool(t *testing.T, spec ToolSpec, input any) (any, string, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, body, err := spec.Handler(context.Background(), raw)
	return result, body, err
}

func sampleMCPManifest() *datapackage.Document {
	records := int64(42)
	return &datapackage.Document{
		Schema: "data-package/v1", ID: "DP-axon-20260804-ab12", Thread: "thread-1",
		Contract: "XC-axon-orders@1.0.0#sha256:contractdigest", Mode: datapackage.ModeSnapshot,
		Classification: "internal", DataProfile: datapackage.DataProfileSynthetic, Format: datapackage.FormatJSON,
		TransportDriver: datapackage.TransportDriverSpaceGit, Locator: "deliveries/DP-axon-20260804-ab12",
		Entries: []datapackage.Entry{
			{Path: "orders.json", Role: datapackage.RoleDataset, MediaType: "application/json", ConformsTo: "schema/orders.json", SizeBytes: 1024, Digest: "sha256:entrydigest", RecordCount: &records},
		},
		AggregateDigest: "sha256:aggregatedigest", SizeBytes: 1024, RecordCount: 42,
		CreatedAt: "2026-08-04T00:00:00Z", ExpiresAt: "2026-08-11T00:00:00Z", Attempt: 1,
		Provenance: datapackage.Provenance{OriginSystem: "axon", ExtractedAt: "2026-08-04T00:00:00Z"},
	}
}

func TestDataToolRoutesEveryClosedAction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input DataInput
		want  string
	}{
		{"pack", DataInput{Action: "pack", Contract: "XC-axon-orders@1.0.0", From: "staging/orders", Profile: "synthetic", Format: "json", Expires: "168h"}, "pack"},
		{"deliver", DataInput{Action: "deliver", StagingRoot: "staging/attempt-1", Fulfills: "XW-axon-20260804-ef56"}, "deliver"},
		{"fetch", DataInput{Action: "fetch", PackageID: "DP-axon-20260804-ab12", To: "vendor/orders"}, "fetch"},
		{"verify", DataInput{Action: "verify", PackageID: "DP-axon-20260804-ab12"}, "verify"},
		{"fetch-blob", DataInput{Action: "fetch-blob", Ref: "BL-axon-20260811-ab12", To: "vendor/blob"}, "fetch-blob"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeMCPDataBackend{result: DataResult{Manifest: sampleMCPManifest()}}
			if _, _, err := callDataTool(t, testDataTool(t, backend), tc.input); err != nil {
				t.Fatalf("handler error: %v; calls=%v", err, backend.calls)
			}
			if len(backend.calls) != 1 || backend.calls[0] != tc.want {
				t.Fatalf("calls=%v want=[%s]", backend.calls, tc.want)
			}
		})
	}
}

func TestDataToolPackForwardsFieldsAndExpiresDuration(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{result: DataResult{Manifest: sampleMCPManifest()}}
	spec := testDataTool(t, backend)
	_, body, err := callDataTool(t, spec, DataInput{
		Action: "pack", Space: "fixture-space", Contract: "XC-axon-orders@1.0.0", From: "staging/orders",
		Profile: "synthetic", Format: "json", Expires: "168h", Fulfills: "XW-axon-20260804-ef56",
		Supersedes: "DP-axon-20260803-zz99", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	want := DataPackRequest{
		Space: "fixture-space", ContractRef: "XC-axon-orders@1.0.0", From: "staging/orders", Profile: "synthetic",
		Format: "json", Expires: 168 * time.Hour, Fulfills: "XW-axon-20260804-ef56",
		Supersedes: "DP-axon-20260803-zz99", MaxAttempts: 3,
	}
	if backend.packReq != want {
		t.Fatalf("pack request mismatch: got=%+v want=%+v", backend.packReq, want)
	}
	if !strings.Contains(body, "DP-axon-20260804-ab12") {
		t.Fatalf("pack summary missing package id: %q", body)
	}
}

func TestDataToolRequiresAction(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{}
	spec := testDataTool(t, backend)
	if _, _, err := callDataTool(t, spec, map[string]any{}); err == nil || len(backend.calls) != 0 {
		t.Fatalf("expected a required-action error, got err=%v calls=%v", err, backend.calls)
	}
	if _, _, err := callDataTool(t, spec, map[string]any{"action": "teleport"}); err == nil {
		t.Fatal("expected an unknown-action error")
	}
}

// TestDataToolClosedActionsRejectOtherActionsFields proves the closed
// per-action field discipline tools_work.go already ships: a field that
// belongs to a DIFFERENT action is forbidden, never silently ignored.
func TestDataToolClosedActionsRejectOtherActionsFields(t *testing.T) {
	t.Parallel()
	for _, input := range []DataInput{
		{Action: "pack", Contract: "XC-a@1.0.0", From: "d", Profile: "synthetic", Format: "json", PackageID: "DP-a-20260804-aaaa"},
		{Action: "deliver", StagingRoot: "s", Fulfills: "XW-a-20260804-aaaa", To: "d"},
		{Action: "fetch", PackageID: "DP-a-20260804-aaaa", To: "d", Record: true},
		{Action: "verify", PackageID: "DP-a-20260804-aaaa", To: "d"},
		{Action: "fetch-blob", Ref: "BL-a-20260811-aaaa", To: "d", Record: true},
	} {
		backend := &fakeMCPDataBackend{}
		if _, _, err := callDataTool(t, testDataTool(t, backend), input); err == nil || len(backend.calls) != 0 {
			t.Fatalf("cross-action field accepted: input=%+v calls=%v err=%v", input, backend.calls, err)
		}
	}
}

func TestDataToolRequiredFieldsPerAction(t *testing.T) {
	t.Parallel()
	for _, input := range []DataInput{
		{Action: "pack"},
		{Action: "pack", Contract: "XC-a@1.0.0"},
		{Action: "deliver"},
		{Action: "deliver", StagingRoot: "s"},
		{Action: "fetch"},
		{Action: "fetch", PackageID: "DP-a-20260804-aaaa"},
		{Action: "verify"},
		{Action: "fetch-blob"},
		{Action: "fetch-blob", Ref: "BL-a-20260811-aaaa"},
	} {
		backend := &fakeMCPDataBackend{}
		if _, _, err := callDataTool(t, testDataTool(t, backend), input); err == nil || len(backend.calls) != 0 {
			t.Fatalf("missing-field input accepted: input=%+v calls=%v err=%v", input, backend.calls, err)
		}
	}
}

func TestDataToolUnavailableOperations(t *testing.T) {
	t.Parallel()
	spec, err := NewDataTool(DataToolDeps{})
	if err != nil {
		t.Fatalf("NewDataTool: %v", err)
	}
	if _, _, err := callDataTool(t, spec, DataInput{Action: "pack", Contract: "XC-a@1.0.0", From: "d", Profile: "synthetic", Format: "json"}); err == nil ||
		!strings.Contains(err.Error(), "service is not configured") {
		t.Fatalf("got err=%v, want a 'service is not configured' refusal", err)
	}
}

// TestDataToolRefusalNamesValueAndPathInBothModes proves the MCP surface —
// like the CLI's own equivalent test — carries the core's own aimed
// message (value, path, ndjson record) through unmodified: the handler's
// non-nil error becomes an isError:true tools/call result whose message IS
// this text (registry.go's own HandlerFunc doc comment), and the same
// error also names the failing entry in the tool's structured result via
// dataSummary being empty on failure (result is always the zero DataResult
// on a Go-level handler error, so a caller reads the message, not a
// half-populated result).
func TestDataToolRefusalNamesValueAndPathInBothModes(t *testing.T) {
	t.Parallel()
	record := int64(4108)
	refusal := fmt.Errorf("datapackage: Pack: %w: entry %q (check %q, status fail) record %d: value is not an integer",
		datapackage.ErrEntryNonConforming, "orders.ndjson", "schema-check", record)
	backend := &fakeMCPDataBackend{err: refusal}
	spec := testDataTool(t, backend)
	_, _, err := callDataTool(t, spec, DataInput{Action: "pack", Contract: "XC-a@1.0.0", From: "d", Profile: "synthetic", Format: "ndjson"})
	if err == nil || !strings.Contains(err.Error(), "orders.ndjson") || !strings.Contains(err.Error(), "record 4108") {
		t.Fatalf("refusal missing value/path/record: %v", err)
	}
}

func TestDataToolDeliverAndFetchCarryWriteAndOutcome(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{result: DataResult{
		Manifest: sampleMCPManifest(), HandoffID: "XH-axon-20260804-cd34",
		Write: &space.WriteResult{Branch: "a2a/data-deliver", PRURL: "https://github.com/axon/space/pull/9", State: space.WriteStatePendingMerge},
	}}
	spec := testDataTool(t, backend)
	result, body, err := callDataTool(t, spec, DataInput{Action: "deliver", StagingRoot: "s", Fulfills: "XW-a-20260804-aaaa"})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	got, ok := result.(DataResult)
	if !ok || got.HandoffID != "XH-axon-20260804-cd34" || got.Write == nil || got.Write.PRURL != "https://github.com/axon/space/pull/9" {
		t.Fatalf("deliver result mismatch: %#v", result)
	}
	if !strings.Contains(body, "XH-axon-20260804-cd34") {
		t.Fatalf("deliver summary missing handoff id: %q", body)
	}

	backend = &fakeMCPDataBackend{result: DataResult{Manifest: sampleMCPManifest(), Outcome: "already-present"}}
	spec = testDataTool(t, backend)
	result, body, err = callDataTool(t, spec, DataInput{Action: "fetch", PackageID: "DP-axon-20260804-ab12", To: "vendor/orders"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got, ok = result.(DataResult)
	if !ok || got.Outcome != "already-present" {
		t.Fatalf("fetch result mismatch: %#v", result)
	}
	if !strings.Contains(body, "already-present") {
		t.Fatalf("fetch summary missing outcome: %q", body)
	}
}

// TestDataToolVerifyForwardsRecordAndActor is this wave's replacement for
// the old "Pass" assertion (spec 05a's own design invariant, cmd_data.go's
// doc comment: --record's write DIRECTION is derived from the report's own
// result by the core, so a forged pass must be UNREPRESENTABLE, not merely
// refused). It proves `record` — not a caller-declared verdict — is what
// reaches the backend, alongside the actor.
func TestDataToolVerifyForwardsRecordAndActor(t *testing.T) {
	t.Parallel()
	record := int64(4108)
	report, err := datapackage.NewReport(
		"VR-axon-20260804-gh78", "thread-1",
		datapackage.PackageRef{ID: "DP-axon-20260804-ab12", AggregateDigest: "sha256:aggregatedigest"},
		"XC-axon-orders@1.0.0#sha256:contractdigest", "beta",
		[]datapackage.Check{datapackage.NewCheck("schema-check", "orders.ndjson", datapackage.CheckFail, []datapackage.InstanceViolation{
			{InstancePointer: "/amount", SchemaPointer: "/properties/amount/type", Message: "value is not an integer", Record: &record},
		})},
		datapackage.Observed{RecordCount: 41, SizeBytes: 1024, DurationMS: 12},
		"2026-08-04T01:00:00Z", "2026-08-04T01:00:01Z",
	)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeMCPDataBackend{result: DataResult{Report: &report}}
	spec := testDataTool(t, backend)
	_, body, callErr := callDataTool(t, spec, DataInput{
		Action: "verify", PackageID: "DP-axon-20260804-ab12", Record: true,
		Actor: ActorInput{Kind: "agent", Name: "beta-bot"},
	})
	if callErr != nil {
		t.Fatalf("verify: %v", callErr)
	}
	if !backend.verifyReq.Record || backend.verifyReq.Actor.Name != "beta-bot" {
		t.Fatalf("verify request not forwarded: %+v", backend.verifyReq)
	}
	if !strings.Contains(body, "fail") {
		t.Fatalf("verify summary missing verdict: %q", body)
	}
}

// TestDataToolVerifyHasNoPassField is AC-5/D-12's negative half on the MCP
// surface: DataInput carries no Pass field at all, so a caller-declared
// verdict is unrepresentable in the wire schema, not merely refused at
// runtime. decodeDataInput's DisallowUnknownFields (tools_data.go) turns a
// raw `"pass"` key into a hard decode error rather than silently accepting
// or ignoring it — proving there is no input, forged or otherwise, that can
// choose verify's transition direction through this tool.
func TestDataToolVerifyHasNoPassField(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{}
	spec := testDataTool(t, backend)
	raw := json.RawMessage(`{"action":"verify","package_id":"DP-axon-20260804-ab12","pass":true}`)
	if _, _, err := spec.Handler(context.Background(), raw); err == nil || len(backend.calls) != 0 {
		t.Fatalf("a raw \"pass\" field must be rejected (unknown field), got err=%v calls=%v", err, backend.calls)
	}
}

// TestDataToolFetchBlobForwardsRequestAndSummarizesOutcome is P10
// (agent-exchange-2026-08) spec 10 wave B's own MCP-twin proof: action=
// fetch-blob forwards space/ref/to to the backend unmodified and the
// summary names the outcome, ref and destination — mirroring
// TestDataToolDeliverAndFetchCarryWriteAndOutcome's own shape for the
// existing `fetch` action.
func TestDataToolFetchBlobForwardsRequestAndSummarizesOutcome(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{fetchBlobRes: DataFetchBlobResult{
		Ref: "BL-axon-20260811-ab12", Destination: "vendor/blob", Outcome: "installed",
	}}
	spec := testDataTool(t, backend)
	result, body, err := callDataTool(t, spec, DataInput{
		Action: "fetch-blob", Space: "fixture-space", Ref: "BL-axon-20260811-ab12", To: "vendor/blob",
	})
	if err != nil {
		t.Fatalf("fetch-blob: %v", err)
	}
	if backend.fetchBlobReq.Space != "fixture-space" || backend.fetchBlobReq.Ref != "BL-axon-20260811-ab12" || backend.fetchBlobReq.Destination != "vendor/blob" {
		t.Fatalf("fetch-blob request not forwarded: %+v", backend.fetchBlobReq)
	}
	got, ok := result.(DataFetchBlobResult)
	if !ok || got.Outcome != "installed" {
		t.Fatalf("fetch-blob result mismatch: %#v", result)
	}
	if !strings.Contains(body, "installed") || !strings.Contains(body, "BL-axon-20260811-ab12") || !strings.Contains(body, "vendor/blob") {
		t.Fatalf("fetch-blob summary missing outcome/ref/destination: %q", body)
	}
}

// TestDataToolFetchBlobDegradesWhenBackendDoesNotImplementIt proves the
// optional-capability discipline DataFetchBlobOperations documents
// (tools_data.go): a DataOperations implementation that has not (yet) grown
// FetchBlob refuses with "not configured" rather than a type-assertion
// panic.
func TestDataToolFetchBlobDegradesWhenBackendDoesNotImplementIt(t *testing.T) {
	t.Parallel()
	spec, err := NewDataTool(DataToolDeps{Operations: &fakeMCPDataBackendNoFetchBlob{}})
	if err != nil {
		t.Fatalf("NewDataTool: %v", err)
	}
	_, _, callErr := callDataTool(t, spec, DataInput{Action: "fetch-blob", Ref: "BL-axon-20260811-ab12", To: "vendor/blob"})
	if callErr == nil || !strings.Contains(callErr.Error(), "not configured") {
		t.Fatalf("got err=%v, want a 'service is not configured' refusal", callErr)
	}
}

// TestDataToolFetchBlobRefusalIsForwarded proves a backend error surfaces
// through the handler unmodified, mirroring
// TestDataToolRefusalNamesValueAndPathInBothModes' own shape.
func TestDataToolFetchBlobRefusalIsForwarded(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{err: fmt.Errorf("space: blob resolves to bytes that do not match its own committed digest sidecar")}
	spec := testDataTool(t, backend)
	_, _, callErr := callDataTool(t, spec, DataInput{Action: "fetch-blob", Ref: "BL-axon-20260811-ab12", To: "vendor/blob"})
	if callErr == nil || !strings.Contains(callErr.Error(), "digest sidecar") {
		t.Fatalf("fetch-blob refusal not forwarded: %v", callErr)
	}
}

// fakeMCPExpressBackend adds the composite capability to the data fake.
type fakeMCPExpressBackend struct {
	fakeMCPDataBackend
	requestReq    DataRequestRequest
	deliverForReq DataDeliverForRequest
	verifyForReq  DataVerifyForRequest
	express       DataExpressResult
}

func (f *fakeMCPExpressBackend) Request(_ context.Context, req DataRequestRequest) (DataExpressResult, error) {
	f.calls = append(f.calls, "request")
	f.requestReq = req
	return f.express, f.err
}

func (f *fakeMCPExpressBackend) DeliverFor(_ context.Context, req DataDeliverForRequest) (DataExpressResult, error) {
	f.calls = append(f.calls, "deliver-for")
	f.deliverForReq = req
	return f.express, f.err
}

func (f *fakeMCPExpressBackend) VerifyFor(_ context.Context, req DataVerifyForRequest) (DataExpressResult, error) {
	f.calls = append(f.calls, "verify-for")
	f.verifyForReq = req
	return f.express, f.err
}

var _ DataExpressOperations = (*fakeMCPExpressBackend)(nil)

func testExpressDataTool(t *testing.T, backend *fakeMCPExpressBackend) ToolSpec {
	t.Helper()
	spec, err := NewDataTool(DataToolDeps{Operations: backend})
	if err != nil {
		t.Fatalf("NewDataTool: %v", err)
	}
	return spec
}

// TestDataToolRoutesExpressActions is spec 05 §8 row 14, routing half:
// action=request and the `for` form of deliver and verify reach the composite
// capability with every field carried, and never the single-step operations.
func TestDataToolRoutesExpressActions(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPExpressBackend{express: DataExpressResult{WorkRequest: "XW-axon-20260804-aaaa", Next: "run deliver"}}
	spec := testExpressDataTool(t, backend)
	actor := ActorInput{Kind: "agent", Name: "mcp-caller", Model: "m"}

	_, body, err := callDataTool(t, spec, DataInput{
		Action: "request", Space: "fixture-space", Producer: "beta", Contract: "XC-axon-orders@1.0.0",
		InterimBehavior: "wait", Title: "Orders", NeededBy: "2026-10-30", AcceptanceCriteria: []string{"extra"},
		Timeout: "30s", Actor: actor,
	})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if !strings.Contains(body, "next: run deliver") {
		t.Fatalf("request summary lacks the next command: %q", body)
	}
	gotReq := backend.requestReq
	if gotReq.Space != "fixture-space" || gotReq.Producer != "beta" || gotReq.Contract != "XC-axon-orders@1.0.0" ||
		gotReq.InterimBehavior != "wait" || gotReq.Title != "Orders" || gotReq.NeededBy != "2026-10-30" ||
		len(gotReq.AcceptanceCriteria) != 1 || gotReq.Timeout != 30*time.Second || gotReq.Actor != actor {
		t.Fatalf("request not carried: %+v", gotReq)
	}

	if _, _, err := callDataTool(t, spec, DataInput{
		Action: "deliver", For: "XW-axon-20260804-aaaa", From: "d", Profile: "synthetic", Format: "json",
		Contract: "XC-axon-orders@1.0.0", MaxAttempts: 2, Expires: "48h", Actor: actor,
	}); err != nil {
		t.Fatalf("deliver for: %v", err)
	}
	gotDel := backend.deliverForReq
	if gotDel.For != "XW-axon-20260804-aaaa" || gotDel.From != "d" || gotDel.Profile != "synthetic" || gotDel.Format != "json" ||
		gotDel.Contract != "XC-axon-orders@1.0.0" || gotDel.MaxAttempts != 2 || gotDel.Expires != 48*time.Hour || gotDel.Actor != actor {
		t.Fatalf("deliver for not carried: %+v", gotDel)
	}

	if _, _, err := callDataTool(t, spec, DataInput{
		Action: "verify", For: "XW-axon-20260804-aaaa", Record: true, To: "vendor", Verdicts: []string{"ac2:met:beta"}, Reason: "r", Actor: actor,
	}); err != nil {
		t.Fatalf("verify for: %v", err)
	}
	gotVer := backend.verifyForReq
	if gotVer.For != "XW-axon-20260804-aaaa" || !gotVer.Record || gotVer.To != "vendor" || len(gotVer.Verdicts) != 1 || gotVer.Reason != "r" || gotVer.Actor != actor {
		t.Fatalf("verify for not carried: %+v", gotVer)
	}

	if want := []string{"request", "deliver-for", "verify-for"}; strings.Join(backend.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v (and none of the single-step operations)", backend.calls, want)
	}
}

// TestDataToolExpressFieldDiscipline is spec 05 §8 row 14, refusal half: `for`
// is exclusive with the fields it derives, request takes none of the
// deliver/verify fields, and the express-only fields are refused on every
// other action rather than ignored.
func TestDataToolExpressFieldDiscipline(t *testing.T) {
	t.Parallel()
	const wr = "XW-axon-20260804-aaaa"
	for name, input := range map[string]DataInput{
		"deliver for + staging_root":     {Action: "deliver", For: wr, From: "d", Profile: "synthetic", Format: "json", StagingRoot: "s"},
		"deliver for + fulfills":         {Action: "deliver", For: wr, From: "d", Profile: "synthetic", Format: "json", Fulfills: wr},
		"deliver for + supersedes":       {Action: "deliver", For: wr, From: "d", Profile: "synthetic", Format: "json", Supersedes: "DP-a-20260804-aaaa"},
		"verify for + package_id":        {Action: "verify", For: wr, PackageID: "DP-a-20260804-aaaa"},
		"request + for":                  {Action: "request", Producer: "beta", Contract: "XC-a@1.0.0", InterimBehavior: "w", For: wr},
		"request + package_id":           {Action: "request", Producer: "beta", Contract: "XC-a@1.0.0", InterimBehavior: "w", PackageID: "DP-a-20260804-aaaa"},
		"plain deliver + producer":       {Action: "deliver", StagingRoot: "s", Fulfills: wr, Producer: "beta"},
		"plain verify + verdicts":        {Action: "verify", PackageID: "DP-a-20260804-aaaa", Verdicts: []string{"0:met:beta"}},
		"plain verify + for-only reason": {Action: "verify", PackageID: "DP-a-20260804-aaaa", Reason: "r"},
		"pack + for":                     {Action: "pack", Contract: "XC-a@1.0.0", From: "d", Profile: "synthetic", Format: "json", For: wr},
		"fetch + timeout":                {Action: "fetch", PackageID: "DP-a-20260804-aaaa", To: "d", Timeout: "1m"},
		"deliver for missing from":       {Action: "deliver", For: wr, Profile: "synthetic", Format: "json"},
		"request missing producer":       {Action: "request", Contract: "XC-a@1.0.0", InterimBehavior: "w"},
		"request missing interim":        {Action: "request", Producer: "beta", Contract: "XC-a@1.0.0"},
		"request bad timeout":            {Action: "request", Producer: "beta", Contract: "XC-a@1.0.0", InterimBehavior: "w", Timeout: "soon"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeMCPExpressBackend{}
			if _, _, err := callDataTool(t, testExpressDataTool(t, backend), input); err == nil || len(backend.calls) != 0 {
				t.Fatalf("accepted %+v: calls=%v err=%v", input, backend.calls, err)
			}
		})
	}
}

// TestDataToolExpressNeedsTheCapability: a backend that cannot run the
// composites refuses them as unconfigured; it does not fall back to the
// single-step action the caller did not ask for.
func TestDataToolExpressNeedsTheCapability(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPDataBackend{}
	for _, input := range []DataInput{
		{Action: "request", Producer: "beta", Contract: "XC-a@1.0.0", InterimBehavior: "w"},
		{Action: "deliver", For: "XW-axon-20260804-aaaa", From: "d", Profile: "synthetic", Format: "json"},
		{Action: "verify", For: "XW-axon-20260804-aaaa"},
	} {
		_, _, err := callDataTool(t, testDataTool(t, backend), input)
		if err == nil || !strings.Contains(err.Error(), "service is not configured") || len(backend.calls) != 0 {
			t.Fatalf("input %+v: err=%v calls=%v, want an unconfigured refusal and no call", input, err, backend.calls)
		}
	}
}

// TestDataActionsNameRequest pins the closed action enum's express member.
func TestDataActionsNameRequest(t *testing.T) {
	t.Parallel()
	if !workStringIn("request", DataActions) {
		t.Fatalf("DataActions = %v, want it to contain request", DataActions)
	}
}
