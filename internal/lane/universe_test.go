package lane

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDenyDirsReadsFromClassifyGuardScript(t *testing.T) {
	dirs, err := denyDirs("testdata/universe-fixture")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{".agents", ".claude"}
	if len(dirs) != len(want) {
		t.Fatalf("got %v, want %v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("dirs[%d] = %q, want %q", i, dirs[i], want[i])
		}
	}
}

// TestUniverseUnionsTrackedAndPresentDenyDirs proves Universe = git ls-files
// UNION a walk of the present DENY_DIRS roots, using a real (throwaway) git
// repo — never the live checkout, which three sibling agents are editing
// concurrently this wave.
func TestUniverseUnionsTrackedAndPresentDenyDirs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")

	mustWrite(t, filepath.Join(root, "README.md"), "hi\n")
	mustWrite(t, filepath.Join(root, "scripts", "classify-guard.sh"), "#!/usr/bin/env bash\nDENY_DIRS=( .agents .claude )\n")
	run("add", "-A")
	run("commit", "-q", "-m", "init")

	// .agents is present and untracked (by design — classify-guard forbids
	// tracking it), .claude is ABSENT entirely: it must be skipped, not
	// refused.
	mustWrite(t, filepath.Join(root, ".agents", "notes.md"), "private\n")

	universe, err := Universe(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	set := map[string]bool{}
	for _, p := range universe {
		set[p] = true
	}
	if !set["README.md"] {
		t.Errorf("tracked README.md missing from universe: %v", universe)
	}
	if !set[".agents/notes.md"] {
		t.Errorf("present-but-untracked .agents/notes.md missing from universe: %v", universe)
	}
	for p := range set {
		if strings.HasPrefix(p, ".claude/") {
			t.Errorf("absent DENY_DIRS root .claude must be skipped, not walked: found %q", p)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
