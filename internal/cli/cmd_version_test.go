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
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// versionFixture builds a mirror directory carrying a space.yaml floor and a
// pinned a2a-validate.yml, which is exactly what a real mirror carries and
// what the two version axes are read from.
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
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := out.String()
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
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := out.String()
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
	store := versionStore(t, "0.25.0", now, []cache.SpaceMirror{{SpaceID: "axon", Dir: dir, Manifest: manifest}})

	cmd := &cli.VersionCommand{Stamp: "a2a 0.23.0 (abc1234)", BinaryVersion: "0.23.0", Store: store, Now: func() time.Time { return now }}
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := out.String()
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
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := out.String()
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
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := out.String()
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
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), nil, io); code != 0 {
		t.Fatalf("code = %d", code)
	}
	got := out.String()
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
