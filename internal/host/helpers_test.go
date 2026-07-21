package host

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// commitInDir writes relPath (relative to dir) with content, stages it and
// commits it — explicit argv, no shell, deterministic author identity so
// the test never depends on the host machine's global git config.
func commitInDir(t testing.TB, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", full, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	runGit(t, dir, nil, "add", relPath)
	env := []string{
		"GIT_AUTHOR_NAME=a2a-test", "GIT_AUTHOR_EMAIL=test@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-test", "GIT_COMMITTER_EMAIL=test@a2ahub.invalid",
	}
	runGit(t, dir, env, "commit", "-m", "test: "+relPath)
}

// runGitClone clones remoteURL into dest (explicit argv).
func runGitClone(t testing.TB, remoteURL, dest string) {
	t.Helper()
	runGit(t, "", nil, "clone", remoteURL, dest)
}

// runGit runs `git <args...>` with cwd=dir (dir=="" uses the process cwd),
// optionally with extraEnv appended, failing the test loudly on error.
func runGit(t testing.TB, dir string, extraEnv []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
}
