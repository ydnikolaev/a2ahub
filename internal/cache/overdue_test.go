package cache

import (
	"testing"
	"time"
)

// TestOverdueAtIsDayGranular pins the rule and the deliberate difference
// from the sender-side check in outbox.go.
//
// `needed_by` is a bare date with no time zone. outbox.go's
// `needed-by-passed` compares against midnight UTC, so it calls an item late
// as soon as the named day BEGINS in UTC — up to a day early for a reader
// west of it. This side treats the deadline as expiring at the END of the
// named day, which is what an author writing "2026-08-20" means.
//
// The two are therefore not identical, and that is recorded rather than
// quietly unified: changing outbox.go's rule would move a signal two people
// are already reading, and this test exists so the next person finds the
// difference stated instead of discovering it.
func TestOverdueAtIsDayGranular(t *testing.T) {
	t.Parallel()

	due := "2026-08-20"
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"the day before", time.Date(2026, 8, 19, 23, 59, 0, 0, time.UTC), false},
		{"the moment the due day starts", time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), false},
		{"late on the due day is still on time", time.Date(2026, 8, 20, 23, 59, 59, 0, time.UTC), false},
		{"the day after", time.Date(2026, 8, 21, 0, 0, 1, 0, time.UTC), true},
		{"long after", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := overdueAt(due, tc.now); got != tc.want {
				t.Errorf("overdueAt(%q, %s) = %v, want %v", due, tc.now, got, tc.want)
			}
		})
	}
}

// TestOverdueAtNeverGuesses: no deadline and an unparseable deadline both
// mean "not overdue". A malformed date states nothing, and a read verb that
// refused to answer because a COUNTERPARTY wrote a bad date would reproduce
// the silence this whole query exists to remove.
func TestOverdueAtNeverGuesses(t *testing.T) {
	t.Parallel()
	now := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, bad := range []string{"", "not-a-date", "2026-13-45", "20/08/2026", "<YYYY-MM-DD>"} {
		if overdueAt(bad, now) {
			t.Errorf("overdueAt(%q) = true, want false — a deadline that states nothing is not a missed one", bad)
		}
	}
}
