package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

const (
	testMCPWorkProject = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testMCPWorkID      = "work:01K1A2B3C4D5E6F7G8H9J0K1M7"
)

type fakeMCPWorkBackend struct {
	calls      []string
	start      workreport.StartInput
	wait       workreport.WaitInput
	result     workreport.OperationResult
	err        error
	identity   workreport.WorkIdentityInput
	leases     []workreport.Lease
	selectArgs []string
	listArgs   []string
}

func (f *fakeMCPWorkBackend) Start(_ context.Context, in workreport.StartInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "start")
	f.start = in
	return f.result, f.err
}
func (f *fakeMCPWorkBackend) Checkpoint(_ context.Context, _ workreport.CheckpointInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "checkpoint")
	return f.result, f.err
}
func (f *fakeMCPWorkBackend) Wait(_ context.Context, in workreport.WaitInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "wait")
	f.wait = in
	return f.result, f.err
}
func (f *fakeMCPWorkBackend) Stop(_ context.Context, _ workreport.StopInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "stop")
	return f.result, f.err
}
func (f *fakeMCPWorkBackend) Heartbeat(_ context.Context, _ workreport.HeartbeatInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "heartbeat")
	return f.result, f.err
}
func (f *fakeMCPWorkBackend) Resume(_ context.Context, _ workreport.ResumeInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "resume")
	return f.result, f.err
}
func (f *fakeMCPWorkBackend) ResolveWork(_ context.Context, project, space, workID, session string) (workreport.WorkIdentityInput, error) {
	f.calls = append(f.calls, "resolve")
	f.selectArgs = []string{project, space, workID, session}
	if f.result.WorkID == "selection-error" {
		return workreport.WorkIdentityInput{}, f.err
	}
	return f.identity, nil
}
func (f *fakeMCPWorkBackend) ListWork(_ context.Context, project, space, workID string, includeExpired bool) ([]workreport.Lease, error) {
	f.calls = append(f.calls, "status")
	f.listArgs = []string{project, space, workID, boolStringMCP(includeExpired)}
	return append([]workreport.Lease(nil), f.leases...), f.err
}

func boolStringMCP(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func testWorkTool(t *testing.T, backend *fakeMCPWorkBackend) ToolSpec {
	t.Helper()
	spec, err := NewWorkTool(WorkToolDeps{
		Starter: backend, Progressor: backend, Local: backend, Reader: backend,
		ProjectID: testMCPWorkProject, Space: "checkout-core",
		ResolveActor: func(in WorkActorInput) (workreport.Actor, error) {
			return workreport.Actor{Kind: in.Kind, Name: in.Name, Model: in.Model, System: "atlas", Session: in.Session}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorkTool: %v", err)
	}
	return spec
}

func callWorkTool(t *testing.T, spec ToolSpec, input any) (any, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := spec.Handler(context.Background(), raw)
	return result, err
}

func TestWorkToolRoutesEveryClosedActionAndBindsSelection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input WorkInput
		want  string
	}{
		{"start", WorkInput{Action: "start", Thread: "thread:t", SubjectRef: "XW-atlas-20260803-a001", Mode: "implementing", Summary: "building", Actor: WorkActorInput{Kind: "agent", Name: "codex", Session: "provider-session"}}, "start"},
		{"heartbeat", WorkInput{Action: "heartbeat", WorkID: testMCPWorkID, Session: "s", TTL: "10m"}, "heartbeat"},
		{"resume", WorkInput{Action: "resume", WorkID: testMCPWorkID, Session: "s"}, "resume"},
		{"checkpoint", WorkInput{Action: "checkpoint", WorkID: testMCPWorkID, Session: "s", Mode: "testing", Summary: "testing"}, "checkpoint"},
		{"wait", WorkInput{Action: "wait", WorkID: testMCPWorkID, Session: "s", Summary: "waiting", WaitingOn: []WorkWaitingInput{{Kind: "system", ID: "billing"}}}, "wait"},
		{"stop", WorkInput{Action: "stop", WorkID: testMCPWorkID, Session: "s", Result: "paused", Summary: "handoff"}, "stop"},
		{"status", WorkInput{Action: "status", WorkID: testMCPWorkID, IncludeExpired: true}, "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeMCPWorkBackend{
				identity: workreport.WorkIdentityInput{ProjectID: testMCPWorkProject, Space: "checkout-core", Thread: "thread:t", WorkID: testMCPWorkID, Actor: workreport.Actor{Kind: "agent", Name: "codex", System: "atlas", Session: "s"}},
				result:   workreport.OperationResult{WorkID: testMCPWorkID, Session: "s", LocalState: workreport.LocalActive},
			}
			if _, err := callWorkTool(t, testWorkTool(t, backend), tc.input); err != nil {
				t.Fatalf("handler error: %v; calls=%v", err, backend.calls)
			}
			if backend.calls[len(backend.calls)-1] != tc.want {
				t.Fatalf("last call=%q want=%q; all=%v", backend.calls[len(backend.calls)-1], tc.want, backend.calls)
			}
			if tc.name != "start" && tc.name != "status" {
				if got := backend.selectArgs; len(got) != 4 || got[0] != testMCPWorkProject || got[1] != "checkout-core" || got[2] != testMCPWorkID || got[3] != "s" {
					t.Fatalf("selection not project+space bound: %v", got)
				}
			}
			if tc.name == "status" {
				if got := backend.listArgs; len(got) != 4 || got[0] != testMCPWorkProject || got[1] != "checkout-core" || got[2] != testMCPWorkID || got[3] != "true" {
					t.Fatalf("status not project+space bound: %v", got)
				}
			}
		})
	}
}

func TestWorkToolRequiresPairedOwnershipAndRedactsSelectionFailure(t *testing.T) {
	t.Parallel()

	for _, input := range []WorkInput{
		{Action: "heartbeat", WorkID: testMCPWorkID},
		{Action: "heartbeat", Session: "provider secret / partial"},
	} {
		backend := &fakeMCPWorkBackend{}
		result, err := callWorkTool(t, testWorkTool(t, backend), input)
		if !errors.Is(err, workreport.ErrLeaseNotOwned) || len(backend.calls) != 0 {
			t.Fatalf("partial selection result/error/calls = %+v/%v/%v", result, err, backend.calls)
		}
		raw, marshalErr := json.Marshal(result)
		if marshalErr != nil || bytes.Contains(raw, []byte("provider secret / partial")) {
			t.Fatalf("partial selection leaked raw session: %s, %v", raw, marshalErr)
		}
	}

	const rawSession = "Bearer provider-secret-value-123456789"
	backend := &fakeMCPWorkBackend{
		result: workreport.OperationResult{WorkID: "selection-error"},
		err:    workreport.ErrLeaseNotFound,
	}
	result, err := callWorkTool(t, testWorkTool(t, backend), WorkInput{Action: "heartbeat", WorkID: testMCPWorkID, Session: rawSession})
	if !errors.Is(err, workreport.ErrLeaseNotFound) {
		t.Fatalf("selection failure error = %v", err)
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil || bytes.Contains(raw, []byte(rawSession)) || !bytes.Contains(raw, []byte(workreport.NormalizeSession(rawSession))) {
		t.Fatalf("selection failure result = %s, %v", raw, marshalErr)
	}
}

func TestWorkToolSchemaIsClosedAndHasAllEnumsWithoutRoot(t *testing.T) {
	t.Parallel()
	spec := testWorkTool(t, &fakeMCPWorkBackend{})
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           map[string]struct {
			Enum  []string `json:"enum"`
			Items struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.AdditionalProperties {
		t.Fatal("a2a_work schema must be closed")
	}
	for _, forbidden := range []string{"root", "project_root", "cwd", "path"} {
		if _, ok := schema.Properties[forbidden]; ok {
			t.Fatalf("schema exposes forbidden root selector %q", forbidden)
		}
	}
	if got := schema.Properties["action"].Enum; strings.Join(got, ",") != strings.Join(WorkActions, ",") {
		t.Fatalf("action enum=%v want=%v", got, WorkActions)
	}
	if got := schema.Properties["mode"].Enum; strings.Join(got, ",") != strings.Join(workModes, ",") {
		t.Fatalf("mode enum=%v want=%v", got, workModes)
	}
	if got := schema.Properties["waiting_on"].Items.Properties["kind"].Enum; strings.Join(got, ",") != strings.Join(workWaitKinds, ",") {
		t.Fatalf("wait-kind enum=%v want=%v", got, workWaitKinds)
	}
}

func TestWorkToolRejectsUnknownRootAndInvalidEnumsBeforeCalls(t *testing.T) {
	t.Parallel()
	cases := []json.RawMessage{
		json.RawMessage(`{"action":"status","root":"/tmp/other"}`),
		json.RawMessage(`{"action":"dance"}`),
		json.RawMessage(`{"action":"start","thread":"t","subject_ref":"X","mode":"busy","summary":"x"}`),
		json.RawMessage(`{"action":"wait","summary":"x","waiting_on":[{"kind":"database","id":"db"}]}`),
		json.RawMessage(`{"action":"stop","result":"done","summary":"x"}`),
		json.RawMessage(`{"action":"heartbeat","ttl":"tomorrow"}`),
		json.RawMessage(`{"action":"resume","summary":"must not replace persisted input"}`),
		json.RawMessage(`{"action":"status","session":"must-not-select-by-session"}`),
		json.RawMessage(`{"action":"start","thread":"t","subject_ref":"X","mode":"planning","summary":"x","session":"one","actor":{"name":"codex","session":"two"}}`),
	}
	for _, raw := range cases {
		backend := &fakeMCPWorkBackend{}
		spec := testWorkTool(t, backend)
		if result, _, err := spec.Handler(context.Background(), raw); err == nil || result != nil {
			t.Errorf("input=%s result=%#v err=%v", raw, result, err)
		}
		if len(backend.calls) != 0 {
			t.Errorf("input=%s called backend: %v", raw, backend.calls)
		}
	}
}

func TestWorkToolPreservesStructuredTwoAxisResultAndServerIsError(t *testing.T) {
	t.Parallel()
	journal, err := workreport.NewWriteResultJournal([]byte(`{"schema_version":1,"stage":"pr-created","state":"needs-attention","pr_url":"https://example.invalid/pr/9"}`))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeMCPWorkBackend{
		result: workreport.OperationResult{
			WorkID: testMCPWorkID, Session: "s", OperationKey: "op-v1-key", SemanticSequence: 3,
			LocalState: workreport.LocalClosing, ExpiresAt: time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC),
			Shared: workreport.PublishAttempt{Attempted: true, WriteResult: journal, Convergence: workreport.ConvergenceResumable, ErrorCode: "provider-partial"},
		},
		err:      errors.New("provider outcome unknown"),
		identity: workreport.WorkIdentityInput{WorkID: testMCPWorkID, Actor: workreport.Actor{Session: "s"}},
	}
	spec := testWorkTool(t, backend)
	r := NewRegistry()
	r.Register(spec)
	server := NewServer(r, "a2a-mcp", "test", nil)
	request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"a2a_work","arguments":{"action":"stop","work_id":"` + testMCPWorkID + `","session":"s","result":"paused","summary":"handoff"}}}` + "\n"
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(request), &out); err != nil {
		t.Fatal(err)
	}
	responses := decodeLines(t, &out)
	if len(responses) != 1 || responses[0].Error != nil {
		t.Fatalf("response=%+v", responses)
	}
	raw, _ := json.Marshal(responses[0].Result)
	var call toolsCallResult
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatal(err)
	}
	if !call.IsError || call.StructuredContent == nil {
		t.Fatalf("semantic error lost structured axis: %+v", call)
	}
	structured, _ := json.Marshal(call.StructuredContent)
	if !bytes.Contains(structured, []byte(`"state":"closing"`)) || !bytes.Contains(structured, []byte(`"pr_url":"https://example.invalid/pr/9"`)) {
		t.Fatalf("partial result reduced: %s", structured)
	}
}

func TestWorkToolPreservesPendingAndStatusPartialFacts(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPWorkBackend{
		identity: workreport.WorkIdentityInput{WorkID: testMCPWorkID, Actor: workreport.Actor{Session: "s"}},
		result: workreport.OperationResult{
			WorkID: testMCPWorkID, Session: "s", OperationKey: "op-v1-pending",
			SemanticSequence: 4, LocalState: workreport.LocalActive,
		},
		err: workreport.ErrPendingOperation,
	}
	result, err := callWorkTool(t, testWorkTool(t, backend), WorkInput{
		Action: "checkpoint", WorkID: testMCPWorkID, Session: "s", Mode: "reviewing", Summary: "reviewing",
	})
	if !errors.Is(err, workreport.ErrPendingOperation) {
		t.Fatalf("error=%v", err)
	}
	pending, ok := result.(workOperationOutput)
	if !ok || pending.OperationKey != "op-v1-pending" || pending.Local.State != workreport.LocalActive {
		t.Fatalf("pending result reduced: %#v", result)
	}

	backend.err = errors.New("one local lease was corrupt")
	backend.leases = []workreport.Lease{{
		Identity: workreport.Identity{WorkID: testMCPWorkID, Thread: "thread:t", Actor: workreport.Actor{Session: "s"}},
		Mode:     workreport.ModeTesting, Summary: "safe partial", WaitingOn: []workreport.WaitingOn{},
	}}
	status, err := callWorkTool(t, testWorkTool(t, backend), WorkInput{Action: "status"})
	if err == nil {
		t.Fatal("expected partial list error")
	}
	partial, ok := status.(workStatusOutput)
	if !ok || len(partial.Items) != 1 || partial.Items[0].Summary != "safe partial" {
		t.Fatalf("status error discarded readable records: %#v", status)
	}
}

func TestWorkToolHeartbeatAndStatusUseOnlyLocalSeams(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPWorkBackend{
		identity: workreport.WorkIdentityInput{WorkID: testMCPWorkID, Actor: workreport.Actor{Session: "s"}},
		result:   workreport.OperationResult{WorkID: testMCPWorkID, Session: "s", LocalState: workreport.LocalActive},
	}
	spec := testWorkTool(t, backend)
	if _, err := callWorkTool(t, spec, WorkInput{Action: "heartbeat", WorkID: testMCPWorkID, Session: "s"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(backend.calls, ","); got != "resolve,heartbeat" {
		t.Fatalf("heartbeat acquired a semantic/sync seam: %s", got)
	}
	backend.calls = nil
	if _, err := callWorkTool(t, spec, WorkInput{Action: "status"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(backend.calls, ","); got != "status" {
		t.Fatalf("status acquired a semantic/sync seam: %s", got)
	}
}

func TestWorkToolStatusNeverLeaksPrivateLeaseOrJournals(t *testing.T) {
	t.Parallel()
	prepared, err := workreport.NewPreparedJournal([]byte(`{"private":"prepared-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	write, err := workreport.NewWriteResultJournal([]byte(`{"private":"result-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeMCPWorkBackend{leases: []workreport.Lease{{
		Identity:   workreport.Identity{LeaseKey: "sha256:lease-private", ProjectID: "sha256:project-private", Space: "checkout-core", Thread: "thread:t", WorkID: testMCPWorkID, Actor: workreport.Actor{Kind: "agent", Name: "codex", System: "atlas", Session: "s"}},
		SubjectRef: "XW-atlas-20260803-a001", Mode: workreport.ModeImplementing, Summary: "safe",
		StartedAt: time.Now(), RenewedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), HeartbeatSequence: 2, SemanticSequence: 3,
		Pending: &workreport.PendingOperation{OperationKey: "op-v1-safe", Action: workreport.ActionCheckpoint, SemanticSequence: 3, Prepared: prepared, Shared: workreport.PublishAttempt{Attempted: true, WriteResult: write, Convergence: workreport.ConvergenceResumable, ErrorCode: "stable-code"}},
	}}}
	result, err := callWorkTool(t, testWorkTool(t, backend), WorkInput{Action: "status", IncludeExpired: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	for _, forbidden := range []string{"lease-private", "project-private", "prepared-secret", "result-secret", "lease_key", "project_id", "prepared_submission", "write_result"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Errorf("status leaked %q: %s", forbidden, raw)
		}
	}
}

func TestWorkToolTreatsInboundTextAsData(t *testing.T) {
	t.Parallel()
	backend := &fakeMCPWorkBackend{result: workreport.OperationResult{LocalState: workreport.LocalActive}}
	payload := `$(touch /tmp/a2a-work-must-not-run); <script>alert(1)</script>`
	_, err := callWorkTool(t, testWorkTool(t, backend), WorkInput{
		Action: "start", Thread: "thread:t", SubjectRef: "XW-atlas-20260803-a001", Mode: "planning", Summary: payload,
		Actor: WorkActorInput{Kind: "agent", Name: "codex"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if backend.start.Summary != payload {
		t.Fatalf("input was interpreted or rewritten: %q", backend.start.Summary)
	}
}

func TestNewWorkToolRefusesIncompleteDependencies(t *testing.T) {
	t.Parallel()
	if spec, err := NewWorkTool(WorkToolDeps{}); err == nil || spec.Name != "" {
		t.Fatalf("spec=%+v err=%v", spec, err)
	}
}
