package operational

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ydnikolaev/a2ahub/internal/sensitive"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

const (
	maximumKeyRunes     = 512
	maximumTitleRunes   = 512
	maximumSummaryRunes = 512
)

var (
	credentialAssignmentPattern = regexp.MustCompile(`(?i)(^|[^a-z0-9])(password|passwd|token|authorization|api[-_]?key|secret)[[:space:]]*[:=]`)
	bearerCredentialPattern     = regexp.MustCompile(`(?i)(^|[^a-z0-9])bearer([[:space:]]+|[:=])`)
)

type projectedWork struct {
	value     Work
	anomalous bool
	orderTime time.Time
	include   bool
}

type projectedRow struct {
	value     TimelineRow
	bucket    int
	orderTime time.Time
}

type workGroup struct {
	committed []CommittedWorkEvidence
	leases    []LocalLeaseEvidence
}

// Build projects bounded source evidence into one immutable operational snapshot.
func Build(input Input, clock Clock, limits Limits) (Snapshot, error) {
	if clock == nil {
		return Snapshot{}, fmt.Errorf("%w: clock is required", ErrInvalidInput)
	}
	normalizedLimits, err := limits.normalized()
	if err != nil {
		return Snapshot{}, err
	}
	now := clock.Now().UTC()
	if now.IsZero() {
		return Snapshot{}, fmt.Errorf("%w: clock returned zero time", ErrInvalidInput)
	}

	sources, err := projectSources(input.Sources)
	if err != nil {
		return Snapshot{}, err
	}
	unavailable, err := projectUnavailable(input.Unavailable)
	if err != nil {
		return Snapshot{}, err
	}

	threadMap := make(map[string]ThreadEvidence, len(input.Threads))
	for _, evidence := range input.Threads {
		if err := validateThreadEvidence(evidence); err != nil {
			return Snapshot{}, err
		}
		key := threadKey(evidence.Space, evidence.Thread)
		if _, exists := threadMap[key]; exists {
			return Snapshot{}, fmt.Errorf("%w: duplicate thread %s/%s", ErrInvalidInput, evidence.Space, evidence.Thread)
		}
		threadMap[key] = evidence
	}

	groups := make(map[string]*workGroup)
	for _, evidence := range input.CommittedWork {
		if err := validateWorkCoordinates(evidence.Space, evidence.Thread, evidence.WorkID); err != nil {
			return Snapshot{}, err
		}
		if _, ok := threadMap[threadKey(evidence.Space, evidence.Thread)]; !ok {
			return Snapshot{}, fmt.Errorf("%w: checkpoint references unknown thread %s/%s", ErrInvalidInput, evidence.Space, evidence.Thread)
		}
		key := workKey(evidence.Space, evidence.Thread, evidence.WorkID)
		group := groups[key]
		if group == nil {
			group = &workGroup{}
			groups[key] = group
		}
		group.committed = append(group.committed, evidence)
	}
	for _, evidence := range input.LocalLeases {
		identity := evidence.Lease.Identity
		if err := validateWorkCoordinates(identity.Space, identity.Thread, identity.WorkID); err != nil {
			unavailable = append(unavailable, Unavailable{
				SourceKind: SourceLocalWork, Code: "invalid-local-lease",
				Summary: safeText("A local work lease could not be associated with a thread", maximumSummaryRunes),
			})
			continue
		}
		if _, ok := threadMap[threadKey(identity.Space, identity.Thread)]; !ok {
			return Snapshot{}, fmt.Errorf("%w: local lease references unknown thread %s/%s", ErrInvalidInput, identity.Space, identity.Thread)
		}
		key := workKey(identity.Space, identity.Thread, identity.WorkID)
		group := groups[key]
		if group == nil {
			group = &workGroup{}
			groups[key] = group
		}
		group.leases = append(group.leases, evidence)
	}

	groupedByThread := make(map[string][]projectedWork)
	consistencyByThread := make(map[string][]Consistency)
	groupKeys := make([]string, 0, len(groups))
	for key := range groups {
		groupKeys = append(groupKeys, key)
	}
	sort.Strings(groupKeys)
	for _, key := range groupKeys {
		work, facts := projectWorkGroup(groups[key], now)
		space, thread, _ := splitWorkKey(key)
		tk := threadKey(space, thread)
		if work.include {
			groupedByThread[tk] = append(groupedByThread[tk], work)
		}
		consistencyByThread[tk] = append(consistencyByThread[tk], facts...)
	}

	rows := make([]projectedRow, 0, len(threadMap))
	threadKeys := make([]string, 0, len(threadMap))
	for key := range threadMap {
		threadKeys = append(threadKeys, key)
	}
	sort.Strings(threadKeys)
	blockingCount := 0
	for _, key := range threadKeys {
		row, err := projectThread(threadMap[key], groupedByThread[key], consistencyByThread[key], now, normalizedLimits)
		if err != nil {
			return Snapshot{}, err
		}
		if blockingProtocol(row.value.Protocol) {
			blockingCount++
		}
		rows = append(rows, row)
	}
	if blockingCount > MaximumTimelineRows {
		return Snapshot{}, &BoundedResultError{Boundary: "blocking timeline rows", Count: blockingCount, Maximum: MaximumTimelineRows}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].bucket != rows[j].bucket {
			return rows[i].bucket < rows[j].bucket
		}
		if rows[i].bucket < 2 && !rows[i].orderTime.Equal(rows[j].orderTime) {
			return rows[i].orderTime.After(rows[j].orderTime)
		}
		if rows[i].value.Space != rows[j].value.Space {
			return rows[i].value.Space < rows[j].value.Space
		}
		return rows[i].value.Thread < rows[j].value.Thread
	})

	totalRows := len(rows)
	shownRows := chooseTimelineRows(rows, normalizedLimits.TimelineRows)
	timeline := make([]TimelineRow, 0, len(shownRows))
	for _, row := range shownRows {
		timeline = append(timeline, row.value)
	}
	if timeline == nil {
		timeline = []TimelineRow{}
	}

	snapshot := Snapshot{
		SchemaVersion:  SchemaVersion,
		GeneratedAt:    now,
		Sources:        sources,
		Timeline:       timeline,
		TimelineWindow: Window{Truncated: len(timeline) < totalRows, Total: totalRows, Shown: len(timeline)},
		Unavailable:    unavailable,
	}
	revision, err := semanticRevision(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Revision = revision
	raw, err := CanonicalJSON(snapshot)
	if err != nil {
		return Snapshot{}, fmt.Errorf("operational: encode snapshot: %w", err)
	}
	if len(raw) > normalizedLimits.EncodedSnapshot {
		return Snapshot{}, &BoundedResultError{Boundary: "encoded snapshot bytes", Count: len(raw), Maximum: normalizedLimits.EncodedSnapshot}
	}
	return snapshot, nil
}

func projectSources(in []SourceEvidence) ([]Source, error) {
	out := make([]Source, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, evidence := range in {
		source := Source(evidence)
		if source.ObservedAt.IsZero() || !validKey(source.Revision) || containsCredentialToken(source.Revision) {
			return nil, fmt.Errorf("%w: invalid source revision or observation", ErrInvalidInput)
		}
		source.ObservedAt = source.ObservedAt.UTC()
		if source.SyncedAt != nil {
			synced := source.SyncedAt.UTC()
			source.SyncedAt = &synced
		}
		var key string
		switch source.Kind {
		case SourceSpace:
			if !validKey(source.Space) || source.SyncedAt == nil ||
				(source.Freshness != SourceCurrent && source.Freshness != SourceStale && source.Freshness != SourceUnavailable) {
				return nil, fmt.Errorf("%w: invalid space source", ErrInvalidInput)
			}
			key = string(source.Kind) + "\x00" + source.Space
		case SourceLocalWork:
			if source.Space != "" || source.SyncedAt != nil ||
				(source.Freshness != SourceCurrent && source.Freshness != SourceDegraded && source.Freshness != SourceUnavailable) {
				return nil, fmt.Errorf("%w: invalid local-work source", ErrInvalidInput)
			}
			key = string(source.Kind)
		default:
			return nil, fmt.Errorf("%w: unknown source kind", ErrInvalidInput)
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: duplicate source %q", ErrInvalidInput, key)
		}
		seen[key] = struct{}{}
		out = append(out, source)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Space < out[j].Space
	})
	if out == nil {
		out = []Source{}
	}
	return out, nil
}

func projectUnavailable(in []Unavailable) ([]Unavailable, error) {
	out := make([]Unavailable, 0, len(in))
	for _, item := range in {
		if item.SourceKind != SourceSpace && item.SourceKind != SourceLocalWork {
			return nil, fmt.Errorf("%w: invalid unavailable source kind", ErrInvalidInput)
		}
		if item.SourceKind == SourceSpace && !validKey(item.Space) || item.SourceKind == SourceLocalWork && item.Space != "" {
			return nil, fmt.Errorf("%w: invalid unavailable source scope", ErrInvalidInput)
		}
		if !machineCodePattern.MatchString(item.Code) {
			return nil, fmt.Errorf("%w: invalid unavailable code", ErrInvalidInput)
		}
		item.Summary = safeText(item.Summary, maximumSummaryRunes)
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].SourceKind != out[j].SourceKind {
			return out[i].SourceKind < out[j].SourceKind
		}
		if out[i].Space != out[j].Space {
			return out[i].Space < out[j].Space
		}
		return out[i].Summary < out[j].Summary
	})
	if out == nil {
		out = []Unavailable{}
	}
	return out, nil
}

func validateThreadEvidence(e ThreadEvidence) error {
	if !validKey(e.Space) || !validKey(e.Thread) || e.Protocol.OpenCount < 0 {
		return fmt.Errorf("%w: invalid thread coordinates or protocol count", ErrInvalidInput)
	}
	if len(e.Participants) > MaximumTimelineRows {
		return &BoundedResultError{Boundary: "thread participants", Count: len(e.Participants), Maximum: MaximumTimelineRows}
	}
	if len(e.Protocol.WaitingOn)+len(e.Protocol.BlockingBy) > MaximumConsistency {
		return &BoundedResultError{Boundary: "thread protocol facts", Count: len(e.Protocol.WaitingOn) + len(e.Protocol.BlockingBy), Maximum: MaximumConsistency}
	}
	if e.Protocol.Settled && (e.Protocol.OpenCount != 0 || len(e.Protocol.WaitingOn) != 0 || len(e.Protocol.BlockingBy) != 0 || e.Protocol.YourMove) {
		return fmt.Errorf("%w: settled protocol has open obligations", ErrInvalidInput)
	}
	if e.LatestMilestone != nil && (e.LatestMilestone.At.IsZero() || !validKey(e.LatestMilestone.Kind) || !validKey(e.LatestMilestone.Transition) || !validKey(e.LatestMilestone.Subject)) {
		return fmt.Errorf("%w: invalid milestone", ErrInvalidInput)
	}
	if e.LatestMilestone != nil && (!validKey(e.LatestMilestone.Actor.Kind) || !validKey(e.LatestMilestone.Actor.Name) ||
		!validKey(e.LatestMilestone.Actor.System) || (e.LatestMilestone.Actor.Session != "" && !validKey(e.LatestMilestone.Actor.Session))) {
		return fmt.Errorf("%w: incomplete milestone actor", ErrInvalidInput)
	}
	return nil
}

func validateWorkCoordinates(space, thread, workID string) error {
	if !validKey(space) || !validKey(thread) || !validKey(workID) {
		return fmt.Errorf("%w: invalid work coordinates", ErrInvalidInput)
	}
	return nil
}

func projectWorkGroup(group *workGroup, now time.Time) (projected projectedWork, facts []Consistency) {
	defer func() {
		projected.value.Current = currentFreshness(projected.value.Freshness)
	}()
	var committed *CommittedWorkEvidence
	if len(group.committed) > 0 {
		sort.SliceStable(group.committed, func(i, j int) bool {
			if group.committed[i].CommitSequence != group.committed[j].CommitSequence {
				return group.committed[i].CommitSequence > group.committed[j].CommitSequence
			}
			if group.committed[i].ArtifactID != group.committed[j].ArtifactID {
				return group.committed[i].ArtifactID > group.committed[j].ArtifactID
			}
			return committedTieKey(group.committed[i]) > committedTieKey(group.committed[j])
		})
		committed = &group.committed[0]
		if len(group.committed) > 1 && group.committed[0].CommitSequence == group.committed[1].CommitSequence &&
			group.committed[0].ArtifactID == group.committed[1].ArtifactID {
			facts = append(facts, consistency("duplicate-work-evidence", committed.SubjectRef, "Conflicting committed evidence shares one checkpoint ordering key"))
		}
		for i := 1; i < len(group.committed); i++ {
			if !sameActor(committed.Actor, group.committed[i].Actor) {
				facts = append(facts, consistency("work-identity-mismatch", committed.SubjectRef, "Committed checkpoints disagree about the immutable actor session"))
				break
			}
		}
	}
	var lease *LocalLeaseEvidence
	if len(group.leases) > 0 {
		sort.SliceStable(group.leases, func(i, j int) bool {
			if group.leases[i].Lease.HeartbeatSequence != group.leases[j].Lease.HeartbeatSequence {
				return group.leases[i].Lease.HeartbeatSequence > group.leases[j].Lease.HeartbeatSequence
			}
			if group.leases[i].Lease.Identity.LeaseKey != group.leases[j].Lease.Identity.LeaseKey {
				return group.leases[i].Lease.Identity.LeaseKey > group.leases[j].Lease.Identity.LeaseKey
			}
			return leaseTieKey(group.leases[i]) > leaseTieKey(group.leases[j])
		})
		lease = &group.leases[0]
		for i := range group.leases {
			if validLocalEvidence(group.leases[i]) {
				lease = &group.leases[i]
				if i > 0 {
					facts = append(facts, consistency("invalid-local-lease", group.leases[0].Lease.SubjectRef, "A newer corrupt local lease was ignored in favor of the latest valid lease"))
				}
				break
			}
		}
		if len(group.leases) > 1 && group.leases[0].Lease.HeartbeatSequence == group.leases[1].Lease.HeartbeatSequence &&
			group.leases[0].Lease.Identity.LeaseKey == group.leases[1].Lease.Identity.LeaseKey {
			facts = append(facts, consistency("duplicate-work-evidence", lease.Lease.SubjectRef, "Conflicting local evidence shares one lease ordering key"))
		}
		for i := range group.leases {
			if &group.leases[i] != lease && !lease.Lease.Identity.Equal(group.leases[i].Lease.Identity) {
				facts = append(facts, consistency("work-identity-mismatch", lease.Lease.SubjectRef, "Local leases disagree about the immutable work identity"))
				break
			}
		}
	}

	if committed == nil {
		local, localFacts := projectLocalOnly(*lease, now)
		return local, append(facts, localFacts...)
	}
	base, anomaly, valid := projectCommitted(*committed, now)
	if !valid {
		facts = append(facts, consistency("invalid-checkpoint", committed.SubjectRef, "The latest committed work checkpoint is invalid"))
	}
	if anomaly {
		facts = append(facts, consistency("future-clock-anomaly", committed.SubjectRef, "The latest committed report is dated in the future"))
	}
	if lease == nil {
		return projectedWork{value: base, anomalous: anomaly, orderTime: boundedTime(committed.ReportedAt, now), include: validWireWork(base)}, facts
	}
	if committed.Mode == workreport.ModeFinished {
		facts = append(facts, consistency("orphan-lease-finished", committed.SubjectRef, "A local lease exists for a work item already committed as finished"))
		return projectedWork{value: base, anomalous: anomaly, orderTime: boundedTime(committed.ReportedAt, now), include: validWireWork(base)}, facts
	}
	if !sameActor(committed.Actor, lease.Lease.Identity.Actor) {
		facts = append(facts, consistency("work-identity-mismatch", committed.SubjectRef, "Committed and local work evidence disagree about the immutable actor session"))
		return projectedWork{value: base, anomalous: anomaly, orderTime: boundedTime(committed.ReportedAt, now), include: validWireWork(base)}, facts
	}
	local, localFacts := projectLocal(*lease, now)
	facts = append(facts, localFacts...)
	local.value.CommittedCheckpoint = checkpointRef(*committed)
	if local.value.Freshness == FreshnessLocalCurrent || local.value.Freshness == FreshnessPendingRecovery {
		return local, facts
	}
	return projectedWork{value: base, anomalous: anomaly, orderTime: boundedTime(committed.ReportedAt, now), include: validWireWork(base)}, facts
}

func currentFreshness(value Freshness) bool {
	switch value {
	case FreshnessLocalCurrent, FreshnessCommittedCurrent:
		return true
	default:
		return false
	}
}

func projectCommitted(e CommittedWorkEvidence, now time.Time) (Work, bool, bool) {
	anomaly := e.ReportedAt.After(now)
	actor := projectActor(e.Actor)
	item := Work{
		WorkID: safeText(e.WorkID, maximumKeyRunes), SubjectRef: safeText(e.SubjectRef, maximumKeyRunes),
		Mode: e.Mode, Summary: safeText(e.Summary, maximumSummaryRunes), Actor: actor,
		Freshness: FreshnessUnknown, Source: "committed-checkpoint", WaitingOn: projectWaiting(e.WaitingOn),
		CommittedCheckpoint: checkpointRef(e),
	}
	reported := e.ReportedAt.UTC()
	item.ReportedAt = &reported
	if !e.ValidUntil.IsZero() {
		validUntil := e.ValidUntil.UTC()
		item.ValidUntil = &validUntil
	}
	valid := validCommittedEvidence(e)
	if anomaly {
		return item, true, valid
	}
	if e.Mode == workreport.ModeFinished {
		valid = valid && e.ValidUntil.IsZero()
		if valid {
			item.Freshness = FreshnessFinished
		}
		return item, false, valid
	}
	valid = valid && !e.ValidUntil.IsZero() && e.ValidUntil.After(e.ReportedAt) && e.ValidUntil.Sub(e.ReportedAt) <= 7*24*time.Hour
	if !valid {
		return item, false, false
	}
	if now.After(e.ValidUntil) {
		item.Freshness = FreshnessStale
	} else {
		item.Freshness = FreshnessCommittedCurrent
	}
	return item, false, true
}

func validCommittedEvidence(e CommittedWorkEvidence) bool {
	if !e.Mode.Valid() || e.ReportedAt.IsZero() || e.CommitSequence == 0 || !validKey(e.ArtifactID) ||
		!validKey(e.SubjectRef) || e.Actor.Kind == "" || e.Actor.Name == "" || e.Actor.System == "" || e.Actor.Session == "" {
		return false
	}
	if e.Mode == workreport.ModeWaiting {
		if len(e.WaitingOn) == 0 || len(e.WaitingOn) > 8 {
			return false
		}
	} else if len(e.WaitingOn) != 0 {
		return false
	}
	for _, waiting := range e.WaitingOn {
		if !waiting.Kind.Valid() || !validKey(waiting.ID) {
			return false
		}
	}
	return true
}

func projectLocalOnly(e LocalLeaseEvidence, now time.Time) (projectedWork, []Consistency) {
	return projectLocal(e, now)
}

func projectLocal(e LocalLeaseEvidence, now time.Time) (projectedWork, []Consistency) {
	lease := e.Lease
	item := Work{
		WorkID: safeText(lease.Identity.WorkID, maximumKeyRunes), SubjectRef: safeText(lease.SubjectRef, maximumKeyRunes),
		Mode: lease.Mode, Summary: safeText(lease.Summary, maximumSummaryRunes), Actor: projectActor(lease.Identity.Actor),
		Freshness: FreshnessUnknown, Source: "local-lease", WaitingOn: projectWaiting(lease.WaitingOn),
	}
	reported := lease.RenewedAt.UTC()
	observed := e.ObservedAt.UTC()
	validUntil := lease.ExpiresAt.UTC()
	item.ReportedAt = &reported
	if !e.ObservedAt.IsZero() {
		item.ObservedAt = &observed
	}
	item.ValidUntil = &validUntil
	facts := []Consistency{}
	valid := workreport.ValidateLease(lease) == nil && !e.ObservedAt.IsZero()
	anomaly := lease.RenewedAt.After(now)
	if !valid {
		facts = append(facts, consistency("invalid-local-lease", lease.SubjectRef, "The latest local work lease is invalid"))
		return projectedWork{value: item, include: validWireWork(item)}, facts
	}
	if anomaly {
		facts = append(facts, consistency("future-clock-anomaly", lease.SubjectRef, "The latest local report is dated in the future"))
		return projectedWork{value: item, anomalous: true, include: validWireWork(item)}, facts
	}
	pendingValid := validatePendingProjection(lease, e.Pending)
	if !pendingValid {
		facts = append(facts, consistency("pending-projection-invalid", lease.SubjectRef, "The safe pending-write projection is absent or inconsistent"))
		return projectedWork{value: item, include: validWireWork(item)}, facts
	}
	item.SharedPending = clonePending(e.Pending)
	switch {
	case lease.Closing:
		item.Freshness = FreshnessPendingRecovery
	case now.After(lease.ExpiresAt):
		item.Freshness = FreshnessStale
		facts = append(facts, consistency("expired-local-lease", lease.SubjectRef, "The latest local work lease expired; current status is unknown"))
	default:
		item.Freshness = FreshnessLocalCurrent
	}
	return projectedWork{value: item, orderTime: boundedTime(lease.RenewedAt, now), include: validWireWork(item)}, facts
}

func validLocalEvidence(e LocalLeaseEvidence) bool {
	return workreport.ValidateLease(e.Lease) == nil && !e.ObservedAt.IsZero() && validatePendingProjection(e.Lease, e.Pending)
}

func validWireWork(item Work) bool {
	if !item.Mode.Valid() || item.WorkID == "" || item.SubjectRef == "" || item.Actor.Kind == "" ||
		item.Actor.Name == "" || item.Actor.System == "" || item.Actor.Session == "" {
		return false
	}
	if item.Mode == workreport.ModeWaiting {
		if len(item.WaitingOn) == 0 {
			return false
		}
	} else if len(item.WaitingOn) != 0 {
		return false
	}
	for _, waiting := range item.WaitingOn {
		if !waiting.Kind.Valid() || waiting.ID == "" {
			return false
		}
	}
	return true
}

func validatePendingProjection(lease workreport.Lease, pending *SharedPending) bool {
	if lease.Pending == nil {
		return pending == nil
	}
	if pending == nil || pending.OperationID != lease.Pending.OperationKey || pending.Action != string(lease.Pending.Action) {
		return false
	}
	if !validKey(pending.OperationID) || !validPendingAction(pending.Action) || !validPendingStage(pending.Stage) ||
		!validPendingState(pending.State) || !validRemainingAction(pending.RemainingAction) {
		return false
	}
	return pendingCombinationValid(pending.Stage, pending.State, pending.RemainingAction)
}

func validPendingAction(value string) bool {
	switch value {
	case "start", "checkpoint", "wait", "stop":
		return true
	default:
		return false
	}
}

func validPendingStage(value string) bool {
	switch value {
	case "none", "committed", "pushed", "pr-created", "auto-merge-armed", "merged":
		return true
	default:
		return false
	}
}

func validPendingState(value string) bool {
	switch value {
	case "", "pending-merge", "already-open", "already-merged", "merged", "needs-attention", "outcome-unknown":
		return true
	default:
		return false
	}
}

func validRemainingAction(value string) bool {
	switch value {
	case "none", "retry-push", "ensure-pr", "arm-auto-merge", "wait-for-gates", "resolve-pr", "observe-provider-outcome":
		return true
	default:
		return false
	}
}

func pendingCombinationValid(stage, state, remaining string) bool {
	if state == "already-merged" || state == "merged" {
		return stage == "merged" && remaining == "none"
	}
	if stage == "merged" {
		return state == "" && remaining == "none"
	}
	switch state {
	case "already-open":
		return stage == "pr-created" && remaining == "arm-auto-merge" ||
			stage == "auto-merge-armed" && remaining == "wait-for-gates"
	case "pending-merge":
		return stage == "auto-merge-armed" && remaining == "wait-for-gates"
	case "needs-attention":
		return stage == "pr-created" && remaining == "resolve-pr"
	case "outcome-unknown":
		return remaining == "observe-provider-outcome"
	default:
		switch stage {
		case "none", "committed":
			return remaining == "retry-push"
		case "pushed":
			return remaining == "ensure-pr"
		case "pr-created":
			return remaining == "arm-auto-merge"
		case "auto-merge-armed":
			return remaining == "wait-for-gates"
		}
	}
	return false
}

func projectThread(e ThreadEvidence, work []projectedWork, facts []Consistency, now time.Time, limits Limits) (projectedRow, error) {
	protocol := e.Protocol
	protocol.WaitingOn = sortedUnique(protocol.WaitingOn)
	protocol.BlockingBy = sortedUnique(protocol.BlockingBy)
	participants := sortedUnique(e.Participants)
	if participants == nil {
		participants = []string{}
	}
	for i := range participants {
		participants[i] = safeText(participants[i], maximumKeyRunes)
	}
	if e.LatestMilestone != nil && e.LatestMilestone.At.After(now) {
		facts = append(facts, consistency("future-clock-anomaly", e.LatestMilestone.Subject, "The latest protocol milestone is dated in the future"))
	}

	sort.SliceStable(work, func(i, j int) bool {
		ri, rj := freshnessRank(work[i].value.Freshness), freshnessRank(work[j].value.Freshness)
		if ri != rj {
			return ri < rj
		}
		if !work[i].anomalous && !work[j].anomalous && !work[i].orderTime.Equal(work[j].orderTime) {
			return work[i].orderTime.After(work[j].orderTime)
		}
		ai, aj := work[i].value.Actor, work[j].value.Actor
		if ai.System != aj.System {
			return ai.System < aj.System
		}
		if ai.Name != aj.Name {
			return ai.Name < aj.Name
		}
		if ai.Session != aj.Session {
			return ai.Session < aj.Session
		}
		return work[i].value.WorkID < work[j].value.WorkID
	})
	totalWork := len(work)
	if len(work) > limits.WorkPerRow {
		work = work[:limits.WorkPerRow]
	}
	workValues := make([]Work, 0, len(work))
	for _, item := range work {
		workValues = append(workValues, item.value)
	}
	if workValues == nil {
		workValues = []Work{}
	}

	sort.Slice(facts, func(i, j int) bool {
		if facts[i].Code != facts[j].Code {
			return facts[i].Code < facts[j].Code
		}
		if facts[i].SubjectRef != facts[j].SubjectRef {
			return facts[i].SubjectRef < facts[j].SubjectRef
		}
		if facts[i].Severity != facts[j].Severity {
			return facts[i].Severity < facts[j].Severity
		}
		return facts[i].Summary < facts[j].Summary
	})
	totalFacts := len(facts)
	if len(facts) > limits.ConsistencyPerRow {
		facts = facts[:limits.ConsistencyPerRow]
	}
	if facts == nil {
		facts = []Consistency{}
	}

	row := TimelineRow{
		Space: safeText(e.Space, maximumKeyRunes), Thread: safeText(e.Thread, maximumKeyRunes),
		Title: safeText(e.Title, maximumTitleRunes), Participants: participants, Protocol: protocol,
		LatestMilestone: cloneMilestone(e.LatestMilestone), Work: workValues,
		WorkWindow:        Window{Truncated: len(workValues) < totalWork, Total: totalWork, Shown: len(workValues)},
		Consistency:       facts,
		ConsistencyWindow: Window{Truncated: len(facts) < totalFacts, Total: totalFacts, Shown: len(facts)},
	}

	bucket := 3
	orderTime := time.Time{}
	hasNormalEvidence := false
	hasAnomaly := false
	if blockingProtocol(protocol) {
		bucket = 0
		hasNormalEvidence = true
	}
	for _, item := range work {
		if item.anomalous {
			hasAnomaly = true
			continue
		}
		switch item.value.Freshness {
		case FreshnessLocalCurrent, FreshnessPendingRecovery, FreshnessCommittedCurrent:
			bucket = 0
			hasNormalEvidence = true
		case FreshnessFinished, FreshnessStale:
			if bucket > 1 {
				bucket = 1
			}
			hasNormalEvidence = true
		}
		if item.orderTime.After(orderTime) {
			orderTime = item.orderTime
		}
	}
	if e.LatestMilestone != nil {
		if e.LatestMilestone.At.After(now) {
			hasAnomaly = true
		} else {
			hasNormalEvidence = true
			if bucket > 1 {
				bucket = 1
			}
			if e.LatestMilestone.At.After(orderTime) {
				orderTime = e.LatestMilestone.At
			}
		}
	}
	if !hasNormalEvidence && hasAnomaly {
		bucket = 2
		orderTime = time.Time{}
	}
	return projectedRow{value: row, bucket: bucket, orderTime: orderTime}, nil
}

func chooseTimelineRows(rows []projectedRow, limit int) []projectedRow {
	if len(rows) <= limit {
		return rows
	}
	blocking := 0
	for _, row := range rows {
		if blockingProtocol(row.value.Protocol) {
			blocking++
		}
	}
	capacity := limit
	if blocking > capacity {
		capacity = blocking
	}
	selected := make([]projectedRow, 0, capacity)
	for _, row := range rows {
		if blockingProtocol(row.value.Protocol) {
			selected = append(selected, row)
		}
	}
	for _, row := range rows {
		if len(selected) >= capacity {
			break
		}
		if !blockingProtocol(row.value.Protocol) {
			selected = append(selected, row)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].bucket != selected[j].bucket {
			return selected[i].bucket < selected[j].bucket
		}
		if selected[i].bucket < 2 && !selected[i].orderTime.Equal(selected[j].orderTime) {
			return selected[i].orderTime.After(selected[j].orderTime)
		}
		if selected[i].value.Space != selected[j].value.Space {
			return selected[i].value.Space < selected[j].value.Space
		}
		return selected[i].value.Thread < selected[j].value.Thread
	})
	return selected
}

func checkpointRef(e CommittedWorkEvidence) *CommittedCheckpoint {
	checkpoint := &CommittedCheckpoint{ArtifactID: safeText(e.ArtifactID, maximumKeyRunes), ReportedAt: e.ReportedAt.UTC()}
	if !e.ValidUntil.IsZero() {
		value := e.ValidUntil.UTC()
		checkpoint.ValidUntil = &value
	}
	return checkpoint
}

func projectActor(actor workreport.Actor) Actor {
	return Actor{
		Kind: safeText(actor.Kind, 32), Name: safeText(actor.Name, 96), System: safeText(actor.System, 96),
		Model: safeText(actor.Model, 96), Session: publicSession(actor.Session),
	}
}

func cloneMilestone(in *Milestone) *Milestone {
	if in == nil {
		return nil
	}
	out := *in
	out.Kind = safeText(out.Kind, 64)
	out.At = out.At.UTC()
	out.Actor.Kind = safeText(out.Actor.Kind, 32)
	out.Actor.Name = safeText(out.Actor.Name, 96)
	out.Actor.System = safeText(out.Actor.System, 96)
	out.Actor.Model = safeText(out.Actor.Model, 96)
	out.Actor.Session = publicSession(out.Actor.Session)
	out.Transition = safeText(out.Transition, 64)
	out.Subject = safeText(out.Subject, maximumKeyRunes)
	return &out
}

func projectWaiting(in []workreport.WaitingOn) []workreport.WaitingOn {
	out := append([]workreport.WaitingOn(nil), in...)
	for i := range out {
		out[i].ID = safeText(out[i].ID, maximumKeyRunes)
		out[i].Summary = safeText(out[i].Summary, maximumSummaryRunes)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Summary < out[j].Summary
	})
	if out == nil {
		out = []workreport.WaitingOn{}
	}
	return out
}

func clonePending(in *SharedPending) *SharedPending {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func sameActor(left, right workreport.Actor) bool {
	return left.System == right.System && left.Name == right.Name && left.Session == right.Session
}

func committedTieKey(value CommittedWorkEvidence) string {
	return value.Actor.System + "\x00" + value.Actor.Name + "\x00" + value.Actor.Session + "\x00" +
		string(value.Mode) + "\x00" + value.ReportedAt.UTC().Format(time.RFC3339Nano) + "\x00" +
		value.ValidUntil.UTC().Format(time.RFC3339Nano) + "\x00" + value.SubjectRef + "\x00" + value.Summary + "\x00" + waitingTieKey(value.WaitingOn)
}

func leaseTieKey(value LocalLeaseEvidence) string {
	lease := value.Lease
	pendingKey := ""
	if lease.Pending != nil {
		pendingKey = lease.Pending.OperationKey
	}
	if value.Pending != nil {
		pendingKey += "\x00" + value.Pending.OperationID + "\x00" + value.Pending.Action + "\x00" + value.Pending.Stage +
			"\x00" + value.Pending.State + "\x00" + value.Pending.RemainingAction
	}
	return lease.Identity.Actor.System + "\x00" + lease.Identity.Actor.Name + "\x00" + lease.Identity.Actor.Session + "\x00" +
		string(lease.Mode) + "\x00" + lease.RenewedAt.UTC().Format(time.RFC3339Nano) + "\x00" +
		lease.ExpiresAt.UTC().Format(time.RFC3339Nano) + "\x00" + value.ObservedAt.UTC().Format(time.RFC3339Nano) + "\x00" +
		lease.SubjectRef + "\x00" + lease.Summary + "\x00" + waitingTieKey(lease.WaitingOn) + "\x00" + pendingKey
}

func waitingTieKey(values []workreport.WaitingOn) string {
	values = projectWaiting(values)
	var builder strings.Builder
	for _, value := range values {
		builder.WriteString(string(value.Kind))
		builder.WriteByte('\x00')
		builder.WriteString(value.ID)
		builder.WriteByte('\x00')
		builder.WriteString(value.Summary)
		builder.WriteByte('\x01')
	}
	return builder.String()
}

func consistency(code, subject, summary string) Consistency {
	return Consistency{Code: code, SubjectRef: safeText(subject, maximumKeyRunes), Severity: "warning", Summary: safeText(summary, maximumSummaryRunes)}
}

func freshnessRank(value Freshness) int {
	switch value {
	case FreshnessLocalCurrent:
		return 0
	case FreshnessPendingRecovery:
		return 1
	case FreshnessCommittedCurrent:
		return 2
	case FreshnessStale:
		return 3
	case FreshnessFinished:
		return 4
	default:
		return 5
	}
}

func blockingProtocol(protocol Protocol) bool {
	return protocol.OpenCount > 0 || protocol.YourMove || len(protocol.WaitingOn) > 0 || len(protocol.BlockingBy) > 0
}

func boundedTime(value, now time.Time) time.Time {
	if value.IsZero() || value.After(now) {
		return time.Time{}
	}
	return value.UTC()
}

func threadKey(space, thread string) string     { return space + "\x00" + thread }
func workKey(space, thread, work string) string { return space + "\x00" + thread + "\x00" + work }

func splitWorkKey(key string) (string, string, string) {
	parts := strings.SplitN(key, "\x00", 3)
	return parts[0], parts[1], parts[2]
}

func validKey(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximumKeyRunes {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			return false
		}
	}
	return true
}

func safeText(value string, maximum int) string {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "�")
	}
	out := make([]rune, 0, min(utf8.RuneCountInString(value), maximum))
	for _, r := range value {
		if len(out) >= maximum {
			break
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Bidi_Control) {
			out = append(out, '�')
			continue
		}
		out = append(out, r)
	}
	result := string(out)
	if containsCredentialToken(result) {
		return "[redacted unsafe text]"
	}
	return result
}

func containsCredentialToken(value string) bool {
	if sensitive.ContainsContent(value) {
		return true
	}
	if credentialAssignmentPattern.MatchString(value) || bearerCredentialPattern.MatchString(value) {
		return true
	}
	tokens := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("._:-", r)
	})
	for _, token := range tokens {
		if sensitive.Identifier(token) {
			return true
		}
	}
	return false
}

func publicSession(value string) string {
	if value == "" {
		return ""
	}
	safe := value != "" && len(value) <= 128 && !sensitive.Identifier(value)
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && !strings.ContainsRune("._:-", r) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[safeText(value, maximumKeyRunes)] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}
