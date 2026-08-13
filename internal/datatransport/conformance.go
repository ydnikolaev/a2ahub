package datatransport

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// RunConformance is the driver conformance suite AC-8 asks for (spec 05a
// AC-8, as narrowed by the 2026-08-13 amendment): every driver — the null
// driver this package ships in test scope, and any later real driver's own
// package — proves the same minimum through this one shared suite, so
// adding a driver is "a driver plus its conformance tests" and nothing
// else. Exported (rather than a _test.go helper) precisely so a driver's
// own package can call it from its own test file without importing this
// package's internals.
// Harness is a Driver plus the one thing the suite cannot know: how bytes
// become READABLE on this transport.
//
// The suite used to assert that Get returns Put's bytes immediately. That is
// a property of an in-memory store, not of a transport — spec 05a's own
// space-git writes through a pull request and reads `origin/main` after
// merge, and every plausible second driver (object storage with eventual
// consistency, deletable release assets) has some publish boundary too. So
// visibility is NOT the Driver's contract: §0's loop already sequences it
// (B delivers, a handoff appears on the thread, A fetches), and §9.1
// CONFIRM-3 puts that handoff in the protocol, not in the transport.
//
// What the suite still demands, and what makes it worth running, is
// byte-identity ONCE visible. Each driver's own package supplies the
// mechanism — merge the fixture's pull request, flush the bucket, nothing at
// all — and the assertion stays as strong as it was.
type Harness struct {
	Driver Driver

	// MakeVisible advances the transport until a prior Put at locator is
	// readable by Get. Nil means "readable immediately", which is the
	// honest answer for an in-memory driver and a lie for space-git.
	MakeVisible func(ctx context.Context, locator string) error
}

// DriverHarness is the trivial harness for a transport with no publish
// boundary.
func DriverHarness(d Driver) Harness { return Harness{Driver: d} }

func RunConformance(t *testing.T, h Harness) {
	t.Helper()
	runConformance(t, h)
}

// conformanceT is the slice of *testing.T the suite actually uses. It exists
// so the SUITE ITSELF can be given teeth: a recorder in this package's own
// tests implements it, drives runConformance against deliberately broken
// drivers, and asserts each assertion reports the failure it claims to.
//
// Without it those branches are unreachable — a conformance suite passing on
// a correct driver never executes its own failure reporting, which is both a
// coverage hole and, worse, a gate nobody has watched red. `testing.TB`
// cannot be implemented outside the standard library (it carries an
// unexported method), so the seam is this three-method interface.
type conformanceT interface {
	Helper()
	Fatalf(format string, args ...any)
}

func runConformance(t conformanceT, h Harness) {
	t.Helper()
	d := h.Driver
	if d == nil {
		t.Fatalf("Harness.Driver is nil")
	}
	makeVisible := h.MakeVisible
	if makeVisible == nil {
		makeVisible = func(context.Context, string) error { return nil }
	}
	ctx := context.Background()

	// The name is non-empty and stable.
	name := d.Name()
	if name == "" {
		t.Fatalf("Driver.Name() = %q, want non-empty", name)
	}
	if got := d.Name(); got != name {
		t.Fatalf("Driver.Name() is not stable: first call %q, second call %q", name, got)
	}

	// A locator the driver produces is one it accepts.
	locator, err := d.Locate("axon", "DP-axon-20260101-abcd")
	if err != nil {
		t.Fatalf("Locate() = %v, want no error", err)
	}
	if !d.AcceptsLocator(locator) {
		t.Fatalf("driver %q produced locator %q but does not accept it", name, locator)
	}

	// An unsafe locator — absolute path, credential-shaped, containing ':'
	// or '@', or escaping via '..' — is REFUSED, not merely warned about
	// (spec 05a §T2.4), and refused by the shared sentinel rather than a
	// driver-invented one.
	unsafeLocators := []string{
		"/etc/passwd",
		"axon/data/../../../etc/passwd",
		"https://token:secret@host/axon",
		"user@host:22/axon",
	}
	for _, bad := range unsafeLocators {
		if d.AcceptsLocator(bad) {
			t.Fatalf("driver %q accepts unsafe locator %q, want refused", name, bad)
		}
		putErr := d.Put(ctx, bad, map[string][]byte{"manifest.json": []byte("{}")})
		if putErr == nil {
			t.Fatalf("Put(%q) = nil error, want a refusal", bad)
		}
		if !errors.Is(putErr, datapackage.ErrUnsafeLocator) {
			t.Fatalf("Put(%q) = %v, want an error wrapping %v", bad, putErr, datapackage.ErrUnsafeLocator)
		}
	}

	// Bytes put and then got are byte-identical.
	files := map[string][]byte{
		"manifest.json":       []byte(`{"schema":"data-package/v1"}`),
		"payload/orders.json": []byte(`{"id":1}`),
	}
	if err := d.Put(ctx, locator, files); err != nil {
		t.Fatalf("Put(%q) = %v, want no error", locator, err)
	}
	if err := makeVisible(ctx, locator); err != nil {
		t.Fatalf("MakeVisible(%q) = %v, want no error", locator, err)
	}
	got, err := d.Get(ctx, locator)
	if err != nil {
		t.Fatalf("Get(%q) = %v, want no error", locator, err)
	}
	assertFilesEqual(t, files, got)

	// A repeated Put of the same package is idempotent rather than a
	// second copy: Put a DIFFERENT file set at the SAME locator and assert
	// Get returns exactly the second set, not a union of both. A driver
	// that appends rather than replaces must redden this, not the earlier
	// "identical" round trip above (which a naive append-only driver would
	// also pass).
	replacement := map[string][]byte{
		"manifest.json": []byte(`{"schema":"data-package/v1","attempt":2}`),
	}
	if err := d.Put(ctx, locator, replacement); err != nil {
		t.Fatalf("repeated Put(%q) = %v, want no error", locator, err)
	}
	if err := makeVisible(ctx, locator); err != nil {
		t.Fatalf("MakeVisible(%q) after repeated Put = %v, want no error", locator, err)
	}
	got2, err := d.Get(ctx, locator)
	if err != nil {
		t.Fatalf("Get(%q) after repeated Put = %v, want no error", locator, err)
	}
	assertFilesEqual(t, replacement, got2)
}

func assertFilesEqual(t conformanceT, want, got map[string][]byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("Get() returned %d entries %v, want %d entries %v", len(got), keysOf(got), len(want), keysOf(want))
	}
	for path, wantBytes := range want {
		gotBytes, ok := got[path]
		if !ok {
			t.Fatalf("Get() is missing entry %q", path)
		}
		if !bytes.Equal(wantBytes, gotBytes) {
			t.Fatalf("Get()[%q] = %q, want %q", path, gotBytes, wantBytes)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
