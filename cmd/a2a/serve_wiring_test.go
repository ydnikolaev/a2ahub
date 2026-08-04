package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/html"
	"github.com/ydnikolaev/a2ahub/internal/localserver"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/operationalsource"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestOperationalPendingDecoderProjectsOnlySafeOutcome(t *testing.T) {
	t.Parallel()
	raw, err := space.EncodeWriteResultJournal(space.WriteResult{
		ArtifactIDs: []string{"XA-atlas-status"}, Branch: "a2a/status", CommitSHA: "abc123",
		MergeMethod: host.MergeMethodSquash, PRNumber: 7, PRURL: "https://example.invalid/pr/7",
		Stage: space.WriteStagePRCreated, State: space.WriteStateNeedsAttention,
		RemainingAction: space.RemainingActionResolvePR, Note: "private provider detail",
	})
	if err != nil {
		t.Fatalf("EncodeWriteResultJournal: %v", err)
	}
	got, err := (operationalPendingDecoder{}).DecodePending(raw)
	if err != nil {
		t.Fatalf("DecodePending: %v", err)
	}
	if got.Stage != "pr-created" || got.State != "needs-attention" || got.RemainingAction != "resolve-pr" {
		t.Fatalf("safe pending projection = %#v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private provider detail", "a2a/status", "example.invalid", "abc123"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("pending projection leaked %q in %s", secret, encoded)
		}
	}
}

func TestStaticHTMLConsumesExactProductionOperationalSnapshot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	p := paths{
		projectConfig: filepath.Join(root, ".a2a", "config.yaml"),
		machineConfig: filepath.Join(root, "machine.yaml"),
		projectRoot:   root,
		staging:       filepath.Join(root, ".a2a", "staging"),
	}
	clock := fixedOperationalClock{now: time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)}
	store := cache.NewStore("atlas", cacheDirOf(p), nil, clock.Now, time.Minute)
	runtimeState, err := buildOperationalRuntimeWithClock(p, store, clock)
	if err != nil {
		t.Fatalf("buildOperationalRuntime: %v", err)
	}
	if runtimeState.historyValidator == nil {
		t.Fatal("buildOperationalRuntime: canonical contract history validator was not injected")
	}
	defer func() {
		if err := runtimeState.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()
	want, err := runtimeState.source.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.NewHtmlCommandWithOperationalAndContractHistory(store, runtimeState.source, runtimeState.historyValidator).Run(context.Background(), []string{"--json", "--no-open"}, cli.IO{
		Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("HTML command = %d, stderr = %q", code, stderr.String())
	}
	var data html.Data
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		t.Fatalf("decode DATA: %v", err)
	}
	wantJSON, err := operational.CanonicalJSON(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := operational.CanonicalJSON(data.Operational)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("static snapshot differs from production source\nwant %s\n got %s", wantJSON, gotJSON)
	}
}

func TestProductionOperationalSnapshotRefreshesFromSecondCloneOverSSE(t *testing.T) {
	t.Parallel()

	const (
		spaceID     = "fixture-space"
		threadID    = "thread:beta-20260803-attn"
		workID      = "work:01K20ABCDEFHJKMNPQRSTVWXYZ"
		localWorkID = "work:01K20ABCDEFHJKMNPQRSTVWXYA"
		itemID      = "XW-beta-20260803-attn"
	)
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	clock := fixedOperationalClock{now: now}
	fixture := spacefixture.New(t, "atlas", "beta")
	seedAttentionFixture(t, fixture.Clone("beta"), itemID, threadID)
	if err := space.CloneOrFetch(t.Context(), fixture.Clone("atlas"), fixture.RemoteURL()); err != nil {
		t.Fatalf("initial CloneOrFetch: %v", err)
	}

	manifestRaw, err := os.ReadFile(filepath.Join(fixture.Clone("atlas"), "space.yaml"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := space.ParseManifest(manifestRaw)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	root := t.TempDir()
	p := paths{
		projectConfig: filepath.Join(root, ".a2a", "config.yaml"),
		machineConfig: filepath.Join(root, "machine.yaml"),
		projectRoot:   root,
		staging:       filepath.Join(root, ".a2a", "staging"),
	}
	store := cache.NewStore("atlas", cacheDirOf(p), []cache.SpaceMirror{{
		SpaceID: spaceID, Dir: fixture.Clone("atlas"), RepoURL: fixture.RemoteURL(), Manifest: manifest,
	}}, clock.Now, time.Minute)
	runtimeState, err := buildOperationalRuntimeWithClock(p, store, clock)
	if err != nil {
		t.Fatalf("buildOperationalRuntimeWithClock: %v", err)
	}
	defer func() {
		if closeErr := runtimeState.Close(); closeErr != nil {
			t.Errorf("close runtime: %v", closeErr)
		}
	}()

	initial, err := runtimeState.source.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("initial production snapshot: %v", err)
	}
	staticData := renderStaticOperationalData(t, store, runtimeState.source)
	assertCanonicalSnapshotEqual(t, staticData.Operational, initial, "static HTML")
	initialAttention := attentionFingerprintOf(staticData.Inbox)
	initialProtocol := protocolForThread(t, initial, threadID)
	if len(initialAttention) == 0 || initialAttention[0].ID != itemID {
		t.Fatalf("fixture attention queue = %#v, want first item %s", initialAttention, itemID)
	}
	if len(initialAttention[0].Reasons) == 0 || initialAttention[0].Prompt == nil || len(initialAttention[0].Prompt.Moves) == 0 {
		t.Fatalf("fixture attention item lacks non-vacuous reasons/prompt/actions: %#v", initialAttention[0])
	}

	config := localserver.DefaultConfig()
	config.Listen = "127.0.0.1:0"
	config.Refresh = localserver.MinimumRefresh
	tickers := newControlledTickerFactory()
	server, err := localserver.New(config, runtimeState.source, runtimeState.source, runtimeState.renderer, tickers)
	if err != nil {
		t.Fatalf("localserver.New: %v", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", config.Listen)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveCtx, stopServer := context.WithCancel(t.Context())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serveCtx, listener) }()
	refreshTicker := tickers.await(t, config.Refresh)
	defer func() {
		stopServer()
		select {
		case serveErr := <-serveDone:
			if serveErr != nil {
				t.Errorf("Serve: %v", serveErr)
			}
		case <-time.After(5 * time.Second):
			// This timeout guards a real network/server join, not state synchronization.
			t.Error("Serve did not join within protocol cleanup bound")
		}
	}()
	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 10 * time.Second}

	initialBody, initialETag := getSnapshotEventually(t, client, baseURL)
	if initialETag != `W/"`+initial.Revision+`"` {
		t.Fatalf("initial ETag = %q, want weak canonical revision ETag", initialETag)
	}
	var servedInitial operational.Snapshot
	if err := json.Unmarshal(initialBody, &servedInitial); err != nil {
		t.Fatalf("decode initial /snapshot: %v", err)
	}
	assertCanonicalSnapshotEqual(t, servedInitial, initial, "/api/v1/snapshot")

	sseRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/v1/events", nil)
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	sseResponse, err := (&http.Client{}).Do(sseRequest)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	defer func() {
		if closeErr := sseResponse.Body.Close(); closeErr != nil {
			t.Errorf("close SSE response: %v", closeErr)
		}
	}()
	if sseResponse.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d", sseResponse.StatusCode)
	}
	sseReader := bufio.NewReader(io.LimitReader(sseResponse.Body, 64<<10))
	if revision := readRevisionEvent(t, sseReader); revision != initial.Revision {
		t.Fatalf("initial SSE revision = %q, want %q", revision, initial.Revision)
	}

	seedCommittedWorkCheckpoint(t, fixture.Clone("beta"), itemID, threadID, workID)
	forceMirrorRefreshDue(t, fixture.Clone("atlas"), now)
	if _, syncErr := runtimeState.source.Sync(t.Context()); syncErr != nil {
		t.Fatalf("production source sync: %v", syncErr)
	}
	refreshTicker.tick()
	newRevision := readRevisionEvent(t, sseReader)
	if newRevision == initial.Revision {
		t.Fatalf("SSE repeated initial revision %q after committed checkpoint", newRevision)
	}

	conditionalRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/v1/snapshot", nil)
	if err != nil {
		t.Fatalf("new conditional request: %v", err)
	}
	conditionalRequest.Header.Set("If-None-Match", initialETag)
	conditionalResponse, err := client.Do(conditionalRequest)
	if err != nil {
		t.Fatalf("conditional GET: %v", err)
	}
	conditionalBody := readBoundedResponse(t, conditionalResponse.Body, operational.MaximumEncodedSnapshot)
	if closeErr := conditionalResponse.Body.Close(); closeErr != nil {
		t.Fatalf("close conditional response: %v", closeErr)
	}
	if conditionalResponse.StatusCode != http.StatusOK {
		t.Fatalf("conditional GET status = %d, want 200 for changed ETag", conditionalResponse.StatusCode)
	}
	var changed operational.Snapshot
	if err := json.Unmarshal(conditionalBody, &changed); err != nil {
		t.Fatalf("decode changed /snapshot: %v", err)
	}
	if changed.Revision != newRevision {
		t.Fatalf("conditional snapshot revision = %q, SSE announced %q", changed.Revision, newRevision)
	}
	wantChanged, err := runtimeState.source.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("changed production snapshot: %v", err)
	}
	assertCanonicalSnapshotEqual(t, changed, wantChanged, "changed conditional snapshot")
	assertCommittedWorkVisible(t, changed, threadID, workID)
	if got := protocolForThread(t, changed, threadID); !reflect.DeepEqual(got, initialProtocol) {
		t.Fatalf("work checkpoint changed independent protocol truth\ninitial: %#v\nchanged: %#v", initialProtocol, got)
	}

	projectID, err := space.CanonicalProjectID(root)
	if err != nil {
		t.Fatalf("CanonicalProjectID: %v", err)
	}
	keys, err := space.NewWorkLeaseKeySource(root)
	if err != nil {
		t.Fatalf("NewWorkLeaseKeySource: %v", err)
	}
	identity := workreport.Identity{
		ProjectID: projectID, Space: spaceID, Thread: threadID, WorkID: localWorkID,
		Actor: workreport.Actor{Kind: "agent", Name: "local-codex", System: "atlas", Model: "test", Session: "session:01K20ABCDEFHJKMNPQRSTVWXYA"},
	}
	identity.LeaseKey, err = keys.LeaseKey(identity)
	if err != nil {
		t.Fatalf("LeaseKey: %v", err)
	}
	lease := workreport.Lease{
		SchemaVersion: workreport.SchemaVersion, Identity: identity, SubjectRef: itemID,
		Mode: workreport.ModeTesting, Summary: "Testing the local operational projection",
		StartedAt: now, RenewedAt: now, ExpiresAt: now.Add(workreport.DefaultTTL),
		HeartbeatSequence: 1, SemanticSequence: 1,
	}
	if _, err := runtimeState.leases.CompareAndSwap(t.Context(), identity.LeaseKey, "", &lease); err != nil {
		t.Fatalf("seed local heartbeat: %v", err)
	}
	refreshTicker.tick()
	localRevision := readRevisionEvent(t, sseReader)
	if localRevision == newRevision {
		t.Fatalf("SSE did not advance for local-only heartbeat: %q", localRevision)
	}
	localBody, localETag := getSnapshotEventually(t, client, baseURL)
	if localETag != `W/"`+localRevision+`"` {
		t.Fatalf("local heartbeat ETag = %q, want revision %q", localETag, localRevision)
	}
	var localSnapshot operational.Snapshot
	if err := json.Unmarshal(localBody, &localSnapshot); err != nil {
		t.Fatalf("decode local heartbeat snapshot: %v", err)
	}
	assertCurrentWorkVisible(t, localSnapshot, threadID, localWorkID)

	isolatedRoot := t.TempDir()
	isolatedPaths := paths{
		projectConfig: filepath.Join(isolatedRoot, ".a2a", "config.yaml"),
		machineConfig: filepath.Join(isolatedRoot, "machine.yaml"),
		projectRoot:   isolatedRoot, staging: filepath.Join(isolatedRoot, ".a2a", "staging"),
	}
	isolatedStore := cache.NewStore("beta", cacheDirOf(isolatedPaths), []cache.SpaceMirror{{
		SpaceID: spaceID, Dir: fixture.Clone("beta"), RepoURL: fixture.RemoteURL(), Manifest: manifest,
	}}, clock.Now, time.Minute)
	isolatedRuntime, err := buildOperationalRuntimeWithClock(isolatedPaths, isolatedStore, clock)
	if err != nil {
		t.Fatalf("build isolated operational runtime: %v", err)
	}
	defer func() {
		if closeErr := isolatedRuntime.Close(); closeErr != nil {
			t.Errorf("close isolated runtime: %v", closeErr)
		}
	}()
	isolated, err := isolatedRuntime.source.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("isolated Snapshot: %v", err)
	}
	assertWorkAbsent(t, isolated, threadID, localWorkID)

	finalStatic := renderStaticOperationalData(t, store, runtimeState.source)
	assertCanonicalSnapshotEqual(t, finalStatic.Operational, localSnapshot, "final static/server snapshot")
	if got := attentionFingerprintOf(finalStatic.Inbox); !reflect.DeepEqual(got, initialAttention) {
		t.Fatalf("local heartbeat changed attention identity/order/reasons/prompts/actions\ninitial: %#v\nchanged: %#v", initialAttention, got)
	}

	rootRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/", nil)
	if err != nil {
		t.Fatalf("new dashboard request: %v", err)
	}
	rootResponse, err := client.Do(rootRequest)
	if err != nil {
		t.Fatalf("dashboard GET: %v", err)
	}
	rootBody := readBoundedResponse(t, rootResponse.Body, localserver.DefaultMaxShellBytes)
	if closeErr := rootResponse.Body.Close(); closeErr != nil {
		t.Fatalf("close dashboard response: %v", closeErr)
	}
	if rootResponse.StatusCode != http.StatusOK {
		t.Fatalf("dashboard status = %d", rootResponse.StatusCode)
	}
	changedData := decodeDashboardData(t, rootBody)
	if got := attentionFingerprintOf(changedData.Inbox); !reflect.DeepEqual(got, initialAttention) {
		t.Fatalf("operational checkpoint changed attention identity/order/reasons/prompts/actions\ninitial: %#v\nchanged: %#v", initialAttention, got)
	}
}

func protocolForThread(t *testing.T, snapshot operational.Snapshot, threadID string) operational.Protocol {
	t.Helper()
	for _, row := range snapshot.Timeline {
		if row.Thread == threadID {
			return row.Protocol
		}
	}
	t.Fatalf("thread %s absent from snapshot", threadID)
	return operational.Protocol{}
}

func assertCurrentWorkVisible(t *testing.T, snapshot operational.Snapshot, threadID, workID string) {
	t.Helper()
	for _, row := range snapshot.Timeline {
		if row.Thread != threadID {
			continue
		}
		for _, work := range row.Work {
			if work.WorkID == workID && work.Current && work.Freshness == operational.FreshnessLocalCurrent {
				return
			}
		}
	}
	t.Fatalf("current local work %s absent from thread %s", workID, threadID)
}

func assertWorkAbsent(t *testing.T, snapshot operational.Snapshot, threadID, workID string) {
	t.Helper()
	for _, row := range snapshot.Timeline {
		if row.Thread != threadID {
			continue
		}
		for _, work := range row.Work {
			if work.WorkID == workID {
				t.Fatalf("machine-local work %s leaked into isolated runtime", workID)
			}
		}
	}
}

type attentionFingerprint struct {
	ID      string
	Reasons []string
	Prompt  *html.AgentPrompt
}

func attentionFingerprintOf(items []html.Item) []attentionFingerprint {
	out := make([]attentionFingerprint, 0, len(items))
	for _, item := range items {
		out = append(out, attentionFingerprint{
			ID: item.ID, Reasons: append([]string(nil), item.Reasons...), Prompt: item.Prompt,
		})
	}
	return out
}

func renderStaticOperationalData(t *testing.T, store *cache.Store, source *operationalsource.Source) html.Data {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.NewHtmlCommandWithOperational(store, source).Run(t.Context(), []string{"--json", "--no-open"}, cli.IO{
		Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("HTML command = %d, stderr = %q", code, stderr.String())
	}
	var data html.Data
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		t.Fatalf("decode static DATA: %v", err)
	}
	return data
}

func assertCanonicalSnapshotEqual(t *testing.T, got, want operational.Snapshot, label string) {
	t.Helper()
	gotJSON, err := operational.CanonicalJSON(got)
	if err != nil {
		t.Fatalf("canonical %s: %v", label, err)
	}
	wantJSON, err := operational.CanonicalJSON(want)
	if err != nil {
		t.Fatalf("canonical expected snapshot: %v", err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("%s differs from production source\nwant %s\n got %s", label, wantJSON, gotJSON)
	}
}

func getSnapshotEventually(t *testing.T, client *http.Client, baseURL string) ([]byte, string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/api/v1/snapshot", nil)
		if err != nil {
			t.Fatalf("new snapshot request: %v", err)
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			body := readBoundedResponse(t, response.Body, operational.MaximumEncodedSnapshot)
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Fatalf("close initial snapshot response: %v", closeErr)
			}
			if response.StatusCode == http.StatusOK {
				return body, response.Header.Get("ETag")
			}
		}
		select {
		case <-t.Context().Done():
			t.Fatal("test context ended before server became ready")
		case <-deadline.C:
			// This timeout bounds real loopback startup, not state propagation.
			t.Fatal("server did not expose its initial snapshot")
		case <-ticker.C:
		}
	}
}

func readRevisionEvent(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	fields := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE revision event: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ": ")
		if !ok {
			t.Fatalf("non-canonical SSE line %q", line)
		}
		fields[name] = value
	}
	if len(fields) != 3 || fields["event"] != "revision" || fields["id"] == "" || fields["data"] == "" {
		t.Fatalf("SSE event leaked more than revision metadata: %#v", fields)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(fields["data"]), &payload); err != nil {
		t.Fatalf("decode SSE data: %v", err)
	}
	if len(payload) != 1 || payload["revision"] != fields["id"] {
		t.Fatalf("SSE data is not revision-only: %#v", payload)
	}
	return fields["id"]
}

type controlledTicker struct {
	channel chan time.Time
}

func (t *controlledTicker) C() <-chan time.Time { return t.channel }
func (t *controlledTicker) Stop()               {}
func (t *controlledTicker) tick()               { t.channel <- time.Now() }

type controlledTickerFactory struct {
	mu      sync.Mutex
	tickers map[time.Duration][]*controlledTicker
	created chan time.Duration
}

func newControlledTickerFactory() *controlledTickerFactory {
	return &controlledTickerFactory{
		tickers: make(map[time.Duration][]*controlledTicker),
		created: make(chan time.Duration, 8),
	}
}

func (f *controlledTickerFactory) NewTicker(interval time.Duration) localserver.Ticker {
	ticker := &controlledTicker{channel: make(chan time.Time, 4)}
	f.mu.Lock()
	f.tickers[interval] = append(f.tickers[interval], ticker)
	f.mu.Unlock()
	f.created <- interval
	return ticker
}

func (f *controlledTickerFactory) await(t *testing.T, interval time.Duration) *controlledTicker {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		available := f.tickers[interval]
		if len(available) > 0 {
			ticker := available[0]
			f.mu.Unlock()
			return ticker
		}
		f.mu.Unlock()
		select {
		case <-t.Context().Done():
			t.Fatal("test context ended before controlled ticker was created")
		case <-deadline.C:
			t.Fatalf("controlled ticker %s was not created", interval)
		case <-f.created:
		}
	}
}

func readBoundedResponse(t *testing.T, body io.Reader, maximum int) []byte {
	t.Helper()
	raw, err := io.ReadAll(io.LimitReader(body, int64(maximum)+1))
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if len(raw) > maximum {
		t.Fatalf("response exceeds %d bytes", maximum)
	}
	return raw
}

func decodeDashboardData(t *testing.T, page []byte) html.Data {
	t.Helper()
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
	var data html.Data
	if err := json.Unmarshal(page[start:start+end], &data); err != nil {
		t.Fatalf("decode rendered DATA: %v", err)
	}
	return data
}

func assertCommittedWorkVisible(t *testing.T, snapshot operational.Snapshot, threadID, workID string) {
	t.Helper()
	for _, row := range snapshot.Timeline {
		if row.Thread != threadID {
			continue
		}
		for _, work := range row.Work {
			if work.WorkID == workID && work.Source == "committed-checkpoint" && work.Current {
				return
			}
		}
	}
	t.Fatalf("current committed work %s absent from thread %s: %#v", workID, threadID, snapshot.Timeline)
}

func seedAttentionFixture(t *testing.T, clone, itemID, threadID string) {
	t.Helper()
	artifact := fmt.Sprintf(`---
schema: envelope/v1
id: %s
type: work_request
title: Investigate the stalled ingest
space: fixture-space
from: beta
to: [atlas]
actor: {kind: agent, name: beta-agent, session: "session:01K20ABCDEFHJKMNPQRSTVWXY1"}
created: 2026-08-03T10:00:00Z
priority: p1
blocking: true
classification: internal
thread: %s
---
Atlas must inspect the ingest failure and report the next safe step.
`, itemID, threadID)
	event := fmt.Sprintf(`schema: event/v1
event: 01K20ABCDEFHJKMNPQRSTVWXY1
space: fixture-space
subject: %s
transition: submit
state: submitted
actor: {kind: agent, name: beta-agent, system: beta, session: "session:01K20ABCDEFHJKMNPQRSTVWXY1"}
at: 2026-08-03T10:01:00Z
`, itemID)
	commitFixtureFiles(t, clone, "fixture: attention item", map[string]string{
		filepath.ToSlash(filepath.Join("beta", "exchanges", itemID+".md")): artifact,
		"beta/events/2026/01K20ABCDEFHJKMNPQRSTVWXY1.yaml":                 event,
	})
}

func seedCommittedWorkCheckpoint(t *testing.T, clone, subjectID, threadID, workID string) {
	t.Helper()
	const checkpointID = "XA-beta-20260803-checkpoint"
	artifact := fmt.Sprintf(`---
schema: envelope/v2
id: %s
type: announcement
title: Implementing the ingest fix
space: fixture-space
from: beta
to: [atlas]
actor: {kind: agent, name: beta-worker, model: test-agent, session: "session:01K20ABCDEFHJKMNPQRSTVWXY2"}
created: 2026-08-03T11:00:00Z
category: status
priority: p2
blocking: false
ack_requested: false
classification: internal
thread: %s
refs: [{ref: %s}]
work:
  id: %s
  semantic_sequence: 1
  mode: implementing
  subject_ref: %s
  summary: Implementing the verified ingest repair
  reported_at: 2026-08-03T11:00:00Z
  valid_until: 2026-08-04T11:00:00Z
---
Implementing the verified ingest repair.
`, checkpointID, threadID, subjectID, workID, subjectID)
	event := fmt.Sprintf(`schema: event/v1
event: 01K20ABCDEFHJKMNPQRSTVWXY2
space: fixture-space
subject: %s
transition: publish
state: published
actor: {kind: agent, name: beta-worker, system: beta, model: test-agent, session: "session:01K20ABCDEFHJKMNPQRSTVWXY2"}
at: 2026-08-03T11:00:00Z
produced_by: {tool: a2a, version: "test"}
`, checkpointID)
	commitFixtureFiles(t, clone, "fixture: committed work checkpoint", map[string]string{
		filepath.ToSlash(filepath.Join("beta", "exchanges", checkpointID+".md")): artifact,
		"beta/events/2026/01K20ABCDEFHJKMNPQRSTVWXY2.yaml":                       event,
	})
}

func commitFixtureFiles(t *testing.T, clone, message string, files map[string]string) {
	t.Helper()
	paths := make([]string, 0, len(files))
	for rel, content := range files {
		full := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir fixture path: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture path: %v", err)
		}
		paths = append(paths, rel)
	}
	fixtureGit(t, clone, append([]string{"add", "--"}, paths...)...)
	fixtureGit(t, clone, "commit", "-m", message)
	fixtureGit(t, clone, "push", "origin", "HEAD:main")
}

func fixtureGit(t *testing.T, clone string, args ...string) {
	t.Helper()
	command := exec.Command("git", gitfixture.Args(args...)...)
	command.Dir = clone
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func forceMirrorRefreshDue(t *testing.T, mirror string, now time.Time) {
	t.Helper()
	old := now.Add(-cache.ReadRefreshTTLForTest - time.Minute)
	path := filepath.Join(mirror, ".git", "FETCH_HEAD")
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("age mirror FETCH_HEAD: %v", err)
	}
}

type fixedOperationalClock struct{ now time.Time }

func (clock fixedOperationalClock) Now() time.Time { return clock.now }
