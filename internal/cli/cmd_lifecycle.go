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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	"github.com/ydnikolaev/a2ahub/internal/operation"
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
	// Thread is the source artifact's §3.8 thread id — spec 46 §T1 R2:
	// `respond` propagates the PARENT's thread onto its response, never
	// minting or inventing a new one for a derived artifact.
	Thread string `yaml:"thread"`
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
// parsed draft) — the same §4.2 shapes, including the contract's fixed
// provides/<slug>/contract.md filename, which V2's placement table now
// validates as such (fb-20260723-9ae145).
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
	// Digest is the publish event's D-023 content identity and is computed
	// before the write. Commit is a legacy/reserved optional field that current
	// writers deliberately leave unset: the enclosing SHA exists only AFTER
	// the funnel commits the already-authored event. Version resolution derives
	// that SHA from the descriptor's Git history instead of backfilling a
	// second commit.
	Commit string `yaml:"commit,omitempty"`
	Digest string `yaml:"digest,omitempty"`
}

type lifecycleEventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"` // Model and Session are DETECTED (schemas/fill-classes.yaml) and were
	// structurally unreachable from this writer until P3: both event schemas
	// allow them, internal/validate's POL-016 bound-checks them, and no
	// first-party writer could produce either — so the policy was dead code
	// against everything that actually writes events.
	Model   string `yaml:"model,omitempty"`
	Session string `yaml:"session,omitempty"`
}

// eventActorFrom builds an event's actor block from the RESOLVED actor plus
// this project's own system id.
//
// It exists so the mapping lives in one place. Every call site used to write
// `lifecycleEventActor{Kind: actor.Kind, Name: actor.Name, System: actor.System}`
// from a fold.Actor — and fold.Actor deliberately carries only what the fold
// needs to resolve a role, so `model` and `session` had nowhere to come from.
// Copying the mapping to eight sites is also how a ninth gets forgotten.
func eventActorFrom(resolved template.Actor, system string) lifecycleEventActor {
	return lifecycleEventActor{
		Kind:    resolved.Kind,
		Name:    resolved.Name,
		System:  system,
		Model:   resolved.Model,
		Session: resolved.Session,
	}
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
			// contractCanonicalVersion (cmd_contract.go): fold.Result.Versions
			// is a map[string]State keyed on the raw string with no
			// canonicalization of its own — two spellings of one version
			// ("1.0.0" and "01.0.0") must reformat identically here, at the
			// ONE place every committed event's `version` field enters
			// fold's own input, or a non-canonically-spelled event (however
			// it was authored — this predates cmd_contract.go's own P4
			// write-side canonicalization) mismatches forever.
			Version: contractCanonicalVersion(ev.Version),
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

// lifecycleEvaluateCandidate is the generic (non-response-scoped) pre-write
// evaluator every OP-211 verb except verify/dispute uses: read id's own
// committed envelope + full event history, fold to its full prior Result, and
// delegate legality, receipt applicability and outcome to
// fold.EvaluateCandidate — never re-deriving §3.4 locally.
//
// candidate.Version is "" for every non-contract-version transition (the fallback
// this leans on lands unchanged on the legacy version-less path — see
// fold.EvaluateCandidate's own doc comment); a contract publish/deprecate/
// retire caller supplies the version the candidate event itself names
// (P4, 04-per-version-lifecycle.plan.md). Passing "" for a contract that
// already has ANY recorded version is a caller bug, not this function's
// to guess around: contractVersionVerdict refuses a version-less publish
// outright once any version is recorded (fold/contract.go), so a caller
// must resolve its own version BEFORE calling this — never after.
func lifecycleEvaluateCandidate(mirrorDir string, manifest space.Manifest, id string, candidate fold.Event) (fold.CandidateEvaluation, fold.Envelope, error) {
	env, _, err := lifecycleLoadEnvelope(mirrorDir, id)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, env, err
	}
	events := lifecycleFoldEvents(all, id)
	membership := lifecycleMembership(manifest)

	// prior carries the FULL fold.Result (not just its .State) so a
	// contract on the per-version path answers per-version rather than
	// per-subject — see fold.EvaluateCandidate's own doc comment.
	prior := fold.NewResult(env.Kind)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, membership)
	}
	candidate.Subject = id
	return fold.EvaluateCandidate(env.Kind, prior, candidate, env, membership), env, nil
}

// lifecycleEvaluateResponseCandidate is the verify/dispute pre-write
// evaluator: response closure state lives in the parent's full fold, while
// fold.EvaluateCandidate remains the sole owner of legality and receipts.
func lifecycleEvaluateResponseCandidate(mirrorDir string, manifest space.Manifest, responseID string, candidate fold.Event) (fold.CandidateEvaluation, fold.Envelope, string, fold.Result, error) {
	_, responseProbe, err := lifecycleLoadEnvelope(mirrorDir, responseID)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, err
	}
	parentID := responseProbe.Parent
	if parentID == "" {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, fmt.Errorf("cli: response %s carries no `parent` link", responseID)
	}
	parentEnv, _, err := lifecycleLoadEnvelope(mirrorDir, parentID)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, err
	}
	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, parentEnv, parentID, fold.Result{}, err
	}
	events := lifecycleFoldEvents(all, parentID)
	membership := lifecycleMembership(manifest)
	result := fold.Fold(parentEnv.Kind, parentEnv, events, membership)
	candidate.Subject = responseID
	evaluation := fold.EvaluateCandidate(parentEnv.Kind, result, candidate, parentEnv, membership)
	return evaluation, parentEnv, parentID, result, nil
}

func lifecycleReceiptState(evaluation fold.CandidateEvaluation) string {
	if !evaluation.Applicable {
		return ""
	}
	return string(evaluation.Outcome)
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

// lifecycleValidateSatisfyRefs enforces §3.4.2's satisfy proof bundle at
// the CLI boundary: one resolvable, version-pinned contract plus one
// verified response whose parent is the requirement being satisfied. The
// lifecycle table deliberately owns only state transitions; cross-artifact
// reference integrity belongs here, where the committed mirror is available.
func lifecycleValidateSatisfyRefs(mirrorDir string, manifest space.Manifest, requirementID string, refs []lifecycleRefEntry) ([]lifecycleRefEntry, error) {
	if len(refs) != 2 {
		return nil, fmt.Errorf("--refs must contain exactly one XC contract@version and one XS response")
	}

	var contractID, contractVersion, responseID string
	normalizedRefs := make([]lifecycleRefEntry, len(refs))
	for i, entry := range refs {
		ref := entry.Ref
		if at := strings.LastIndex(ref, "@"); at >= 0 {
			id, rawVersion := ref[:at], ref[at+1:]
			parsed, err := artifact.ParseID(id)
			if err != nil || parsed.Prefix != "XC" {
				return nil, fmt.Errorf("invalid contract ref %q: want XC-...@<semver>", ref)
			}
			version, err := contractParseSemver(rawVersion)
			if err != nil {
				return nil, fmt.Errorf("invalid contract ref %q: %w", ref, err)
			}
			if contractID != "" {
				return nil, fmt.Errorf("--refs contains more than one contract ref")
			}
			contractID, contractVersion = id, version.String()
			normalizedRefs[i] = lifecycleRefEntry{Ref: contractID + "@" + contractVersion}
			continue
		}

		parsed, err := artifact.ParseID(ref)
		if err == nil && parsed.Prefix == "XC" {
			return nil, fmt.Errorf("contract ref %q must pin an explicit semantic version", ref)
		}
		if err != nil || parsed.Prefix != "XS" {
			return nil, fmt.Errorf("invalid response ref %q: want XS-prefixed response", ref)
		}
		if responseID != "" {
			return nil, fmt.Errorf("--refs contains more than one response ref")
		}
		responseID = ref
		normalizedRefs[i] = entry
	}
	if contractID == "" || responseID == "" {
		return nil, fmt.Errorf("--refs must contain one XC contract@version and one XS response")
	}

	contractEnv, _, err := lifecycleLoadEnvelope(mirrorDir, contractID)
	if err != nil {
		return nil, fmt.Errorf("contract ref %s@%s does not resolve: %w", contractID, contractVersion, err)
	}
	if contractEnv.Kind != fold.KindContract {
		return nil, fmt.Errorf("contract ref %s@%s resolves to %s, want contract", contractID, contractVersion, contractEnv.Kind)
	}
	responseEnv, responseProbe, err := lifecycleLoadEnvelope(mirrorDir, responseID)
	if err != nil {
		return nil, fmt.Errorf("response ref %s does not resolve: %w", responseID, err)
	}
	if responseEnv.Kind != fold.KindResponse {
		return nil, fmt.Errorf("response ref %s resolves to %s, want response", responseID, responseEnv.Kind)
	}
	if responseProbe.Parent != requirementID {
		return nil, fmt.Errorf("response ref %s has parent %q, want %q", responseID, responseProbe.Parent, requirementID)
	}

	all, err := lifecycleReadAllEvents(mirrorDir)
	if err != nil {
		return nil, err
	}
	membership := lifecycleMembership(manifest)
	contractResult := fold.Fold(contractEnv.Kind, contractEnv, lifecycleFoldEvents(all, contractID), membership)
	if _, ok := contractResult.Versions[contractVersion]; !ok {
		return nil, fmt.Errorf("contract ref %s@%s does not resolve to a recorded version", contractID, contractVersion)
	}
	requirementEnv, _, err := lifecycleLoadEnvelope(mirrorDir, requirementID)
	if err != nil {
		return nil, err
	}
	requirementResult := fold.Fold(requirementEnv.Kind, requirementEnv, lifecycleFoldEvents(all, requirementID), membership)
	// A requirement does not have a parent-level `respond` transition. Its
	// response is attached by the XS envelope's `parent` field, so seed that
	// response's documented submitted state and replay its own closure events
	// through fold.Apply (the canonical authorization/transition engine).
	if requirementResult.Responses == nil {
		requirementResult.Responses = map[string]fold.State{}
	}
	requirementResult.Responses[responseID] = fold.StateSubmitted
	var responseEvents []lifecycleEventDoc
	for _, ev := range all {
		if ev.Subject == responseID {
			responseEvents = append(responseEvents, ev)
		}
	}
	sort.Slice(responseEvents, func(i, j int) bool { return responseEvents[i].Event < responseEvents[j].Event })
	for _, ev := range responseEvents {
		requirementResult = fold.Apply(requirementEnv.Kind, requirementEnv, requirementResult, fold.Event{
			ULID: ev.Event, Subject: ev.Subject, Transition: ev.Transition,
			ClaimedState: fold.State(ev.State),
			Actor:        fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
		}, membership)
	}
	if got := requirementResult.Responses[responseID]; got != fold.StateVerified {
		return nil, fmt.Errorf("response ref %s is %q, want %q", responseID, got, fold.StateVerified)
	}

	return normalizedRefs, nil
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
	resolveActor func(ActorFlags) (template.Actor, error)
	pending      PendingMarker

	now      func() time.Time
	entropy  io.Reader
	readFile func(path string) ([]byte, error)
}

func newLifecycleDeps(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) lifecycleDeps {
	return lifecycleDeps{
		funnel: funnel, mirrorDir: mirrorDir, spaceID: spaceID, ownSystem: ownSystem,
		manifest: manifest, hostCfg: hostCfg, resolveActor: resolveActor,
		pending: NewNoopPendingMarker(),
		now:     time.Now, entropy: rand.Reader, readFile: os.ReadFile,
	}
}

func (d *lifecycleDeps) setPendingMarker(pending PendingMarker) {
	if pending == nil {
		pending = NewNoopPendingMarker()
	}
	d.pending = pending
}

func (d lifecycleDeps) refusalMessage(id string, verdict fold.Verdict) string {
	message := verdictRefusalMessage(id, verdict)
	if verdict != fold.VerdictIllegalTransition {
		return message
	}
	reader, ok := d.pending.(PendingMarkerReader)
	if !ok {
		return message
	}
	pending, found, err := reader.Pending(d.spaceID, id)
	if err != nil || !found {
		return message
	}
	return fmt.Sprintf("%s; previous write is pending-merge (PR %s); run `a2a await %s` and retry", message, pending.PRURL, id)
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
		Verb: verb, ArtifactID: strings.Join(sorted, "+"), ArtifactIDs: sorted, Files: files,
		CommitMessage: commitMsg, CommitAuthorName: d.hostCfg.CommitAuthorName, CommitAuthorEmail: d.hostCfg.CommitAuthorEmail,
		RemoteURL: d.hostCfg.RemoteURL, Repo: d.hostCfg.Repo, BaseBranch: baseBranch,
		PRTitle: commitMsg, PRBody: prBody, Credential: d.hostCfg.Credential,
		// The write floor (CC-085) belongs on EVERY write, not only on
		// `a2a submit`. Until 2026-07-26 this field was set by exactly three
		// call sites — submit (CLI and MCP) and `space update` — so the funnel's
		// guard, which fires only `if req.MinBinaryVersion != ""`, was inert for
		// all 15 lifecycle verbs and for `contract publish/deprecate/retire`.
		//
		// That is not hypothetical. The branch grammar BranchName renders — the
		// funnel's idempotency key — changed in 0.4.0, and the getvisa space
		// still carries two open PRs for ONE `contract publish` of one artifact,
		// on the two grammars, because a pre-0.4.0 binary was allowed to write.
		// Raising a space's floor is exactly how an operator stops that, and it
		// was doing nothing on the verb that caused it.
		//
		// Read from the parsed manifest rather than re-probing space.yaml the
		// way SubmitCommand.readMinBinaryVersion does: this path already
		// REQUIRES a parsed manifest (membership resolution reads it), so if we
		// got here the field is available and a second file read would only add
		// a way for the two to disagree.
		MinBinaryVersion: d.manifest.MinBinaryVersion,
	}
}

func (d lifecycleDeps) submit(ctx context.Context, req space.SubmitRequest, verb string, ids []string, stdio IO) int {
	result, err := d.funnel.Submit(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", verb, err)
		return 1
	}
	effectiveIDs := ids
	if len(result.ArtifactIDs) > 0 {
		effectiveIDs = result.ArtifactIDs
	}
	for _, id := range effectiveIDs {
		switch result.State {
		case space.WriteStatePendingMerge, space.WriteStateAlreadyOpen:
			if err := d.pending.MarkPending(ctx, d.spaceID, id, result); err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "%s: pending-merge marker failed: %v\n", verb, err)
				return 1
			}
		default:
			if clearer, ok := d.pending.(PendingMarkerClearer); ok {
				if err := clearer.ClearPending(d.spaceID, id); err != nil {
					_, _ = fmt.Fprintf(stdio.Stderr, "%s: pending-merge marker cleanup failed: %v\n", verb, err)
					return 1
				}
			}
		}
	}
	switch result.State {
	case space.WriteStateAlreadyOpen, space.WriteStateAlreadyMerged:
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: already submitted for %s (PR %s, %s)\n",
			verb, strings.Join(effectiveIDs, ", "), result.PRURL, result.State)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout, "%s: opened PR %s for %s (%s)\n", verb, result.PRURL, strings.Join(effectiveIDs, ", "), result.State)
		warnAutoMerge(stdio, verb, result.AutoMergeNote)
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
	{Verb: "withdraw", Transition: fold.TWithdraw, Synopsis: "withdraw one or more requirements or proposed decisions"},
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

// SetPendingMarker wires the machine-local pending-write store.
func (c *LifecycleCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// newLifecycleCommand is every generic-verb NewXCommand constructor's
// shared body (table-driven, §9 Future-proofing — one place to extend
// when a new §3.4 transition needs an OP-211 verb).
func newLifecycleCommand(spec lifecycleVerbSpec, funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return &LifecycleCommand{spec: spec, deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// NewAckCommand constructs `a2a ack <id...>`. funnel, manifest and
// resolveActor must not be nil/zero-configured (rails anti-pattern #10).
func NewAckCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[0], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewAcceptCommand constructs `a2a accept <id...>`.
func NewAcceptCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[1], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewDeclineCommand constructs `a2a decline <id...> --reason <text> [--reason-code <enum>]`.
func NewDeclineCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[2], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewStartCommand constructs `a2a start <id...>`.
func NewStartCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[3], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewBlockCommand constructs `a2a block <id...> --refs <blocker-id>`.
func NewBlockCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[4], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewUnblockCommand constructs `a2a unblock <id...>`.
func NewUnblockCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[5], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewCancelCommand constructs `a2a cancel <id...>`.
func NewCancelCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[6], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewCloseCommand constructs `a2a close <parent-id...>`.
func NewCloseCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[7], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewWithdrawCommand constructs `a2a withdraw <requirement-id...>`.
func NewWithdrawCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[8], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewSupersedeCommand constructs `a2a supersede <id> --refs <successor-id>`.
func NewSupersedeCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[9], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewSatisfyCommand constructs `a2a satisfy <requirement-id> --refs <XC-id@version>,<XS-id>`.
func NewSatisfyCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[10], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewApproveCommand constructs `a2a approve <decision-id>` (ALWAYS G3-gated, P8-3).
func NewApproveCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[11], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewRejectCommand constructs `a2a reject <decision-id> --reason <text>` (ALWAYS G3-gated, P8-3).
func NewRejectCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[12], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewVerifyPassCommand constructs `a2a verify-pass <handoff-id>`.
func NewVerifyPassCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
	return newLifecycleCommand(lifecycleVerbTable[13], funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)
}

// NewVerifyFailCommand constructs `a2a verify-fail <handoff-id> --findings <text>`.
func NewVerifyFailCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *LifecycleCommand {
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
	// Wave K fix (live run 6, "thirteen verbs refuse a flag written after
	// their positional argument"): parseArgsAnyOrder, not a bare
	// fs.Parse(args) — this is the most important of the thirteen, since
	// it is every N-id batch verb's own Run (ack/accept/decline/start/
	// block/unblock/cancel/close/withdraw/supersede/satisfy/approve/
	// reject/verify-pass/verify-fail all share this one method via the
	// table). See parseArgsAnyOrder's own doc comment (cli.go).
	ids, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
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

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: %v\n", c.spec.Verb, err)
		return 1
	}

	var files []space.FileWrite
	parsedRefs := lifecycleRefsFromFlag(*refs)
	for _, id := range ids {
		evaluation, _, err := lifecycleEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, id, fold.Event{
			Transition: c.spec.Transition, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s\n", c.spec.Verb, c.deps.refusalMessage(id, evaluation.Verdict))
			return 1
		}
		eventRefs := parsedRefs
		if c.spec.Transition == fold.TSatisfy {
			eventRefs, err = lifecycleValidateSatisfyRefs(c.deps.mirrorDir, c.deps.manifest, id, parsedRefs)
			if err != nil {
				_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
				return 1
			}
		}

		_, probe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, id)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: %s: %v\n", c.spec.Verb, id, err)
			return 1
		}
		eventID, err := artifact.MintULIDAt(now, c.deps.entropy)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "%s: cannot mint event id: %v\n", c.spec.Verb, err)
			return 1
		}
		ev := lifecycleEventDoc{
			Schema: "event/v1", Event: eventID.String(), Space: probe.Space,
			Subject: id, Transition: c.spec.Transition, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
			At:    now.UTC().Format(time.RFC3339),
		}
		if *reason != "" {
			ev.Note = *reason
		}
		if *reasonCode != "" {
			ev.ReasonCode = *reasonCode
		}
		if len(eventRefs) > 0 {
			ev.Refs = eventRefs
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

// lifecycleRespondSeed builds `respond`'s own canonical, content-derived
// seed (HIGH-1 fix-wave finding): a fixed-order join of parentID, result,
// every respFields k=v pair — SORTED by key, since respFields is a map and
// Go's own map iteration order is randomized per-process; skipping the
// sort would make the seed (and therefore responseID) nondeterministic in
// production while a fixed-entropy unit test still passed, the exact trap
// this fix targets — the body override, and the actor's kind/name/system.
// Deliberately EXCLUDES `now` (see this file's own respond Run comment)
// and any random id.
func lifecycleRespondSeed(parentID, result string, respFields map[string]string, bodyOverride []byte, actor fold.Actor) []byte {
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

// --- respond (scaffolds + submits an XS) ---------------------------------

// RespondCommand implements `a2a respond <parent-id...>`: scaffolds a new
// XS response artifact per parent (draft->submit collapsed, D-026) AND
// authors the parent's own `respond` event (linking via refs[0], see
// lifecycleEventDoc's own doc comment) — batch = N parents, one PR.
type RespondCommand struct {
	deps lifecycleDeps
}

// SetPendingMarker replaces the command's pending-write recorder.
func (c *RespondCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewRespondCommand constructs the respond command.
func NewRespondCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *RespondCommand {
	return &RespondCommand{deps: newLifecycleDeps(funnel, mirrorDir, spaceID, ownSystem, manifest, hostCfg, resolveActor)}
}

// SetClockForTest overrides this command's injected clock (test-only DI
// seam, rails anti-pattern #10: production always uses the constructor's
// own time.Now default). HIGH-1 fix-wave finding: proving responseID's
// determinism across two calls needs a FIXED, reproducible `now` — a real
// wall-clock read would make the assertion flaky near a UTC-date boundary
// (MintExchangeIDAt embeds today's UTC date).
func (c *RespondCommand) SetClockForTest(now func() time.Time) {
	c.deps.now = now
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
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	parents, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
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

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}
	operationKey := operation.Respond(
		c.deps.ownSystem, actor.Kind, actor.Name, parents, *result, fields, bodyOverride,
	)

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "respond: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	var ids []string
	for _, parentID := range parents {
		parentEnv, parentProbe, err := lifecycleLoadEnvelope(c.deps.mirrorDir, parentID)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
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

		// HIGH-1 fix-wave finding: responseID's random suffix is derived
		// from the response's OWN content (lifecycleRespondSeed — parentID,
		// result, every respFields k=v pair SORTED by key, the body
		// override, and the actor), never c.deps.entropy directly — a retry
		// with IDENTICAL inputs reproduces the IDENTICAL responseID, landing
		// on the funnel's SAME deterministic branch (dedup,
		// space.WriteStateAlreadyOpen) instead of authoring a duplicate
		// response artifact + PR. Deliberately NOT keyed on parentID alone:
		// the verify/dispute design allows multiple distinct responses from
		// the same system on the same parent (TestVerifyMultiResponseDoes
		// NotAutoClose), so a parentID-only branch would silently collapse
		// two genuinely different responses onto one branch. NOTE:
		// MintExchangeIDAt still embeds today's UTC date from `now`; a retry
		// crossing midnight still mints a different id (spec 08 §11
		// amendment — accepted, out of scope here).
		seed := lifecycleRespondSeed(parentID, *result, respFields, bodyOverride, actor)
		responseID, err := artifact.MintExchangeIDAt("XS", c.deps.ownSystem, now, bytes.NewReader(seed))
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: cannot mint response id: %v\n", err)
			return 1
		}
		evaluation, _, err := lifecycleEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, parentID, fold.Event{
			Transition: fold.TRespond, ResponseID: responseID, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: %v\n", parentID, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s\n", c.deps.refusalMessage(parentID, evaluation.Verdict))
			return 1
		}

		// Wave K fix, `space`/`title` half (found alongside the pinned
		// `to` fix while checking this exact "drafts AND writes in one
		// call" shape, per that fix's own instruction to check other
		// verbs for the same class of gap): response.md's `space:
		// <space-id>` and `title: <human/agent-scannable title, ...>`
		// are SCALAR placeholders respFields never carried keys for, so
		// template.Render's own applyFills left both completely unfilled
		// — the committed response would carry those LITERAL strings
		// forever. internal/validate's V2 pass rejects neither (space is
		// a free-form string field; title has no placeholder-literal
		// check outside SubmitCommand.Run, cmd_submit.go, a path
		// `respond` never goes through — it calls c.deps.submit
		// directly). `contract deprecate` (cmd_contract.go) already
		// fixes the identical `space`/`title` gap on its own
		// announcement draft the same way.
		//
		// Added HERE, deliberately AFTER lifecycleRespondSeed/
		// MintExchangeIDAt above rather than alongside `parent`/`result`/
		// `from`: both are DERIVED defaults (parentProbe.Space; a
		// parentID-based title text), not part of what a retry's
		// dedup-by-content check (this file's own HIGH-1 fix, doc'd
		// below) needs to distinguish two responses — folding them into
		// the seed would also silently change every already-computed
		// responseID's own hash input, which is exactly the kind of
		// seed-shape change HIGH-1 warns never to make casually. It
		// happens to also keep this verb's content-derived id
		// numerically identical to internal/mcp's own (unfixed) respond
		// path, which mints from the SAME parent/result/from-only seed —
		// not the goal, but a useful confirmation neither seed shape
		// silently drifted.
		respFields["space"] = parentProbe.Space
		if _, has := respFields["title"]; !has {
			respFields["title"] = fmt.Sprintf("Response to %s", parentID)
		}

		// spec 46 §T1 R2/R4/R5: a derived artifact inherits its SOURCE's
		// thread — the response is derived from parentID, so it inherits
		// parentProbe.Thread, set here beside space/title (a derived
		// default, added AFTER lifecycleRespondSeed/MintExchangeIDAt above
		// for the exact reason space/title are: folding it into the seed
		// would silently change every already-computed responseID, per
		// this file's own comment above). An explicit --field thread=<id>
		// that DIFFERS from the parent's thread is refused (exit 2 — bad
		// caller input, same class as the --result usage check above)
		// naming both values — never a silent precedence, never a guess
		// (R4).
		// `parentProbe.Thread != ""` matters and is not defensive noise: without
		// it, a threadless parent plus an explicit `--field thread=X` takes THIS
		// branch and prints "conflicts with parent's thread " — an empty value in
		// a message whose whole job is naming both sides — instead of falling
		// through to the threadless refusal below, which names the real condition
		// and the real fix. MCP's twin already carried the guard; the two
		// surfaces disagreed on the message and the exit code for identical
		// input, which is the divergence class ADR-001's deliberate duplication
		// is supposed to be watched for rather than assumed away.
		if explicit, has := respFields["thread"]; has && explicit != "" && parentProbe.Thread != "" && explicit != parentProbe.Thread {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: %s: --field thread=%s conflicts with parent's thread %s\n", parentID, explicit, parentProbe.Thread)
			return 2
		}
		// A parent with NO thread is refused, loudly and by name. Since
		// `a2a new` always mints (R1), the only artifacts in this state are
		// ones committed before P46 — and spec 46 carries no legacy path by
		// operator decision (greenfield: the spaces are reseeded). The two
		// alternatives are both worse and both were observed while this
		// wave was built: propagating the empty value writes YAML `null`
		// and the reply dies later with an opaque SCH-006 type error, while
		// leaving the field unset lets the canonical template's own
		// placeholder text survive into a committed document. Refusing here
		// names the actual condition and the actual fix.
		if parentProbe.Thread == "" {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"respond: %s: the parent carries no thread, so this reply has no conversation to join.\n"+
					"That artifact predates thread propagation; reseed the space (see `a2a space init`) or reply to an artifact drafted by this version.\n",
				parentID)
			return 1
		}
		respFields["thread"] = parentProbe.Thread

		draft, err := template.Render(template.Input{
			Type: "response", ID: responseID, Actor: resolved, Created: now,
			Fields: respFields, Body: bodyOverride,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: render failed for %s: %v\n", parentID, err)
			return 1
		}

		// Wave K fix (live run 6, "a2a respond writes the response
		// template's to: placeholder verbatim"): response.md's own `to:
		// [<requester-system>]` is SEQUENCE-valued, and internal/template's
		// applyFills/setScalar only ever rewrites a scalar node (P18's
		// deliberately-deferred "Fix C (--field lists)", off-limits this
		// wave) — so the rendered draft still carries the literal
		// placeholder text, and the funnel's own V2 pass refuses it
		// (REF-006/CC-008: "`to` includes an unknown system:
		// <requester-system>"). A response answers the system that asked
		// (§3.4.3: "to: EXACTLY one entry"), and that requester is already
		// in hand as parentEnv.From from this loop's own legality check
		// above — never re-derived. Fixed the SAME way runPublish
		// (cmd_contract.go) already sets `version`: decode the rendered
		// frontmatter into a map, assign the ONE real field, re-encode via
		// artifact.SerializeFrontmatter. Never ad-hoc text surgery on a
		// structured document (rails), and never reopening applyFills for
		// sequence values (that is P18's call, not this wave's).
		respFm, err := artifact.ParseFrontmatter(draft)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: parse rendered response for %s: %v\n", parentID, err)
			return 1
		}
		var respDoc map[string]any
		if err := yaml.Unmarshal(respFm.YAML, &respDoc); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: decode rendered response for %s: %v\n", parentID, err)
			return 1
		}
		respDoc["to"] = []string{parentEnv.From}
		respYAML, err := yaml.Marshal(respDoc)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "respond: encode rendered response for %s: %v\n", parentID, err)
			return 1
		}
		draft = artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: respYAML, Body: respFm.Body})

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
			Subject: parentID, Transition: fold.TRespond, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
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
	req.OperationKey = operationKey
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

// SetPendingMarker replaces the command's pending-write recorder.
func (c *VerifyCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewVerifyCommand constructs the verify command.
func NewVerifyCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *VerifyCommand {
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
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	targets, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a verify <response-id|parent-id...> [--refs <response-id>]")
		return 2
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "verify: %v\n", actorErr)
		return 1
	}
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

		evaluation, parentEnv, parentID, result, err := lifecycleEvaluateResponseCandidate(c.deps.mirrorDir, c.deps.manifest, responseID, fold.Event{
			Transition: fold.TVerify, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s: %v\n", responseID, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "verify: %s\n", c.deps.refusalMessage(responseID, evaluation.Verdict))
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
			Subject: responseID, Transition: fold.TVerify, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
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
			closeEvaluation := fold.EvaluateCandidate(parentEnv.Kind, evaluation.Result, fold.Event{
				Subject: parentID, Transition: fold.TClose, Actor: actor,
			}, parentEnv, lifecycleMembership(c.deps.manifest))
			if closeEvaluation.Verdict != fold.VerdictLegal {
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
				Subject: parentID, Transition: fold.TClose, State: lifecycleReceiptState(closeEvaluation),
				Actor: eventActorFrom(resolved, actor.System),
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

// SetPendingMarker replaces the command's pending-write recorder.
func (c *DisputeCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewDisputeCommand constructs the dispute command.
func NewDisputeCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *DisputeCommand {
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
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	ids, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(ids) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a dispute --reason <text> <response-id>")
		return 2
	}
	if *reason == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a dispute --reason <text> <response-id>")
		return 2
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	for _, responseID := range ids {
		evaluation, _, parentID, _, err := lifecycleEvaluateResponseCandidate(c.deps.mirrorDir, c.deps.manifest, responseID, fold.Event{
			Transition: fold.TDispute, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s: %v\n", responseID, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "dispute: %s\n", c.deps.refusalMessage(responseID, evaluation.Verdict))
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
			Subject: responseID, Transition: fold.TDispute, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
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
// free annotation (D-025), legal regardless of folded state. The shared
// fold predicate still checks authorization for either party before the
// funnel, matching the space's V3 required check.
type NoteCommand struct {
	deps lifecycleDeps
}

// SetPendingMarker replaces the command's pending-write recorder.
func (c *NoteCommand) SetPendingMarker(pending PendingMarker) {
	c.deps.setPendingMarker(pending)
}

// NewNoteCommand constructs the note command.
func NewNoteCommand(funnel lifecycleFunnel, mirrorDir, spaceID, ownSystem string, manifest space.Manifest, hostCfg SubmitHostConfig, resolveActor func(ActorFlags) (template.Actor, error)) *NoteCommand {
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
	// Wave K fix (see LifecycleCommand.Run's own comment above): any-order
	// parsing, not a bare fs.Parse(args).
	ids, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(ids) == 0 || *noteText == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a note --note <text> <id...>")
		return 2
	}

	resolved, actorErr := c.deps.resolveActor(ActorFlags{Kind: *actorKind, Name: *actorName, Model: *actorModel})
	if actorErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "note: %v\n", actorErr)
		return 1
	}
	actor := fold.Actor{Kind: resolved.Kind, Name: resolved.Name, System: c.deps.ownSystem}

	now := c.deps.now()
	layout, err := space.NewLayout(c.deps.ownSystem)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "note: %v\n", err)
		return 1
	}

	var files []space.FileWrite
	for _, id := range ids {
		evaluation, _, err := lifecycleEvaluateCandidate(c.deps.mirrorDir, c.deps.manifest, id, fold.Event{
			Transition: fold.TNote, Actor: actor,
		})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: %s: %v\n", id, err)
			return 1
		}
		if evaluation.Verdict != fold.VerdictLegal {
			_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", c.deps.refusalMessage(id, evaluation.Verdict))
			return 1
		}

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
			Subject: id, Transition: fold.TNote, State: lifecycleReceiptState(evaluation),
			Actor: eventActorFrom(resolved, actor.System),
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
