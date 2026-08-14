package localserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type streamWriter struct {
	mu      sync.Mutex
	header  http.Header
	status  int
	body    bytes.Buffer
	flushes chan string
	// armed and cleared are counted separately rather than as one call
	// tally, so a test can assert the BRACKETING withWriteDeadline
	// promises (every arm gets its clear) instead of a magic minimum
	// that happens to equal twice the flush count.
	armed   int
	cleared int
}

func newStreamWriter() *streamWriter {
	return &streamWriter{header: make(http.Header), flushes: make(chan string, 16)}
}
func (w *streamWriter) Header() http.Header { return w.header }
func (w *streamWriter) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = status
	}
	w.mu.Unlock()
}
func (w *streamWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(body)
}
func (w *streamWriter) Flush() {
	w.mu.Lock()
	snapshot := w.body.String()
	w.mu.Unlock()
	w.flushes <- snapshot
}
func (w *streamWriter) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	if deadline.IsZero() {
		w.cleared++
	} else {
		w.armed++
	}
	w.mu.Unlock()
	return nil
}

func TestSSEImmediateRevisionChangeOnlyAndNoSnapshotBody(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	writer := newStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(writer, request)
	}()
	initial := awaitFlush(t, writer)
	initialID, initialPayload := parseLastRevisionEvent(t, initial)
	if initialPayload.Revision != testRevision("sha256:one") || initialID != initialPayload.ViewModel || strings.Contains(initial, "timeline") {
		t.Fatalf("initial SSE = %q", initial)
	}
	if err := server.publish(t.Context(), testSnapshot("sha256:one")); err != nil {
		t.Fatalf("same publish error = %v", err)
	}
	if err := server.publish(t.Context(), testSnapshot("sha256:two")); err != nil {
		t.Fatalf("changed publish error = %v", err)
	}
	changed := awaitFlush(t, writer)
	changedID, changedPayload := parseLastRevisionEvent(t, changed)
	if strings.Count(changed, "event: revision") != 2 || changedPayload.Revision != testRevision("sha256:two") || changedID != changedPayload.ViewModel {
		t.Fatalf("changed SSE = %q", changed)
	}
	cancel()
	awaitDone(t, done)
	if server.broker.count() != 0 {
		t.Fatalf("client remained registered: %d", server.broker.count())
	}
}

func TestSSEMatchingLastEventIDWaitsForNextRevision(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Last-Event-ID", server.store.dashboard().viewModelDigest)
	writer := newStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(writer, request)
	}()
	for server.broker.count() == 0 {
		select {
		case <-t.Context().Done():
			t.Fatal("SSE client did not register")
		default:
			runtime.Gosched()
		}
	}
	if err := server.publish(t.Context(), testSnapshot("sha256:two")); err != nil {
		t.Fatalf("publish error = %v", err)
	}
	flush := awaitFlush(t, writer)
	_, payload := parseLastRevisionEvent(t, flush)
	if payload.Revision != testRevision("sha256:two") {
		t.Fatalf("Last-Event-ID behavior = %q", flush)
	}
	cancel()
	awaitDone(t, done)
}

func TestSSEKeepaliveIsTransportOnly(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	server, tickers := newTestServer(t, config)
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	writer := newStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(writer, request)
	}()
	_ = awaitFlush(t, writer)
	ticker := tickers.await(t, config.SSEKeepalive)
	ticker.channel <- testSnapshot("x").GeneratedAt
	flush := awaitFlush(t, writer)
	if !strings.Contains(flush, ": keepalive") || strings.Count(flush, "event: revision") != 1 {
		t.Fatalf("keepalive = %q", flush)
	}
	cancel()
	awaitDone(t, done)

	// Read the counters only after the handler goroutine has returned.
	// withWriteDeadline arms, writes, and clears in that order, and the
	// write is what publishes the snapshot to flushes — so a read taken
	// straight after awaitFlush observes the arm of the last flush but
	// races its clear. That is a real happens-before gap, not slowness:
	// it passed locally for as long as the goroutine happened to reach
	// the clear first, and failed on a loaded CI runner at calls=5.
	// awaitDone is the only point where every deadline call this handler
	// will ever make has happened.
	writer.mu.Lock()
	armed, cleared := writer.armed, writer.cleared
	writer.mu.Unlock()
	if armed != cleared {
		t.Fatalf("write deadline was not cleared for every arm: armed=%d cleared=%d", armed, cleared)
	}
	// One arm for requireBoundedWriter's support probe, one for the
	// initial revision flush, one for the keepalive flush. Asserting the
	// arms rather than the total is what makes this count readable.
	if armed < 3 {
		t.Fatalf("rolling write deadline was not armed per flush: armed=%d", armed)
	}
}

func TestBrokerReplacesBackpressuredRevisionAndClosesClients(t *testing.T) {
	t.Parallel()
	broker := newRevisionBroker(1)
	id, revisions, err := broker.register()
	if err != nil {
		t.Fatalf("register() error = %v", err)
	}
	if _, _, err := broker.register(); !errors.Is(err, ErrClientLimit) {
		t.Fatalf("excess register error = %v", err)
	}
	broker.publish(dashboardVersion{revision: "r1", viewModelDigest: "d1"})
	broker.publish(dashboardVersion{revision: "r2", viewModelDigest: "d2"})
	if got := <-revisions; got != (dashboardVersion{revision: "r2", viewModelDigest: "d2"}) {
		t.Fatalf("backpressured token = %#v", got)
	}
	broker.deregister(id)
	if broker.count() != 0 {
		t.Fatal("deregister did not release client")
	}

	_, revisions, err = broker.register()
	if err != nil {
		t.Fatalf("second register error = %v", err)
	}
	broker.close()
	if _, open := <-revisions; open {
		t.Fatal("shutdown did not close stream")
	}
}

func TestSSESameRevisionChangedDashboardDigestPublishesAndReplaysByDigest(t *testing.T) {
	t.Parallel()
	renderer := &countingRenderer{shell: []byte("first"), viewModel: []byte(`{"version":1}`), fingerprint: "content:first"}
	server, err := New(DefaultConfig(), &fakeReader{}, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot := testSnapshot("same-revision")
	if err := server.publish(t.Context(), snapshot); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	previousDigest := server.store.dashboard().viewModelDigest

	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	request.Header.Set("Last-Event-ID", previousDigest)
	writer := newStreamWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(writer, request)
	}()
	for server.broker.count() == 0 {
		select {
		case <-t.Context().Done():
			t.Fatal("SSE client did not register")
		default:
			runtime.Gosched()
		}
	}
	renderer.set([]byte("second"), []byte(`{"version":2}`), "content:second")
	if err := server.publish(t.Context(), snapshot); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	flush := awaitFlush(t, writer)
	id, payload := parseLastRevisionEvent(t, flush)
	if payload.Revision != snapshot.Revision || payload.ViewModel == previousDigest || id != payload.ViewModel {
		t.Fatalf("same-revision dashboard invalidation = id %q payload %#v", id, payload)
	}
	cancel()
	awaitDone(t, done)
}

type revisionEventPayload struct {
	Revision  string `json:"revision"`
	ViewModel string `json:"viewModel"`
}

func parseLastRevisionEvent(t *testing.T, stream string) (string, revisionEventPayload) {
	t.Helper()
	var id, data string
	for _, line := range strings.Split(stream, "\n") {
		if value, ok := strings.CutPrefix(line, "id: "); ok {
			id = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			data = value
		}
	}
	if id == "" || data == "" {
		t.Fatalf("SSE event missing id/data: %q", stream)
	}
	var payload revisionEventPayload
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("decode SSE payload: %v", err)
	}
	if payload.Revision == "" || payload.ViewModel == "" {
		t.Fatalf("SSE payload missing fields: %#v", payload)
	}
	return id, payload
}

func TestSSEClientLimitReturns503(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.MaxSSEClients = 1
	server, _ := newTestServer(t, config)
	id, _, err := server.broker.register()
	if err != nil {
		t.Fatalf("register() error = %v", err)
	}
	defer server.broker.deregister(id)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	writer := newStreamWriter()
	server.Handler().ServeHTTP(writer, request)
	writer.mu.Lock()
	status := writer.status
	writer.mu.Unlock()
	if status != http.StatusServiceUnavailable || writer.header.Get("Retry-After") == "" {
		t.Fatalf("excess SSE status/headers = %d %v", status, writer.header)
	}
}

func awaitFlush(t *testing.T, writer *streamWriter) string {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case value := <-writer.flushes:
		return value
	case <-timer.C:
		writer.mu.Lock()
		body := writer.body.String()
		writer.mu.Unlock()
		t.Fatalf("SSE did not flush within 5s; body=%q", body)
		return ""
	case <-t.Context().Done():
		t.Fatal("SSE did not flush")
		return ""
	}
}

func awaitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("handler did not stop")
	}
}
