package cli_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestInboxCommand_JSONOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-a1.md", cliWR("XW-seomatrix-20260701-a1", "a1", "seomatrix", []string{"axon"}, "p1", false), "body")
	cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000001", cliEvt("XW-seomatrix-20260701-a1", "submit", "seomatrix", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewInboxCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--actionable", "--json"}, io)
	if code != 0 {
		t.Fatalf("code = %d, stdout=%s", code, out.String())
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out.String())
	}
	if len(items) != 1 || items[0].ID != "XW-seomatrix-20260701-a1" {
		t.Fatalf("got %+v", items)
	}
}

func TestInboxCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewInboxCommand(store)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"unexpected-arg"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
}

func TestInboxCommand_NoConnectedSpacesEmptyJSON(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewInboxCommand(store)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--json"}, io)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want empty array, got %+v", items)
	}
}
