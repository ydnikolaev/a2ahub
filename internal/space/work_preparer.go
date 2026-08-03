package space

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"gopkg.in/yaml.v3"
)

var (
	ErrWorkPreparationScopeMismatch = errors.New("space: work preparation scope does not match resolved runtime")
	ErrWorkPreparationBaseInvalid   = errors.New("space: work preparation base commit is not a canonical Git object ID")
	ErrWorkPreparationTargetInvalid = errors.New("space: work preparation remote target cannot be bound")
	ErrWorkEvaluationRefused        = errors.New("space: work announcement publish evaluation refused")
	ErrWorkPreparedShapeInvalid     = errors.New("space: prepared work submission changed its bounded two-file shape")
	ErrWorkOperationKeyLeak         = errors.New("space: work operation key entered durable protocol bytes")
)

// WorkPreparationRuntime contains the non-secret, authoritative facts needed
// to freeze a P4 submission. It deliberately has no project root, mirror path,
// credential, provider response or raw provider session.
type WorkPreparationRuntime struct {
	ProjectID            string
	Space                string
	MinimumBinaryVersion string
	Repository           host.Repo
	RemoteURL            string
	BaseBranch           string
	BaseCommitSHA        string
	ReporterMembership   fold.MembershipStatus
	CommitAuthorName     string
	CommitAuthorEmail    string
}

// WorkPreparationRuntimeProvider resolves one current, authoritative runtime
// snapshot. The preparer binds its project/space exactly before consuming ID
// entropy or presenting bytes to validation.
type WorkPreparationRuntimeProvider interface {
	ResolveWorkPreparation(context.Context) (WorkPreparationRuntime, error)
}

// WorkCheckpointValidation is the narrow V2 contextual-validation boundary.
// The injected adapter owns repository/manifest lookup and translation to the
// canonical validation engine; space supplies exact candidate documents and
// does not duplicate contextual rules.
type WorkCheckpointValidation struct {
	InvocationPoint string
	ProjectID       string
	Space           string
	ArtifactID      string
	EventID         string
	ArtifactPath    string
	EventPath       string
	Artifact        []byte
	Event           []byte
}

type WorkCheckpointValidator interface {
	ValidateWorkCheckpoint(context.Context, WorkCheckpointValidation) error
}

// WorkAnnouncementRenderInput is the exact template-layer input assembled by
// WorkPreparer. The cmd/a2a adapter routes it to internal/template.Render;
// keeping that one-method seam here avoids a space -> template -> space import
// cycle without giving rendering semantics a second production implementation.
type WorkAnnouncementRenderInput struct {
	Type           string
	EnvelopeSchema string
	ID             string
	Actor          WorkAnnouncementActor
	Created        time.Time
	Fields         map[string]string
	Body           []byte
}

type WorkAnnouncementActor struct {
	Kind  string
	Name  string
	Model string
}

type WorkAnnouncementRenderer interface {
	RenderWorkAnnouncement(WorkAnnouncementRenderInput) ([]byte, error)
}

// WorkSubmissionPreparer is the one-method P4 side-effect-free funnel seam.
// *WriteFunnel satisfies it.
type WorkSubmissionPreparer interface {
	PrepareSubmission(context.Context, SubmitRequest, PreparationContext) (PreparedSubmission, error)
}

// WorkPreparer is the production workreport.Preparer. It renders and evaluates
// one immutable status announcement plus its publish event, delegates all
// contextual policy to the injected canonical validator, and freezes the exact
// final producer-stamped bytes through P4. It never submits prepared work.
type WorkPreparer struct {
	mu        sync.Mutex
	entropy   io.Reader
	runtime   WorkPreparationRuntimeProvider
	renderer  WorkAnnouncementRenderer
	validator WorkCheckpointValidator
	funnel    WorkSubmissionPreparer
}

func NewWorkPreparer(
	entropy io.Reader,
	runtime WorkPreparationRuntimeProvider,
	renderer WorkAnnouncementRenderer,
	validator WorkCheckpointValidator,
	funnel WorkSubmissionPreparer,
) (*WorkPreparer, error) {
	if entropy == nil || runtime == nil || renderer == nil || validator == nil || funnel == nil {
		return nil, fmt.Errorf("space: work preparer entropy, runtime, renderer, validator and funnel are required")
	}
	return &WorkPreparer{entropy: entropy, runtime: runtime, renderer: renderer, validator: validator, funnel: funnel}, nil
}

// Prepare implements workreport.Preparer. Every value returned here is final:
// callers persist its P4 journal and resume with SubmitPrepared instead of
// re-rendering or re-evaluating.
func (p *WorkPreparer) Prepare(ctx context.Context, request workreport.PreparationRequest) (workreport.PreparedOperation, error) {
	runtime, err := p.runtime.ResolveWorkPreparation(ctx)
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: resolve work preparation runtime: %w", err)
	}
	if request.Identity.ProjectID == "" || request.Identity.Space == "" ||
		runtime.ProjectID != request.Identity.ProjectID || runtime.Space != request.Identity.Space {
		return workreport.PreparedOperation{}, fmt.Errorf("%w: requested project/space is not the resolved runtime", ErrWorkPreparationScopeMismatch)
	}
	runtime, err = bindWorkPreparationRuntime(runtime)
	if err != nil {
		return workreport.PreparedOperation{}, err
	}

	artifactID, eventID, err := p.mintDocumentIDs(request.ReportedAt, request.Identity.Actor.System)
	if err != nil {
		return workreport.PreparedOperation{}, err
	}
	layout, err := NewLayout(request.Identity.Actor.System)
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: work layout: %w", err)
	}

	announcement, err := renderWorkAnnouncement(p.renderer, artifactID, request)
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: render work announcement: %w", err)
	}
	evaluation := evaluateWorkAnnouncementPublish(artifactID, eventID, request, runtime.ReporterMembership)
	if evaluation.Verdict != fold.VerdictLegal || !evaluation.Applicable ||
		evaluation.Scope.Kind != fold.EvaluationScopePrimary || evaluation.Scope.Subject != artifactID ||
		evaluation.Outcome != fold.StatePublished {
		return workreport.PreparedOperation{}, fmt.Errorf("%w: verdict=%s applicable=%t outcome=%s",
			ErrWorkEvaluationRefused, evaluation.Verdict, evaluation.Applicable, evaluation.Outcome)
	}
	event, err := encodeWorkPublishEvent(eventID, artifactID, request, evaluation.Outcome)
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: encode work publish event: %w", err)
	}

	artifactPath := layout.Exchange(artifactID)
	eventPath := layout.EventFile(request.ReportedAt.UTC().Format("2006"), eventID)
	validation := WorkCheckpointValidation{
		ProjectID: request.Identity.ProjectID, Space: request.Identity.Space,
		ArtifactID: artifactID, EventID: eventID,
		ArtifactPath: artifactPath, EventPath: eventPath,
		Artifact: bytes.Clone(announcement), Event: bytes.Clone(event),
	}
	if err := p.validator.ValidateWorkCheckpoint(ctx, validation); err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: validate work checkpoint context: %w", err)
	}

	candidate := SubmitRequest{
		System: request.Identity.Actor.System, Verb: "work-checkpoint",
		ArtifactID: artifactID, ArtifactIDs: []string{artifactID}, OperationKey: request.OperationKey,
		Files: []FileWrite{
			{Path: artifactPath, Content: announcement},
			{Path: eventPath, Content: event},
		},
		CommitMessage:     "a2a(announcement): " + artifactID,
		CommitAuthorName:  runtime.CommitAuthorName,
		CommitAuthorEmail: runtime.CommitAuthorEmail,
		RemoteURL:         runtime.RemoteURL,
		Repo:              runtime.Repository,
		BaseBranch:        runtime.BaseBranch,
		PRTitle:           "Work checkpoint " + artifactID,
		PRBody:            "Publish one durable work checkpoint and its lifecycle event.",
		MinBinaryVersion:  runtime.MinimumBinaryVersion,
	}
	prepared, err := p.funnel.PrepareSubmission(ctx, candidate, PreparationContext{
		TargetIdentity:        repositoryIdentity(runtime.Repository),
		BaseCommitSHA:         runtime.BaseCommitSHA,
		ObservedSpaceFloor:    runtime.MinimumBinaryVersion,
		FeatureFloor:          version.OperationalConfidenceFloor,
		SchemaFloor:           version.OperationalConfidenceFloor,
		ProducerCompatibility: "",
	})
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: prepare work submission: %w", err)
	}
	if err := verifyPreparedWorkShape(prepared, artifactID, artifactPath, eventPath, request.OperationKey); err != nil {
		return workreport.PreparedOperation{}, err
	}
	journalRaw, err := EncodePreparedSubmissionJournal(prepared)
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: encode prepared work journal: %w", err)
	}
	journal, err := workreport.NewPreparedJournal(journalRaw)
	if err != nil {
		return workreport.PreparedOperation{}, fmt.Errorf("space: bind prepared work journal: %w", err)
	}
	return workreport.PreparedOperation{
		OperationKey: request.OperationKey, Action: request.Action,
		ArtifactID: artifactID, EventID: eventID, SemanticSequence: request.SemanticSequence,
		Prepared: journal, LocalTarget: request.LocalTarget,
	}, nil
}

func bindWorkPreparationRuntime(runtime WorkPreparationRuntime) (WorkPreparationRuntime, error) {
	baseCommitSHA := strings.ToLower(runtime.BaseCommitSHA)
	if !validRemoteRecoveryOID(baseCommitSHA) {
		return WorkPreparationRuntime{}, ErrWorkPreparationBaseInvalid
	}
	if !validRecoveryRepository(repositoryIdentity(runtime.Repository)) {
		return WorkPreparationRuntime{}, ErrWorkPreparationTargetInvalid
	}
	remoteIdentity, err := canonicalRemoteIdentityDigest(runtime.RemoteURL, runtime.Repository)
	if err != nil || remoteIdentity == "" {
		return WorkPreparationRuntime{}, ErrWorkPreparationTargetInvalid
	}
	runtime.BaseCommitSHA = baseCommitSHA
	return runtime, nil
}

func (p *WorkPreparer) mintDocumentIDs(at time.Time, system string) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	artifactID, err := artifact.MintExchangeIDAt("XA", system, at, p.entropy)
	if err != nil {
		return "", "", fmt.Errorf("space: mint work announcement id: %w", err)
	}
	eventID, err := artifact.MintULIDAt(at, p.entropy)
	if err != nil {
		return "", "", fmt.Errorf("space: mint work event id: %w", err)
	}
	return artifactID, eventID.String(), nil
}

type workAnnouncementWire struct {
	ID               string                 `yaml:"id"`
	SemanticSequence uint64                 `yaml:"semantic_sequence"`
	Mode             workreport.Mode        `yaml:"mode"`
	SubjectRef       string                 `yaml:"subject_ref"`
	Summary          string                 `yaml:"summary"`
	ReportedAt       string                 `yaml:"reported_at"`
	ValidUntil       *string                `yaml:"valid_until,omitempty"`
	WaitingOn        []workreport.WaitingOn `yaml:"waiting_on,omitempty"`
}

type workRefWire struct {
	Ref  string `yaml:"ref"`
	Note string `yaml:"note"`
}

func renderWorkAnnouncement(renderer WorkAnnouncementRenderer, artifactID string, request workreport.PreparationRequest) ([]byte, error) {
	work := workAnnouncementWire{
		ID: request.Identity.WorkID, SemanticSequence: request.SemanticSequence,
		Mode: request.Mode, SubjectRef: request.SubjectRef, Summary: request.Summary,
		ReportedAt: request.ReportedAt.UTC().Format(time.RFC3339),
		WaitingOn:  append([]workreport.WaitingOn(nil), request.WaitingOn...),
	}
	if request.Mode != workreport.ModeFinished {
		value := request.ValidUntil.UTC().Format(time.RFC3339)
		work.ValidUntil = &value
	}
	workYAML, err := yamlField(work)
	if err != nil {
		return nil, err
	}
	toYAML, err := workRecipientsYAML(request.Recipients)
	if err != nil {
		return nil, err
	}
	refsYAML, err := yamlField([]workRefWire{{Ref: request.SubjectRef, Note: "Work checkpoint subject"}})
	if err != nil {
		return nil, err
	}
	raw, err := renderer.RenderWorkAnnouncement(WorkAnnouncementRenderInput{
		Type: "announcement", EnvelopeSchema: "envelope/v2", ID: artifactID,
		Actor:   WorkAnnouncementActor{Kind: request.Identity.Actor.Kind, Name: request.Identity.Actor.Name, Model: request.Identity.Actor.Model},
		Created: request.ReportedAt,
		Fields: map[string]string{
			"title":          "Work checkpoint " + artifactID,
			"space":          request.Identity.Space,
			"from":           request.Identity.Actor.System,
			"to":             toYAML,
			"actor.session":  request.Identity.Actor.Session,
			"classification": request.Classification,
			"thread":         request.Identity.Thread,
			"refs":           refsYAML,
			"work":           workYAML,
		},
		Body: []byte("Structured work checkpoint; see the work block.\n"),
	})
	if err != nil {
		return nil, err
	}
	if _, err := artifact.ParseThreadID(request.SubjectRef); err == nil {
		return removeWorkFrontmatterField(raw, "refs")
	}
	return raw, nil
}

func workRecipientsYAML(recipients []string) (string, error) {
	if len(recipients) == 1 && recipients[0] == "all" {
		return yamlField("all")
	}
	return yamlField(append([]string(nil), recipients...))
}

func yamlField(value any) (string, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func removeWorkFrontmatterField(raw []byte, field string) ([]byte, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(fm.YAML, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("work announcement frontmatter is not a mapping")
	}
	mapping := document.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == field {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			break
		}
	}
	yamlRaw, err := yaml.Marshal(&document)
	if err != nil {
		return nil, err
	}
	return artifact.SerializeFrontmatter(artifact.Frontmatter{YAML: yamlRaw, Body: fm.Body}), nil
}

func evaluateWorkAnnouncementPublish(
	artifactID string,
	eventID string,
	request workreport.PreparationRequest,
	reporterMembership fold.MembershipStatus,
) fold.CandidateEvaluation {
	actor := fold.Actor{
		Kind: request.Identity.Actor.Kind, Name: request.Identity.Actor.Name,
		System: request.Identity.Actor.System,
	}
	envelope := fold.Envelope{
		ID: artifactID, Kind: fold.KindAnnouncement, From: request.Identity.Actor.System,
		To: append([]string(nil), request.Recipients...),
	}
	membership := func(system string) fold.MembershipStatus {
		if system == request.Identity.Actor.System {
			return reporterMembership
		}
		return fold.MembershipUnknown
	}
	return fold.EvaluateCandidate(
		fold.KindAnnouncement,
		fold.NewResult(fold.KindAnnouncement),
		fold.Event{ULID: eventID, Subject: artifactID, Transition: fold.TPublish, Actor: actor},
		envelope,
		membership,
	)
}

type workEventActorWire struct {
	Kind    string `yaml:"kind"`
	Name    string `yaml:"name"`
	System  string `yaml:"system"`
	Model   string `yaml:"model,omitempty"`
	Session string `yaml:"session,omitempty"`
}

type workPublishEventWire struct {
	Schema     string             `yaml:"schema"`
	Event      string             `yaml:"event"`
	Space      string             `yaml:"space"`
	Subject    string             `yaml:"subject"`
	Transition string             `yaml:"transition"`
	State      string             `yaml:"state"`
	Actor      workEventActorWire `yaml:"actor"`
	At         string             `yaml:"at"`
}

func encodeWorkPublishEvent(
	eventID string,
	artifactID string,
	request workreport.PreparationRequest,
	outcome fold.State,
) ([]byte, error) {
	return yaml.Marshal(workPublishEventWire{
		Schema: "event/v1", Event: eventID, Space: request.Identity.Space,
		Subject: artifactID, Transition: fold.TPublish, State: string(outcome),
		Actor: workEventActorWire{
			Kind: request.Identity.Actor.Kind, Name: request.Identity.Actor.Name,
			System: request.Identity.Actor.System, Model: request.Identity.Actor.Model,
			Session: request.Identity.Actor.Session,
		},
		At: request.ReportedAt.UTC().Format(time.RFC3339),
	})
}

func verifyPreparedWorkShape(
	prepared PreparedSubmission,
	artifactID string,
	artifactPath string,
	eventPath string,
	operationKey string,
) error {
	ids := prepared.ArtifactIDs()
	if len(ids) != 1 || ids[0] != artifactID {
		return ErrWorkPreparedShapeInvalid
	}
	mutations := prepared.Mutations()
	if len(mutations) != 2 {
		return ErrWorkPreparedShapeInvalid
	}
	wantPaths := map[string]bool{artifactPath: true, eventPath: true}
	seen := make(map[string]bool, 2)
	for _, mutation := range mutations {
		if mutation.Operation != MutationWrite || !wantPaths[mutation.Path] || seen[mutation.Path] {
			return ErrWorkPreparedShapeInvalid
		}
		seen[mutation.Path] = true
		if operationKey != "" && bytes.Contains(mutation.Bytes, []byte(operationKey)) {
			return ErrWorkOperationKeyLeak
		}
	}
	if len(seen) != 2 || !seen[artifactPath] || !seen[eventPath] {
		return ErrWorkPreparedShapeInvalid
	}
	return nil
}
