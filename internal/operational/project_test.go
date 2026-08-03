package operational

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestBuildMergesCommittedAndLocalEvidenceWithoutCollapsingActors(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	input := baseInput(now)
	input.CommittedWork = []CommittedWorkEvidence{
		checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "session-a", workreport.ModeTesting, now.Add(-2*time.Minute), now.Add(time.Hour), 1),
		checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAW", "bob", "session-b", workreport.ModeWaiting, now.Add(-time.Minute), now.Add(time.Hour), 2),
	}
	input.CommittedWork[1].WaitingOn = []workreport.WaitingOn{{Kind: workreport.WaitSystem, ID: "checkout", Summary: "review"}}
	input.LocalLeases = []LocalLeaseEvidence{{Lease: validLease("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "session-a", now), ObservedAt: now}}

	snapshot, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	work := snapshot.Timeline[0].Work
	if len(work) != 2 {
		t.Fatalf("work count = %d, want 2", len(work))
	}
	if work[0].Freshness != FreshnessLocalCurrent || work[0].Actor.Name != "alice" || work[0].CommittedCheckpoint == nil {
		t.Fatalf("fresh local merge = %#v", work[0])
	}
	if work[1].Freshness != FreshnessCommittedCurrent || work[1].Actor.Name != "bob" {
		t.Fatalf("independent committed actor = %#v", work[1])
	}
	if !work[0].Current || !work[1].Current {
		t.Fatalf("current classification not projected by core: %#v", work)
	}
	if snapshot.Timeline[0].Protocol.Settled || snapshot.Timeline[0].Protocol.OpenCount != 1 {
		t.Fatalf("work changed protocol = %#v", snapshot.Timeline[0].Protocol)
	}
}

func TestBuildFreshnessExpiryAndFinishedOrphanBoundaries(t *testing.T) {
	t.Parallel()
	boundary := time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC)
	input := baseInput(boundary)
	input.CommittedWork = []CommittedWorkEvidence{checkpoint(
		"work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "session-a", workreport.ModeTesting,
		boundary.Add(-time.Hour), boundary, 1,
	)}

	atBoundary, err := Build(input, fixedClock{boundary}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(at boundary) error = %v", err)
	}
	if got := atBoundary.Timeline[0].Work[0].Freshness; got != FreshnessCommittedCurrent {
		t.Fatalf("freshness at exact expiry = %q", got)
	}
	if !atBoundary.Timeline[0].Work[0].Current {
		t.Fatal("committed-current work is not current")
	}
	after, err := Build(input, fixedClock{boundary.Add(time.Nanosecond)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(after boundary) error = %v", err)
	}
	if got := after.Timeline[0].Work[0].Freshness; got != FreshnessStale {
		t.Fatalf("freshness after expiry = %q", got)
	}
	if after.Timeline[0].Work[0].Current {
		t.Fatal("stale work is current")
	}
	if atBoundary.Revision == after.Revision {
		t.Fatal("expiry transition did not change semantic revision")
	}

	finished := input
	finished.CommittedWork = []CommittedWorkEvidence{checkpoint(
		"work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "session-a", workreport.ModeFinished,
		boundary.Add(-time.Minute), time.Time{}, 3,
	)}
	finished.LocalLeases = []LocalLeaseEvidence{{Lease: validLease("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "session-a", boundary), ObservedAt: boundary}}
	snapshot, err := Build(finished, fixedClock{boundary}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(finished) error = %v", err)
	}
	if snapshot.Timeline[0].Work[0].Freshness != FreshnessFinished {
		t.Fatalf("orphan lease resurrected finished work: %#v", snapshot.Timeline[0].Work[0])
	}
	if snapshot.Timeline[0].Work[0].Current {
		t.Fatal("finished work is current")
	}
	if !containsConsistency(snapshot.Timeline[0], "orphan-lease-finished") {
		t.Fatalf("missing orphan consistency fact: %#v", snapshot.Timeline[0].Consistency)
	}
}

func TestBuildFutureAnomalyUsesStableBucketAndTransitionsOnce(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	future := base.Add(time.Hour)
	input := Input{Threads: []ThreadEvidence{
		thread("space-z", "thread:future", false),
		thread("space-a", "thread:stale", false),
	}}
	input.CommittedWork = []CommittedWorkEvidence{
		withThread(checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "future", "session-f", workreport.ModeTesting, future, future.Add(time.Hour), 1), "space-z", "thread:future"),
		withThread(checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAW", "stale", "session-s", workreport.ModeTesting, base.Add(-2*time.Hour), base.Add(-time.Hour), 1), "space-a", "thread:stale"),
	}

	first, err := Build(input, fixedClock{base}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(first) error = %v", err)
	}
	second, err := Build(input, fixedClock{base.Add(30 * time.Minute)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(second) error = %v", err)
	}
	if first.Revision != second.Revision {
		t.Fatalf("pre-boundary poll churned revision: %s != %s", first.Revision, second.Revision)
	}
	if first.Timeline[0].Thread != "thread:stale" || first.Timeline[1].Thread != "thread:future" {
		t.Fatalf("future timestamp outranked bounded evidence: %#v", first.Timeline)
	}
	if first.Timeline[1].Work[0].Freshness != FreshnessUnknown {
		t.Fatalf("future checkpoint freshness = %q", first.Timeline[1].Work[0].Freshness)
	}

	atBoundary, err := Build(input, fixedClock{future}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(boundary) error = %v", err)
	}
	if atBoundary.Revision == first.Revision || atBoundary.Timeline[0].Thread != "thread:future" {
		t.Fatalf("anomaly boundary did not transition once: %#v", atBoundary.Timeline)
	}
	after, err := Build(input, fixedClock{future.Add(time.Minute)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(after) error = %v", err)
	}
	if after.Revision != atBoundary.Revision {
		t.Fatalf("post-boundary poll churned revision: %s != %s", after.Revision, atBoundary.Revision)
	}
}

func TestBuildDeterministicAcrossInputPermutationsAndObservationPolls(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	input := Input{
		Sources: []SourceEvidence{
			{Kind: SourceLocalWork, Revision: "sha256:leases", ObservedAt: now, Freshness: SourceCurrent},
			{Kind: SourceSpace, Space: "space-b", Revision: "abc", SyncedAt: ptrTime(now.Add(-time.Hour)), ObservedAt: now, Freshness: SourceCurrent},
		},
		Threads: []ThreadEvidence{thread("space-b", "thread:b", false), thread("space-a", "thread:a", false)},
		CommittedWork: []CommittedWorkEvidence{
			withThread(checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "s-a", workreport.ModeTesting, now.Add(-time.Minute), now.Add(time.Hour), 1), "space-b", "thread:b"),
			withThread(checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAW", "bob", "s-b", workreport.ModeTesting, now.Add(-2*time.Minute), now.Add(time.Hour), 1), "space-a", "thread:a"),
		},
		Unavailable: []Unavailable{{SourceKind: SourceSpace, Space: "space-z", Code: "mirror-missing", Summary: "missing"}},
	}
	left, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(left) error = %v", err)
	}
	slices.Reverse(input.Sources)
	slices.Reverse(input.Threads)
	slices.Reverse(input.CommittedWork)
	right, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(right) error = %v", err)
	}
	leftJSON, _ := CanonicalJSON(left)
	rightJSON, _ := CanonicalJSON(right)
	if string(leftJSON) != string(rightJSON) {
		t.Fatalf("permutation changed snapshot\nleft:  %s\nright: %s", leftJSON, rightJSON)
	}

	input.Sources[1].ObservedAt = now.Add(time.Minute)
	poll, err := Build(input, fixedClock{now.Add(time.Minute)}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build(poll) error = %v", err)
	}
	if poll.Revision != right.Revision {
		t.Fatalf("generated/observed poll changed revision: %s != %s", poll.Revision, right.Revision)
	}
}

func TestBuildPendingProjectionAndHostileTextRemainBoundedData(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	lease := pendingLease(t, now, false)
	lease.Summary = "</script><img src=x onerror=alert(1)> javascript: CSS url(https://evil)\u202e"
	input := baseInput(now)
	input.LocalLeases = []LocalLeaseEvidence{{
		Lease: lease, ObservedAt: now,
		Pending: &SharedPending{OperationID: lease.Pending.OperationKey, Action: "checkpoint", Stage: "pushed", State: "outcome-unknown", RemainingAction: "observe-provider-outcome"},
	}}
	snapshot, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	item := snapshot.Timeline[0].Work[0]
	if item.Freshness != FreshnessLocalCurrent || item.SharedPending == nil || item.SharedPending.Stage != "pushed" {
		t.Fatalf("pending projection = %#v", item)
	}
	if !item.Current {
		t.Fatal("local-current work is not current")
	}
	if strings.Contains(item.Summary, "\u202e") || !strings.Contains(item.Summary, "</script>") {
		t.Fatalf("hostile text was interpreted or bidi control retained: %q", item.Summary)
	}
	if item.Actor.Session == lease.Identity.Actor.Session || !strings.HasPrefix(item.Actor.Session, "sha256:") {
		t.Fatalf("unsafe session leaked: %q", item.Actor.Session)
	}
	raw, err := CanonicalJSON(snapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if strings.Contains(string(raw), "</script>") || strings.Contains(string(raw), "secret-prepared") || strings.Contains(string(raw), "password:") {
		t.Fatalf("encoded snapshot contains breakout or private journal/session: %s", raw)
	}
}

func TestBuildClosingLeaseIsPendingRecoveryNotPresence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	lease := pendingLease(t, now.Add(-48*time.Hour), true)
	input := baseInput(now)
	input.LocalLeases = []LocalLeaseEvidence{{
		Lease: lease, ObservedAt: now,
		Pending: &SharedPending{OperationID: lease.Pending.OperationKey, Action: "stop", Stage: "pr-created", State: "needs-attention", RemainingAction: "resolve-pr"},
	}}
	snapshot, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := snapshot.Timeline[0].Work[0].Freshness; got != FreshnessPendingRecovery {
		t.Fatalf("closing lease freshness = %q", got)
	}
	if snapshot.Timeline[0].Work[0].Current {
		t.Fatal("pending-recovery work must not claim current execution")
	}
}

func TestBuildRedactsCredentialShapedLocalSummary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	lease := validLease("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "safe-session", now)
	lease.Summary = "testing with ghp_not-a-real-token"
	input := baseInput(now)
	input.LocalLeases = []LocalLeaseEvidence{{Lease: lease, ObservedAt: now}}
	snapshot, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := snapshot.Timeline[0].Work[0].Summary; got != "[redacted unsafe text]" {
		t.Fatalf("credential-shaped summary = %q", got)
	}
}

func TestCredentialRedactionCoversAssignmentsAndAuthorizationHeaders(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"password=correct-horse", "PASSWORD : secret", "Authorization: Bearer abc.def",
		"Bearer abc.def", "api_key=abcd", "token = abcd", "xoxb-not-real",
	} {
		if got := safeText(value, maximumSummaryRunes); got != "[redacted unsafe text]" {
			t.Fatalf("safeText(%q) = %q", value, got)
		}
	}
	for _, value := range []string{"risk-score", "bearer-authentication design", "tokenization tests"} {
		if got := safeText(value, maximumSummaryRunes); got != value {
			t.Fatalf("safeText(%q) false-positive = %q", value, got)
		}
	}
}

func TestPublicSessionHashesCanonicalCredentialShapes(t *testing.T) {
	t.Parallel()
	values := []string{
		"gho_" + strings.Repeat("a", 36),
		"ghu_" + strings.Repeat("a", 36),
		"xoxr-" + strings.Repeat("1", 12),
		"AIza" + strings.Repeat("A", 35),
		"sk_live_" + strings.Repeat("1", 24),
		"AKIAIOSFODNN7EXAMPLE",
		"eyJabc.eyJdef.signature",
	}
	for _, value := range values {
		got := publicSession(value)
		if !strings.HasPrefix(got, "sha256:") || got == value {
			t.Fatalf("publicSession(%q) = %q, want hash-only evidence", value, got)
		}
	}
	if got := publicSession("session-1"); got != "session-1" {
		t.Fatalf("safe session = %q", got)
	}
}

func TestBuildCorruptHigherSequenceLeaseCannotHideLatestValidLease(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	valid := validLease("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "safe-session", now)
	if err := workreport.ValidateLease(valid); err != nil {
		t.Fatalf("test valid lease is invalid: %v", err)
	}
	corrupt := valid.Clone()
	corrupt.HeartbeatSequence = 999
	corrupt.ExpiresAt = corrupt.RenewedAt.Add(-time.Second)
	input := baseInput(now)
	input.LocalLeases = []LocalLeaseEvidence{{Lease: corrupt, ObservedAt: now}, {Lease: valid, ObservedAt: now}}
	snapshot, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	row := snapshot.Timeline[0]
	if len(row.Work) != 1 || row.Work[0].Freshness != FreshnessLocalCurrent || row.Work[0].ReportedAt == nil || !row.Work[0].ReportedAt.Equal(valid.RenewedAt) {
		t.Fatalf("valid lease was hidden: %#v", row.Work)
	}
	if !containsConsistency(row, "invalid-local-lease") {
		t.Fatalf("ignored corruption was not surfaced: %#v", row.Consistency)
	}
}

func TestBuildRejectsCorruptModeFromClosedWireAndKeepsWarning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	input := baseInput(now)
	corrupt := checkpoint("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "safe-session", workreport.Mode("executing"), now.Add(-time.Minute), now.Add(time.Hour), 1)
	input.CommittedWork = []CommittedWorkEvidence{corrupt}
	snapshot, err := Build(input, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	row := snapshot.Timeline[0]
	if len(row.Work) != 0 || !containsConsistency(row, "invalid-checkpoint") {
		t.Fatalf("corrupt closed enum reached wire or warning was lost: %#v", row)
	}
}

func TestBuildLimitsAreExplicitAndNeverDropBlockingRows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	input := Input{Threads: []ThreadEvidence{
		thread("space-a", "thread:optional", false),
		thread("space-b", "thread:blocking-1", true),
		thread("space-c", "thread:blocking-2", true),
	}}
	limits := DefaultLimits()
	limits.TimelineRows = 1
	snapshot, err := Build(input, fixedClock{now}, limits)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(snapshot.Timeline) != 2 || !snapshot.TimelineWindow.Truncated || snapshot.TimelineWindow.Total != 3 || snapshot.TimelineWindow.Shown != 2 {
		t.Fatalf("timeline bounds = %#v rows=%d", snapshot.TimelineWindow, len(snapshot.Timeline))
	}
	for _, row := range snapshot.Timeline {
		if !blockingProtocol(row.Protocol) {
			t.Fatalf("optional row displaced blocker: %#v", row)
		}
	}

	limits = DefaultLimits()
	limits.EncodedSnapshot = 64
	_, err = Build(baseInput(now), fixedClock{now}, limits)
	if !errors.Is(err, ErrBoundedResult) {
		t.Fatalf("encoded bound error = %v", err)
	}
}

func TestBuildRefusesBlockingOnlyHardOverflow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	input := Input{Threads: make([]ThreadEvidence, 0, MaximumTimelineRows+1)}
	for i := 0; i <= MaximumTimelineRows; i++ {
		input.Threads = append(input.Threads, thread("space-a", fmt.Sprintf("thread:%04d", i), true))
	}
	_, err := Build(input, fixedClock{now}, DefaultLimits())
	var bounded *BoundedResultError
	if !errors.As(err, &bounded) || bounded.Boundary != "blocking timeline rows" || bounded.Count != MaximumTimelineRows+1 {
		t.Fatalf("blocking overflow error = %#v (%v)", bounded, err)
	}
}

func TestBuildSerializesAllArraysAsNonNull(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	snapshot, err := Build(Input{}, fixedClock{now}, DefaultLimits())
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	raw, err := CanonicalJSON(snapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	for _, want := range []string{`"sources":[]`, `"timeline":[]`, `"unavailable":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("JSON lacks %s: %s", want, raw)
		}
	}
}

func TestPendingProjectionUsesExactP4ClosedCrossProduct(t *testing.T) {
	t.Parallel()
	valid := []SharedPending{
		{Stage: "none", State: "", RemainingAction: "retry-push"},
		{Stage: "committed", State: "", RemainingAction: "retry-push"},
		{Stage: "pushed", State: "", RemainingAction: "ensure-pr"},
		{Stage: "pr-created", State: "already-open", RemainingAction: "arm-auto-merge"},
		{Stage: "auto-merge-armed", State: "already-open", RemainingAction: "wait-for-gates"},
		{Stage: "auto-merge-armed", State: "pending-merge", RemainingAction: "wait-for-gates"},
		{Stage: "pr-created", State: "needs-attention", RemainingAction: "resolve-pr"},
		{Stage: "pushed", State: "outcome-unknown", RemainingAction: "observe-provider-outcome"},
		{Stage: "merged", State: "", RemainingAction: "none"},
		{Stage: "merged", State: "merged", RemainingAction: "none"},
		{Stage: "merged", State: "already-merged", RemainingAction: "none"},
	}
	for _, item := range valid {
		if !validPendingStage(item.Stage) || !validPendingState(item.State) || !validRemainingAction(item.RemainingAction) ||
			!pendingCombinationValid(item.Stage, item.State, item.RemainingAction) {
			t.Fatalf("valid P4 outcome rejected: %#v", item)
		}
	}
	invalid := []SharedPending{
		{Stage: "merged", State: "pending-merge", RemainingAction: "wait-for-gates"},
		{Stage: "auto-merge-armed", State: "needs-attention", RemainingAction: "resolve-pr"},
		{Stage: "pushed", State: "", RemainingAction: "retry-push"},
		{Stage: "pr-created", State: "already-open", RemainingAction: "wait-for-gates"},
	}
	for _, item := range invalid {
		if pendingCombinationValid(item.Stage, item.State, item.RemainingAction) {
			t.Fatalf("invalid P4 outcome accepted: %#v", item)
		}
	}
}

func baseInput(now time.Time) Input {
	return Input{
		Sources: []SourceEvidence{{Kind: SourceSpace, Space: "space-a", Revision: "abc123", SyncedAt: ptrTime(now.Add(-time.Minute)), ObservedAt: now, Freshness: SourceCurrent}},
		Threads: []ThreadEvidence{thread("space-a", "thread:one", true)},
	}
}

func thread(space, id string, blocking bool) ThreadEvidence {
	protocol := Protocol{Settled: !blocking, WaitingOn: []string{}, BlockingBy: []string{}}
	if blocking {
		protocol.OpenCount = 1
		protocol.WaitingOn = []string{"atlas"}
	}
	return ThreadEvidence{Space: space, Thread: id, Title: "A process", Participants: []string{"atlas", "checkout"}, Protocol: protocol}
}

func checkpoint(workID, name, session string, mode workreport.Mode, reportedAt, validUntil time.Time, sequence uint64) CommittedWorkEvidence {
	return CommittedWorkEvidence{
		Space: "space-a", Thread: "thread:one", WorkID: workID, SubjectRef: "XC-contract@2.0.0", Mode: mode,
		Summary: "Running tests", Actor: workreport.Actor{Kind: "agent", Name: name, System: "atlas", Model: "gpt", Session: session},
		WaitingOn: []workreport.WaitingOn{}, ReportedAt: reportedAt, ValidUntil: validUntil,
		ArtifactID: "XA-atlas-20260803-abc1", CommitSequence: sequence,
	}
}

func withThread(e CommittedWorkEvidence, space, thread string) CommittedWorkEvidence {
	e.Space, e.Thread = space, thread
	return e
}

func validLease(workID, name, session string, now time.Time) workreport.Lease {
	return workreport.Lease{
		SchemaVersion: workreport.SchemaVersion,
		Identity: workreport.Identity{
			LeaseKey:  strings.Repeat("sha256:0", 0) + "sha256:" + strings.Repeat("a", 64),
			ProjectID: "sha256:" + strings.Repeat("b", 64), Space: "space-a", Thread: "thread:one", WorkID: workID,
			Actor: workreport.Actor{Kind: "agent", Name: name, System: "atlas", Model: "gpt", Session: session},
		},
		SubjectRef: "XC-contract@2.0.0", Mode: workreport.ModeTesting, Summary: "Running tests",
		WaitingOn: []workreport.WaitingOn{}, Recipients: []string{"all"}, Classification: workreport.DefaultClassification,
		StartedAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Minute), ExpiresAt: now.Add(14 * time.Minute),
		HeartbeatSequence: 2, SemanticSequence: 1,
	}
}

func pendingLease(t *testing.T, now time.Time, closing bool) workreport.Lease {
	t.Helper()
	lease := validLease("work:01ARZ3NDEKTSV4RRFFQ69G5FAV", "alice", "password:unsafe-session", now)
	lease.StartedAt = now
	lease.RenewedAt = now
	lease.ExpiresAt = now.Add(workreport.DefaultTTL)
	lease.SemanticSequence = 2
	action := workreport.ActionCheckpoint
	target := workreport.TargetActive
	if closing {
		action = workreport.ActionStop
		target = workreport.TargetClosing
		lease.Closing = true
		lease.Mode = workreport.ModePaused
	}
	key, err := operation.Work(lease.Identity.WorkID, 2, string(action))
	if err != nil {
		t.Fatalf("operation.Work() error = %v", err)
	}
	prepared, err := workreport.NewPreparedJournal([]byte(`{"secret":"secret-prepared"}`))
	if err != nil {
		t.Fatalf("NewPreparedJournal() error = %v", err)
	}
	lease.Pending = &workreport.PendingOperation{
		OperationKey: key, Action: action, ArtifactID: "XA-atlas-20260803-abc1", EventID: "01K1A2B3C4D5E6F7G8H9J0K1M2",
		SemanticSequence: 2, Prepared: prepared, LocalTarget: target,
	}
	return lease
}

func containsConsistency(row TimelineRow, code string) bool {
	for _, fact := range row.Consistency {
		if fact.Code == code {
			return true
		}
	}
	return false
}

func ptrTime(value time.Time) *time.Time { return &value }
