package mcp

import (
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// TestBuildRegistryExpectedToolCount proves BuildRegistry registers
// exactly the §7.7-designated tool set: 6 read + new + submit + 19
// lifecycle + 6 contract sub-verbs = 33 tools. cmd/a2a/mcp_parity_test.go
// is the authoritative bijection check against the CLI's own verb set;
// this is a package-local sanity count.
func TestBuildRegistryExpectedToolCount(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	store := cache.NewStore("beta", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	fake := &fakeFunnel{}
	write := testWriteDeps(mirrorDir, fake)
	legality := NewLegalityAdapter(mirrorDir, "beta", testManifest())
	newDeps := testNewDeps(t.TempDir())

	registry := BuildRegistry(store, write, mirrorDir, legality, newDeps)
	names := registry.ToolNames()
	const want = 6 + 1 + 1 + 19 + 6
	if len(names) != want {
		t.Fatalf("expected %d tools, got %d: %v", want, len(names), names)
	}

	mustHave := []string{
		"a2a_inbox", "a2a_outbox", "a2a_show", "a2a_thread", "a2a_search", "a2a_contracts",
		"a2a_new", "a2a_submit",
		"a2a_ack", "a2a_accept", "a2a_decline", "a2a_start", "a2a_block", "a2a_unblock",
		"a2a_cancel", "a2a_close", "a2a_withdraw", "a2a_supersede", "a2a_satisfy",
		"a2a_approve", "a2a_reject", "a2a_verify_pass", "a2a_verify_fail",
		"a2a_respond", "a2a_verify", "a2a_dispute", "a2a_note",
		"a2a_contract_new", "a2a_contract_publish", "a2a_contract_deprecate",
		"a2a_contract_retire", "a2a_contract_diff", "a2a_contract_verify_export",
	}
	for _, name := range mustHave {
		if _, ok := registry.Get(name); !ok {
			t.Errorf("expected tool %q to be registered", name)
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
