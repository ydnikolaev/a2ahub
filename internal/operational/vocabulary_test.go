package operational

import (
	"reflect"
	"testing"
)

func TestFreshnessValues(t *testing.T) {
	t.Parallel()
	want := []Freshness{
		FreshnessLocalCurrent,
		FreshnessCommittedCurrent,
		FreshnessStale,
		FreshnessFinished,
		FreshnessPendingRecovery,
		FreshnessUnknown,
	}
	assertFreshValues(t, "FreshnessValues", FreshnessValues, want)
}

func TestSourceFreshnessValues(t *testing.T) {
	t.Parallel()
	want := []SourceFreshness{
		SourceCurrent,
		SourceStale,
		SourceUnavailable,
		SourceDegraded,
	}
	assertFreshValues(t, "SourceFreshnessValues", SourceFreshnessValues, want)
}

func TestConsistencySeverities(t *testing.T) {
	t.Parallel()
	assertFreshValues(t, "ConsistencySeverities", ConsistencySeverities, []ConsistencySeverity{ConsistencyWarning})
}

func assertFreshValues[T comparable](t *testing.T, name string, values func() []T, want []T) {
	t.Helper()
	first := values()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("%s() = %v, want %v", name, first, want)
	}
	if len(first) == 0 {
		t.Fatalf("%s() returned no values", name)
	}
	var replacement T
	if len(first) > 1 {
		replacement = first[1]
	}
	first[0] = replacement
	if reflect.DeepEqual(first, want) {
		t.Fatalf("%s() freshness mutation did not alter the returned slice", name)
	}
	if got := values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s() after caller mutation = %v, want fresh %v", name, got, want)
	}
}
