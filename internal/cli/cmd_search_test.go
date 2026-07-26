package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestSearchCommand_ZeroHitsNotError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon")
	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, time.Now, 0)
	cmd := cli.NewSearchCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--json", "no-such-query"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (zero hits is not an error)", code)
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want empty result, got %+v", items)
	}
}

// TestSearchCommand_FlagAfterPositional is Wave K's live-run 6 defect
// applied to `a2a search`: `search <query> --json` used to refuse with a
// usage error (Go's flag package stops parsing at the first non-flag
// token).
//
// TEETH: reverting SearchCommand.Run's parseArgsAnyOrder call
// (cmd_search.go) back to a bare `fs.Parse(args)` reds this.
func TestSearchCommand_FlagAfterPositional(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon")
	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, time.Now, 0)
	cmd := cli.NewSearchCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"no-such-query", "--json"}, io)
	if code != 0 {
		t.Fatalf("search <query> --json: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want empty result, got %+v", items)
	}
}

// TestSearchCommand_ThreadFilter is the brief's own new clause: --thread
// narrows results to items on that thread, client-side over cache.Item's
// own Thread field (cache.SearchFilters has no thread slot; internal/cache
// is off limits this wave — see this phase's Deviations report).
func TestSearchCommand_ThreadFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	onThread := cliWR("XW-axon-20260701-t1", "matching item", "axon", []string{"seomatrix"}, "p2", false)
	onThread["thread"] = "TH-axon-target"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-t1.md", onThread, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000050", cliEvt("XW-axon-20260701-t1", "submit", "axon", base))

	offThread := cliWR("XW-axon-20260701-t2", "matching item", "axon", []string{"seomatrix"}, "p2", false)
	offThread["thread"] = "TH-axon-other"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-t2.md", offThread, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000051", cliEvt("XW-axon-20260701-t2", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewSearchCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"matching", "--thread", "TH-axon-target", "--json"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(items) != 1 || items[0].ID != "XW-axon-20260701-t1" {
		t.Fatalf("got %+v, want exactly XW-axon-20260701-t1", items)
	}
}

func TestSearchCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	cmd := cli.NewSearchCommand(store)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestContractsCommand_ProviderFilter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	cliWriteArtifact(t, dir, "axon/provides/ingest/contract.md", map[string]any{
		"schema": "envelope/v1", "id": "XC-axon-ingest", "type": "contract", "title": "ingest",
		"space": "fixture-space", "from": "axon", "to": []string{"seomatrix"},
		"actor": map[string]any{"kind": "agent", "name": "axon-bot"}, "created": base.Format(time.RFC3339),
		"priority": "p2", "blocking": false, "classification": "internal",
	}, "contract body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000030", cliEvt("XC-axon-ingest", "publish", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewContractsCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--json", "--provider", "axon"}, io)
	if code != 0 {
		t.Fatalf("code = %d, stdout=%s", code, out.String())
	}
	var contracts []cache.ContractInfo
	if err := json.Unmarshal(out.Bytes(), &contracts); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(contracts) != 1 || contracts[0].ID != "XC-axon-ingest" {
		t.Fatalf("got %+v", contracts)
	}
}

func TestContractsCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	cmd := cli.NewContractsCommand(store)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"unexpected"}, io); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
