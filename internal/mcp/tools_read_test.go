package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func testStore(t *testing.T, mirrorDir string) *cache.Store {
	t.Helper()
	manifest := space.Manifest{Participants: []space.Participant{
		{System: "axon", Status: "active"}, {System: "beta", Status: "active"},
	}}
	return cache.NewStore("beta", t.TempDir(), []cache.SpaceMirror{{SpaceID: "fixture-space", Dir: mirrorDir, Manifest: manifest}},
		func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) }, 0)
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
	items, ok := result.([]cache.Item)
	if !ok {
		t.Fatalf("expected []cache.Item, got %T", result)
	}
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("expected one inbox item for %s, got %+v", id, items)
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
	items, ok := result.([]cache.Item)
	if !ok || items == nil {
		t.Fatalf("expected a non-nil empty []cache.Item, got %#v", result)
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

func TestThreadHandler(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	id := "XQ-axon-20260721-c001"
	writeMirrorFile(t, mirrorDir, "axon/exchanges/"+id+".md",
		"---\nschema: envelope/v1\nid: "+id+"\ntype: question\ntitle: t\nspace: fixture-space\nfrom: axon\nto: [beta]\nthread: T-1\nactor: {kind: agent, name: bot}\ncreated: 2026-07-21T10:00:00Z\ncategory: clarification\npriority: p3\nblocking: true\nclassification: internal\n---\nbody\n")

	handler := newThreadHandler(testStore(t, mirrorDir))
	args, _ := json.Marshal(ThreadInput{ThreadID: "T-1"})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("thread handler failed: %v", err)
	}
	items := result.([]cache.Item)
	if len(items) != 1 || items[0].ID != id {
		t.Fatalf("expected 1 thread item, got %+v", items)
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
	items := result.([]cache.Item)
	if len(items) != 1 {
		t.Fatalf("expected 1 search hit, got %+v", items)
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
