package cli

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/skill"
)

// runSkill drives SkillCommand over the real embedded tree and returns exit
// code + captured stdout/stderr.
func runSkill(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf strings.Builder
	code := NewSkillCommand(skill.Files, "test").Run(
		context.Background(), args, IO{Stdout: &out, Stderr: &errBuf})
	return code, out.String(), errBuf.String()
}

// TestSkillInstall_CleanRepo is the operator's clean-repo acceptance: install
// into a repo that ALREADY has a harness (AGENTS.md + a .claude/ skill), and
// prove (a) the harness is untouched, (b) the tree is written under our own
// namespace, byte-identical to the embedded source, (c) a provenance marker is
// present.
func TestSkillInstall_CleanRepo(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()

	// Pre-seed an existing harness that must NOT be clobbered.
	agents := filepath.Join(repo, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# My AGENTS\nexisting content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mineDir := filepath.Join(repo, ".claude", "skills", "mine")
	if err := os.MkdirAll(mineDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(mineDir, "SKILL.md")
	if err := os.WriteFile(mine, []byte("my own skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(repo, ".a2ahub", "skill")
	code, stdout, stderr := runSkill(t, "install", "--dir", target)
	if code != 0 {
		t.Fatalf("install exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "installed") || !strings.Contains(stdout, "SKILL.md") {
		t.Fatalf("stdout = %q, want an install summary naming SKILL.md", stdout)
	}

	// (a) existing harness untouched.
	if b, _ := os.ReadFile(agents); string(b) != "# My AGENTS\nexisting content\n" {
		t.Fatalf("AGENTS.md was modified: %q", b)
	}
	if b, _ := os.ReadFile(mine); string(b) != "my own skill\n" {
		t.Fatalf(".claude skill was modified: %q", b)
	}

	// (b) tree written, byte-identical to the embedded source.
	assertSkillTreeMatchesEmbed(t, target)

	// (c) provenance marker present and tagged.
	prov, err := os.ReadFile(filepath.Join(target, skillProvenanceFile))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if !strings.HasPrefix(string(prov), skillProvenanceTag) {
		t.Fatalf("provenance missing the machine tag; head=%q", string(prov)[:min(60, len(prov))])
	}
	if !strings.Contains(string(prov), "a2a skill install") || !strings.Contains(string(prov), "ydnikolaev/a2ahub") {
		t.Fatalf("provenance missing what/where: %q", prov)
	}
}

// assertSkillTreeMatchesEmbed walks the embedded a2ahub tree and asserts every
// file was written under target byte-identically (root prefix stripped).
func assertSkillTreeMatchesEmbed(t *testing.T, target string) {
	t.Helper()
	err := fs.WalkDir(skill.Files, "a2ahub", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "a2ahub"), "/")
		want, _ := fs.ReadFile(skill.Files, p)
		got, readErr := os.ReadFile(filepath.Join(target, rel))
		if readErr != nil {
			t.Fatalf("installed file missing: %s: %v", rel, readErr)
		}
		if string(got) != string(want) {
			t.Fatalf("installed %s differs from embedded source", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embed: %v", err)
	}
}

// TestSkillInstall_RefusesForeignTarget: a non-empty target WITHOUT our
// provenance marker is someone else's content — refuse (exit 1) and write
// nothing, unless --force.
func TestSkillInstall_RefusesForeignTarget(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	foreign := filepath.Join(target, "user-file.md")
	if err := os.WriteFile(foreign, []byte("do not touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runSkill(t, "install", "--dir", target)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (refuse foreign target)", code)
	}
	if !strings.Contains(stderr, "refusing to overwrite") {
		t.Fatalf("stderr = %q, want a refusal message", stderr)
	}
	if b, _ := os.ReadFile(foreign); string(b) != "do not touch\n" {
		t.Fatalf("foreign file was modified: %q", b)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("SKILL.md written into a refused target")
	}

	// --force overrides.
	code, _, stderr = runSkill(t, "install", "--dir", target, "--force")
	if code != 0 {
		t.Fatalf("--force exit = %d, want 0; stderr=%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("--force did not install: %v", err)
	}
}

// TestSkillInstall_IdempotentRefresh: re-installing into OUR OWN target (marker
// present) refreshes without --force.
func TestSkillInstall_IdempotentRefresh(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if code, _, stderr := runSkill(t, "install", "--dir", target); code != 0 {
		t.Fatalf("first install exit = %d; stderr=%s", code, stderr)
	}
	// Second install: our marker is present -> owned -> refresh, exit 0.
	code, _, stderr := runSkill(t, "install", "--dir", target)
	if code != 0 {
		t.Fatalf("refresh exit = %d, want 0; stderr=%s", code, stderr)
	}
	assertSkillTreeMatchesEmbed(t, target)
}

// TestSkillInstall_EmptyDirOK: an existing but EMPTY target installs cleanly
// (treated as unowned-but-safe).
func TestSkillInstall_EmptyDirOK(t *testing.T) {
	t.Parallel()
	target := t.TempDir() // exists, empty
	if code, _, stderr := runSkill(t, "install", "--dir", target); code != 0 {
		t.Fatalf("empty-dir install exit = %d; stderr=%s", code, stderr)
	}
}

func TestSkillInstall_UsageErrors(t *testing.T) {
	t.Parallel()
	if code, _, _ := runSkill(t); code != 2 {
		t.Errorf("no subcommand: exit = %d, want 2", code)
	}
	if code, _, _ := runSkill(t, "bogus"); code != 2 {
		t.Errorf("unknown subcommand: exit = %d, want 2", code)
	}
	if code, _, _ := runSkill(t, "install", "extra-arg"); code != 2 {
		t.Errorf("unexpected arg: exit = %d, want 2", code)
	}
}
