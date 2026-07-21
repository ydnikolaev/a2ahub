package cli_test

import (
	"context"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
)

func TestStatuslineCommand_SilentZeroNoise(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewStatuslineCommand(store)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), nil, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if out.Len() != 0 {
		t.Fatalf("want zero-noise (empty stdout), got %q", out.String())
	}
}

func TestStatuslineCommand_P1Severity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-urg.md", cliWR("XW-seomatrix-20260701-urg", "urgent item", "seomatrix", []string{"axon"}, "p1", false), "body")
	cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000040", cliEvt("XW-seomatrix-20260701-urg", "submit", "seomatrix", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, time.Hour)
	cmd := cli.NewStatuslineCommand(store)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), nil, io)
	if code != 11 {
		t.Fatalf("code = %d, want 11 (p1 severity); stdout=%s", code, out.String())
	}
	if out.Len() == 0 {
		t.Fatal("want a non-empty status line")
	}
}

func TestStatuslineCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return time.Now() }, 0)
	cmd := cli.NewStatuslineCommand(store)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"unexpected"}, io); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
