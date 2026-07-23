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

// TestSkillInstall_RefreshPrunesStaleFiles: a refresh MIRRORS the embedded
// tree — a file left from a prior install that the current tree no longer ships
// is removed, not orphaned.
func TestSkillInstall_RefreshPrunesStaleFiles(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	if code, _, stderr := runSkill(t, "install", "--dir", target); code != 0 {
		t.Fatalf("first install exit = %d; stderr=%s", code, stderr)
	}
	// Simulate a file a PRIOR skill version shipped that the current tree drops.
	stale := filepath.Join(target, "reference", "authoring", "obsolete-type.md")
	if err := os.WriteFile(stale, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := runSkill(t, "install", "--dir", target); code != 0 {
		t.Fatalf("refresh exit = %d; stderr=%s", code, stderr)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale file survived the refresh — tree is not mirrored")
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

// --- skill link (P32) -----------------------------------------------------

// runSkillLink drives `a2a skill link <args...>` with ProjectRoot wired to
// root and returns exit code + captured stdout/stderr.
func runSkillLink(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var out, errBuf strings.Builder
	cmd := NewSkillCommand(skill.Files, "test")
	cmd.ProjectRoot = root
	code := cmd.Run(context.Background(), append([]string{"link"}, args...),
		IO{Stdout: &out, Stderr: &errBuf})
	return code, out.String(), errBuf.String()
}

// installSSOT installs the embedded skill tree at <root>/.a2ahub/skill (the
// skillDefaultDir runLink guards on) and returns that path.
func installSSOT(t *testing.T, root string) string {
	t.Helper()
	target := filepath.Join(root, skillDefaultDir)
	if code, _, stderr := runSkill(t, "install", "--dir", target); code != 0 {
		t.Fatalf("install SSOT exit = %d; stderr=%s", code, stderr)
	}
	return target
}

// TestSkillLink_ExplicitSurfaceResolvesToSSOT is AC-916.1 + AC-916.2 (mode
// reporting): `skill link --surface claude` installs a symlink discovery
// entry that resolves to the installed SSOT tree's own SKILL.md.
func TestSkillLink_ExplicitSurfaceResolvesToSSOT(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installSSOT(t, root)
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSkillLink(t, root, "--surface", "claude")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "linked claude: .claude/skills/a2ahub (") {
		t.Fatalf("stdout = %q, want a linked-summary line naming the mode", stdout)
	}

	linked := filepath.Join(root, ".claude", "skills", "a2ahub", "SKILL.md")
	got, err := os.ReadFile(linked)
	if err != nil {
		t.Fatalf("linked entry unreadable: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(root, skillDefaultDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("linked SKILL.md does not resolve to the installed SSOT tree")
	}
}

// TestSkillLink_RefusesWithoutSSOTInstalled: linking before installing is a
// dangling pointer — refused with an actionable message.
func TestSkillLink_RefusesWithoutSSOTInstalled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runSkillLink(t, root, "--surface", "claude")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "run 'a2a skill install' first") {
		t.Fatalf("stderr = %q, want the install-first guidance", stderr)
	}
}

// TestSkillLink_UnknownSurface: an unrecognized --surface id is a usage
// error (exit 2), not a link attempt.
func TestSkillLink_UnknownSurface(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installSSOT(t, root)
	code, _, stderr := runSkillLink(t, root, "--surface", "bogus")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, `unknown surface "bogus"`) {
		t.Fatalf("stderr = %q, want an unknown-surface message", stderr)
	}
}

// TestSkillLink_NoSurfaceDetected: no --surface and no known marker dir
// present -> nothing to link, exit 0, no directory invented.
func TestSkillLink_NoSurfaceDetected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installSSOT(t, root)
	code, stdout, stderr := runSkillLink(t, root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "nothing to link") {
		t.Fatalf("stdout = %q, want a nothing-to-link message", stdout)
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Fatal(".claude was invented despite no detected surface")
	}
}

// TestSkillLink_ForeignTarget_ExplicitFailsWithoutForce is AC-917.2 (explicit
// half): a foreign a2ahub entry under an explicitly-named surface is refused
// (exit 1) and left untouched; --force overwrites it.
func TestSkillLink_ForeignTarget_ExplicitFailsWithoutForce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installSSOT(t, root)
	foreignDir := filepath.Join(root, ".claude", "skills", "a2ahub")
	if err := os.MkdirAll(foreignDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreignFile := filepath.Join(foreignDir, "mine.md")
	if err := os.WriteFile(foreignFile, []byte("do not touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runSkillLink(t, root, "--surface", "claude")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (refuse foreign target)", code)
	}
	if !strings.Contains(stderr, "refusing") {
		t.Fatalf("stderr = %q, want a refusal message", stderr)
	}
	if b, err := os.ReadFile(foreignFile); err != nil || string(b) != "do not touch\n" {
		t.Fatalf("foreign content was modified: err=%v content=%q", err, b)
	}

	code, _, stderr = runSkillLink(t, root, "--surface", "claude", "--force")
	if code != 0 {
		t.Fatalf("--force exit = %d, want 0; stderr=%s", code, stderr)
	}
	if _, err := os.Stat(foreignFile); !os.IsNotExist(err) {
		t.Fatal("--force did not replace the foreign target")
	}
}

// TestSkillLink_ForeignTarget_DetectAllWarnsAndContinues is AC-917.2's
// detect-all half: a foreign entry under a DETECTED (not explicitly named)
// surface degrades to a warning, not a hard failure.
func TestSkillLink_ForeignTarget_DetectAllWarnsAndContinues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	installSSOT(t, root)
	foreignDir := filepath.Join(root, ".claude", "skills", "a2ahub")
	if err := os.MkdirAll(foreignDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignDir, "mine.md"), []byte("do not touch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runSkillLink(t, root)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (detect-all warns, does not hard-fail); stderr=%s", code, stderr)
	}
	if !strings.Contains(stderr, "refusing") {
		t.Fatalf("stderr = %q, want a warning about the foreign target", stderr)
	}
	if _, err := os.Stat(filepath.Join(foreignDir, "mine.md")); err != nil {
		t.Fatal("foreign content was removed despite detect-all warn-and-continue")
	}
}

func TestSkillLink_NoProjectRoot(t *testing.T) {
	t.Parallel()
	var out, errBuf strings.Builder
	code := NewSkillCommand(skill.Files, "test").Run(context.Background(),
		[]string{"link"}, IO{Stdout: &out, Stderr: &errBuf})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
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
