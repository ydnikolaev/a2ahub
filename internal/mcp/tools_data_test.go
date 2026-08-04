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
	calls      []string
	packReq    DataPackRequest
	deliverReq DataDeliverRequest
	fetchReq   DataFetchRequest
	verifyReq  DataVerifyRequest
	result     DataResult
	err        error
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

var _ DataOperations = (*fakeMCPDataBackend)(nil)

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
		{Action: "fetch", PackageID: "DP-a-20260804-aaaa", To: "d", Pass: true},
		{Action: "verify", PackageID: "DP-a-20260804-aaaa", To: "d"},
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

func TestDataToolVerifyForwardsPassAndActor(t *testing.T) {
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
		Action: "verify", PackageID: "DP-axon-20260804-ab12", Pass: true,
		Actor: ActorInput{Kind: "agent", Name: "beta-bot"},
	})
	if callErr != nil {
		t.Fatalf("verify: %v", callErr)
	}
	if !backend.verifyReq.Pass || backend.verifyReq.Actor.Name != "beta-bot" {
		t.Fatalf("verify request not forwarded: %+v", backend.verifyReq)
	}
	if !strings.Contains(body, "fail") {
		t.Fatalf("verify summary missing verdict: %q", body)
	}
}
