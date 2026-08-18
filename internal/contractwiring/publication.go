package contractwiring

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// Host is the narrow capability this package's own assembly needs from a
// host.Host implementation: the six host.Host methods space.NewWriteFunnel
// requires, host.AutoMerger (space.NewContractPublicationRecovery requires
// EnableAutoMerge unconditionally, not as an optional capability), and the
// four git-only remote-tree-reading methods
// space.NewContractPublicationRecovery additionally requires. It is
// declared here, by the consumer, rather than reused from a wider
// host.Host-shaped export, because contractP6Core's own single concrete
// *host.GitHubHost field serves both the funnel and the recovery role and
// this is the exact intersection both callers need.
//
// *host.GitHubHost satisfies it in production. Any type embedding
// *host.FakeHost (which already implements host.Host and host.AutoMerger)
// plus the four recovery-reader methods — as cmd/a2a's own
// contractLifecycleRecoveryHost does, forwarding those four to a real
// *host.GitHubHost against a local filesystem remote — satisfies it in
// tests.
type Host interface {
	host.Host
	host.AutoMerger
	ListContractPublishHeads(ctx context.Context, repoDir, remoteURL, prefix string, credential host.Credential) (host.RemoteHeadListing, error)
	ReadRemoteRecoveryCommit(ctx context.Context, repoDir, remoteURL string, repository host.Repo, branch string, credential host.Credential) (host.Repo, string, string, string, string, string, string, map[string]host.RemoteRecoveryChange, bool, error)
	ReadRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, paths []string) (map[string]host.RemoteTreeFile, error)
	ListRemoteRecoveryTreeFiles(ctx context.Context, repoDir, commit string, prefixes []string) (map[string]host.RemoteTreeFile, error)
}

// PublicationDependencies bundles everything NewPublicationService needs to
// assemble the production contract publication service. Every field is
// required unless its own comment says otherwise.
type PublicationDependencies struct {
	MirrorDir  string
	RemoteURL  string
	SpaceID    string
	OwnSystem  string
	Binary     string
	Repository host.Repo
	Credential host.Credential
	Host       Host
	Engine     *validate.Engine

	ManifestValidator space.ManifestValidator
	HistoryValidator  space.ContractHistoryDocumentValidator
	Compatibility     contract.CompatibilityChecker

	Actor        template.Actor
	Now          func() time.Time
	Entropy      io.Reader
	LoadManifest func() (space.Manifest, error)

	// ValidateSubmitFiles runs the space's own artifact-resolution and
	// legality rules over the candidate, against the manifest as refreshed at
	// submit time.
	//
	// INJECTED rather than built here, and the reason is ADR-001. Those
	// adapters (`MirrorResolver`, `LegalityAdapter`, `SubmitValidatorAdapter`)
	// live in `internal/cli`, which ADR-001's table calls the "OP-2xx verb
	// surface — thin frontend" and which nothing below it imports: the `mcp`
	// row says "never `cli`" in as many words. This package sits below both.
	// Calling into `cli` from here would invert that direction — and because
	// no import-boundary test guards `internal/cli` the way one guards
	// `internal/html` and `internal/localserver`, it would have inverted it
	// silently. Found by the wave's own coherence probe, not by a gate.
	ValidateSubmitFiles func(ctx context.Context, manifest space.Manifest, files []space.FileWrite) error
}

func (d PublicationDependencies) validate() error {
	if strings.TrimSpace(d.MirrorDir) == "" || strings.TrimSpace(d.RemoteURL) == "" ||
		strings.TrimSpace(d.SpaceID) == "" || strings.TrimSpace(d.OwnSystem) == "" || strings.TrimSpace(d.Binary) == "" ||
		d.Repository.Owner == "" || d.Repository.Name == "" || d.Host == nil || d.Engine == nil ||
		d.ManifestValidator == nil || d.HistoryValidator == nil || d.Compatibility == nil ||
		d.Now == nil || d.Entropy == nil || d.LoadManifest == nil || d.ValidateSubmitFiles == nil {
		return fmt.Errorf("contractwiring: complete publication dependencies are required")
	}
	return nil
}

// NewPublicationService assembles the production contract publication
// service — the repository, the publication event builder, the submit
// validator, a space.WriteFunnel and a space.ContractPublicationRecovery —
// exactly as cmd/a2a's contractP6Core.publish does, so no other caller
// needs its own copy of this wiring.
func NewPublicationService(deps PublicationDependencies) (*space.ContractPublicationService, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	repository, err := space.NewContractPublicationRepository(
		deps.MirrorDir, deps.RemoteURL, deps.Credential, deps.ManifestValidator, deps.HistoryValidator,
	)
	if err != nil {
		return nil, err
	}
	events := &contractPublicationEventBuilder{
		mirrorDir: deps.MirrorDir, spaceID: deps.SpaceID, ownSystem: deps.OwnSystem,
		loadManifest: deps.LoadManifest, actor: deps.Actor, engine: deps.Engine,
		now: deps.Now, entropy: deps.Entropy,
	}
	validator := &contractPublicationSubmitValidator{
		events: events, engine: deps.Engine, mirrorDir: deps.MirrorDir,
		ownSystem: deps.OwnSystem, loadManifest: deps.LoadManifest,
		validateSubmitFiles: deps.ValidateSubmitFiles,
	}
	funnel := space.NewWriteFunnel(deps.Host, validator, deps.Binary)
	recovery, err := space.NewContractPublicationRecovery(deps.Host, deps.MirrorDir, deps.RemoteURL, deps.Repository, deps.Credential, deps.Binary, space.ContractPublicationRecoveryValidation{
		ManifestValidator: deps.ManifestValidator,
		HistoryValidator:  deps.HistoryValidator,
		Compatibility:     deps.Compatibility,
	})
	if err != nil {
		return nil, err
	}
	return space.NewContractPublicationService(repository, repository, recovery, deps.Compatibility, events, funnel)
}

type contractPublicationEnvelope struct {
	ID                string   `yaml:"id"`
	Space             string   `yaml:"space"`
	From              string   `yaml:"from"`
	To                any      `yaml:"to"`
	RequiredApprovers []string `yaml:"required_approvers"`
}

type contractPublicationEventActor struct {
	Kind   string `yaml:"kind"`
	Name   string `yaml:"name"`
	System string `yaml:"system"`
	Model  string `yaml:"model,omitempty"`
}

type contractPublicationEventDocument struct {
	Schema        string                        `yaml:"schema"`
	Event         string                        `yaml:"event"`
	Space         string                        `yaml:"space"`
	Subject       string                        `yaml:"subject"`
	Transition    string                        `yaml:"transition"`
	State         string                        `yaml:"state,omitempty"`
	Actor         contractPublicationEventActor `yaml:"actor"`
	At            string                        `yaml:"at"`
	Version       string                        `yaml:"version"`
	Digest        string                        `yaml:"digest"`
	DigestProfile string                        `yaml:"digest_profile,omitempty"`
	Publication   *contract.PublicationIntent   `yaml:"publication,omitempty"`
}

type contractPublicationEventBuilder struct {
	mirrorDir    string
	spaceID      string
	ownSystem    string
	actor        template.Actor
	engine       *validate.Engine
	now          func() time.Time
	entropy      io.Reader
	loadManifest func() (space.Manifest, error)

	evaluation fold.CandidateEvaluation
	eventPath  string
	eventRaw   []byte
}

func (b *contractPublicationEventBuilder) BuildContractPublicationEvent(_ context.Context, plan contract.PublicationPlan) (space.FileWrite, error) {
	manifest, err := b.loadManifest()
	if err != nil {
		return space.FileWrite{}, fmt.Errorf("load refreshed publication manifest: %w", err)
	}
	frontmatter, err := artifact.ParseFrontmatter(plan.FinalDescriptorBytes())
	if err != nil {
		return space.FileWrite{}, fmt.Errorf("parse finalized descriptor: %w", err)
	}
	var probe contractPublicationEnvelope
	if err := yaml.Unmarshal(frontmatter.YAML, &probe); err != nil {
		return space.FileWrite{}, fmt.Errorf("decode finalized descriptor: %w", err)
	}
	if probe.ID != plan.Contract || probe.From != b.ownSystem || probe.Space != b.spaceID {
		return space.FileWrite{}, fmt.Errorf("finalized descriptor identity does not match publication wiring")
	}
	env := fold.Envelope{
		ID: plan.Contract, Kind: fold.KindContract, From: probe.From,
		To: contractPublicationToStrings(probe.To), RequiredApprovers: probe.RequiredApprovers,
	}
	events, err := b.committedEvents(manifest, plan.Contract)
	if err != nil {
		return space.FileWrite{}, err
	}
	prior := fold.NewResult(fold.KindContract)
	if len(events) != 0 {
		prior = fold.Fold(fold.KindContract, env, events, contractPublicationMembership(manifest))
	}
	actor := fold.Actor{Kind: b.actor.Kind, Name: b.actor.Name, System: b.ownSystem}
	evaluation := fold.EvaluateCandidate(fold.KindContract, prior, fold.Event{
		Subject: plan.Contract, Transition: fold.TPublish, Version: plan.TargetVersion, Actor: actor,
	}, env, contractPublicationMembership(manifest))
	if evaluation.Verdict != fold.VerdictLegal {
		return space.FileWrite{}, fmt.Errorf("contract publish evaluation refused: %s", evaluation.Verdict)
	}
	now := b.now().UTC()
	eventID, err := artifact.MintULIDAt(now, b.entropy)
	if err != nil {
		return space.FileWrite{}, fmt.Errorf("mint publication event: %w", err)
	}
	document := contractPublicationEventDocument{
		Schema: plan.EventSchema, Event: eventID.String(), Space: probe.Space,
		Subject: plan.Contract, Transition: fold.TPublish,
		Actor: contractPublicationEventActor{Kind: b.actor.Kind, Name: b.actor.Name, System: b.ownSystem, Model: b.actor.Model},
		At:    now.Format(time.RFC3339), Version: plan.TargetVersion, Digest: plan.AggregateDigest,
	}
	if evaluation.Applicable {
		document.State = string(evaluation.Outcome)
	}
	if plan.EventSchema == "event/v2" {
		document.DigestProfile = string(plan.DigestProfile)
		document.Publication = &contract.PublicationIntent{
			IntentKey: plan.IntentKey, CandidateIntentDigest: plan.CandidateIntentDigest,
			VersionSelector: plan.VersionSelector, OperationKey: plan.OperationKey,
		}
	}
	raw, err := yaml.Marshal(document)
	if err != nil {
		return space.FileWrite{}, fmt.Errorf("encode publication event: %w", err)
	}
	layout, err := space.NewLayout(b.ownSystem)
	if err != nil {
		return space.FileWrite{}, err
	}
	b.evaluation = evaluation
	b.eventPath = layout.EventFile(now.Format("2006"), eventID.String())
	b.eventRaw = bytes.Clone(raw)
	return space.FileWrite{Path: b.eventPath, Content: raw}, nil
}

func (b *contractPublicationEventBuilder) committedEvents(manifest space.Manifest, subject string) ([]fold.Event, error) {
	systems := make(map[string]struct{}, len(manifest.Participants)+1)
	systems[b.ownSystem] = struct{}{}
	for _, participant := range manifest.Participants {
		systems[participant.System] = struct{}{}
	}
	var events []fold.Event
	for system := range systems {
		committed, err := cache.CommittedEvents(b.mirrorDir, system, subject)
		if err != nil {
			return nil, fmt.Errorf("read committed contract events for %s: %w", system, err)
		}
		events = append(events, committed...)
	}
	return events, nil
}

func contractPublicationMembership(manifest space.Manifest) fold.MembershipView {
	return func(system string) fold.MembershipStatus {
		for _, participant := range manifest.Participants {
			if participant.System == system {
				if participant.Status == "left" {
					return fold.MembershipLeft
				}
				return fold.MembershipMember
			}
		}
		return fold.MembershipUnknown
	}
}

func contractPublicationToStrings(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return nil
	}
}

type contractPublicationSubmitValidator struct {
	events              *contractPublicationEventBuilder
	engine              *validate.Engine
	mirrorDir           string
	ownSystem           string
	loadManifest        func() (space.Manifest, error)
	validateSubmitFiles func(ctx context.Context, manifest space.Manifest, files []space.FileWrite) error
}

func (v *contractPublicationSubmitValidator) ValidateSubmitCandidate(_ context.Context, files []space.FileWrite) error {
	raw, err := v.publicationEvent(files)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, v.events.eventRaw) {
		return fmt.Errorf("publication event changed before producer stamping")
	}
	result, err := v.engine.ValidateEventWithEvaluation(raw, "0.0.0", v.events.evaluation)
	if err != nil {
		return err
	}
	return validationResultError("publication candidate event", result)
}

func (v *contractPublicationSubmitValidator) ValidateSubmit(ctx context.Context, files []space.FileWrite) error {
	raw, err := v.publicationEvent(files)
	if err != nil {
		return err
	}
	manifest, err := v.loadManifest()
	if err != nil {
		return fmt.Errorf("load refreshed publication manifest: %w", err)
	}
	result, err := v.engine.ValidateEventWithEvaluation(raw, manifest.MinBinaryVersion, v.events.evaluation)
	if err != nil {
		return err
	}
	if err := validationResultError("publication event", result); err != nil {
		return err
	}
	return v.validateSubmitFiles(ctx, manifest, files)
}

func (v *contractPublicationSubmitValidator) publicationEvent(files []space.FileWrite) ([]byte, error) {
	if v == nil || v.events == nil || v.events.eventPath == "" {
		return nil, fmt.Errorf("publication event builder has no evaluated event")
	}
	var raw []byte
	for _, file := range files {
		if file.Path == v.events.eventPath {
			if raw != nil {
				return nil, fmt.Errorf("publication event path is duplicated")
			}
			raw = file.Content
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("publication event is missing")
	}
	return raw, nil
}

// validationResultError is this package's own copy of cmd/a2a's
// contractValidationResultError — a small, self-contained formatting
// helper (violation code/path/message into one error), not assembly logic
// with a drift risk, so it stays duplicated rather than dragging
// contractManifestEngine/contractHistoryDocumentEngine (and their
// cmd/a2a-only callers) across the package boundary.
func validationResultError(label string, result validate.Result) error {
	if result.Valid {
		return nil
	}
	parts := make([]string, 0, len(result.Violations))
	for _, violation := range result.Violations {
		parts = append(parts, fmt.Sprintf("%s %s: %s", violation.Code, violation.Path, violation.Message))
	}
	return fmt.Errorf("%s validation rejected: %s", label, strings.Join(parts, "; "))
}

var (
	_ space.ContractPublicationEventBuilder = (*contractPublicationEventBuilder)(nil)
	_ space.CandidateSubmitValidator        = (*contractPublicationSubmitValidator)(nil)
)
