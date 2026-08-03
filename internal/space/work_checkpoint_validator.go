package space

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"gopkg.in/yaml.v3"
)

const maxWorkHistoryListingBytes = 1 << 20

// WorkCheckpointRepositoryFacts is the neutral bounded Git/history adapter.
// Policy remains in internal/validate through internal/workcheckpoint.
// WorkCheckpointRepositoryFacts is part of the public package API.
type WorkCheckpointRepositoryFacts struct {
	mirrorDir     string
	ownSystem     string
	authorityRef  string
	subjectRef    string
	continuations WorkCheckpointContinuationResolver
}

// WorkCheckpointContinuationResolver exposes only the exact prior accepted
// local publication receipt. It never turns arbitrary working-tree or remote
// branch contents into durable subject facts.
// WorkCheckpointContinuationResolver is part of the public package API.
type WorkCheckpointContinuationResolver interface {
	ResolveWorkContinuation(context.Context, string, uint64) (WorkCheckpointContinuation, bool, error)
}

// WorkCheckpointContinuation is part of the public package API.
type WorkCheckpointContinuation struct {
	ArtifactID string
	Artifact   []byte
}

// WorkSubmissionValidator mounts the same contextual validator at the P4
// candidate and final-byte funnel seams. The final event may differ only by
// its producer stamp; all work semantics and repository facts remain shared.
// WorkSubmissionValidator is part of the public package API.
type WorkSubmissionValidator struct {
	validator WorkCheckpointValidator
	projectID string
	spaceID   string
}

// NewWorkSubmissionValidator is part of the public package API.
func NewWorkSubmissionValidator(validator WorkCheckpointValidator, projectID, spaceID string) (*WorkSubmissionValidator, error) {
	if validator == nil || strings.TrimSpace(projectID) == "" || strings.TrimSpace(spaceID) == "" {
		return nil, fmt.Errorf("space: work submission validator requires checkpoint validator, project and space")
	}
	return &WorkSubmissionValidator{validator: validator, projectID: projectID, spaceID: spaceID}, nil
}

// ValidateSubmitCandidate is part of the public package API.
func (v *WorkSubmissionValidator) ValidateSubmitCandidate(ctx context.Context, files []FileWrite) error {
	return v.validate(ctx, files, true)
}

// ValidateSubmit is part of the public package API.
func (v *WorkSubmissionValidator) ValidateSubmit(ctx context.Context, files []FileWrite) error {
	return v.validate(ctx, files, false)
}

func (v *WorkSubmissionValidator) validate(ctx context.Context, files []FileWrite, producerStampPending bool) error {
	if len(files) != 2 {
		return ErrWorkPreparedShapeInvalid
	}
	var candidate WorkCheckpointValidation
	candidate.ProjectID, candidate.Space, candidate.ProducerStampPending = v.projectID, v.spaceID, producerStampPending
	for _, file := range files {
		switch {
		case strings.HasSuffix(file.Path, ".md"):
			envelope, err := decodeWorkCheckpointEnvelope(file.Content)
			if err != nil {
				return err
			}
			candidate.ArtifactID, candidate.ArtifactPath, candidate.Artifact = envelope.ID, file.Path, append([]byte(nil), file.Content...)
		case strings.HasSuffix(file.Path, ".yaml"):
			var event workCheckpointEvent
			if err := yaml.Unmarshal(file.Content, &event); err != nil {
				return fmt.Errorf("space: decode work event: %w", err)
			}
			candidate.EventID, candidate.EventPath, candidate.Event = event.Event, file.Path, append([]byte(nil), file.Content...)
		default:
			return ErrWorkPreparedShapeInvalid
		}
	}
	if candidate.ArtifactID == "" || candidate.EventID == "" || len(candidate.Artifact) == 0 || len(candidate.Event) == 0 {
		return ErrWorkPreparedShapeInvalid
	}
	return v.validator.ValidateWorkCheckpoint(ctx, candidate)
}

// NewWorkCheckpointRepositoryFacts is part of the public package API.
func NewWorkCheckpointRepositoryFacts(mirrorDir, ownSystem string) (*WorkCheckpointRepositoryFacts, error) {
	return NewWorkCheckpointRepositoryFactsWithContinuations(mirrorDir, ownSystem, nil)
}

// NewWorkCheckpointRepositoryFactsWithContinuations is part of the public package API.
func NewWorkCheckpointRepositoryFactsWithContinuations(mirrorDir, ownSystem string, continuations WorkCheckpointContinuationResolver) (*WorkCheckpointRepositoryFacts, error) {
	return NewWorkCheckpointRepositoryFactsAt(mirrorDir, ownSystem, "origin/main", "origin/main", continuations)
}

// NewWorkCheckpointRepositoryFactsAt binds durable history to authorityRef
// and subject resolution/manifest to subjectRef. V3 passes merge-base and
// proposed head explicitly; V2 uses origin/main for both.
// NewWorkCheckpointRepositoryFactsAt is part of the public package API.
func NewWorkCheckpointRepositoryFactsAt(mirrorDir, ownSystem, authorityRef, subjectRef string, continuations WorkCheckpointContinuationResolver) (*WorkCheckpointRepositoryFacts, error) {
	if strings.TrimSpace(mirrorDir) == "" || strings.TrimSpace(ownSystem) == "" {
		return nil, fmt.Errorf("space: work checkpoint facts require mirror and own system")
	}
	if strings.TrimSpace(authorityRef) == "" || strings.TrimSpace(subjectRef) == "" {
		return nil, fmt.Errorf("space: work checkpoint facts require authority and subject refs")
	}
	return &WorkCheckpointRepositoryFacts{mirrorDir: mirrorDir, ownSystem: ownSystem, authorityRef: authorityRef, subjectRef: subjectRef, continuations: continuations}, nil
}

type workCheckpointEnvelope struct {
	Schema         string  `yaml:"schema"`
	ID             string  `yaml:"id"`
	Type           string  `yaml:"type"`
	Space          string  `yaml:"space"`
	From           string  `yaml:"from"`
	To             any     `yaml:"to"`
	Category       string  `yaml:"category"`
	Blocking       bool    `yaml:"blocking"`
	AckRequested   bool    `yaml:"ack_requested"`
	TopValidUntil  *string `yaml:"valid_until"`
	Deprecates     *string `yaml:"deprecates"`
	Period         *string `yaml:"period"`
	Thread         string  `yaml:"thread"`
	Classification string  `yaml:"classification"`
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

type workCheckpointEvent struct {
	Event   string `yaml:"event"`
	Space   string `yaml:"space"`
	Subject string `yaml:"subject"`
}

func decodeWorkCheckpointEnvelope(raw []byte) (workCheckpointEnvelope, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return workCheckpointEnvelope{}, fmt.Errorf("space: parse work checkpoint: %w", err)
	}
	var envelope workCheckpointEnvelope
	if err := yaml.Unmarshal(fm.YAML, &envelope); err != nil {
		return workCheckpointEnvelope{}, fmt.Errorf("space: decode work checkpoint: %w", err)
	}
	return envelope, nil
}

func (v *WorkCheckpointRepositoryFacts) repositoryManifestContext(ctx context.Context) (Manifest, []byte, []string, error) {
	mainCommit, err := contractGitResolveCommit(ctx, v.mirrorDir, v.subjectRef)
	if err != nil {
		return Manifest{}, nil, nil, fmt.Errorf("space: resolve work manifest authority: %w", err)
	}
	file, err := contractGitReadPath(ctx, v.mirrorDir, mainCommit, "space.yaml", maxConfigBytes)
	if err != nil {
		return Manifest{}, nil, nil, fmt.Errorf("space: read committed work manifest: %w", err)
	}
	manifest, err := ParseManifest(file.Raw)
	if err != nil {
		return Manifest{}, nil, nil, err
	}
	historical, err := v.historicalManifestSystems(ctx)
	if err != nil {
		return Manifest{}, nil, nil, err
	}
	return manifest, append([]byte(nil), file.Raw...), historical, nil
}

func (v *WorkCheckpointRepositoryFacts) historicalManifestSystems(ctx context.Context) ([]string, error) {
	raw, err := contractGitBytes(ctx, v.mirrorDir, maxWorkHistoryListingBytes,
		"log", "--first-parent", "--format=%H", v.authorityRef, "--", "space.yaml")
	if err != nil {
		return nil, fmt.Errorf("space: read manifest history: %w", err)
	}
	seen := map[string]bool{}
	for _, commit := range strings.Fields(string(raw)) {
		file, readErr := contractGitReadPath(ctx, v.mirrorDir, commit, "space.yaml", maxConfigBytes)
		if readErr != nil {
			return nil, fmt.Errorf("space: read historical manifest %s: %w", commit, readErr)
		}
		manifest, parseErr := ParseManifest(file.Raw)
		if parseErr != nil {
			return nil, fmt.Errorf("space: parse historical manifest %s: %w", commit, parseErr)
		}
		for _, participant := range manifest.Participants {
			seen[participant.System] = true
		}
	}
	out := make([]string, 0, len(seen))
	for system := range seen {
		out = append(out, system)
	}
	sort.Strings(out)
	return out, nil
}

// WorkCheckpointFactQuery is part of the public package API.
type WorkCheckpointFactQuery struct {
	ArtifactID       string
	WorkID           string
	SemanticSequence uint64
	SubjectRef       string
}

// WorkCheckpointPreviousFact is part of the public package API.
type WorkCheckpointPreviousFact struct {
	WorkID, Space, Thread, System, Name, Session, Mode string
	Recipients                                         []string
	Classification                                     string
	SemanticSequence                                   uint64
}

// WorkCheckpointSubjectFact is part of the public package API.
type WorkCheckpointSubjectFact struct{ Ref, Thread, Classification string }

// WorkCheckpointFacts is part of the public package API.
type WorkCheckpointFacts struct {
	ManifestRaw       []byte
	Manifest          Manifest
	HistoricalSystems []string
	Previous          *WorkCheckpointPreviousFact
	Subjects          []WorkCheckpointSubjectFact
}

// ResolveWorkCheckpointFacts is part of the public package API.
func (v *WorkCheckpointRepositoryFacts) ResolveWorkCheckpointFacts(ctx context.Context, query WorkCheckpointFactQuery) (WorkCheckpointFacts, error) {
	manifest, raw, historical, err := v.repositoryManifestContext(ctx)
	if err != nil {
		return WorkCheckpointFacts{}, err
	}
	candidate := workCheckpointEnvelope{}
	candidate.ID = query.ArtifactID
	candidate.Work.ID, candidate.Work.SemanticSequence, candidate.Work.SubjectRef = query.WorkID, query.SemanticSequence, query.SubjectRef
	previous, subjects, err := v.repositoryWorkFacts(ctx, candidate)
	if err != nil {
		return WorkCheckpointFacts{}, err
	}
	return WorkCheckpointFacts{ManifestRaw: raw, Manifest: manifest, HistoricalSystems: historical, Previous: previous, Subjects: subjects}, nil
}

func (v *WorkCheckpointRepositoryFacts) repositoryWorkFacts(ctx context.Context, candidate workCheckpointEnvelope) (*WorkCheckpointPreviousFact, []WorkCheckpointSubjectFact, error) {
	commitOrder, err := v.workCommitOrder(ctx, v.authorityRef)
	if err != nil {
		return nil, nil, err
	}
	var previous *WorkCheckpointPreviousFact
	previousCommit := len(commitOrder) + 1
	previousArtifactID := ""
	var subjects []WorkCheckpointSubjectFact
	wantSubjectID := workSubjectArtifactID(candidate.Work.SubjectRef)
	entries, err := contractGitListTree(ctx, v.mirrorDir, v.subjectRef, true)
	if err != nil {
		return nil, nil, fmt.Errorf("space: list committed work facts: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Path, ".md") {
			continue
		}
		raw, err := contractGitReadBlob(ctx, v.mirrorDir, entry, maxConfigBytes)
		if err != nil {
			if errors.Is(err, ErrContractBoundedResult) {
				// Oversized Markdown cannot be a schema-valid work checkpoint;
				// unrelated repository prose must not block authoring.
				continue
			}
			return nil, nil, err
		}
		envelope, err := decodeWorkCheckpointEnvelope(raw)
		if err != nil {
			// Non-envelope Markdown is unrelated repository data.
			continue
		}
		if wantSubjectID != "" && envelope.ID == wantSubjectID {
			subjects = append(subjects, WorkCheckpointSubjectFact{
				Ref: candidate.Work.SubjectRef, Thread: envelope.Thread, Classification: envelope.Classification,
			})
		}
	}
	entries, err = contractGitListTree(ctx, v.mirrorDir, v.authorityRef, true)
	if err != nil {
		return nil, nil, fmt.Errorf("space: list committed work history: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Path, ".md") {
			continue
		}
		raw, readErr := contractGitReadBlob(ctx, v.mirrorDir, entry, maxConfigBytes)
		if readErr != nil {
			if errors.Is(readErr, ErrContractBoundedResult) {
				continue
			}
			return nil, nil, readErr
		}
		envelope, decodeErr := decodeWorkCheckpointEnvelope(raw)
		if decodeErr != nil || envelope.ID == candidate.ID || envelope.Type != "announcement" || envelope.Work.ID == "" || envelope.Work.ID != candidate.Work.ID {
			continue
		}
		commit, err := contractGitBytes(ctx, v.mirrorDir, 128,
			"log", "--first-parent", "-1", "--format=%H", v.authorityRef, "--", entry.Path)
		if err != nil {
			return nil, nil, fmt.Errorf("space: resolve work checkpoint commit %s: %w", envelope.ID, err)
		}
		commitIndex, committed := commitOrder[strings.ToLower(strings.TrimSpace(string(commit)))]
		if !committed {
			continue
		}
		fact := &WorkCheckpointPreviousFact{
			WorkID: envelope.Work.ID, SemanticSequence: envelope.Work.SemanticSequence,
			Space: envelope.Space, Thread: envelope.Thread, System: envelope.From,
			Name: envelope.Actor.Name, Session: envelope.Actor.Session, Mode: envelope.Work.Mode,
			Recipients: workCheckpointRecipients(envelope.To), Classification: envelope.Classification,
		}
		if previous == nil || commitIndex < previousCommit ||
			(commitIndex == previousCommit && envelope.ID > previousArtifactID) {
			previous = fact
			previousCommit = commitIndex
			previousArtifactID = envelope.ID
		}
	}
	if expectedPrevious := candidate.Work.SemanticSequence - 1; candidate.Work.SemanticSequence > 1 && (previous == nil || previous.SemanticSequence < expectedPrevious) && v.continuations != nil {
		continuation, found, resolveErr := v.continuations.ResolveWorkContinuation(ctx, candidate.Work.ID, candidate.Work.SemanticSequence-1)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		if found {
			envelope, decodeErr := decodeWorkCheckpointEnvelope(continuation.Artifact)
			if decodeErr != nil || envelope.ID != continuation.ArtifactID || envelope.Work.ID != candidate.Work.ID || envelope.Work.SemanticSequence != candidate.Work.SemanticSequence-1 {
				return nil, nil, fmt.Errorf("space: local work continuation does not match expected prior checkpoint")
			}
			previous = &WorkCheckpointPreviousFact{
				WorkID: envelope.Work.ID, SemanticSequence: envelope.Work.SemanticSequence,
				Space: envelope.Space, Thread: envelope.Thread, System: envelope.From, Name: envelope.Actor.Name, Session: envelope.Actor.Session,
				Mode: envelope.Work.Mode, Recipients: workCheckpointRecipients(envelope.To), Classification: envelope.Classification,
			}
		}
	}
	if len(subjects) > 1 {
		first := subjects[0]
		for _, subject := range subjects[1:] {
			if subject != first {
				return nil, nil, fmt.Errorf("space: ambiguous work subject %s", candidate.Work.SubjectRef)
			}
		}
		subjects = subjects[:1]
	}
	return previous, subjects, nil
}

func workCheckpointRecipients(value any) []string {
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

func (v *WorkCheckpointRepositoryFacts) workCommitOrder(ctx context.Context, ref string) (map[string]int, error) {
	raw, err := contractGitBytes(ctx, v.mirrorDir, maxWorkHistoryListingBytes,
		"rev-list", "--first-parent", ref)
	if err != nil {
		return nil, fmt.Errorf("space: read work first-parent history: %w", err)
	}
	order := make(map[string]int)
	for index, commit := range strings.Fields(string(raw)) {
		commit = strings.ToLower(commit)
		if !validRemoteRecoveryOID(commit) {
			return nil, fmt.Errorf("space: invalid work history commit %q", commit)
		}
		order[commit] = index
	}
	if len(order) == 0 {
		return nil, fmt.Errorf("space: empty work first-parent history")
	}
	return order, nil
}

func workSubjectArtifactID(ref string) string {
	if _, err := artifact.ParseThreadID(ref); err == nil {
		return ""
	}
	base := ref
	if index := strings.IndexByte(base, '#'); index >= 0 {
		base = base[:index]
	}
	if index := strings.IndexByte(base, '@'); index >= 0 {
		base = base[:index]
	}
	if _, rest, found := strings.Cut(base, ":"); found {
		base = rest
	}
	if _, err := artifact.ParseID(base); err != nil {
		return ""
	}
	return base
}

var _ CandidateSubmitValidator = (*WorkSubmissionValidator)(nil)
var _ SubmitValidator = (*WorkSubmissionValidator)(nil)
