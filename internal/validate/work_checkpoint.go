package validate

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

var workSpaceIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// WorkMode is validate's closed contextual view of the durable checkpoint
// mode vocabulary. The schema owns structural enum rejection; this view lets
// V2 and V3 apply the same mode-dependent policy without importing the
// sibling workreport core (ADR-001). scripts/check_work_checkpoint_schema.sh
// keeps the copies byte-for-byte aligned.
type WorkMode string

const (
	// WorkModePlanning represents plan-definition work.
	WorkModePlanning WorkMode = "planning"
	// WorkModeImplementing represents implementation work.
	WorkModeImplementing WorkMode = "implementing"
	// WorkModeTesting represents verification work.
	WorkModeTesting WorkMode = "testing"
	// WorkModeReviewing represents review work.
	WorkModeReviewing WorkMode = "reviewing"
	// WorkModeWaiting represents work blocked on a dependency.
	WorkModeWaiting WorkMode = "waiting"
	// WorkModePaused represents explicitly paused work.
	WorkModePaused WorkMode = "paused"
	// WorkModeFinished represents completed work.
	WorkModeFinished WorkMode = "finished"
)

// WorkWaitKind is validate's closed contextual view of work.waiting_on.kind.
// Non-system targets stay opaque; only system targets need manifest context.
type WorkWaitKind string

const (
	// WorkWaitSystem waits for a named system dependency.
	WorkWaitSystem WorkWaitKind = "system"
	// WorkWaitHuman waits for a human dependency.
	WorkWaitHuman WorkWaitKind = "human"
	// WorkWaitTool waits for a tool dependency.
	WorkWaitTool WorkWaitKind = "tool"
	// WorkWaitTimer waits for a timed dependency.
	WorkWaitTimer WorkWaitKind = "timer"
	// WorkWaitExternal waits for an external dependency.
	WorkWaitExternal WorkWaitKind = "external"
)

// WorkCheckpointActor carries only the durable owner fields used by the
// contextual validator. System is derived from envelope.from by the adapter;
// it is not an extra serialized actor field.
type WorkCheckpointActor struct {
	System  string
	Name    string
	Session string
}

// WorkCheckpointWait is the minimal dependency shape needed for referential
// validation of system waits.
type WorkCheckpointWait struct {
	Kind WorkWaitKind
	ID   string
}

// WorkCheckpoint is the schema-decoded work block projected into the fields
// whose meaning needs repository or transition context.
type WorkCheckpoint struct {
	ID               string
	SemanticSequence uint64
	Mode             WorkMode
	SubjectRef       string
	ReportedAt       string
	ValidUntil       *string
	WaitingOn        []WorkCheckpointWait
}

// WorkCheckpointOwner is the immutable owner tuple bound to a work id on its
// first durable publication.
type WorkCheckpointOwner struct {
	Space   string
	Thread  string
	System  string
	Name    string
	Session string
}

// WorkCheckpointPrevious is the latest committed checkpoint for this work id,
// including the exact audience/classification selected at start. A nil
// previous value means this is the first publication; each accepted
// continuation carries the same values forward, preserving the original by
// induction.
type WorkCheckpointPrevious struct {
	WorkID           string
	SemanticSequence uint64
	Owner            WorkCheckpointOwner
	Mode             WorkMode
	Recipients       []string
	Classification   string
}

// WorkCheckpointSubject is a repository-resolved artifact or contract fact.
// Thread is the durable thread which references that subject; Classification
// is the resolved target classification used by the no-downgrade check.
type WorkCheckpointSubject struct {
	Ref            string
	Thread         string
	Classification string
}

// WorkCheckpointInput is the detached, pure V2/V3 boundary for contextual
// work-checkpoint validation. Callers own YAML decoding and repository/manifest
// lookup, then supply exact facts here; this function performs no I/O.
type WorkCheckpointInput struct {
	InvocationPoint InvocationPoint
	ArtifactID      string

	Type           string
	Category       string
	Blocking       bool
	AckRequested   bool
	ValidUntil     *string // top-level status-announcement field; forbidden
	Deprecates     *string
	Period         *string
	Space          string
	Thread         string
	Refs           []string
	Recipients     []string
	Classification string
	FirstParty     bool
	Actor          WorkCheckpointActor
	Work           WorkCheckpoint

	Previous                  *WorkCheckpointPrevious
	ResolvedSubjects          []WorkCheckpointSubject
	ManifestSystems           []string
	HistoricalManifestSystems []string
}

// ValidateWorkCheckpoint applies the single contextual work policy used at
// pre-write V2 and merge-gate V3. JSON Schema remains responsible for the
// closed object shapes, required fields, scalar/array bounds and enum syntax;
// this validator owns the cross-field time, ownership, repository reference,
// manifest-history and classification facts JSON Schema cannot see.
//
//nolint:revive // Validate is the established package API verb used by V2/V3 callers.
func ValidateWorkCheckpoint(in WorkCheckpointInput) Result {
	violations := checkWorkCheckpointPolicy(in)
	violations = append(violations, checkWorkCheckpointReferences(in)...)
	return newResult(in.InvocationPoint, in.ArtifactID, violations)
}

func checkWorkCheckpointPolicy(in WorkCheckpointInput) []Violation {
	var violations []Violation
	if in.InvocationPoint != V2 && in.InvocationPoint != V3 {
		violations = append(violations, workPolicyViolation("invocation_point", "work checkpoint validation requires V2 or V3 context"))
	}
	if in.Type != "announcement" {
		violations = append(violations, workPolicyViolation("type", "a durable work checkpoint must be an announcement"))
	}
	if in.Category != "status" {
		violations = append(violations, workPolicyViolation("category", "a durable work checkpoint must use category status"))
	}
	if in.Blocking {
		violations = append(violations, workPolicyViolation("blocking", "a durable work checkpoint must not block protocol work"))
	}
	if in.AckRequested {
		violations = append(violations, workPolicyViolation("ack_requested", "a durable work checkpoint must not request acknowledgement"))
	}
	for _, field := range []struct {
		path    string
		present bool
	}{
		{path: "valid_until", present: in.ValidUntil != nil},
		{path: "deprecates", present: in.Deprecates != nil},
		{path: "period", present: in.Period != nil},
	} {
		if field.present {
			violations = append(violations, workPolicyViolation(field.path, "work checkpoints forbid top-level attention, replacement and feed-cadence fields"))
		}
	}
	if in.FirstParty && strings.TrimSpace(in.Actor.Session) == "" {
		violations = append(violations, workPolicyViolation("actor.session", "a first-party work checkpoint requires a non-empty actor session"))
	}

	violations = append(violations, checkWorkTimeWindow(in.Work)...)
	violations = append(violations, checkWorkContinuation(in)...)
	return violations
}

func checkWorkTimeWindow(work WorkCheckpoint) []Violation {
	reported, err := parseWorkUTC(work.ReportedAt)
	if err != nil {
		return []Violation{workPolicyViolation("work.reported_at", "work.reported_at must be an RFC-3339 UTC timestamp")}
	}
	if work.Mode == WorkModeFinished {
		if work.ValidUntil != nil {
			return []Violation{workPolicyViolation("work.valid_until", "finished work forbids work.valid_until")}
		}
		return nil
	}
	if work.ValidUntil == nil {
		return []Violation{workPolicyViolation("work.valid_until", "non-terminal work requires work.valid_until")}
	}
	validUntil, err := parseWorkUTC(*work.ValidUntil)
	if err != nil {
		return []Violation{workPolicyViolation("work.valid_until", "work.valid_until must be an RFC-3339 UTC timestamp")}
	}
	if !validUntil.After(reported) {
		return []Violation{workPolicyViolation("work.valid_until", "work.valid_until must be later than work.reported_at")}
	}
	if validUntil.Sub(reported) > 7*24*time.Hour {
		return []Violation{workPolicyViolation("work.valid_until", "work validity must not exceed seven days")}
	}
	return nil
}

func parseWorkUTC(value string) (time.Time, error) {
	// RFC 3339 reserves -00:00 for an unknown local offset. Its numeric
	// value happens to be zero, but it is not an assertion that the timestamp
	// is UTC and therefore cannot satisfy the checkpoint's UTC contract.
	if strings.HasSuffix(value, "-00:00") {
		return time.Time{}, fmt.Errorf("timestamp offset is unknown")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return time.Time{}, fmt.Errorf("timestamp offset is not UTC")
	}
	return parsed, nil
}

func checkWorkContinuation(in WorkCheckpointInput) []Violation {
	if in.Previous == nil {
		if in.Work.SemanticSequence != 1 {
			return []Violation{workPolicyViolation("work.semantic_sequence", "a first work checkpoint must start at semantic_sequence 1")}
		}
		return nil
	}
	previous := *in.Previous
	if previous.WorkID != in.Work.ID {
		return []Violation{workPolicyViolation("work.id", "a continuation must retain its original work id")}
	}
	if previous.Owner.Space != in.Space {
		return []Violation{workPolicyViolation("space", "a work id cannot move to another space")}
	}
	if previous.Owner.Thread != in.Thread {
		return []Violation{workPolicyViolation("thread", "a work id cannot move to another thread")}
	}
	if previous.Owner.System != in.Actor.System {
		return []Violation{workPolicyViolation("actor.system", "a work id cannot change reporter system")}
	}
	if previous.Owner.Name != in.Actor.Name {
		return []Violation{workPolicyViolation("actor.name", "a work id cannot change reporter name")}
	}
	if previous.Owner.Session != in.Actor.Session {
		return []Violation{workPolicyViolation("actor.session", "a work id cannot change reporter session")}
	}
	if !slices.Equal(previous.Recipients, in.Recipients) {
		return []Violation{workPolicyViolation("to", "a continuation must retain the audience selected by its first checkpoint")}
	}
	if previous.Classification != in.Classification {
		return []Violation{workPolicyViolation("classification", "a continuation must retain the classification selected by its first checkpoint")}
	}
	if previous.Mode == WorkModeFinished {
		return []Violation{workPolicyViolation("work.mode", "a finished work stream cannot publish another checkpoint")}
	}
	if previous.SemanticSequence == math.MaxUint64 || in.Work.SemanticSequence != previous.SemanticSequence+1 {
		return []Violation{workPolicyViolation("work.semantic_sequence", "a continuation must increment semantic_sequence by exactly one")}
	}
	return nil
}

func checkWorkCheckpointReferences(in WorkCheckpointInput) []Violation {
	if parsed, err := artifact.ParseThreadID(in.Work.SubjectRef); err == nil {
		if parsed.Raw != in.Thread {
			return []Violation{workReferenceViolation("work.subject_ref", "thread-valued work.subject_ref must equal the checkpoint thread")}
		}
		return checkWorkSystemWaits(in)
	}

	id, _, _ := parseRef(in.Work.SubjectRef)
	if !canonicalWorkArtifactRef(in.Work.SubjectRef, id) {
		return []Violation{workReferenceViolation("work.subject_ref", "work.subject_ref is not a canonical thread, artifact or contract reference")}
	}
	if !slices.Contains(in.Refs, in.Work.SubjectRef) {
		return []Violation{workReferenceViolation("work.subject_ref", "artifact or contract work.subject_ref must also be present in refs")}
	}
	resolved, found := resolvedWorkSubject(in.ResolvedSubjects, in.Work.SubjectRef)
	if !found || resolved.Thread != in.Thread {
		return []Violation{workReferenceViolation("work.subject_ref", "work.subject_ref does not resolve to an artifact or contract referenced by this thread")}
	}
	if classificationRank(resolved.Classification) < 0 || classificationRank(in.Classification) < classificationRank(resolved.Classification) {
		return []Violation{workPolicyViolation("classification", "checkpoint classification must be known and no weaker than its resolved subject")}
	}
	return checkWorkSystemWaits(in)
}

func canonicalWorkArtifactRef(ref, id string) bool {
	if strings.TrimSpace(ref) != ref || id == "" {
		return false
	}
	artifactID := id
	if spaceID, rest, found := strings.Cut(id, ":"); found {
		if !workSpaceIDPattern.MatchString(spaceID) || strings.Contains(rest, ":") {
			return false
		}
		artifactID = rest
	}
	if _, err := artifact.ParseID(artifactID); err != nil {
		return false
	}
	_, version, digest := parseRef(ref)
	if strings.Count(ref, "@") > 1 || strings.Count(ref, "#") > 1 || strings.Contains(ref, "#") && strings.Index(ref, "@") > strings.Index(ref, "#") {
		return false
	}
	if strings.Contains(ref, "@") && version == "" || strings.Contains(ref, "#") && digest == "" {
		return false
	}
	return true
}

func resolvedWorkSubject(subjects []WorkCheckpointSubject, ref string) (WorkCheckpointSubject, bool) {
	for _, subject := range subjects {
		if subject.Ref == ref {
			return subject, true
		}
	}
	return WorkCheckpointSubject{}, false
}

func checkWorkSystemWaits(in WorkCheckpointInput) []Violation {
	for i, wait := range in.Work.WaitingOn {
		if wait.Kind != WorkWaitSystem {
			continue
		}
		if slices.Contains(in.ManifestSystems, wait.ID) || slices.Contains(in.HistoricalManifestSystems, wait.ID) {
			continue
		}
		return []Violation{workReferenceViolation(
			fmt.Sprintf("work.waiting_on.%d.id", i),
			"system wait target is absent from current and historical manifests",
		)}
	}
	return nil
}

func classificationRank(classification string) int {
	switch classification {
	case "public":
		return 0
	case "internal":
		return 1
	case "restricted":
		return 2
	default:
		return -1
	}
}

func workPolicyViolation(path, message string) Violation {
	return Violation{
		Code: "POL-015", Class: ClassPolicy, Path: path, Message: message, Severity: SeverityReject,
	}
}

func workReferenceViolation(path, message string) Violation {
	return Violation{
		Code: "REF-016", Class: ClassReferential, Path: path, Message: message, Severity: SeverityReject,
	}
}
