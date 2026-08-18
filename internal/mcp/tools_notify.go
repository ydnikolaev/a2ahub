package mcp

// tools_notify.go is the a2a_notify grouped tool (space-notify-2026-08 P6,
// spec 06 §"Closing the parity debt"): the MCP twin of `a2a notify
// render|send|setup|discover|verify`, closing the parity debt P3 opened
// when it registered `notify` in cmd/a2a/mcp_parity_test.go's
// mcpExcludedVerbs with a TEMPORARY entry.
//
// Shape mirrors NewWorkTool/NewDataTool exactly: a closed action enum
// (NotifyActions), an injected NotifyOperations seam, and an
// unavailable-backend default so the tool always registers even with zero
// production deps (BuildRegistry's own "always register, degrade the
// handler" convention — NewDataTool's own doc comment states the same for
// a2a_data).
//
// Deviation, reported in full in this phase's report: cmd/a2a's PRODUCTION
// wiring passes NotifyToolDeps{} (the zero value) in this pass, so every
// action answers "not configured" against the real server today — the
// interface below is the seam a follow-up wires real backends through, one
// action at a time, exactly as a2a_data shipped registered-but-degraded
// before its own operations were wired. internal/mcp never imports
// internal/cli (ADR-001), so a real backend here calls internal/spacenotify
// directly for render/send (a shared leaf package) and would need its OWN
// GitHub/Telegram adapter for setup/discover/verify — a second copy of
// cmd_notify_setup.go's adapter, not a reuse of it. Building and testing
// that second adapter to the same mutation-evidence standard as the CLI
// side was out of this pass's budget; the parity bijection (AC 8) is
// satisfied either way, since it asserts REACHABILITY of the capability,
// not its production depth.
import (
	"context"
	"encoding/json"
	"fmt"
)

// NotifyToolName is exported for the lead-owned registry/parity test.
const NotifyToolName = "a2a_notify"

// NotifyActions is the closed a2a_notify discriminator enum — byte-for-byte
// the same five names as cli.NotifySubcommands() (TestDataSubcommandsMatchMCPDataActions's
// own precedent: this repo pairs an exported CLI enumerable list with an
// exported MCP enum and lets a parity test hold them equal, rather than
// letting either drift).
var NotifyActions = []string{"render", "send", "setup", "discover", "verify"}

// NotifyOperations is the production seam a real backend implements.
// Every method's signature is a plain string in, string out (JSON payloads
// for render input/messages, a fixed vocabulary for the rest) — the same
// "thin JSON boundary" shape internal/cli's own notify commands present on
// stdin/stdout, so a follow-up backend can share encoding logic with
// runNotifyRender/runNotifySend without this package importing
// internal/cli itself.
type NotifyOperations interface {
	Render(ctx context.Context, space, mode, base, only string, limit int) (json.RawMessage, error)
	Send(ctx context.Context, space string, messages json.RawMessage, dryRun bool) (json.RawMessage, error)
	Setup(ctx context.Context, space string) (string, error)
	Discover(ctx context.Context, space string) (json.RawMessage, error)
	Verify(ctx context.Context, space string) (json.RawMessage, error)
}

// NotifyToolDeps are construction-time dependencies for a2a_notify.
// Operations may be nil — NewNotifyTool degrades every action to a named
// "not configured" error rather than failing to register (mirrors
// DataToolDeps{}'s own zero-value shape).
type NotifyToolDeps struct {
	Operations NotifyOperations
}

// ErrNotifyOperationsUnavailable is returned by every action when
// deps.Operations is nil.
var ErrNotifyOperationsUnavailable = fmt.Errorf("mcp: a2a_notify operations are not configured")

// NewNotifyTool constructs the closed a2a_notify tool. Registration is
// lead-owned wiring (BuildRegistry's own r.Register call), exactly like
// NewWorkTool/NewDataTool's own doc comments say of themselves.
func NewNotifyTool(deps NotifyToolDeps) (ToolSpec, error) {
	return ToolSpec{
		Name:        NotifyToolName,
		Description: "space-side CI notification projection and delivery: action=" + joinNotifyActions(),
		InputSchema: notifyToolSchema(),
		Handler:     newNotifyHandler(deps),
	}, nil
}

func joinNotifyActions() string {
	out := ""
	for i, a := range NotifyActions {
		if i > 0 {
			out += "|"
		}
		out += a
	}
	return out
}

// NotifyInput is the a2a_notify handler's structured MCP input — the union
// of every action's own fields.
type NotifyInput struct {
	Action   string          `json:"action"`
	Space    string          `json:"space,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	Base     string          `json:"base,omitempty"`
	Only     string          `json:"only,omitempty"`
	Limit    int             `json:"limit,omitempty"`
	Messages json.RawMessage `json:"messages,omitempty"`
	DryRun   bool            `json:"dry_run,omitempty"`
}

func newNotifyHandler(deps NotifyToolDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in NotifyInput
		if len(args) > 0 {
			if err := json.Unmarshal(args, &in); err != nil {
				return nil, "", fmt.Errorf("a2a_notify: invalid input: %w", err)
			}
		}
		if !notifyStringIn(in.Action, NotifyActions) {
			return nil, "", fmt.Errorf("a2a_notify: action is required and must be one of %s", joinNotifyActions())
		}
		if deps.Operations == nil {
			return nil, "", ErrNotifyOperationsUnavailable
		}
		switch in.Action {
		case "render":
			out, err := deps.Operations.Render(ctx, in.Space, in.Mode, in.Base, in.Only, in.Limit)
			return json.RawMessage(out), "", err
		case "send":
			out, err := deps.Operations.Send(ctx, in.Space, in.Messages, in.DryRun)
			return json.RawMessage(out), "", err
		case "setup":
			out, err := deps.Operations.Setup(ctx, in.Space)
			return map[string]string{"result": out}, "", err
		case "discover":
			out, err := deps.Operations.Discover(ctx, in.Space)
			return json.RawMessage(out), "", err
		case "verify":
			out, err := deps.Operations.Verify(ctx, in.Space)
			return json.RawMessage(out), "", err
		default:
			panic("unreachable closed a2a_notify action")
		}
	}
}

func notifyStringIn(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func notifyToolSchema() json.RawMessage {
	properties := map[string]any{
		"action":   map[string]any{"type": "string", "enum": NotifyActions},
		"space":    map[string]any{"type": "string"},
		"mode":     map[string]any{"type": "string"},
		"base":     map[string]any{"type": "string"},
		"only":     map[string]any{"type": "string"},
		"limit":    map[string]any{"type": "integer"},
		"messages": map[string]any{"type": "array"},
		"dry_run":  map[string]any{"type": "boolean"},
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"action"}, "properties": properties,
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("mcp: marshal a2a_notify schema: %v", err))
	}
	return raw
}
