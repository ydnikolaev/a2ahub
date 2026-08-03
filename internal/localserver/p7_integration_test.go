package localserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	dashboard "github.com/ydnikolaev/a2ahub/internal/html"
	"github.com/ydnikolaev/a2ahub/internal/operational"
)

func TestIT03StaticRendererAndHTTPExposeTheSameCanonicalSnapshot(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	snapshot := testSnapshot("it-03-shared-source")
	snapshot.GeneratedAt = now
	snapshot.Timeline = []operational.TimelineRow{{
		Space: "space-a", Thread: "thread:one", Title: "One shared projection",
		Participants: []string{"atlas", "checkout"},
		Protocol:     operational.Protocol{OpenCount: 1, WaitingOn: []string{"atlas"}, BlockingBy: []string{}},
		Work:         []operational.Work{}, Consistency: []operational.Consistency{},
	}}
	snapshot.TimelineWindow = operational.Window{Total: 1, Shown: 1}

	store := cache.NewStore("atlas", t.TempDir(), nil, func() time.Time { return now }, time.Minute)
	renderer, err := dashboard.NewDashboardRenderer(store, "atlas")
	if err != nil {
		t.Fatalf("NewDashboardRenderer() error = %v", err)
	}
	reader := &fakeReader{snapshot: snapshot}
	staticSource, err := reader.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("static Snapshot() error = %v", err)
	}
	staticPage, err := renderer.Render(t.Context(), staticSource)
	if err != nil {
		t.Fatalf("static Render() error = %v", err)
	}
	staticSnapshot := operationalFromDashboardPage(t, staticPage)

	server, err := New(DefaultConfig(), reader, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.refresh(t.Context()); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	response := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	if response.status != http.StatusOK {
		t.Fatalf("snapshot status = %d, body = %s", response.status, response.body)
	}
	var servedSnapshot operational.Snapshot
	if err := json.Unmarshal(response.body, &servedSnapshot); err != nil {
		t.Fatalf("decode served snapshot: %v", err)
	}
	staticJSON, err := operational.CanonicalJSON(staticSnapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON(static) error = %v", err)
	}
	servedJSON, err := operational.CanonicalJSON(servedSnapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON(served) error = %v", err)
	}
	if !bytes.Equal(staticJSON, servedJSON) || staticSnapshot.Revision != servedSnapshot.Revision {
		t.Fatalf("static/server snapshot mismatch\nstatic: %s\nserved: %s", staticJSON, servedJSON)
	}
}

func TestIT04RapidRevisionsBoundSlowWritersAndSSEReconnectGetsCurrent(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	server, err := New(config, &fakeReader{}, nil, fakeRenderer{shell: []byte("dashboard")}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	initial := largeIntegrationSnapshot(t, "it-04-revision-0")
	if err := server.publish(t.Context(), initial); err != nil {
		t.Fatalf("initial publish error = %v", err)
	}

	clientCancels := make([]context.CancelFunc, 0, 2)
	clientDone := make([]<-chan struct{}, 0, 2)
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		writer := newStreamWriter()
		done := make(chan struct{})
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/events", nil)
		if requestErr != nil {
			t.Fatalf("NewRequest(SSE) error = %v", requestErr)
		}
		request.Header.Set("Last-Event-ID", initial.Revision)
		go func() {
			defer close(done)
			server.Handler().ServeHTTP(writer, request)
		}()
		clientCancels = append(clientCancels, cancel)
		clientDone = append(clientDone, done)
	}
	awaitBrokerClients(t, server, 2)

	writers := make([]*staggeredSnapshotWriter, 0, config.SnapshotWriters)
	writerDone := make([]<-chan struct{}, 0, config.SnapshotWriters)
	superseded := make([]*snapshotGeneration, 0, config.SnapshotWriters)
	for revision := 1; revision <= config.SnapshotWriters; revision++ {
		generation := server.store.get()
		writer := newStaggeredSnapshotWriter()
		done := make(chan struct{})
		request, requestErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://localhost/api/v1/snapshot", nil)
		if requestErr != nil {
			t.Fatalf("NewRequest(snapshot) error = %v", requestErr)
		}
		go func() {
			defer close(done)
			server.Handler().ServeHTTP(writer, request)
		}()
		awaitSignal(t, writer.started, "slow snapshot writer did not start")
		writers = append(writers, writer)
		writerDone = append(writerDone, done)
		superseded = append(superseded, generation)
		if err := server.publish(t.Context(), largeIntegrationSnapshot(t, fmt.Sprintf("it-04-revision-%d", revision))); err != nil {
			t.Fatalf("publish revision %d error = %v", revision, err)
		}
	}

	if got := len(server.writerSlots); got != config.SnapshotWriters {
		t.Fatalf("active writers = %d, want %d", got, config.SnapshotWriters)
	}
	if cap(server.writerSlots) != DefaultSnapshotWriters || MaximumRetainedBodies*config.MaxSnapshotBytes > 80<<20 {
		t.Fatalf("retained snapshot cap drift: writers=%d retained=%d", cap(server.writerSlots), MaximumRetainedBodies*config.MaxSnapshotBytes)
	}
	for index, generation := range superseded {
		select {
		case <-generation.ctx.Done():
		default:
			t.Fatalf("superseded generation %d was not canceled", index)
		}
	}
	excess := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	if excess.status != http.StatusServiceUnavailable || excess.header.Get("Retry-After") == "" {
		t.Fatalf("excess writer response = %d headers=%v", excess.status, excess.header)
	}

	for _, cancel := range clientCancels {
		cancel()
	}
	for _, done := range clientDone {
		awaitSignal(t, done, "disconnected SSE handler did not stop")
	}
	awaitBrokerClients(t, server, 0)

	current := server.store.get().revision
	laterCtx, laterCancel := context.WithCancel(t.Context())
	laterWriter := newStreamWriter()
	laterDone := make(chan struct{})
	laterRequest, err := http.NewRequestWithContext(laterCtx, http.MethodGet, "http://localhost/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest(later SSE) error = %v", err)
	}
	laterRequest.Header.Set("Last-Event-ID", initial.Revision)
	go func() {
		defer close(laterDone)
		server.Handler().ServeHTTP(laterWriter, laterRequest)
	}()
	flush := awaitFlush(t, laterWriter)
	if !strings.Contains(flush, `data: {"revision":"`+current+`"}`) || strings.Contains(flush, "timeline") {
		t.Fatalf("later SSE did not receive revision-only current token: %q", flush)
	}
	laterCancel()
	awaitSignal(t, laterDone, "later SSE handler did not stop")

	for _, writer := range writers {
		close(writer.release)
	}
	for _, done := range writerDone {
		awaitSignal(t, done, "canceled snapshot writer did not stop")
	}
	if len(server.writerSlots) != 0 {
		t.Fatalf("snapshot writer slots leaked: %d", len(server.writerSlots))
	}
	for index, writer := range writers {
		writer.mu.Lock()
		writes, deadlineCalls := writer.writes, writer.deadlineCalls
		writer.mu.Unlock()
		if writes != 1 || deadlineCalls < 4 {
			t.Fatalf("writer %d writes/deadlines = %d/%d, want one canceled chunk and bounded deadlines", index, writes, deadlineCalls)
		}
	}
}

type staggeredSnapshotWriter struct {
	mu            sync.Mutex
	header        http.Header
	status        int
	writes        int
	deadlineCalls int
	started       chan struct{}
	release       chan struct{}
	startOnce     sync.Once
}

func newStaggeredSnapshotWriter() *staggeredSnapshotWriter {
	return &staggeredSnapshotWriter{
		header: make(http.Header), started: make(chan struct{}), release: make(chan struct{}),
	}
}

func (w *staggeredSnapshotWriter) Header() http.Header { return w.header }

func (w *staggeredSnapshotWriter) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 {
		w.status = status
	}
	w.mu.Unlock()
}

func (w *staggeredSnapshotWriter) Write(body []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.mu.Unlock()
	w.startOnce.Do(func() { close(w.started) })
	<-w.release
	return len(body), nil
}

func (w *staggeredSnapshotWriter) SetWriteDeadline(time.Time) error {
	w.mu.Lock()
	w.deadlineCalls++
	w.mu.Unlock()
	return nil
}

func operationalFromDashboardPage(t *testing.T, page []byte) operational.Snapshot {
	t.Helper()
	const prefix = "window.A2A_DEMO="
	start := bytes.Index(page, []byte(prefix))
	if start < 0 {
		t.Fatal("dashboard page has no DATA payload")
	}
	start += len(prefix)
	end := bytes.Index(page[start:], []byte(";window.A2A_DOCS="))
	if end < 0 {
		t.Fatal("dashboard page has no DATA terminator")
	}
	var payload struct {
		Operational operational.Snapshot `json:"operational"`
	}
	if err := json.Unmarshal(page[start:start+end], &payload); err != nil {
		t.Fatalf("decode dashboard DATA: %v", err)
	}
	return payload.Operational
}

func largeIntegrationSnapshot(t *testing.T, label string) operational.Snapshot {
	t.Helper()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	syncedAt := now.Add(-time.Minute)
	threads := make([]operational.ThreadEvidence, 100)
	for index := range threads {
		threads[index] = operational.ThreadEvidence{
			Space: "space-a", Thread: fmt.Sprintf("thread:%03d", index), Title: strings.Repeat("x", 512),
			Participants: []string{}, Protocol: operational.Protocol{Settled: true, WaitingOn: []string{}, BlockingBy: []string{}},
		}
	}
	snapshot, err := operational.Build(operational.Input{
		Sources: []operational.SourceEvidence{{
			Kind: operational.SourceSpace, Space: "space-a", Revision: label,
			SyncedAt: &syncedAt, ObservedAt: now, Freshness: operational.SourceCurrent,
		}},
		Threads: threads,
	}, integrationClock{now: now}, operational.DefaultLimits())
	if err != nil {
		t.Fatalf("Build large integration snapshot: %v", err)
	}
	body, err := operational.CanonicalJSON(snapshot)
	if err != nil {
		t.Fatalf("CanonicalJSON(large integration snapshot): %v", err)
	}
	if len(body) <= 32<<10 {
		t.Fatalf("large integration snapshot = %d bytes, want more than one write chunk", len(body))
	}
	return snapshot
}

type integrationClock struct{ now time.Time }

func (clock integrationClock) Now() time.Time { return clock.now }

func awaitBrokerClients(t *testing.T, server *Server, want int) {
	t.Helper()
	for server.broker.count() != want {
		select {
		case <-t.Context().Done():
			t.Fatalf("SSE clients = %d, want %d", server.broker.count(), want)
		default:
			runtime.Gosched()
		}
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-t.Context().Done():
		t.Fatal(failure)
	}
}
