package cli

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
	testWorkProject = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testWorkID      = "work:01K1A2B3C4D5E6F7G8H9J0K1M7"
)

type fakeWorkBackend struct {
	calls      []string
	start      workreport.StartInput
	checkpoint workreport.CheckpointInput
	wait       workreport.WaitInput
	stop       workreport.StopInput
	result     workreport.OperationResult
	err        error
	identity   workreport.WorkIdentityInput
	leases     []workreport.Lease
	selectArgs []string
	listArgs   []string
}

func (f *fakeWorkBackend) Start(_ context.Context, in workreport.StartInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "start")
	f.start = in
	return f.result, f.err
}

func (f *fakeWorkBackend) Checkpoint(_ context.Context, in workreport.CheckpointInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "checkpoint")
	f.checkpoint = in
	return f.result, f.err
}

func (f *fakeWorkBackend) Wait(_ context.Context, in workreport.WaitInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "wait")
	f.wait = in
	return f.result, f.err
}

func (f *fakeWorkBackend) Stop(_ context.Context, in workreport.StopInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "stop")
	f.stop = in
	return f.result, f.err
}

func (f *fakeWorkBackend) Heartbeat(_ context.Context, _ workreport.HeartbeatInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "heartbeat")
	return f.result, f.err
}

func (f *fakeWorkBackend) Resume(_ context.Context, _ workreport.ResumeInput) (workreport.OperationResult, error) {
	f.calls = append(f.calls, "resume")
	return f.result, f.err
}

func (f *fakeWorkBackend) ResolveWork(_ context.Context, project, space, workID, session string) (workreport.WorkIdentityInput, error) {
	f.calls = append(f.calls, "resolve")
	f.selectArgs = []string{project, space, workID, session}
	if f.err != nil && f.result.WorkID == "selection-error" {
		return workreport.WorkIdentityInput{}, f.err
	}
	return f.identity, nil
}

func (f *fakeWorkBackend) ListWork(_ context.Context, project, space, workID string, includeExpired bool) ([]workreport.Lease, error) {
	f.calls = append(f.calls, "status")
	f.listArgs = []string{project, space, workID, boolString(includeExpired)}
	return append([]workreport.Lease(nil), f.leases...), f.err
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func testWorkCommand(t *testing.T, backend *fakeWorkBackend) *WorkCommand {
	t.Helper()
	command, err := NewWorkCommand(WorkCommandDeps{
		Starter: backend, Progressor: backend, Local: backend, Reader: backend,
		ProjectID: testWorkProject, Space: "checkout-core",
		ResolveActor: func(flags WorkActorFlags) (workreport.Actor, error) {
			return workreport.Actor{Kind: flags.Kind, Name: flags.Name, Model: flags.Model, System: "atlas", Session: flags.Session}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewWorkCommand: %v", err)
	}
	return command
}

func runWorkCommand(t *testing.T, command *WorkCommand, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	exit := command.Run(context.Background(), args, IO{Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
	return exit, stdout.String(), stderr.String()
}

func TestWorkCommandRoutesEveryActionAndBindsLocalSelection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"start", []string{"start", "--thread", "thread:checkout", "--subject-ref", "XW-atlas-20260803-a001", "--mode", "implementing", "--summary", "building", "--actor-kind", "agent", "--actor-name", "codex", "--session", "provider-session"}, "start"},
		{"heartbeat", []string{"heartbeat", "--work-id", testWorkID, "--session", "s", "--ttl", "10m"}, "heartbeat"},
		{"resume", []string{"resume", "--work-id", testWorkID, "--session", "s"}, "resume"},
		{"checkpoint", []string{"checkpoint", "--work-id", testWorkID, "--session", "s", "--mode", "testing", "--summary", "tests running"}, "checkpoint"},
		{"wait", []string{"wait", "--work-id", testWorkID, "--session", "s", "--summary", "waiting", "--waiting-on", "system:billing:contract update"}, "wait"},
		{"stop", []string{"stop", "--work-id", testWorkID, "--session", "s", "--result", "paused", "--summary", "handoff"}, "stop"},
		{"status", []string{"status", "--work-id", testWorkID, "--include-expired", "--json"}, "status"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeWorkBackend{
				identity: workreport.WorkIdentityInput{ProjectID: testWorkProject, Space: "checkout-core", Thread: "thread:checkout", WorkID: testWorkID, Actor: workreport.Actor{Kind: "agent", Name: "codex", System: "atlas", Session: "s"}},
				result:   workreport.OperationResult{WorkID: testWorkID, Session: "s", LocalState: workreport.LocalActive},
			}
			exit, _, stderr := runWorkCommand(t, testWorkCommand(t, backend), tc.args...)
			if exit != 0 {
				t.Fatalf("exit=%d stderr=%s calls=%v", exit, stderr, backend.calls)
			}
			if backend.calls[len(backend.calls)-1] != tc.want {
				t.Fatalf("last call=%q want %q; all=%v", backend.calls[len(backend.calls)-1], tc.want, backend.calls)
			}
			if tc.name != "start" && tc.name != "status" {
				if got := backend.selectArgs; len(got) != 4 || got[0] != testWorkProject || got[1] != "checkout-core" || got[2] != testWorkID || got[3] != "s" {
					t.Fatalf("selection was not bound to exact project+space: %v", got)
				}
			}
			if tc.name == "status" {
				if got := backend.listArgs; len(got) != 4 || got[0] != testWorkProject || got[1] != "checkout-core" || got[2] != testWorkID || got[3] != "true" {
					t.Fatalf("status was not bound to exact project+space: %v", got)
				}
			}
		})
	}
}

func TestWorkCommandRequiresPairedOwnershipAndRedactsSelectionFailure(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"heartbeat", "--work-id", testWorkID, "--json"},
		{"heartbeat", "--session", "provider secret / partial", "--json"},
	} {
		backend := &fakeWorkBackend{}
		exit, stdout, _ := runWorkCommand(t, testWorkCommand(t, backend), args...)
		if exit != 1 || len(backend.calls) != 0 {
			t.Fatalf("partial selection exit/calls = %d/%v, want refusal before reader", exit, backend.calls)
		}
		if strings.Contains(stdout, "provider secret / partial") {
			t.Fatalf("partial selection leaked raw session: %s", stdout)
		}
	}

	const rawSession = "Bearer provider-secret-value-123456789"
	backend := &fakeWorkBackend{
		result: workreport.OperationResult{WorkID: "selection-error"},
		err:    workreport.ErrLeaseNotFound,
	}
	exit, stdout, _ := runWorkCommand(t, testWorkCommand(t, backend), "heartbeat", "--work-id", testWorkID, "--session", rawSession, "--json")
	if exit != 1 || strings.Contains(stdout, rawSession) || !strings.Contains(stdout, workreport.NormalizeSession(rawSession)) {
		t.Fatalf("selection failure output exit=%d body=%s", exit, stdout)
	}
}

func TestWorkCommandRejectsInvalidEnumsAndFlagsBeforeCalls(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"start", "--thread", "t", "--subject-ref", "X", "--mode", "busy", "--summary", "x"},
		{"checkpoint", "--mode", "finished", "--summary", "x"},
		{"wait", "--summary", "x", "--waiting-on", "database:db"},
		{"stop", "--result", "done", "--summary", "x"},
		{"heartbeat", "--ttl", "not-duration"},
		{"resume", "unexpected"},
	}
	for _, args := range cases {
		backend := &fakeWorkBackend{}
		exit, _, _ := runWorkCommand(t, testWorkCommand(t, backend), args...)
		if exit != 2 {
			t.Errorf("%v exit=%d want 2", args, exit)
		}
		if len(backend.calls) != 0 {
			t.Errorf("%v unexpectedly called backend: %v", args, backend.calls)
		}
	}
}

func TestWorkCommandPreservesTwoAxisPartialResultOnError(t *testing.T) {
	t.Parallel()
	journal, err := workreport.NewWriteResultJournal([]byte(`{"schema_version":1,"stage":"pr-created","state":"needs-attention","pr_url":"https://example.invalid/pr/8"}`))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeWorkBackend{
		result: workreport.OperationResult{
			WorkID: testWorkID, Session: "session:01K1A2B3C4D5E6F7G8H9J0K1M7", OperationKey: "op-v1-key",
			SemanticSequence: 2, LocalState: workreport.LocalActive, ExpiresAt: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
			Shared: workreport.PublishAttempt{Attempted: true, WriteResult: journal, Convergence: workreport.ConvergenceResumable, ErrorCode: "provider-partial"},
		},
		err: errors.New("provider outcome unknown"),
	}
	exit, stdout, stderr := runWorkCommand(t, testWorkCommand(t, backend), "start", "--thread", "thread:x", "--subject-ref", "XW-atlas-20260803-a001", "--mode", "implementing", "--summary", "building", "--actor-name", "codex", "--json")
	if exit != 1 || !strings.Contains(stderr, "provider outcome unknown") {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("structured result missing on error: %v; %s", err, stdout)
	}
	local := got["local"].(map[string]any)
	shared := got["shared"].(map[string]any)
	write := shared["write_result"].(map[string]any)
	if local["state"] != "active" || shared["attempted"] != true || write["pr_url"] != "https://example.invalid/pr/8" {
		t.Fatalf("partial two-axis result reduced: %#v", got)
	}
}

func TestWorkCommandHumanOutputCarriesBothAxes(t *testing.T) {
	t.Parallel()
	journal, err := workreport.NewWriteResultJournal([]byte(`{"schema_version":1,"stage":"merged"}`))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeWorkBackend{
		identity: workreport.WorkIdentityInput{WorkID: testWorkID, Actor: workreport.Actor{Session: "s"}},
		result: workreport.OperationResult{
			WorkID: testWorkID, Session: "s", LocalState: workreport.LocalActive,
			Shared: workreport.PublishAttempt{Attempted: true, WriteResult: journal, Convergence: workreport.ConvergenceAccepted},
		},
	}
	exit, stdout, stderr := runWorkCommand(t, testWorkCommand(t, backend), "checkpoint", "--work-id", testWorkID, "--session", "s", "--mode", "testing", "--summary", "green")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "local state=active") || !strings.Contains(stdout, "shared attempted=true") || !strings.Contains(stdout, `"stage":"merged"`) {
		t.Fatalf("human output omitted an axis: %s", stdout)
	}
}

func TestWorkStatusNeverLeaksPrivateLeaseOrJournals(t *testing.T) {
	t.Parallel()
	prepared, err := workreport.NewPreparedJournal([]byte(`{"private":"prepared-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	write, err := workreport.NewWriteResultJournal([]byte(`{"private":"result-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeWorkBackend{leases: []workreport.Lease{{
		Identity: workreport.Identity{
			LeaseKey: "sha256:lease-private", ProjectID: "sha256:project-private", Space: "checkout-core", Thread: "thread:t", WorkID: testWorkID,
			Actor: workreport.Actor{Kind: "agent", Name: "codex", System: "atlas", Session: "s"},
		},
		SubjectRef: "XW-atlas-20260803-a001", Mode: workreport.ModeImplementing, Summary: "safe",
		StartedAt: time.Now(), RenewedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), HeartbeatSequence: 2, SemanticSequence: 3,
		Pending: &workreport.PendingOperation{
			OperationKey: "op-v1-safe", Action: workreport.ActionCheckpoint, SemanticSequence: 3, Prepared: prepared,
			Shared: workreport.PublishAttempt{Attempted: true, WriteResult: write, Convergence: workreport.ConvergenceResumable, ErrorCode: "stable-code"},
		},
	}}}
	exit, stdout, stderr := runWorkCommand(t, testWorkCommand(t, backend), "status", "--json", "--include-expired")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	for _, forbidden := range []string{"lease-private", "project-private", "prepared-secret", "result-secret", "lease_key", "project_id", "prepared_submission", "write_result"} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("status leaked %q: %s", forbidden, stdout)
		}
	}
	if !strings.Contains(stdout, "op-v1-safe") || !strings.Contains(stdout, "stable-code") {
		t.Fatalf("status lost bounded recovery metadata: %s", stdout)
	}
}

func TestWorkCommandTreatsInboundTextAsData(t *testing.T) {
	t.Parallel()
	backend := &fakeWorkBackend{result: workreport.OperationResult{LocalState: workreport.LocalActive}}
	payload := `$(touch /tmp/a2a-work-must-not-run); <script>alert(1)</script>`
	exit, _, stderr := runWorkCommand(t, testWorkCommand(t, backend), "start", "--thread", "thread:x", "--subject-ref", "XW-atlas-20260803-a001", "--mode", "planning", "--summary", payload, "--actor-name", "codex")
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr)
	}
	if backend.start.Summary != payload {
		t.Fatalf("input was interpreted or rewritten: %q", backend.start.Summary)
	}
}

func TestNewWorkCommandRefusesIncompleteDependencies(t *testing.T) {
	t.Parallel()
	if command, err := NewWorkCommand(WorkCommandDeps{}); err == nil || command != nil {
		t.Fatalf("got command=%v err=%v", command, err)
	}
}
