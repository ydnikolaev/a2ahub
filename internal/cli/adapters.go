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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
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
	envActorKind  = "A2A_ACTOR_KIND"
	envActorName  = "A2A_ACTOR_NAME"
	envActorModel = "A2A_ACTOR_MODEL"
)

// ActorFlags carries the explicit --actor-* flag values a verb parsed —
// the highest-priority source in the §7.4 order.
type ActorFlags struct {
	Kind  string
	Name  string
	Model string
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
func ResolveActor(flags ActorFlags, harness HarnessDefaults, cfg ConfigActor) template.Actor {
	return template.Actor{
		Kind:  firstNonEmpty(flags.Kind, os.Getenv(envActorKind), harness.Kind, cfg.Kind, "agent"),
		Name:  firstNonEmpty(flags.Name, os.Getenv(envActorName), harness.Name, cfg.Name),
		Model: firstNonEmpty(flags.Model, os.Getenv(envActorModel), harness.Model, cfg.Model),
	}
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
func (a *LegalityAdapter) CheckLegality(candidate validate.CandidateEvent) (validate.Verdict, error) {
	if candidate.Transition == fold.TVerify || candidate.Transition == fold.TDispute {
		return 0, fmt.Errorf("cli: LegalityAdapter.CheckLegality: transition %q is unsupported in P6 (verify/dispute legality is a P7/P8 backlog item, not a silent legal verdict)", candidate.Transition)
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

	var state fold.State
	if len(events) == 0 {
		// No committed history at all: the pre-entry-event state is
		// `draft` (fold.NewResult's own doc comment) — NOT fold.Fold's
		// zero-events fallback (postSubmissionState), which answers a
		// different question (an artifact already IN the space with no
		// recorded event trail). This adapter never hits that case: the
		// candidate event's own commit is what introduces the artifact.
		state = fold.NewResult(env.Kind).State
	} else {
		state = fold.Fold(env.Kind, env, events, a.membershipView).State
	}

	actorStatus := a.membershipView(candidate.Actor.System)
	verdict := fold.CheckLegality(env.Kind, state, candidate.Transition, env, fold.Actor{
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

func (a *LegalityAdapter) committedEvents(subject string) ([]fold.Event, error) {
	dir := filepath.Join(a.mirrorDir, a.system, "events")
	years, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var out []fold.Event
	for _, year := range years {
		if !year.IsDir() {
			continue
		}
		yearDir := filepath.Join(dir, year.Name())
		files, err := os.ReadDir(yearDir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}
			raw, err := readBoundedFile(filepath.Join(yearDir, f.Name()), maxMirrorEventBytes)
			if err != nil {
				return nil, err
			}
			var ev mirrorEvent
			if err := yaml.Unmarshal(raw, &ev); err != nil {
				return nil, fmt.Errorf("cli: decode committed event %s: %w", f.Name(), err)
			}
			if ev.Subject != subject {
				continue
			}
			out = append(out, fold.Event{
				ULID:       ev.Event,
				Subject:    ev.Subject,
				Transition: ev.Transition,
				Actor:      fold.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
			})
		}
	}
	return out, nil
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
// known-artifact/digest/system-membership facts from the connected
// space's mirror clone on disk (never internal/cache, P7, absent). The
// artifact index is built once, lazily, on first use, and is safe for
// concurrent read after that (sync.Once).
type MirrorResolver struct {
	mirrorDir string
	manifest  space.Manifest

	once  sync.Once
	index map[string]string // artifact id -> mirror-relative path
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
// segment is used to resolve the target file, whose current on-disk
// digest is returned.
func (r *MirrorResolver) Digest(ref string) (string, bool) {
	r.ensureIndex()
	id, _, _ := splitRefGrammar(ref)
	relPath, ok := r.index[id]
	if !ok {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(r.mirrorDir, relPath))
	if err != nil {
		return "", false
	}
	return artifact.Digest(raw), true
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

func (r *MirrorResolver) ensureIndex() {
	r.once.Do(func() {
		r.index = map[string]string{}
		_ = filepath.WalkDir(r.mirrorDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				// Best-effort index build: an unreadable file/dir is
				// simply skipped, never fails the whole walk.
				return nil
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			fm, ferr := artifact.ParseFrontmatter(raw)
			if ferr != nil {
				return nil
			}
			var probe struct {
				ID string `yaml:"id"`
			}
			if yerr := yaml.Unmarshal(fm.YAML, &probe); yerr != nil || probe.ID == "" {
				return nil
			}
			rel, relErr := filepath.Rel(r.mirrorDir, path)
			if relErr != nil {
				return nil
			}
			r.index[probe.ID] = rel
			return nil
		})
	})
}

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
type ViolationError struct {
	Violations []validate.Violation
}

func (e *ViolationError) Error() string {
	var b strings.Builder
	b.WriteString("submit validation failed:")
	for _, v := range e.Violations {
		fmt.Fprintf(&b, " [%s %s: %s]", v.Code, v.Path, v.Message)
	}
	return b.String()
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
				return fmt.Errorf("cli: SubmitValidatorAdapter: decode event %s: %w", f.Path, err)
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
			candidates = []validate.CandidateEvent{{
				Subject:    ev.Subject,
				Transition: ev.Transition,
				Actor:      validate.Actor{Kind: ev.Actor.Kind, Name: ev.Actor.Name, System: ev.Actor.System},
			}}
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
		return &ViolationError{Violations: violations}
	}
	return nil
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

// ManifestValidatorAdapter is the concrete space.ManifestValidator P6
// wires: SCHEMA-CLASS validation only (internal/schema's manifest corpus)
// — referential/policy manifest checks exist in no package yet (a tracked
// backlog row per space.ManifestValidator's own doc comment); this
// adapter does not invent that scope.
type ManifestValidatorAdapter struct {
	corpus *schema.Corpus
}

// NewManifestValidatorAdapter constructs a ManifestValidatorAdapter over
// corpus (schema.Load's result).
func NewManifestValidatorAdapter(corpus *schema.Corpus) *ManifestValidatorAdapter {
	return &ManifestValidatorAdapter{corpus: corpus}
}

// ValidateManifest implements space.ManifestValidator.
func (m *ManifestValidatorAdapter) ValidateManifest(_ context.Context, raw []byte) error {
	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		return fmt.Errorf("cli: ManifestValidatorAdapter: decode: %w", err)
	}

	// space.schema.json's own `schema` const is literally "space/v1" (not
	// "manifest/v1" — a documented naming tension in the schema file's own
	// description, unresolved upstream); the fallback below only matters
	// when raw carries no `schema` field of its own to read.
	version := "space/v1"
	if doc, ok := instance.(map[string]any); ok {
		if s, ok := doc["schema"].(string); ok && s != "" {
			version = s
		}
	}

	violations, err := m.corpus.ValidateManifest(version, instance)
	if err != nil {
		return fmt.Errorf("cli: ManifestValidatorAdapter: %w", err)
	}
	if len(violations) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("manifest schema violations:")
	for _, v := range violations {
		fmt.Fprintf(&b, " [%s: %s]", v.Path, v.Keyword)
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

// NoopPendingMarker is P6's injected no-op PendingMarker.
type NoopPendingMarker struct{}

// NewNoopPendingMarker constructs a NoopPendingMarker.
func NewNoopPendingMarker() *NoopPendingMarker { return &NoopPendingMarker{} }

// MarkPending implements PendingMarker as a pure no-op.
func (NoopPendingMarker) MarkPending(context.Context, string, string, space.WriteResult) error {
	return nil
}

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
