package mcp

// OP-211 lifecycle verb tools (a2a_ack .. a2a_note) — the §7.7-enumerated
// tool set's largest family. Mirrors internal/cli's cmd_lifecycle.go verb
// set exactly (19 verbs: 15 generic table-driven + respond/verify/dispute/
// note), duplicating only the OUTER shape (structured JSON input instead
// of flag.FlagSet, HandlerFunc instead of cli.Command) — the inner
// legality/event-authoring/funnel-submit logic is this file's own call
// into eventdoc.go's shared helpers, itself a projection of the SAME
// event/v1 schema the CLI projects (plan 14 Placement decisions).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"gopkg.in/yaml.v3"
)

// lifecycleVerbSpec is one OP-211 generic verb's own row (mirrors
// internal/cli's lifecycleVerbSpec).
type lifecycleVerbSpec struct {
	Verb              string
	Transition        string
	RequireReason     bool
	RequireReasonCode bool
	RequireRefs       bool
	RequireFindings   bool
	GateMarker        bool
}

// LifecycleVerbTable is the SSOT of every OP-211 generic (table-driven)
// verb this package registers a tool for — exported so tools.go (the
// registry-construction file) can iterate it without a second, drifting
// copy.
var LifecycleVerbTable = []lifecycleVerbSpec{
	{Verb: "ack", Transition: fold.TAcknowledge},
	{Verb: "accept", Transition: fold.TAccept},
	{Verb: "decline", Transition: fold.TDecline, RequireReason: true, RequireReasonCode: true},
	{Verb: "start", Transition: fold.TStart},
	{Verb: "block", Transition: fold.TBlock, RequireRefs: true},
	{Verb: "unblock", Transition: fold.TUnblock},
	{Verb: "cancel", Transition: fold.TCancel},
	{Verb: "close", Transition: fold.TClose},
	{Verb: "withdraw", Transition: fold.TWithdraw},
	{Verb: "supersede", Transition: fold.TSupersede, RequireRefs: true},
	{Verb: "satisfy", Transition: fold.TSatisfy, RequireRefs: true},
	{Verb: "approve", Transition: fold.TApprove, GateMarker: true},
	{Verb: "reject", Transition: fold.TReject, RequireReason: true, GateMarker: true},
	{Verb: "verify-pass", Transition: fold.TVerifyPass},
	{Verb: "verify-fail", Transition: fold.TVerifyFail, RequireFindings: true},
}

// LifecycleInput is the structured input every generic OP-211 verb tool
// takes (structured form replaces the CLI's flag parsing, per plan 14
// Brief item 4): N ids batched into one commit/one PR.
type LifecycleInput struct {
	IDs        []string   `json:"ids"`
	Reason     string     `json:"reason,omitempty"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Refs       []string   `json:"refs,omitempty"`
	Findings   string     `json:"findings,omitempty"`
	Actor      ActorInput `json:"actor,omitempty"`
}

// newLifecycleHandler builds the table-driven generic verb handler for
// spec (mirrors internal/cli's LifecycleCommand.Run).
func newLifecycleHandler(spec lifecycleVerbSpec, deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in LifecycleInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("%s: invalid input: %w", spec.Verb, err)
		}
		if len(in.IDs) == 0 {
			return nil, "", fmt.Errorf("%s: ids is required", spec.Verb)
		}
		if spec.RequireReason && in.Reason == "" {
			return nil, "", fmt.Errorf("%s: reason is required", spec.Verb)
		}
		if spec.RequireRefs && len(in.Refs) == 0 {
			return nil, "", fmt.Errorf("%s: refs is required", spec.Verb)
		}
		if spec.RequireFindings && in.Findings == "" {
			return nil, "", fmt.Errorf("%s: findings is required", spec.Verb)
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", spec.Verb, err)
		}

		var files []space.FileWrite
		for _, id := range in.IDs {
			verdict, _, err := checkLegality(deps.MirrorDir, deps.Manifest, id, spec.Transition, actor)
			if err != nil {
				return nil, "", fmt.Errorf("%s: %s: %w", spec.Verb, id, err)
			}
			if verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("%s: %w", spec.Verb, verdictError(id, verdict))
			}
			_, probe, err := loadEnvelope(deps.MirrorDir, id)
			if err != nil {
				return nil, "", fmt.Errorf("%s: %s: %w", spec.Verb, id, err)
			}

			eventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("%s: cannot mint event id: %w", spec.Verb, err)
			}
			ev := eventDoc{
				Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
				Subject: id, Transition: spec.Transition,
				Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
				At:    now.UTC().Format(time.RFC3339),
			}
			if in.Reason != "" {
				ev.Note = in.Reason
			}
			if in.ReasonCode != "" {
				ev.ReasonCode = in.ReasonCode
			}
			if len(in.Refs) > 0 {
				ev.Refs = refsFromList(in.Refs)
			}
			if in.Findings != "" {
				ev.Note = in.Findings
			}
			raw, merr := yaml.Marshal(ev)
			if merr != nil {
				return nil, "", fmt.Errorf("%s: cannot encode event for %s: %w", spec.Verb, id, merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
		}

		req := deps.buildRequest(in.IDs, files, spec.Verb, spec.GateMarker)
		result, err := deps.submit(ctx, req, spec.Verb, in.IDs)
		return result, "", err
	}
}

// refsFromList builds event/v1 refs entries from a structured refs array
// (the CLI's comma-separated --refs flag's structured-input equivalent).
func refsFromList(refs []string) []refEntry {
	out := make([]refEntry, 0, len(refs))
	for _, r := range refs {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, refEntry{Ref: r})
		}
	}
	return out
}

// --- respond --------------------------------------------------------------

// RespondInput is a2a_respond's structured input (mirrors internal/cli's
// RespondCommand flags).
type RespondInput struct {
	ParentIDs    []string          `json:"parent_ids"`
	Result       string            `json:"result"`
	Fields       map[string]string `json:"fields,omitempty"`
	BodyOverride string            `json:"body_override,omitempty"`
	Actor        ActorInput        `json:"actor,omitempty"`
}

func newRespondHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in RespondInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("respond: invalid input: %w", err)
		}
		if len(in.ParentIDs) == 0 {
			return nil, "", fmt.Errorf("respond: parent_ids is required")
		}
		switch in.Result {
		case "answered", "delivered", "partial", "cannot":
		default:
			return nil, "", fmt.Errorf("respond: result must be one of answered|delivered|partial|cannot")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("respond: %w", err)
		}

		// A nil bodyOverride (not merely an empty-but-non-nil slice)
		// leaves the canonical template's own default placeholder body
		// untouched (template.Render's own contract: `in.Body != nil`
		// triggers an override) — an unconditional []byte(in.BodyOverride)
		// conversion of an empty string still yields a non-nil slice and
		// would silently WIPE the template's default body whenever the
		// caller omits body_override, unlike the CLI's own respond verb
		// (which only sets bodyOverride when --body-file is given).
		var bodyOverride []byte
		if in.BodyOverride != "" {
			bodyOverride = []byte(in.BodyOverride)
		}

		var files []space.FileWrite
		var ids []string
		for _, parentID := range in.ParentIDs {
			verdict, _, err := checkLegality(deps.MirrorDir, deps.Manifest, parentID, fold.TRespond, actor)
			if err != nil {
				return nil, "", fmt.Errorf("respond: %s: %w", parentID, err)
			}
			if verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("respond: %w", verdictError(parentID, verdict))
			}
			_, parentProbe, err := loadEnvelope(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("respond: %s: %w", parentID, err)
			}

			respFields := map[string]string{}
			for k, v := range in.Fields {
				respFields[k] = v
			}
			respFields["parent"] = parentID
			respFields["result"] = in.Result
			if _, has := respFields["from"]; !has {
				respFields["from"] = deps.OwnSystem
			}

			seed := respondSeed(parentID, in.Result, respFields, bodyOverride, actor)
			responseID, err := artifact.MintExchangeIDAt("XS", deps.OwnSystem, now, bytes.NewReader(seed))
			if err != nil {
				return nil, "", fmt.Errorf("respond: cannot mint response id: %w", err)
			}
			draft, err := template.Render(template.Input{
				Type: "response", ID: responseID, Actor: resolved, Created: now,
				Fields: respFields, Body: bodyOverride,
			})
			if err != nil {
				return nil, "", fmt.Errorf("respond: render failed for %s: %w", parentID, err)
			}
			files = append(files, space.FileWrite{Path: layout.Exchange(responseID), Content: draft})

			respondEventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("respond: cannot mint event id: %w", err)
			}
			respondEvent := eventDoc{
				Schema: "event/v1", Event: respondEventID.String(), Space: parentProbe.Space,
				Subject: parentID, Transition: fold.TRespond,
				Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
				At:    now.UTC().Format(time.RFC3339),
				Refs:  []refEntry{{Ref: responseID}},
			}
			respondRaw, merr := yaml.Marshal(respondEvent)
			if merr != nil {
				return nil, "", fmt.Errorf("respond: cannot encode respond event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), respondEventID.String()), Content: respondRaw})

			ids = append(ids, parentID, responseID)
		}

		req := deps.buildRequest(ids, files, "respond", false)
		result, err := deps.submit(ctx, req, "respond", ids)
		return result, "", err
	}
}

// respondSeed builds respond's own canonical, content-derived seed
// (mirrors internal/cli's lifecycleRespondSeed exactly — fixed-order join,
// SORTED field keys, no `now`).
func respondSeed(parentID, result string, respFields map[string]string, bodyOverride []byte, actor fold.Actor) []byte {
	keys := make([]string, 0, len(respFields))
	for k := range respFields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("parent=" + parentID + "\n")
	buf.WriteString("result=" + result + "\n")
	for _, k := range keys {
		buf.WriteString(k + "=" + respFields[k] + "\n")
	}
	buf.WriteString("body=")
	buf.Write(bodyOverride)
	buf.WriteString("\n")
	buf.WriteString("actor.kind=" + actor.Kind + "\n")
	buf.WriteString("actor.name=" + actor.Name + "\n")
	buf.WriteString("actor.system=" + actor.System + "\n")

	sum := sha256.Sum256(buf.Bytes())
	return sum[:]
}

// --- verify (response-scoped, D-024 single-response convenience close) --

// VerifyInput is a2a_verify's structured input.
type VerifyInput struct {
	Targets []string   `json:"targets"`
	Refs    string     `json:"refs,omitempty"`
	Actor   ActorInput `json:"actor,omitempty"`
}

func newVerifyHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in VerifyInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("verify: invalid input: %w", err)
		}
		if len(in.Targets) == 0 {
			return nil, "", fmt.Errorf("verify: targets is required")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("verify: %w", err)
		}

		var files []space.FileWrite
		var ids []string
		for _, target := range in.Targets {
			responseID, err := resolveResponseID(deps.MirrorDir, target, in.Refs)
			if err != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", target, err)
			}

			verdict, _, parentID, result, err := checkResponseLegality(deps.MirrorDir, deps.Manifest, responseID, fold.TVerify, actor)
			if err != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", responseID, err)
			}
			if verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("verify: %w", verdictError(responseID, verdict))
			}
			_, parentProbe, err := loadEnvelope(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("verify: %s: %w", parentID, err)
			}

			verifyEventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("verify: cannot mint event id: %w", err)
			}
			verifyEvent := eventDoc{
				Schema: "event/v1", Event: verifyEventID.String(), Space: parentProbe.Space,
				Subject: responseID, Transition: fold.TVerify,
				Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
				At:    now.UTC().Format(time.RFC3339),
			}
			verifyRaw, merr := yaml.Marshal(verifyEvent)
			if merr != nil {
				return nil, "", fmt.Errorf("verify: cannot encode event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), verifyEventID.String()), Content: verifyRaw})
			ids = append(ids, responseID)

			// D-024 convenience: single-response exchange also closes the
			// parent in the SAME PR.
			if len(result.Responses) == 1 {
				closeVerdict, _, cerr := checkLegality(deps.MirrorDir, deps.Manifest, parentID, fold.TClose, actor)
				if cerr != nil {
					return nil, "", fmt.Errorf("verify: %s: %w", parentID, cerr)
				}
				if closeVerdict != fold.VerdictLegal {
					continue
				}
				closeEventID, err := artifact.MintULIDAt(now, deps.Entropy)
				if err != nil {
					return nil, "", fmt.Errorf("verify: cannot mint event id: %w", err)
				}
				closeEvent := eventDoc{
					Schema: "event/v1", Event: closeEventID.String(), Space: parentProbe.Space,
					Subject: parentID, Transition: fold.TClose,
					Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
					At:    now.UTC().Format(time.RFC3339),
				}
				closeRaw, merr := yaml.Marshal(closeEvent)
				if merr != nil {
					return nil, "", fmt.Errorf("verify: cannot encode close event: %w", merr)
				}
				files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), closeEventID.String()), Content: closeRaw})
				ids = append(ids, parentID)
			}
		}

		req := deps.buildRequest(ids, files, "verify", false)
		result, err := deps.submit(ctx, req, "verify", ids)
		return result, "", err
	}
}

// --- dispute (response-scoped) -------------------------------------------

// DisputeInput is a2a_dispute's structured input.
type DisputeInput struct {
	IDs        []string   `json:"ids"`
	Reason     string     `json:"reason"`
	ReasonCode string     `json:"reason_code,omitempty"`
	Actor      ActorInput `json:"actor,omitempty"`
}

func newDisputeHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in DisputeInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("dispute: invalid input: %w", err)
		}
		if len(in.IDs) == 0 || in.Reason == "" {
			return nil, "", fmt.Errorf("dispute: ids and reason are required")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("dispute: %w", err)
		}

		var files []space.FileWrite
		for _, responseID := range in.IDs {
			verdict, _, parentID, _, err := checkResponseLegality(deps.MirrorDir, deps.Manifest, responseID, fold.TDispute, actor)
			if err != nil {
				return nil, "", fmt.Errorf("dispute: %s: %w", responseID, err)
			}
			if verdict != fold.VerdictLegal {
				return nil, "", fmt.Errorf("dispute: %w", verdictError(responseID, verdict))
			}
			_, parentProbe, err := loadEnvelope(deps.MirrorDir, parentID)
			if err != nil {
				return nil, "", fmt.Errorf("dispute: %s: %w", parentID, err)
			}

			eventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("dispute: cannot mint event id: %w", err)
			}
			ev := eventDoc{
				Schema: "event/v1", Event: eventID.String(), Space: parentProbe.Space,
				Subject: responseID, Transition: fold.TDispute,
				Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
				At:    now.UTC().Format(time.RFC3339),
				Note:  in.Reason, ReasonCode: in.ReasonCode,
			}
			raw, merr := yaml.Marshal(ev)
			if merr != nil {
				return nil, "", fmt.Errorf("dispute: cannot encode event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
		}

		req := deps.buildRequest(in.IDs, files, "dispute", false)
		result, err := deps.submit(ctx, req, "dispute", in.IDs)
		return result, "", err
	}
}

// --- note (transition-free, D-025) ---------------------------------------

// NoteInput is a2a_note's structured input.
type NoteInput struct {
	IDs   []string   `json:"ids"`
	Note  string     `json:"note"`
	Actor ActorInput `json:"actor,omitempty"`
}

func newNoteHandler(deps WriteDeps) HandlerFunc {
	return func(ctx context.Context, args json.RawMessage) (any, string, error) {
		var in NoteInput
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, "", fmt.Errorf("note: invalid input: %w", err)
		}
		if len(in.IDs) == 0 || in.Note == "" {
			return nil, "", fmt.Errorf("note: ids and note are required")
		}

		resolved := deps.ResolveActor(in.Actor)
		actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: deps.OwnSystem}

		now := deps.Now()
		layout, err := space.NewLayout(deps.OwnSystem)
		if err != nil {
			return nil, "", fmt.Errorf("note: %w", err)
		}

		var files []space.FileWrite
		for _, id := range in.IDs {
			_, probe, err := loadEnvelope(deps.MirrorDir, id)
			if err != nil {
				return nil, "", fmt.Errorf("note: %s: %w", id, err)
			}
			eventID, err := artifact.MintULIDAt(now, deps.Entropy)
			if err != nil {
				return nil, "", fmt.Errorf("note: cannot mint event id: %w", err)
			}
			ev := eventDoc{
				Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
				Subject: id, Transition: fold.TNote,
				Actor: eventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
				At:    now.UTC().Format(time.RFC3339),
				Note:  in.Note,
			}
			raw, merr := yaml.Marshal(ev)
			if merr != nil {
				return nil, "", fmt.Errorf("note: cannot encode event: %w", merr)
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
		}

		req := deps.buildRequest(in.IDs, files, "note", false)
		result, err := deps.submit(ctx, req, "note", in.IDs)
		return result, "", err
	}
}
