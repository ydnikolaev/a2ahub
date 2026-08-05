package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	semver "github.com/ydnikolaev/a2ahub/internal/version"
	"gopkg.in/yaml.v3"
)

// contractP6Core is the one production adapter shared by the CLI and MCP
// transports. It selects bounded rooted inputs and delegates planning,
// history, materialization and conformance to their owning core packages.
type contractP6Core struct {
	projectRoot       string
	mirrorDir         string
	remoteURL         string
	spaceID           string
	ownSystem         string
	repository        host.Repo
	authorName        string
	authorEmail       string
	binary            string
	engine            *validate.Engine
	host              *host.GitHubHost
	now               func() time.Time
	entropy           io.Reader
	resolveActor      func(kind, name, model string) (template.Actor, error)
	resolveCredential func(context.Context) (host.Credential, error)
	loadManifest      func() (space.Manifest, error)
}

func newContractP6Core(
	projectRoot, mirrorDir, remoteURL, spaceID, ownSystem string,
	repository host.Repo,
	authorName, authorEmail, binary string,
	engine *validate.Engine,
	h *host.GitHubHost,
	resolveActor func(kind, name, model string) (template.Actor, error),
	resolveCredential func(context.Context) (host.Credential, error),
) (*contractP6Core, error) {
	if projectRoot == "" || mirrorDir == "" || remoteURL == "" || spaceID == "" || ownSystem == "" ||
		repository.Owner == "" || repository.Name == "" || authorName == "" || authorEmail == "" || binary == "" ||
		engine == nil || h == nil || resolveActor == nil || resolveCredential == nil {
		return nil, fmt.Errorf("contract P6 wiring: complete production dependencies are required")
	}
	return &contractP6Core{
		projectRoot: projectRoot, mirrorDir: mirrorDir, remoteURL: remoteURL,
		spaceID: spaceID, ownSystem: ownSystem, repository: repository,
		authorName: authorName, authorEmail: authorEmail, binary: binary,
		engine: engine, host: h, now: time.Now, entropy: rand.Reader,
		resolveActor: resolveActor, resolveCredential: resolveCredential,
		loadManifest: func() (space.Manifest, error) { return loadManifest(mirrorDir) },
	}, nil
}

func newCLIContractP6Core(p paths, deps lifecycleDeps) (*contractP6Core, error) {
	engine, err := newEngine()
	if err != nil {
		return nil, err
	}
	return newContractP6Core(
		p.projectRoot, deps.mirrorDir, deps.hostCfg.RemoteURL, deps.spaceID, deps.ownSystem,
		deps.hostCfg.Repo,
		deps.hostCfg.CommitAuthorName, deps.hostCfg.CommitAuthorEmail, funnelBinaryVersion(),
		engine, host.NewGitHubHost(http.DefaultClient, githubAPIBase()),
		func(kind, name, model string) (template.Actor, error) {
			return deps.resolveActor(cli.ActorFlags{Kind: kind, Name: name, Model: model})
		},
		func(context.Context) (host.Credential, error) { return deps.hostCfg.Credential, nil },
	)
}

func newMCPContractOperationsFactory(p paths, cfg space.ProjectConfig, machine space.MachineConfig) mcp.ContractOperationsFactory {
	return func(resolveMCPActor mcp.ActorResolver) (mcp.ContractToolOperations, error) {
		engine, err := newEngine()
		if err != nil {
			return mcp.ContractToolOperations{}, err
		}
		cores := make(map[string]*contractP6Core, len(cfg.Spaces))
		github := host.NewGitHubHost(http.DefaultClient, githubAPIBase())
		for _, ref := range cfg.Spaces {
			owner, name, parseErr := parseGitHubRepo(ref.RepoURL)
			if parseErr != nil {
				return mcp.ContractToolOperations{}, parseErr
			}
			core, coreErr := newContractP6Core(
				p.projectRoot, space.ResolveMirrorLocation(p.projectRoot, ref, machine), ref.RepoURL, ref.ID, cfg.System,
				host.Repo{Owner: owner, Name: name}, cfg.System, cfg.System+"@a2a.local", funnelBinaryVersion(),
				engine, github,
				func(kind, name, model string) (template.Actor, error) {
					return resolveMCPActor(mcp.ActorInput{Kind: kind, Name: name, Model: model})
				},
				func(ctx context.Context) (host.Credential, error) { return resolveCredential(ctx, ref.ID, machine) },
			)
			if coreErr != nil {
				return mcp.ContractToolOperations{}, coreErr
			}
			cores[ref.ID] = core
		}
		return contractMCPToolOperations(mcpContractP6Router{bySpace: cores}), nil
	}
}

type contractP6PublicationInput struct {
	id         string
	version    string
	bump       string
	staging    string
	expectPlan string
	actorKind  string
	actorName  string
	actorModel string
}

func (c *contractP6Core) preflight(ctx context.Context, input contractP6PublicationInput) (space.ContractPublicationResult, error) {
	request, err := c.publicationRequest(ctx, input, host.Credential{}, false)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	// The REQUEST stays credential-free — preflight performs no write and must
	// not imply one. The REPOSITORY still needs a credential, because its
	// refresh fetches origin/main, and against a private space an unauthenticated
	// fetch cannot even see the branch it is asked to pin.
	repository, err := c.publicationRepository(c.readCredential(ctx))
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	service, err := space.NewContractPreflightService(repository, validate.ContractCompatibilityAdapter{})
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return service.Preflight(ctx, request)
}

func (c *contractP6Core) publish(ctx context.Context, input contractP6PublicationInput) (space.ContractPublicationResult, error) {
	actor, err := c.resolveActor(input.actorKind, input.actorName, input.actorModel)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	request, err := c.publicationRequest(ctx, input, credential, true)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	repository, err := c.publicationRepository(credential)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	events := &contractPublicationEventBuilder{
		mirrorDir: c.mirrorDir, spaceID: c.spaceID, ownSystem: c.ownSystem,
		loadManifest: c.loadManifest, actor: actor, engine: c.engine, now: c.now, entropy: c.entropy,
	}
	validator := &contractPublicationSubmitValidator{
		events: events, engine: c.engine, mirrorDir: c.mirrorDir,
		ownSystem: c.ownSystem, loadManifest: c.loadManifest,
	}
	funnel := space.NewWriteFunnel(c.host, validator, c.binary)
	recovery, err := space.NewContractPublicationRecovery(c.host, c.mirrorDir, c.remoteURL, c.repository, credential, space.ContractPublicationRecoveryValidation{
		ManifestValidator: contractManifestEngine{engine: c.engine},
		HistoryValidator:  contractHistoryDocumentEngine{engine: c.engine},
		Compatibility:     validate.ContractCompatibilityAdapter{},
	})
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	service, err := space.NewContractPublicationService(repository, repository, recovery, validate.ContractCompatibilityAdapter{}, events, funnel)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return service.Publish(ctx, request)
}

func (c *contractP6Core) publicationRepository(credential host.Credential) (*space.ContractPublicationRepository, error) {
	return space.NewContractPublicationRepository(
		c.mirrorDir, c.remoteURL, credential,
		contractManifestEngine{engine: c.engine}, contractHistoryDocumentEngine{engine: c.engine},
	)
}

// readCredential resolves this space's credential for a REFRESH, degrading to
// the empty credential when none resolves — the same lenient sibling of
// resolveCredential that wire.go carries, and for the same reason. Preflight
// in particular must keep working against a public space with no credential
// configured, while still being able to reach a private one.
func (c *contractP6Core) readCredential(ctx context.Context) host.Credential {
	credential, err := c.resolveCredential(ctx)
	if err != nil {
		return host.Credential{}
	}
	return credential
}

func (c *contractP6Core) publicationRequest(ctx context.Context, input contractP6PublicationInput, credential host.Credential, publishing bool) (space.ContractPublicationRequest, error) {
	selector, err := contractPublicationSelector(input.version, input.bump)
	if err != nil {
		return space.ContractPublicationRequest{}, err
	}
	candidate, source, err := c.freezePublicationCandidate(ctx, input.id, input.staging)
	if err != nil {
		return space.ContractPublicationRequest{}, err
	}
	observedFloor := ""
	if publishing {
		manifest, manifestErr := c.loadManifest()
		if manifestErr != nil {
			return space.ContractPublicationRequest{}, fmt.Errorf("load publication write floor: %w", manifestErr)
		}
		observedFloor = manifest.MinBinaryVersion
	}
	request := space.ContractPublicationRequest{
		System: c.ownSystem, ContractID: input.id, Selector: selector,
		Candidate: candidate, CandidateSource: source, ExpectPlan: input.expectPlan,
		ProducerCompatibility: c.binary,
	}
	if publishing {
		request.SubmitTemplate = space.SubmitRequest{
			RepoDir: c.mirrorDir, RemoteURL: c.remoteURL, Repo: c.repository, MinBinaryVersion: observedFloor,
			CommitMessage:    "a2a(contract-publish): " + input.id,
			CommitAuthorName: c.authorName, CommitAuthorEmail: c.authorEmail,
			PRTitle: "Publish " + input.id, PRBody: "Publish an exact immutable contract version.",
			Credential: credential,
		}
		request.SubmissionRuntime = space.SubmissionRuntime{
			RepoDir: c.mirrorDir, RemoteURL: c.remoteURL, Credential: credential,
		}
	}
	return request, nil
}

func contractPublicationSelector(explicit, bump string) (string, error) {
	if (explicit == "") == (bump == "") {
		return "", fmt.Errorf("contract publication requires exactly one of version or bump")
	}
	if explicit != "" {
		canonical, err := semver.Canonical(explicit)
		if err != nil || canonical != explicit {
			return "", fmt.Errorf("contract publication version %q is not canonical", explicit)
		}
		return "explicit:" + canonical, nil
	}
	if bump != "patch" && bump != "minor" && bump != "major" {
		return "", fmt.Errorf("contract publication bump %q is invalid", bump)
	}
	return "auto:" + bump, nil
}

func (c *contractP6Core) freezePublicationCandidate(ctx context.Context, contractID, staging string) (space.ContractPublicationCandidateReader, contract.CandidateSource, error) {
	parsed, err := artifact.ParseID(contractID)
	if err != nil || parsed.Prefix != "XC" || parsed.System != c.ownSystem {
		return nil, contract.CandidateSource{}, fmt.Errorf("contract publication id %q is not owned by %s", contractID, c.ownSystem)
	}
	layout, err := space.NewLayout(c.ownSystem)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	contractRoot := path.Dir(layout.ProvidesContract(parsed.Slug))
	commit, err := space.ResolveContractPublicationCandidateCommit(ctx, c.mirrorDir)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	mirrorReader, err := space.OpenContractCandidateReader(c.mirrorDir, space.ContractCandidateLocation{Path: contractRoot, Source: space.ContractCandidateSourceMirror})
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	mirrorFrozen, freezeErr := space.NewFrozenContractPublicationCandidate(ctx, mirrorReader, space.ContractPublicationMirrorTree{
		RepoDir: c.mirrorDir, Commit: commit, Path: contractRoot,
	})
	closeErr := mirrorReader.Close()
	if freezeErr != nil {
		return nil, contract.CandidateSource{}, freezeErr
	}
	if closeErr != nil {
		return nil, contract.CandidateSource{}, closeErr
	}
	if staging == "" {
		return mirrorFrozen, mirrorFrozen.ContractPublicationCandidateSource(), nil
	}

	stagingReader, err := space.OpenContractCandidateReader(c.projectRoot, space.ContractCandidateLocation{Path: staging, Source: space.ContractCandidateSourceStaging})
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	stagingFrozen, freezeErr := space.NewFrozenContractPublicationCandidate(ctx, stagingReader, space.ContractPublicationMirrorTree{})
	closeErr = stagingReader.Close()
	if freezeErr != nil {
		return nil, contract.CandidateSource{}, freezeErr
	}
	if closeErr != nil {
		return nil, contract.CandidateSource{}, closeErr
	}
	overlay, err := space.NewContractPublicationStagingOverlayReader(mirrorFrozen, stagingFrozen, staging)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	snapshot, err := overlay.ReadContractPublicationCandidate(ctx)
	if err != nil {
		return nil, contract.CandidateSource{}, err
	}
	frozen := &contractFixedPublicationCandidate{
		snapshot: cloneContractCandidateSnapshot(snapshot),
		source:   contract.CandidateSource{Kind: contract.CandidateSourceStaging, Location: staging, Fingerprint: snapshot.Fingerprint},
	}
	return frozen, frozen.source, nil
}

type contractFixedPublicationCandidate struct {
	snapshot space.ContractCandidateSnapshot
	source   contract.CandidateSource
}

func (c *contractFixedPublicationCandidate) ReadContractPublicationCandidate(context.Context) (space.ContractCandidateSnapshot, error) {
	if c == nil || c.source.Fingerprint == "" {
		return space.ContractCandidateSnapshot{}, space.ErrContractPublicationInvalid
	}
	return cloneContractCandidateSnapshot(c.snapshot), nil
}

func (c *contractFixedPublicationCandidate) ContractPublicationCandidateSource() contract.CandidateSource {
	if c == nil {
		return contract.CandidateSource{}
	}
	return c.source
}

func cloneContractCandidateSnapshot(snapshot space.ContractCandidateSnapshot) space.ContractCandidateSnapshot {
	clone := snapshot
	clone.Files = make([]space.ContractSnapshotFile, len(snapshot.Files))
	for index := range snapshot.Files {
		clone.Files[index] = snapshot.Files[index]
		clone.Files[index].Raw = bytes.Clone(snapshot.Files[index].Raw)
	}
	return clone
}

type contractManifestEngine struct{ engine *validate.Engine }

func (v contractManifestEngine) ValidateManifest(_ context.Context, raw []byte) error {
	result, err := v.engine.ValidateManifest(raw)
	if err != nil {
		return err
	}
	return contractValidationResultError("manifest", result)
}

// contractHistoryDocumentEngine is the production historical-document seam.
// It validates the exact descriptor path and exact v1/v2 event bytes through
// the same validate.Engine used by submit and V3. A low historical floor keeps
// producer-stamp rollout policy from being applied retroactively.
type contractHistoryDocumentEngine struct{ engine *validate.Engine }

func (v contractHistoryDocumentEngine) ValidateHistoricalContractDocuments(_ context.Context, documents space.ContractHistoryDocuments) error {
	descriptor, err := v.engine.ValidateDraft(validate.Draft{Path: documents.Descriptor.Path, Raw: documents.Descriptor.Raw})
	if err != nil {
		return err
	}
	if err := contractValidationResultError("historical descriptor", descriptor); err != nil {
		return err
	}
	event, err := v.engine.ValidateEvent(documents.PublishEvent.Raw, "0.0.0")
	if err != nil {
		return err
	}
	return contractValidationResultError("historical publish event", event)
}

func contractValidationResultError(label string, result validate.Result) error {
	if result.Valid {
		return nil
	}
	parts := make([]string, 0, len(result.Violations))
	for _, violation := range result.Violations {
		parts = append(parts, fmt.Sprintf("%s %s: %s", violation.Code, violation.Path, violation.Message))
	}
	return fmt.Errorf("%s validation rejected: %s", label, strings.Join(parts, "; "))
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
	events       *contractPublicationEventBuilder
	engine       *validate.Engine
	mirrorDir    string
	ownSystem    string
	loadManifest func() (space.Manifest, error)
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
	return contractValidationResultError("publication candidate event", result)
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
	if err := contractValidationResultError("publication event", result); err != nil {
		return err
	}
	resolver := cli.NewMirrorResolver(v.mirrorDir, manifest)
	legality := cli.NewLegalityAdapter(v.mirrorDir, v.ownSystem, manifest)
	return cli.NewSubmitValidatorAdapter(v.engine, v.ownSystem, resolver, legality).ValidateSubmit(ctx, files)
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

func (c *contractP6Core) resolveHistorical(ctx context.Context, ref string) (space.HistoricalSnapshot, error) {
	id, requestedVersion, err := splitExactContractReference(ref)
	if err != nil {
		return space.HistoricalSnapshot{}, err
	}
	return space.ResolveContractVersion(ctx, c.mirrorDir, id, requestedVersion, contractHistoryDocumentEngine{engine: c.engine})
}

func splitExactContractReference(ref string) (string, string, error) {
	id, requestedVersion, found := strings.Cut(ref, "@")
	parsed, idErr := artifact.ParseID(id)
	canonical, versionErr := semver.Canonical(requestedVersion)
	if !found || strings.Contains(requestedVersion, "@") || idErr != nil || parsed.Prefix != "XC" ||
		versionErr != nil || canonical != requestedVersion {
		return "", "", fmt.Errorf("contract reference %q must be an exact XC-id@canonical-version", ref)
	}
	return id, canonical, nil
}

func (c *contractP6Core) materialize(ctx context.Context, ref, destination string) (space.ContractMaterializeResult, error) {
	snapshot, err := c.resolveHistorical(ctx, ref)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	materializer, err := space.NewContractMaterializer(c.projectRoot)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	return materializeContractAndClose(ctx, materializer, snapshot, destination)
}

type contractMaterializeCapability interface {
	Materialize(context.Context, space.HistoricalSnapshot, string) (space.ContractMaterializeResult, error)
	Close() error
}

func materializeContractAndClose(
	ctx context.Context,
	materializer contractMaterializeCapability,
	snapshot space.HistoricalSnapshot,
	destination string,
) (result space.ContractMaterializeResult, resultErr error) {
	defer func() {
		if closeErr := materializer.Close(); closeErr != nil {
			wrapped := fmt.Errorf("close contract materializer: %w", closeErr)
			if resultErr == nil {
				resultErr = wrapped
				return
			}
			resultErr = errors.Join(resultErr, wrapped)
		}
	}()
	return materializer.Materialize(ctx, snapshot, destination)
}

func (c *contractP6Core) check(ctx context.Context, ref, payloadPath, schemaPath string, suite bool) (contract.ConformanceResult, error) {
	snapshot, err := c.resolveHistorical(ctx, ref)
	if err != nil {
		return contract.ConformanceResult{}, err
	}
	mode := contract.ConformanceModePayload
	var payload []byte
	if suite {
		if payloadPath != "" || schemaPath != "" {
			return contract.ConformanceResult{}, fmt.Errorf("suite mode does not accept payload or schema paths")
		}
		mode = contract.ConformanceModeSuite
	} else {
		if payloadPath == "" {
			return contract.ConformanceResult{}, fmt.Errorf("payload mode requires a project-relative payload path")
		}
		payload, err = readBoundedProjectFile(c.projectRoot, payloadPath, contract.MaxFileBytes)
		if err != nil {
			return contract.ConformanceResult{}, err
		}
	}
	result := contract.CheckConformance(contract.ConformanceInput{
		ContractID: snapshot.ContractID, Version: snapshot.Version, Commit: snapshot.CommitSHA,
		SchemaFormat: snapshot.Descriptor.SchemaFormat, PublishedDigest: snapshot.PublishedDigest,
		Set: snapshot.CarriedSet, Mode: mode, SchemaPath: schemaPath,
		PayloadPath: payloadPath, Payload: payload,
	}, validate.ContractInstanceAdapter{})
	if violations := validate.ValidateContractConformance(result); len(violations) != 0 {
		result.Passed = false
		if result.Message == "" {
			result.Message = violations[0].Code + ": " + violations[0].Message
		}
	}
	return result, nil
}

func readBoundedProjectFile(projectRoot, name string, maximum int) ([]byte, error) {
	if name == "" || name == "." || path.IsAbs(name) || filepath.VolumeName(name) != "" ||
		path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return nil, fmt.Errorf("project payload path %q is not safely contained", name)
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return nil, fmt.Errorf("project payload path %q is not safely contained", name)
		}
	}
	rootBefore, err := os.Lstat(projectRoot)
	if err != nil || rootBefore.Mode()&os.ModeSymlink != 0 || !rootBefore.IsDir() {
		return nil, fmt.Errorf("project root is not a real directory")
	}
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("open project root: %w", err)
	}
	defer func() { _ = root.Close() }() // reason: preserve the payload result
	leafBefore, err := root.Lstat(name)
	if err != nil || leafBefore.Mode()&os.ModeSymlink != 0 || !leafBefore.Mode().IsRegular() {
		return nil, fmt.Errorf("project payload %q is not a regular contained file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open project payload %q: %w", name, err)
	}
	defer func() { _ = file.Close() }() // reason: preserve the bounded read result
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(leafBefore, opened) {
		return nil, fmt.Errorf("project payload %q changed while opening", name)
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, fmt.Errorf("read project payload %q: %w", name, err)
	}
	leafAfter, err := root.Lstat(name)
	if err != nil || leafAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, leafAfter) {
		return nil, fmt.Errorf("project payload %q changed while reading", name)
	}
	if len(raw) > maximum {
		return nil, fmt.Errorf("project payload %q exceeds %d bytes", name, maximum)
	}
	return raw, nil
}

type contractInspectionResult struct {
	added              []string
	removed            []string
	changed            []string
	frontmatterChanged []string
}

type contractVerifyResult struct {
	id          string
	matches     bool
	localDigest string
	wantDigest  string
	diff        contractInspectionResult
}

func (c *contractP6Core) diff(ctx context.Context, id, v1, v2 string) (contractInspectionResult, error) {
	left, err := c.resolveHistorical(ctx, id+"@"+v1)
	if err != nil {
		return contractInspectionResult{}, err
	}
	right, err := c.resolveHistorical(ctx, id+"@"+v2)
	if err != nil {
		return contractInspectionResult{}, err
	}
	return inspectContractSnapshots(left.Files, right.Files), nil
}

func (c *contractP6Core) verifyExport(ctx context.Context, local, ref string) (contractVerifyResult, error) {
	historical, err := c.resolveHistorical(ctx, ref)
	if err != nil {
		return contractVerifyResult{}, err
	}
	localSnapshot, err := space.ReadContractCandidate(ctx, c.projectRoot, space.ContractCandidateLocation{Path: local, Source: space.ContractCandidateSourceStaging})
	if err != nil {
		return contractVerifyResult{}, err
	}
	localDigest := contractLocalSnapshotDigest(localSnapshot, historical.DigestProfile)
	diff := inspectContractSnapshots(localSnapshot.Files, historical.Files)
	matches := localDigest == historical.PublishedDigest && contractSnapshotFilesEqual(localSnapshot.Files, historical.Files)
	return contractVerifyResult{
		id: historical.ContractID, matches: matches, localDigest: localDigest,
		wantDigest: historical.PublishedDigest, diff: diff,
	}, nil
}

func contractLocalSnapshotDigest(snapshot space.ContractCandidateSnapshot, profile contract.DigestProfile) string {
	core := snapshot.CoreSnapshot()
	descriptor, err := contract.ParseDescriptor(core.Descriptor.Raw)
	if err != nil {
		return "invalid"
	}
	files := core.Files
	if profile == contract.ProfileContractTreeV1 {
		filtered := files[:0:0]
		for _, file := range files {
			if strings.HasPrefix(file.Path, "schema/") || strings.HasPrefix(file.Path, "fixtures/") {
				filtered = append(filtered, file)
			}
		}
		set, issues := contract.BuildCarriedSet(profile, nil, contract.Descriptor{}, filtered)
		if len(issues) != 0 {
			return "invalid"
		}
		return set.AggregateDigest
	}
	set, issues := contract.BuildCarriedSet(profile, core.Descriptor.Raw, descriptor, files)
	if len(issues) != 0 {
		return "invalid"
	}
	return set.AggregateDigest
}

func inspectContractSnapshots(left, right []space.ContractSnapshotFile) contractInspectionResult {
	leftByPath, rightByPath := contractSnapshotMap(left), contractSnapshotMap(right)
	result := contractInspectionResult{added: []string{}, removed: []string{}, changed: []string{}, frontmatterChanged: []string{}}
	for name, leftFile := range leftByPath {
		if name == contract.DescriptorPath {
			continue
		}
		rightFile, ok := rightByPath[name]
		if !ok {
			result.removed = append(result.removed, name)
			continue
		}
		if leftFile.Mode != rightFile.Mode || !bytes.Equal(leftFile.Raw, rightFile.Raw) {
			result.changed = append(result.changed, name)
		}
	}
	for name := range rightByPath {
		if name == contract.DescriptorPath {
			continue
		}
		if _, ok := leftByPath[name]; !ok {
			result.added = append(result.added, name)
		}
	}
	result.frontmatterChanged = contractFrontmatterChanges(leftByPath[contract.DescriptorPath].Raw, rightByPath[contract.DescriptorPath].Raw)
	sort.Strings(result.added)
	sort.Strings(result.removed)
	sort.Strings(result.changed)
	return result
}

func contractSnapshotMap(files []space.ContractSnapshotFile) map[string]space.ContractSnapshotFile {
	out := make(map[string]space.ContractSnapshotFile, len(files))
	for _, file := range files {
		out[file.Path] = file
	}
	return out
}

func contractSnapshotFilesEqual(left, right []space.ContractSnapshotFile) bool {
	if len(left) != len(right) {
		return false
	}
	leftByPath, rightByPath := contractSnapshotMap(left), contractSnapshotMap(right)
	for name, leftFile := range leftByPath {
		rightFile, ok := rightByPath[name]
		if !ok || leftFile.Mode != rightFile.Mode || !bytes.Equal(leftFile.Raw, rightFile.Raw) {
			return false
		}
	}
	return true
}

func contractFrontmatterChanges(leftRaw, rightRaw []byte) []string {
	left, leftOK := decodeContractFrontmatter(leftRaw)
	right, rightOK := decodeContractFrontmatter(rightRaw)
	if !leftOK || !rightOK {
		return []string{"contract.md: frontmatter could not be compared"}
	}
	keys := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		keys[key] = struct{}{}
	}
	for key := range right {
		keys[key] = struct{}{}
	}
	out := make([]string, 0)
	for key := range keys {
		if reflect.DeepEqual(left[key], right[key]) {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %s -> %s", key, contractFrontmatterValue(left, key), contractFrontmatterValue(right, key)))
	}
	sort.Strings(out)
	return out
}

func decodeContractFrontmatter(raw []byte) (map[string]any, bool) {
	frontmatter, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, false
	}
	var value map[string]any
	if yaml.Unmarshal(frontmatter.YAML, &value) != nil {
		return nil, false
	}
	return value, true
}

func contractFrontmatterValue(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok {
		return "<absent>"
	}
	raw, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSpace(string(raw))
}

type cliContractP6Adapter struct{ core *contractP6Core }

func (a cliContractP6Adapter) Preflight(ctx context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.preflight(ctx, contractP6PublicationInput{id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging})
}

func (a cliContractP6Adapter) Publish(ctx context.Context, request cli.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.publish(ctx, contractP6PublicationInput{
		id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging, expectPlan: request.ExpectPlan,
		actorKind: request.Actor.Kind, actorName: request.Actor.Name, actorModel: request.Actor.Model,
	})
}

func (a cliContractP6Adapter) MaterializeContract(ctx context.Context, request cli.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return a.core.materialize(ctx, request.Ref, request.Destination)
}

func (a cliContractP6Adapter) CheckContract(ctx context.Context, request cli.ContractCheckRequest) (contract.ConformanceResult, error) {
	return a.core.check(ctx, request.Ref, request.PayloadPath, request.SchemaPath, request.Suite)
}

func (a cliContractP6Adapter) DiffContract(ctx context.Context, request cli.ContractDiffRequest) (cli.ContractDiffResult, error) {
	result, err := a.core.diff(ctx, request.ID, request.V1, request.V2)
	return cli.ContractDiffResult{Added: result.added, Removed: result.removed, Changed: result.changed, FrontmatterChanged: result.frontmatterChanged}, err
}

func (a cliContractP6Adapter) VerifyContractExport(ctx context.Context, request cli.ContractVerifyExportRequest) (cli.ContractVerifyExportResult, error) {
	result, err := a.core.verifyExport(ctx, request.Local, request.Ref)
	return cli.ContractVerifyExportResult{
		ID: result.id, Matches: result.matches, LocalDigest: result.localDigest, WantDigest: result.wantDigest,
		Diff: cli.ContractDiffResult{Added: result.diff.added, Removed: result.diff.removed, Changed: result.diff.changed, FrontmatterChanged: result.diff.frontmatterChanged},
	}, err
}

type mcpContractP6Adapter struct{ core *contractP6Core }

func (a mcpContractP6Adapter) Preflight(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.preflight(ctx, contractP6PublicationInput{id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging})
}

func (a mcpContractP6Adapter) Publish(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	return a.core.publish(ctx, contractP6PublicationInput{
		id: request.ID, version: request.Version, bump: request.Bump, staging: request.Staging, expectPlan: request.ExpectPlan,
		actorKind: request.Actor.Kind, actorName: request.Actor.Name, actorModel: request.Actor.Model,
	})
}

func (a mcpContractP6Adapter) MaterializeContract(ctx context.Context, request mcp.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	return a.core.materialize(ctx, request.Ref, request.Destination)
}

func (a mcpContractP6Adapter) CheckContract(ctx context.Context, request mcp.ContractCheckRequest) (contract.ConformanceResult, error) {
	return a.core.check(ctx, request.Ref, request.PayloadPath, request.SchemaPath, request.Suite)
}

func (a mcpContractP6Adapter) DiffContract(ctx context.Context, request mcp.ContractDiffRequest) (mcp.ContractDiffResult, error) {
	result, err := a.core.diff(ctx, request.ID, request.V1, request.V2)
	return mcp.ContractDiffResult{Added: result.added, Removed: result.removed, Changed: result.changed, FrontmatterChanged: result.frontmatterChanged}, err
}

func (a mcpContractP6Adapter) VerifyContractExport(ctx context.Context, request mcp.ContractVerifyExportRequest) (mcp.ContractVerifyExportResult, error) {
	result, err := a.core.verifyExport(ctx, request.Local, request.Ref)
	return mcp.ContractVerifyExportResult{
		ID: result.id, Matches: result.matches, LocalDigest: result.localDigest, WantDigest: result.wantDigest,
		Diff: mcp.ContractDiffResult{Added: result.diff.added, Removed: result.diff.removed, Changed: result.diff.changed, FrontmatterChanged: result.diff.frontmatterChanged},
	}, err
}

type mcpContractP6Router struct {
	bySpace map[string]*contractP6Core
}

func (r mcpContractP6Router) coreFor(spaceID string) (*contractP6Core, error) {
	if spaceID != "" {
		core, ok := r.bySpace[spaceID]
		if !ok {
			return nil, fmt.Errorf("a2a_contract: space %q is not connected", spaceID)
		}
		return core, nil
	}
	if len(r.bySpace) == 0 {
		return nil, fmt.Errorf("a2a_contract: no connected space")
	}
	if len(r.bySpace) > 1 {
		return nil, fmt.Errorf("a2a_contract: space is required when multiple spaces are connected")
	}
	for _, core := range r.bySpace {
		return core, nil
	}
	return nil, fmt.Errorf("a2a_contract: no connected space")
}

func (r mcpContractP6Router) adapter(spaceID string) (mcpContractP6Adapter, error) {
	core, err := r.coreFor(spaceID)
	return mcpContractP6Adapter{core: core}, err
}

func (r mcpContractP6Router) Preflight(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return adapter.Preflight(ctx, request)
}

func (r mcpContractP6Router) Publish(ctx context.Context, request mcp.ContractPublicationRequest) (space.ContractPublicationResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return space.ContractPublicationResult{}, err
	}
	return adapter.Publish(ctx, request)
}

func (r mcpContractP6Router) MaterializeContract(ctx context.Context, request mcp.ContractMaterializeRequest) (space.ContractMaterializeResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return space.ContractMaterializeResult{}, err
	}
	return adapter.MaterializeContract(ctx, request)
}

func (r mcpContractP6Router) CheckContract(ctx context.Context, request mcp.ContractCheckRequest) (contract.ConformanceResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return contract.ConformanceResult{}, err
	}
	return adapter.CheckContract(ctx, request)
}

func (r mcpContractP6Router) DiffContract(ctx context.Context, request mcp.ContractDiffRequest) (mcp.ContractDiffResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return mcp.ContractDiffResult{}, err
	}
	return adapter.DiffContract(ctx, request)
}

func (r mcpContractP6Router) VerifyContractExport(ctx context.Context, request mcp.ContractVerifyExportRequest) (mcp.ContractVerifyExportResult, error) {
	adapter, err := r.adapter(request.Space)
	if err != nil {
		return mcp.ContractVerifyExportResult{}, err
	}
	return adapter.VerifyContractExport(ctx, request)
}

type mcpContractP6Operations interface {
	mcp.ContractPublicationOperations
	mcp.ContractMaterializeOperation
	mcp.ContractCheckOperation
	mcp.ContractInspectionOperations
}

func contractMCPToolOperations(adapter mcpContractP6Operations) mcp.ContractToolOperations {
	return mcp.ContractToolOperations{Publication: adapter, Materialize: adapter, Check: adapter, Inspection: adapter}
}

var (
	_ space.ContractHistoryDocumentValidator = contractHistoryDocumentEngine{}
	_ space.ContractPublicationEventBuilder  = (*contractPublicationEventBuilder)(nil)
	_ space.CandidateSubmitValidator         = (*contractPublicationSubmitValidator)(nil)
	_ cli.ContractPublicationOperations      = cliContractP6Adapter{}
	_ cli.ContractMaterializeOperation       = cliContractP6Adapter{}
	_ cli.ContractCheckOperation             = cliContractP6Adapter{}
	_ cli.ContractInspectionOperations       = cliContractP6Adapter{}
	_ mcp.ContractPublicationOperations      = mcpContractP6Adapter{}
	_ mcp.ContractMaterializeOperation       = mcpContractP6Adapter{}
	_ mcp.ContractCheckOperation             = mcpContractP6Adapter{}
	_ mcp.ContractInspectionOperations       = mcpContractP6Adapter{}
	_ mcpContractP6Operations                = mcpContractP6Router{}
)
