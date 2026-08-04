package localserver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/operational"
)

type fakeReader struct {
	mu       sync.Mutex
	snapshot operational.Snapshot
	err      error
	calls    int
	called   chan struct{}
}

func (f *fakeReader) Snapshot(context.Context) (operational.Snapshot, error) {
	f.mu.Lock()
	f.calls++
	snapshot, err := f.snapshot, f.err
	f.mu.Unlock()
	if f.called != nil {
		select {
		case f.called <- struct{}{}:
		default:
		}
	}
	return snapshot, err
}

type fakeSyncer struct {
	mu       sync.Mutex
	snapshot operational.Snapshot
	err      error
	calls    int
	called   chan struct{}
}

func (f *fakeSyncer) Sync(context.Context) (operational.Snapshot, error) {
	f.mu.Lock()
	f.calls++
	snapshot, err := f.snapshot, f.err
	f.mu.Unlock()
	if f.called != nil {
		select {
		case f.called <- struct{}{}:
		default:
		}
	}
	return snapshot, err
}

func (f *fakeSyncer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeRenderer struct {
	shell []byte
	err   error
}

func (f fakeRenderer) Render(context.Context, operational.Snapshot) ([]byte, error) {
	return append([]byte(nil), f.shell...), f.err
}

type countingRenderer struct {
	mu    sync.Mutex
	shell []byte
	err   error
	calls int
}

func (r *countingRenderer) Render(context.Context, operational.Snapshot) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return append([]byte(nil), r.shell...), r.err
}

func (r *countingRenderer) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type fakeTicker struct{ channel chan time.Time }

func (f *fakeTicker) C() <-chan time.Time { return f.channel }
func (f *fakeTicker) Stop()               {}

type fakeTickerFactory struct {
	mu      sync.Mutex
	tickers map[time.Duration][]*fakeTicker
	created chan time.Duration
}

func newFakeTickerFactory() *fakeTickerFactory {
	return &fakeTickerFactory{tickers: make(map[time.Duration][]*fakeTicker), created: make(chan time.Duration, 16)}
}

func (f *fakeTickerFactory) NewTicker(interval time.Duration) Ticker {
	ticker := &fakeTicker{channel: make(chan time.Time, 8)}
	f.mu.Lock()
	f.tickers[interval] = append(f.tickers[interval], ticker)
	f.mu.Unlock()
	f.created <- interval
	return ticker
}

func (f *fakeTickerFactory) await(t *testing.T, interval time.Duration) *fakeTicker {
	t.Helper()
	for {
		select {
		case got := <-f.created:
			if got == interval {
				f.mu.Lock()
				list := f.tickers[interval]
				ticker := list[len(list)-1]
				f.mu.Unlock()
				return ticker
			}
		case <-t.Context().Done():
			t.Fatalf("ticker %s was not created", interval)
		}
	}
}

func testSnapshot(revision string) operational.Snapshot {
	revision = testRevision(revision)
	return operational.Snapshot{
		SchemaVersion:  operational.SchemaVersion,
		GeneratedAt:    time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC),
		Revision:       revision,
		Sources:        []operational.Source{},
		Timeline:       []operational.TimelineRow{},
		TimelineWindow: operational.Window{},
		Unavailable:    []operational.Unavailable{},
	}
}

func testRevision(label string) string {
	sum := sha256.Sum256([]byte(label))
	return fmt.Sprintf("sha256:%x", sum)
}

func newTestServer(t *testing.T, config Config) (*Server, *fakeTickerFactory) {
	t.Helper()
	tickers := newFakeTickerFactory()
	reader := &fakeReader{snapshot: testSnapshot("sha256:one")}
	server, err := New(config, reader, nil, fakeRenderer{shell: []byte("<!doctype html><title>dashboard</title>")}, tickers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.refresh(t.Context()); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	return server, tickers
}

func request(t *testing.T, server *Server, method, path string, body bool) *responseCapture {
	t.Helper()
	var reader io.Reader
	if body {
		reader = &fixedBody{data: []byte("body")}
	}
	req, err := http.NewRequestWithContext(t.Context(), method, "http://localhost"+path, reader)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	capture := newResponseCapture()
	server.Handler().ServeHTTP(deadlineResponseCapture{responseCapture: capture}, req)
	return capture
}

type fixedBody struct{ data []byte }

func (r *fixedBody) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type responseCapture struct {
	header http.Header
	status int
	body   []byte
}

type deadlineResponseCapture struct{ responseCapture *responseCapture }

func (w deadlineResponseCapture) Header() http.Header    { return w.responseCapture.Header() }
func (w deadlineResponseCapture) WriteHeader(status int) { w.responseCapture.WriteHeader(status) }
func (w deadlineResponseCapture) Write(body []byte) (int, error) {
	return w.responseCapture.Write(body)
}
func (deadlineResponseCapture) SetWriteDeadline(time.Time) error { return nil }

func newResponseCapture() *responseCapture     { return &responseCapture{header: make(http.Header)} }
func (r *responseCapture) Header() http.Header { return r.header }
func (r *responseCapture) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
func (r *responseCapture) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body = append(r.body, body...)
	return len(body), nil
}
