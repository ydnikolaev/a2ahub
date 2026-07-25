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

// TestThreadCommand_FlagAfterPositional is Wave K's live-run 6 defect
// applied to `a2a thread`: `thread <thread-id> --json` used to refuse with
// a usage error (Go's flag package stops parsing at the first non-flag
// token).
//
// TEETH: reverting ThreadCommand.Run's parseArgsAnyOrder call
// (cmd_thread.go) back to a bare `fs.Parse(args)` reds this.
func TestThreadCommand_FlagAfterPositional(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	item1 := cliWR("XW-axon-20260701-th2", "th2", "axon", []string{"seomatrix"}, "p2", false)
	item1["thread"] = "TH-axon-thread2"
	cliWriteArtifact(t, dir, "axon/exchanges/XW-axon-20260701-th2.md", item1, "body")
	cliWriteEvent(t, dir, "axon", "01HFX00000000000000000021", cliEvt("XW-axon-20260701-th2", "submit", "axon", base))

	store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
	cmd := cli.NewThreadCommand(store)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"TH-axon-thread2", "--json"}, io)
	if code != 0 {
		t.Fatalf("thread <id> --json: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	var items []cache.Item
	if err := json.Unmarshal(out.Bytes(), &items); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if len(items) != 1 || items[0].ID != "XW-axon-20260701-th2" {
		t.Fatalf("got %+v", items)
	}
}

func TestThreadCommand_UsageError(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	cmd := cli.NewThreadCommand(store)
	io, _, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}
