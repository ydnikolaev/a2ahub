package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

func testStore(t *testing.T, mirrorDir string) *cache.Store {
	t.Helper()
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: fold.MembershipMember}, {System: "beta", Status: fold.MembershipMember},
	}}
	return cache.NewStore("beta", t.TempDir(), []cache.SpaceMirror{{SpaceID: "fixture-space", Dir: mirrorDir, Manifest: manifest}},
		func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }, 0)
}

// threadFixtureQuestionID / threadFixtureStore seed one committed exchange with
// events, so the thread handler has a real transcript to return rather than a
// single artifact.
const threadFixtureQuestionID = "XQ-axon-20260721-thr1"

func threadFixtureStore(t *testing.T) (*cache.Store, string) {
	t.Helper()
	mirrorDir := t.TempDir()
	writeQuestionArtifact(t, mirrorDir, threadFixtureQuestionID, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, threadFixtureQuestionID, "submit", "axon")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, threadFixtureQuestionID, "acknowledge", "beta")
	return testStore(t, mirrorDir), testFixtureThread
}

func TestInboxHandler(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-a001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	handler := newInboxHandler(testStore(t, mirrorDir))
	result, body, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("inbox handler failed: %v", err)
	}
	if body != "" {
		t.Fatalf("expected no body for a list tool, got %q", body)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("expected itemsWithSkipped, got %T", result)
	}
	if len(out.Items) != 1 || out.Items[0].ID != id {
		t.Fatalf("expected one inbox item for %s, got %+v", id, out.Items)
	}
	if len(out.Skipped) != 0 {
		t.Fatalf("expected no skipped files, got %+v", out.Skipped)
	}
}

func TestOutboxHandlerEmpty(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	handler := newOutboxHandler(testStore(t, mirrorDir))
	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("outbox handler failed: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok || out.Items == nil {
		t.Fatalf("expected a non-nil empty []cache.Item wrapped in itemsWithSkipped, got %#v", result)
	}
	if len(out.Skipped) != 0 {
		t.Fatalf("expected no skipped files, got %+v", out.Skipped)
	}
}

func TestShowHandlerReturnsBodyVerbatim(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-b001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	handler := newShowHandler(testStore(t, mirrorDir))
	args, _ := json.Marshal(ShowInput{Ref: id})
	result, body, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("show handler failed: %v", err)
	}
	if strings.TrimSpace(body) != "body" {
		t.Fatalf("expected the verbatim markdown body %q, got %q", "body", body)
	}
	out, ok := result.(showOutput)
	if !ok {
		t.Fatalf("expected showOutput, got %T", result)
	}
	if out.ID != id {
		t.Fatalf("ID = %q, want %q", out.ID, id)
	}
}

// TestShowV5WarningsAllBranches exercises showV5Warnings' three warning
// branches directly (pure function, no fixture/git dependency — cheap,
// fixture-independent coverage margin).
func TestShowV5WarningsAllBranches(t *testing.T) {
	t.Parallel()

	t.Run("digest_mismatch", func(t *testing.T) {
		t.Parallel()
		out := showV5Warnings(cache.ShowResult{Refs: []cache.RefFact{{Ref: "XR-axon-x#sha256:aaaa", Resolved: true, DigestMismatch: true}}})
		if len(out) != 1 || out[0].Code != "REF-004" {
			t.Fatalf("expected exactly one REF-004 warning, got %+v", out)
		}
	})

	t.Run("pinned_unresolved", func(t *testing.T) {
		t.Parallel()
		out := showV5Warnings(cache.ShowResult{Refs: []cache.RefFact{{Ref: "XR-axon-x#sha256:aaaa", PinnedDigest: "sha256:aaaa", Resolved: false}}})
		if len(out) != 1 || out[0].Code != "REF-008" {
			t.Fatalf("expected exactly one REF-008 warning, got %+v", out)
		}
	})

	t.Run("sync_stale", func(t *testing.T) {
		t.Parallel()
		out := showV5Warnings(cache.ShowResult{SyncStale: true, SyncAge: "10h0m0s"})
		if len(out) != 1 || out[0].Code != "" {
			t.Fatalf("expected exactly one uncoded staleness warning, got %+v", out)
		}
	})

	t.Run("no_warnings", func(t *testing.T) {
		t.Parallel()
		out := showV5Warnings(cache.ShowResult{})
		if len(out) != 0 {
			t.Fatalf("expected zero warnings, got %+v", out)
		}
	})
}

func TestShowHandlerMissingRef(t *testing.T) {
	t.Parallel()
	handler := newShowHandler(testStore(t, t.TempDir()))
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for a missing ref")
	}
}

func TestShowHandlerNotFound(t *testing.T) {
	t.Parallel()
	handler := newShowHandler(testStore(t, t.TempDir()))
	args, _ := json.Marshal(ShowInput{Ref: "XQ-axon-20260721-zzzz"})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for a not-found ref")
	}
}

// TestThreadHandler is the baseline: one artifact on a thread comes back, keyed
// by the thread id. Its fixture used to carry `thread: T-1` — a value that was
// never schema-valid (base.schema.json pins the §3.8 grammar) and only "worked"
// because the pre-P46 reader compared thread ids by raw string equality.
func TestThreadHandler(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-c001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")

	handler := newThreadHandler(testStore(t, mirrorDir))
	args, _ := json.Marshal(ThreadInput{ThreadID: testFixtureThread})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("thread handler failed: %v", err)
	}
	res, ok := result.(threadOutput)
	if !ok {
		t.Fatalf("thread handler returned %T, want threadOutput", result)
	}
	if len(res.Artifacts) != 1 || res.Artifacts[0].ID != id {
		t.Fatalf("expected exactly the one member %s, got %+v", id, res.Artifacts)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("expected no skipped files, got %+v", res.Skipped)
	}
}

// TestThreadInputCarriesOptionalSpace covers §T1's MCP-side obligation: an
// optional `space` field decodes and is accepted by the handler (the
// recovery path for §T4's ambiguity refusal). This asserts the field is
// decoded AND actually narrows the read — the closeout audit found it decoded
// and then dropped, which is worse than not offering it: a caller's narrowing
// intent silently ignored, on the one input that exists to recover from a
// refusal.
func TestThreadInputCarriesOptionalSpace(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"thread_id":"T-1","space":"fixture-space"}`)
	var in ThreadInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("decode ThreadInput: %v", err)
	}
	if in.Space != "fixture-space" {
		t.Fatalf("Space = %q, want %q", in.Space, "fixture-space")
	}

	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-c002"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	handler := newThreadHandler(testStore(t, mirrorDir))

	// The right space renders.
	args, _ := json.Marshal(ThreadInput{ThreadID: testFixtureThread, Space: "fixture-space"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("thread handler with space input failed: %v", err)
	}
	res := result.(threadOutput)
	if len(res.Artifacts) != 1 || res.Artifacts[0].ID != id {
		t.Fatalf("expected exactly the one member %s, got %+v", id, res.Artifacts)
	}
	if res.Space != "fixture-space" {
		t.Fatalf("space = %q, want fixture-space", res.Space)
	}

	// A space that is not connected must NOT quietly fall back to searching
	// everywhere — that is precisely the silent behaviour the audit found.
	argsWrong, _ := json.Marshal(ThreadInput{ThreadID: testFixtureThread, Space: "not-connected"})
	if _, _, err := handler(context.Background(), argsWrong); err == nil {
		t.Fatal("a space input naming no connected space must fail, not silently widen the search")
	}
}

func TestSearchHandler(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-d001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")

	handler := newSearchHandler(testStore(t, mirrorDir))
	args, _ := json.Marshal(SearchInput{Query: id})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("search handler failed: %v", err)
	}
	out := result.(itemsWithSkipped)
	if len(out.Items) != 1 {
		t.Fatalf("expected 1 search hit, got %+v", out.Items)
	}
	if len(out.Skipped) != 0 {
		t.Fatalf("expected no skipped files, got %+v", out.Skipped)
	}
}

// TestWithUpdateNoticeErrorPassthrough proves an inner handler error is
// returned exactly as produced — no notice lookup, result/body untouched
// (spec 19 T4 AMENDED / §11 wave-12c: the wrapper never masks or rewrites a
// tool failure).
func TestWithUpdateNoticeErrorPassthrough(t *testing.T) {
	t.Parallel()

	// Use a GradeAvailable-enabled store (not GradeNone): if withUpdateNotice
	// ever appended the advisory BEFORE checking err, a GradeNone store would
	// still pass this test vacuously (nothing to append). GradeAvailable
	// makes the assertion bite on the actual ordering the wrapper must
	// guard: err short-circuits before any notice lookup/append.
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := cache.NewStore("beta", t.TempDir(), nil, func() time.Time { return now }, 0)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: now, Latest: "0.3.0", Source: "test"}); err != nil {
		t.Fatalf("seed update-check cache: %v", err)
	}
	store.EnableUpdateNotice("0.1.0", cachePath, 6*time.Hour, nil)
	if store.UpdateNotice().Grade != release.GradeAvailable {
		t.Fatalf("test setup: expected GradeAvailable")
	}

	wantErr := errors.New("boom")
	inner := func(_ context.Context, _ json.RawMessage) (any, string, error) {
		return nil, "unused body", wantErr
	}
	wrapped := withUpdateNotice(inner, store)
	result, body, err := wrapped(context.Background(), json.RawMessage(`{}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the inner error passed through unchanged, got %v", err)
	}
	if result != nil {
		t.Fatalf("expected a nil result on error, got %v", result)
	}
	if body != "unused body" {
		t.Fatalf("expected body passed through unchanged on error (no advisory appended), got %q", body)
	}
}

// TestWithUpdateNoticeGradeNoneLeavesBodyUnchanged proves the wrapper is a
// no-op on the body when the store's UpdateNotice grades GradeNone (the
// default: EnableUpdateNotice never called) — the existing mcp parity/
// equivalence tests build stores this way, so this documents why they
// still pass unwrapped through a2a_read.
//
// The "want" (unwrapped) and "got" (wrapped) calls each use their OWN Store
// instance over the SAME mirror: Store.Inbox advances the on-disk read
// cursor as a side effect (an item's New field flips false on a second call
// against the SAME store), which would corrupt a byte-for-byte comparison
// if both calls shared one store.
func TestWithUpdateNoticeGradeNoneLeavesBodyUnchanged(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-e001"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	inner := newInboxHandler(testStore(t, mirrorDir))
	wantResult, wantBody, err := inner(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("inner inbox handler: %v", err)
	}

	wrappedStore := testStore(t, mirrorDir)
	wrapped := withUpdateNotice(newInboxHandler(wrappedStore), wrappedStore)
	gotResult, gotBody, err := wrapped(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("wrapped inbox handler: %v", err)
	}
	if gotBody != wantBody {
		t.Fatalf("GradeNone: expected body unchanged, want %q got %q", wantBody, gotBody)
	}
	wantJSON, _ := json.Marshal(wantResult)
	gotJSON, _ := json.Marshal(gotResult)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("GradeNone: StructuredContent diverged:\nwant %s\ngot  %s", wantJSON, gotJSON)
	}
}

func TestContractsHandler(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	writeMirrorFile(t, mirrorDir, "axon/provides/widget/contract.md",
		"---\nschema: envelope/v1\nid: XC-axon-widget\ntype: contract\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nversion: 1.0.0\ncompat_policy: additive-minor\nschema_format: json-schema\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\npriority: p3\nblocking: false\nclassification: internal\n---\nbody\n")

	handler := newContractsHandler(testStore(t, mirrorDir))
	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("contracts handler failed: %v", err)
	}
	contracts := result.([]cache.ContractInfo)
	if len(contracts) != 1 || contracts[0].ID != "XC-axon-widget" {
		t.Fatalf("expected 1 contract, got %+v", contracts)
	}
}

// TestThreadHandlerReadsTheTranscript is the regression for the closeout
// audit's one HIGH finding: this handler used to call the pre-P46 flat reader,
// so the MCP surface had none of the phase — no events, no commit ordering, no
// open items, and no ambiguity refusal for its own `space` input to recover
// from. The input decoded and was then dropped, which is worse than not
// offering it: a caller's narrowing intent silently ignored.
func TestThreadHandlerReadsTheTranscript(t *testing.T) {
	t.Parallel()
	store, threadID := threadFixtureStore(t)
	handler := newThreadHandler(store)

	args, _ := json.Marshal(ThreadInput{ThreadID: threadID})
	got, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("a2a_thread: %v", err)
	}
	res, ok := got.(threadOutput)
	if !ok {
		t.Fatalf("a2a_thread returned %T, want threadOutput — the flat []Item shape means this surface is still on the old reader", got)
	}
	if res.Thread != threadID {
		t.Fatalf("thread = %q, want %q", res.Thread, threadID)
	}
	if len(res.Transcript) == 0 {
		t.Fatal("empty transcript: the whole point of this surface change is that events and artifacts both appear")
	}
	var sawEvent bool
	for _, e := range res.Transcript {
		if e.Kind == "event" {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Fatal("transcript carries no event entries — the old reader listed artifacts only (spec 46 D2)")
	}
	if res.Flags == nil || res.Unresolved == nil {
		t.Fatal("flags/unresolved must be non-nil empty arrays: an agent asserts completeness on them, and an absent key is indistinguishable from a reader too old to know")
	}
}

// TestThreadHandlerAcceptsMemberArtifactID: a caller pastes whichever id it
// holds. Forcing it to find the thread id first is a determinism tax.
func TestThreadHandlerAcceptsMemberArtifactID(t *testing.T) {
	t.Parallel()
	store, threadID := threadFixtureStore(t)
	handler := newThreadHandler(store)

	args, _ := json.Marshal(ThreadInput{ThreadID: threadFixtureQuestionID})
	got, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("a2a_thread with a member artifact id: %v", err)
	}
	res := got.(threadOutput)
	if res.Thread != threadID {
		t.Fatalf("thread = %q, want %q (resolved from the member id)", res.Thread, threadID)
	}
	if res.ResolvedFrom != threadFixtureQuestionID {
		t.Fatalf("resolved_from = %q, want %q", res.ResolvedFrom, threadFixtureQuestionID)
	}
}

// --- MCP parity: the "skipped" field (defect filed 2026-07-26) -------------
//
// Unlike internal/cli (whose stdout item array must stay byte-identical, so
// the equivalent advisory rides stderr — see cmd_inbox.go's
// inboxWriteUpdateAdvisory doc comment), an MCP tool's StructuredContent IS
// the whole structured result: the skip list is folded into the result
// itself, as its own field (itemsWithSkipped/threadOutput, tools_read.go).
// These tests prove that field is populated for a2a_inbox/a2a_outbox/
// a2a_search/a2a_thread and left empty on a clean mirror.

// skippedFieldBadRelPath is the malformed artifact's space-relative path
// every case below asserts is named in the populated Skipped field.
const skippedFieldBadRelPath = "axon/exchanges/XQ-axon-20260721-bad.md"

// buildSkippedFieldMirror writes a bare mirror tree with one well-formed
// question FROM axon TO beta (reaches a2a_inbox, since testStore's own
// system is "beta") and one well-formed question FROM beta TO axon
// (reaches a2a_outbox, ownedByMe against "beta") — both on testFixtureThread
// so a2a_thread renders them together — plus, when bad is true, one
// artifact whose `thread:` key is written twice (the defect's own shape,
// internal/cache/skipped_test.go's fixture), written via writeMirrorFile
// (raw bytes) rather than any YAML-marshaling helper, which could never
// reproduce a duplicate mapping key.
func buildSkippedFieldMirror(t *testing.T, bad bool) string {
	t.Helper()
	mirrorDir := t.TempDir()
	writeQuestionArtifact(t, mirrorDir, "XQ-axon-20260721-skip1", "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, "XQ-axon-20260721-skip1", "submit", "axon")

	writeMirrorFile(t, mirrorDir, "beta/exchanges/XQ-beta-20260721-skip2.md",
		"---\n"+
			"schema: envelope/v1\n"+
			"id: XQ-beta-20260721-skip2\n"+
			"type: question\n"+
			"title: t\n"+
			"space: fixture-space\n"+
			"from: beta\n"+
			"to: [axon]\n"+
			"thread: "+testFixtureThread+"\n"+
			"actor: {kind: agent, name: bot}\n"+
			"created: 2026-07-21T10:00:00Z\n"+
			"category: clarification\n"+
			"priority: p3\n"+
			"blocking: true\n"+
			"classification: internal\n"+
			"---\nbody\n")
	writeLifecycleEvent(t, mirrorDir, "beta", 1, "XQ-beta-20260721-skip2", "submit", "beta")

	if bad {
		writeMirrorFile(t, mirrorDir, skippedFieldBadRelPath,
			"---\nid: XQ-axon-20260721-bad\nthread: thread:axon:one\nthread: thread:axon:two\n---\nbad\n")
	}
	return mirrorDir
}

// assertSkippedField is the shared assertion every case below uses: on the
// dirty mirror, exactly one entry naming skippedFieldBadRelPath and
// cache.SkipReasonUndecodableYAML; on the clean mirror, none at all (a gate
// that fires on a clean space is a gate people silence).
func assertSkippedField(t *testing.T, got []cache.SkippedFile, wantBad bool) {
	t.Helper()
	if !wantBad {
		if len(got) != 0 {
			t.Fatalf("skipped = %+v, want none (clean mirror)", got)
		}
		return
	}
	if len(got) != 1 || got[0].Path != skippedFieldBadRelPath || got[0].Reason != cache.SkipReasonUndecodableYAML {
		t.Fatalf("skipped = %+v, want exactly one entry naming %q/%q", got, skippedFieldBadRelPath, cache.SkipReasonUndecodableYAML)
	}
}

func TestInboxHandler_SkippedField(t *testing.T) {
	t.Parallel()
	for _, bad := range []bool{false, true} {
		t.Run(boolLabel(bad), func(t *testing.T) {
			t.Parallel()
			store := testStore(t, buildSkippedFieldMirror(t, bad))
			result, _, err := newInboxHandler(store)(context.Background(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("a2a_inbox: %v", err)
			}
			out, ok := result.(itemsWithSkipped)
			if !ok {
				t.Fatalf("a2a_inbox returned %T, want itemsWithSkipped", result)
			}
			assertSkippedField(t, out.Skipped, bad)
		})
	}
}

func TestOutboxHandler_SkippedField(t *testing.T) {
	t.Parallel()
	for _, bad := range []bool{false, true} {
		t.Run(boolLabel(bad), func(t *testing.T) {
			t.Parallel()
			store := testStore(t, buildSkippedFieldMirror(t, bad))
			result, _, err := newOutboxHandler(store)(context.Background(), json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("a2a_outbox: %v", err)
			}
			out, ok := result.(itemsWithSkipped)
			if !ok {
				t.Fatalf("a2a_outbox returned %T, want itemsWithSkipped", result)
			}
			assertSkippedField(t, out.Skipped, bad)
		})
	}
}

func TestSearchHandler_SkippedField(t *testing.T) {
	t.Parallel()
	for _, bad := range []bool{false, true} {
		t.Run(boolLabel(bad), func(t *testing.T) {
			t.Parallel()
			store := testStore(t, buildSkippedFieldMirror(t, bad))
			args, _ := json.Marshal(SearchInput{Query: ""})
			result, _, err := newSearchHandler(store)(context.Background(), args)
			if err != nil {
				t.Fatalf("a2a_search: %v", err)
			}
			out, ok := result.(itemsWithSkipped)
			if !ok {
				t.Fatalf("a2a_search returned %T, want itemsWithSkipped", result)
			}
			assertSkippedField(t, out.Skipped, bad)
		})
	}
}

func TestThreadHandler_SkippedField(t *testing.T) {
	t.Parallel()
	for _, bad := range []bool{false, true} {
		t.Run(boolLabel(bad), func(t *testing.T) {
			t.Parallel()
			store := testStore(t, buildSkippedFieldMirror(t, bad))
			args, _ := json.Marshal(ThreadInput{ThreadID: testFixtureThread})
			result, _, err := newThreadHandler(store)(context.Background(), args)
			if err != nil {
				t.Fatalf("a2a_thread: %v", err)
			}
			out, ok := result.(threadOutput)
			if !ok {
				t.Fatalf("a2a_thread returned %T, want threadOutput", result)
			}
			assertSkippedField(t, out.Skipped, bad)
		})
	}
}

func boolLabel(b bool) string {
	if b {
		return "dirty"
	}
	return "clean"
}

// TestInboxHandler_Overdue keeps the two read surfaces level. The CLI grew
// `--overdue` in the same change; a read filter that exists on one surface
// only is exactly the asymmetry P43 was cut to close, and the cheapest
// moment not to create one is the commit that adds the filter.
func TestInboxHandler_Overdue(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-a002"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	handler := newInboxHandler(testStore(t, mirrorDir))

	// The fixture carries no needed_by, so nothing is overdue — the useful
	// assertion here is that the parameter is READ and CHANGES the query,
	// rather than being accepted and ignored, which is how a mirrored flag
	// usually rots.
	result, _, err := handler(context.Background(), json.RawMessage(`{"overdue":true}`))
	if err != nil {
		t.Fatalf("overdue handler failed: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("expected itemsWithSkipped, got %T", result)
	}
	if len(out.Items) != 0 {
		t.Fatalf("expected no overdue items for a fixture with no needed_by, got %+v", out.Items)
	}
	// Same store, no overdue filter: the item IS there. Without this the
	// assertion above would pass just as well against a handler that always
	// returns nothing.
	plain, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("plain handler failed: %v", err)
	}
	if len(plain.(itemsWithSkipped).Items) != 1 {
		t.Fatalf("control case: expected the item to be visible without --overdue, got %+v", plain)
	}
}

// TestInboxHandler_ActionableAndOverdueRefused mirrors the CLI's refusal.
// Silently answering one of two contradictory questions is worse over MCP
// than on a terminal: no human reads the result, so a caller that meant
// "what is late" and received "what needs my move" acts on the wrong list
// with no signal that it happened.
func TestInboxHandler_ActionableAndOverdueRefused(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	handler := newInboxHandler(testStore(t, mirrorDir))

	_, _, err := handler(context.Background(), json.RawMessage(`{"actionable":true,"overdue":true}`))
	if err == nil {
		t.Fatal("expected a refusal when both actionable and overdue are set")
	}
	if !strings.Contains(err.Error(), "different questions") {
		t.Errorf("error = %v, want it to explain the two questions", err)
	}
}

// --- P4 AC-11: an unreadable AllSkippedFiles reports unmeasured, not
// silence (computed-not-listed-2026-08 P4, §8 row 11) ----------------------
//
// failingSkipStore embeds a real *cache.Store — so Overdue/Inbox/Outbox/
// Search still run against a genuine fixture mirror — and shadows only
// AllSkippedFiles. This is the same "override one method, keep the rest
// real" judgement internal/cli/skipadvisory_unavailable_test.go's own
// header applies, rather than fabricating a *cache.Store whose
// AllSkippedFiles fails via the real filesystem walk: buildIndex's own
// walkArtifacts/walkEvents/commitOrder degrade every read failure they can
// hit to a reported SkippedFile rather than a returned error, by design —
// there is no filesystem fixture that makes AllSkippedFiles itself return
// an error without breaking that design.
type failingSkipStore struct {
	*cache.Store
	err error
}

func (f failingSkipStore) AllSkippedFiles(context.Context) (map[string][]cache.SkippedFile, error) {
	return nil, f.err
}

// assertUnmeasuredWarning is the shared assertion every case below uses:
// exactly one Violation, SeverityUnmeasured, naming the underlying error.
func assertUnmeasuredWarning(t *testing.T, warnings []validate.Violation) {
	t.Helper()
	if len(warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one unmeasured violation", warnings)
	}
	w := warnings[0]
	if w.Severity != validate.SeverityUnmeasured {
		t.Fatalf("severity = %q, want %q", w.Severity, validate.SeverityUnmeasured)
	}
	if !strings.Contains(w.Message, "boom") {
		t.Fatalf("message = %q, want it to name the underlying error", w.Message)
	}
}

// TestInboxHandler_AllSkippedFilesUnavailable covers site :191 (the plain
// a2a_inbox path, tools_read.go).
func TestInboxHandler_AllSkippedFilesUnavailable(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-unmea1"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	store := failingSkipStore{Store: testStore(t, mirrorDir), err: errors.New("boom")}
	handler := newInboxHandler(store)

	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("a2a_inbox: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("a2a_inbox returned %T, want itemsWithSkipped", result)
	}
	// The item list still renders — an unmeasured skip report must not turn
	// a working read into a refusal.
	if len(out.Items) != 1 || out.Items[0].ID != id {
		t.Fatalf("expected the item list to still render, got %+v", out.Items)
	}
	if out.Skipped != nil {
		t.Fatalf("Skipped = %+v, want nil when the report itself could not be computed", out.Skipped)
	}
	assertUnmeasuredWarning(t, out.Warnings)
}

// TestInboxHandlerOverdue_AllSkippedFilesUnavailable covers site :184 (the
// `overdue` branch, tools_read.go) — the one site a plain a2a_inbox call
// (`{}`) never reaches.
func TestInboxHandlerOverdue_AllSkippedFilesUnavailable(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	store := failingSkipStore{Store: testStore(t, mirrorDir), err: errors.New("boom")}
	handler := newInboxHandler(store)

	result, _, err := handler(context.Background(), json.RawMessage(`{"overdue":true}`))
	if err != nil {
		t.Fatalf("a2a_inbox overdue: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("a2a_inbox overdue returned %T, want itemsWithSkipped", result)
	}
	assertUnmeasuredWarning(t, out.Warnings)
}

// TestOutboxHandler_AllSkippedFilesUnavailable covers newOutboxHandler's own
// AllSkippedFiles call site.
func TestOutboxHandler_AllSkippedFilesUnavailable(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	store := failingSkipStore{Store: testStore(t, mirrorDir), err: errors.New("boom")}
	handler := newOutboxHandler(store)

	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("a2a_outbox: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("a2a_outbox returned %T, want itemsWithSkipped", result)
	}
	assertUnmeasuredWarning(t, out.Warnings)
}

// TestSearchHandler_AllSkippedFilesUnavailable covers newSearchHandler's own
// AllSkippedFiles call site.
func TestSearchHandler_AllSkippedFilesUnavailable(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-unmea2"
	writeQuestionArtifact(t, mirrorDir, id, "beta")

	store := failingSkipStore{Store: testStore(t, mirrorDir), err: errors.New("boom")}
	handler := newSearchHandler(store)

	args, _ := json.Marshal(SearchInput{Query: id})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("a2a_search: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("a2a_search returned %T, want itemsWithSkipped", result)
	}
	if len(out.Items) != 1 {
		t.Fatalf("expected the item list to still render, got %+v", out.Items)
	}
	assertUnmeasuredWarning(t, out.Warnings)
}

// TestInboxHandler_WarningsAbsentOnCleanRead is the control case every
// assertion above needs: a normal store, nothing skipped, no failure.
// Warnings must be nil/omitted, not merely empty by convention — the P15
// per-verb StructuredContent byte-identity guarantee (updateNoticeDecorator's
// own doc comment, tools_read.go) means a handler that always emitted a
// Warnings entry would still pass every assertion above while breaking that
// guarantee for the common (clean) case.
func TestInboxHandler_WarningsAbsentOnCleanRead(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-clean1"
	writeQuestionArtifact(t, mirrorDir, id, "beta")
	writeLifecycleEvent(t, mirrorDir, "axon", 0, id, "submit", "axon")

	handler := newInboxHandler(testStore(t, mirrorDir))
	result, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("a2a_inbox: %v", err)
	}
	out, ok := result.(itemsWithSkipped)
	if !ok {
		t.Fatalf("a2a_inbox returned %T, want itemsWithSkipped", result)
	}
	if out.Warnings != nil {
		t.Fatalf("Warnings = %+v, want nil on a clean read", out.Warnings)
	}
	data, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal itemsWithSkipped: %v", err)
	}
	if strings.Contains(string(data), `"warnings"`) {
		t.Fatalf(`expected "warnings" to be omitted by omitempty on a clean read, got %s`, data)
	}
}
