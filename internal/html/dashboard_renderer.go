package html

import (
	"context"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
)

var ErrInvalidDashboardRenderer = errors.New("html: invalid dashboard renderer")

// DashboardRenderer turns an already-built operational snapshot into the
// local dashboard shell. It deliberately retains the supplied snapshot
// byte-for-byte at the model boundary; only presentation is owned here.
type DashboardRenderer struct {
	store    *cache.Store
	system   string
	template []byte
	docs     []DocSection
}

func NewDashboardRenderer(store *cache.Store, system string) (*DashboardRenderer, error) {
	if store == nil {
		return nil, ErrInvalidDashboardRenderer
	}
	docs, err := Docs()
	if err != nil {
		return nil, fmt.Errorf("html: load dashboard docs: %w", err)
	}
	return &DashboardRenderer{
		store: store, system: system, template: DefaultTemplate(), docs: docs,
	}, nil
}

// Render implements localserver.ShellRenderer without importing the transport
// package. The snapshot observation time anchors the surrounding static data
// so one published generation has one coherent time boundary.
func (r *DashboardRenderer) Render(ctx context.Context, snapshot operational.Snapshot) ([]byte, error) {
	if snapshot.SchemaVersion != operational.SchemaVersion || snapshot.GeneratedAt.IsZero() || snapshot.Revision == "" {
		return nil, ErrInvalidDashboardRenderer
	}
	data, err := AssembleWithOperational(ctx, r.store, r.system, snapshot.GeneratedAt, snapshot)
	if err != nil {
		return nil, err
	}
	return Render(r.template, data, r.docs)
}
