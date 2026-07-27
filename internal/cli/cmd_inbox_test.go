package cli_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/release"
)

// seedUpdateCache writes a spec 19 T3 update-check.json at
// dir/update-check.json (release.WriteCheck) and returns its path — shared
// across this test package's inbox/outbox/doctor/sync advisory tests (same
// helper cache/update_test.go's own seedUpdateCache uses, mirrored here
// since internal/cache's is unexported to that package).
func seedUpdateCache(t *testing.T, dir, latest string, checkedAt time.Time) string {
	t.Helper()
	path := filepath.Join(dir, "update-check.json")
	if err := release.WriteCheck(path, release.CheckState{CheckedAt: checkedAt, Latest: latest, Source: "github"}); err != nil {
		t.Fatalf("seedUpdateCache: WriteCheck: %v", err)
	}
	return path
}

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
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
	cmd := cli.NewInboxCommand(store)
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"unexpected-arg"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage); stderr=%s", code, errOut.String())
	}
}

// TestInboxCommand_UpdateAdvisory_HumanMode is spec 19 T4: with a newer
// cached release and the notice enabled, human-mode `a2a inbox` appends the
// advisory prose line to STDERR, and stdout is byte-identical to the
// no-notice baseline (the item text projection never changes).
func TestInboxCommand_UpdateAdvisory_HumanMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, t.TempDir(), "0.3.0", now)

	baseline := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cmdBaseline := cli.NewInboxCommand(baseline)
	ioBaseline, outBaseline, errBaseline := newIO()
	if code := cmdBaseline.Run(context.Background(), nil, ioBaseline); code != 0 {
		t.Fatalf("baseline: code = %d", code)
	}
	if errBaseline.Len() != 0 {
		t.Fatalf("baseline stderr = %q, want empty (notice not enabled)", errBaseline.String())
	}

	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)
	cmd := cli.NewInboxCommand(store)
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

// TestInboxCommand_UpdateAdvisory_JSONMode is spec 19 T4: `--json` mode
// writes the shared cache.UpdateNotice object as one JSON line to STDERR,
// and stdout stays the byte-identical bare item array (wave 12c: never a
// top-level object on stdout).
func TestInboxCommand_UpdateAdvisory_JSONMode(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	cachePath := seedUpdateCache(t, t.TempDir(), "0.3.0", now)

	baseline := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cmdBaseline := cli.NewInboxCommand(baseline)
	ioBaseline, outBaseline, _ := newIO()
	if code := cmdBaseline.Run(context.Background(), []string{"--json"}, ioBaseline); code != 0 {
		t.Fatalf("baseline: code = %d", code)
	}

	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	store.EnableUpdateNotice("0.1.2", cachePath, time.Hour, nil)
	cmd := cli.NewInboxCommand(store)
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

// TestInboxCommand_NoAdvisoryWhenNoticeNotEnabled asserts a Store that never
// called EnableUpdateNotice emits no stderr advisory at all, whatever the T3
// cache on disk holds.
func TestInboxCommand_NoAdvisoryWhenNoticeNotEnabled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	seedUpdateCache(t, t.TempDir(), "9.9.9", now)

	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cmd := cli.NewInboxCommand(store)
	io, _, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty (notice never enabled)", errOut.String())
	}
}

func TestInboxCommand_NoConnectedSpacesEmptyJSON(t *testing.T) {
	t.Parallel()
	store := cache.NewStore("axon", t.TempDir(), nil, time.Now, 0)
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

// TestInboxCommand_ExitCode covers the flag a scheduled agent loop branches
// on. Without it, "empty inbox" and "five p1 items" are both exit 0, so an
// unattended runner has to shell out through `--json | jq length` to learn
// whether to start work at all.
func TestInboxCommand_ExitCode(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)

	t.Run("nothing actionable is quiet", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
		store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base }, 0)
		io, _, _ := newIO()
		if code := cli.NewInboxCommand(store).Run(context.Background(), []string{"--actionable", "--exit-code"}, io); code != 0 {
			t.Fatalf("code = %d, want 0 (quiet)", code)
		}
	})

	t.Run("a p1 item is urgent", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
		cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-a1.md", cliWR("XW-seomatrix-20260701-a1", "a1", "seomatrix", []string{"axon"}, "p1", false), "body")
		cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000001", cliEvt("XW-seomatrix-20260701-a1", "submit", "seomatrix", base))
		store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
		io, _, _ := newIO()
		if code := cli.NewInboxCommand(store).Run(context.Background(), []string{"--actionable", "--exit-code"}, io); code != 11 {
			t.Fatalf("code = %d, want 11 (urgent)", code)
		}
	})

	t.Run("without the flag the same run still exits 0", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
		cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-a1.md", cliWR("XW-seomatrix-20260701-a1", "a1", "seomatrix", []string{"axon"}, "p1", false), "body")
		cliWriteEvent(t, dir, "seomatrix", "01HFX00000000000000000001", cliEvt("XW-seomatrix-20260701-a1", "submit", "seomatrix", base))
		store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base.Add(time.Hour) }, 0)
		io, _, _ := newIO()
		// The whole point of the flag being opt-in: every existing caller,
		// script and test keeps reading 0 as "it worked".
		if code := cli.NewInboxCommand(store).Run(context.Background(), []string{"--actionable"}, io); code != 0 {
			t.Fatalf("code = %d, want 0 — --exit-code must not change the default contract", code)
		}
	})

	t.Run("a usage error stays 2, never a severity", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
		store := cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return base }, 0)
		io, _, _ := newIO()
		// A scheduler must never read a broken invocation as "you have work",
		// nor a full inbox as "the tool is broken". 1 and 2 stay reserved.
		if code := cli.NewInboxCommand(store).Run(context.Background(), []string{"--exit-code", "stray-arg"}, io); code != 2 {
			t.Fatalf("code = %d, want 2 (usage)", code)
		}
	})
}

// TestInboxCommand_Overdue pins the exact asymmetry --overdue exists to
// close, and it is the whole reason the flag was built: an item you have
// ACKNOWLEDGED leaves --actionable (OP-207 condition 1 is "addressed to me
// with NO ack by me") while the work and its deadline remain yours. Before
// this flag, the passed deadline was reported only by `outbox --attention`,
// to the requester — the one person who cannot close it.
func TestInboxCommand_Overdue(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	// `now` is well past the deadline below.
	now := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

	newStore := func(t *testing.T) *cache.Store {
		t.Helper()
		dir := t.TempDir()
		manifest := cliWriteManifest(t, dir, "axon", "seomatrix")
		env := cliWR("XW-seomatrix-20260701-od", "late work", "seomatrix", []string{"axon"}, "p2", false)
		env["needed_by"] = "2026-08-20"
		cliWriteArtifact(t, dir, "seomatrix/exchanges/XW-seomatrix-20260701-od.md", env, "body")
		cliWriteEvent(t, dir, "seomatrix", "01HFX0000000000000000000A", cliEvt("XW-seomatrix-20260701-od", "submit", "seomatrix", base))
		// axon ACKS it — which is what removes it from --actionable while
		// leaving the work owed.
		cliWriteEvent(t, dir, "axon", "01HFX0000000000000000000B", cliEvt("XW-seomatrix-20260701-od", "acknowledge", "axon", base.Add(time.Hour)))
		return cache.NewStore("axon", t.TempDir(), []cache.SpaceMirror{{SpaceID: "sp1", Dir: dir, Manifest: manifest}}, func() time.Time { return now }, 0)
	}

	decode := func(t *testing.T, raw []byte) []cache.Item {
		t.Helper()
		var items []cache.Item
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("json.Unmarshal: %v (%s)", err, raw)
		}
		return items
	}

	t.Run("an acked overdue item is invisible to --actionable", func(t *testing.T) {
		t.Parallel()
		io, out, _ := newIO()
		if code := cli.NewInboxCommand(newStore(t)).Run(context.Background(), []string{"--actionable", "--json"}, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		if items := decode(t, out.Bytes()); len(items) != 0 {
			t.Fatalf("--actionable returned %d items, want 0 — if this changed, OP-207's union changed", len(items))
		}
	})

	t.Run("--overdue finds it and says why", func(t *testing.T) {
		t.Parallel()
		io, out, _ := newIO()
		if code := cli.NewInboxCommand(newStore(t)).Run(context.Background(), []string{"--overdue", "--json"}, io); code != 0 {
			t.Fatalf("code = %d", code)
		}
		items := decode(t, out.Bytes())
		if len(items) != 1 {
			t.Fatalf("--overdue returned %d items, want 1", len(items))
		}
		if items[0].ID != "XW-seomatrix-20260701-od" {
			t.Errorf("id = %q", items[0].ID)
		}
		if len(items[0].Reasons) != 1 || items[0].Reasons[0] != "overdue-on-me" {
			t.Errorf("reasons = %v, want [overdue-on-me]", items[0].Reasons)
		}
	})

	t.Run("--overdue composes with --exit-code for a scheduler", func(t *testing.T) {
		t.Parallel()
		io, _, _ := newIO()
		if code := cli.NewInboxCommand(newStore(t)).Run(context.Background(), []string{"--overdue", "--exit-code"}, io); code != 10 {
			t.Fatalf("code = %d, want 10 (items pending)", code)
		}
	})

	t.Run("--actionable with --overdue is refused, not silently merged", func(t *testing.T) {
		t.Parallel()
		io, _, errOut := newIO()
		if code := cli.NewInboxCommand(newStore(t)).Run(context.Background(), []string{"--actionable", "--overdue"}, io); code != 2 {
			t.Fatalf("code = %d, want 2", code)
		}
		if !strings.Contains(errOut.String(), "different questions") {
			t.Errorf("stderr = %q, want it to explain the two questions", errOut.String())
		}
	})
}
