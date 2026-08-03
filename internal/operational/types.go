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

// SchemaVersion is the version of the operational snapshot wire format.
const SchemaVersion = 1

var (
	// ErrInvalidInput reports invalid source evidence or projection configuration.
	ErrInvalidInput = errors.New("operational: invalid input")
	// ErrBoundedResult reports a projection result exceeding its configured bound.
	ErrBoundedResult = errors.New("operational: bounded result")

	machineCodePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

// Freshness classifies how current a projected work item is.
type Freshness string

const (
	// FreshnessLocalCurrent identifies a non-expired valid local lease.
	FreshnessLocalCurrent Freshness = "local-current"
	// FreshnessCommittedCurrent identifies current durable shared evidence.
	FreshnessCommittedCurrent Freshness = "committed-current"
	// FreshnessStale identifies evidence that is no longer current.
	FreshnessStale Freshness = "stale"
	// FreshnessFinished identifies completed work.
	FreshnessFinished Freshness = "finished"
	// FreshnessPendingRecovery identifies a closing lease awaiting recovery.
	FreshnessPendingRecovery Freshness = "pending-recovery"
	// FreshnessUnknown identifies work whose current status cannot be established.
	FreshnessUnknown Freshness = "unknown"
)

// SourceKind identifies the origin of a projection fact.
type SourceKind string

const (
	// SourceSpace identifies committed evidence from a configured space.
	SourceSpace SourceKind = "space"
	// SourceLocalWork identifies evidence read from the local lease store.
	SourceLocalWork SourceKind = "local-work"
)

// SourceFreshness classifies how current a source mirror is.
type SourceFreshness string

const (
	// SourceCurrent identifies a readable current source mirror.
	SourceCurrent SourceFreshness = "current"
	// SourceStale identifies a readable mirror older than its freshness target.
	SourceStale SourceFreshness = "stale"
	// SourceUnavailable identifies a source whose evidence cannot be read.
	SourceUnavailable SourceFreshness = "unavailable"
	// SourceDegraded identifies a source refreshed with an explicit problem.
	SourceDegraded SourceFreshness = "degraded"
)

// Actor identifies a protocol or work actor in public-safe form.
type Actor struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Model   string `json:"model,omitempty"`
	Session string `json:"session"`
}

// Source describes a committed-space or local-work evidence source.
type Source struct {
	Kind       SourceKind      `json:"kind"`
	Space      string          `json:"space,omitempty"`
	Revision   string          `json:"revision"`
	SyncedAt   *time.Time      `json:"synced_at,omitempty"`
	ObservedAt time.Time       `json:"observed_at"`
	Freshness  SourceFreshness `json:"freshness"`
}

// Unavailable reports a bounded, public-safe evidence gap.
type Unavailable struct {
	SourceKind SourceKind `json:"source_kind"`
	Space      string     `json:"space,omitempty"`
	Code       string     `json:"code"`
	Summary    string     `json:"summary"`
}

// Protocol is the folded obligation state of one timeline row.
type Protocol struct {
	Settled    bool     `json:"settled"`
	OpenCount  int      `json:"open_count"`
	WaitingOn  []string `json:"waiting_on"`
	YourMove   bool     `json:"your_move"`
	BlockingBy []string `json:"blocking_by"`
}

// Milestone is the latest meaningful protocol transition on a row.
type Milestone struct {
	Kind       string    `json:"kind"`
	At         time.Time `json:"at"`
	Actor      Actor     `json:"actor"`
	Transition string    `json:"transition"`
	Subject    string    `json:"subject"`
}

// CommittedCheckpoint points to durable shared work evidence.
type CommittedCheckpoint struct {
	ArtifactID string     `json:"artifact_id"`
	ReportedAt time.Time  `json:"reported_at"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

// SharedPending is the safe projection of an unfinished shared write.
type SharedPending struct {
	OperationID     string `json:"operation_id"`
	Action          string `json:"action"`
	Stage           string `json:"stage"`
	State           string `json:"state"`
	RemainingAction string `json:"remaining_action"`
}

// Work is one bounded committed or local work-status projection.
type Work struct {
	WorkID              string                 `json:"work_id"`
	SubjectRef          string                 `json:"subject_ref"`
	Mode                workreport.Mode        `json:"mode"`
	Summary             string                 `json:"summary"`
	Actor               Actor                  `json:"actor"`
	Freshness           Freshness              `json:"freshness"`
	Current             bool                   `json:"current"`
	ReportedAt          *time.Time             `json:"reported_at,omitempty"`
	ObservedAt          *time.Time             `json:"observed_at,omitempty"`
	ValidUntil          *time.Time             `json:"valid_until,omitempty"`
	Source              string                 `json:"source"`
	CommittedCheckpoint *CommittedCheckpoint   `json:"committed_checkpoint,omitempty"`
	SharedPending       *SharedPending         `json:"shared_pending,omitempty"`
	WaitingOn           []workreport.WaitingOn `json:"waiting_on"`
}

// Consistency records a bounded cross-source anomaly for a work item.
type Consistency struct {
	Code       string `json:"code"`
	SubjectRef string `json:"subject_ref"`
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
}

// Window describes truncation applied to a bounded collection.
type Window struct {
	Truncated bool `json:"truncated"`
	Total     int  `json:"total"`
	Shown     int  `json:"shown"`
}

// TimelineRow is the public projection of one protocol thread.
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

// Snapshot is the immutable, versioned operational read model.
type Snapshot struct {
	SchemaVersion  int           `json:"schema_version"`
	GeneratedAt    time.Time     `json:"generated_at"`
	Revision       string        `json:"revision"`
	Sources        []Source      `json:"sources"`
	Timeline       []TimelineRow `json:"timeline"`
	TimelineWindow Window        `json:"timeline_window"`
	Unavailable    []Unavailable `json:"unavailable"`
}

// Input groups parsed evidence supplied to Build.
type Input struct {
	Sources       []SourceEvidence
	Threads       []ThreadEvidence
	CommittedWork []CommittedWorkEvidence
	LocalLeases   []LocalLeaseEvidence
	Unavailable   []Unavailable
}

// SourceEvidence is input-only source evidence for a snapshot.
type SourceEvidence Source

// ThreadEvidence is input-only folded evidence for a protocol thread.
type ThreadEvidence struct {
	Space           string
	Thread          string
	Title           string
	Participants    []string
	Protocol        Protocol
	LatestMilestone *Milestone
}

// CommittedWorkEvidence is input-only durable work checkpoint evidence.
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

// Clock supplies the explicit projection time boundary.
type Clock interface{ Now() time.Time }

// BoundedResultError reports a collection that exceeds a public result bound.
type BoundedResultError struct {
	Boundary string
	Count    int
	Maximum  int
}

func (e *BoundedResultError) Error() string {
	return fmt.Sprintf("%v: %s has %d items, maximum %d", ErrBoundedResult, e.Boundary, e.Count, e.Maximum)
}

func (e *BoundedResultError) Unwrap() error { return ErrBoundedResult }
