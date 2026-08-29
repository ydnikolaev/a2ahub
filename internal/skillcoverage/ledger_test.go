package skillcoverage

import (
	"reflect"
	"testing"
)

// --- local fixture types, mirroring reflect_test.go's own precedent: never
// the real domain types, so this file keeps asserting the REGISTRY's own
// behaviour rather than re-describing a real surface every time one of its
// fields changes. ---

type ledgerFixtureA struct {
	Alpha string `json:"alpha"`
}

type ledgerFixtureB struct {
	Beta string `json:"beta"`
}

// resetRegistry restores the package-level registry to empty around a test,
// so tests in this file never see each other's Register() calls — the
// registry is process-lifetime by design (see ledger.go's own doc comment)
// but a test binary is one process running many tests.
func resetRegistry(t *testing.T) {
	t.Helper()
	saved := registry
	registry = map[string]reflect.Type{}
	t.Cleanup(func() { registry = saved })
}

func TestRegisterAndRegistered(t *testing.T) {
	resetRegistry(t)
	Register("fixture-a", reflect.TypeOf(ledgerFixtureA{}))
	Register("fixture-b", reflect.TypeOf(ledgerFixtureB{}))

	got := Registered()
	if len(got) != 2 {
		t.Fatalf("Registered() = %v, want exactly 2 entries", got)
	}
	if want := []string{"alpha"}; !reflect.DeepEqual(got["fixture-a"], want) {
		t.Fatalf("Registered()[\"fixture-a\"] = %v, want %v", got["fixture-a"], want)
	}
	if want := []string{"beta"}; !reflect.DeepEqual(got["fixture-b"], want) {
		t.Fatalf("Registered()[\"fixture-b\"] = %v, want %v", got["fixture-b"], want)
	}
}

func TestRegisteredReturnsAFreshCopy(t *testing.T) {
	resetRegistry(t)
	Register("fixture-a", reflect.TypeOf(ledgerFixtureA{}))

	got := Registered()
	got["fixture-a"] = append(got["fixture-a"], "mutated")
	got["injected"] = []string{"should not stick"}

	again := Registered()
	if want := []string{"alpha"}; !reflect.DeepEqual(again["fixture-a"], want) {
		t.Fatalf("mutating a returned map leaked into the registry: %v", again["fixture-a"])
	}
	if _, ok := again["injected"]; ok {
		t.Fatalf("mutating a returned map injected a phantom surface into the registry")
	}
}

func TestRegisterTwiceUnderTheSameNamePanics(t *testing.T) {
	resetRegistry(t)
	Register("fixture-a", reflect.TypeOf(ledgerFixtureA{}))

	defer func() {
		if recover() == nil {
			t.Fatal("Register() a second time under the same name did not panic")
		}
	}()
	Register("fixture-a", reflect.TypeOf(ledgerFixtureB{}))
}

func TestWithRegisteredMergesWithoutMutatingBase(t *testing.T) {
	resetRegistry(t)
	Register("fixture-b", reflect.TypeOf(ledgerFixtureB{}))

	base := map[string][]string{"fixture-a": {"alpha"}}
	merged := WithRegistered(base)

	if len(base) != 1 {
		t.Fatalf("WithRegistered mutated its base argument: %v", base)
	}
	if len(merged) != 2 {
		t.Fatalf("WithRegistered(base) = %v, want exactly 2 entries", merged)
	}
	if want := []string{"alpha"}; !reflect.DeepEqual(merged["fixture-a"], want) {
		t.Fatalf("merged[\"fixture-a\"] = %v, want %v", merged["fixture-a"], want)
	}
	if want := []string{"beta"}; !reflect.DeepEqual(merged["fixture-b"], want) {
		t.Fatalf("merged[\"fixture-b\"] = %v, want %v", merged["fixture-b"], want)
	}
}

// TestWithRegisteredPanicsOnACollidingName proves the "pick one" refusal:
// a surface name present in BOTH base and the registry is exactly as much
// of a bug as calling Register() twice under the same name, and must be
// caught the same way rather than silently letting one definition win.
func TestWithRegisteredPanicsOnACollidingName(t *testing.T) {
	resetRegistry(t)
	Register("fixture-a", reflect.TypeOf(ledgerFixtureA{}))

	defer func() {
		if recover() == nil {
			t.Fatal("WithRegistered did not panic on a name present in both base and the registry")
		}
	}()
	WithRegistered(map[string][]string{"fixture-a": {"hand-listed"}})
}
