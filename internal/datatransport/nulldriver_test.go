package datatransport

import (
	"context"
	"fmt"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// NewNullDriver returns AC-8's own verification method (spec 05a AC-8, as
// narrowed by the 2026-08-13 amendment: "add a null driver in test scope
// and run the driver conformance suite"): an in-memory Driver that never
// leaves this process, proving the seam is real without touching a wire
// shape v1 cannot carry.
//
// Exported (capitalised, despite living in a _test.go file) so this
// package's own external test package (datatransport_test) can construct
// one — proving RunConformance is callable the way a real driver's own
// package would call it, from outside this package.
func NewNullDriver() Driver {
	return &nullDriver{store: make(map[string]map[string][]byte)}
}

// nullDriver is the concrete Driver NewNullDriver returns. Locator safety
// is enforced with artifact.CleanRelativePath — the exact predicate
// internal/datapackage's own Pack already uses for the same rule (see that
// package's pack.go step 2) — so this driver refuses the same locator
// shapes space-git's own manifest field would, rather than restating the
// rule with a different, possibly drifting, check.
type nullDriver struct {
	mu    sync.Mutex
	store map[string]map[string][]byte
}

func (d *nullDriver) Name() string { return "null-test" }

func (d *nullDriver) Locate(system, packageID string) (string, error) {
	if system == "" || packageID == "" {
		return "", fmt.Errorf("datatransport: nullDriver: Locate: system and packageID are required")
	}
	return system + "/data/" + packageID, nil
}

func (d *nullDriver) AcceptsLocator(locator string) bool {
	return artifact.CleanRelativePath(locator)
}

func (d *nullDriver) Put(ctx context.Context, locator string, files map[string][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !d.AcceptsLocator(locator) {
		return fmt.Errorf("datatransport: nullDriver: Put: %w: %q", datapackage.ErrUnsafeLocator, locator)
	}
	copied := make(map[string][]byte, len(files))
	for path, raw := range files {
		copied[path] = append([]byte(nil), raw...)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// Replace, never merge: a repeated Put at the same locator holds
	// exactly the latest file set afterward, not a union of every Put ever
	// made to that locator.
	d.store[locator] = copied
	return nil
}

func (d *nullDriver) Get(ctx context.Context, locator string) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !d.AcceptsLocator(locator) {
		return nil, fmt.Errorf("datatransport: nullDriver: Get: %w: %q", datapackage.ErrUnsafeLocator, locator)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	files, ok := d.store[locator]
	if !ok {
		return nil, fmt.Errorf("datatransport: nullDriver: Get: no package at %q", locator)
	}
	out := make(map[string][]byte, len(files))
	for path, raw := range files {
		out[path] = append([]byte(nil), raw...)
	}
	return out, nil
}
