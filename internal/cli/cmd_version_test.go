package cli_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// requireFrozenStampOnStdout holds the ONE contract every already-installed
// a2a depends on: `a2a version`'s stdout is exactly the stamp line, and it
// parses with the very expression the updater applies — not a copy of it.
//
// This test exists because the contract was broken in a shipped release and
// nothing noticed. v0.25.0 moved the three-axis report onto stdout, and every
// binary already on a machine — carrying a frozen copy of that parser —
// refused the upgrade with "unparseable version stamp" and rolled back. It was
// found by running `a2a update` for real on 2026-08-22, which is the only
// thing that had ever exercised the path.
func requireFrozenStampOnStdout(t *testing.T, stdout, wantStamp string) {
	t.Helper()
	if lines := strings.Count(strings.TrimSuffix(stdout, "\n"), "\n"); lines != 0 {
		t.Fatalf("version stdout must be exactly the stamp line — every installed a2a matches the WHOLE of it; got %d extra line(s):\n%s", lines, stdout)
	}
	if strings.TrimSpace(stdout) != strings.TrimSpace(wantStamp) {
		t.Fatalf("version stdout = %q, want the stamp %q", stdout, wantStamp)
	}
	got, ok := release.ParseVersionStamp(stdout)
	if !ok {
		t.Fatalf("the updater's own parser rejects this stdout — `a2a update` would abort on it:\n%s", stdout)
	}
	if want := strings.Fields(wantStamp)[1]; got != want {
		t.Fatalf("the updater would read version %q from the stamp, want %q", got, want)
	}
}

// versionFixture builds a mirror directory carrying a space.yaml floor and a
// pinned a2a-validate.yml, which is exactly what a real mirror carries and
// what the two version axes are read from.
// markMirrorSynced makes a fixture mirror look freshly fetched, which is what
// `mirrorSyncAge` reads. Without it every fixture is a NEVER-SYNCED mirror,
// and a never-synced mirror deliberately refuses to recommend acting on what
// it shows — so a test asserting the "behind → a2a space update" remedy must
// say that its snapshot is current.
func markMirrorSynced(t *testing.T, dir string, at time.Time) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o750); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	head := filepath.Join(gitDir, "FETCH_HEAD")
	if err := os.WriteFile(head, []byte("0000000000000000000000000000000000000000\n"), 0o600); err != nil {
		t.Fatalf("write FETCH_HEAD: %v", err)
	}
	if err := os.Chtimes(head, at, at); err != nil {
		t.Fatalf("chtimes FETCH_HEAD: %v", err)
	}
}

func versionFixture(t *testing.T, floor, pin string) (dir string, manifest space.Manifest) {
	t.Helper()
	dir = t.TempDir()
	manifest = cliWriteManifest(t, dir, "axon")
	manifest.MinBinaryVersion = floor
	if pin != "" {
		wf := filepath.Join(dir, ".github", "workflows")
		if err := os.MkdirAll(wf, 0o750); err != nil {
			t.Fatalf("mkdir workflows: %v", err)
		}
		body := "name: a2a-validate\njobs:\n  validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@" + pin + "\n"
		if err := os.WriteFile(filepath.Join(wf, "a2a-validate.yml"), []byte(body), 0o600); err != nil {
			t.Fatalf("write workflow: %v", err)
		}
	}
	return dir, manifest
}

func versionStore(t *testing.T, latest string, now time.Time, mirrors []cache.SpaceMirror) *cache.Store {
	t.Helper()
	store := cache.NewStore("axon", t.TempDir(), mirrors, func() time.Time { return now }, 0)
	if latest != "" {
		cachePath := seedUpdateCache(t, t.TempDir(), latest, now.Add(-2*time.Hour))
		store.EnableUpdateNotice("0.23.0", cachePath, time.Hour, func(context.Context) {})
	}
	return store
}

// A DEVELOPMENT BUILD IS NOT "UP TO DATE", and the first draft of this command
// said it was. `dev` cannot be compared to any release, so the release axis
// must report that it cannot judge — internal/version.ErrInvalidVersion's own
// "cannot verify, never fine" contract, surfaced instead of swallowed.
func TestVersionCommand_DevBuildRefusesToClaimUpToDate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	store := cache.NewStore("axon", t.TempDir(), nil, func() time.Time { return now }, 0)
	cachePath := seedUpdateCache(t, t.TempDir(), "0.25.0", now.Add(-time.Hour))
	store.EnableUpdateNotice("dev", cachePath, time.Hour, func(context.Context) {})

	cmd := &cli.VersionCommand{Stamp: "a2a dev (abc1234)", BinaryVersion: "dev", Store: store, Now: func() time.Time { return now }}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)
	if !strings.Contains(got, "CANNOT be judged") {
		t.Fatalf("a dev build must not be graded against a release; got:\n%s", got)
	}
	if strings.Contains(got, "up to date") {
		t.Fatalf("a dev build was reported as up to date:\n%s", got)
	}
}

// The paired half: a REAL version against a newer release must say so, or the
// test above passes for a command that never grades anything at all.
func TestVersionCommand_ReleasedBuildNamesTheUpdate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	store := versionStore(t, "0.25.0", now, nil)

	cmd := &cli.VersionCommand{Stamp: "a2a 0.23.0 (abc1234)", BinaryVersion: "0.23.0", Store: store, Now: func() time.Time { return now }}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)
	for _, want := range []string{"UPDATE AVAILABLE", "0.25.0", "a2a update"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// THE 2026-08-21 STORY, as a test. A space whose floor is met but whose
// template pin is older than the newest release is the exact state axon sat in
// while an agent wrote to it from a stale binary: legitimate, and invisible.
// Both halves — floor OK, template BEHIND — or "behind" cannot be told from
// "the floor is what is wrong".
func TestVersionCommand_FloorMetButTemplateBehind(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	dir, manifest := versionFixture(t, "0.23.0", "v0.23.0")
	// A CURRENT snapshot, so "behind" is a claim worth acting on and the
	// remedy below is the space update rather than a sync.
	markMirrorSynced(t, dir, now.Add(-time.Minute))
	store := versionStore(t, "0.25.0", now, []cache.SpaceMirror{{SpaceID: "axon", Dir: dir, Manifest: manifest}})

	cmd := &cli.VersionCommand{Stamp: "a2a 0.23.0 (abc1234)", BinaryVersion: "0.23.0", Store: store, Now: func() time.Time { return now }}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)
	if !strings.Contains(got, "floor 0.23.0  ok") {
		t.Fatalf("the floor is met and must be reported met:\n%s", got)
	}
	if !strings.Contains(got, "template v0.23.0  BEHIND") {
		t.Fatalf("a space pinning an older template must be named BEHIND:\n%s", got)
	}
	if !strings.Contains(got, "a2a space update") {
		t.Fatalf("the report must name the command that fixes it:\n%s", got)
	}
}

// A CURRENT SPACE MUST NOT BE CALLED BEHIND — without this, the test above
// passes for a command that labels every space behind unconditionally.
func TestVersionCommand_CurrentSpaceIsNotBehind(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	dir, manifest := versionFixture(t, "0.25.0", "v0.25.0")
	store := versionStore(t, "0.25.0", now, []cache.SpaceMirror{{SpaceID: "axon", Dir: dir, Manifest: manifest}})

	cmd := &cli.VersionCommand{Stamp: "a2a 0.25.0 (abc1234)", BinaryVersion: "0.25.0", Store: store, Now: func() time.Time { return now }}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)
	if strings.Contains(got, "BEHIND") {
		t.Fatalf("a space pinning the newest release must not be called behind:\n%s", got)
	}
	if !strings.Contains(got, "template v0.25.0  ok") {
		t.Fatalf("expected an ok template row:\n%s", got)
	}
}

// A floor this binary does not meet is the ONE refusal in the system, and the
// report must name the space, not just the fact.
func TestVersionCommand_UnmetFloorNamesTheSpace(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	dir, manifest := versionFixture(t, "0.30.0", "v0.25.0")
	store := versionStore(t, "0.25.0", now, []cache.SpaceMirror{{SpaceID: "axon", Dir: dir, Manifest: manifest}})

	cmd := &cli.VersionCommand{Stamp: "a2a 0.23.0 (abc1234)", BinaryVersion: "0.23.0", Store: store, Now: func() time.Time { return now }}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)
	if !strings.Contains(got, "UNMET — writes refused") {
		t.Fatalf("an unmet floor must say writes are refused:\n%s", got)
	}
}

// No store is the "outside a project" case, and it must say which axes it
// cannot report — an empty space list reads as "none connected", which is a
// different and false claim.
func TestVersionCommand_NoProjectSaysUnavailable(t *testing.T) {
	t.Parallel()
	cmd := &cli.VersionCommand{Stamp: "a2a 0.25.0 (abc1234)", BinaryVersion: "0.25.0"}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)
	if !strings.Contains(got, "UNAVAILABLE") {
		t.Fatalf("outside a project the report must say so:\n%s", got)
	}
	if strings.Contains(got, "none connected") {
		t.Fatalf("no project must not be rendered as no spaces:\n%s", got)
	}
}

func TestVersionCommand_JSONCarriesBothAxes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	dir, manifest := versionFixture(t, "0.23.0", "v0.23.0")
	store := versionStore(t, "0.25.0", now, []cache.SpaceMirror{{SpaceID: "axon", Dir: dir, Manifest: manifest}})

	cmd := &cli.VersionCommand{Stamp: "a2a 0.23.0 (abc1234)", BinaryVersion: "0.23.0", Store: store, Now: func() time.Time { return now }}
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"--json"}, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	var report cache.VersionReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("json.Unmarshal: %v (stdout=%s)", err, out.String())
	}
	if report.Binary != "0.23.0" || report.Update.Latest != "0.25.0" {
		t.Fatalf("release axis wrong: %+v", report)
	}
	if len(report.Spaces) != 1 || !report.Spaces[0].TemplateBehind || !report.Spaces[0].FloorMet {
		t.Fatalf("space axis wrong: %+v", report.Spaces)
	}
}

// A STALE MIRROR CANNOT TELL "BEHIND" FROM "ALREADY UPDATED".
//
// Every space row `a2a version` prints is read from a local mirror the command
// deliberately never refreshes. On 2026-08-23 that produced the worst possible
// output: an operator ran `a2a space update`, merged its PR, and was then told
// the space was still BEHIND and that the remedy was to run the command they
// had just run. Nothing was broken; the mirror was simply older than the merge.
func TestVersionCommand_StaleMirrorSaysSyncNotSpaceUpdate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	dir, manifest := versionFixture(t, "0.23.0", "v0.22.0")
	store := versionStore(t, "0.25.0", now, []cache.SpaceMirror{{SpaceID: "axon", Dir: dir, Manifest: manifest}})

	cmd := &cli.VersionCommand{Stamp: "a2a 0.25.0 (abc1234)", BinaryVersion: "0.25.0", Store: store, Now: func() time.Time { return now }}
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0", code)
	}
	got := errOut.String()
	requireFrozenStampOnStdout(t, out.String(), cmd.Stamp)

	// The fixture's mirror is a bare temp dir: never fetched, which is a
	// DIFFERENT state from "fetched long ago" and must read as such.
	if !strings.Contains(got, "NEVER SYNCED") {
		t.Errorf("a mirror that was never fetched must say so:\n%s", got)
	}
	if !strings.Contains(got, "a2a sync") {
		t.Errorf("a stale mirror's remedy is to sync, not to act on what it shows:\n%s", got)
	}
	if strings.Contains(got, "a2a space update    —") {
		t.Errorf("a stale mirror must not recommend acting on a snapshot:\n%s", got)
	}
}
