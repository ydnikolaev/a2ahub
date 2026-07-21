// Package spacefixture builds throwaway git space fixtures for tests: a
// bare "origin" repo (the space's remote, simulating a git host with zero
// network) plus one working clone per simulated system, seeded with the
// §4.2 tree. internal/space and internal/host tests use this instead of
// hand-rolling git plumbing per test file (rails pre-flight #6). P10
// extends this builder for full e2e (three-system) scenarios; this phase
// ships the minimal version its own tests need.
//
// Every fixture is t.TempDir-based and never touches the network — all
// git operations run against local paths.
package spacefixture

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fixture is one throwaway space: a bare origin repo plus a working clone
// per seeded system.
type Fixture struct {
	// Dir is the fixture's root temp directory.
	Dir string
	// OriginDir is the bare repo's path — usable directly as a git remote
	// URL (git accepts local filesystem paths).
	OriginDir string
	// Clones maps system-id -> that system's working clone directory.
	Clones map[string]string

	t testing.TB
}

// New creates a bare origin repo on branch "main", seeds it with a minimal
// space.yaml (participants = the given systems) and the §4.2 tree for each
// system (provides/requires/consumes.yaml/exchanges/events/docs, plus the
// space-level decisions/ and vendored/ directories), pushes that seed to
// origin, then clones origin once per system. t.Helper(); cleanup is
// automatic via t.TempDir().
func New(t testing.TB, systems ...string) *Fixture {
	t.Helper()
	if len(systems) == 0 {
		t.Fatal("spacefixture.New: at least one system is required")
	}

	dir := t.TempDir()
	originDir := filepath.Join(dir, "origin.git")
	runGit(t, dir, "init", "--bare", "-b", "main", originDir)

	seedDir := filepath.Join(dir, "seed")
	runGit(t, dir, "init", "-b", "main", seedDir)
	seedTree(t, seedDir, systems)
	runGit(t, seedDir, "add", "-A")
	runGitWithEnv(t, seedDir, commitEnv(), "commit", "-m", "seed: §4.2 tree")
	runGit(t, seedDir, "remote", "add", "origin", originDir)
	runGit(t, seedDir, "push", "origin", "main")

	clones := make(map[string]string, len(systems))
	for _, sys := range systems {
		cloneDir := filepath.Join(dir, "clone-"+sys)
		runGit(t, dir, "clone", originDir, cloneDir)
		clones[sys] = cloneDir
	}

	return &Fixture{Dir: dir, OriginDir: originDir, Clones: clones, t: t}
}

// seedTree lays out the §4.2 normative tree for each system under root,
// plus the space-level decisions/ and vendored/ directories, plus a
// minimal space.yaml. Empty directories are given a `.gitkeep` placeholder
// so the seed commit actually contains them (git tracks files, not dirs).
func seedTree(t testing.TB, root string, systems []string) {
	t.Helper()

	manifest := "id: fixture-space\nschema_version: \"1\"\nmin_binary_version: \"0.0.0\"\nparticipants:\n"
	for _, sys := range systems {
		manifest += fmt.Sprintf("  %s-bot: %s\n", sys, sys)
	}
	writeFile(t, filepath.Join(root, "space.yaml"), manifest)

	for _, sys := range systems {
		dirs := []string{
			filepath.Join(root, sys, "provides"),
			filepath.Join(root, sys, "requires"),
			filepath.Join(root, sys, "exchanges"),
			filepath.Join(root, sys, "events"),
			filepath.Join(root, sys, "docs"),
		}
		for _, d := range dirs {
			mkdirAll(t, d)
			writeFile(t, filepath.Join(d, ".gitkeep"), "")
		}
		writeFile(t, filepath.Join(root, sys, "consumes.yaml"), "consumes: []\n")
	}

	mkdirAll(t, filepath.Join(root, "decisions"))
	writeFile(t, filepath.Join(root, "decisions", ".gitkeep"), "")
	mkdirAll(t, filepath.Join(root, "vendored"))
	writeFile(t, filepath.Join(root, "vendored", ".gitkeep"), "")
}

// RemoteURL returns the fixture origin's push/clone URL (a local
// filesystem path — no network involved).
func (f *Fixture) RemoteURL() string { return f.OriginDir }

// Clone returns the working clone directory for system, failing the test
// if system was not part of the fixture.
func (f *Fixture) Clone(system string) string {
	f.t.Helper()
	dir, ok := f.Clones[system]
	if !ok {
		f.t.Fatalf("spacefixture: no clone for system %q", system)
	}
	return dir
}

// HeadSHA returns the current HEAD commit SHA of the origin's main branch,
// as seen from a fresh fetch in dir (any clone of the fixture).
func (f *Fixture) HeadSHA(dir, ref string) string {
	f.t.Helper()
	out := runGitOutput(f.t, dir, "rev-parse", ref)
	return string(bytes.TrimSpace(out))
}

// commitEnv returns the GIT_AUTHOR_*/GIT_COMMITTER_* environment pairs
// used for every fixture-authored commit, so tests never depend on the
// host machine's global git user.name/user.email being configured.
func commitEnv() []string {
	return []string{
		"GIT_AUTHOR_NAME=a2a-fixture",
		"GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture",
		"GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	}
}

func mkdirAll(t testing.TB, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("spacefixture: mkdir %s: %v", dir, err)
	}
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("spacefixture: write %s: %v", path, err)
	}
}

// runGit runs `git <args...>` with cwd=dir (explicit argv, never sh -c),
// failing the test loudly on error.
func runGit(t testing.TB, dir string, args ...string) {
	t.Helper()
	runGitWithEnv(t, dir, nil, args...)
}

func runGitWithEnv(t testing.TB, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("spacefixture: git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
}

func runGitOutput(t testing.TB, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("spacefixture: git %v (dir=%s): %v\n%s", args, dir, err, stderr.String())
	}
	return out.Bytes()
}
