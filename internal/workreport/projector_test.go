package workreport

import (
	"testing"
	"time"
)

func TestClassifyLeaseExpiryBoundaryAndClosing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	base := Lease{
		SchemaVersion: SchemaVersion, Identity: testIdentity(), SubjectRef: "XW-test", Mode: ModeImplementing,
		Summary: "work", StartedAt: now.Add(-time.Minute), RenewedAt: now.Add(-time.Minute), ExpiresAt: now,
		HeartbeatSequence: 1, SemanticSequence: 1,
	}
	if got := ClassifyLease(base, now); got != FreshnessLocalCurrent {
		t.Fatalf("equality boundary = %s", got)
	}
	if got := ClassifyLease(base, now.Add(time.Nanosecond)); got != FreshnessUnknown {
		t.Fatalf("expired = %s", got)
	}
	closing := base.Clone()
	closing.Closing = true
	closing.SemanticSequence = 2
	closing.Mode = ModeFinished
	closing.Pending = newPending(testPrepared(t, ActionStop, 2, "stop"))
	if got := ClassifyLease(closing, now.Add(48*time.Hour)); got != FreshnessPendingRecovery {
		t.Fatalf("closing = %s", got)
	}
	futureClosing := closing.Clone()
	futureClosing.RenewedAt = now.Add(time.Minute)
	futureClosing.ExpiresAt = futureClosing.RenewedAt.Add(DefaultTTL)
	if got := ClassifyLease(futureClosing, now); got != FreshnessUnknown {
		t.Fatalf("future closing lease = %s, want unknown", got)
	}
	if got := ClassifyLease(futureClosing, futureClosing.RenewedAt); got != FreshnessPendingRecovery {
		t.Fatalf("closing lease at clock boundary = %s, want pending-recovery", got)
	}
}

func TestClassifyCheckpointMatrix(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		checkpoint Checkpoint
		want       Freshness
	}{
		{name: "current at equality", checkpoint: Checkpoint{Mode: ModeTesting, ReportedAt: now.Add(-time.Hour), ValidUntil: now}, want: FreshnessCommittedCurrent},
		{name: "stale", checkpoint: Checkpoint{Mode: ModeTesting, ReportedAt: now.Add(-time.Hour), ValidUntil: now.Add(-time.Nanosecond)}, want: FreshnessStale},
		{name: "clock anomaly", checkpoint: Checkpoint{Mode: ModeTesting, ReportedAt: now.Add(5*time.Minute + time.Nanosecond), ValidUntil: now.Add(time.Hour)}, want: FreshnessUnknown},
		{name: "finished", checkpoint: Checkpoint{Mode: ModeFinished, ReportedAt: now}, want: FreshnessFinished},
		{name: "finished with validity", checkpoint: Checkpoint{Mode: ModeFinished, ReportedAt: now, ValidUntil: now.Add(time.Hour)}, want: FreshnessInvalid},
		{name: "window over seven days", checkpoint: Checkpoint{Mode: ModeTesting, ReportedAt: now, ValidUntil: now.Add(7*24*time.Hour + time.Nanosecond)}, want: FreshnessInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyCheckpoint(test.checkpoint, now); got != test.want {
				t.Fatalf("ClassifyCheckpoint() = %s, want %s", got, test.want)
			}
		})
	}
}
