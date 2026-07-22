package mcp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func testNewDeps(stagingDir string) NewDeps {
	return NewDeps{
		StagingDir: stagingDir, OwnSystem: "beta",
		Now:          func() time.Time { return time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC) },
		Entropy:      repeatingReader{pattern: []byte("0123456789abcdef")},
		ResolveActor: fixedActorResolver("agent", "bot"),
		WriteFile:    os.WriteFile,
	}
}

func TestNewHandlerDraftsExchangeType(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newNewHandler(testNewDeps(staging))

	args, _ := json.Marshal(NewInput{Items: []NewItem{{Type: "question", Fields: map[string]string{"to": "axon"}}}})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("new handler failed: %v", err)
	}
	drafts, ok := result.([]newDraftResult)
	if !ok || len(drafts) != 1 {
		t.Fatalf("expected 1 draft result, got %#v", result)
	}
	if _, err := os.Stat(drafts[0].Path); err != nil {
		t.Fatalf("expected the draft to be written to disk: %v", err)
	}
}

func TestNewHandlerBatchItemsOnOneThread(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newNewHandler(testNewDeps(staging))

	args, _ := json.Marshal(NewInput{
		Thread: "T-shared",
		Items: []NewItem{
			{Type: "question", Fields: map[string]string{"to": "axon"}},
			{Type: "work_request", Fields: map[string]string{"to": "axon"}},
		},
	})
	result, _, err := handler(context.Background(), args)
	if err != nil {
		t.Fatalf("new handler failed: %v", err)
	}
	drafts := result.([]newDraftResult)
	if len(drafts) != 2 {
		t.Fatalf("expected 2 drafted items, got %d", len(drafts))
	}
}

func TestNewHandlerStandingTypeRequiresSlug(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newNewHandler(testNewDeps(staging))

	args, _ := json.Marshal(NewInput{Items: []NewItem{{Type: "requirement"}}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for a standing type with no slug")
	}
}

func TestNewHandlerUnknownType(t *testing.T) {
	t.Parallel()
	handler := newNewHandler(testNewDeps(t.TempDir()))
	args, _ := json.Marshal(NewInput{Items: []NewItem{{Type: "bogus"}}})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for an unknown type")
	}
}

func TestNewHandlerEmptyItems(t *testing.T) {
	t.Parallel()
	handler := newNewHandler(testNewDeps(t.TempDir()))
	_, _, err := handler(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error for empty items")
	}
}
