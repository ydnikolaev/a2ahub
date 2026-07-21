// OP-211 generic lifecycle verbs (spec 08 T1): ack/accept/decline/start/
// block/unblock/cancel/respond/verify/dispute/close/supersede/withdraw/
// satisfy/approve/reject/verify-pass/verify-fail/note. Every mutating verb
// batches N ids into one commit/one PR, runs V2 legality locally (via
// internal/fold, reused — never re-derived) BEFORE the funnel, and ships
// through the SAME uniform write funnel (auto-merge always on; no verb
// passes a gate/review parameter — approve/reject add an advisory PR
// marker only, per this phase's plan Placement decisions).
//
// This file's only package-level symbols are the per-verb command types
// (LifecycleCommand, RespondCommand, VerifyCommand, DisputeCommand,
// NoteCommand) + their NewXCommand constructors, the lifecycleVerbTable
// (Future-proofing table, §9) and file-private, uniquely-named helpers
// (lifecycle* prefix) — no shared helper, no package var beyond that
// table, per this phase's plan Placement decision (avoids collision with
// P7/P9's parallel verb files in this package). It never touches or
// imports P7's cmd_inbox/outbox/show/thread/search/statusline files, nor
// internal/cache.
package cli

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"gopkg.in/yaml.v3"
)

// lifecycleFunnel is this file's own narrow consumer-side seam over
// *space.WriteFunnel (rails ISP/DI; own name, deliberately not shared with
// cmd_submit.go's submitFunnel — "disjoint files" per this phase's plan
// Placement decision) — tests inject a hand-written fake.
type lifecycleFunnel interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// lifecycleEnvelopeProbe is this file's own minimal envelope decode — the
// fields every OP-211 verb's legality/event-authoring path needs (id,
// type-derived facts are resolved from the §3.3 id prefix instead, see
// lifecyclePrefixInfo). A response's own `parent` field is the closure
// model's linkage (§3.4.6): "a response MUST reference its parent
// exchange ID."
type lifecycleEnvelopeProbe struct {
	ID                string   `yaml:"id"`
	Space             string   `yaml:"space"`
	From              string   `yaml:"from"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
	Parent            string   `yaml:"parent"` // response only
}

// lifecyclePrefixInfo maps a §3.3 id prefix to its fold.Kind — the same
// table-driven idiom as cmd_submit.go's submitFirstTransition (Future-
// proofing table, §9: no per-type branch to hand-edit).
var lifecyclePrefixInfo = map[string]fold.Kind{
	"XC": fold.KindContract,
	"XR": fold.KindRequirement,
	"XQ": fold.KindQuestion,
	"XW": fold.KindWorkRequest,
	"XD": fold.KindDecision,
	"XH": fold.KindHandoff,
	"XS": fold.KindResponse,
	"XA": fold.KindAnnouncement,
}

// lifecycleArtifactPath resolves parsed's committed space-relative path
// per §4.2's layout (internal/space/layout.go) — this file's own copy of
// cmd_submit.go's submitSectionPath, keyed by id PREFIX rather than
// envelope `type` (an OP-211 verb reads an EXISTING artifact by id alone,
// before any envelope is available, unlike submit which already holds a
// parsed draft) — same known layout quirk (contract's fixed
// provides/<slug>/contract.md filename), not re-litigated here.
func lifecycleArtifactPath(parsed artifact.ID) (string, error) {
	switch parsed.Prefix {
	case "XC":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.ProvidesContract(parsed.Slug), nil
	case "XR":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.Requires(parsed.Raw), nil
	case "XD":
		return space.Decision(parsed.Raw), nil
	case "XQ", "XW", "XH", "XA", "XS":
		layout, err := space.NewLayout(parsed.System)
		if err != nil {
			return "", err
		}
		return layout.Exchange(parsed.Raw), nil
	default:
		return "", fmt.Errorf("cli: unknown artifact id prefix %q", parsed.Prefix)
	}
}

// lifecycleLoadEnvelope reads and parses id's committed artifact file from
// mirrorDir, returning fold's own minimal Envelope projection alongside
// this file's richer probe (for the `space`/`parent` fields fold.Envelope
// does not carry).
func lifecycleLoadEnvelope(mirrorDir, id string) (fold.Envelope, lifecycleEnvelopeProbe, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: %w", id, err)
	}
	kind, ok := lifecyclePrefixInfo[parsed.Prefix]
	if !ok {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: unknown artifact id prefix %q", id, parsed.Prefix)
	}
	relPath, err := lifecycleArtifactPath(parsed)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, err
	}
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: %w", id, err)
	}
	var probe lifecycleEnvelopeProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return fold.Envelope{}, lifecycleEnvelopeProbe{}, fmt.Errorf("cli: %s: cannot decode envelope: %w", id, err)
	}
	env := fold.Envelope{
		ID: id, Kind: kind, From: probe.From,
		To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
	}
	return env, probe, nil
}

// lifecycleEventDoc is this file's own minimal event/v1 document decode
// (reading back committed events for folding) AND encode (authoring new
// events) — a richer sibling of adapters.go's mirrorEvent/cmd_submit.go's
// submitEventDoc: this phase's verbs additionally need `refs` (the
// respond event's response-id linkage, since event/v1 has no dedicated
// `response_id` field — this phase's own resolution, see this phase's
// Deviations report) and `state`/`note`/`reason_code` (annotation and
// closed-enum reason fields §5.2.2).
type lifecycleEventDoc struct {
	Schema     string              `yaml:"schema"`
	Event      string              `yaml:"event"`
	Space      string              `yaml:"space"`
	Subject    string              `yaml:"subject"`
	Transition string              `yaml:"transition"`
	State      string              `yaml:"state,omitempty"`
	Actor      lifecycleEventActor `yaml:"actor"`
	At         string              `yaml:"at"`
	Note       string              `yaml:"note,omitempty"`
	Refs       []lifecycleRefEntry `yaml:"refs,omitempty"`
	ReasonCode string              `yaml:"reason_code,omitempty"`
	// Version is the contract-scope §5.2.2 optional field (cmd_contract.go
	// publish/deprecate/retire read and author it — shared here, not
	// duplicated, since both lifecycle and contract verbs decode/encode
	// the same event/v1 shape).
	Version string `yaml:"version,omitempty"`
	// Commit/Digest are the publish event's own §5.2.2 prose-only fields
	// ("publish events also record commit (SHA) + digest of the published
	// content"). Digest is set by cmd_contract.go's publish verb (computed
	// pre-commit, from content); Commit is deliberately left unset — the
	// real commit SHA is only known AFTER the write funnel returns
	// WriteResult.CommitSHA, i.e. after the event file already had to be
	// authored, and this phase (like P6's cmd_submit.go before it) does
	// not attempt a second, amending commit to backfill it. See this
	// phase's Deviations report.
	Commit string `yaml:"commit,omitempty"`
	Digest string `yaml:"digest,omitempty"`
}

type lifecycleEventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"`
}

type lifecycleRefEntry struct {
	Ref string `yaml:"ref"`
}

// lifecycleReadAllEvents walks every <system>/events/<year>/<ulid>.yaml
// under mirrorDir — EVERY participating system's own section, not just
// this binary's configured own system: a lifecycle event is committed to
// the ACTING system's own section (§3.5), which for many OP-211
// transitions (ack/accept/decline/block/respond/...) is the OTHER party,
// not the artifact's owning system. Folding a subject's current state
// correctly therefore requires the full cross-system event set, not one
// system's own slice (the narrower scope adapters.go's LegalityAdapter
// needed for P6's entry-transitions-only scope).
func lifecycleReadAllEvents(mirrorDir string) ([]lifecycleEventDoc, error) {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "events", "*", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("cli: list committed events: %w", err)
	}
	out := make([]lifecycleEventDoc, 0, len(matches))
	for _, m := range matches {
		raw, err := readBoundedFile(m, maxMirrorEventBytes)
		if err != nil {
			return nil, err
		}
		var ev lifecycleEventDoc
		if err := yaml.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("cli: decode committed event %s: %w", m, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// lifecycleFoldEvents selects, from every committed event, the ones
// relevant to primaryID's own fold: events whose Subject is primaryID
// itself, UNION events whose Subject is a response id attached to
// primaryID via a `respond` event's refs[0] (verify/dispute target the
// response, D-024 — Fold needs both the parent's own primary events and
// the response-scoped ones to compute Result.Responses correctly).
// Ordering falls back to ULID only (ascending) — this phase inherits
// P6/adapters.go's own simplification of never threading real commit
// order (CommitSeq) through the mirror read; ULIDs are monotonic on mint
// time, an accepted approximation for one still-open batch, documented as
// a deviation.
func lifecycleFoldEvents(all []lifecycleEventDoc, primaryID string) []fold.Event {
	responseIDs := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == primaryID && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			responseIDs[ev.Refs[0].Ref] = true
		}
	}
	var out []fold.Event
	for _, ev := range all {
		if ev.Subject != primaryID && !responseIDs[ev.Subject] {
			continue
		}
		fe := fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition,
			ClaimedState: fold.State(ev.State),
			Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
		}
		if ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			fe.ResponseID = ev.Refs[0].Ref
		}
		out = append(out, fe)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ULID < out[j].ULID })
	return out
}

// lifecycleMembership adapts a space.Manifest into a fold.MembershipView —
// this file's own copy of adapters.go's LegalityAdapter.membershipView
// (same tiny logic, kept file-private per the "disjoint files" Placement
// decision rather than exported cross-file).
func lifecycleMembership(manifest space.Manifest) fold.MembershipView {
	return func(system string) fold.MembershipStatus {
		for _, p := range manifest.Participants {
			if p.System == system {
				if p.Status == "left" {
					return fold.MembershipLeft
				}
				return fold.MembershipMember
			}
		}
		return fold.MembershipUnknown
	}
}

// lifecycleCheckLegality is the generic (non-response-scoped) pre-write
// legality check every OP-211 verb except verify/dispute uses: read id's
// own committed envelope + full event history, fold to its current state,
// and delegate to fold.CheckLegality — never re-deriving §3.4 locally.
func lifecycleCheckLegality(mirrorDir string, manifest space.Manifest, id, transition string, actor fold.Actor) (fold.Verdict, fold.Envelope, error) {
	env, _, err := lifecycleLoadEnvelope(mirrorDir, id)
	if err != nil {
		return "", fold.Envelope{}, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return "", env, err
	}
	events := lifecycleFoldEvents(all, id)
	membership := lifecycleMembership(manifest)

	var state fold.State
	if len(events) == 0 {
		state = fold.NewResult(env.Kind).State
	} else {
		state = fold.Fold(env.Kind, env, events, membership).State
	}
	actorStatus := membership(actor.System)
	return fold.CheckLegality(env.Kind, state, transition, env, actor, actorStatus), env, nil
}

// lifecycleCheckResponseLegality is the verify/dispute pre-write legality
// check (spec 08 Placement decisions, binding): the subject is a RESPONSE,
// so it resolves the response's own closure sub-state (Result.Responses,
// folded from the PARENT's full history) and the PARENT's own envelope
// (RoleOwner resolves to the original requester) — mirroring
// internal/fold's own applyResponseScoped exactly, at pre-write time via
// the new fold.CheckLegality branch this phase adds.
func lifecycleCheckResponseLegality(mirrorDir string, manifest space.Manifest, responseID, transition string, actor fold.Actor) (fold.Verdict, fold.Envelope, string, fold.Result, error) {
	_, responseProbe, err := lifecycleLoadEnvelope(mirrorDir, responseID)
	if err != nil {
		return "", fold.Envelope{}, "", fold.Result{}, err
	}
	parentID := responseProbe.Parent
	if parentID == "" {
		return "", fold.Envelope{}, "", fold.Result{}, fmt.Errorf("cli: response %s carries no `parent` link", responseID)
	}
	parentEnv, _, err := lifecycleLoadEnvelope(mirrorDir, parentID)
	if err != nil {
		return "", fold.Envelope{}, "", fold.Result{}, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return "", parentEnv, parentID, fold.Result{}, err
	}
	events := lifecycleFoldEvents(all, parentID)
	membership := lifecycleMembership(manifest)
	result := fold.Fold(parentEnv.Kind, parentEnv, events, membership)

	substate := fold.State("")
	if result.Responses != nil {
		substate = result.Responses[responseID]
	}
	actorStatus := membership(actor.System)
	verdict := fold.CheckLegality(fold.KindResponse, substate, transition, parentEnv, actor, actorStatus)
	return verdict, parentEnv, parentID, result, nil
}

// lifecycleRefsFromFlag splits a comma-separated --refs value into
// event/v1 refs entries (§5.2.2's `refs[].ref`); an empty value yields no
// entries.
func lifecycleRefsFromFlag(v string) []lifecycleRefEntry {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]lifecycleRefEntry, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, lifecycleRefEntry{Ref: p})
		}
	}
	return out
}

// verdictRefusalMessage renders a human-readable local-refusal message for
// a non-legal fold.Verdict, naming the LFC- registry code (rails: "every
// violation carries a non-empty registry code" applies to any V2-class
// refusal surfaced to a human, CLI or otherwise).
func verdictRefusalMessage(id string, verdict fold.Verdict) string {
	switch verdict {
	case fold.VerdictIllegalTransition:
		return fmt.Sprintf("%s: refused: illegal transition for the current folded state (LFC-001)", id)
	case fold.VerdictUnauthorizedActor:
		return fmt.Sprintf("%s: refused: actor is not authorized for this transition (LFC-002)", id)
	default:
		return fmt.Sprintf("%s: refused: unknown verdict %v", id, verdict)
	}
}

// --- shared verb plumbing (constructor DI) -------------------------------

// lifecycleDeps is the common constructor-injected dependency set every
// OP-211 verb command needs — factored out so each NewXCommand constructor
// stays a short, readable wrapper (rails DI, anti-pattern #10: every field
// here is required).
type lifecycleDeps struct {
	funnel       lifecycleFunnel
	mirrorDir    string
	spaceID      string
	ownSystem    string
	manifest     space.Manifest
	hostCfg      SubmitHostConfig
	resolveActor func(ActorFlags) template.Actor

	now      func() time.Time
	entropy  io.Reader
	readFile func(path string) ([]byte, error)
}

func newLifecycleDeps(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) lifecycleDeps {
	return lifecycleDeps{
		funnel: funnel, mirrorDir: mirrorDir, spaceID: spaceID, ownSystem: ownSystem,
		manifest: manifest, hostCfg: hostCfg, resolveActor: resolveActor,
		now: time.Now, entropy: rand.Reader, readFile: os.ReadFile,
	}
}

// lifecycleActorFlags registers the §7.4 explicit-actor-override flags
// every verb accepts (same three flags NewCommand already registers) onto
// fs, returning pointers Run reads after fs.Parse.
func lifecycleActorFlags(fs *flag.FlagSet) (kind, name, model *string) {
	kind = fs.String("actor-kind", "", "explicit actor.kind override")
	name = fs.String("actor-name", "", "explicit actor.name override")
	model = fs.String("actor-model", "", "explicit actor.model override")
	return
}

func (d lifecycleDeps) buildRequest(ids []string, files []space.FileWrite, verb string, gated bool) space.SubmitRequest {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	commitMsg := fmt.Sprintf("a2a(%s): %s", verb, strings.Join(sorted, ", "))
	baseBranch := d.hostCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	var prBody string
	if gated {
		prBody = fmt.Sprintf("ADVISORY GATE: %s requires an approving CODEOWNERS review before auto-merge (§3.7 G3).", verb)
	}
	return space.SubmitRequest{
		RepoDir: d.mirrorDir, System: d.ownSystem,
		ArtifactID: strings.Join(sorted, "+"), Files: files,
		CommitMessage: commitMsg, CommitAuthorName: d.hostCfg.CommitAuthorName, CommitAuthorEmail: d.hostCfg.CommitAuthorEmail,
		RemoteURL: d.hostCfg.RemoteURL, Repo: d.hostCfg.Repo, BaseBranch: baseBranch,
		PRTitle: commitMsg, PRBody: prBody, Credential: d.hostCfg.Credential,
	}
}

func (d lifecycleDeps) submit(ctx context.Context, req space.SubmitRequest, verb string, ids []string, stdio IO) int {
	result, err := d.funnel.Submit(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", verb, err)
		return 1
	}
	switch result.State {
	case space.WriteStateAlreadyOpen, space.WriteStateAlreadyMerged:
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: already submitted (PR %s, %s)\n", verb, result.PRURL, result.State)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: opened PR %s for %s (%s)\n", verb, result.PRURL, strings.Join(ids, ", "), result.State)
	}
	return 0
}

// --- generic OP-211 verbs (table-driven) ---------------------------------

// lifecycleVerbSpec is one OP-211 generic verb's own row (Future-proofing
// table, §9: a new §3.4 transition slots in as a new row, never a new
// branch). Every field mirrors spec 08 T1's flags column exactly.
type lifecycleVerbSpec struct {
	Verb              string
	Transition        string
	Synopsis          string
	RequireReason     bool
	RequireReasonCode bool
	RequireRefs       bool
	RequireFindings   bool
	GateMarker        bool // ALWAYS advisory-gated (approve/reject, G3)
}

var lifecycleVerbTable = []lifecycleVerbSpec{
	{Verb: "ack", Transition: fold.TAcknowledge, Synopsis: "acknowledge one or more artifacts"},
	{Verb: "accept", Transition: fold.TAccept, Synopsis: "accept one or more artifacts"},
	{Verb: "decline", Transition: fold.TDecline, Synopsis: "decline one or more artifacts", RequireReason: true, RequireReasonCode: true},
	{Verb: "start", Transition: fold.TStart, Synopsis: "start work on one or more artifacts"},
	{Verb: "block", Transition: fold.TBlock, Synopsis: "block one or more artifacts on a blocker", RequireRefs: true},
	{Verb: "unblock", Transition: fold.TUnblock, Synopsis: "unblock one or more artifacts (recovers pre-block state)"},
	{Verb: "cancel", Transition: fold.TCancel, Synopsis: "cancel one or more artifacts"},
	{Verb: "close", Transition: fold.TClose, Synopsis: "close one or more responded parents"},
	{Verb: "withdraw", Transition: fold.TWithdraw, Synopsis: "withdraw one or more requirements"},
	{Verb: "supersede", Transition: fold.TSupersede, Synopsis: "supersede an artifact with its successor", RequireRefs: true},
	{Verb: "satisfy", Transition: fold.TSatisfy, Synopsis: "satisfy a requirement", RequireRefs: true},
	{Verb: "approve", Transition: fold.TApprove, Synopsis: "approve a decision (always G3-gated)", GateMarker: true},
	{Verb: "reject", Transition: fold.TReject, Synopsis: "reject a decision (always G3-gated)", RequireReason: true, GateMarker: true},
	{Verb: "verify-pass", Transition: fold.TVerifyPass, Synopsis: "record a passing handoff verification"},
	{Verb: "verify-fail", Transition: fold.TVerifyFail, Synopsis: "record a failing handoff verification", RequireFindings: true},
}

// LifecycleCommand implements every table-driven OP-211 generic verb: N
// ids batched into one commit/one PR, V2 legality refusal locally BEFORE
// the funnel (AC-302.1), the SAME uniform funnel call (no gate parameter —
// approve/reject add an advisory PR marker only, P8-3).
type LifecycleCommand struct {
	spec lifecycleVerbSpec
	deps lifecycleDeps
}

// newLifecycleCommand is every generic-verb NewXCommand constructor's
// shared body (table-driven, §9 Future-proofing — one place to extend
// when a new §3.4 transition needs an OP-211 verb).
func newLifecycleCommand(spec lifecycleVerbSpec, funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return &LifecycleCommand{spec: spec, deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// NewAckCommand constructs `a2a ack <id...>`. funnel, manifest and
// resolveActor must not be nil/zero-configured (rails anti-pattern #10).
func NewAckCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[0], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewAcceptCommand constructs `a2a accept <id...>`.
func NewAcceptCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[1], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewDeclineCommand constructs `a2a decline <id...> --reason <text> [--reason-code <enum>]`.
func NewDeclineCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[2], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewStartCommand constructs `a2a start <id...>`.
func NewStartCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[3], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewBlockCommand constructs `a2a block <id...> --refs <blocker-id>`.
func NewBlockCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[4], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewUnblockCommand constructs `a2a unblock <id...>`.
func NewUnblockCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[5], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewCancelCommand constructs `a2a cancel <id...>`.
func NewCancelCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[6], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewCloseCommand constructs `a2a close <parent-id...>`.
func NewCloseCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[7], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewWithdrawCommand constructs `a2a withdraw <requirement-id...>`.
func NewWithdrawCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[8], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewSupersedeCommand constructs `a2a supersede <id> --refs <successor-id>`.
func NewSupersedeCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[9], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewSatisfyCommand constructs `a2a satisfy <requirement-id> --refs <XC-id@version>,<XS-id>`.
func NewSatisfyCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[10], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewApproveCommand constructs `a2a approve <decision-id>` (ALWAYS G3-gated, P8-3).
func NewApproveCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[11], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewRejectCommand constructs `a2a reject <decision-id> --reason <text>` (ALWAYS G3-gated, P8-3).
func NewRejectCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[12], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewVerifyPassCommand constructs `a2a verify-pass <handoff-id>`.
func NewVerifyPassCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[13], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewVerifyFailCommand constructs `a2a verify-fail <handoff-id> --findings <text>`.
func NewVerifyFailCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[14], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// Name implements cli.Command.
func (c *LifecycleCommand) Name() string { return c.spec.Verb }

// Synopsis implements cli.Command.
func (c *LifecycleCommand) Synopsis() string { return c.spec.Synopsis }

// Run implements cli.Command. Exit codes: 2 = usage; 1 = local legality
// refusal or a funnel/IO error (all-or-nothing across the batch, OP-220
// pattern); 0 = success.
func (c *LifecycleCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet(c.spec.Verb, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	reason := fs.String("reason", "", "reason text")
	reasonCode := fs.String("reason-code", "", "machine-readable reason code")
	refs := fs.String("refs", "", "comma-separated refs (blocker/successor/contract+response ids)")
	findings := fs.String("findings", "", "verification findings text")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids := fs.Args()
	if len(ids) == 0 {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s <id...>\n", c.spec.Verb)
		return 2
	}
	if c.spec.RequireReason && *reason == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s --reason <text> <id...>\n", c.spec.Verb)
		return 2
	}
	if c.spec.RequireRefs && *refs == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s --refs <ref,...> <id...>\n", c.spec.Verb)
		return 2
	}
	if c.spec.RequireFindings && *findings == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s --findings <text> <id...>\n", c.spec.Verb)
		return 2
	}

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, err)
		return 1
	}

	var files []space.FileWrite
	for _, id := range ids {
		verdict, env, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, id, c.spec.Transition, actor)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
			return 1
		}
		if verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s\n", c.spec.Verb, verdictRefusalMessage(id, verdict))
			return 1
		}

		_, probe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, id)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
			return 1
		}
		_ = env

		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: cannot mint event id: %v\n", c.spec.Verb, err)
			return 1
		}
		ev := lifecycleEventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: id, Transition: c.spec.Transition,
			Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
		}
		if *reason != "" {
			ev.Note = *reason
		}
		if *reasonCode != "" {
			ev.ReasonCode = *reasonCode
		}
		if *refs != "" {
			ev.Refs = lifecycleRefsFromFlag(*refs)
		}
		if *findings != "" {
			ev.Note = *findings
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: cannot encode event for %s: %v\n", c.spec.Verb, id, merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
	}

	req := c.deps.buildRequest(ids, files, c.spec.Verb, c.spec.GateMarker)
	return c.deps.submit(ctx, req, c.spec.Verb, ids, stdio)
}

var _ Command = (*LifecycleCommand)(nil)

// --- respond (scaffolds + submits an XS) ---------------------------------

// RespondCommand implements `a2a respond <parent-id...>`: scaffolds a new
// XS response artifact per parent (draft->submit collapsed, D-026) AND
// authors the parent's own `respond` event (linking via refs[0], see
// lifecycleEventDoc's own doc comment) — batch = N parents, one PR.
type RespondCommand struct {
	deps lifecycleDeps
}

// NewRespondCommand constructs the respond command.
func NewRespondCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *RespondCommand {
	return &RespondCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *RespondCommand) Name() string { return "respond" }

// Synopsis implements cli.Command.
func (c *RespondCommand) Synopsis() string {
	return "respond to one or more parents: respond --result <answered|delivered|partial|cannot> <parent-id...>"
}

// Run implements cli.Command.
func (c *RespondCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("respond", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	var fieldFlags newStringList
	fs.Var(&fieldFlags, "field", "k=v field override (repeatable)")
	bodyFile := fs.String("body-file", "", "path to a file whose contents replace the response body")
	result := fs.String("result", "", "answered|delivered|partial|cannot (required)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	parents := fs.Args()
	if len(parents) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a respond --result <answered|delivered|partial|cannot> <parent-id...>")
		return 2
	}
	switch *result {
	case "answered", "delivered", "partial", "cannot":
	default:
		_, _ = fmt.Fprintln(stdio.Stderr, "respond: --result must be one of answered|delivered|partial|cannot")
		return 2
	}
	fields, ferr := newParseFields(fieldFlags)
	if ferr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", ferr)
		return 2
	}
	var bodyOverride []byte
	if *bodyFile != "" {
		b, err := c.deps.readFile(*bodyFile)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot read --body-file: %v\n", err)
			return 1
		}
		bodyOverride = b
	}

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	var ids []string
	for _, parentID := range parents {
		verdict, _, err := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, parentID, fold.TRespond, actor)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
			return 1
		}
		if verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s\n", verdictRefusalMessage(parentID, verdict))
			return 1
		}
		_, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
			return 1
		}

		responseID, err := artifact.MintExchangeIDAt("XS", c.deps.ownSystem, now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot mint response id: %v\n", err)
			return 1
		}
		respFields := map[string]string{}
		for k, v := range fields {
			respFields[k] = v
		}
		respFields["parent"] = parentID
		respFields["result"] = *result
		if _, has := respFields["from"]; !has {
			respFields["from"] = c.deps.ownSystem
		}
		draft, err := template.Render(template.Input{
			Type: "response", ID: responseID, Actor: resolved, Created: now,
			Fields: respFields, Body: bodyOverride,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: render failed for %s: %v\n", parentID, err)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.Exchange(responseID), Content: draft})

		// NOTE (deviation, see this phase's report): §3.4.6 prose describes
		// a response's own "draft -> submit -> submitted" mini-lifecycle,
		// but internal/fold's Apply (fold.go, off-limits this phase — only
		// legality.go was granted) has no dispatch case for a response-
		// SUBJECT event outside verify/dispute; a literal second `submit`
		// event on the response would fall through to applyPrimaryScoped
		// keyed on the PARENT's own kind/state and be flagged illegal
		// (spurious noise), while contributing nothing — Result.Responses
		// is seeded to `submitted` entirely by the PARENT's own `respond`
		// event below (applyPrimaryScoped's TRespond handling), independent
		// of any response-owned event. This phase does not author that
		// second event; a future fold amendment could add the dispatch
		// case if a literal audit-trail event is later required.

		// Parent's own `respond` event, linking the new response via refs[0].
		respondEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot mint event id: %v\n", err)
			return 1
		}
		respondEvent := lifecycleEventDoc{
			Schema: "event/v1", Event: respondEventID.String(), Space: parentProbe.Space,
			Subject: parentID, Transition: fold.TRespond,
			Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
			Refs:  []lifecycleRefEntry{{Ref: responseID}},
		}
		respondRaw, merr := yaml.Marshal(respondEvent)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot encode respond event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), respondEventID.String()), Content: respondRaw})

		ids = append(ids, parentID, responseID)
	}

	req := c.deps.buildRequest(ids, files, "respond", false)
	return c.deps.submit(ctx, req, "respond", ids, stdio)
}

var _ Command = (*RespondCommand)(nil)

// --- verify (response-scoped, D-024 single-response convenience close) --

// VerifyCommand implements `a2a verify <response-id|parent-id>...
// [--refs <response-id>]`: verifies one or more responses; on a
// single-response exchange it ALSO emits `close` on the parent in the
// same PR (D-024 convenience) — with multiple responses, `close` stays a
// separate, deliberate act.
type VerifyCommand struct {
	deps lifecycleDeps
}

// NewVerifyCommand constructs the verify command.
func NewVerifyCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *VerifyCommand {
	return &VerifyCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *VerifyCommand) Name() string { return "verify" }

// Synopsis implements cli.Command.
func (c *VerifyCommand) Synopsis() string {
	return "verify one or more responses: verify <response-id|parent-id...> [--refs <response-id>]"
}

// Run implements cli.Command.
func (c *VerifyCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	refs := fs.String("refs", "", "response id (disambiguates a multi-response parent)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	targets := fs.Args()
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a verify <response-id|parent-id...> [--refs <response-id>]")
		return 2
	}

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	var ids []string
	for _, target := range targets {
		responseID, err := lifecycleResolveResponseID(c.deps.mirrorDir, c.deps.manifest, target, *refs)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", target, err)
			return 1
		}

		verdict, parentEnv, parentID, result, err := lifecycleCheckResponseLegality(c.deps.mirrorDir, c.deps.manifest, responseID, fold.TVerify, actor)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", responseID, err)
			return 1
		}
		if verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s\n", verdictRefusalMessage(responseID, verdict))
			return 1
		}
		_, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", parentID, err)
			return 1
		}

		verifyEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot mint event id: %v\n", err)
			return 1
		}
		verifyEvent := lifecycleEventDoc{
			Schema: "event/v1", Event: verifyEventID.String(), Space: parentProbe.Space,
			Subject: responseID, Transition: fold.TVerify,
			Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
		}
		verifyRaw, merr := yaml.Marshal(verifyEvent)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot encode event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), verifyEventID.String()), Content: verifyRaw})
		ids = append(ids, responseID)

		// D-024 convenience: single-response exchange also closes the
		// parent in the SAME PR. len(result.Responses) counts every
		// response tracked so far (this one included, already legal).
		if len(result.Responses) == 1 {
			closeVerdict, _, cerr := lifecycleCheckLegality(c.deps.mirrorDir, c.deps.manifest, parentID, fold.TClose, actor)
			if cerr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", parentID, cerr)
				return 1
			}
			if closeVerdict != fold.VerdictLegal {
				// Not this phase's business to force a close that isn't
				// legal (e.g. an already-superseded parent) — verify still
				// stands on its own merit; only the convenience is skipped.
				continue
			}
			closeEventID, err := artifact.MintULIDAt(now, c.deps.entropy)
			if err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot mint event id: %v\n", err)
				return 1
			}
			closeEvent := lifecycleEventDoc{
				Schema: "event/v1", Event: closeEventID.String(), Space: parentProbe.Space,
				Subject: parentID, Transition: fold.TClose,
				Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
				At:    now.UTC().Format(time.RFC3339),
			}
			closeRaw, merr := yaml.Marshal(closeEvent)
			if merr != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "verify: cannot encode close event: %v\n", merr)
				return 1
			}
			files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), closeEventID.String()), Content: closeRaw})
			ids = append(ids, parentID)
		}
		_ = parentEnv
	}

	req := c.deps.buildRequest(ids, files, "verify", false)
	return c.deps.submit(ctx, req, "verify", ids, stdio)
}

var _ Command = (*VerifyCommand)(nil)

// lifecycleResolveResponseID resolves verify's own `<response-id|parent-
// id>` ambiguity (spec 08 T1): a bare XS- id is used directly; anything
// else is treated as a parent id, whose single open response is looked up
// (refsFlag disambiguates when the parent has more than one).
func lifecycleResolveResponseID(mirrorDir string, _ space.Manifest, target, refsFlag string) (string, error) {
	parsed, err := artifact.ParseID(target)
	if err == nil && parsed.Prefix == "XS" {
		return target, nil
	}
	if refsFlag != "" {
		return refsFlag, nil
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return "", err
	}
	var candidates []string
	seen := map[string]bool{}
	for _, ev := range all {
		if ev.Subject == target && ev.Transition == fold.TRespond && len(ev.Refs) > 0 {
			rid := ev.Refs[0].Ref
			if !seen[rid] {
				seen[rid] = true
				candidates = append(candidates, rid)
			}
		}
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("cli: %s has no attached response", target)
	case 1:
		return candidates[0], nil
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("cli: %s has multiple responses (%s) — disambiguate with --refs", target, strings.Join(candidates, ", "))
	}
}

// --- dispute (response-scoped) -------------------------------------------

// DisputeCommand implements `a2a dispute <response-id> --reason <text>
// [--reason-code <enum>]`: folds the response to `disputed`; the parent's
// responded->in_progress reopening is fold's OWN side effect
// (applyResponseScoped), never a second authored event.
type DisputeCommand struct {
	deps lifecycleDeps
}

// NewDisputeCommand constructs the dispute command.
func NewDisputeCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *DisputeCommand {
	return &DisputeCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *DisputeCommand) Name() string { return "dispute" }

// Synopsis implements cli.Command.
func (c *DisputeCommand) Synopsis() string {
	return "dispute a response: dispute --reason <text> [--reason-code <enum>] <response-id>"
}

// Run implements cli.Command.
func (c *DisputeCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("dispute", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	reason := fs.String("reason", "", "reason text (required)")
	reasonCode := fs.String("reason-code", "", "machine-readable reason code")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids := fs.Args()
	if len(ids) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a dispute --reason <text> <response-id>")
		return 2
	}
	if *reason == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a dispute --reason <text> <response-id>")
		return 2
	}

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	for _, responseID := range ids {
		verdict, _, parentID, _, err := lifecycleCheckResponseLegality(c.deps.mirrorDir, c.deps.manifest, responseID, fold.TDispute, actor)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s: %v\n", responseID, err)
			return 1
		}
		if verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s\n", verdictRefusalMessage(responseID, verdict))
			return 1
		}
		_, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s: %v\n", parentID, err)
			return 1
		}

		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: cannot mint event id: %v\n", err)
			return 1
		}
		ev := lifecycleEventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: parentProbe.Space,
			Subject: responseID, Transition: fold.TDispute,
			Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
			Note:  *reason, ReasonCode: *reasonCode,
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: cannot encode event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
	}

	req := c.deps.buildRequest(ids, files, "dispute", false)
	return c.deps.submit(ctx, req, "dispute", ids, stdio)
}

var _ Command = (*DisputeCommand)(nil)

// --- note (transition-free, D-025) ---------------------------------------

// NoteCommand implements `a2a note <id...> --note <text>`: a transition-
// free annotation (D-025) — no fold-legality check (spec 08 Open Q1: legal
// on any open artifact with a thread, X/S/B), authorized for either party
// at fold-apply time (non-fatal flag only, never a local refusal).
type NoteCommand struct {
	deps lifecycleDeps
}

// NewNoteCommand constructs the note command.
func NewNoteCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) template.Actor) *NoteCommand {
	return &NoteCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// Name implements cli.Command.
func (c *NoteCommand) Name() string { return "note" }

// Synopsis implements cli.Command.
func (c *NoteCommand) Synopsis() string {
	return "annotate one or more artifacts: note --note <text> <id...>"
}

// Run implements cli.Command.
func (c *NoteCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("note", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	noteText := fs.String("note", "", "annotation text (required)")
	actorKind, actorName, actorModel := lifecycleActorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ids := fs.Args()
	if len(ids) == 0 || *noteText == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a note --note <text> <id...>")
		return 2
	}

	resolved := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "note: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	for _, id := range ids {
		_, probe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, id)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: %s: %v\n", id, err)
			return 1
		}
		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: cannot mint event id: %v\n", err)
			return 1
		}
		ev := lifecycleEventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: id, Transition: fold.TNote,
			Actor: lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System},
			At:    now.UTC().Format(time.RFC3339),
			Note:  *noteText,
		}
		raw, merr := yaml.Marshal(ev)
		if merr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: cannot encode event: %v\n", merr)
			return 1
		}
		files = append(files, space.FileWrite{Path: layout.EventFile(now.UTC().Format("2006"), eventID.String()), Content: raw})
	}

	req := c.deps.buildRequest(ids, files, "note", false)
	return c.deps.submit(ctx, req, "note", ids, stdio)
}

var _ Command = (*NoteCommand)(nil)
