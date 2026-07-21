package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestThreadCommand_JSONOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	item1 := cliWR("XW-axon-20260701-th1", "th1", "axon", []string{"seomatrix"}, "p2", false)
	item1["thread"] = "TH-axon-thread1"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-th1.md", item1, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000020", cliEvt("XW-axon-20260701-th1", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--json", "TH-axon-thread1"}, io)
	if code != 0 {
		t.Fatalf("code = %d, stdout=%s", code, out.String())
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(items) != 1 || items[0].ID != "XW-axon-20260701-th1" {
		t.Fatalf("got %+v", items)
	}
}

func TestThreadCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewThreadCommand(store)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
