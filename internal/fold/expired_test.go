package fold

import (
	"testing"
	"time"
)

// TestExpiredOverlay is spec 04 §8 AC 8 (3.4.7): `expired` is a
// fold-computed overlay from `valid_until` and a caller-supplied
// reference instant — never an event, absent from the transition enum,
// and (asserted structurally by this whole package) it never touches
// State.
func TestExpiredOverlay(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		validUntil time.Time
		reference  time.Time
		want       bool
	}{
		{"valid_until_in_the_past", base.Add(-time.Hour), base, true},
		{"valid_until_in_the_future", base.Add(time.Hour), base, false},
		{"valid_until_absent", time.Time{}, base, false},
		{"valid_until_exactly_now_is_not_yet_expired", base, base, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ExpiredOverlay(tc.validUntil, tc.reference)
			if got != tc.want {
				t.Fatalf("ExpiredOverlay(%v, %v) = %v, want %v", tc.validUntil, tc.reference, got, tc.want)
			}
		})
	}

	t.Run("never_present_in_the_transition_table", func(t *testing.T) {
		t.Parallel()
		for _, r := range rows {
			if r.Transition == "expired" {
				t.Fatalf("expired must never appear in the transition table: %+v", r)
			}
		}
	})
}
