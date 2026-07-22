package mcp

// This file is internal/mcp's own DI-adapter seam — a duplicate of
// internal/cli/adapters.go's LegalityAdapter/MirrorResolver/
// SubmitValidatorAdapter/actor-resolution logic (plan 14 Placement
// decisions, binding: "mcp re-wires the core... does NOT import
// internal/cli"). The original only ever depended on core packages
// (fold/schema/space/template/validate) — never anything internal/cli-
// specific — so this is a faithful port, not a reinterpretation; any
// future amendment to either copy is this phase's own drift to watch for
// (there is no shared extraction point per the plan's binding decision).
//
// os.Getenv lives ONLY in this file within internal/mcp (rails "config &
// secrets": env access confined to the actor-resolution layer) —
// resolveActor is the one call site.

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

// resolveActor resolves the actor identity to fill into a new draft or
// event per §7.4's binding order: explicit input > A2A_ACTOR_* env vars >
// harness/config defaults (this phase has no live harness-adapter
// integration, mirroring internal/cli's own P6 scope); actor.kind
// defaults to "agent" when no source names one.
func resolveActor(in ActorInput) template.Actor {
	return template.Actor{
		Kind:  firstNonEmpty(in.Kind, os.Getenv(envActorKind), "agent"),
		Name:  firstNonEmpty(in.Name, os.Getenv(envActorName)),
		Model: firstNonEmpty(in.Model, os.Getenv(envActorModel)),
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

	var state fold.State
	if len(events) == 0 {
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
				return nil, fmt.Errorf("mcp: decode committed event %s: %w", f.Name(), err)
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
		return nil, fmt.Errorf("mcp: %s exceeds %d byte read bound", path, max)
	}
	return raw, nil
}

// --- MirrorResolver (validate.Resolver) ---------------------------------

// MirrorResolver is the concrete validate.Resolver this package wires: it
// resolves known-artifact/digest/system-membership facts from the
// connected space's mirror clone on disk.
type MirrorResolver struct {
	mirrorDir string
	manifest  space.Manifest

	once  sync.Once
	index map[string]string // artifact id -> mirror-relative path
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

// Digest implements validate.Resolver.
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
				return nil
			}
			raw, rerr := os.ReadFile(path) //nolint:gosec // reason: path comes from walking this system's own already-cloned mirror dir, not attacker-controlled input
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
		return &ViolationError{Violations: violations}
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
