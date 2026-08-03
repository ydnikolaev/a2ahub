package localserver

import (
	"context"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/operational"
)

// SnapshotReader supplies the common pure operational projection. It owns all
// filesystem/cache composition; the HTTP transport never reads paths itself.
type SnapshotReader interface {
	Snapshot(context.Context) (operational.Snapshot, error)
}

// FetchSyncer is the opt-in fetch-only refresh seam. On error it MUST return a
// non-zero snapshot containing explicit degraded/unavailable source evidence.
// The server refuses an unmarked error snapshot and retains the last known
// generation; transport code never invents domain degradation facts.
type FetchSyncer interface {
	Sync(context.Context) (operational.Snapshot, error)
}

type ShellRenderer interface {
	Render(context.Context, operational.Snapshot) ([]byte, error)
}

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type TickerFactory interface {
	NewTicker(time.Duration) Ticker
}

type realTickerFactory struct{}

func (realTickerFactory) NewTicker(interval time.Duration) Ticker {
	return realTicker{Ticker: time.NewTicker(interval)}
}

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }
