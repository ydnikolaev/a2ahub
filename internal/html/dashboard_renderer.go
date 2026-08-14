package html

import (
	"context"
	"errors"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// ErrInvalidDashboardRenderer reports an incomplete renderer or invalid snapshot.
var ErrInvalidDashboardRenderer = errors.New("html: invalid dashboard renderer")

// DashboardRenderer turns an already-built operational snapshot into the
// local dashboard shell. It deliberately retains the supplied snapshot
// byte-for-byte at the model boundary; only presentation is owned here.
type DashboardRenderer struct {
	store            *cache.Store
	system           string
	historyValidator space.ContractHistoryDocumentValidator
	template         []byte
	docs             []DocSection
}

// NewDashboardRendererWithContractHistory creates the production renderer
// with the canonical historical-document validator supplied by cmd/a2a.
func NewDashboardRendererWithContractHistory(store *cache.Store, system string, validator space.ContractHistoryDocumentValidator) (*DashboardRenderer, error) {
	renderer, err := NewDashboardRenderer(store, system)
	if err != nil {
		return nil, err
	}
	renderer.historyValidator = validator
	return renderer, nil
}

// NewDashboardRenderer creates a renderer backed by the supplied cache store.
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
func (r *DashboardRenderer) Render(ctx context.Context, snapshot operational.Snapshot) ([]byte, []byte, string, error) {
	if snapshot.SchemaVersion != operational.SchemaVersion || snapshot.GeneratedAt.IsZero() || snapshot.Revision == "" {
		return nil, nil, "", ErrInvalidDashboardRenderer
	}
	data, err := AssembleWithOperationalAndContractHistory(ctx, r.store, r.system, snapshot.GeneratedAt, snapshot, r.historyValidator)
	if err != nil {
		return nil, nil, "", fmt.Errorf("html: assemble dashboard: %w", err)
	}
	viewModel, err := CanonicalViewModelJSON(data)
	if err != nil {
		return nil, nil, "", fmt.Errorf("html: encode dashboard view model: %w", err)
	}
	fingerprint, err := ContentFingerprint(data)
	if err != nil {
		return nil, nil, "", fmt.Errorf("html: fingerprint dashboard content: %w", err)
	}
	shell, err := renderEncoded(r.template, viewModel, r.docs)
	if err != nil {
		return nil, nil, "", fmt.Errorf("html: render dashboard shell: %w", err)
	}
	return shell, viewModel, fingerprint, nil
}
