package workreport

import "time"

type Freshness string

const (
	FreshnessLocalCurrent     Freshness = "local-current"
	FreshnessUnknown          Freshness = "unknown"
	FreshnessPendingRecovery  Freshness = "pending-recovery"
	FreshnessCommittedCurrent Freshness = "committed-current"
	FreshnessStale            Freshness = "stale"
	FreshnessFinished         Freshness = "finished"
	FreshnessInvalid          Freshness = "invalid"
)

func ClassifyLease(lease Lease, now time.Time) Freshness {
	if ValidateLease(lease) != nil {
		return FreshnessUnknown
	}
	if now.Before(lease.RenewedAt) {
		return FreshnessUnknown
	}
	if lease.Closing {
		return FreshnessPendingRecovery
	}
	if lease.Expired(now) {
		return FreshnessUnknown
	}
	return FreshnessLocalCurrent
}

type Checkpoint struct {
	Mode       Mode
	ReportedAt time.Time
	ValidUntil time.Time
}

func ClassifyCheckpoint(checkpoint Checkpoint, now time.Time) Freshness {
	if !checkpoint.Mode.Valid() || checkpoint.ReportedAt.IsZero() {
		return FreshnessInvalid
	}
	if checkpoint.Mode == ModeFinished {
		if !checkpoint.ValidUntil.IsZero() {
			return FreshnessInvalid
		}
		return FreshnessFinished
	}
	if checkpoint.ValidUntil.IsZero() || !checkpoint.ValidUntil.After(checkpoint.ReportedAt) ||
		checkpoint.ValidUntil.Sub(checkpoint.ReportedAt) > 7*24*time.Hour {
		return FreshnessInvalid
	}
	if checkpoint.ReportedAt.After(now.Add(5 * time.Minute)) {
		return FreshnessUnknown
	}
	if now.After(checkpoint.ValidUntil) {
		return FreshnessStale
	}
	return FreshnessCommittedCurrent
}
