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
