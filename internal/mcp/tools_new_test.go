package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/template"
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

	const explicitThread = "thread:beta-20260721-abcd"
	args, _ := json.Marshal(NewInput{
		Thread: explicitThread,
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
	for _, d := range drafts {
		raw, rerr := os.ReadFile(d.Path)
		if rerr != nil {
			t.Fatalf("read drafted file: %v", rerr)
		}
		if !strings.Contains(string(raw), "thread: "+explicitThread) {
			t.Fatalf("expected %s to carry the explicit thread %q, got:\n%s", d.Path, explicitThread, raw)
		}
	}
}

func TestNewHandlerResolvesWholeBatchBeforeMutation(t *testing.T) {
	t.Parallel()

	staging := filepath.Join(t.TempDir(), "staging")
	deps := testNewDeps(staging)
	var resolveCalls, writeCalls int
	deps.ResolveActor = func(ActorInput) (template.Actor, error) {
		resolveCalls++
		if resolveCalls == 2 {
			return template.Actor{}, ErrNoActorName
		}
		return template.Actor{Kind: "agent", Name: "bot"}, nil
	}
	deps.WriteFile = func(string, []byte, os.FileMode) error {
		writeCalls++
		return nil
	}
	handler := newNewHandler(deps)
	args, _ := json.Marshal(NewInput{Items: []NewItem{
		{Type: "question", Fields: map[string]string{"to": "axon"}},
		{Type: "work_request", Fields: map[string]string{"to": "axon"}},
	}})

	_, _, err := handler(context.Background(), args)
	if !errors.Is(err, ErrNoActorName) {
		t.Fatalf("batch error = %v, want ErrNoActorName", err)
	}
	if writeCalls != 0 {
		t.Fatalf("WriteFile called %d times before batch actor refusal, want 0", writeCalls)
	}
	if _, statErr := os.Stat(staging); !os.IsNotExist(statErr) {
		t.Fatalf("staging directory exists after preflight refusal (stat error %v)", statErr)
	}
}

// TestNewHandlerBatchMintsSharedThreadWhenAbsent covers §T1 "Batch": no
// --thread supplied on a multi-item call mints ONE thread on the first item
// and applies it to every other item — the agent makes no per-item choice.
func TestNewHandlerBatchMintsSharedThreadWhenAbsent(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newNewHandler(testNewDeps(staging))

	args, _ := json.Marshal(NewInput{
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

	var threads []string
	for _, d := range drafts {
		raw, rerr := os.ReadFile(d.Path)
		if rerr != nil {
			t.Fatalf("read drafted file: %v", rerr)
		}
		idx := strings.Index(string(raw), "thread: ")
		if idx == -1 {
			t.Fatalf("expected %s to carry a minted thread line, got:\n%s", d.Path, raw)
		}
		line, _, ok := strings.Cut(string(raw)[idx:], "\n")
		if !ok {
			t.Fatalf("expected the thread line in %s to be newline-terminated, got:\n%s", d.Path, raw)
		}
		threads = append(threads, line)
	}
	if threads[0] == "" || threads[0] != threads[1] {
		t.Fatalf("expected the SAME minted thread on both items, got %q and %q", threads[0], threads[1])
	}
	if !strings.Contains(threads[0], "thread:beta-") {
		t.Fatalf("expected a minted thread under the drafting system (beta), got %q", threads[0])
	}
}

// TestNewHandlerItemFieldThreadConflictRefused guards against silently
// clobbering a per-item `fields.thread` override with the batch-minted (or
// batch-explicit) thread — the same D0 class of bug this phase exists to
// close, applied within a batch call: a per-item value that disagrees with
// the resolved batch thread is a conflict, named, never silently discarded.
func TestNewHandlerItemFieldThreadConflictRefused(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newNewHandler(testNewDeps(staging))

	args, _ := json.Marshal(NewInput{
		Items: []NewItem{
			{Type: "question", Fields: map[string]string{"to": "axon", "thread": "thread:beta-20260721-abcd"}},
		},
	})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error: item-level fields.thread disagrees with the freshly minted batch thread")
	}
	entries, rerr := os.ReadDir(staging)
	if rerr != nil {
		t.Fatalf("read staging dir: %v", rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected NOTHING written for a refused thread conflict, got %d entries", len(entries))
	}
}

// TestNewHandlerMalformedThreadRefused covers §T1's draft-time grammar
// check: a --thread that fails ParseThreadID is refused before anything is
// minted or written, never silently accepted or guessed at.
func TestNewHandlerMalformedThreadRefused(t *testing.T) {
	t.Parallel()
	staging := t.TempDir()
	handler := newNewHandler(testNewDeps(staging))

	args, _ := json.Marshal(NewInput{
		Thread: "not-a-real-thread-id",
		Items:  []NewItem{{Type: "question", Fields: map[string]string{"to": "axon"}}},
	})
	_, _, err := handler(context.Background(), args)
	if err == nil {
		t.Fatal("expected an error for a malformed thread")
	}
	entries, rerr := os.ReadDir(staging)
	if rerr != nil {
		t.Fatalf("read staging dir: %v", rerr)
	}
	if len(entries) != 0 {
		t.Fatalf("expected NOTHING written for a refused malformed thread, got %d entries", len(entries))
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
