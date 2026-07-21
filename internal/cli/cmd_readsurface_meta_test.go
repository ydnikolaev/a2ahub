package cli_test

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// TestReadSurfaceCommands_NameAndSynopsis exercises every P7 verb's
// trivial Name/Synopsis accessors (usage-listing wiring, cmd/a2a's own
// dispatch table depends on Name() being stable).
func TestReadSurfaceCommands_NameAndSynopsis(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)

	cmds := []cli.Command{
		cli.NewInboxCommand(store),
		cli.NewOutboxCommand(store),
		cli.NewShowCommand(store),
		cli.NewThreadCommand(store),
		cli.NewSearchCommand(store),
		cli.NewContractsCommand(store),
		cli.NewStatuslineCommand(store),
	}
	for _, c := range cmds {
		if c.Name() == "" {
			t.Errorf("%T: Name() is empty", c)
		}
		if c.Synopsis() == "" {
			t.Errorf("%T: Synopsis() is empty", c)
		}
	}
}

// TestReadSurfaceCommands_TextRendering exercises the non-JSON (text
// projection) render branch every list-shaped verb carries (T1: "text
// rendering is a projection of the same JSON").
func TestReadSurfaceCommands_TextRendering(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-tx.md", cliWR("XW-seomatrix-20260701-tx", "text render", "seomatrix", []string{"axon"}, "p1", false), "body")
	cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000050", cliEvt("XW-seomatrix-20260701-tx", "submit", "seomatrix", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)

	t.Run("inbox", func(t *testing.T) {
		io, out, _ := newIO()
		if code := cli.NewInboxCommand(store).Run(context.Background(), []string{"--actionable"}, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if out.Len() == 0 {
			t.Fatal("expected text output")
		}
	})
	t.Run("outbox", func(t *testing.T) {
		io, out, _ := newIO()
		if code := cli.NewOutboxCommand(store).Run(context.Background(), nil, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		_ = out
	})
	t.Run("thread", func(t *testing.T) {
		item2 := cliWR("XW-seomatrix-20260701-tx2", "tx2", "seomatrix", []string{"axon"}, "p2", false)
		item2["thread"] = "TH-text-render"
		cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-tx2.md", item2, "body")
		cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000051", cliEvt("XW-seomatrix-20260701-tx2", "submit", "seomatrix", base))
		io, out, _ := newIO()
		if code := cli.NewThreadCommand(store).Run(context.Background(), []string{"TH-text-render"}, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if out.Len() == 0 {
			t.Fatal("expected text output")
		}
	})
	t.Run("search", func(t *testing.T) {
		io, out, _ := newIO()
		if code := cli.NewSearchCommand(store).Run(context.Background(), []string{"text render"}, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if out.Len() == 0 {
			t.Fatal("expected text output")
		}
	})
	t.Run("contracts", func(t *testing.T) {
		io, out, _ := newIO()
		if code := cli.NewContractsCommand(store).Run(context.Background(), nil, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		_ = out
	})
	t.Run("show", func(t *testing.T) {
		io, out, _ := newIO()
		if code := cli.NewShowCommand(store).Run(context.Background(), []string{"XW-seomatrix-20260701-tx"}, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if out.Len() == 0 {
			t.Fatal("expected text output")
		}
	})
}
