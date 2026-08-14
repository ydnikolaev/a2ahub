package cache

import (
	"reflect"
	"testing"
)

func TestDependencyDrifts(t *testing.T) {
	t.Parallel()
	want := []string{
		DependencyDriftCurrent,
		DependencyDriftBehind,
		DependencyDriftDeprecated,
		DependencyDriftRetired,
		DependencyDriftMissing,
		DependencyDriftDangling,
	}
	assertFreshStrings(t, "DependencyDrifts", DependencyDrifts, want)
}

func TestOperationalStates(t *testing.T) {
	t.Parallel()
	want := []string{
		OperationalStateReady,
		OperationalStateAbsent,
		OperationalStateUndeclared,
	}
	assertFreshStrings(t, "OperationalStates", OperationalStates, want)
}

func assertFreshStrings(t *testing.T, name string, values func() []string, want []string) {
	t.Helper()
	first := values()
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("%s() = %v, want %v", name, first, want)
	}
	if len(first) == 0 {
		t.Fatalf("%s() returned no values", name)
	}
	first[0] = first[len(first)-1]
	if got := values(); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s() after caller mutation = %v, want fresh %v", name, got, want)
	}
}
