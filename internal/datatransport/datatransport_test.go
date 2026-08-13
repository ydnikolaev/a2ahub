package datatransport_test

import (
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/datatransport"
)

// TestNullDriverConformance is AC-8's own verification method (spec 05a
// AC-8, 2026-08-13 amendment): the null driver, run through the exported
// suite, from an EXTERNAL test package — proving RunConformance is callable
// the way a real driver's own package would call it, not just from inside
// datatransport itself.
func TestNullDriverConformance(t *testing.T) {
	datatransport.RunConformance(t, datatransport.NewNullDriver())
}

func TestRegistryRegisterDuplicateRefused(t *testing.T) {
	r := datatransport.NewRegistry()
	d := datatransport.NewNullDriver()
	if err := r.Register(d); err != nil {
		t.Fatalf("first Register() = %v, want nil", err)
	}
	err := r.Register(d)
	if err == nil {
		t.Fatalf("second Register() = nil, want a refusal for a duplicate name")
	}
	if !errors.Is(err, datatransport.ErrDuplicateDriver) {
		t.Fatalf("second Register() = %v, want an error wrapping %v", err, datatransport.ErrDuplicateDriver)
	}
}

func TestRegistryLookupUnknownRefused(t *testing.T) {
	r := datatransport.NewRegistry()
	_, err := r.Lookup("does-not-exist")
	if err == nil {
		t.Fatalf("Lookup(unknown) = nil, want a refusal")
	}
	if !errors.Is(err, datatransport.ErrUnknownDriver) {
		t.Fatalf("Lookup(unknown) = %v, want an error wrapping %v", err, datatransport.ErrUnknownDriver)
	}
}

func TestRegistryLookupRegistered(t *testing.T) {
	r := datatransport.NewRegistry()
	d := datatransport.NewNullDriver()
	if err := r.Register(d); err != nil {
		t.Fatalf("Register() = %v, want nil", err)
	}
	got, err := r.Lookup(d.Name())
	if err != nil {
		t.Fatalf("Lookup(%q) = %v, want nil", d.Name(), err)
	}
	if got != d {
		t.Fatalf("Lookup(%q) returned a different driver instance than was registered", d.Name())
	}
}

// TestRegistryIsolated proves two Registry instances share no state — the
// brief's own requirement: "No global mutable state that a test cannot
// reset."
func TestRegistryIsolated(t *testing.T) {
	r1 := datatransport.NewRegistry()
	r2 := datatransport.NewRegistry()
	d := datatransport.NewNullDriver()
	if err := r1.Register(d); err != nil {
		t.Fatalf("r1.Register() = %v, want nil", err)
	}
	if _, err := r2.Lookup(d.Name()); err == nil {
		t.Fatalf("r2.Lookup(%q) succeeded after only r1 registered it; registries must not share state", d.Name())
	}
}
