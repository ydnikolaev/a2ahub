package mcp

// This file is internal/mcp's own projection of the FROZEN schemas/event/v1
// document shape (D-030) — built to match internal/cli's cmd_lifecycle.go
// (lifecycleEventDoc, ~L149-196) and cmd_submit.go (submitEventDoc)
// field-for-field, WITHOUT importing them (plan 14 Placement decisions,
// binding). The per-write-verb equivalence suite in cmd/a2a is the
// anti-drift gate that proves this struct's yaml.Marshal output is
// byte-identical to the CLI's own, modulo the event/artifact id.

import (
	"context"

	"fmt"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// eventDoc is this package's own event/v1 document decode+encode struct —
// field order and yaml tags mirror internal/cli's lifecycleEventDoc
// exactly (the equivalence suite's byte-identity depends on it).
type eventDoc struct {
	Schema     string     `yaml:"schema"`
	Event      string     `yaml:"event"`
	Space      string     `yaml:"space"`
	Subject    string     `yaml:"subject"`
	Transition string     `yaml:"transition"`
	State      string     `yaml:"state,omitempty"`
	Actor      eventActor `yaml:"actor"`
	At         string     `yaml:"at"`
	Note       string     `yaml:"note,omitempty"`
	Refs       []refEntry `yaml:"refs,omitempty"`
	ReasonCode string     `yaml:"reason_code,omitempty"`
	Version    string     `yaml:"version,omitempty"`
	Commit     string     `yaml:"commit,omitempty"`
	Digest     string     `yaml:"digest,omitempty"`
	// Verdicts is P1's own field (one-answer-2026-08 spec 01, mirroring
	// internal/cli's lifecycleEventDoc.Verdicts field exactly, spec §11's
	// amendment): the verifier's per-criterion judgement, conditionally
	// required by schemas/event/v2/event.schema.json on `verify`/`close` —
	// WITH a pointer, not a bare slice: yaml.v3's `omitempty` drops an
	// empty slice exactly the same as a nil one, and the schema's own
	// description is explicit that a close over a parent with no
	// acceptance_criteria[] at all must stay expressible with an empty
	// array, not an absent key. Every other event-authoring site in this
	// file leaves this nil, which `omitempty` on the POINTER still omits —
	// so v1 writers are unaffected and the v1 schema's
	// additionalProperties: false is never violated. MUST stay the LAST
	// field: this struct's own field order mirrors internal/cli's
	// lifecycleEventDoc exactly, and the equivalence suite's byte-identity
	// depends on it.
	Verdicts *[]VerdictInputEntry `yaml:"verdicts,omitempty"`
}

type eventActor struct {
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
// this project's own system id — the MCP mirror of internal/cli's function
// of the same name, and for the same reason: the mapping was written out at
// nine call sites from a fold.Actor, which deliberately carries only what
// the fold needs, so `model` and `session` had nowhere to come from.
func eventActorFrom(resolved template.Actor, system string) eventActor {
	return eventActor{
		Kind:    resolved.Kind,
		Name:    resolved.Name,
		System:  system,
		Model:   resolved.Model,
		Session: resolved.Session,
	}
}

type refEntry struct {
	Ref string `yaml:"ref"`
}

// envelopeProbe is this package's own minimal envelope decode (mirrors
// internal/cli's lifecycleEnvelopeProbe).
type envelopeProbe struct {
	ID                string   `yaml:"id"`
	Space             string   `yaml:"space"`
	From              string   `yaml:"from"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
	Parent            string   `yaml:"parent"`
}

// prefixInfo maps a §3.3 id prefix to its fold.Kind.
var prefixInfo = map[string]fold.Kind{
	"XC": fold.KindContract,
	"XR": fold.KindRequirement,
	"XQ": fold.KindQuestion,
	"XW": fold.KindWorkRequest,
	"XD": fold.KindDecision,
	"XH": fold.KindHandoff,
	"XS": fold.KindResponse,
	"XA": fold.KindAnnouncement,
}

// artifactPath resolves parsed's committed space-relative path per §4.2's
// layout (mirrors internal/cli's lifecycleArtifactPath).
func artifactPath(parsed artifact.ID) (string, error) {
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
		return "", fmt.Errorf("mcp: unknown artifact id prefix %q", parsed.Prefix)
	}
}

// loadEnvelope reads and parses id's committed artifact file from
// mirrorDir.
func loadEnvelope(mirrorDir, id string) (fold.Envelope, envelopeProbe, error) {
	parsed, err := artifact.ParseID(id)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: %w", id, err)
	}
	kind, ok := prefixInfo[parsed.Prefix]
	if !ok {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: unknown artifact id prefix %q", id, parsed.Prefix)
	}
	relPath, err := artifactPath(parsed)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, err
	}
	raw, err := readBoundedFile(filepath.Join(mirrorDir, relPath), maxMirrorEventBytes)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: cannot read %s: %w", id, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: %w", id, err)
	}
	var probe envelopeProbe
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return fold.Envelope{}, envelopeProbe{}, fmt.Errorf("mcp: %s: cannot decode envelope: %w", id, err)
	}
	env := fold.Envelope{
		ID: id, Kind: kind, From: probe.From,
		To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
	}
	return env, probe, nil
}

// readAllEvents walks every <system>/events/<year>/<ulid>.yaml under
// mirrorDir — every participating system's own section.
func readAllEvents(mirrorDir string) ([]eventDoc, error) {
	matches, err := filepath.Glob(filepath.Join(mirrorDir, "*", "events", "*", "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("mcp: list committed events: %w", err)
	}
	out := make([]eventDoc, 0, len(matches))
	for _, m := range matches {
		raw, err := readBoundedFile(m, maxMirrorEventBytes)
		if err != nil {
			return nil, err
		}
		var ev eventDoc
		if err := yaml.Unmarshal(raw, &ev); err != nil {
			return nil, fmt.Errorf("mcp: decode committed event %s: %w", m, err)
		}
		out = append(out, ev)
	}
	return out, nil
}

// foldEvents selects, from every committed event, the ones relevant to
// primaryID's own fold (mirrors internal/cli's lifecycleFoldEvents).
func foldEvents(all []eventDoc, primaryID string) []fold.Event {
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
			// contractCanonicalVersion (tools_contract.go): see internal/cli's
			// own lifecycleFoldEvents comment — fold.Result.Versions keys on
			// the raw string with no canonicalization of its own, so every
			// committed event's `version` field is reformatted here, at the
			// one place it enters fold's own input.
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

// membership adapts a space.Manifest into a fold.MembershipView.
func membership(manifest space.Manifest) fold.MembershipView {
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

// evaluateCandidate is the generic (non-response-scoped, no-successor-facts)
// pre-write evaluator most write tools use. It is this package's one bridge
// from committed envelope/history/membership inputs to fold.EvaluateCandidate
// for every caller that has no successor refs to resolve — tools_contract.go's
// deprecate/retire evaluations (off this wave's allowlist) still call this
// EXACT 3-return-value shape, so it stays evaluateCandidate' own
// thin nil-refs wrapper rather than changing shape out from under those
// callers. callers serialize only the returned receipt, never derive state
// themselves.
//
// candidate.Version is "" for every non-contract-version transition; a
// contract publish/deprecate/retire caller supplies the version the candidate
// event itself names, resolved before calling this.
// evaluateCandidate accepts the batch's own §5.2.2 `refs[].ref` values and resolve
// the SUCCESSOR facts a decision-supersede row's declared Precondition
// checks (internal/fold/table.go), via fold.EvaluateCandidateWithSuccessor
// rather than the nil-successor fold.EvaluateCandidate wrapper. Used ONLY by
// the generic table-driven verb handler (newLifecycleHandler,
// tools_lifecycle.go), the ONE write path whose spec.Transition can equal
// TSupersede on a decision — the same way the CLI write surface's own
// generic pre-write evaluator already does, via that package's own
// MirrorResolver.Successor —
// never a second, independently typed successor reader.
//
// Before this fix, every caller on this surface reached fold's
// nil-successor EvaluateCandidate wrapper unconditionally, so both
// decision-supersede rows refused UNCONDITIONALLY for every actor through
// the MCP tool surface — the defect this closes (see resolveSuccessorFacts'
// own doc comment for the resolution rule).
//
// The returned *fold.SuccessorFacts is the SAME value this function already
// resolves internally and passes to fold.EvaluateCandidateWithSuccessor —
// surfaced to the caller so a refusal on a decision-supersede candidate can
// discriminate "successor resolved, precondition failed" (LFC-005 alone)
// from "successor unresolvable" (LFC-005 paired with an LFC-006 advisory),
// mirroring internal/validate's own checkLifecycle discrimination
// (`ev.SuccessorEnvelope == nil`). nil for every non-supersede/non-decision/
// ref-less call (refs is nil for every caller today except the generic
// handler's own supersede row).
func evaluateCandidate(mirrorDir string, manifest space.Manifest, id string, candidate fold.Event, refs []refEntry) (fold.CandidateEvaluation, fold.Envelope, *fold.SuccessorFacts, error) {
	env, _, err := loadEnvelope(mirrorDir, id)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, nil, err
	}
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, env, nil, err
	}
	events := foldEvents(all, id)
	memb := membership(manifest)

	prior := fold.NewResult(env.Kind)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, memb)
	}
	candidate.Subject = id
	successor := resolveSuccessorFacts(mirrorDir, manifest, env.Kind, candidate.Transition, refs)
	return fold.EvaluateCandidateWithSuccessor(env.Kind, prior, candidate, env, memb, successor), env, successor, nil
}

// resolveSuccessorFacts resolves the caller-supplied facts about the
// SUCCESSOR artifact a decision-supersede row's declared
// SuccessorPrecondition checks (internal/fold/table.go,
// fold.CheckCandidateWithSuccessor) — this package's own
// MirrorResolver.Successor (validate.SuccessorResolver), the SAME capability
// the SUBMIT path's resolveSuccessorEnvelope (adapters.go) already uses,
// never a second successor reader. The CLI write surface carries the
// structurally identical helper over its own resolver — ADR-001
// forbids internal/mcp importing internal/cli, so this is this package's own
// copy of the same gate.
//
// Gated exactly like internal/cli's own gate: only a decision's own
// supersede transition, with at least one ref, ever resolves — every other
// transition/kind/ref-less call returns nil (unresolved). Every row but the
// two decision-supersede rows carries PreconditionNone and never consults
// this value regardless (preconditionTable, table.go), so this gate is a
// cost optimization (skip the mirror read) rather than a correctness
// requirement.
//
// A nil return (successorID absent from THIS caller's own local mirror,
// unparsable, or the resolver call failing) is D9's own "unresolved" case
// (fold/types.go's SuccessorPrecondition doc comment) —
// CheckCandidateWithSuccessor refuses a Precondition-bearing row uniformly
// rather than silently granting on a resolution failure.
func resolveSuccessorFacts(mirrorDir string, manifest space.Manifest, kind fold.Kind, transition string, refs []refEntry) *fold.SuccessorFacts {
	if transition != fold.TSupersede || kind != fold.KindDecision || len(refs) == 0 {
		return nil
	}
	resolver := NewMirrorResolver(mirrorDir, manifest)
	author, state, ok := resolver.Successor(refs[0].Ref)
	if !ok {
		return nil
	}
	return &fold.SuccessorFacts{Author: author, State: fold.State(state)}
}

// evaluateResponseCandidate is the verify/dispute pre-write evaluator. The
// parent fold is the authority for response substates, so the response id is
// installed as the candidate subject before the sole evaluator call.
func evaluateResponseCandidate(mirrorDir string, manifest space.Manifest, responseID string, candidate fold.Event) (fold.CandidateEvaluation, fold.Envelope, string, fold.Result, error) {
	_, responseProbe, err := loadEnvelope(mirrorDir, responseID)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, err
	}
	parentID := responseProbe.Parent
	if parentID == "" {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, fmt.Errorf("mcp: response %s carries no `parent` link", responseID)
	}
	parentEnv, _, err := loadEnvelope(mirrorDir, parentID)
	if err != nil {
		return fold.CandidateEvaluation{}, fold.Envelope{}, "", fold.Result{}, err
	}
	all, err := readAllEvents(mirrorDir)
	if err != nil {
		return fold.CandidateEvaluation{}, parentEnv, parentID, fold.Result{}, err
	}
	events := foldEvents(all, parentID)
	memb := membership(manifest)
	result := fold.Fold(parentEnv.Kind, parentEnv, events, memb)
	candidate.Subject = responseID
	evaluation := fold.EvaluateCandidate(parentEnv.Kind, result, candidate, parentEnv, memb)
	return evaluation, parentEnv, parentID, result, nil
}

// eventReceiptState projects an applicable evaluator outcome onto event/v1.
// The empty string is intentional for receipt-N/A events and combines with
// yaml's omitempty tag to omit the field entirely.
func eventReceiptState(evaluation fold.CandidateEvaluation) string {
	if !evaluation.Applicable {
		return ""
	}
	return string(evaluation.Outcome)
}

// resolveResponseID resolves verify's own `<response-id|parent-id>`
// ambiguity.
func resolveResponseID(mirrorDir string, target, refsHint string) (string, error) {
	parsed, err := artifact.ParseID(target)
	if err == nil && parsed.Prefix == "XS" {
		return target, nil
	}
	if refsHint != "" {
		return refsHint, nil
	}
	all, err := readAllEvents(mirrorDir)
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
		return "", fmt.Errorf("mcp: %s has no attached response", target)
	case 1:
		return candidates[0], nil
	default:
		sort.Strings(candidates)
		return "", fmt.Errorf("mcp: %s has multiple responses (%s) — disambiguate with refs", target, strings.Join(candidates, ", "))
	}
}

// verdictError renders a registry-code-carrying refusal error for a
// non-legal fold.Verdict; the CLI write surface carries its own twin.
// UNCHANGED signature: tools_contract.go's/tools_submit.go's callers (off
// this wave's allowlist) still call this exact 2-arg shape, and none of
// their own transitions can ever be a decision's own supersede (the ONE
// case that needs LFC-005/LFC-006 instead — see
// decisionSupersedeRefusalError, below), so widening this signature to
// thread that discrimination through every caller would only add dead
// parameters to callers that could never use them. The generic table-driven
// verb handler (newLifecycleHandler, tools_lifecycle.go) — the ONE write
// path whose own transition can equal TSupersede on a decision — instead
// applies the SAME discrimination checkLifecycle already applies
// (isDecisionSupersedeCandidate's own coarse test: transition ==
// "supersede" && kind == "decision") AT ITS OWN CALL SITE, calling
// decisionSupersedeRefusalError directly instead of this function. The CLI
// write surface's own verb runner does the identical thing with its own
// twins: the generic renderer there is equally unaware of the
// decision-supersede case, and the same call-site check substitutes.
//
// The comments here named those CLI functions by symbol until 2026-08-27,
// which is the documentation convention ADR-019 exists to end — caught by
// ADR-019's own gate, on the wave that wrote them.
func verdictError(id string, verdict fold.Verdict) error {
	switch verdict {
	case fold.VerdictIllegalTransition:
		return fmt.Errorf("%s: refused: illegal transition for the current folded state (LFC-001)", id)
	case fold.VerdictUnauthorizedActor:
		return fmt.Errorf("%s: refused: actor is not authorized for this transition (LFC-002)", id)
	default:
		return fmt.Errorf("%s: refused: unknown verdict %v", id, verdict)
	}
}

// decisionSupersedeRefusalError renders the LFC-005 (optionally paired with
// an LFC-006 advisory) refusal for a decision `supersede` candidate that
// CheckCandidateWithSuccessor refused as VerdictUnauthorizedActor — the
// generic table-driven verb handler's own call-site substitute for
// verdictError's generic LFC-002, exactly when evaluation.Verdict ==
// VerdictUnauthorizedActor && spec.Transition == fold.TSupersede && env.Kind
// == fold.KindDecision (newLifecycleHandler, tools_lifecycle.go — mirrors
// internal/cli's LifecycleCommand.Run's own identical call-site check).
//
// The wording is NOT this package's. It is
// validate.DecisionSupersedePreconditionMessage and
// validate.DecisionSupersedeUnresolvedMessage — one home, below both write
// surfaces, which both already import. AC 6 requires the two surfaces to
// produce the SAME refusal message, and referencing one symbol is how that
// stops depending on two people typing the same sentence.
//
// This comment said the opposite for one wave: three byte-identical copies
// existed (here, internal/cli, internal/validate) because validate's own
// builders were unexported and internal/mcp may not import internal/cli
// (ADR-001). That was reported rather than hidden, and then fixed — ADR-019's
// move-down, applied to the refusal TEXT, because the text is part of the
// rule: spec 06's discoverability instrument requires the message to name
// what would make the supersede legal.
//
// The merge silently disarmed the test that had been holding the copies in
// agreement — cmd/a2a's byte-equality parity suite can only see drift while
// the copies are independent. internal/validate/supersede_message_test.go is
// the replacement, and it asserts what the message must TEACH rather than
// that two copies match. See docs/decisions.md's ADR-019 amendment.
//
// successorResolved mirrors checkLifecycle's own `ev.SuccessorEnvelope ==
// nil` test (D9's own rule): false means the successor could not be
// resolved AT ALL (never a synthesized resolution), which pairs the LFC-006
// UNMEASURED advisory alongside LFC-005 — never alone. true means the
// successor WAS resolved and the precondition still failed — LFC-005 alone.
//
// The message names WHAT WOULD MAKE THE SUPERSEDE LEGAL (spec 06's own
// discoverability instrument, epic AC-2): an approved successor, or one the
// acting system authored.
func decisionSupersedeRefusalError(id string, successorResolved bool) error {
	// The wording is validate's, not this file's: ADR-019's move-down applied
	// to the refusal TEXT, because the text is part of the rule (spec 06's
	// discoverability instrument — the message names what would make the
	// supersede legal). internal/mcp/eventdoc.go references the same two
	// constants, so the two surfaces cannot drift by editing one.
	message := fmt.Sprintf("%s: refused: %s (LFC-005)", id, validate.DecisionSupersedePreconditionMessage)
	if !successorResolved {
		message += "; " + validate.DecisionSupersedeUnresolvedMessage + " (LFC-006)"
	}
	return fmt.Errorf("%s", message)
}

// --- shared write-verb plumbing (constructor DI) -------------------------

// Funnel is the consumer-side seam over *space.WriteFunnel (rails ISP/DI,
// mirrors internal/cli's lifecycleFunnel/submitFunnel) — tests inject a
// spy that records the SubmitRequest for the cmd/a2a equivalence suite.
type Funnel interface {
	Submit(ctx context.Context, req space.SubmitRequest) (space.WriteResult, error)
}

// SubmitHostConfig carries the write funnel's per-space host-facing
// config a SubmitRequest needs beyond the artifact content itself —
// mirrors internal/cli's SubmitHostConfig field-for-field.
type SubmitHostConfig struct {
	RemoteURL         string
	Repo              host.Repo
	BaseBranch        string
	Credential        host.Credential
	CommitAuthorName  string
	CommitAuthorEmail string
}

// WriteDeps is the common constructor-injected dependency set every write
// tool handler needs (mirrors internal/cli's lifecycleDeps).
type WriteDeps struct {
	Funnel    Funnel
	MirrorDir string
	// RepoURL is the mirror's origin — carried so a long-lived session can
	// REFRESH MirrorDir, not merely read it. See wire.go's pre-call hook.
	RepoURL      string
	SpaceID      string
	OwnSystem    string
	Manifest     space.Manifest
	HostCfg      SubmitHostConfig
	ResolveActor ActorResolver

	Now      func() time.Time
	Entropy  io.Reader
	ReadFile func(path string) ([]byte, error)

	// ResolveSpace binds this dependency set to one connected space by id.
	// nil in a single-space session, where every write already has exactly
	// one possible target and the eager fields above are the answer — the
	// same shape as WorkToolDeps.ResolveSpace (tools_work.go), and it
	// relaxes the eager fields' NewWorkTool-style validation the same way.
	//
	// SubmitDeps embeds WriteDeps, so this field is promoted onto SubmitDeps
	// too — but calling the PROMOTED closure there is a trap: it returns a
	// WriteDeps with the resolved space's MirrorDir, while SubmitDeps.Legality
	// (mirror-bound, built per-space in wire.go's buildWriteDeps) stays
	// whatever space SubmitDeps was originally built against. A submit
	// handler that resolves via the promoted field gets a legality fold
	// judged against the WRONG space's committed history while the write
	// lands in the resolved one — exactly the defect this seam exists to
	// close, one field over. SubmitDeps therefore carries its OWN
	// `ResolveSpace func(spaceID string) (SubmitDeps, error)`
	// (tools_submit.go); a submit handler uses that one, never this.
	ResolveSpace func(spaceID string) (WriteDeps, error)

	// SpaceOfArtifacts names the connected space whose mirror holds every
	// one of ids — the id most write tools already carry, used to DERIVE a
	// target when the call names no explicit space. nil in a single-space
	// session. Refuses (naming the connected spaces) when any id resolves to
	// no connected mirror, or when two ids resolve to different spaces.
	//
	// A contract id (XC-<slug>) never matches here by design: a contract
	// lives at <slug>/provides/<name>/contract.md
	// (space.Layout.ProvidesContract), not at "<id>.md" — the a2a_contract
	// family passes its own explicit "space" input instead of deriving from
	// ids, so this refusing on a contract id is the intended outcome, not a
	// gap.
	SpaceOfArtifacts func(ids []string) (string, error)
}

// resolveWriteSpace binds deps to the space this call targets. spaceID is
// the caller's explicit choice (the a2a_contract family's own "space" input,
// already declared on that tool's schema); ids are the artifacts the call
// acts on, from which the target is DERIVED when no explicit id is given.
// A single-space session returns deps unchanged — byte-identical to today,
// since deps.ResolveSpace is nil there and nothing below runs.
//
// The no-id/no-space case routes to deps.ResolveSpace(""): the production
// closure (wire.go's buildWriteDeps) is the only place that knows the
// connected-space list, so an empty spaceID there produces the same "space
// is required when multiple spaces are connected" refusal, naming every
// connected space, that WorkToolDeps.ResolveSpace's own id=="" branch
// already returns (cmd/a2a/work_wiring.go) — this file never falls back to
// Spaces[0] itself; a silent default there is precisely the defect this
// closes, one layer up.
func resolveWriteSpace(deps WriteDeps, spaceID string, ids []string) (WriteDeps, error) {
	if deps.ResolveSpace == nil {
		return deps, nil
	}
	if spaceID != "" {
		return deps.ResolveSpace(spaceID)
	}
	if len(ids) > 0 {
		name, err := deps.SpaceOfArtifacts(ids)
		if err != nil {
			return WriteDeps{}, err
		}
		return deps.ResolveSpace(name)
	}
	return deps.ResolveSpace("")
}

// buildRequest assembles a space.SubmitRequest for a batch of ids +
// files, mirroring internal/cli's lifecycleDeps.buildRequest exactly
// (commit message convention, branch/PR body shape).
func (d WriteDeps) buildRequest(ids []string, files []space.FileWrite, verb string, gated bool) space.SubmitRequest {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	commitMsg := fmt.Sprintf("a2a(%s): %s", verb, strings.Join(sorted, ", "))
	baseBranch := d.HostCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	var prBody string
	if gated {
		prBody = fmt.Sprintf("ADVISORY GATE: %s requires an approving CODEOWNERS review before auto-merge (§3.7 G3).", verb)
	}
	return space.SubmitRequest{
		RepoDir: d.MirrorDir, System: d.OwnSystem,
		Verb: verb, ArtifactID: strings.Join(sorted, "+"), ArtifactIDs: sorted, Files: files,
		CommitMessage: commitMsg, CommitAuthorName: d.HostCfg.CommitAuthorName, CommitAuthorEmail: d.HostCfg.CommitAuthorEmail,
		RemoteURL: d.HostCfg.RemoteURL, Repo: d.HostCfg.Repo, BaseBranch: baseBranch,
		PRTitle: commitMsg, PRBody: prBody, Credential: d.HostCfg.Credential,
		// Same floor, same reason as internal/cli's buildRequest — see its
		// comment for the production incident this closes. The two surfaces
		// must agree here: a floor that binds the CLI and not MCP would just
		// move the stale-binary write to the other door.
		MinBinaryVersion: d.Manifest.MinBinaryVersion,
	}
}

// submitResult is the structured StructuredContent shape every write
// tool's handler returns on success or alongside a partial-write error — the
// §7.7 structured-return contract plus P4's two-axis outcome contract.
type submitResult struct {
	Verb            string   `json:"verb"`
	IDs             []string `json:"ids"`
	Branch          string   `json:"branch"`
	PRNumber        int      `json:"pr_number,omitempty"`
	PRURL           string   `json:"pr_url"`
	CommitSHA       string   `json:"commit_sha,omitempty"`
	State           string   `json:"state"`
	Stage           string   `json:"stage,omitempty"`
	MergeMethod     string   `json:"merge_method,omitempty"`
	RemainingAction string   `json:"remaining_action,omitempty"`
	Note            string   `json:"note,omitempty"`
}

// submit runs req through d.Funnel and shapes the result/error the same
// way every write tool handler needs.
func (d WriteDeps) submit(ctx context.Context, req space.SubmitRequest, verb string, ids []string) (any, error) {
	result, err := d.Funnel.Submit(ctx, req)
	if err != nil && !hasWriteResult(result) {
		return nil, fmt.Errorf("%s: %w", verb, err)
	}
	effectiveIDs := ids
	if len(result.ArtifactIDs) > 0 {
		effectiveIDs = result.ArtifactIDs
	}
	structured := submitResult{
		Verb: verb, IDs: effectiveIDs, Branch: result.Branch,
		PRNumber: result.PRNumber, PRURL: result.PRURL, CommitSHA: result.CommitSHA,
		State: string(result.State), Stage: string(result.Stage), MergeMethod: string(result.MergeMethod),
		RemainingAction: string(result.RemainingAction), Note: result.Note,
	}
	if err != nil {
		return structured, fmt.Errorf("%s: %w", verb, err)
	}
	return structured, nil
}

// hasWriteResult distinguishes a genuine partial P4 outcome from the zero
// result returned when the funnel failed before proving any write boundary.
// WriteResult intentionally permits result+error, so checking err first and
// discarding result would erase the PR/stage a caller needs to recover.
func hasWriteResult(result space.WriteResult) bool {
	return result.Branch != "" || result.PRNumber != 0 || result.PRURL != "" ||
		result.CommitSHA != "" || result.State != "" || result.Stage != "" ||
		len(result.ArtifactIDs) != 0 || result.MergeMethod != "" ||
		result.RemainingAction != "" || result.Note != "" || result.AutoMergeNote != ""
}
