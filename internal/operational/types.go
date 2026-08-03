// Package operational builds the immutable, versioned operational read model.
// It is deliberately pure: callers supply already-read protocol, repository,
// checkpoint, and local-lease evidence together with an explicit clock.
package operational

import (
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

const SchemaVersion = 1

var (
	ErrInvalidInput  = errors.New("operational: invalid input")
	ErrBoundedResult = errors.New("operational: bounded result")

	machineCodePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

type Freshness string

const (
	FreshnessLocalCurrent     Freshness = "local-current"
	FreshnessCommittedCurrent Freshness = "committed-current"
	FreshnessStale            Freshness = "stale"
	FreshnessFinished         Freshness = "finished"
	FreshnessPendingRecovery  Freshness = "pending-recovery"
	FreshnessUnknown          Freshness = "unknown"
)

func (f Freshness) valid() bool {
	switch f {
	case FreshnessLocalCurrent, FreshnessCommittedCurrent, FreshnessStale,
		FreshnessFinished, FreshnessPendingRecovery, FreshnessUnknown:
		return true
	default:
		return false
	}
}

type SourceKind string

const (
	SourceSpace     SourceKind = "space"
	SourceLocalWork SourceKind = "local-work"
)

type SourceFreshness string

const (
	SourceCurrent     SourceFreshness = "current"
	SourceStale       SourceFreshness = "stale"
	SourceUnavailable SourceFreshness = "unavailable"
	SourceDegraded    SourceFreshness = "degraded"
)

type Actor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session"`
}

type Source struct {
	Kind       SourceKind      `json:"kind"`
	Space      string          `json:"space,omitempty"`
	Revision   string          `json:"revision"`
	SyncedAt   *time.Time      `json:"synced_at,omitempty"`
	ObservedAt time.Time       `json:"observed_at"`
	Freshness  SourceFreshness `json:"freshness"`
}

type Unavailable struct {
	SourceKind SourceKind `json:"source_kind"`
	Space      string     `json:"space,omitempty"`
	Code       string     `json:"code"`
	Summary    string     `json:"summary"`
}

type Protocol struct {
	Settled    bool     `json:"settled"`
	OpenCount  int      `json:"open_count"`
	WaitingOn  []string `json:"waiting_on"`
	YourMove   bool     `json:"your_move"`
	BlockingBy []string `json:"blocking_by"`
}

type Milestone struct {
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
	Actor      Actor     `json:"actor"`
	Transition string    `json:"transition"`
	Subject    string    `json:"subject"`
}

type CommittedCheckpoint struct {
	ArtifactID string     `json:"artifact_id"`
	ReportedAt time.Time  `json:"reported_at"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

type SharedPending struct {
	OperationID     string `json:"operation_id"`
	Action          string `json:"action"`
	Stage           string `json:"stage"`
	State           string `json:"state"`
	RemainingAction string `json:"remaining_action"`
}

type Work struct {
	WorkID              string                 `json:"work_id"`
	SubjectRef          string                 `json:"subject_ref"`
	Mode                workreport.Mode        `json:"mode"`
	Summary             string                 `json:"summary"`
	Actor               Actor                  `json:"actor"`
	Freshness           Freshness              `json:"freshness"`
	ReportedAt          *time.Time             `json:"reported_at,omitempty"`
	ObservedAt          *time.Time             `json:"observed_at,omitempty"`
	ValidUntil          *time.Time             `json:"valid_until,omitempty"`
	Source              string                 `json:"source"`
	CommittedCheckpoint *CommittedCheckpoint   `json:"committed_checkpoint,omitempty"`
	SharedPending       *SharedPending         `json:"shared_pending,omitempty"`
	WaitingOn           []workreport.WaitingOn `json:"waiting_on"`
}

type Consistency struct {
	Code       string `json:"code"`
	SubjectRef string `json:"subject_ref"`
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
}

type Window struct {
	Truncated bool `json:"truncated"`
	Total     int  `json:"total"`
	Shown     int  `json:"shown"`
}

type TimelineRow struct {
	Space             string        `json:"space"`
	Thread            string        `json:"thread"`
	Title             string        `json:"title"`
	Participants      []string      `json:"participants"`
	Protocol          Protocol      `json:"protocol"`
	LatestMilestone   *Milestone    `json:"latest_milestone,omitempty"`
	Work              []Work        `json:"work"`
	WorkWindow        Window        `json:"work_window"`
	Consistency       []Consistency `json:"consistency"`
	ConsistencyWindow Window        `json:"consistency_window"`
}

type Snapshot struct {
	SchemaVersion  int           `json:"schema_version"`
	GeneratedAt    time.Time     `json:"generated_at"`
	Revision       string        `json:"revision"`
	Sources        []Source      `json:"sources"`
	Timeline       []TimelineRow `json:"timeline"`
	TimelineWindow Window        `json:"timeline_window"`
	Unavailable    []Unavailable `json:"unavailable"`
}

type Input struct {
	Sources       []SourceEvidence
	Threads       []ThreadEvidence
	CommittedWork []CommittedWorkEvidence
	LocalLeases   []LocalLeaseEvidence
	Unavailable   []Unavailable
}

type SourceEvidence Source

type ThreadEvidence struct {
	Space           string
	Thread          string
	Title           string
	Participants    []string
	Protocol        Protocol
	LatestMilestone *Milestone
}

type CommittedWorkEvidence struct {
	Space          string
	Thread         string
	WorkID         string
	SubjectRef     string
	Mode           workreport.Mode
	Summary        string
	Actor          workreport.Actor
	WaitingOn      []workreport.WaitingOn
	ReportedAt     time.Time
	ValidUntil     time.Time
	ArtifactID     string
	CommitSequence uint64
}

// LocalLeaseEvidence carries a parsed P2 lease. Pending is the safe projection
// decoded by the space adapter from P4's result journal; opaque journal bytes
// never enter this package or its output.
type LocalLeaseEvidence struct {
	Lease      workreport.Lease
	ObservedAt time.Time
	Pending    *SharedPending
}

type Clock interface{ Now() time.Time }

type BoundedResultError struct {
	Boundary string
	Count    int
	Maximum  int
}

func (e *BoundedResultError) Error() string {
	return fmt.Sprintf("%v: %s has %d items, maximum %d", ErrBoundedResult, e.Boundary, e.Count, e.Maximum)
}

func (e *BoundedResultError) Unwrap() error { return ErrBoundedResult }
