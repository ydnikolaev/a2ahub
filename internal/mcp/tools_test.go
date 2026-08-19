package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// TestBuildRegistryExpectedToolCount proves BuildRegistry registers
// exactly the P15 capability-grouped tool set plus P31's standalone
// a2a_whatsnew, spec 05a's a2a_data, and space-notify-2026-08 P6's
// a2a_notify: a2a_read + a2a_new + a2a_submit + a2a_lifecycle +
// a2a_exchange + a2a_contract + a2a_data + a2a_whatsnew + a2a_work +
// a2a_notify = 10 tools (spec 15 §T1/§8 AC #1, extended P31, extended spec
// 05a, extended spec 06). BuildRegistry registers a2a_data AND a2a_notify
// degraded (zero-value DataToolDeps{}/NotifyToolDeps{}, via
// BuildRegistryWithOperations) even though this constructor takes no
// data/notify-operations argument — the same "degraded but present"
// precedent ContractToolOperations{}'s zero value already sets for
// a2a_contract. cmd/a2a/mcp_parity_test.go is the authoritative
// capability-parity check against the CLI's own verb set; this is a
// package-local sanity count.
func TestBuildRegistryExpectedToolCount(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	store := cache.NewStore("beta", t.TempDir(), nil, time.Now, 0)
	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	newDeps := testNewDeps(t.TempDir())

	registry := BuildRegistry(store, write, mirrorDir, legality, newDeps)
	names := registry.ToolNames()
	want := []string{
		"a2a_contract", "a2a_data", "a2a_exchange", "a2a_lifecycle",
		"a2a_new", "a2a_notify", "a2a_read", "a2a_submit", "a2a_whatsnew", "a2a_work",
	}
	if len(names) != len(want) {
		t.Fatalf("expected %d tools, got %d: %v", len(want), len(names), names)
	}
	for i, name := range want {
		if names[i] != name {
			t.Fatalf("tool set mismatch: want %v, got %v", want, names)
		}
	}

	// The per-verb tool names P14 shipped must NO LONGER be registered —
	// they are folded into the grouped tools' action/view enums.
	removed := []string{
		"a2a_inbox", "a2a_outbox", "a2a_show", "a2a_thread", "a2a_search", "a2a_contracts",
		"a2a_ack", "a2a_accept", "a2a_respond", "a2a_verify", "a2a_dispute", "a2a_note",
		"a2a_contract_new", "a2a_contract_publish",
	}
	for _, name := range removed {
		if _, ok := registry.Get(name); ok {
			t.Errorf("expected folded tool %q to be ABSENT (now an action/view of a grouped tool)", name)
		}
	}

	excluded := []string{"a2a_version", "a2a_init", "a2a_connect", "a2a_disconnect", "a2a_doctor", "a2a_template", "a2a_sync", "a2a_validate", "a2a_mcp"}
	for _, name := range excluded {
		if _, ok := registry.Get(name); ok {
			t.Errorf("expected tool %q to be ABSENT (CLI-only verb)", name)
		}
	}
}

func TestRawSchemaShape(t *testing.T) {
	t.Parallel()
	raw := rawSchema(map[string]string{"ids": "array"}, "ids")
	if len(raw) == 0 {
		t.Fatal("expected a non-empty schema")
	}
}

// TestBuildRegistryWithOperationsCarriesSubmitResolver pins the seam that
// went missing once and was invisible to every unit test that owns it.
//
// SubmitDeps grew its own per-space ResolveSpace closure, wire.go installed
// it, and this constructor threw it away — it rebuilt SubmitDeps from a
// (staging dir, legality) pair rather than carrying the caller's own value.
// a2a_submit therefore went on refusing every multi-space write in a real
// session while tools_submit_test.go stayed green throughout, because those
// tests construct SubmitDeps directly and never travel through here. The
// defect lived exactly in the gap between the two.
//
// Anything the caller puts on SubmitDeps must survive registry assembly.
func TestBuildRegistryWithOperationsCarriesSubmitResolver(t *testing.T) {
	t.Parallel()

	staging := t.TempDir()
	writeStagedDraftForSpace(t, staging, "XQ-beta-20260721-reg1", "question", "space-b")

	var asked string
	submit := SubmitDeps{
		WriteDeps:  WriteDeps{OwnSystem: "beta", ReadFile: os.ReadFile},
		StagingDir: staging,
		ResolveSpace: func(spaceID string) (SubmitDeps, error) {
			asked = spaceID
			return SubmitDeps{}, errors.New("sentinel: the resolver reached the handler")
		},
	}

	r := BuildRegistryWithOperations(nil, WriteDeps{OwnSystem: "beta", ReadFile: os.ReadFile},
		submit, NewDeps{}, ContractToolOperations{}, DataToolDeps{})

	spec, ok := r.Get("a2a_submit")
	if !ok {
		t.Fatal("a2a_submit is not registered")
	}
	_, _, err := spec.Handler(context.Background(),
		json.RawMessage(`{"ids":["XQ-beta-20260721-reg1"]}`))
	if err == nil || !strings.Contains(err.Error(), "sentinel: the resolver reached the handler") {
		t.Fatalf("the registered a2a_submit handler did not use the caller's SubmitDeps.ResolveSpace; err = %v", err)
	}
	if asked != "space-b" {
		t.Fatalf("resolver asked for space %q, want the draft's own space-b", asked)
	}
}
