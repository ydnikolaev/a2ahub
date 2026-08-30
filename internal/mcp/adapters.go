package mcp

// This file is internal/mcp's own DI-adapter seam — a duplicate of
// internal/cli/adapters.go's LegalityAdapter/MirrorResolver/
// SubmitValidatorAdapter/actor-resolution logic (plan 14 Placement
// decisions, binding: "mcp re-wires the core... does NOT import
// internal/cli"). The original only ever depended on core packages
// (fold/schema/space/template/validate) — never anything internal/cli-
// specific — so this is a faithful port, not a reinterpretation.
//
// ADR-004 (docs/decisions.md) re-scopes plan 14's "no shared extraction"
// to "no cli<->mcp sharing": the mirror-artifact walk MirrorResolver used
// to carry as its own third, unreported copy now lives in internal/cache
// (BuildArtifactIndex, CommittedEvents) — a core package both this file
// and internal/cli/adapters.go were already allowed to import — so this
// stops being a duplicate walk while staying a duplicate ADAPTER: this
// package still never imports internal/cli, and internal/cli still never
// imports internal/mcp. Any future amendment to the parts that ARE still
// independently owned here (actor resolution, SubmitValidatorAdapter's
// event/draft partitioning) is this phase's own drift to watch for.
//
// rules-that-reach-2026-08 P5 applied the SAME ADR-004 shape one layer
// deeper: MirrorResolver's AcceptanceCriteriaCount/AcceptanceCriteriaIDs/
// ParentOf (validate.ParentCriteriaCounter/ParentCriteriaIDs/
// ResponseParentResolver) now delegate to internal/cache, closing
// KI-02301-MCP-VERDICT-RESOLVER-GAP: this surface previously carried NO
// implementation of those three capabilities at all (not a duplicate — an
// absence), so REF-019/REF-023 degraded to "cannot check" on every
// MCP-authored verify/close, silently, while the CLI surface already
// answered them. See those three methods' own doc comments, below.
//
// os.Getenv lives ONLY in this file within internal/mcp (rails "config &
// secrets": env access confined to the actor-resolution layer) —
// resolveActor is the one call site.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/agentid"
	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// --- Actor resolution (§7.4) -------------------------------------------

const (
	envActorKind  = "A2A_ACTOR_KIND"
	envActorName  = "A2A_ACTOR_NAME"
	envActorModel = "A2A_ACTOR_MODEL"
)

// ActorInput carries the structured actor override a tool call's
// arguments may supply — the highest-priority source in the §7.4 order
// (the CLI's --actor-* flags' structured-input equivalent).
type ActorInput struct {
	Kind  string `json:"kind,omitempty"`
	Name  string `json:"name,omitempty"`
	Model string `json:"model,omitempty"`
	// Session mirrors internal/cli's ActorFlags.Session — the explicit
	// override for a detected session id, in the same position in the same
	// resolution order.
	Session string `json:"session,omitempty"`
}

// ActorResolver resolves the durable identity attached to an MCP-authored
// draft or event. A missing identity is an input error, never a schema-level
// failure discovered after a remote branch and PR have already been created.
type ActorResolver func(ActorInput) (template.Actor, error)

// resolveActor resolves the actor identity per §7.4's binding order:
// explicit input > A2A_ACTOR_* env vars > OS user. MCP has no harness/config
// actor defaults, so the OS user is its final honest fallback. actor.kind
// defaults to "agent" when no source names one.
// # Detection outranks the name a tool input can carry
//
// Same inversion as internal/cli's resolver, and the same reason: the getvisa
// space records `kind: agent, name: codex` on a publish codex did not perform,
// because a caller typed the name and nothing checked it. An agent asked to
// name itself will sometimes name something else; an environment will not.
//
// This surface is where that matters MOST. Every write here is authored by a
// model filling in a structured tool input, so `actor.name` is a field the
// agent literally chooses — which is exactly how the false value got in.
//
// An explicit `kind: human` still suppresses detection.
func resolveActor(in ActorInput) (template.Actor, error) {
	return resolveActorFrom(in, ActorInput{
		Kind:  os.Getenv(envActorKind),
		Name:  os.Getenv(envActorName),
		Model: os.Getenv(envActorModel),
	}, osUsername(), os.Getenv)
}

func resolveActorFrom(in, env ActorInput, osUser string, lookup agentid.Lookup) (template.Actor, error) {
	explicitKind := firstNonEmpty(in.Kind, env.Kind)
	if !strings.EqualFold(explicitKind, "human") && lookup != nil {
		if detected, ok := agentid.Detect(lookup); ok {
			// Same boundary as internal/cli: the environment overrules a
			// claim to be a DIFFERENT agent, and nothing else. A tool input
			// naming a person, a service or a fixture is a different kind of
			// claim and stands. See agentid.Contradicts for what a wider
			// rule cost.
			claimed := firstNonEmpty(in.Name, env.Name)
			if claimed == "" || agentid.Contradicts(claimed, detected.ID) {
				return template.Actor{
					Kind:        "agent",
					KindClaimed: true, // detection IS a claim about the process
					Name:        detected.ID,
					Model:       firstNonEmpty(in.Model, env.Model, detected.Model),
					Session:     firstNonEmpty(in.Session, env.Session, detected.Session),
				}, nil
			}
			return template.Actor{
				Kind:        firstNonEmpty(explicitKind, "agent"),
				KindClaimed: true,
				Name:        claimed,
				Model:       firstNonEmpty(in.Model, env.Model, detected.Model),
				Session:     firstNonEmpty(in.Session, env.Session, detected.Session),
			}, nil
		}
	}

	name := firstNonEmpty(in.Name, env.Name, osUser)
	if name == "" {
		return template.Actor{}, ErrNoActorName
	}
	return template.Actor{
		Kind: firstNonEmpty(explicitKind, "agent"),
		// False only here, exactly as internal/cli's resolver does it: this
		// is the branch where no source named a kind and "agent" above is a
		// DEFAULT rather than a claim.
		//
		// This mirror was missed when KindClaimed shipped, and the cost was
		// precise: with it always false, fillActor never overwrote a
		// template's literal for ANY MCP-drafted artifact, so an MCP caller
		// explicitly asserting `actor.kind: agent` on a decision was
		// silently dropped and the template's `kind: human` stood. The fix
		// for one surface had broken the other.
		KindClaimed: explicitKind != "",
		Name:        name,
		Model:       firstNonEmpty(in.Model, env.Model),
		Session:     firstNonEmpty(in.Session, env.Session),
	}, nil
}

// ErrNoActorName is returned before any MCP write when no durable actor name
// can be resolved. The remedies use MCP's structured-input vocabulary.
var ErrNoActorName = errors.New("cannot determine who is acting: pass actor.name in the tool input, " +
	"or set A2A_ACTOR_NAME. Every artifact and event records its actor permanently, so a write " +
	"without one is refused rather than attributed to nobody (no OS user resolved either — " +
	"expected in a container or CI runner)")

func osUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return os.Getenv("USER")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- mirrorEvent: this package's own minimal event/v1 decode ------------

type mirrorEvent struct {
	Event      string `yaml:"event"`
	Subject    string `yaml:"subject"`
	Transition string `yaml:"transition"`
	Version    string `yaml:"version"`
	Actor      struct {
		Kind   string `yaml:"kind"`
		Name   string `yaml:"name"`
		System string `yaml:"system"`
	} `yaml:"actor"`
	// Refs is the event's own §5.2.2 `refs[]` — decoded here specifically
	// for a decision-supersede event's own successor linkage (§3.4.4:
	// "rejected | supersede (refs successor decision)"; internal/validate/
	// supersession.go's own SupersedeLink doc comment: "the real link
	// lives on the supersede EVENT's refs[].ref"). Unused by every other
	// transition this adapter reads.
	Refs []struct {
		Ref string `yaml:"ref"`
	} `yaml:"refs"`
}

// --- LegalityAdapter (validate.LegalityChecker) -------------------------

// LegalityAdapter is the concrete validate.LegalityChecker this package
// wires: it folds a candidate event's subject against events already
// committed to the connected space's mirror clone on disk (internal/space
// layout + internal/fold), mirroring internal/cli's own P6 adapter.
//
// no-silent-yes-2026-08/P6, US-3: this adapter used to carry a
// `map[string]fold.Envelope` side-channel for the candidate's own SUBJECT
// envelope, filled by a SEPARATE exported method a caller had to remember
// to call before ValidateForSubmit — CheckLegality errored at RUNTIME when
// that call was skipped ("no envelope registered for subject").
// validate.CandidateEvent now carries its own Envelope field (seam.go),
// populated at the SAME construction site that builds every other field of
// the candidate (SubmitValidatorAdapter.ValidateSubmit, below) — a
// forgotten envelope is now a zero-valued struct field visible right
// there, not a missing statement two lines away. The map, its mutex and
// that separate registration method are gone.
type LegalityAdapter struct {
	mirrorDir string
	system    string
	manifest  space.Manifest
}

// NewLegalityAdapter constructs a LegalityAdapter.
func NewLegalityAdapter(mirrorDir, system string, manifest space.Manifest) *LegalityAdapter {
	return &LegalityAdapter{mirrorDir: mirrorDir, system: system, manifest: manifest}
}

// MirrorDir is the space checkout this adapter reads. It is exported so a
// sibling validator can ask the SPACE whether a declared contract companion
// already exists, rather than inferring absence from one batch's contents.
func (a *LegalityAdapter) MirrorDir() string { return a.mirrorDir }

// CheckLegality implements validate.LegalityChecker.
func (a *LegalityAdapter) CheckLegality(candidate validate.CandidateEvent) (validate.Verdict, error) {
	env := fold.Envelope{
		ID: candidate.Subject, Kind: fold.Kind(candidate.Envelope.Kind), From: candidate.Envelope.From,
		To: candidate.Envelope.To, RequiredApprovers: candidate.Envelope.RequiredApprovers,
	}

	events, err := a.committedEvents(candidate.Subject)
	if err != nil {
		return 0, fmt.Errorf("mcp: LegalityAdapter.CheckLegality: read committed history for %q: %w", candidate.Subject, err)
	}

	// prior carries the FULL fold.Result (not just its .State) so a
	// contract on the per-version path (P4) can answer per-version rather
	// than per-subject — see fold.CheckCandidate's own doc comment.
	prior := fold.NewResult(env.Kind)
	if len(events) > 0 {
		prior = fold.Fold(env.Kind, env, events, a.membershipView)
	}

	actorStatus := a.membershipView(candidate.Actor.System)
	// successorFacts converts candidate.SuccessorEnvelope (validate's own
	// plain-string vocabulary, seam.go) into internal/fold's own
	// SuccessorFacts shape — the ONE place this conversion happens, per
	// fold.CheckCandidateWithSuccessor's own doc comment on why validate
	// must not import internal/fold's richer types directly. nil stays
	// nil (unresolved), never coerced into a zero-valued "resolved" struct
	// — see fold.SuccessorFacts' own doc comment for why that distinction
	// is load-bearing (D9's own rule).
	var successorFacts *fold.SuccessorFacts
	if candidate.SuccessorEnvelope != nil {
		successorFacts = &fold.SuccessorFacts{
			Author: candidate.SuccessorEnvelope.Author,
			State:  fold.State(candidate.SuccessorEnvelope.State),
		}
	}
	verdict := fold.CheckCandidateWithSuccessor(env.Kind, prior, candidate.Transition, candidate.Version, env, fold.Actor{
		Kind: candidate.Actor.Kind, Name: candidate.Actor.Name, System: candidate.Actor.System,
	}, actorStatus, successorFacts)

	return mapFoldVerdict(verdict), nil
}

// HasCommittedHistory reports whether subject already has at least one
// committed lifecycle event in the mirror (a2a_submit's own "already
// submitted" idempotency short-circuit).
func (a *LegalityAdapter) HasCommittedHistory(subject string) (bool, error) {
	events, err := a.committedEvents(subject)
	if err != nil {
		return false, err
	}
	return len(events) > 0, nil
}

func mapFoldVerdict(v fold.Verdict) validate.Verdict {
	switch v {
	case fold.VerdictLegal:
		return validate.VerdictLegal
	case fold.VerdictUnauthorizedActor:
		return validate.VerdictUnauthorizedActor
	default:
		return validate.VerdictIllegalTransition
	}
}

func (a *LegalityAdapter) membershipView(system string) fold.MembershipStatus {
	for _, p := range a.manifest.Participants {
		if p.System == system {
			if p.Status == fold.MembershipLeft {
				return fold.MembershipLeft
			}
			return fold.MembershipMember
		}
	}
	return fold.MembershipUnknown
}

// maxMirrorEventBytes bounds every committed-event file read (rails:
// "bounded reads everywhere").
const maxMirrorEventBytes = 1 << 20 // 1 MiB

// committedEvents delegates to internal/cache.CommittedEvents — the
// identical subject-filtered committed-history read this method and
// internal/cli's own LegalityAdapter.committedEvents used to carry
// verbatim in both adapter files (spec 01-resolver-one-home.md §5, "also
// in scope, same disease"). readBoundedFile/maxMirrorEventBytes below stay
// in this file: eventdoc.go/tools_contract.go/tools_lifecycle.go still
// call them directly for unrelated reads, so they are not this method's to
// remove.
func (a *LegalityAdapter) committedEvents(subject string) ([]fold.Event, error) {
	return cache.CommittedEvents(a.mirrorDir, a.system, subject)
}

func readBoundedFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only fd, close error is not actionable here

	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("mcp: %s exceeds %d byte read bound", path, max)
	}
	return raw, nil
}

// --- MirrorResolver (validate.Resolver) ---------------------------------

// MirrorResolver is the concrete validate.Resolver this package wires: it
// resolves known-artifact/digest/thread/system-membership facts from the
// connected space's mirror clone on disk. KnownArtifact/Digest/ThreadOf/
// ThreadExists resolve against internal/cache.BuildArtifactIndex — the
// SAME best-effort walk internal/cli's own MirrorResolver now uses (ADR-004:
// the walk moved DOWN into core; this package still never imports
// internal/cli, so the ADAPTER stays a deliberate duplicate, only the walk
// underneath it does not). System() stays manifest-local.
type MirrorResolver struct {
	mirrorDir string
	manifest  space.Manifest

	once    sync.Once
	index   map[string]cache.ArtifactIndexEntry // artifact id -> indexed facts
	skipped []cache.SkippedFile
}

// NewMirrorResolver constructs a MirrorResolver.
func NewMirrorResolver(mirrorDir string, manifest space.Manifest) *MirrorResolver {
	return &MirrorResolver{mirrorDir: mirrorDir, manifest: manifest}
}

// KnownArtifact implements validate.Resolver.
func (r *MirrorResolver) KnownArtifact(id string) bool {
	r.ensureIndex()
	_, ok := r.index[id]
	return ok
}

// Digest implements validate.Resolver. The digest is captured AT WALK TIME
// by internal/cache.BuildArtifactIndex (see its own doc comment) rather
// than re-read per call, as the former adapter-local walk did.
func (r *MirrorResolver) Digest(ref string) (string, bool) {
	r.ensureIndex()
	id, _, _ := splitRefGrammar(ref)
	entry, ok := r.index[id]
	if !ok {
		return "", false
	}
	return entry.Digest, true
}

// ThreadOf implements validate.ThreadResolver: the §3.8 thread id the
// artifact carries, and whether the artifact was found at all. Without it
// this surface's submit path fails OPEN on REF-009/REF-010 — the checks
// run, find no capability, and return no violation, which is
// indistinguishable from a clean document.
func (r *MirrorResolver) ThreadOf(id string) (thread string, found bool) {
	r.ensureIndex()
	entry, ok := r.index[id]
	if !ok {
		return "", false
	}
	return entry.Thread, true
}

// ThreadExists implements validate.ThreadResolver: whether any indexed
// artifact already carries this exact thread value. An empty thread is
// never "carried" by anything, so it is answered false rather than
// matching every threadless artifact.
func (r *MirrorResolver) ThreadExists(thread string) bool {
	if thread == "" {
		return false
	}
	r.ensureIndex()
	for _, entry := range r.index {
		if entry.Thread == thread {
			return true
		}
	}
	return false
}

// System implements validate.Resolver.
func (r *MirrorResolver) System(system string) (member bool, left bool) {
	for _, p := range r.manifest.Participants {
		if p.System == system {
			return true, p.Status == fold.MembershipLeft
		}
	}
	return false, false
}

// ActiveParticipants implements validate.ActiveParticipantLister (no-silent-
// yes-2026-08/P3 stage 2 fix wave): mirrors internal/cli's own MirrorResolver
// method (adapters.go), delegated to the SAME internal/cache.ActiveParticipants
// derivation — not a second, independently computed copy (ADR-019, docs/
// decisions.md). r.manifest is already-parsed, in-memory space.yaml, so ok is
// unconditionally true.
func (r *MirrorResolver) ActiveParticipants() (systems []string, ok bool) {
	return cache.ActiveParticipants(r.manifest), true
}

// Skipped reports every mirror file this resolver's own index build could
// not decode — internal/cache.SkippedFile, unchanged and unextended (§9,
// out of scope). SubmitValidatorAdapter.ValidateSubmit reads this (via the
// unexported skipReporter capability probe) to attach it to a returned
// *ViolationError, so a REF-009/REF-010 refusal caused by an unrelated
// file failing to parse names THAT file, not just the ref that looked
// wrong (US-2).
func (r *MirrorResolver) Skipped() []cache.SkippedFile {
	r.ensureIndex()
	return r.skipped
}

func (r *MirrorResolver) ensureIndex() {
	r.once.Do(func() {
		idx, skipped, err := cache.BuildArtifactIndex(r.mirrorDir)
		if err != nil {
			r.index = map[string]cache.ArtifactIndexEntry{}
			return
		}
		r.index = idx
		r.skipped = skipped
	})
}

// AcceptanceCriteriaCount implements validate.ParentCriteriaCounter (P6's
// REF-018 rule, internal/validate/incompleteness.go): it reports how many
// `acceptance_criteria[]` entries parentID's own frontmatter declares.
//
// rules-that-reach-2026-08 P5: this surface previously carried no
// implementation of this capability at all — internal/mcp.MirrorResolver
// satisfied validate.Resolver but not validate.ParentCriteriaCounter, so
// checkVerdictIndexRange (verdicts.go) type-asserted, failed, and degraded
// to "cannot check" on every MCP-authored verify/close, silently, even
// though the CLI surface already answered the identical question
// (KI-02301-MCP-VERDICT-RESOLVER-GAP, now retired). ADR-004 (docs/
// decisions.md) is why the fix is a delegation to
// internal/cache.AcceptanceCriteriaCount rather than a second,
// independently-worded copy of internal/cli's own former method: both
// surfaces already import internal/cache, and this package still imports
// no internal/cli symbol.
//
// ok=false covers every "cannot count" case alike — parentID absent from
// this resolver's index, its file failing to re-read/parse/decode, or
// `acceptance_criteria` absent from its frontmatter — deliberately never
// degrading to (0, true) for an absent field. See
// internal/cache.AcceptanceCriteriaCount's own doc comment for the full
// contract.
func (r *MirrorResolver) AcceptanceCriteriaCount(parentID string) (count int, ok bool) {
	r.ensureIndex()
	return cache.AcceptanceCriteriaCount(r.mirrorDir, r.index, parentID)
}

// var _ validate.ParentCriteriaCounter = (*MirrorResolver)(nil) is P5's
// own type-level gate, mirroring internal/cli/adapters.go's identical
// guard: it fails to COMPILE if AcceptanceCriteriaCount is ever removed or
// its signature drifts. The runtime type-assertion this capability rides
// (checkVerdictIndexRange) cannot otherwise distinguish "this Resolver
// deliberately cannot answer" from "this Resolver was never given the
// capability at all" — both degrade to the identical "cannot check"
// outcome, which is exactly how this surface's own gap survived five
// prior instances of this duplication class before being found by a
// person reading code rather than a test going red. This guard turns the
// next removal into a compile error instead.
var _ validate.ParentCriteriaCounter = (*MirrorResolver)(nil)

// var _ validate.ActiveParticipantLister = (*MirrorResolver)(nil) is
// AcceptanceCriteriaCount's own type-level-gate pattern, applied to this
// capability — mirrors internal/cli/adapters.go's identical guard. ADR-019's
// own detection half requires it: it turns a future silent degradation back
// to "every classification: restricted submission refuses" into a build
// error instead of a runtime capability miss nobody notices.
var _ validate.ActiveParticipantLister = (*MirrorResolver)(nil)

// Successor implements validate.SuccessorResolver (no-silent-yes-2026-08/
// P6, D7/D9; extended wave 2c, D-1/D-2 — that wave's own report): it
// resolves successorID's own envelope `from` (author), `required_
// approvers`-derived quorum and current folded lifecycle state — the
// facts internal/fold's own declared decision-supersede row preconditions
// check (table.go's SuccessorPrecondition).
//
// P6 closeout moved the read/parse/decode/fold substance into
// internal/cache.SuccessorFacts (see AcceptanceCriteriaCount's own doc
// comment above for the full move rationale, and SuccessorFacts' own doc
// comment — successor_facts.go — for D-1/D-2 and every "cannot resolve"
// case this now covers); this method is now a thin delegation over this
// resolver's own already-built index, the SAME internal/cache.SuccessorFacts
// call internal/cli's own MirrorResolver makes — not a second,
// independently typed copy of the read, and not a second membershipView
// closure either (ADR-019, docs/decisions.md).
func (r *MirrorResolver) Successor(successorID string) (author, state string, ok bool) {
	r.ensureIndex()
	return cache.SuccessorFacts(r.mirrorDir, r.index, r.manifest, successorID)
}

// var _ validate.SuccessorResolver = (*MirrorResolver)(nil) is
// AcceptanceCriteriaCount's own type-level-gate pattern, applied to this
// capability.
var _ validate.SuccessorResolver = (*MirrorResolver)(nil)

// AcceptanceCriteriaIDs implements validate.ParentCriteriaIDs
// (defects-fix-2026-08 P4). It is the SAME read as AcceptanceCriteriaCount
// above, one field deeper: the ids an id-addressed parent declares, so
// REF-019's range check and REF-023's completeness rule can resolve a
// `criterion:`-keyed verdict entry to a position.
//
// P5: a thin delegation to internal/cache.AcceptanceCriteriaIDs — see
// AcceptanceCriteriaCount's own doc comment above for the full move
// rationale.
//
// ok=false means the parent declares no ids — a plain-string
// acceptance_criteria array, an unresolvable parent, or no criteria at all.
// The consumer degrades to the ordinal path rather than refusing, which is
// resolveParentCriteriaIDs' own documented contract.
func (r *MirrorResolver) AcceptanceCriteriaIDs(parentID string) (ids []string, ok bool) {
	r.ensureIndex()
	return cache.AcceptanceCriteriaIDs(r.mirrorDir, r.index, parentID)
}

// var _ validate.ParentCriteriaIDs = (*MirrorResolver)(nil) is
// AcceptanceCriteriaCount's own type-level-gate pattern, applied to this
// second capability.
var _ validate.ParentCriteriaIDs = (*MirrorResolver)(nil)

// ParentOf implements validate.ResponseParentResolver (P6 wave C's REF-019,
// internal/validate/verdicts.go): it reports a RESPONSE artifact's own
// `parent` field, so checkVerdictIndexRange can hop from a `verify` event's
// subject (the response id) to the criteria-bearing artifact
// AcceptanceCriteriaCount actually knows how to count.
//
// P5: a thin delegation to internal/cache.ParentOf — see
// AcceptanceCriteriaCount's own doc comment above for the full move
// rationale.
//
// ok=false covers every "cannot resolve" case alike: responseID absent from
// the index, the file failing to re-read/parse/decode, or `parent` empty or
// absent (a non-response artifact, or a malformed response) — never a
// synthesized parent id.
func (r *MirrorResolver) ParentOf(responseID string) (parentID string, ok bool) {
	r.ensureIndex()
	return cache.ParentOf(r.mirrorDir, r.index, responseID)
}

// var _ validate.ResponseParentResolver = (*MirrorResolver)(nil) is
// AcceptanceCriteriaCount's own type-level-gate pattern, applied to this
// third and final capability — every mcp.NewMirrorResolver construction
// site gets all three by construction, with nothing left to remember or
// silently drop at a call site.
var _ validate.ResponseParentResolver = (*MirrorResolver)(nil)

// splitRefGrammar parses a §5.7 ref (`id`, `id@version`, `id#digest`,
// `id@version#digest`) into its id/version/digest components.
func splitRefGrammar(ref string) (id, version, digest string) {
	rest := ref
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		version = rest[i+1:]
		rest = rest[:i]
	}
	return rest, version, digest
}

// --- SubmitValidatorAdapter (space.SubmitValidator) ---------------------

// ViolationError is returned by SubmitValidatorAdapter.ValidateSubmit and
// carries the full violation list a non-Valid V2 Result found.
//
// Skipped mirrors internal/cli's own ViolationError.Skipped: the mirror
// files this surface's resolver walk could not decode, named alongside the
// violations they may have caused (US-2, spec 01-resolver-one-home.md
// §1). Unlike the shipped `{items, skipped}` StructuredContent precedent
// (internal/mcp/tools_read.go's itemsWithSkipped), this does NOT ride a
// structured payload: a HandlerFunc's error branch has no such channel —
// registry.go's own doc comment on HandlerFunc: "a non-nil error is
// surfaced as an isError:true tools/call result", message-only, no data
// field. A genuine structured error payload would need
// internal/mcp/tools_submit.go (off this brief's allowlist) to intercept
// *ViolationError before returning it as the handler's own error and
// re-shape it into StructuredContent; reported as this phase's Deviations
// item rather than routed around by smuggling JSON into the message
// string.
type ViolationError struct {
	Violations []validate.Violation
	Skipped    []cache.SkippedFile
}

func (e *ViolationError) Error() string {
	var b strings.Builder
	b.WriteString("submit validation failed:")
	for _, v := range e.Violations {
		fmt.Fprintf(&b, " [%s %s: %s]", v.Code, v.Path, v.Message)
	}
	if len(e.Skipped) > 0 {
		fmt.Fprintf(&b, " (the reference index could not decode %d file(s), which is why a resolvable ref may still fail: %s)",
			len(e.Skipped), cache.FormatSkippedList(e.Skipped))
	}
	return b.String()
}

// skipReporter is the optional capability a validate.Resolver may also
// implement — mirrors internal/cli's own skipReporter (adapters.go), a
// deliberate second copy per this file's own header comment.
type skipReporter interface {
	Skipped() []cache.SkippedFile
}

// SubmitValidatorAdapter is the concrete space.SubmitValidator the write
// funnel calls at its step 1c.
type SubmitValidatorAdapter struct {
	engine    *validate.Engine
	ownSystem string
	resolver  validate.Resolver
	legality  *LegalityAdapter
}

// NewSubmitValidatorAdapter constructs a SubmitValidatorAdapter.
func NewSubmitValidatorAdapter(engine *validate.Engine, ownSystem string, resolver validate.Resolver, legality *LegalityAdapter) *SubmitValidatorAdapter {
	return &SubmitValidatorAdapter{engine: engine, ownSystem: ownSystem, resolver: resolver, legality: legality}
}

// submitEnvelopeProbe is this package's own minimal decode of the fields
// it needs from an artifact draft.
type submitEnvelopeProbe struct {
	ID                string   `yaml:"id"`
	Type              string   `yaml:"type"`
	From              string   `yaml:"from"`
	Space             string   `yaml:"space"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
	Actor             struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"actor"`
}

// carriedFindingViolations is internal/cli/adapters.go's twin, duplicated
// on purpose: internal/mcp may never import internal/cli (ADR-001), and the
// shared half — the class decision, the codes, the messages — already lives
// DOWN in internal/space (ADR-004). What is copied here is a five-field
// struct conversion, which internal/space cannot do for either surface
// without taking an internal/validate dependency that closes an import
// cycle through internal/template.
func carriedFindingViolations(findings []space.CarriedFinding) []validate.Violation {
	if len(findings) == 0 {
		return nil
	}
	out := make([]validate.Violation, 0, len(findings))
	for _, f := range findings {
		out = append(out, validate.Violation{
			Code:     f.Code,
			Class:    validate.Class(f.Class),
			Path:     f.Path,
			Message:  f.Message,
			Severity: validate.Severity(f.Severity),
		})
	}
	return out
}

// ValidateSubmit implements space.SubmitValidator.
func (v *SubmitValidatorAdapter) ValidateSubmit(_ context.Context, files []space.FileWrite) error {
	events := map[string]mirrorEvent{}
	var drafts []space.FileWrite
	var violations []validate.Violation
	// P9 (spec 09): the MCP twin of internal/cli/adapters.go's own batch
	// classification — the SAME shared function in internal/space, not a
	// second copy of the reasoning (ADR-004, epic AC5).
	carriedClasses := make(map[string]space.Carried, len(files))
	for _, carried := range space.ClassifyCarriedBatch(files) {
		carriedClasses[carried.Path] = carried
	}
	for _, f := range files {
		switch {
		case strings.Contains(f.Path, "/events/"):
			var ev mirrorEvent
			if err := yaml.Unmarshal(f.Content, &ev); err != nil {
				return fmt.Errorf("mcp: SubmitValidatorAdapter: decode event %s: %w", f.Path, err)
			}
			events[ev.Subject] = ev

			// P1 (spec 01-the-write-gate-reaches-the-write.md §T1): the MCP
			// twin of internal/cli/adapters.go's identical call — see that
			// file's own comment on this call site for the full rationale.
			// spaceFloor comes from v.legality's own already-parsed
			// manifest, never a second space.yaml read (AC11).
			result, err := v.engine.ValidateEventWithContext(f.Content, v.legality.manifest.MinBinaryVersion,
				validate.EventContext{Resolver: v.resolver})
			if err != nil {
				return fmt.Errorf("mcp: SubmitValidatorAdapter: ValidateEventWithContext %s: %w", f.Path, err)
			}
			if !result.Valid {
				violations = append(violations, result.Violations...)
			}
		case carriedClasses[f.Path].Class.IsCompanion():
			// Contract schema/**, fixtures/** and artifacts/** are data
			// carried beside the descriptor, not envelope artifacts.
			// Compatibility/publishability owns their validation; feeding
			// them to ParseFrontmatter makes a correct MCP first-publish
			// impossible — and for a declared companion whose media type is
			// text/markdown, ParseFrontmatter's HARD ERROR (not a violation:
			// this arm returns, aborting the whole submit) was reachable
			// through no local verb at all before P9.
			//
			// This branched on space.IsContractBaselinePath directly until
			// spec 09; it now asks the ONE classifier, exactly as
			// internal/cli/adapters.go and `validate --ci` do, so the write
			// surface an agent uses is not the weaker one (epic AC5).
		default:
			drafts = append(drafts, f)
		}
	}

	// P9 S-3/S-4: the twin of internal/cli/adapters.go's identical call —
	// an undeclared carried file (POL-013) and a declared-but-absent
	// inventory entry (REF-014) are refused on this surface too, by the
	// same shared function, so `a2a_submit` and `a2a submit` reach the
	// identical verdict rather than a similar one.
	violations = append(violations, carriedFindingViolations(space.ContractCarriedMembership(files, space.PresenceFromDir(v.legality.MirrorDir())))...)

	for _, d := range drafts {
		fm, err := artifact.ParseFrontmatter(d.Content)
		if err != nil {
			return fmt.Errorf("mcp: SubmitValidatorAdapter: parse %s: %w", d.Path, err)
		}
		var probe submitEnvelopeProbe
		if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
			return fmt.Errorf("mcp: SubmitValidatorAdapter: decode envelope %s: %w", d.Path, err)
		}

		var candidates []validate.CandidateEvent
		if ev, ok := events[probe.ID]; ok {
			// no-silent-yes-2026-08/P6, US-3: the SUBJECT's own envelope is
			// carried ON the candidate now (validate.CandidateEvent.Envelope,
			// seam.go) rather than registered through a separate side-channel
			// method call — see LegalityAdapter's own doc comment for the
			// full rationale.
			candidateEnv := validate.Envelope{
				ID: probe.ID, Kind: probe.Type, From: probe.From,
				To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
			}
			var refs []string
			for _, r := range ev.Refs {
				refs = append(refs, r.Ref)
			}
			candidates = []validate.CandidateEvent{{
				Subject:    ev.Subject,
				Transition: ev.Transition,
				Actor:      validate.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
				Version:    ev.Version,
				Envelope:   candidateEnv,
				// SuccessorEnvelope (D7/D9): the SOURCE half — resolveSuccessorEnvelope's
				// own doc comment covers when this stays nil.
				SuccessorEnvelope: resolveSuccessorEnvelope(v.resolver, probe.Type, ev.Transition, refs),
			}}
		}

		result, err := v.engine.ValidateForSubmit(
			validate.Draft{Path: d.Path, Raw: d.Content},
			candidates,
			validate.LocalContext{OwnSystem: v.ownSystem, Resolver: v.resolver, Legality: v.legality},
		)
		if err != nil {
			return fmt.Errorf("mcp: SubmitValidatorAdapter: ValidateForSubmit %s: %w", d.Path, err)
		}
		if !result.Valid {
			violations = append(violations, result.Violations...)
		}
	}

	if len(violations) > 0 {
		verr := &ViolationError{Violations: violations}
		if sr, ok := v.resolver.(skipReporter); ok {
			verr.Skipped = sr.Skipped()
		}
		return verr
	}
	return nil
}

// resolveSuccessorEnvelope is D7/D9's SOURCE half (no-silent-yes-2026-08/
// P6): an optional consumer-side capability (validate.SuccessorResolver)
// type-asserted off the SAME resolver value ctx.Resolver will later see —
// the EXACT pattern seam.go's own doc comment establishes for
// ParentCriteriaCounter/ActiveParticipantLister, applied here at the
// candidate-construction site rather than inside internal/validate itself
// (that package does no I/O; this adapter already does, for the same
// candidate's own committed history).
//
// refs is the supersede event's own §5.2.2 `refs[].ref` values (§3.4.4:
// "rejected | supersede (refs successor decision)" — the successor's id).
// Every one of these leaves the result nil (UNRESOLVED, which D9's rule
// reads as "refuse", never a resolved grant): a non-supersede transition,
// a non-decision kind, no refs at all, a resolver without the capability,
// or a resolver that cannot resolve THIS particular id.
func resolveSuccessorEnvelope(resolver validate.Resolver, kind, transition string, refs []string) *validate.SuccessorEnvelope {
	if transition != "supersede" || kind != "decision" || len(refs) == 0 {
		return nil
	}
	successorResolver, capable := resolver.(validate.SuccessorResolver)
	if !capable {
		return nil
	}
	author, state, ok := successorResolver.Successor(refs[0])
	if !ok {
		return nil
	}
	return &validate.SuccessorEnvelope{Author: author, State: state}
}

// toStringSlice normalizes an envelope `to` field into a []string.
func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	default:
		return nil
	}
}
