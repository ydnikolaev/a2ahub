package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/release"
)

func decorateTestStore(t *testing.T) *cache.Store {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	if err := release.WriteCheck(cachePath, release.CheckState{CheckedAt: now, Latest: "0.25.0", Source: "test"}); err != nil {
		t.Fatalf("seed update-check cache: %v", err)
	}
	store.EnableUpdateNotice("0.23.0", cachePath, 6*time.Hour, nil)
	return store
}

func echoTool(name string) ToolSpec {
	return ToolSpec{
		Name:        name,
		Description: "fixture",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(context.Context, json.RawMessage) (any, string, error) {
			return map[string]string{"ok": "yes"}, "artifact body", nil
		},
	}
}

// THE 2026-08-21 STORY ON THE MCP SIDE. The advisory was wrapped around
// a2a_read alone, so an agent asked to send a notification reached a2a_notify
// and was told nothing about the release it was missing. A tool that is not a
// read tool must carry it too, and the registry is the seam every tool passes
// through.
func TestRegistryDecorate_EveryToolCarriesTheAdvisory(t *testing.T) {
	t.Parallel()
	store := decorateTestStore(t)

	r := NewRegistry()
	r.Decorate(updateNoticeDecorator(store))
	r.Register(echoTool("a2a_notify"))

	spec, ok := r.Get("a2a_notify")
	if !ok {
		t.Fatal("a2a_notify was not registered")
	}
	result, body, err := spec.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(body, "note:") {
		t.Fatalf("a non-read tool must carry the update advisory; body = %q", body)
	}
	// StructuredContent is untouched — the advisory is out-of-band prose, and
	// a decorator that reshaped the result would break every consumer's
	// byte-identity guarantee.
	m, isMap := result.(map[string]string)
	if !isMap || m["ok"] != "yes" {
		t.Fatalf("the decorator changed StructuredContent: %#v", result)
	}
	if !strings.HasPrefix(body, "artifact body") {
		t.Fatalf("the decorator replaced the body instead of appending: %q", body)
	}
}

// The paired half. Without it, the test above passes for a handler that emits
// "note:" on its own, and the decorator could be doing nothing at all.
func TestRegistryDecorate_UndecoratedRegistryAddsNothing(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(echoTool("a2a_notify"))

	spec, _ := r.Get("a2a_notify")
	_, body, err := spec.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if strings.Contains(body, "note:") {
		t.Fatalf("an undecorated registry must add nothing; body = %q", body)
	}
}

// A decorator installed after registration would cover some tools and not
// others — the exact partial application this seam exists to prevent, so it
// refuses rather than silently half-applying.
func TestRegistryDecorate_RefusesLateInstallation(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(echoTool("a2a_read"))

	defer func() {
		if recover() == nil {
			t.Fatal("Decorate after Register must panic; a decorator covering only some tools is worse than none")
		}
	}()
	r.Decorate(func(h HandlerFunc) HandlerFunc { return h })
}

// A registry built with no store must register working tools, not panic on
// the first call. Widening the decorator from a2a_read to every tool made this
// reachable for the first time: Store.UpdateNotice dereferences its receiver,
// and several registries are legitimately built without a store.
func TestRegistryDecorate_NilStoreLeavesHandlersIntact(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Decorate(updateNoticeDecorator(nil))
	r.Register(echoTool("a2a_contract"))

	spec, _ := r.Get("a2a_contract")
	result, body, err := spec.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if body != "artifact body" {
		t.Fatalf("body = %q, want the handler's own body untouched", body)
	}
	if m, ok := result.(map[string]string); !ok || m["ok"] != "yes" {
		t.Fatalf("result = %#v", result)
	}
}
