package cli_test

import (
	"context"
	"encoding/json"
	"strings"
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

// TestOutboxCommand_UpdateAdvisory_HumanMode is spec 19 T4: with a newer
// cached release and the notice enabled, human-mode `a2a outbox` appends the
// advisory prose line to STDERR, and stdout is byte-identical to the
// no-notice baseline.
func TestOutboxCommand_UpdateAdvisory_HumanMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, t.TempDir(), "0.3.0", now)

	baseline := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cmdBaseline := cli.NewOutboxCommand(baseline)
	ioBaseline, outBaseline, errBaseline := newIO()
	if code := cmdBaseline.Run(context.Background(), nil, ioBaseline); code != 0 {
		t.Fatalf("baseline: code = %d", code)
	}
	if errBaseline.Len() != 0 {
		t.Fatalf("baseline stderr = %q, want empty (notice not enabled)", errBaseline.String())
	}

	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)
	cmd := cli.NewOutboxCommand(store)
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}

	if out.String() != outBaseline.String() {
		t.Fatalf("stdout = %q, want byte-identical to no-notice baseline %q", out.String(), outBaseline.String())
	}
	if !strings.Contains(errOut.String(), "note: a2a update available: v0.1.2 -> v0.3.0 — run 'a2a update'") {
		t.Fatalf("stderr = %q, want the update-available prose line", errOut.String())
	}
}

// TestOutboxCommand_UpdateAdvisory_JSONMode is spec 19 T4: `--json` mode
// writes the shared cache.UpdateNotice object as one JSON line to STDERR,
// and stdout stays the byte-identical bare item array.
func TestOutboxCommand_UpdateAdvisory_JSONMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, t.TempDir(), "0.3.0", now)

	baseline := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cmdBaseline := cli.NewOutboxCommand(baseline)
	ioBaseline, outBaseline, _ := newIO()
	if code := cmdBaseline.Run(context.Background(), []string{"--json"}, ioBaseline); code != 0 {
		t.Fatalf("baseline: code = %d", code)
	}

	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)
	cmd := cli.NewOutboxCommand(store)
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{"--json"}, io); code != 0 {
		t.Fatalf("code = %d", code)
	}

	if out.String() != outBaseline.String() {
		t.Fatalf("stdout = %q, want byte-identical to no-notice baseline %q", out.String(), outBaseline.String())
	}

	var notice cache.UpdateNotice
	stderrLine := strings.TrimSpace(errOut.String())
	if err := json.Unmarshal([]byte(stderrLine), &notice); err != nil {
		t.Fatalf("json.Unmarshal stderr line: %v (stderr=%q)", err, errOut.String())
	}
	if !notice.UpdateAvailable || notice.Current != "0.1.2" || notice.Latest != "0.3.0" {
		t.Fatalf("stderr JSON notice = %+v, want UpdateAvailable=true Current=0.1.2 Latest=0.3.0", notice)
	}
	if strings.Count(errOut.String(), "\n") != 1 {
		t.Fatalf("stderr = %q, want exactly one JSON line", errOut.String())
	}
}

// TestOutboxCommand_NoAdvisoryWhenNoticeNotEnabled asserts a Store that
// never called EnableUpdateNotice emits no stderr advisory at all, whatever
// the T3 cache on disk holds.
func TestOutboxCommand_NoAdvisoryWhenNoticeNotEnabled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seedUpdateCache(t, t.TempDir(), "9.9.9", now)

	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cmd := cli.NewOutboxCommand(store)
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty (notice never enabled)", errOut.String())
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
