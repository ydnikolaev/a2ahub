package cache

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// mustManifest reads and structurally parses fx's own space.yaml —
// mirrors what a real cache.Store does via space.LoadProjectConfig's
// sibling (space.ParseManifest), for tests that need a real
// space.Manifest to pass to buildIndex/membershipView.
func mustManifest(t *testing.T, fx *fixtureSpace) space.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fx.dir, "space.yaml"))
	if err != nil {
		t.Fatalf("mustManifest: read space.yaml: %v", err)
	}
	m, err := space.ParseManifest(raw)
	if err != nil {
		t.Fatalf("mustManifest: parse: %v", err)
	}
	return m
}

// fixtureSpace is this package's own minimal throwaway git space builder
// (test-only): a single working clone of a bare origin, committed to
// directly (cache's own tests exercise the READ side only — no write
// funnel / authz enforcement is involved in producing fixture history,
// so per-system clones per testkit/spacefixture are unnecessary
// complexity here).
type fixtureSpace struct {
	t    *testing.T
	dir  string // working clone — also this fixture's buildIndex read dir
	year string
}

// fixtureParticipant is one space.yaml participant entry (real
// []Participant array shape — testkit/spacefixture's own seeded
// space.yaml is a bare map that does NOT parse into that shape; this
// package's tests need real membership resolution, so they build their
// own manifest).
type fixtureParticipant struct {
	System string
	Status string // "active" (default) | "left"
}

func newFixtureSpace(t *testing.T, participants ...fixtureParticipant) *fixtureSpace {
	t.Helper()
	return newFixtureSpaceIn(t, t.TempDir(), participants...)
}

// newFixtureSpaceIn is newFixtureSpace's own root-dir-parameterized form:
// most tests want t.TempDir()'s automatic, synchronous cleanup, but a
// test that deliberately exercises the detached background-refresh
// goroutine (statusline_test.go's TestStatusline_NoHubSymbol) must NOT
// use t.TempDir() for that goroutine's target directory — the goroutine
// is fire-and-forget BY DESIGN (this package never waits for it) and can
// still be running `git fetch` against dir after the test function
// itself returns, racing t.TempDir()'s synchronous RemoveAll (observed
// flake: "unlinkat .../.git: directory not empty").
func newFixtureSpaceIn(t *testing.T, dir string, participants ...fixtureParticipant) *fixtureSpace {
	t.Helper()
	origin := filepath.Join(dir, "origin.git")
	fxRunGit(t, dir, "init", "--bare", "-b", "main", origin)
	work := filepath.Join(dir, "work")
	fxRunGit(t, dir, "clone", origin, work)

	fs := &fixtureSpace{t: t, dir: work, year: "2026"}
	fs.writeManifest(participants...)

	for _, p := range participants {
		fxMkdirAll(t, filepath.Join(work, p.System, "events", fs.year))
		fxWriteFile(t, filepath.Join(work, p.System, "events", fs.year, ".gitkeep"), "")
	}
	fxCommit(t, work, "seed")
	fxRunGit(t, work, "push", "origin", "main")
	return fs
}

func (f *fixtureSpace) writeManifest(participants ...fixtureParticipant) {
	f.t.Helper()
	type manifestParticipant struct {
		System  string   `yaml:"system"`
		Org     string   `yaml:"org"`
		Section string   `yaml:"section"`
		Owners  []string `yaml:"owners"`
		Status  string   `yaml:"status"`
		Joined  string   `yaml:"joined"`
	}
	type manifest struct {
		Schema           string                `yaml:"schema"`
		Space            string                `yaml:"space"`
		MinBinaryVersion string                `yaml:"min_binary_version"`
		Participants     []manifestParticipant `yaml:"participants"`
		StalenessSLADays int                   `yaml:"staleness_sla_days,omitempty"`
	}
	m := manifest{Schema: "space/v1", Space: "fixture-space", MinBinaryVersion: "0.0.0"}
	for _, p := range participants {
		status := p.Status
		if status == "" {
			status = "active"
		}
		m.Participants = append(m.Participants, manifestParticipant{
			System: p.System, Org: p.System + "-org", Section: p.System,
			Owners: []string{p.System + "-human"}, Status: status, Joined: "2026-01-01",
		})
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		f.t.Fatalf("fixtureSpace: marshal manifest: %v", err)
	}
	fxWriteFile(f.t, filepath.Join(f.dir, "space.yaml"), string(raw))
}

// commitArtifact writes a *.md artifact at spaceRelPath with the given
// envelope fields (a plain map — this builder's own minimal envelope
// authoring, not internal/schema/internal/template) and commits it.
func (f *fixtureSpace) commitArtifact(spaceRelPath string, fields map[string]any, body string) {
	f.t.Helper()
	raw, err := yaml.Marshal(fields)
	if err != nil {
		f.t.Fatalf("fixtureSpace: marshal envelope: %v", err)
	}
	full := "---\n" + string(raw) + "---\n" + body
	fullPath := filepath.Join(f.dir, filepath.FromSlash(spaceRelPath))
	fxMkdirAll(f.t, filepath.Dir(fullPath))
	fxWriteFile(f.t, fullPath, full)
	id, _ := fields["id"].(string)
	fxCommitAndPush(f.t, f.dir, "fixture: artifact "+id)
}

// commitEvent writes a committed event/v1 YAML file under
// <system>/events/<year>/<ulid>.yaml and commits it — one commit per
// event, mirroring D-026 (each event travels in its own single-purpose
// commit at this fixture granularity; batch/co-commit scenarios build
// their own fields explicitly via commitArtifactAndEvent).
func (f *fixtureSpace) commitEvent(system, ulid string, fields map[string]any) {
	f.t.Helper()
	base := map[string]any{"schema": "event/v1", "event": ulid, "space": "fixture-space"}
	for k, v := range fields {
		base[k] = v
	}
	raw, err := yaml.Marshal(base)
	if err != nil {
		f.t.Fatalf("fixtureSpace: marshal event: %v", err)
	}
	path := filepath.Join(f.dir, system, "events", f.year, ulid+".yaml")
	fxMkdirAll(f.t, filepath.Dir(path))
	fxWriteFile(f.t, path, string(raw))
	fxCommitAndPush(f.t, f.dir, "fixture: event "+ulid)
}

// commitArtifactAndEvent commits an artifact file AND an event file
// together in ONE commit (D-026's real shape: "one commit = artifact +
// its accompanying event") — used for the response+respond correlation
// tests (same-commit is this package's own correlation key).
func (f *fixtureSpace) commitArtifactAndEvent(artifactPath string, artifactFields map[string]any, body, eventSystem, eventULID string, eventFields map[string]any) {
	f.t.Helper()
	raw, err := yaml.Marshal(artifactFields)
	if err != nil {
		f.t.Fatalf("fixtureSpace: marshal envelope: %v", err)
	}
	full := "---\n" + string(raw) + "---\n" + body
	fullPath := filepath.Join(f.dir, filepath.FromSlash(artifactPath))
	fxMkdirAll(f.t, filepath.Dir(fullPath))
	fxWriteFile(f.t, fullPath, full)

	base := map[string]any{"schema": "event/v1", "event": eventULID, "space": "fixture-space"}
	for k, v := range eventFields {
		base[k] = v
	}
	eraw, err := yaml.Marshal(base)
	if err != nil {
		f.t.Fatalf("fixtureSpace: marshal event: %v", err)
	}
	eventPath := filepath.Join(f.dir, eventSystem, "events", f.year, eventULID+".yaml")
	fxMkdirAll(f.t, filepath.Dir(eventPath))
	fxWriteFile(f.t, eventPath, string(eraw))

	fxCommitAndPush(f.t, f.dir, "fixture: co-commit "+eventULID)
}

func fxRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixtureSpace: git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
}

func fxCommit(t *testing.T, dir, msg string) {
	t.Helper()
	fxRunGit(t, dir, "add", "-A")
	cmd := exec.Command("git", "commit", "-m", msg)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixtureSpace: git commit (dir=%s): %v\n%s", dir, err, out.String())
	}
}

func fxCommitAndPush(t *testing.T, dir, msg string) {
	t.Helper()
	fxCommit(t, dir, msg)
	fxRunGit(t, dir, "push", "origin", "main")
}

func fxMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("fixtureSpace: mkdir %s: %v", dir, err)
	}
}

func fxWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("fixtureSpace: write %s: %v", path, err)
	}
}

// fxAt formats t as the event/v1 `at` field (RFC3339).
func fxAt(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// fxULID returns a syntactically-valid-looking, deterministic per-test
// ULID string built from a small integer seed — real ULID monotonicity
// is not load-bearing for this package's tests (commit order, not ULID
// order, is the primary ordering key; ULID is only an intra-commit
// tiebreak, D-017).
func fxULID(seed int) string {
	return fmt.Sprintf("01HFX%020d", seed)
}
