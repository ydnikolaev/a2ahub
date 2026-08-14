package localserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/operational"
)

func TestServeDefaultDoesNoSyncAndJoinsOnCancellation(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.Listen = "127.0.0.1:0"
	reader := &fakeReader{snapshot: testSnapshot("sha256:one"), called: make(chan struct{}, 2)}
	syncer := &fakeSyncer{called: make(chan struct{}, 1)}
	server, err := New(config, reader, syncer, fakeRenderer{shell: []byte("ok")}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	select {
	case <-reader.called:
	case <-t.Context().Done():
		t.Fatal("initial snapshot was not read")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("Serve did not join after cancellation")
	}
	if syncer.callCount() != 0 {
		t.Fatalf("default server made %d sync calls", syncer.callCount())
	}
}

func TestOptInSyncPublishesDegradedSnapshotWhileReportingFailure(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.Listen = "127.0.0.1:0"
	config.SyncEvery = MinimumSyncEvery
	tickers := newFakeTickerFactory()
	reader := &fakeReader{snapshot: testSnapshot("sha256:one"), called: make(chan struct{}, 2)}
	syncFailure := errors.New("fetch unavailable")
	degraded := testSnapshot("sha256:degraded")
	degraded.Unavailable = []operational.Unavailable{{SourceKind: operational.SourceSpace, Space: "space-a", Code: "sync-failed", Summary: "fetch unavailable"}}
	syncer := &fakeSyncer{snapshot: degraded, err: syncFailure, called: make(chan struct{}, 1)}
	server, err := New(config, reader, syncer, fakeRenderer{shell: []byte("ok")}, tickers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	select {
	case <-reader.called:
	case <-t.Context().Done():
		t.Fatal("initial snapshot was not read")
	}
	syncTicker := tickers.await(t, config.SyncEvery)
	syncTicker.channel <- time.Now()
	select {
	case <-syncer.called:
	case <-t.Context().Done():
		t.Fatal("sync was not invoked")
	}
	for server.store.get().revision != testRevision("sha256:degraded") {
		select {
		case <-t.Context().Done():
			t.Fatal("degraded snapshot was not published")
		default:
			runtime.Gosched()
		}
	}
	for !errors.Is(server.LastError(), syncFailure) {
		select {
		case <-t.Context().Done():
			t.Fatalf("LastError() = %v", server.LastError())
		default:
			runtime.Gosched()
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("Serve did not stop")
	}
}

func TestConfigEnforcesHeaderClientRefreshAndBodyHardBounds(t *testing.T) {
	t.Parallel()
	tests := []Config{
		func() Config { c := DefaultConfig(); c.Refresh = MinimumRefresh - time.Nanosecond; return c }(),
		func() Config { c := DefaultConfig(); c.SyncEvery = MinimumSyncEvery - time.Nanosecond; return c }(),
		func() Config { c := DefaultConfig(); c.MaxSSEClients = MaximumSSEClients + 1; return c }(),
		func() Config { c := DefaultConfig(); c.MaxHeaderBytes = DefaultMaxHeaderBytes + 1; return c }(),
		func() Config { c := DefaultConfig(); c.MaxSnapshotBytes = (16 << 20) + 1; return c }(),
		func() Config { c := DefaultConfig(); c.MaxShellBytes = DefaultMaxShellBytes + 1; return c }(),
		func() Config { c := DefaultConfig(); c.MaxDashboardBytes = MaximumDashboardBytes + 1; return c }(),
	}
	for _, config := range tests {
		if _, err := New(config, &fakeReader{}, nil, fakeRenderer{}, nil); err == nil {
			t.Fatalf("New() accepted invalid config: %#v", config)
		}
	}
}

func TestDashboardBoundDefaultsHardMaximumAndLastGoodRetention(t *testing.T) {
	t.Parallel()
	defaults := DefaultConfig()
	if defaults.MaxDashboardBytes != DefaultMaxDashboardBytes || DefaultMaxDashboardBytes != 2<<20 || MaximumDashboardBytes != 4<<20 {
		t.Fatalf("dashboard bounds = default %d configured %d hard %d", defaults.MaxDashboardBytes, DefaultMaxDashboardBytes, MaximumDashboardBytes)
	}

	config := defaults
	config.MaxDashboardBytes = 8
	renderer := &countingRenderer{shell: []byte("first"), viewModel: bytes.Repeat([]byte("x"), config.MaxDashboardBytes), fingerprint: "content:first"}
	server, err := New(config, &fakeReader{}, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.publish(t.Context(), testSnapshot("exact-bound")); err != nil {
		t.Fatalf("exact dashboard bound was refused: %v", err)
	}
	lastGood := server.store.dashboard()
	renderer.set([]byte("second"), bytes.Repeat([]byte("y"), config.MaxDashboardBytes+1), "content:second")
	if err := server.publish(t.Context(), testSnapshot("one-over")); !errors.Is(err, ErrSnapshotUnavailable) {
		t.Fatalf("one-over dashboard error = %v, want ErrSnapshotUnavailable", err)
	}
	if current := server.store.dashboard(); current != lastGood || !bytes.Equal(current.viewModel, bytes.Repeat([]byte("x"), config.MaxDashboardBytes)) {
		t.Fatal("oversized dashboard replaced the last good generation")
	}
}

type blockingReader struct {
	started chan struct{}
	release chan struct{}
	result  operational.Snapshot
}

func (r *blockingReader) Snapshot(context.Context) (operational.Snapshot, error) {
	close(r.started)
	<-r.release
	return r.result, nil
}

func TestPollAndSyncPublicationIsCausallySerialized(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.SyncEvery = MinimumSyncEvery
	reader := &blockingReader{started: make(chan struct{}), release: make(chan struct{}), result: testSnapshot("old-poll")}
	syncer := &fakeSyncer{snapshot: testSnapshot("new-sync")}
	server, err := New(config, reader, syncer, fakeRenderer{shell: []byte("ok")}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.publish(t.Context(), testSnapshot("initial")); err != nil {
		t.Fatalf("initial publish error = %v", err)
	}
	pollDone := make(chan error, 1)
	go func() { pollDone <- server.refresh(t.Context()) }()
	select {
	case <-reader.started:
	case <-t.Context().Done():
		t.Fatal("poll did not begin")
	}
	syncDone := make(chan error, 1)
	go func() { syncDone <- server.sync(t.Context()) }()
	runtime.Gosched()
	close(reader.release)
	if err := <-pollDone; err != nil {
		t.Fatalf("poll error = %v", err)
	}
	if err := <-syncDone; err != nil {
		t.Fatalf("sync error = %v", err)
	}
	if got := server.store.get().revision; got != testRevision("new-sync") {
		t.Fatalf("old poll regressed newer sync: revision=%s", got)
	}
}

func TestSyncErrorRequiresExplicitDegradedSnapshotAndPreservesCurrent(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.SyncEvery = MinimumSyncEvery
	syncFailure := errors.New("fetch failed")
	server, err := New(config, &fakeReader{}, &fakeSyncer{err: syncFailure}, fakeRenderer{shell: []byte("ok")}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.publish(t.Context(), testSnapshot("current")); err != nil {
		t.Fatalf("publish() error = %v", err)
	}
	err = server.sync(t.Context())
	if !errors.Is(err, syncFailure) || !errors.Is(err, ErrDegradedSnapshotRequired) {
		t.Fatalf("sync error = %v", err)
	}
	if got := server.store.get().revision; got != testRevision("current") {
		t.Fatalf("failed sync replaced current snapshot: %s", got)
	}
}

func TestSameKeyPublicationRefreshesSnapshotAndRetainsDashboardCandidate(t *testing.T) {
	t.Parallel()
	renderer := &countingRenderer{shell: []byte("first")}
	server, err := New(DefaultConfig(), &fakeReader{}, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first := testSnapshot("stable")
	if err := server.publish(t.Context(), first); err != nil {
		t.Fatalf("first publish error = %v", err)
	}
	servingSnapshot := server.store.get()
	servingDashboard := server.store.dashboard()

	second := first
	second.GeneratedAt = second.GeneratedAt.Add(time.Minute)
	renderer.set([]byte("discarded shell"), []byte(`{"candidate":true}`), sha256Digest([]byte("first")))
	if err := server.publish(t.Context(), second); err != nil {
		t.Fatalf("same-revision publish error = %v", err)
	}
	refreshedSnapshot := server.store.get()
	refreshedDashboard := server.store.dashboard()
	if refreshedSnapshot == servingSnapshot {
		t.Fatal("same semantic revision retained stale snapshot bytes")
	}
	if refreshedSnapshot.revision != servingSnapshot.revision || refreshedSnapshot.ctx != servingSnapshot.ctx || refreshedDashboard.ctx != servingDashboard.ctx {
		t.Fatal("same semantic revision did not preserve one cancellation lifetime")
	}
	if !bytes.Equal(refreshedDashboard.shell, servingDashboard.shell) || !bytes.Equal(refreshedDashboard.viewModel, servingDashboard.viewModel) || refreshedDashboard.viewModelDigest != servingDashboard.viewModelDigest {
		t.Fatal("same dashboard key did not retain its coherent exact bytes and digest")
	}
	if calls := renderer.callCount(); calls != 2 {
		t.Fatalf("same semantic revision render calls = %d, want 2", calls)
	}
	if bytes.Equal(refreshedSnapshot.body, servingSnapshot.body) {
		t.Fatal("generated_at change did not refresh nonconditional snapshot body")
	}
	select {
	case <-servingSnapshot.ctx.Done():
		t.Fatal("same semantic revision canceled an in-flight snapshot reader")
	default:
	}
	select {
	case <-servingDashboard.ctx.Done():
		t.Fatal("same semantic revision canceled an in-flight shell reader")
	default:
	}
}

func TestSameRevisionSemanticDashboardChangeReplacesWholeGeneration(t *testing.T) {
	t.Parallel()
	renderer := &countingRenderer{shell: []byte("first shell"), viewModel: []byte(`{"semantic":1}`), fingerprint: "content:first"}
	server, err := New(DefaultConfig(), &fakeReader{}, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	snapshot := testSnapshot("stable-operational-revision")
	if err := server.publish(t.Context(), snapshot); err != nil {
		t.Fatalf("first publish error = %v", err)
	}
	previous := server.store.dashboard()
	renderer.set([]byte("second shell"), []byte(`{"semantic":2}`), "content:second")
	if err := server.publish(t.Context(), snapshot); err != nil {
		t.Fatalf("semantic publish error = %v", err)
	}
	current := server.store.dashboard()
	if current == previous || current.revision != previous.revision || current.contentFingerprint == previous.contentFingerprint ||
		bytes.Equal(current.shell, previous.shell) || bytes.Equal(current.viewModel, previous.viewModel) || current.viewModelDigest == previous.viewModelDigest {
		t.Fatalf("same-revision semantic change did not replace the coherent generation: old=%+v new=%+v", previous, current)
	}
	select {
	case <-previous.ctx.Done():
	default:
		t.Fatal("same-revision semantic change did not cancel superseded dashboard readers")
	}
}

func TestClosedSnapshotStoreRejectsAndCancelsLatePublication(t *testing.T) {
	t.Parallel()

	store := &snapshotStore{}
	store.close()
	snapshotCtx, snapshotCancel := context.WithCancel(context.Background())
	dashboardCtx, dashboardCancel := context.WithCancel(context.Background())
	changed := store.replace(
		&snapshotGeneration{revision: "late", body: []byte("snapshot"), ctx: snapshotCtx, cancel: snapshotCancel},
		&dashboardGeneration{
			revision: "late", contentFingerprint: "content:late", shell: []byte("shell"),
			viewModel: []byte(`{"late":true}`), viewModelDigest: "sha256:late",
			ctx: dashboardCtx, cancel: dashboardCancel,
		},
	)
	if changed || store.get() != nil || store.dashboard() != nil {
		t.Fatal("late publication repopulated a closed snapshot store")
	}
	select {
	case <-snapshotCtx.Done():
	default:
		t.Fatal("closed store did not cancel the late snapshot candidate")
	}
	select {
	case <-dashboardCtx.Done():
	default:
		t.Fatal("closed store did not cancel the late dashboard candidate")
	}
}

func TestRealNetSlowRootWriterIsBoundedAndCanceledByPublication(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.WriteDeadline = 250 * time.Millisecond
	largeShell := bytes.Repeat([]byte("x"), config.MaxShellBytes)
	server, err := New(config, &fakeReader{snapshot: testSnapshot("one")}, nil, fakeRenderer{shell: largeShell}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.refresh(t.Context()); err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	testServer := httptest.NewUnstartedServer(server.Handler())
	testServer.Start()
	t.Cleanup(testServer.Close)
	host, port, err := net.SplitHostPort(testServer.Listener.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	server.mu.Lock()
	server.listenerHost, server.listenerPort = host, port
	server.mu.Unlock()
	connection, err := net.Dial("tcp", testServer.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	})
	if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: localhost:%s\r\nConnection: close\r\n\r\n", port); err != nil {
		t.Fatalf("write request error = %v", err)
	}
	for len(server.writerSlots) == 0 {
		select {
		case <-t.Context().Done():
			t.Fatal("slow root writer never acquired bounded slot")
		default:
			runtime.Gosched()
		}
	}
	oldDashboard := server.store.dashboard()
	if err := server.publish(t.Context(), testSnapshot("two")); err != nil {
		t.Fatalf("publish() error = %v", err)
	}
	select {
	case <-oldDashboard.ctx.Done():
	default:
		t.Fatal("new publication did not cancel slow shell generation")
	}
	for len(server.writerSlots) != 0 {
		select {
		case <-t.Context().Done():
			t.Fatal("slow root writer did not release bounded slot")
		default:
			runtime.Gosched()
		}
	}
}

func TestRealNetSSEDisconnectReleasesClient(t *testing.T) {
	t.Parallel()
	server, _ := newTestServer(t, DefaultConfig())
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(testServer.URL, "http://"))
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("Atoi() error = %v", err)
	}
	server.mu.Lock()
	server.listenerHost, server.listenerPort = "127.0.0.1", strconv.Itoa(port)
	server.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, testServer.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Host = "localhost:" + portText
	response, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "event: revision\n" {
		t.Fatalf("first SSE line = %q, %v", line, err)
	}
	cancel()
	if err := response.Body.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	for server.broker.count() != 0 {
		select {
		case <-t.Context().Done():
			t.Fatalf("SSE disconnect leaked %d clients", server.broker.count())
		default:
			runtime.Gosched()
		}
	}
}

func TestProductionServeKeepsHealthySSEBeyondOrdinaryWriteDeadlineAndJoins(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.Listen = "127.0.0.1:0"
	config.WriteDeadline = 25 * time.Millisecond
	reader := &fakeReader{snapshot: testSnapshot("sse-one"), called: make(chan struct{}, 1)}
	server, err := New(config, reader, nil, fakeRenderer{shell: []byte("ok")}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	select {
	case <-reader.called:
	case <-t.Context().Done():
		t.Fatal("initial snapshot was not read")
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:"+port+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Host = "localhost:" + port
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE request error = %v", err)
	}
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	})
	stream := bufio.NewReader(response.Body)
	if line, err := stream.ReadString('\n'); err != nil || line != "event: revision\n" {
		t.Fatalf("first SSE line = %q, %v", line, err)
	}
	// Real time is the behavior under test: the stream must outlive the
	// ordinary per-write deadline while idle between successful flushes.
	timer := time.NewTimer(3 * config.WriteDeadline)
	select {
	case <-timer.C:
	case <-t.Context().Done():
		timer.Stop()
		t.Fatal("test context ended before SSE lifetime probe")
	}
	if err := server.publish(t.Context(), testSnapshot("sse-two")); err != nil {
		t.Fatalf("publish changed revision error = %v", err)
	}
	found := false
	for i := 0; i < 8; i++ {
		line, readErr := stream.ReadString('\n')
		if readErr != nil {
			t.Fatalf("read changed SSE event error = %v", readErr)
		}
		if strings.Contains(line, testRevision("sse-two")) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("healthy SSE did not deliver a revision after the ordinary timeout")
	}
	cancel()
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close SSE body error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("production Serve did not join with an active SSE client")
	}
	if server.broker.count() != 0 {
		t.Fatalf("shutdown retained %d SSE clients", server.broker.count())
	}
}

func TestProductionServeStalledRootWriteHitsDeadlineAndReleasesBudget(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.Listen = "127.0.0.1:0"
	config.WriteDeadline = 25 * time.Millisecond
	reader := &fakeReader{snapshot: testSnapshot("deadline"), called: make(chan struct{}, 1)}
	server, err := New(config, reader, nil, fakeRenderer{shell: bytes.Repeat([]byte("x"), config.MaxShellBytes)}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	select {
	case <-reader.called:
	case <-t.Context().Done():
		t.Fatal("initial snapshot was not read")
	}
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	})
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: localhost:%s\r\nConnection: close\r\n\r\n", port); err != nil {
		t.Fatalf("write request error = %v", err)
	}
	// Both spin loops below are bounded by the SAME budget, and that symmetry
	// is the point. The first one used to have no bound of its own — it
	// leaned on t.Context(), which is cancelled only when the whole test
	// BINARY is torn down. So a slot that was merely slow to be acquired did
	// not fail this test; it spun until the package's own 10-minute timeout
	// killed every test in internal/localserver and took a release candidate's
	// `make check` down with it. That is what happened on 2026-08-05, on the
	// exact-candidate verification for v0.19.0, and never once in ~20 local
	// runs including under deliberate CPU contention.
	//
	// The budget is deliberately enormous relative to what is being measured
	// (a 25ms write deadline, 600x over). It is not a timing assertion — the
	// second loop makes the timing assertion. It exists so that a genuine
	// product hang fails in seconds, naming what it was waiting for, instead
	// of presenting as an unrelated package-wide timeout in a log nobody
	// associates with this test.
	//
	// The paragraph above used to end "and never once in ~20 local runs
	// including under deliberate CPU contention". **That claim was falsified on
	// 2026-08-10**: this test failed at the 15s budget inside a full
	// `make check`, then passed 3/3 in isolation immediately after — the same
	// signature as the 2026-08-05 incident, one layer in.
	//
	// The budget was not the defect, and raising it would have hidden one. Both
	// loops spun on `runtime.Gosched()`, which yields the goroutine but KEEPS
	// THE P. When `make check` has every P saturated by parallel packages, that
	// spin can starve the very goroutine it waits for: the waiter stays
	// runnable and is rescheduled onto the P the writer needs. A short sleep
	// releases the P outright. The budget is unchanged, so a real hang still
	// fails in seconds with its own name on it.
	const spinBudget = 15 * time.Second

	acquired := time.NewTimer(spinBudget)
	defer acquired.Stop()
	for len(server.writerSlots) == 0 {
		select {
		case <-acquired.C:
			t.Fatalf("stalled writer never acquired a bounded slot within %s", spinBudget)
		case <-t.Context().Done():
			t.Fatal("stalled writer never acquired a bounded slot")
		default:
			// sleep, not Gosched — see the budget comment above.
			time.Sleep(time.Millisecond)
		}
	}
	deadline := time.NewTimer(spinBudget)
	defer deadline.Stop()
	for len(server.writerSlots) != 0 || server.LastError() == nil {
		select {
		case <-deadline.C:
			t.Fatalf("stalled writer did not hit its deadline within %s: slots=%d error=%v", spinBudget, len(server.writerSlots), server.LastError())
		case <-t.Context().Done():
			t.Fatalf("stalled writer did not hit its deadline: slots=%d error=%v", len(server.writerSlots), server.LastError())
		default:
			// sleep, not Gosched — see the budget comment above.
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("Serve did not join after stalled-writer probe")
	}
}

func TestProductionServeStalledDashboardWriteHitsDeadlineAndReleasesSharedBudget(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	config.Listen = "127.0.0.1:0"
	config.WriteDeadline = 25 * time.Millisecond
	config.MaxDashboardBytes = MaximumDashboardBytes
	reader := &fakeReader{snapshot: testSnapshot("dashboard-deadline"), called: make(chan struct{}, 1)}
	renderer := fakeRenderer{
		shell: []byte("dashboard"), viewModel: bytes.Repeat([]byte("x"), config.MaxDashboardBytes), fingerprint: "content:dashboard-deadline",
	}
	server, err := New(config, reader, nil, renderer, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, listener) }()
	select {
	case <-reader.called:
	case <-t.Context().Done():
		t.Fatal("initial snapshot was not read")
	}
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Errorf("close connection: %v", err)
		}
	})
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	if _, err := fmt.Fprintf(connection, "GET /api/v1/dashboard HTTP/1.1\r\nHost: localhost:%s\r\nConnection: close\r\n\r\n", port); err != nil {
		t.Fatalf("write request error = %v", err)
	}

	const spinBudget = 15 * time.Second
	acquired := time.NewTimer(spinBudget)
	defer acquired.Stop()
	for len(server.writerSlots) == 0 {
		select {
		case <-acquired.C:
			t.Fatalf("stalled dashboard writer never acquired a shared slot within %s", spinBudget)
		case <-t.Context().Done():
			t.Fatal("stalled dashboard writer never acquired a shared slot")
		default:
			// Real write-deadline behavior is under test; release the P while the server writes.
			time.Sleep(time.Millisecond)
		}
	}
	deadline := time.NewTimer(spinBudget)
	defer deadline.Stop()
	for len(server.writerSlots) != 0 || server.LastError() == nil {
		select {
		case <-deadline.C:
			t.Fatalf("stalled dashboard writer did not hit its deadline within %s: slots=%d error=%v", spinBudget, len(server.writerSlots), server.LastError())
		case <-t.Context().Done():
			t.Fatalf("stalled dashboard writer did not hit its deadline: slots=%d error=%v", len(server.writerSlots), server.LastError())
		default:
			// See the acquired loop: this wait deliberately observes real timeout semantics.
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-t.Context().Done():
		t.Fatal("Serve did not join after stalled dashboard-writer probe")
	}
}

func TestPollLoopUsesInjectedTickerAndStopsWithoutSleep(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	tickers := newFakeTickerFactory()
	reader := &fakeReader{snapshot: testSnapshot("sha256:one"), called: make(chan struct{}, 2)}
	server, err := New(config, reader, nil, fakeRenderer{shell: []byte("ok")}, tickers)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.pollLoop(ctx)
	}()
	ticker := tickers.await(t, config.Refresh)
	ticker.channel <- time.Now()
	select {
	case <-reader.called:
	case <-t.Context().Done():
		t.Fatal("poll did not read snapshot")
	}
	cancel()
	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("poll loop did not stop")
	}
}

// TestBackgroundRefreshFailureIsVisibleToBothChannels pins that a server whose
// refresh loop is failing stops looking healthy.
//
// It used to be completely silent: keepalives flowed, /api/v1/snapshot kept
// answering 200, and the body stayed frozen at the last good generation for as
// long as the process ran. The error was stored in a field nothing read, so a
// consumer polling the dashboard could not tell live data from data that
// stopped updating an hour ago.
func TestBackgroundRefreshFailureIsVisibleToBothChannels(t *testing.T) {
	t.Parallel()
	var operatorLog bytes.Buffer
	config := DefaultConfig()
	config.ErrorLog = &operatorLog
	reader := &fakeReader{snapshot: testSnapshot("sha256:one")}
	server, err := New(config, reader, nil, fakeRenderer{shell: []byte("dashboard")}, newFakeTickerFactory())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.publish(t.Context(), testSnapshot("sha256:one")); err != nil {
		t.Fatalf("publish error = %v", err)
	}

	healthy := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	if healthy.header.Get(StaleHeader) != "" {
		t.Fatalf("a healthy server marked its snapshot stale: %q", healthy.header.Get(StaleHeader))
	}

	reader.setError(errors.New("read committed evidence: mirror is unreadable"))
	// Two failures, one transition: the operator channel must not emit a line
	// per tick — at the minimum refresh interval that is four lines a second.
	for range 2 {
		if refreshErr := server.refresh(t.Context()); refreshErr != nil {
			server.recordError(refreshErr)
		}
	}

	stale := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	if stale.status != http.StatusOK {
		t.Fatalf("a stale server stopped answering: status = %d", stale.status)
	}
	if got := stale.header.Get(StaleHeader); !strings.Contains(got, "mirror is unreadable") {
		t.Fatalf("%s = %q, want the failing reason", StaleHeader, got)
	}
	if got := strings.Count(operatorLog.String(), "background refresh is failing"); got != 1 {
		t.Fatalf("operator log has %d failure lines, want exactly one per transition: %q", got, operatorLog.String())
	}

	reader.setError(nil)
	if refreshErr := server.refresh(t.Context()); refreshErr != nil {
		t.Fatalf("recovered refresh error = %v", refreshErr)
	}
	server.recordSuccess()
	recovered := request(t, server, http.MethodGet, "/api/v1/snapshot", false)
	if recovered.header.Get(StaleHeader) != "" {
		t.Fatalf("a recovered server still marks its snapshot stale: %q", recovered.header.Get(StaleHeader))
	}
	if !strings.Contains(operatorLog.String(), "recovered") {
		t.Fatalf("operator log never reported recovery: %q", operatorLog.String())
	}
}
