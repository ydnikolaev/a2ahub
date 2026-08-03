package workcheckpoint

import (
	"context"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

type FactResolver interface {
	ResolveWorkCheckpointFacts(context.Context, space.WorkCheckpointFactQuery) (space.WorkCheckpointFacts, error)
}

type GenericValidator interface {
	ValidateGenericWorkCheckpoint(context.Context, space.WorkCheckpointValidation) error
}

type SubmitValidatorProvider func(context.Context) (space.SubmitValidator, error)

type GenericSubmit struct{ provider SubmitValidatorProvider }

func NewGenericSubmit(provider SubmitValidatorProvider) (*GenericSubmit, error) {
	if provider == nil {
		return nil, fmt.Errorf("workcheckpoint: generic submit validator provider is required")
	}
	return &GenericSubmit{provider: provider}, nil
}

func (v *GenericSubmit) ValidateGenericWorkCheckpoint(ctx context.Context, candidate space.WorkCheckpointValidation) error {
	if strings.TrimSpace(candidate.ArtifactPath) == "" || strings.TrimSpace(candidate.EventPath) == "" {
		return fmt.Errorf("workcheckpoint: canonical artifact and event paths are required")
	}
	validator, err := v.provider(ctx)
	if err != nil {
		return err
	}
	if validator == nil {
		return fmt.Errorf("workcheckpoint: generic submit validator provider returned nil")
	}
	return validator.ValidateSubmit(ctx, []space.FileWrite{
		{Path: candidate.ArtifactPath, Content: append([]byte(nil), candidate.Artifact...)},
		{Path: candidate.EventPath, Content: append([]byte(nil), candidate.Event...)},
	})
}

type Validator struct {
	engine    *validate.Engine
	facts     FactResolver
	generic   GenericValidator
	ownSystem string
}

func New(engine *validate.Engine, facts FactResolver, generic GenericValidator, ownSystem string) (*Validator, error) {
	if engine == nil || facts == nil || generic == nil || strings.TrimSpace(ownSystem) == "" {
		return nil, fmt.Errorf("workcheckpoint: engine, facts, generic validator and own system are required")
	}
	return &Validator{engine: engine, facts: facts, generic: generic, ownSystem: ownSystem}, nil
}

func NewContextual(engine *validate.Engine, facts FactResolver, ownSystem string) (*Validator, error) {
	if engine == nil || facts == nil || strings.TrimSpace(ownSystem) == "" {
		return nil, fmt.Errorf("workcheckpoint: engine, facts and own system are required")
	}
	return &Validator{engine: engine, facts: facts, ownSystem: ownSystem}, nil
}

type envelope struct {
	ID             string  `yaml:"id"`
	Type           string  `yaml:"type"`
	Space          string  `yaml:"space"`
	From           string  `yaml:"from"`
	To             any     `yaml:"to"`
	Category       string  `yaml:"category"`
	Thread         string  `yaml:"thread"`
	Classification string  `yaml:"classification"`
	Blocking       bool    `yaml:"blocking"`
	AckRequested   bool    `yaml:"ack_requested"`
	TopValidUntil  *string `yaml:"valid_until"`
	Deprecates     *string `yaml:"deprecates"`
	Period         *string `yaml:"period"`
	Refs           []struct {
		Ref string `yaml:"ref"`
	} `yaml:"refs"`
	Actor struct {
		Name    string `yaml:"name"`
		Session string `yaml:"session"`
	} `yaml:"actor"`
	Work struct {
		ID               string  `yaml:"id"`
		SemanticSequence uint64  `yaml:"semantic_sequence"`
		Mode             string  `yaml:"mode"`
		SubjectRef       string  `yaml:"subject_ref"`
		ReportedAt       string  `yaml:"reported_at"`
		ValidUntil       *string `yaml:"valid_until"`
		WaitingOn        []struct {
			Kind string `yaml:"kind"`
			ID   string `yaml:"id"`
		} `yaml:"waiting_on"`
	} `yaml:"work"`
}

type event struct {
	Event   string `yaml:"event"`
	Space   string `yaml:"space"`
	Subject string `yaml:"subject"`
}

type ViolationError struct{ Violations []validate.Violation }

func (e *ViolationError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		parts = append(parts, violation.Code+":"+violation.Path)
	}
	return "workcheckpoint: validation rejected: " + strings.Join(parts, ",")
}

func (v *Validator) ValidateWorkCheckpoint(ctx context.Context, candidate space.WorkCheckpointValidation) error {
	point := validate.V2
	if candidate.InvocationPoint != "" {
		point = validate.InvocationPoint(candidate.InvocationPoint)
	}
	if point != validate.V2 && point != validate.V3 {
		return fmt.Errorf("workcheckpoint: invocation point must be V2 or V3")
	}
	if point == validate.V2 {
		if v.generic == nil {
			return fmt.Errorf("workcheckpoint: V2 generic validator is unavailable")
		}
		if err := v.generic.ValidateGenericWorkCheckpoint(ctx, candidate); err != nil {
			return err
		}
	}

	envelope, err := decodeEnvelope(candidate.Artifact)
	if err != nil {
		return err
	}
	var event event
	if err := yaml.Unmarshal(candidate.Event, &event); err != nil {
		return fmt.Errorf("workcheckpoint: decode event: %w", err)
	}
	if envelope.ID != candidate.ArtifactID || envelope.Space != candidate.Space || envelope.From != v.ownSystem || event.Event != candidate.EventID || event.Space != candidate.Space || event.Subject != candidate.ArtifactID {
		return fmt.Errorf("workcheckpoint: candidate identity/scope mismatch")
	}
	facts, err := v.facts.ResolveWorkCheckpointFacts(ctx, space.WorkCheckpointFactQuery{ArtifactID: candidate.ArtifactID, WorkID: envelope.Work.ID, SemanticSequence: envelope.Work.SemanticSequence, SubjectRef: envelope.Work.SubjectRef})
	if err != nil {
		return err
	}
	manifestResult, err := v.engine.ValidateManifest(facts.ManifestRaw)
	if err != nil {
		return err
	}
	if err := resultError(manifestResult); err != nil {
		return err
	}
	eventResult, err := v.engine.ValidateEvent(candidate.Event, facts.Manifest.MinBinaryVersion)
	if err != nil {
		return fmt.Errorf("workcheckpoint: validate event schema: %w", err)
	}
	if err := resultError(eventResult); err != nil {
		return err
	}

	refs := make([]string, 0, len(envelope.Refs))
	for _, ref := range envelope.Refs {
		refs = append(refs, ref.Ref)
	}
	waits := make([]validate.WorkCheckpointWait, 0, len(envelope.Work.WaitingOn))
	for _, wait := range envelope.Work.WaitingOn {
		waits = append(waits, validate.WorkCheckpointWait{Kind: validate.WorkWaitKind(wait.Kind), ID: wait.ID})
	}
	current := make([]string, 0, len(facts.Manifest.Participants))
	for _, participant := range facts.Manifest.Participants {
		current = append(current, participant.System)
	}
	var previous *validate.WorkCheckpointPrevious
	if facts.Previous != nil {
		previous = &validate.WorkCheckpointPrevious{WorkID: facts.Previous.WorkID, SemanticSequence: facts.Previous.SemanticSequence, Owner: validate.WorkCheckpointOwner{Space: facts.Previous.Space, Thread: facts.Previous.Thread, System: facts.Previous.System, Name: facts.Previous.Name, Session: facts.Previous.Session}, Mode: validate.WorkMode(facts.Previous.Mode), Recipients: append([]string(nil), facts.Previous.Recipients...), Classification: facts.Previous.Classification}
	}
	subjects := make([]validate.WorkCheckpointSubject, 0, len(facts.Subjects))
	for _, subject := range facts.Subjects {
		subjects = append(subjects, validate.WorkCheckpointSubject{Ref: subject.Ref, Thread: subject.Thread, Classification: subject.Classification})
	}
	return resultError(validate.ValidateWorkCheckpoint(validate.WorkCheckpointInput{
		InvocationPoint: point, ArtifactID: candidate.ArtifactID, Type: envelope.Type, Category: envelope.Category,
		Blocking: envelope.Blocking, AckRequested: envelope.AckRequested, ValidUntil: envelope.TopValidUntil, Deprecates: envelope.Deprecates, Period: envelope.Period,
		Space: envelope.Space, Thread: envelope.Thread, Refs: refs, Recipients: envelopeRecipients(envelope.To), Classification: envelope.Classification, FirstParty: true,
		Actor:    validate.WorkCheckpointActor{System: envelope.From, Name: envelope.Actor.Name, Session: envelope.Actor.Session},
		Work:     validate.WorkCheckpoint{ID: envelope.Work.ID, SemanticSequence: envelope.Work.SemanticSequence, Mode: validate.WorkMode(envelope.Work.Mode), SubjectRef: envelope.Work.SubjectRef, ReportedAt: envelope.Work.ReportedAt, ValidUntil: envelope.Work.ValidUntil, WaitingOn: waits},
		Previous: previous, ResolvedSubjects: subjects, ManifestSystems: current, HistoricalManifestSystems: facts.HistoricalSystems,
	}))
}

func envelopeRecipients(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, text)
		}
		return out
	default:
		return nil
	}
}

func decodeEnvelope(raw []byte) (envelope, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return envelope{}, fmt.Errorf("workcheckpoint: parse artifact: %w", err)
	}
	var out envelope
	if err := yaml.Unmarshal(fm.YAML, &out); err != nil {
		return envelope{}, fmt.Errorf("workcheckpoint: decode artifact: %w", err)
	}
	return out, nil
}

func resultError(result validate.Result) error {
	if result.Valid {
		return nil
	}
	violations := make([]validate.Violation, 0, len(result.Violations))
	for _, violation := range result.Violations {
		if violation.Severity != validate.SeverityWarning {
			violations = append(violations, violation)
		}
	}
	if len(violations) == 0 {
		return fmt.Errorf("workcheckpoint: validation rejected without a machine code")
	}
	return &ViolationError{Violations: violations}
}

var _ space.WorkCheckpointValidator = (*Validator)(nil)
