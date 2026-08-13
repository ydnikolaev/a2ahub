package datatransport

import (
	"fmt"
	"sync"
)

// Registry is a Driver lookup keyed by driver name (the manifest's own
// transport_driver value). It is an explicit, constructible type — not
// package-level mutable state a test cannot reset: every test that wants
// isolation makes its own with NewRegistry() rather than sharing state with
// any other test in the same run.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

// NewRegistry returns an empty, ready-to-use Registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

// Register adds d under d.Name(). Refuses an empty name and a name already
// registered (ErrDuplicateDriver).
func (r *Registry) Register(d Driver) error {
	if d == nil {
		return fmt.Errorf("datatransport: Register: driver is nil")
	}
	name := d.Name()
	if name == "" {
		return fmt.Errorf("datatransport: Register: driver name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.drivers[name]; exists {
		return fmt.Errorf("datatransport: Register: %w: %q", ErrDuplicateDriver, name)
	}
	r.drivers[name] = d
	return nil
}

// Lookup returns the driver registered under name, or ErrUnknownDriver.
func (r *Registry) Lookup(name string) (Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("datatransport: Lookup: %w: %q", ErrUnknownDriver, name)
	}
	return d, nil
}

// Default is a package-level, constructible Registry — exactly what
// NewRegistry() returns, never a hidden singleton with state a test cannot
// reset.
//
// Its production consumer is internal/space, which registers the space-git
// driver into it lazily (data_transport.go's registerSpaceGit) and looks a
// driver up by the value the MANIFEST declares. It had no consumer at all
// until 2026-08-13, and that is precisely what made AC-8's "a second driver
// is a registry entry" untrue as wired — the delivery path constructed its
// driver directly. Found by this phase's own audit.
//
// A test that wants isolation should call NewRegistry() instead of this var.
var Default = NewRegistry()
