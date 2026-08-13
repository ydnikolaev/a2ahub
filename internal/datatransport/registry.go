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
// reset. It has no consumer in this brief (no driver is registered into it
// anywhere in this codebase yet); it exists so a future caller wiring a
// real driver at cmd/a2a's DI boundary has a shared registry to reach for
// without inventing one. A test that wants isolation should call
// NewRegistry() instead of using this var.
var Default = NewRegistry()
