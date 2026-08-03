package operationalsource

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type fakeCacheReader struct {
	evidence cache.OperationalEvidence
	err      error
}

type mutableCacheReader struct {
	mu       sync.RWMutex
	evidence cache.OperationalEvidence
}

func (reader *mutableCacheReader) OperationalEvidence(context.Context) (cache.OperationalEvidence, error) {
	reader.mu.RLock()
	defer reader.mu.RUnlock()
	return reader.evidence, nil
}

func (reader *mutableCacheReader) setRevision(space, revision string) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for index := range reader.evidence.Sources {
		if reader.evidence.Sources[index].Space == space {
			reader.evidence.Sources[index].Revision = revision
		}
	}
}

func (reader fakeCacheReader) OperationalEvidence(context.Context) (cache.OperationalEvidence, error) {
	return reader.evidence, reader.err
}

type fakeLeaseReader struct {
	bySpace map[string][]workreport.Lease
	errors  map[string]error
}

func (reader fakeLeaseReader) ListWork(_ context.Context, _, space, _ string, includeExpired bool) ([]workreport.Lease, error) {
	if !includeExpired {
		panic("operationalsource did not request expired leases")
	}
	if err := reader.errors[space]; err != nil {
		return nil, err
	}
	return append([]workreport.Lease(nil), reader.bySpace[space]...), nil
}

type fakePendingDecoder struct {
	result PendingResult
	err    error
}

func (decoder fakePendingDecoder) DecodePending([]byte) (PendingResult, error) {
	return decoder.result, decoder.err
}

type fakeFetcher struct {
	results []SyncResult
	err     error
}

type blockingFetcher struct {
	started chan struct{}
	release chan struct{}
	reader  *mutableCacheReader
}

func (fetcher blockingFetcher) Sync(context.Context) ([]SyncResult, error) {
	fetcher.reader.setRevision("space-a", "revision-after-fetch")
	close(fetcher.started)
	<-fetcher.release
	return []SyncResult{{Space: "space-a"}}, nil
}

type fakeCacheSyncer struct{ results []cache.SpaceSyncResult }

func (syncer fakeCacheSyncer) SyncIfStaleResults(context.Context) []cache.SpaceSyncResult {
	return append([]cache.SpaceSyncResult(nil), syncer.results...)
}

func (fetcher fakeFetcher) Sync(context.Context) ([]SyncResult, error) {
	return append([]SyncResult(nil), fetcher.results...), fetcher.err
}

func TestSnapshotKeepsSameThreadIDSeparateAcrossSpaces(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	evidence := baseEvidence(now)
	evidence.Sources = append(evidence.Sources, cache.OperationalSpaceSource{
		Space: "space-b", Revision: "bbbb", SyncedAt: now.Add(-time.Minute), ObservedAt: now, Freshness: "current",
	})
	evidence.Threads = append(evidence.Threads, cache.OperationalThread{
		Space: "space-b", Thread: "thread:same", Title: "Second process", Settled: true,
		Participants: []string{"checkout"}, WaitingOn: []string{}, BlockingBy: []string{},
	})
	source := newTestSource(t, now, []string{"space-a", "space-b"}, evidence, fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}, nil)

	snapshot, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Timeline) != 2 || snapshot.Timeline[0].Space == snapshot.Timeline[1].Space {
		t.Fatalf("same thread id was collapsed across spaces: %#v", snapshot.Timeline)
	}
	if snapshot.Timeline[0].Thread != "thread:same" || snapshot.Timeline[1].Thread != "thread:same" {
		t.Fatalf("unexpected thread rows: %#v", snapshot.Timeline)
	}
}

func TestSnapshotRetainsCommittedRowsWhenLeaseStoreDegrades(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	source := newTestSource(t, now, []string{"space-a"}, baseEvidence(now), fakeLeaseReader{
		errors: map[string]error{"space-a": errors.New("private path: permission denied")},
	}, nil)

	snapshot, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Timeline) != 1 {
		t.Fatalf("committed row was dropped: %#v", snapshot.Timeline)
	}
	local := sourceByKind(t, snapshot, operational.SourceLocalWork)
	if local.Freshness != operational.SourceUnavailable {
		t.Fatalf("local source freshness = %q", local.Freshness)
	}
	raw, _ := operational.CanonicalJSON(snapshot)
	if strings.Contains(string(raw), "private path") || strings.Contains(string(raw), "permission denied") {
		t.Fatalf("raw lease error leaked: %s", raw)
	}
}

func TestSyncFailureRetainsEvidenceAndMarksExactSpaceStale(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	source := newTestSource(t, now, []string{"space-a"}, baseEvidence(now), fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}, fakeFetcher{
		err: errors.New("network unavailable"),
	})

	snapshot, err := source.Sync(context.Background())
	if !errors.Is(err, source.fetcher.(fakeFetcher).err) {
		t.Fatalf("Sync() error = %v", err)
	}
	if len(snapshot.Timeline) != 1 {
		t.Fatalf("sync failure dropped cached evidence: %#v", snapshot.Timeline)
	}
	space := sourceBySpace(t, snapshot, "space-a")
	if space.Freshness != operational.SourceStale {
		t.Fatalf("failed sync source freshness = %q", space.Freshness)
	}
	if len(snapshot.Unavailable) == 0 || snapshot.Unavailable[0].Code != "space-sync-failed" {
		t.Fatalf("missing typed sync degradation: %#v", snapshot.Unavailable)
	}
}

func TestSyncDegradationPersistsAcrossPollsAndClearsOnSuccessfulSync(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	evidence := baseEvidence(now)
	evidence.Sources = append(evidence.Sources, cache.OperationalSpaceSource{
		Space: "space-b", Revision: "bbbb", SyncedAt: now, ObservedAt: now, Freshness: "current",
	})
	evidence.Threads = append(evidence.Threads, cache.OperationalThread{
		Space: "space-b", Thread: "thread:b", Title: "B", Settled: true,
		Participants: []string{}, WaitingOn: []string{}, BlockingBy: []string{},
	})
	leases := fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}
	failure := fakeFetcher{results: []SyncResult{{Space: "space-a", Failed: true}}, err: errors.New("offline")}
	source := newTestSource(t, now, []string{"space-a", "space-b"}, evidence, leases, failure)

	immediate, err := source.Sync(context.Background())
	if !errors.Is(err, failure.err) {
		t.Fatalf("Sync(failure) error = %v", err)
	}
	assertExactSyncDegradation(t, immediate, "space-a")
	for poll := 0; poll < 2; poll++ {
		snapshot, snapshotErr := source.Snapshot(context.Background())
		if snapshotErr != nil {
			t.Fatalf("Snapshot(%d) error = %v", poll, snapshotErr)
		}
		assertExactSyncDegradation(t, snapshot, "space-a")
	}

	source.fetcher = fakeFetcher{results: []SyncResult{{Space: "space-a"}, {Space: "space-b"}}}
	recovered, err := source.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync(success) error = %v", err)
	}
	if sourceBySpace(t, recovered, "space-a").Freshness != operational.SourceCurrent || hasUnavailable(recovered, "space-a", "space-sync-failed") {
		t.Fatalf("successful sync did not clear degradation: sources=%#v unavailable=%#v", recovered.Sources, recovered.Unavailable)
	}
	after, err := source.Snapshot(context.Background())
	if err != nil || hasUnavailable(after, "space-a", "space-sync-failed") {
		t.Fatalf("cleared degradation returned on next poll: err=%v unavailable=%#v", err, after.Unavailable)
	}
}

func TestSyncDegradationClearsWhenExactSourceRevisionAdvances(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	reader := &mutableCacheReader{evidence: baseEvidence(now)}
	source, err := New(Config{
		ProjectID: "sha256:" + strings.Repeat("b", 64), Spaces: []string{"space-a"}, Limits: operational.DefaultLimits(),
	}, reader, fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}, fakePendingDecoder{},
		fakeFetcher{results: []SyncResult{{Space: "space-a", Failed: true}}, err: errors.New("offline")}, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Sync(context.Background()); err == nil {
		t.Fatal("failed sync returned nil error")
	}
	reader.setRevision("space-a", "aaaa-next")

	snapshot, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if sourceBySpace(t, snapshot, "space-a").Freshness != operational.SourceCurrent || hasUnavailable(snapshot, "space-a", "space-sync-failed") {
		t.Fatalf("source transition did not clear old degradation: sources=%#v unavailable=%#v", snapshot.Sources, snapshot.Unavailable)
	}
}

func TestSyncDegradationIsSafeAcrossConcurrentSnapshotPolls(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	failure := fakeFetcher{results: []SyncResult{{Space: "space-a", Failed: true}}, err: errors.New("offline")}
	source := newTestSource(t, now, []string{"space-a"}, baseEvidence(now),
		fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}, failure)
	if _, err := source.Sync(context.Background()); !errors.Is(err, failure.err) {
		t.Fatalf("Sync() error = %v", err)
	}

	const pollers = 8
	errs := make(chan error, pollers)
	var wait sync.WaitGroup
	for range pollers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			snapshot, err := source.Snapshot(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if sourceBySpaceValue(snapshot, "space-a").Freshness != operational.SourceStale ||
				!hasUnavailable(snapshot, "space-a", "space-sync-failed") {
				errs <- errors.New("persisted exact-space degradation was lost")
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotCannotObserveMirrorWhileSyncIsInFlight(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	reader := &mutableCacheReader{evidence: baseEvidence(now)}
	fetcher := blockingFetcher{started: make(chan struct{}), release: make(chan struct{}), reader: reader}
	source, err := New(Config{
		ProjectID: "sha256:" + strings.Repeat("b", 64), Spaces: []string{"space-a"}, Limits: operational.DefaultLimits(),
	}, reader, fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}, fakePendingDecoder{}, fetcher, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}

	syncDone := make(chan error, 1)
	go func() {
		_, syncErr := source.Sync(context.Background())
		syncDone <- syncErr
	}()
	<-fetcher.started

	snapshotDone := make(chan operational.Snapshot, 1)
	go func() {
		snapshot, _ := source.Snapshot(context.Background())
		snapshotDone <- snapshot
	}()
	select {
	case snapshot := <-snapshotDone:
		t.Fatalf("Snapshot returned mixed in-flight fetch state: %#v", snapshot.Sources)
	case <-time.After(50 * time.Millisecond):
		// This is the intentional exclusion window under test: Snapshot must
		// remain blocked until the fetch-and-build transaction is released.
	}
	close(fetcher.release)
	if err := <-syncDone; err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	snapshot := <-snapshotDone
	if got := sourceBySpace(t, snapshot, "space-a").Revision; got != "revision-after-fetch" {
		t.Fatalf("Snapshot revision = %q, want completed fetch revision", got)
	}
}

func TestCacheFetcherMapsTypedSpaceFailureAndPreservesCause(t *testing.T) {
	t.Parallel()
	cause := errors.New("private provider failure")
	fetcher, err := NewCacheFetcher(fakeCacheSyncer{results: []cache.SpaceSyncResult{
		{Space: "space-a", Err: cause}, {Space: "space-b"},
	}})
	if err != nil {
		t.Fatalf("NewCacheFetcher: %v", err)
	}
	results, err := fetcher.Sync(context.Background())
	if !errors.Is(err, cause) {
		t.Fatalf("Sync error = %v, want wrapped cause", err)
	}
	if len(results) != 2 || results[0].Space != "space-a" || !results[0].Failed ||
		results[0].Code != "space-sync-failed" || results[1].Failed {
		t.Fatalf("Sync results = %#v", results)
	}
	if strings.Contains(results[0].Summary, cause.Error()) {
		t.Fatalf("provider error leaked through stable summary: %q", results[0].Summary)
	}
}

func TestPendingProjectionUsesSafeDefaultBeforeFirstAttempt(t *testing.T) {
	t.Parallel()
	source := &Source{pending: fakePendingDecoder{}}
	lease := workreport.Lease{Pending: &workreport.PendingOperation{
		OperationKey: "sha256:" + strings.Repeat("c", 64), Action: workreport.ActionCheckpoint,
	}}

	pending, err := source.pendingProjection(lease)
	if err != nil {
		t.Fatalf("pendingProjection() error = %v", err)
	}
	if pending.Stage != "none" || pending.State != "" || pending.RemainingAction != "retry-push" ||
		pending.Action != "checkpoint" || pending.OperationID != lease.Pending.OperationKey {
		t.Fatalf("pending projection = %#v", pending)
	}
}

func TestSnapshotIsDeterministicAcrossConfiguredSpaceOrder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	evidence := baseEvidence(now)
	evidence.Sources = append(evidence.Sources, cache.OperationalSpaceSource{
		Space: "space-b", Revision: "bbbb", SyncedAt: now, ObservedAt: now, Freshness: "current",
	})
	evidence.Threads = append(evidence.Threads, cache.OperationalThread{
		Space: "space-b", Thread: "thread:b", Title: "B", Settled: true,
		Participants: []string{}, WaitingOn: []string{}, BlockingBy: []string{},
	})
	leases := fakeLeaseReader{bySpace: map[string][]workreport.Lease{}}
	left := newTestSource(t, now, []string{"space-b", "space-a"}, evidence, leases, nil)
	right := newTestSource(t, now, []string{"space-a", "space-b"}, evidence, leases, nil)

	a, err := left.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := right.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	aJSON, _ := operational.CanonicalJSON(a)
	bJSON, _ := operational.CanonicalJSON(b)
	if string(aJSON) != string(bJSON) {
		t.Fatalf("configured space order changed snapshot:\n%s\n%s", aJSON, bJSON)
	}
}

func newTestSource(t *testing.T, now time.Time, spaces []string, evidence cache.OperationalEvidence, leases LeaseReader, fetcher Fetcher) *Source {
	t.Helper()
	source, err := New(Config{
		ProjectID: "sha256:" + strings.Repeat("b", 64), Spaces: spaces, Limits: operational.DefaultLimits(),
	}, fakeCacheReader{evidence: evidence}, leases, fakePendingDecoder{}, fetcher, fixedClock{now})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return source
}

func baseEvidence(now time.Time) cache.OperationalEvidence {
	return cache.OperationalEvidence{
		Sources: []cache.OperationalSpaceSource{{
			Space: "space-a", Revision: "aaaa", SyncedAt: now.Add(-time.Minute), ObservedAt: now, Freshness: "current",
		}},
		Threads: []cache.OperationalThread{{
			Space: "space-a", Thread: "thread:same", Title: "First process", Settled: true,
			Participants: []string{"atlas"}, WaitingOn: []string{}, BlockingBy: []string{},
		}},
		CommittedWork: []cache.OperationalCommittedWork{},
		Unavailable:   []cache.OperationalUnavailable{},
	}
}

func sourceByKind(t *testing.T, snapshot operational.Snapshot, kind operational.SourceKind) operational.Source {
	t.Helper()
	for _, source := range snapshot.Sources {
		if source.Kind == kind {
			return source
		}
	}
	t.Fatalf("source kind %q absent: %#v", kind, snapshot.Sources)
	return operational.Source{}
}

func sourceBySpace(t *testing.T, snapshot operational.Snapshot, space string) operational.Source {
	t.Helper()
	for _, source := range snapshot.Sources {
		if source.Space == space {
			return source
		}
	}
	t.Fatalf("space source %q absent: %#v", space, snapshot.Sources)
	return operational.Source{}
}

func sourceBySpaceValue(snapshot operational.Snapshot, space string) operational.Source {
	for _, source := range snapshot.Sources {
		if source.Space == space {
			return source
		}
	}
	return operational.Source{}
}

func assertExactSyncDegradation(t *testing.T, snapshot operational.Snapshot, degradedSpace string) {
	t.Helper()
	for _, source := range snapshot.Sources {
		if source.Kind != operational.SourceSpace {
			continue
		}
		want := operational.SourceCurrent
		if source.Space == degradedSpace {
			want = operational.SourceStale
		}
		if source.Freshness != want {
			t.Fatalf("space %s freshness = %q, want %q; sources=%#v", source.Space, source.Freshness, want, snapshot.Sources)
		}
	}
	if !hasUnavailable(snapshot, degradedSpace, "space-sync-failed") {
		t.Fatalf("missing persisted degradation for %s: %#v", degradedSpace, snapshot.Unavailable)
	}
	for _, unavailable := range snapshot.Unavailable {
		if unavailable.Code == "space-sync-failed" && unavailable.Space != degradedSpace {
			t.Fatalf("sync degradation leaked to %s: %#v", unavailable.Space, snapshot.Unavailable)
		}
	}
}

func hasUnavailable(snapshot operational.Snapshot, space, code string) bool {
	for _, unavailable := range snapshot.Unavailable {
		if unavailable.Space == space && unavailable.Code == code {
			return true
		}
	}
	return false
}
