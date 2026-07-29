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
}

// ActorResolver resolves the durable identity attached to an MCP-authored
// draft or event. A missing identity is an input error, never a schema-level
// failure discovered after a remote branch and PR have already been created.
type ActorResolver func(ActorInput) (template.Actor, error)

// resolveActor resolves the actor identity per §7.4's binding order:
// explicit input > A2A_ACTOR_* env vars > OS user. MCP has no harness/config
// actor defaults, so the OS user is its final honest fallback. actor.kind
// defaults to "agent" when no source names one.
func resolveActor(in ActorInput) (template.Actor, error) {
	return resolveActorFrom(in, ActorInput{
		Kind:  os.Getenv(envActorKind),
		Name:  os.Getenv(envActorName),
		Model: os.Getenv(envActorModel),
	}, osUsername())
}

func resolveActorFrom(in, env ActorInput, osUser string) (template.Actor, error) {
	name := firstNonEmpty(in.Name, env.Name, osUser)
	if name == "" {
		return template.Actor{}, ErrNoActorName
	}
	return template.Actor{
		Kind:  firstNonEmpty(in.Kind, env.Kind, "agent"),
		Name:  name,
		Model: firstNonEmpty(in.Model, env.Model),
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
}

// --- LegalityAdapter (validate.LegalityChecker) -------------------------

// LegalityAdapter is the concrete validate.LegalityChecker this package
// wires: it folds a candidate event's subject against events already
// committed to the connected space's mirror clone on disk (internal/space
// layout + internal/fold), mirroring internal/cli's own P6 adapter.
type LegalityAdapter struct {
	mirrorDir string
	system    string
	manifest  space.Manifest

	mu        sync.Mutex
	envelopes map[string]fold.Envelope
}

// NewLegalityAdapter constructs a LegalityAdapter.
func NewLegalityAdapter(mirrorDir, system string, manifest space.Manifest) *LegalityAdapter {
	return &LegalityAdapter{mirrorDir: mirrorDir, system: system, manifest: manifest, envelopes: map[string]fold.Envelope{}}
}

// RegisterEnvelope makes subject's envelope facts available to a
// subsequent CheckLegality(candidate) call for that same subject.
func (a *LegalityAdapter) RegisterEnvelope(subject string, env fold.Envelope) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.envelopes[subject] = env
}

// CheckLegality implements validate.LegalityChecker.
func (a *LegalityAdapter) CheckLegality(candidate validate.CandidateEvent) (validate.Verdict, error) {
	a.mu.Lock()
	env, ok := a.envelopes[candidate.Subject]
	a.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("mcp: LegalityAdapter.CheckLegality: no envelope registered for subject %q (RegisterEnvelope must run before ValidateForSubmit)", candidate.Subject)
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
	verdict := fold.CheckCandidate(env.Kind, prior, candidate.Transition, candidate.Version, env, fold.Actor{
		Kind: candidate.Actor.Kind, Name: candidate.Actor.Name, System: candidate.Actor.System,
	}, actorStatus)

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
			r.index = map[string]cache.ArtifactIndexEntry{}
			return
		}
		r.index = idx
		r.skipped = skipped
	})
}

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

// ValidateSubmit implements space.SubmitValidator.
func (v *SubmitValidatorAdapter) ValidateSubmit(_ context.Context, files []space.FileWrite) error {
	events := map[string]mirrorEvent{}
	var drafts []space.FileWrite
	for _, f := range files {
		if strings.Contains(f.Path, "/events/") {
			var ev mirrorEvent
			if err := yaml.Unmarshal(f.Content, &ev); err != nil {
				return fmt.Errorf("mcp: SubmitValidatorAdapter: decode event %s: %w", f.Path, err)
			}
			events[ev.Subject] = ev
			continue
		}
		drafts = append(drafts, f)
	}

	var violations []validate.Violation
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
			v.legality.RegisterEnvelope(probe.ID, fold.Envelope{
				ID: probe.ID, Kind: fold.Kind(probe.Type), From: probe.From,
				To: toStringSlice(probe.To), RequiredApprovers: probe.RequiredApprovers,
			})
			candidates = []validate.CandidateEvent{{
				Subject:    ev.Subject,
				Transition: ev.Transition,
				Actor:      validate.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
				Version:    ev.Version,
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
