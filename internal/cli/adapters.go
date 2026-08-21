package cli

// This file is P6's DI-adapter seam (plan 06 Placement decisions,
// binding): the concrete implementations of internal/validate's
// LegalityChecker/Resolver interfaces and internal/space's
// SubmitValidator/ManifestValidator interfaces, plus the actor-resolution
// helper (§7.4) and the PendingMarker cache no-op seam (spec 06 Open
// Q-A). cmd/a2a (lead, post-wave) constructs these with real config/
// mirror paths and wires them into the verb constructors this phase's
// other files export.
//
// os.Getenv lives ONLY in this file within internal/cli (rails "config &
// secrets": env access confined to the config/credentials/actor-
// resolution layer) — ResolveActor is the one call site.

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
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// --- Actor resolution (§7.4) -------------------------------------------

// Actor env var names this phase pins (the plan/spec name only the
// "A2A_ACTOR_*" prefix and the resolution order, not literal variable
// names — see this phase's Deviations report for the explicit call-out).
const (
	envActorKind    = "A2A_ACTOR_KIND"
	envActorName    = "A2A_ACTOR_NAME"
	envActorModel   = "A2A_ACTOR_MODEL"
	envActorSession = "A2A_ACTOR_SESSION"
)

// ActorFlags carries the explicit --actor-* flag values a verb parsed —
// the highest-priority source in the §7.4 order.
type ActorFlags struct {
	Kind  string
	Name  string
	Model string
	// Session is the explicit override for the detected session id. It has
	// no --actor-session flag today: the dotted-field pass already accepts
	// `--field actor.session=`, and adding a second spelling of one override
	// would be the duplication this whole classification exists to remove.
	Session string
}

// HarnessDefaults is the "harness adapter defaults" source (§7.4 order,
// third priority). This phase has no live harness-adapter integration
// (out of scope, no such adapter exists yet); callers pass a zero value —
// the seam exists so a later phase can supply one without touching the
// order logic here.
type HarnessDefaults struct {
	Kind  string
	Name  string
	Model string
}

// ConfigActor is the config-level fallback (§7.4 order, lowest priority).
// space.ProjectConfig does not define a default-actor block yet; callers
// pass a zero value until that lands.
type ConfigActor struct {
	Kind  string
	Name  string
	Model string
}

// ResolveActor resolves the actor identity to fill into a new draft per
// §7.4's binding order: explicit flags > A2A_ACTOR_* env vars > harness
// adapter defaults > config; actor.kind defaults to "agent" when no
// source names one.
//
// actor.Name carries one more fallback below config: the OS user
// (osUsername) — the HIGH-finding stopgap for the anonymous-actor gap.
// Every §7.4 source above it can legitimately be empty (unset flag/env, no
// harness adapter integration yet, no config default-actor block), and
// unlike Kind, Name previously had no final fallback at all, so a CLI-minted
// write could carry actor.name: "" straight through. This is deliberately
// the LAST fallback, not a new higher-priority source — it never overrides
// an explicit flag, env var, harness default, or config value.
//
// # And when even that resolves to nothing, it REFUSES here
//
// AC-1013.1. In a minimal container os/user has no passwd entry and $USER is
// unset, so every source in the chain is empty. The write was already refused
// in that case — actor.name carries a minLength in both event/v1 and
// envelope/v1, so there was never a correctness hole — but the message an
// agent got was a schema violation about a field it never knowingly set. It
// named neither the flag nor the env var that fixes it.
//
// The refusal lives HERE, at the one place the CLI resolves an actor, rather
// than at each of the ~10 verbs that write one. internal/mcp is deliberately
// untouched and keeps its own resolver (internal/mcp/adapters.go's private
// resolveActor) and its own schema-level refusal — the two surfaces have
// always had separate resolvers, so this is not a new divergence and the
// operator's MCP fence costs nothing here. An earlier reading of this file
// claimed every fix crossed into MCP; that was wrong, and it was wrong
// because it was reasoned about rather than read.
// # Detection now outranks every name a caller can type
//
// The §7.4 order above decides who acted only when internal/agentid CANNOT
// identify the running agent. When it can, that verdict wins outright: kind
// "agent", name the registry id.
//
// That inversion is deliberate and it is the point of the change. `actor.name`
// is a single slot, and letting callers fill it produced two different
// failures in the same space:
//
//   - `kind: agent, name: yuranikolaev` — the OS username, because it was the
//     last fallback and nothing above it answered. A person's login recorded
//     under a claim that a machine acted.
//   - `kind: agent, name: codex` — an agent id that is FALSE. codex did not
//     perform that publish. The name was typed at the command line, nothing
//     checked it, and it is now permanent in an append-only log.
//
// An agent asked to name itself will sometimes name something else. An
// environment will not. So the environment decides, and A2A_ACTOR_AGENT
// remains for the vendor whose detector is not wired yet.
//
// An explicit `kind: human` still suppresses detection, because that is a
// person claiming something about themselves rather than about the process.
//
// The human an agent acted FOR is not written here at all. It is derived at
// read time from the space manifest's `owners` for the acting system — where
// it already lives, and where it stays correct when the owner changes.
func ResolveActor(flags ActorFlags, harness HarnessDefaults, cfg ConfigActor) (template.Actor, error) {
	return resolveActorFrom(flags, harness, cfg, osUsername(), os.Getenv)
}

// resolveActorFrom is ResolveActor with the OS-user fallback passed IN rather
// than looked up, which is what makes the refusal testable.
//
// The alternative was a test that cleared $USER and hoped os/user would fail —
// and on a developer machine it does not, so that test SKIPPED and guarded
// nothing on the only machine anyone runs it on. A skipped test for a refusal
// is worse than no test: it reads as coverage. Taking the value as a parameter
// makes the empty case an ordinary argument, deterministic and parallel-safe,
// with no process environment involved.
// It also takes the environment as a parameter for the same reason. The
// detector below reads it, so with a package-level os.Getenv every test in
// this package would silently inherit the developer's own agent session and
// assert against whatever harness happened to be running the suite.
func resolveActorFrom(flags ActorFlags, harness HarnessDefaults, cfg ConfigActor, osUser string, env agentid.Lookup) (template.Actor, error) {
	// Only the EXPLICIT sources count here. `kind` falls back to "agent"
	// below, and treating that default as a claim would make detection
	// unreachable for everyone who never passed the flag — which is
	// everyone.
	explicitKind := firstNonEmpty(flags.Kind, env(envActorKind), harness.Kind, cfg.Kind)

	// A person declaring `kind: human` is claiming something about
	// themselves, not about the process. Detection stays out of it.
	if !strings.EqualFold(explicitKind, "human") {
		if detected, ok := agentid.Detect(env); ok {
			claimed := firstNonEmpty(flags.Name, env(envActorName), harness.Name, cfg.Name)
			if agentid.Contradicts(claimed, detected.ID) {
				return template.Actor{
					Kind:        "agent",
					KindClaimed: true, // detection IS a claim about the process
					Name:        detected.ID,
					// An explicitly passed model still wins: the environment
					// names the product reliably and the model only
					// sometimes, so a caller who knows it is adding
					// information rather than contradicting any.
					Model:   firstNonEmpty(flags.Model, env(envActorModel), harness.Model, cfg.Model, detected.Model),
					Session: firstNonEmpty(flags.Session, env(envActorSession), detected.Session),
				}, nil
			}
			if claimed == "" {
				return template.Actor{
					Kind:        "agent",
					KindClaimed: true,
					Name:        detected.ID,
					Model:       firstNonEmpty(flags.Model, env(envActorModel), harness.Model, cfg.Model, detected.Model),
					Session:     firstNonEmpty(flags.Session, env(envActorSession), detected.Session),
				}, nil
			}
			// The caller named something that is not a rival agent — a
			// person, a service, a test fixture. That is a different kind
			// of claim and it stands; detection only contributes the model.
			return template.Actor{
				Kind:        firstNonEmpty(explicitKind, "agent"),
				KindClaimed: true,
				Name:        claimed,
				Model:       firstNonEmpty(flags.Model, env(envActorModel), harness.Model, cfg.Model, detected.Model),
				Session:     firstNonEmpty(flags.Session, env(envActorSession), detected.Session),
			}, nil
		}
	}

	name := firstNonEmpty(flags.Name, env(envActorName), harness.Name, cfg.Name, osUser)
	if name == "" {
		return template.Actor{}, ErrNoActorName
	}
	return template.Actor{
		Kind: firstNonEmpty(explicitKind, "agent"),
		// KindClaimed is false here and only here: this is the branch where
		// no source named a kind and the "agent" above is a DEFAULT rather
		// than a claim. fillActor reads it to decide whether it may
		// overwrite a template's own literal.
		KindClaimed: explicitKind != "",
		Name:        name,
		Model:       firstNonEmpty(flags.Model, env(envActorModel), harness.Model, cfg.Model),
		// No detection fired on this branch, so there is no detected
		// session to carry — only an explicit one.
		Session: firstNonEmpty(flags.Session, env(envActorSession)),
	}, nil
}

// ErrNoActorName is returned when no §7.4 source names the acting identity and
// even the OS-user fallback is empty — the CI/container case.
//
// The message names both remedies, in the order a caller would reach for them,
// because the whole point of this error existing is that the schema violation
// it replaces named neither.
var ErrNoActorName = errors.New("cannot determine who is acting: pass --actor-name <name>, " +
	"or set A2A_ACTOR_NAME. Every artifact and event records its actor permanently, so a write " +
	"without one is refused rather than attributed to nobody (no OS user resolved either — " +
	"expected in a container or CI runner)")

// osUsername is ResolveActor's final non-empty fallback for actor.name: the
// OS user (os/user.Current's Username, falling back to $USER when the
// os/user lookup fails or returns an empty username — e.g. no /etc/passwd
// entry in a minimal container). If neither resolves, osUsername returns ""
// and ResolveActor REFUSES with ErrNoActorName. This function does NOT invent
// a placeholder: an actor is recorded permanently in a shared log, and a
// fabricated one is worse than a refusal.
//
// It used to be the last word here, with the schema's actor.name minLength
// left to reject the write. That still holds as the backstop on both surfaces
// — but a schema violation about a field the caller never knowingly set names
// neither `--actor-name` nor A2A_ACTOR_NAME, which is the whole reason
// ResolveActor now refuses first.
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

// mirrorEvent is internal/cli's own minimal projection of a committed
// event/v1 YAML file (§5.2.2) — just the fields the legality/submit
// adapters need. Every layer in this repo owns its own minimal decode of
// the same underlying document (fold.Event, validate.CandidateEvent,
// this struct) rather than sharing one — the established ISP idiom (see
// e.g. internal/validate/seam.go's own doc comment on CandidateEvent).
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
}

// --- LegalityAdapter (validate.LegalityChecker) -------------------------

// LegalityAdapter is the concrete validate.LegalityChecker P6 wires: it
// folds a candidate event's subject against events already committed to
// the connected space's mirror clone on disk (internal/space layout +
// internal/fold), never against internal/cache (P7, absent).
//
// validate.CandidateEvent carries no envelope (from/to/required_approvers)
// by the seam's own design (validate/seam.go: "a concrete implementation
// is expected to close over whatever locally-staged history/manifest it
// needs"). For a first-time submit the subject's artifact is not yet
// committed anywhere this adapter could read it from — the artifact is
// still a local staged draft; submit's own commit is what introduces it
// to the mirror. cmd_submit.go, which already parses the draft to build
// the write funnel's FileWrite payload, therefore calls RegisterEnvelope
// with that artifact's own envelope facts BEFORE calling
// Engine.ValidateForSubmit. This is this phase's own resolution of a real
// gap between the LegalityChecker interface's shape and what a concrete
// checker needs to answer a first-submit candidate — see this phase's
// Deviations report.
//
// It only ever answers legality for the entry (draft -> X) transitions
// this phase's verbs emit (submit/publish/propose). verify/dispute
// (response-scoped, D-024) is out of P6's verb set and returns a
// documented "unsupported in P6" error rather than a silent legal
// verdict (the KNOWN GAP the plan's Placement decisions call out,
// backlogged to P7/P8).
type LegalityAdapter struct {
	mirrorDir string
	system    string
	manifest  space.Manifest

	mu        sync.Mutex
	envelopes map[string]fold.Envelope
}

// NewLegalityAdapter constructs a LegalityAdapter reading committed
// history from mirrorDir (the connected space's local mirror clone,
// system's own section) and resolving membership against manifest
// (space.ParseManifest's own structural decode of space.yaml, as staged
// locally — pre-merge, per §5.5).
func NewLegalityAdapter(mirrorDir, system string, manifest space.Manifest) *LegalityAdapter {
	return &LegalityAdapter{mirrorDir: mirrorDir, system: system, manifest: manifest, envelopes: map[string]fold.Envelope{}}
}

// RegisterEnvelope makes subject's envelope facts available to a
// subsequent CheckLegality(candidate) call for that same subject — see
// the type's doc comment for why this closure is necessary.
func (a *LegalityAdapter) RegisterEnvelope(subject string, env fold.Envelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.envelopes[subject] = env
}

// CheckLegality implements validate.LegalityChecker.
//
// The verify/dispute guard below is PROVABLY UNREACHED, not a known gap
// awaiting a later phase, and the difference matters to anyone reading it.
// Verified exhaustively 2026-08-09 by the readiness audit: the sole call site
// of the validate.LegalityChecker interface is internal/validate's
// checkLifecycle, reached only via Engine.ValidateForSubmit, and no wired
// production caller reaches it with verify or dispute. docs/backlog.md already
// records the real work as DONE under P8: the canonical legality table and its
// regression suite cover response-scoped verify/dispute.
//
// The merge gate — the surface that actually stops anything — answers these
// transitions for real, in a sibling adapter: validate_ci_lifecycle.go's
// ciBaseLegalityChecker loads the response's PARENT envelope, folds prior
// events and calls fold.CheckLegality. That is a different body sharing only
// this condition line; the two are not duplicates of one guard.
//
// So this stays as a fail-loud backstop rather than a silent legal verdict, and
// its error text no longer claims a phase owes it.
func (a *LegalityAdapter) CheckLegality(candidate validate.CandidateEvent) (validate.Verdict, error) {
	if candidate.Transition == fold.TVerify || candidate.Transition == fold.TDispute {
		return 0, fmt.Errorf("cli: LegalityAdapter.CheckLegality: transition %q is response-scoped and this adapter folds per subject; the merge-gate path (validate --ci) answers it against the parent envelope. Reaching this is a wiring error, not a missing feature", candidate.Transition)
	}

	a.mu.Lock()
	env, ok := a.envelopes[candidate.Subject]
	a.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("cli: LegalityAdapter.CheckLegality: no envelope registered for subject %q (RegisterEnvelope must run before ValidateForSubmit)", candidate.Subject)
	}

	events, err := a.committedEvents(candidate.Subject)
	if err != nil {
		return 0, fmt.Errorf("cli: LegalityAdapter.CheckLegality: read committed history for %q: %w", candidate.Subject, err)
	}

	// prior carries the FULL fold.Result (not just its .State) so a
	// contract on the per-version path (P4) can answer per-version rather
	// than per-subject — see fold.CheckCandidate's own doc comment.
	prior := fold.NewResult(env.Kind)
	if len(events) == 0 {
		// No committed history at all: the pre-entry-event state is
		// `draft` (fold.NewResult's own doc comment) — NOT fold.Fold's
		// zero-events fallback (postSubmissionState), which answers a
		// different question (an artifact already IN the space with no
		// recorded event trail). This adapter never hits that case: the
		// candidate event's own commit is what introduces the artifact.
	} else {
		prior = fold.Fold(env.Kind, env, events, a.membershipView)
	}

	actorStatus := a.membershipView(candidate.Actor.System)
	verdict := fold.CheckCandidate(env.Kind, prior, candidate.Transition, candidate.Version, env, fold.Actor{
		Kind: candidate.Actor.Kind, Name: candidate.Actor.Name, System: candidate.Actor.System,
	}, actorStatus)

	return mapFoldVerdict(verdict), nil
}

// HasCommittedHistory reports whether subject already has at least one
// committed lifecycle event in the mirror — cmd_submit's own "already
// submitted" idempotency short-circuit (AC-301.1), which must run BEFORE
// any V2/legality/funnel work so a re-run never re-validates or re-commits.
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
			if p.Status == "left" {
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
// internal/mcp's own LegalityAdapter.committedEvents used to carry
// verbatim in both adapter files (spec 01-resolver-one-home.md §5, "also
// in scope, same disease"). readBoundedFile/maxMirrorEventBytes above stay
// in this file: cmd_contract.go/cmd_lifecycle.go/cmd_validate_ci.go still
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
		return nil, fmt.Errorf("cli: %s exceeds %d byte read bound", path, max)
	}
	return raw, nil
}

// --- MirrorResolver (validate.Resolver) ---------------------------------

// MirrorResolver is the concrete validate.Resolver P6 wires: it resolves
// known-artifact/digest/thread/system-membership facts from the connected
// space's mirror clone on disk. KnownArtifact/Digest/ThreadOf/ThreadExists
// resolve against internal/cache.BuildArtifactIndex — the SAME best-effort
// walk (bounded read -> ParseFrontmatter -> envelope decode -> id
// presence) internal/cache's own read model performs for its own index,
// complete with its []SkippedFile report, rather than a third, worse copy
// of that walk with no report at all (spec agent-ops-2026-07/specs/
// 01-resolver-one-home.md, closing a filed defect: a file that failed to
// parse used to drop silently from THIS index, so a legitimate `refs:`
// entry into a real, unrelated artifact was refused REF-009/REF-010 with
// no hint that the actual cause was a THIRD file elsewhere that would not
// decode). System() stays manifest-local — no walk answers it. The index
// is built once, lazily, on first use, and is safe for concurrent read
// after that (sync.Once).
type MirrorResolver struct {
	mirrorDir string
	manifest  space.Manifest

	once    sync.Once
	index   map[string]cache.ArtifactIndexEntry // artifact id -> its indexed facts
	skipped []cache.SkippedFile
}

// NewMirrorResolver constructs a MirrorResolver over mirrorDir (the
// connected space's local mirror clone) and manifest (the space's
// structurally-parsed space.yaml, as staged locally).
func NewMirrorResolver(mirrorDir string, manifest space.Manifest) *MirrorResolver {
	return &MirrorResolver{mirrorDir: mirrorDir, manifest: manifest}
}

// KnownArtifact implements validate.Resolver.
func (r *MirrorResolver) KnownArtifact(id string) bool {
	r.ensureIndex()
	_, ok := r.index[id]
	return ok
}

// Digest implements validate.Resolver: ref is a §5.7 ref grammar string
// (`id`, `id@version`, `id#digest`, `id@version#digest`); only the `id`
// segment is used to resolve the target file, whose digest — as captured
// by the SAME walk-time bounded read that decoded its envelope (see
// cache.BuildArtifactIndex's own doc comment on why this is walk-time, not
// re-read-per-call) — is returned.
func (r *MirrorResolver) Digest(ref string) (string, bool) {
	r.ensureIndex()
	id, _, _ := splitRefGrammar(ref)
	entry, ok := r.index[id]
	if !ok {
		return "", false
	}
	return entry.Digest, true
}

// ThreadOf implements validate.ThreadResolver.
func (r *MirrorResolver) ThreadOf(id string) (thread string, found bool) {
	r.ensureIndex()
	entry, ok := r.index[id]
	if !ok {
		return "", false
	}
	return entry.Thread, true
}

// ThreadExists implements validate.ThreadResolver: it reports whether any
// artifact already indexed from the mirror carries this exact thread
// value. An empty thread is never "carried" by anything — without this
// guard a threadless indexed artifact (thread: "") would make
// ThreadExists("") true, which is not what validate.ThreadResolver's own
// doc comment promises ("already carries this exact thread value").
// checkForeignMint (thread.go) already guards env.Thread == "" before
// ever calling this, so no current caller can observe the difference —
// this is belt-and-braces for the interface contract itself, not a fix
// to a live bug.
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
			return true, p.Status == "left"
		}
	}
	return false, false
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
			// Best-effort index build: an error here means the walk root
			// itself could not be traversed at all (walkArtifacts' own
			// per-file errors are already folded into skipped, never
			// returned here) — degrade to an empty index rather than
			// panicking or blocking every V2 check on it.
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
// P5 (rules-that-reach-2026-08) MOVED the read/parse/decode substance of
// this method into internal/cache.AcceptanceCriteriaCount, per ADR-004
// (docs/decisions.md): both internal/cli and internal/mcp already import
// internal/cache, and a second, independently-worded copy in
// internal/mcp/adapters.go would have been the sixth instance of the
// cli/mcp duplication class the ADR-001 DESIGN row tracks. What remains
// here is a thin delegation — this resolver's own already-built index
// (ensureIndex, above) is handed to the shared function rather than
// walking the mirror a second time; TestMirrorResolverAdapterCarriesNoWalk
// (adapters_test.go) still guards this file against ever regaining its
// own directory walk (AC-1.3, spec 01-resolver-one-home.md).
//
// ok=false covers every "cannot count" case alike — parentID absent from
// this resolver's index, its file failing to re-read/parse/decode, or
// `acceptance_criteria` absent from its frontmatter — deliberately never
// degrading to (0, true) for an absent field: that would make REF-018's
// caller (checkUnmetIndexRange) treat "this parent kind carries no
// acceptance_criteria[] at all" the same as "this parent declares zero
// criteria", firing REF-018 against any unmet[] entry on a parent kind
// that never had the field to begin with — a verdict change
// ParentCriteriaCounter's own doc comment (incompleteness.go) does not
// permit. See internal/cache.AcceptanceCriteriaCount's own doc comment for
// the full contract, carried forward unabridged by the move.
func (r *MirrorResolver) AcceptanceCriteriaCount(parentID string) (count int, ok bool) {
	r.ensureIndex()
	return cache.AcceptanceCriteriaCount(r.mirrorDir, r.index, parentID)
}

// AcceptanceCriteriaIDs implements validate.ParentCriteriaIDs
// (defects-fix-2026-08 P4). It is the SAME read as AcceptanceCriteriaCount
// above, one field deeper: the ids an id-addressed parent declares, so
// REF-019's range check and REF-023's completeness rule can resolve a
// `criterion:`-keyed verdict entry to a position.
//
// P5 moved the read/parse/decode substance into
// internal/cache.AcceptanceCriteriaIDs (see AcceptanceCriteriaCount's own
// doc comment above for the full move rationale); this method is now a
// thin delegation over this resolver's own already-built index.
//
// ok=false means the parent declares no ids — a plain-string
// acceptance_criteria array, an unresolvable parent, or no criteria at all.
// The consumer degrades to the ordinal path rather than refusing, which is
// resolveParentCriteriaIDs' own documented contract.
func (r *MirrorResolver) AcceptanceCriteriaIDs(parentID string) (ids []string, ok bool) {
	r.ensureIndex()
	return cache.AcceptanceCriteriaIDs(r.mirrorDir, r.index, parentID)
}

// var _ validate.ParentCriteriaIDs = (*MirrorResolver)(nil) is the same
// type-level gate the counter carries below, for the same reason.
var _ validate.ParentCriteriaIDs = (*MirrorResolver)(nil)

// var _ validate.ParentCriteriaCounter = (*MirrorResolver)(nil) is P6's
// type-level gate (2026-08-09 readiness audit, row 50): it fails to COMPILE
// if AcceptanceCriteriaCount is ever removed or its signature drifts, which
// protects every current and future cli.NewMirrorResolver construction site
// at once — no per-call-site review, and no AST scan of individual wiring
// files, can substitute for this.
var _ validate.ParentCriteriaCounter = (*MirrorResolver)(nil)

// ParentOf implements validate.ResponseParentResolver (P6 wave C's REF-019,
// internal/validate/verdicts.go): it reports a RESPONSE artifact's own
// `parent` field, so checkVerdictIndexRange can hop from a `verify` event's
// subject (the response id) to the criteria-bearing artifact
// AcceptanceCriteriaCount actually knows how to count.
//
// P5 moved the read/parse/decode substance into internal/cache.ParentOf
// (see AcceptanceCriteriaCount's own doc comment above for the full move
// rationale); this method is now a thin delegation over this resolver's
// own already-built index.
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
// ParentCriteriaCounter's own type-level-gate pattern, applied to this
// second optional upgrade: every cli.NewMirrorResolver construction site
// gets ParentOf by construction, with nothing to remember per call site.
var _ validate.ResponseParentResolver = (*MirrorResolver)(nil)

// splitRefGrammar parses a §5.7 ref (`id`, `id@version`, `id#digest`,
// `id@version#digest`) into its id/version/digest components — this
// package's own minimal copy of the same small parse internal/validate's
// referential.go performs internally (unexported there); duplicated here
// deliberately rather than exported cross-package, per the ISP pattern
// this repo already uses at every layer boundary.
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
// carries the full violation list a non-Valid V2 Result found. The write
// funnel's SubmitValidator seam takes only a plain error; this type is
// what preserves violation detail up to the CLI's JSON output
// (errors.As(err, &violationErr)).
//
// Skipped names every mirror file the resolver's own index build could not
// decode (internal/cache.SkippedFile) — populated from the resolver's
// skipReporter capability (MirrorResolver implements it) whenever
// ValidateSubmit returns violations. Before this, a REF-009/REF-010
// refusal named only the ref that looked wrong, even when the actual cause
// was a THIRD, unrelated file elsewhere in the mirror that failed to
// parse and so never made it into the resolver's index (US-2, spec
// 01-resolver-one-home.md §1).
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
		// A DIFFERENT framing sentence from skipAdvisory's own "missing
		// from this output" (skipadvisory.go): that sentence describes a
		// read verb's own item list, and reusing it here would tell a
		// reader debugging a refusal to go check an output rather than the
		// file that actually caused it — the exact wrong-target-named
		// defect class this phase exists to close (see this file's own
		// ViolationError doc comment). Only the item-list formatting is
		// shared (formatSkippedList, skipadvisory.go).
		fmt.Fprintf(&b, " (the reference index could not decode %d file(s), which is why a resolvable ref may still fail: %s)",
			len(e.Skipped), formatSkippedList(e.Skipped))
	}
	return b.String()
}

// skipReporter is the optional capability a validate.Resolver may also
// implement (the same type-assertion pattern ADR-003 uses for
// host.Forker, and thread.go's own ThreadResolver): a resolver whose index
// build produced a skip report. MirrorResolver implements it; a resolver
// double used in a test that does not need one simply doesn't, and
// ValidateSubmit degrades to an empty Skipped rather than requiring every
// validate.Resolver implementation to grow this method.
type skipReporter interface {
	Skipped() []cache.SkippedFile
}

// SubmitValidatorAdapter is the concrete space.SubmitValidator the write
// funnel calls at its step 1c (internal/space/funnel.go): it partitions
// the about-to-be-committed files into artifact drafts and their paired
// lifecycle event files (D-026: one commit, one event per artifact),
// registers each artifact's own envelope facts with the injected
// LegalityAdapter, then delegates to Engine.ValidateForSubmit — mapping a
// non-Valid Result to a *ViolationError.
type SubmitValidatorAdapter struct {
	engine    *validate.Engine
	ownSystem string
	resolver  validate.Resolver
	legality  *LegalityAdapter
}

// NewSubmitValidatorAdapter constructs a SubmitValidatorAdapter. engine,
// resolver and legality are required (a nil dependency used at runtime is
// a constructor bug, rails anti-pattern #10).
func NewSubmitValidatorAdapter(engine *validate.Engine, ownSystem string, resolver validate.Resolver, legality *LegalityAdapter) *SubmitValidatorAdapter {
	return &SubmitValidatorAdapter{engine: engine, ownSystem: ownSystem, resolver: resolver, legality: legality}
}

// submitEnvelopeProbe is this package's own minimal decode of the fields
// it needs from an artifact draft: the SubmitValidatorAdapter uses it to
// build the fold.Envelope a LegalityAdapter registration needs;
// cmd_submit.go (this package's own sibling file) reuses the SAME struct
// rather than declaring a second, near-identical one. Note Actor here is
// the base envelope's actor shape (kind/name/model — no `system`, unlike
// the event actor block); cmd_submit.go always resolves the committed
// event's own actor.system from the configured own system, never from
// this field.
type submitEnvelopeProbe struct {
	ID                string   `yaml:"id"`
	Type              string   `yaml:"type"`
	From              string   `yaml:"from"`
	Space             string   `yaml:"space"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
	// Parent is a response's own §3.4.6 linkage to the exchange it
	// answers (response only — same tag/comment as cmd_lifecycle.go's
	// lifecycleEnvelopeProbe.Parent). ValidateSubmit reads it to widen
	// THIS draft's own candidate-event list to events over the artifact
	// it refers to (see ValidateSubmit's own comment, LFC-004).
	Parent string `yaml:"parent"`
	Actor  struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
		// Model and Session are DETECTED (schemas/fill-classes.yaml) and
		// were decoded nowhere on this path. The submit event records who
		// authored the artifact being submitted, so it reads the DRAFT's
		// own actor — a fact already on disk — rather than re-resolving one
		// at submit time, which would attribute the authoring to whoever
		// happened to run `a2a submit`.
		Model   string `yaml:"model"`
		Session string `yaml:"session"`
	} `yaml:"actor"`
}

// carriedFindingViolations maps internal/space's registry-coded membership
// findings into this package's Violation shape. It is a field copy, not a
// decision: the code, class, severity and message are all internal/space's
// answer, so `a2a submit`, `a2a validate --all` and `validate --ci` cannot
// name different codes for one disagreement.
//
// internal/space returns its own struct rather than a validate.Violation
// because internal/template imports internal/space and internal/validate's
// own tests import internal/template — that edge is an import cycle Go
// refuses at test-build time (carried_class.go says so at the type).
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
	var registries []space.FileWrite
	var violations []validate.Violation
	// P9 (spec 09): ONE classifier decides what each carried path is, for
	// this surface and for `validate --ci` alike — see the contract arm
	// below. Resolved once for the whole batch so a contract's descriptor
	// is decoded once, not once per companion.
	carriedClasses := make(map[string]space.Carried, len(files))
	for _, carried := range space.ClassifyCarriedBatch(files) {
		carriedClasses[carried.Path] = carried
	}
	for _, f := range files {
		switch {
		case strings.Contains(f.Path, "/events/"):
			var ev mirrorEvent
			if err := yaml.Unmarshal(f.Content, &ev); err != nil {
				return fmt.Errorf("cli: SubmitValidatorAdapter: decode event %s: %w", f.Path, err)
			}
			events[ev.Subject] = ev

			// P1 (spec 01-the-write-gate-reaches-the-write.md §T1): the ONLY
			// place a `verify`/`close` submission — event files exclusively,
			// so the drafts loop below never runs — gets judged at all.
			// ValidateEventWithContext is the entry point cmd_validate_ci.go's
			// validateCIEvent already calls at the merge gate; this is the
			// same call one layer earlier. spaceFloor comes from
			// v.legality's own already-parsed manifest (readMinBinaryVersion
			// (cmd_submit.go) is what populated it) — never a second
			// space.yaml read (AC11).
			result, err := v.engine.ValidateEventWithContext(f.Content, v.legality.manifest.MinBinaryVersion,
				validate.EventContext{Resolver: v.resolver})
			if err != nil {
				return fmt.Errorf("cli: SubmitValidatorAdapter: ValidateEventWithContext %s: %w", f.Path, err)
			}
			if !result.Valid {
				violations = append(violations, result.Violations...)
			}
		case isConsumesRegistry(f.Path):
			// The D-022 consumer registry is a non-artifact file (no
			// envelope, no frontmatter) that the funnel nonetheless
			// carries — it gets the consumes/v1 schema check here, the
			// SAME one the space's V3 runs, so a write can never land a
			// registry the space would then reject.
			registries = append(registries, f)
		case carriedClasses[f.Path].Class.IsCompanion():
			// A contract's own schema/**, fixtures/** and artifacts/**
			// (P37 D-D + contract-set-v2's companion root), carried
			// alongside contract.md by `submit`. They are not artifacts:
			// no envelope, no frontmatter, no id. Feeding one to the drafts
			// loop below runs artifact.ParseFrontmatter over a JSON schema
			// and refuses the WHOLE submit — so the very files D-D requires
			// would make the contract unsubmittable.
			//
			// P9 (spec 09, fb-20260820-d1e370) replaced this arm's
			// `space.IsContractBaselinePath(f.Path)` guard with the ONE
			// classifier every reader now consults. The predicate said only
			// "this path lives beside a descriptor"; the classifier says
			// WHICH file the descriptor declares it to be, which is the
			// fact `validate --ci` needs and used to answer for itself by
			// filename suffix. Two halves of one binary disagreeing about
			// one path is what made a declared changelog submittable and
			// unmergeable at the same time.
			//
			// Still validated by no ARTIFACT class here — that part of the
			// old comment was right and stands. What is no longer true is
			// "validated NOWHERE": ContractCarriedMembership judges the
			// carried set against the descriptor's inventory in both
			// directions at cmd_submit.go's pre-flight, before this funnel
			// is reached at all. The compatibility and carried-set rules
			// that read the bytes still live in internal/validate and still
			// run at publish and at merge; re-deciding those here would be
			// the second copy AC-970.2 exists to forbid, and §5.4b permits
			// non-JSON-Schema formats whose files this engine could not
			// parse at all.
		default:
			drafts = append(drafts, f)
		}
	}

	// P9 S-3/S-4 at the funnel itself, so the rule cannot be reached only
	// through cmd_submit.go's pre-flight: the identical call, on the
	// identical shared function, is what makes the MCP surface's verdict
	// identical rather than merely similar (epic AC5).
	violations = append(violations, carriedFindingViolations(space.ContractCarriedMembership(files))...)

	for _, r := range registries {
		result, err := v.engine.ValidateConsumes(r.Content)
		if err != nil {
			return fmt.Errorf("cli: SubmitValidatorAdapter: ValidateConsumes %s: %w", r.Path, err)
		}
		if !result.Valid {
			violations = append(violations, result.Violations...)
		}
	}
	for _, d := range drafts {
		fm, err := artifact.ParseFrontmatter(d.Content)
		if err != nil {
			return fmt.Errorf("cli: SubmitValidatorAdapter: parse %s: %w", d.Path, err)
		}
		var probe submitEnvelopeProbe
		if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
			return fmt.Errorf("cli: SubmitValidatorAdapter: decode envelope %s: %w", d.Path, err)
		}

		var candidates []validate.CandidateEvent
		if ev, ok := events[probe.ID]; ok {
			v.legality.RegisterEnvelope(probe.ID, fold.Envelope{
				ID: probe.ID, Kind: fold.Kind(probe.Type), From: probe.From,
				To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
			})
			candidates = append(candidates, validate.CandidateEvent{
				Subject:    ev.Subject,
				Transition: ev.Transition,
				Actor:      validate.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
				Version:    ev.Version,
			})
		}

		// LFC-004 (internal/validate/incompleteness.go's checkResidue) needs
		// a CandidateEvent, in this SAME ValidateForSubmit call, whose
		// Subject is this draft's own `parent` and whose Transition closes
		// it — a closing event always targets the PARENT's id, never the
		// response's own, so the events[probe.ID] lookup above can never
		// see it (incompleteness.go's package-doc Deviation names this
		// exact gap: "dormant through the one wired caller"). This is that
		// caller-side widening — THIS draft's own candidate list only,
		// never every draft's, and gated on the same terminal-transition
		// set incompleteness.go's terminalParentTransitions declares.
		//
		// Deliberately excludes `respond`: cmd_lifecycle.go's RespondCommand
		// authors a fresh response draft alongside a `respond` event whose
		// Subject is that SAME parent, in the SAME batch — an unfiltered
		// subject-only widen would attach that event here too, and
		// checkLifecycle (engine.go) runs CheckLegality on every candidate
		// it is handed. CheckLegality (below, this file) errors when no
		// envelope is registered for the candidate's Subject, and this loop
		// only ever registers the draft's OWN probe.ID (above) — never an
		// unrelated parent's, which this adapter has no way to look up (the
		// same seam.go Resolver gap incompleteness.go's own
		// ParentCriteriaCounter Deviation documents: no content lookup).
		// Restricting the widen to close/withdraw/cancel keeps `a2a respond`
		// batches unaffected: no verb in this codebase closes a parent in
		// the SAME batch as a fresh draft that names it as parent, so this
		// widening is provably inert against every wired caller today —
		// see this brief's own report for the residual finding (a future
		// caller that DOES co-submit such a batch must call
		// v.legality.RegisterEnvelope(probe.Parent, ...) itself first, or
		// CheckLegality's own error fires instead of a violation).
		if probe.Parent != "" && probe.Parent != probe.ID {
			if ev, ok := events[probe.Parent]; ok && submitTerminalParentTransitions[ev.Transition] {
				candidates = append(candidates, validate.CandidateEvent{
					Subject:    ev.Subject,
					Transition: ev.Transition,
					Actor:      validate.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
					Version:    ev.Version,
				})
			}
		}

		result, err := v.engine.ValidateForSubmit(
			validate.Draft{Path: d.Path, Raw: d.Content},
			candidates,
			validate.LocalContext{OwnSystem: v.ownSystem, Resolver: v.resolver, Legality: v.legality},
		)
		if err != nil {
			return fmt.Errorf("cli: SubmitValidatorAdapter: ValidateForSubmit %s: %w", d.Path, err)
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

// submitTerminalParentTransitions mirrors internal/validate/
// incompleteness.go's own terminalParentTransitions (D3's exact
// three-member set — close, withdraw, cancel — unexported there).
// Duplicated here, rather than exporting a predicate from
// internal/validate (off this wave's allowlist), so ValidateSubmit's own
// widening above cannot silently drift wider than what checkResidue's
// LFC-004 trigger needs — see this wave's own report for the ideal fix
// (export a shared predicate from internal/validate so this literal has
// one owner instead of two).
var submitTerminalParentTransitions = map[string]bool{
	"close":    true,
	"withdraw": true,
	"cancel":   true,
}

// toStringSlice normalizes an envelope `to` field (either a []any of
// strings, per YAML decode, or the literal "all") into a []string —
// fold.Envelope's own shape. "all" is represented as a single-element
// slice; nothing in P6's entry-transition legality checks (RoleOwner,
// which only reads From) ever consults To for a broadcast, so the exact
// broadcast representation is not load-bearing here.
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

// --- ManifestValidatorAdapter (space.ManifestValidator) -----------------

// ManifestValidatorAdapter is the concrete space.ManifestValidator. It calls
// validate.Engine rather than addressing the schema corpus directly so manifest
// shape and authority-map policy have one owner on both the load seam and V3.
type ManifestValidatorAdapter struct {
	engine *validate.Engine
}

// NewManifestValidatorAdapter constructs a ManifestValidatorAdapter over
// corpus (schema.Load's result).
func NewManifestValidatorAdapter(corpus *schema.Corpus) *ManifestValidatorAdapter {
	return &ManifestValidatorAdapter{engine: validate.New(corpus)}
}

// ValidateManifest implements space.ManifestValidator.
func (m *ManifestValidatorAdapter) ValidateManifest(_ context.Context, raw []byte) error {
	result, err := m.engine.ValidateManifest(raw)
	if err != nil {
		return fmt.Errorf("cli: ManifestValidatorAdapter: %w", err)
	}
	if result.Valid {
		return nil
	}
	var b strings.Builder
	b.WriteString("manifest violations:")
	for _, v := range result.Violations {
		fmt.Fprintf(&b, " [%s %s: %s]", v.Code, v.Path, v.Message)
	}
	return fmt.Errorf("cli: %s", b.String())
}

// --- PendingMarker (P7 cache seam) --------------------------------------

// PendingMarker is the future internal/cache seam (P7, blocked_by: [P6]):
// cmd_submit calls MarkPending once per successfully-submitted artifact
// with the funnel's WriteResult (the "local cache marks pending-merge"
// step, §7.2 OP-205); cmd_sync calls it once per refreshed space with an
// empty artifactID and a zero WriteResult (the "refresh local cache"
// step, §7.2 OP-206) — this phase's own calling convention, since the
// seam is one method covering both call sites (spec 06 Open Q-A
// resolution: an explicit call-site, never a silent skip). This phase's
// injected implementation is a pure no-op; P7 supplies the real
// internal/cache-backed one later.
type PendingMarker interface {
	MarkPending(ctx context.Context, spaceID, artifactID string, result space.WriteResult) error
}

// PendingWrite is the CLI-facing projection of a machine-local marker.
type PendingWrite struct {
	Branch   string
	PRNumber int
	PRURL    string
}

// PendingMarkerReader is the optional marker lookup used by lifecycle
// diagnosis and await without widening the original P6 seam.
type PendingMarkerReader interface {
	Pending(spaceID, artifactID string) (PendingWrite, bool, error)
}

// PendingMarkerClearer is the optional marker cleanup used after await
// observes a successful merge and refresh.
type PendingMarkerClearer interface {
	ClearPending(spaceID, artifactID string) error
}

// PendingMarkerReconciler is the optional post-refresh cleanup seam. A real
// cache-backed marker store removes markers whose artifacts are now visible in
// the refreshed canonical mirror; no-op and test adapters may omit it.
type PendingMarkerReconciler interface {
	ReconcilePending(spaceID, mirrorDir string) error
}

// NoopPendingMarker is P6's injected no-op PendingMarker.
type NoopPendingMarker struct{}

// NewNoopPendingMarker constructs a NoopPendingMarker.
func NewNoopPendingMarker() *NoopPendingMarker { return &NoopPendingMarker{} }

// MarkPending implements PendingMarker as a pure no-op.
func (NoopPendingMarker) MarkPending(context.Context, string, string, space.WriteResult) error {
	return nil
}

// Pending implements PendingMarkerReader as a pure no-op lookup.
func (NoopPendingMarker) Pending(string, string) (PendingWrite, bool, error) {
	return PendingWrite{}, false, nil
}

// ClearPending implements PendingMarkerClearer as a pure no-op cleanup.
func (NoopPendingMarker) ClearPending(string, string) error { return nil }

// ReconcilePending implements PendingMarkerReconciler as a pure no-op.
func (NoopPendingMarker) ReconcilePending(string, string) error { return nil }

// CacheRemover is the future internal/cache seam for `a2a disconnect`'s
// "remove config entry + mirror + cache for that space" step (§7.2
// OP-202) — a distinct seam from PendingMarker (that one marks a pending
// state; this one clears cached state for a space being disconnected).
// This phase's injected implementation is a pure no-op; P7 supplies the
// real internal/cache-backed one later.
type CacheRemover interface {
	RemoveSpace(ctx context.Context, spaceID string) error
}

// NoopCacheRemover is P6's injected no-op CacheRemover.
type NoopCacheRemover struct{}

// NewNoopCacheRemover constructs a NoopCacheRemover.
func NewNoopCacheRemover() *NoopCacheRemover { return &NoopCacheRemover{} }

// RemoveSpace implements CacheRemover as a pure no-op.
func (NoopCacheRemover) RemoveSpace(context.Context, string) error { return nil }

// warnAutoMerge surfaces a write's auto-merge note on stderr, if any.
//
// The note is non-empty when the PR opened but auto-merge could not be armed
// — the repository has it disabled, or the PR is already mergeable. Neither
// is a failed write, so the verb still succeeds; but the state it reports is
// "pending-merge", and nothing will act on that pending unless a human does.
// Saying so is the difference between a PR that is in flight and a PR that
// only looks like it.
//
// Written to Stderr deliberately: stdout stays the machine-readable result
// line that harnesses parse.
func warnAutoMerge(stdio IO, verb, note string) {
	if note == "" {
		return
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "%s: NOTE auto-merge is not armed — %s\n", verb, note)
}
