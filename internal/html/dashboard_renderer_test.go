package html

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
)

func TestDashboardRendererInjectsExactOperationalSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-time.Minute)
	snapshot, err := operational.Build(operational.Input{
		Sources: []operational.SourceEvidence{{
			Kind: operational.SourceSpace, Space: "northstar", Revision: "abc",
			SyncedAt: &syncedAt, ObservedAt: now, Freshness: operational.SourceCurrent,
		}},
		Threads: []operational.ThreadEvidence{{
			Space: "northstar", Thread: "thread-1", Title: "Integrate ingest",
			Protocol: operational.Protocol{OpenCount: 1, WaitingOn: []string{"atlas"}},
		}},
	}, fixedOperationalClock{now: now}, operational.Limits{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	store := cache.NewStore("atlas", t.TempDir(), nil, func() time.Time { return now }, time.Minute)
	renderer, err := NewDashboardRenderer(store, "atlas")
	if err != nil {
		t.Fatalf("NewDashboardRenderer: %v", err)
	}
	page, viewModel, fingerprint, err := renderer.Render(context.Background(), snapshot)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if fingerprint == "" {
		t.Fatal("Render returned an empty content fingerprint")
	}
	const prefix = "window.A2A_DEMO="
	start := bytes.Index(page, []byte(prefix))
	if start < 0 {
		t.Fatal("rendered shell has no DATA payload")
	}
	start += len(prefix)
	end := bytes.Index(page[start:], []byte(";window.A2A_DOCS="))
	if end < 0 {
		t.Fatal("rendered shell has no DATA terminator")
	}
	var rendered Data
	if err := json.Unmarshal(page[start:start+end], &rendered); err != nil {
		t.Fatalf("decode rendered DATA: %v", err)
	}
	wantJSON, err := operational.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON(want): %v", err)
	}
	gotJSON, err := operational.CanonicalJSON(rendered.Operational)
	if err != nil {
		t.Fatalf("CanonicalJSON(got): %v", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatal("rendered shell does not contain the exact operational snapshot JSON")
	}
	if !bytes.Equal(page[start:start+end], viewModel) {
		t.Fatal("renderer shell DATA and returned view model differ")
	}
}

func TestDashboardRendererRejectsUnversionedSnapshot(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("atlas", t.TempDir(), nil, time.Now, time.Minute)
	renderer, err := NewDashboardRenderer(store, "atlas")
	if err != nil {
		t.Fatalf("NewDashboardRenderer: %v", err)
	}
	if _, _, _, err := renderer.Render(context.Background(), operational.Snapshot{}); !errors.Is(err, ErrInvalidDashboardRenderer) {
		t.Fatalf("Render error = %v, want ErrInvalidDashboardRenderer", err)
	}
}

type fixedOperationalClock struct{ now time.Time }

func (c fixedOperationalClock) Now() time.Time { return c.now }
