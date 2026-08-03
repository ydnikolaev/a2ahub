package workreport

import "time"

// Freshness is part of the public package API.
type Freshness string

const (
	// FreshnessLocalCurrent is part of the public package API.
	FreshnessLocalCurrent Freshness = "local-current"
	// FreshnessUnknown is part of the public package API.
	FreshnessUnknown Freshness = "unknown"
	// FreshnessPendingRecovery is part of the public package API.
	FreshnessPendingRecovery Freshness = "pending-recovery"
	// FreshnessCommittedCurrent is part of the public package API.
	FreshnessCommittedCurrent Freshness = "committed-current"
	// FreshnessStale is part of the public package API.
	FreshnessStale Freshness = "stale"
	// FreshnessFinished is part of the public package API.
	FreshnessFinished Freshness = "finished"
	// FreshnessInvalid is part of the public package API.
	FreshnessInvalid Freshness = "invalid"
)

// ClassifyLease is part of the public package API.
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

// Checkpoint is part of the public package API.
type Checkpoint struct {
	Mode       Mode
	ReportedAt time.Time
	ValidUntil time.Time
}

// ClassifyCheckpoint is part of the public package API.
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
