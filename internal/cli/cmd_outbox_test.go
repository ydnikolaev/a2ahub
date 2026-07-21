package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestOutboxCommand_JSONOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-b1.md", cliWR("XW-axon-20260701-b1", "b1", "axon", []string{"seomatrix"}, "p2", false), "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000002", cliEvt("XW-axon-20260701-b1", "submit", "axon", base))
	cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000003", cliEvt("XW-axon-20260701-b1", "decline", "seomatrix", base.Add(time.Hour)))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(2 * time.Hour) }, 0)
	cmd := cli.NewOutboxCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--attention", "--json"}, io)
	if code != 0 {
		t.Fatalf("code = %d, stdout=%s", code, out.String())
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out.String())
	}
	if len(items) != 1 || items[0].ID != "XW-axon-20260701-b1" {
		t.Fatalf("got %+v", items)
	}
}

func TestOutboxCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewOutboxCommand(store)
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"unexpected-arg"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
