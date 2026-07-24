package livee2e

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestClassifyStatus(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	cases := []struct {
		name         string
		status       int
		transportErr error
		want         Outcome
	}{
		{"transport error wins over any status", 200, boom, OutcomeUnknown},
		{"transport error with zero status", 0, boom, OutcomeUnknown},
		{"transport error with a 404", 404, boom, OutcomeUnknown},

		{"200 ok", 200, nil, OutcomeOK},
		{"201 created", 201, nil, OutcomeOK},
		{"299 boundary ok", 299, nil, OutcomeOK},

		{"zero status is not ok", 0, nil, OutcomeFatal},
		{"100 continue is not ok", 100, nil, OutcomeFatal},
		{"199 boundary is not ok", 199, nil, OutcomeFatal},
		{"300 redirect is not ok", 300, nil, OutcomeFatal},
		{"302 redirect is not ok", 302, nil, OutcomeFatal},

		// The exact spec §T6-d cases: a 5xx mid-write is UNKNOWN, never fail.
		{"504 on OpenPR — the PR may have been created", 504, nil, OutcomeUnknown},
		{"500 on git push — the push may not have landed", 500, nil, OutcomeUnknown},
		{"599 boundary is unknown", 599, nil, OutcomeUnknown},
		{"500 boundary is unknown", 500, nil, OutcomeUnknown},

		{"429 rate limit is unknown", 429, nil, OutcomeUnknown},

		// 403 cannot be told apart from a secondary rate limit by status
		// alone, so it is fatal (see ClassifyStatus's doc comment for the
		// header-based escape hatch a caller can apply BEFORE calling in).
		{"403 forbidden is fatal", 403, nil, OutcomeFatal},

		{"404 not found is fatal", 404, nil, OutcomeFatal},
		{"422 validation is fatal", 422, nil, OutcomeFatal},
		{"401 unauthorized is fatal", 401, nil, OutcomeFatal},
		{"499 boundary is fatal", 499, nil, OutcomeFatal},
		{"600 out of range is fatal", 600, nil, OutcomeFatal},
		{"negative status is fatal", -1, nil, OutcomeFatal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyStatus(tc.status, tc.transportErr)
			if got != tc.want {
				t.Fatalf("ClassifyStatus(%d, %v) = %s, want %s", tc.status, tc.transportErr, got, tc.want)
			}
		})
	}
}

// The zero value must be the most pessimistic member, the same property
// verdict.go relies on. An Outcome nobody set means "we do not know what
// happened", which costs a caller one re-read; the alternative — a
// zero-valued OutcomeOK — would let an unclassified attempt read as an
// accepted write, which is the §T6-d failure mode in miniature.
//
// Written as its own test rather than left to the iota's ordering, because
// the ordering is exactly what a later edit can change without noticing.
func TestOutcomeZeroValueIsUnknown(t *testing.T) {
	t.Parallel()
	var zero Outcome
	if zero != OutcomeUnknown {
		t.Fatalf("zero Outcome = %v, want OutcomeUnknown — an unset outcome must never read as an accepted write", zero)
	}
	if zero.String() != "unknown" {
		t.Errorf("zero Outcome renders as %q, want %q", zero.String(), "unknown")
	}
}

func TestOutcomeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		o    Outcome
		want string
	}{
		{OutcomeOK, "ok"},
		{OutcomeUnknown, "unknown"},
		{OutcomeFatal, "fatal"},
		{Outcome(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("Outcome(%d).String() = %q, want %q", tc.o, got, tc.want)
		}
	}
}

// TestRetryDelaySchedule pins the exact documented schedule so the doc
// comment's worked table cannot silently drift from the code (a sibling
// agent budgets the operator's worst-case wall time off this doc).
func TestRetryDelaySchedule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 8 * time.Second},
		{5, 8 * time.Second},
		// attempt <= 0 clamps to the first attempt's delay, never zero and
		// never negative — a mis-indexed caller gets the smallest real
		// delay, not a hot loop.
		{0, 2 * time.Second},
		{-1, 2 * time.Second},
	}
	for _, tc := range cases {
		if got := RetryDelay(tc.attempt); got != tc.want {
			t.Errorf("RetryDelay(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

// TestRetryDelayMonotonicAndBounded proves the schedule never decreases and
// never exceeds the documented cap, across a wide attempt range — including
// the values that would overflow a naive `base << attempt` shift.
func TestRetryDelayMonotonicAndBounded(t *testing.T) {
	t.Parallel()

	const cap8s = 8 * time.Second
	prev := time.Duration(0)
	for attempt := 1; attempt <= 200; attempt++ {
		d := RetryDelay(attempt)
		if d <= 0 {
			t.Fatalf("RetryDelay(%d) = %s, want a positive duration", attempt, d)
		}
		if d > cap8s {
			t.Fatalf("RetryDelay(%d) = %s, exceeds the documented cap %s", attempt, d, cap8s)
		}
		if d < prev {
			t.Fatalf("RetryDelay(%d) = %s, decreased from RetryDelay(%d) = %s — schedule must be monotonic", attempt, d, attempt-1, prev)
		}
		prev = d
	}

	for _, huge := range []int{1000, math.MaxInt32, math.MaxInt} {
		d := RetryDelay(huge)
		if d <= 0 {
			t.Fatalf("RetryDelay(%d) = %s, want a positive duration (overflow guard)", huge, d)
		}
		if d != cap8s {
			t.Fatalf("RetryDelay(%d) = %s, want the cap %s", huge, d, cap8s)
		}
	}
}

// TestRetryDelayWorstCaseWallTime pins the total wall time a caller retrying
// to exhaustion (MaxAttempts) spends sleeping — the number the RetryDelay doc
// comment states an operator budgets against.
func TestRetryDelayWorstCaseWallTime(t *testing.T) {
	t.Parallel()

	var total time.Duration
	for attempt := 1; attempt < MaxAttempts; attempt++ {
		total += RetryDelay(attempt)
	}
	want := 2*time.Second + 4*time.Second + 8*time.Second
	if total != want {
		t.Fatalf("worst-case wall time across MaxAttempts=%d = %s, want %s (2s+4s+8s, per the doc comment)", MaxAttempts, total, want)
	}
}

func TestErrUnknownOutcomeIsASentinel(t *testing.T) {
	t.Parallel()
	if ErrUnknownOutcome == nil {
		t.Fatal("ErrUnknownOutcome must not be nil")
	}
	wrapped := errors.New("retry loop: " + ErrUnknownOutcome.Error())
	if errors.Is(wrapped, ErrUnknownOutcome) {
		t.Fatal("errors.New does not wrap — this assertion is here to document that callers must use fmt.Errorf(\"%w\", ...) to keep errors.Is working")
	}
}
