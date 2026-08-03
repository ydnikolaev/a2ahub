package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

const (
	// WorkToolName is exported for lead-owned registry and pre-call wiring.
	// The server must skip generic mirror refresh for this offline/local tool.
	WorkToolName         = "a2a_work"
	maximumWorkToolInput = 64 * 1024
)

// WorkActions is the closed a2a_work discriminator enum.
var WorkActions = []string{"start", "heartbeat", "resume", "checkpoint", "wait", "stop", "status"}

var workModes = []string{
	string(workreport.ModePlanning), string(workreport.ModeImplementing), string(workreport.ModeTesting),
	string(workreport.ModeReviewing), string(workreport.ModeWaiting), string(workreport.ModePaused), string(workreport.ModeFinished),
}
var workWaitKinds = []string{
	string(workreport.WaitSystem), string(workreport.WaitHuman), string(workreport.WaitTool),
	string(workreport.WaitTimer), string(workreport.WaitExternal),
}

// WorkStarter starts a durable work lease.
type WorkStarter interface {
	Start(context.Context, workreport.StartInput) (workreport.OperationResult, error)
}

// WorkProgressor advances or closes a durable work lease.
type WorkProgressor interface {
	Checkpoint(context.Context, workreport.CheckpointInput) (workreport.OperationResult, error)
	Wait(context.Context, workreport.WaitInput) (workreport.OperationResult, error)
	Stop(context.Context, workreport.StopInput) (workreport.OperationResult, error)
}

// WorkLocalOperator changes a machine-local lease state.
type WorkLocalOperator interface {
	Heartbeat(context.Context, workreport.HeartbeatInput) (workreport.OperationResult, error)
	Resume(context.Context, workreport.ResumeInput) (workreport.OperationResult, error)
}

// WorkLocalReader is the offline rooted-store seam. Selection/listing never
// invokes mirror sync, Git, host, preparation, or publication.
type WorkLocalReader interface {
	ResolveWork(context.Context, string, string, string, string) (workreport.WorkIdentityInput, error)
	ListWork(context.Context, string, string, string, bool) ([]workreport.Lease, error)
}

// WorkActorInput is the actor subset accepted by the work tool.
type WorkActorInput struct {
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session,omitempty"`
}

// WorkActorResolver resolves an MCP actor input to its durable identity.
type WorkActorResolver func(WorkActorInput) (workreport.Actor, error)

// WorkToolDeps are construction-time dependencies for the offline work tool.
type WorkToolDeps struct {
	Starter      WorkStarter
	Progressor   WorkProgressor
	Local        WorkLocalOperator
	Reader       WorkLocalReader
	ResolveActor WorkActorResolver
	ProjectID    string
	Space        string
	ResolveSpace func(string) (WorkToolDeps, error)
}

type unavailableWorkBackend struct{}

func (unavailableWorkBackend) Start(context.Context, workreport.StartInput) (workreport.OperationResult, error) {
	return workreport.OperationResult{LocalState: workreport.LocalUnchanged}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) Checkpoint(context.Context, workreport.CheckpointInput) (workreport.OperationResult, error) {
	return workreport.OperationResult{LocalState: workreport.LocalUnchanged}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) Wait(context.Context, workreport.WaitInput) (workreport.OperationResult, error) {
	return workreport.OperationResult{LocalState: workreport.LocalUnchanged}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) Stop(context.Context, workreport.StopInput) (workreport.OperationResult, error) {
	return workreport.OperationResult{LocalState: workreport.LocalUnchanged}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) Heartbeat(context.Context, workreport.HeartbeatInput) (workreport.OperationResult, error) {
	return workreport.OperationResult{LocalState: workreport.LocalUnchanged}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) Resume(context.Context, workreport.ResumeInput) (workreport.OperationResult, error) {
	return workreport.OperationResult{LocalState: workreport.LocalUnchanged}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) ResolveWork(context.Context, string, string, string, string) (workreport.WorkIdentityInput, error) {
	return workreport.WorkIdentityInput{}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}
func (unavailableWorkBackend) ListWork(context.Context, string, string, string, bool) ([]workreport.Lease, error) {
	return nil, fmt.Errorf("a2a_work: production work dependencies are unavailable")
}

func unavailableWorkToolDeps() WorkToolDeps {
	backend := unavailableWorkBackend{}
	return WorkToolDeps{
		Starter: backend, Progressor: backend, Local: backend, Reader: backend,
		ResolveActor: func(WorkActorInput) (workreport.Actor, error) {
			return workreport.Actor{}, fmt.Errorf("a2a_work: production work dependencies are unavailable")
		},
		ProjectID: "unavailable", Space: "unavailable",
	}
}

// NewWorkTool constructs the closed, offline a2a_work tool. Registration and
// the server pre-call exclusion are lead-owned wiring concerns.
func NewWorkTool(deps WorkToolDeps) (ToolSpec, error) {
	if deps.ResolveSpace == nil && (deps.Starter == nil || deps.Progressor == nil || deps.Local == nil || deps.Reader == nil || deps.ResolveActor == nil) {
		return ToolSpec{}, fmt.Errorf("mcp: work starter, progressor, local operator, local reader and actor resolver are required")
	}
	if strings.TrimSpace(deps.ProjectID) == "" || (deps.ResolveSpace == nil && strings.TrimSpace(deps.Space) == "") {
		return ToolSpec{}, fmt.Errorf("mcp: work project id and space are required")
	}
	return ToolSpec{
		Name:        WorkToolName,
		Description: "report semantic work or inspect/renew the machine-local lease; action=start|heartbeat|resume|checkpoint|wait|stop|status",
		InputSchema: workToolSchema(),
		Handler:     newWorkHandler(deps),
	}, nil
}

// WorkInput is the a2a_work handler's structured MCP input.
type WorkInput struct {
	Action         string             `json:"action"`
	Space          string             `json:"space,omitempty"`
	Thread         string             `json:"thread,omitempty"`
	SubjectRef     string             `json:"subject_ref,omitempty"`
	Mode           string             `json:"mode,omitempty"`
	Summary        string             `json:"summary,omitempty"`
	WorkID         string             `json:"work_id,omitempty"`
	Session        string             `json:"session,omitempty"`
	TTL            string             `json:"ttl,omitempty"`
	ReportValidFor string             `json:"report_valid_for,omitempty"`
	Recipients     []string           `json:"recipients,omitempty"`
	Classification string             `json:"classification,omitempty"`
	Actor          WorkActorInput     `json:"actor,omitempty"`
	WaitingOn      []WorkWaitingInput `json:"waiting_on,omitempty"`
	Result         string             `json:"result,omitempty"`
	IncludeExpired bool               `json:"include_expired,omitempty"`
}

// WorkWaitingInput identifies one dependency for a work wait operation.
type WorkWaitingInput struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
}

func newWorkHandler(deps WorkToolDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in WorkInput
		if err := decodeWorkInput(args, &in); err != nil {
			return nil, "", err
		}
		if !workStringIn(in.Action, WorkActions) {
			return nil, "", fmt.Errorf("a2a_work: action is required and must be one of %s", strings.Join(WorkActions, "|"))
		}
		callDeps := deps
		if deps.ResolveSpace != nil {
			var resolveErr error
			callDeps, resolveErr = deps.ResolveSpace(in.Space)
			if resolveErr != nil {
				return nil, "", resolveErr
			}
		} else if in.Space != "" && in.Space != deps.Space {
			return nil, "", fmt.Errorf("a2a_work: space %q is not connected", in.Space)
		}
		ttl, err := workDuration(in.TTL, "ttl")
		if err != nil {
			return nil, "", err
		}
		validFor, err := workDuration(in.ReportValidFor, "report_valid_for")
		if err != nil {
			return nil, "", err
		}

		switch in.Action {
		case "start":
			if err := workForbidInput(in, "work_id", "result", "waiting_on", "include_expired"); err != nil {
				return nil, "", err
			}
			if in.Session != "" && in.Actor.Session != "" && in.Session != in.Actor.Session {
				return nil, "", fmt.Errorf("a2a_work start: session conflicts with actor.session")
			}
			if strings.TrimSpace(in.Thread) == "" || strings.TrimSpace(in.SubjectRef) == "" || strings.TrimSpace(in.Summary) == "" || !workActiveMode(in.Mode) {
				return nil, "", fmt.Errorf("a2a_work start: thread, subject_ref, summary and active mode are required")
			}
			actorInput := in.Actor
			if actorInput.Session == "" {
				actorInput.Session = in.Session
			}
			actor, actorErr := callDeps.ResolveActor(actorInput)
			if actorErr != nil {
				return nil, "", fmt.Errorf("a2a_work start: %w", actorErr)
			}
			result, callErr := callDeps.Starter.Start(ctx, workreport.StartInput{
				ProjectID: callDeps.ProjectID, Space: callDeps.Space, Thread: in.Thread, Actor: actor,
				SubjectRef: in.SubjectRef, Mode: workreport.Mode(in.Mode), Summary: in.Summary,
				TTL: ttl, ReportValidFor: validFor, Recipients: append([]string(nil), in.Recipients...), Classification: in.Classification,
			})
			return workOperationFrom(result), "", callErr
		case "heartbeat":
			if err := workForbidInput(in, "thread", "subject_ref", "mode", "summary", "report_valid_for", "recipients", "classification", "actor", "waiting_on", "result", "include_expired"); err != nil {
				return nil, "", err
			}
			identity, selectErr := resolveWork(ctx, callDeps, in.WorkID, in.Session)
			if selectErr != nil {
				return workOperationFrom(workSelectionFailure(in.WorkID, in.Session)), "", selectErr
			}
			result, callErr := callDeps.Local.Heartbeat(ctx, workreport.HeartbeatInput{Work: identity, TTL: ttl})
			return workOperationFrom(result), "", callErr
		case "resume":
			if err := workForbidInput(in, "thread", "subject_ref", "mode", "summary", "ttl", "report_valid_for", "recipients", "classification", "actor", "waiting_on", "result", "include_expired"); err != nil {
				return nil, "", fmt.Errorf("%w; persisted inputs are replayed exactly", err)
			}
			identity, selectErr := resolveWork(ctx, callDeps, in.WorkID, in.Session)
			if selectErr != nil {
				return workOperationFrom(workSelectionFailure(in.WorkID, in.Session)), "", selectErr
			}
			result, callErr := callDeps.Local.Resume(ctx, workreport.ResumeInput{Work: identity})
			return workOperationFrom(result), "", callErr
		case "checkpoint":
			if err := workForbidInput(in, "thread", "recipients", "classification", "actor", "waiting_on", "result", "include_expired"); err != nil {
				return nil, "", err
			}
			if strings.TrimSpace(in.Summary) == "" || !workActiveMode(in.Mode) {
				return nil, "", fmt.Errorf("a2a_work checkpoint: summary and active mode are required")
			}
			identity, selectErr := resolveWork(ctx, callDeps, in.WorkID, in.Session)
			if selectErr != nil {
				return workOperationFrom(workSelectionFailure(in.WorkID, in.Session)), "", selectErr
			}
			result, callErr := callDeps.Progressor.Checkpoint(ctx, workreport.CheckpointInput{
				Work: identity, SubjectRef: in.SubjectRef, Mode: workreport.Mode(in.Mode), Summary: in.Summary,
				TTL: ttl, ReportValidFor: validFor,
			})
			return workOperationFrom(result), "", callErr
		case "wait":
			if err := workForbidInput(in, "thread", "subject_ref", "mode", "recipients", "classification", "actor", "result", "include_expired"); err != nil {
				return nil, "", err
			}
			waiting, waitingErr := workWaitingOn(in.WaitingOn)
			if waitingErr != nil || strings.TrimSpace(in.Summary) == "" {
				if waitingErr != nil {
					return nil, "", waitingErr
				}
				return nil, "", fmt.Errorf("a2a_work wait: summary and at least one waiting_on item are required")
			}
			identity, selectErr := resolveWork(ctx, callDeps, in.WorkID, in.Session)
			if selectErr != nil {
				return workOperationFrom(workSelectionFailure(in.WorkID, in.Session)), "", selectErr
			}
			result, callErr := callDeps.Progressor.Wait(ctx, workreport.WaitInput{
				Work: identity, Summary: in.Summary, WaitingOn: waiting, TTL: ttl, ReportValidFor: validFor,
			})
			return workOperationFrom(result), "", callErr
		case "stop":
			if err := workForbidInput(in, "thread", "subject_ref", "mode", "ttl", "recipients", "classification", "actor", "waiting_on", "include_expired"); err != nil {
				return nil, "", err
			}
			if strings.TrimSpace(in.Summary) == "" || (in.Result != string(workreport.ModeFinished) && in.Result != string(workreport.ModePaused)) {
				return nil, "", fmt.Errorf("a2a_work stop: summary and result=finished|paused are required")
			}
			identity, selectErr := resolveWork(ctx, callDeps, in.WorkID, in.Session)
			if selectErr != nil {
				return workOperationFrom(workSelectionFailure(in.WorkID, in.Session)), "", selectErr
			}
			result, callErr := callDeps.Progressor.Stop(ctx, workreport.StopInput{
				Work: identity, Result: workreport.Mode(in.Result), Summary: in.Summary, ReportValidFor: validFor,
			})
			return workOperationFrom(result), "", callErr
		case "status":
			if err := workForbidInput(in, "thread", "subject_ref", "mode", "summary", "session", "ttl", "report_valid_for", "recipients", "classification", "actor", "waiting_on", "result"); err != nil {
				return nil, "", err
			}
			leases, listErr := callDeps.Reader.ListWork(ctx, callDeps.ProjectID, callDeps.Space, in.WorkID, in.IncludeExpired)
			return workStatusFrom(leases), "", listErr
		default:
			panic("unreachable closed a2a_work action")
		}
	}
}

func workForbidInput(in WorkInput, fields ...string) error {
	for _, field := range fields {
		present := false
		switch field {
		case "thread":
			present = in.Thread != ""
		case "subject_ref":
			present = in.SubjectRef != ""
		case "mode":
			present = in.Mode != ""
		case "summary":
			present = in.Summary != ""
		case "work_id":
			present = in.WorkID != ""
		case "session":
			present = in.Session != ""
		case "ttl":
			present = in.TTL != ""
		case "report_valid_for":
			present = in.ReportValidFor != ""
		case "recipients":
			present = len(in.Recipients) != 0
		case "classification":
			present = in.Classification != ""
		case "actor":
			present = in.Actor != (WorkActorInput{})
		case "waiting_on":
			present = len(in.WaitingOn) != 0
		case "result":
			present = in.Result != ""
		case "include_expired":
			present = in.IncludeExpired
		default:
			panic("unknown a2a_work forbidden field " + field)
		}
		if present {
			return fmt.Errorf("a2a_work %s: %s is forbidden for this action", in.Action, field)
		}
	}
	return nil
}

func decodeWorkInput(raw json.RawMessage, out *WorkInput) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > maximumWorkToolInput {
		return fmt.Errorf("a2a_work: input exceeds %d bytes", maximumWorkToolInput)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("a2a_work: invalid input: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("a2a_work: invalid input: multiple JSON values")
		}
		return fmt.Errorf("a2a_work: invalid input: %w", err)
	}
	return nil
}

func workDuration(raw, field string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("a2a_work: %s must be a Go duration: %w", field, err)
	}
	return value, nil
}

func workActiveMode(value string) bool {
	switch workreport.Mode(value) {
	case workreport.ModePlanning, workreport.ModeImplementing, workreport.ModeTesting, workreport.ModeReviewing:
		return true
	default:
		return false
	}
}

func workWaitingOn(values []WorkWaitingInput) ([]workreport.WaitingOn, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("a2a_work wait: at least one waiting_on item is required")
	}
	out := make([]workreport.WaitingOn, 0, len(values))
	for _, value := range values {
		kind := workreport.WaitKind(value.Kind)
		if !kind.Valid() || strings.TrimSpace(value.ID) == "" {
			return nil, fmt.Errorf("a2a_work wait: waiting_on kind must be system|human|tool|timer|external and id is required")
		}
		out = append(out, workreport.WaitingOn{Kind: kind, ID: value.ID, Summary: value.Summary})
	}
	return out, nil
}

func workStringIn(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

type workOperationOutput struct {
	WorkID           string            `json:"work_id,omitempty"`
	Session          string            `json:"session,omitempty"`
	OperationKey     string            `json:"operation_key,omitempty"`
	Action           workreport.Action `json:"action,omitempty"`
	SemanticSequence uint64            `json:"semantic_sequence,omitempty"`
	Local            workLocalOutput   `json:"local"`
	Shared           workSharedOutput  `json:"shared"`
}

type workLocalOutput struct {
	State     workreport.LocalState `json:"state"`
	ErrorCode string                `json:"error_code,omitempty"`
	ExpiresAt *time.Time            `json:"expires_at,omitempty"`
}

type workSharedOutput struct {
	Attempted   bool                   `json:"attempted"`
	WriteResult json.RawMessage        `json:"write_result,omitempty"`
	Convergence workreport.Convergence `json:"convergence,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
}

func workOperationFrom(result workreport.OperationResult) workOperationOutput {
	local := workLocalOutput{State: result.LocalState, ErrorCode: result.LocalErrorCode}
	if !result.ExpiresAt.IsZero() {
		expires := result.ExpiresAt.UTC()
		local.ExpiresAt = &expires
	}
	shared := workSharedOutput{
		Attempted: result.Shared.Attempted, Convergence: result.Shared.Convergence, ErrorCode: result.Shared.ErrorCode,
	}
	if raw := result.Shared.WriteResult.Bytes(); len(raw) > 0 {
		shared.WriteResult = json.RawMessage(raw)
	}
	return workOperationOutput{
		WorkID: result.WorkID, Session: result.Session, OperationKey: result.OperationKey, Action: result.Action,
		SemanticSequence: result.SemanticSequence, Local: local, Shared: shared,
	}
}

func workSelectionFailure(workID, session string) workreport.OperationResult {
	return workreport.OperationResult{
		WorkID: workID, Session: workreport.NormalizeSession(session), LocalState: workreport.LocalUnchanged,
	}
}

func resolveWork(ctx context.Context, deps WorkToolDeps, workID, session string) (workreport.WorkIdentityInput, error) {
	if (workID == "") != (session == "") {
		return workreport.WorkIdentityInput{}, workreport.ErrLeaseNotOwned
	}
	return deps.Reader.ResolveWork(ctx, deps.ProjectID, deps.Space, workID, session)
}

type workStatusOutput struct {
	Items []workStatusItem `json:"items"`
}

type workStatusItem struct {
	WorkID            string                 `json:"work_id"`
	Session           string                 `json:"session"`
	Thread            string                 `json:"thread"`
	SubjectRef        string                 `json:"subject_ref"`
	Actor             workreport.Actor       `json:"actor"`
	Mode              workreport.Mode        `json:"mode"`
	Summary           string                 `json:"summary"`
	WaitingOn         []workreport.WaitingOn `json:"waiting_on"`
	StartedAt         time.Time              `json:"started_at"`
	RenewedAt         time.Time              `json:"renewed_at"`
	ExpiresAt         time.Time              `json:"expires_at"`
	HeartbeatSequence uint64                 `json:"heartbeat_sequence"`
	SemanticSequence  uint64                 `json:"semantic_sequence"`
	Closing           bool                   `json:"closing"`
	Pending           *workPendingStatus     `json:"pending,omitempty"`
}

type workPendingStatus struct {
	OperationKey     string            `json:"operation_key"`
	Action           workreport.Action `json:"action"`
	SemanticSequence uint64            `json:"semantic_sequence"`
	Shared           workStatusShared  `json:"shared"`
	LastErrorCode    string            `json:"last_error_code,omitempty"`
}

type workStatusShared struct {
	Attempted   bool                   `json:"attempted"`
	Convergence workreport.Convergence `json:"convergence,omitempty"`
	ErrorCode   string                 `json:"error_code,omitempty"`
}

func workStatusFrom(leases []workreport.Lease) workStatusOutput {
	items := make([]workStatusItem, 0, len(leases))
	for _, lease := range leases {
		item := workStatusItem{
			WorkID: lease.Identity.WorkID, Session: lease.Identity.Actor.Session, Thread: lease.Identity.Thread,
			SubjectRef: lease.SubjectRef, Actor: lease.Identity.Actor, Mode: lease.Mode, Summary: lease.Summary,
			WaitingOn: append([]workreport.WaitingOn(nil), lease.WaitingOn...), StartedAt: lease.StartedAt.UTC(),
			RenewedAt: lease.RenewedAt.UTC(), ExpiresAt: lease.ExpiresAt.UTC(), HeartbeatSequence: lease.HeartbeatSequence,
			SemanticSequence: lease.SemanticSequence, Closing: lease.Closing,
		}
		if item.WaitingOn == nil {
			item.WaitingOn = []workreport.WaitingOn{}
		}
		if lease.Pending != nil {
			item.Pending = &workPendingStatus{
				OperationKey: lease.Pending.OperationKey, Action: lease.Pending.Action,
				SemanticSequence: lease.Pending.SemanticSequence, LastErrorCode: lease.Pending.LastErrorCode,
				Shared: workStatusShared{
					Attempted: lease.Pending.Shared.Attempted, Convergence: lease.Pending.Shared.Convergence,
					ErrorCode: lease.Pending.Shared.ErrorCode,
				},
			}
		}
		items = append(items, item)
	}
	return workStatusOutput{Items: items}
}

func workToolSchema() json.RawMessage {
	properties := map[string]any{
		"action": map[string]any{"type": "string", "enum": WorkActions},
		"space":  map[string]any{"type": "string"},
		"thread": map[string]any{"type": "string"}, "subject_ref": map[string]any{"type": "string"},
		"mode": map[string]any{"type": "string", "enum": workModes}, "summary": map[string]any{"type": "string"},
		"work_id": map[string]any{"type": "string"}, "session": map[string]any{"type": "string"},
		"ttl": map[string]any{"type": "string"}, "report_valid_for": map[string]any{"type": "string"},
		"recipients":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"classification": map[string]any{"type": "string"},
		"actor": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"kind": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
				"model": map[string]any{"type": "string"}, "session": map[string]any{"type": "string"},
			},
		},
		"waiting_on": map[string]any{
			"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"kind", "id"},
				"properties": map[string]any{
					"kind": map[string]any{"type": "string", "enum": workWaitKinds},
					"id":   map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"},
				},
			},
		},
		"result":          map[string]any{"type": "string", "enum": []string{"finished", "paused"}},
		"include_expired": map[string]any{"type": "boolean"},
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": properties,
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("mcp: marshal a2a_work schema: %v", err))
	}
	return raw
}
