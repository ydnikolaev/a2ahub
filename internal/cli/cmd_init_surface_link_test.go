package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/skill"
)

// newInitCommandWithFullOnboarding wires every P32 onboarding seam
// (skill install, surface link, AGENTS.md pointer, CLAUDE.md pointer) at
// once, mirroring TestInitDefaultOnboarding's shape.
func newInitCommandWithFullOnboarding(dir string) *cli.InitCommand {
	cmd := cli.NewInitCommand(filepath.Join(dir, ".a2a", "config.yaml"))
	cmd.AgentsPath = filepath.Join(dir, "AGENTS.md")
	cmd.ClaudeMdPath = filepath.Join(dir, "CLAUDE.md")
	cmd.SkillFiles = skill.Files
	cmd.SkillTarget = filepath.Join(dir, ".a2ahub", "skill")
	cmd.ProjectRoot = dir
	cmd.Version = "test"
	return cmd
}

func runInitFullOnboarding(t *testing.T, dir string, extra ...string) (int, string, string) {
	t.Helper()
	cmd := newInitCommandWithFullOnboarding(dir)
	io, out, errOut := newIO()
	args := append([]string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, extra...)
	code := cmd.Run(context.Background(), args, io)
	return code, out.String(), errOut.String()
}

// TestInitLinksOnlyDetectedSurfaces is AC-917.1: init links exactly the
// surfaces the repo already shows (.claude/ present here, .codex/ absent)
// and reports what it linked; the undetected surface is never invented.
func TestInitLinksOnlyDetectedSurfaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runInitFullOnboarding(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "init: linked claude:") {
		t.Fatalf("stdout = %q, want a linked-claude report line", stdout)
	}
	if strings.Contains(stdout, "linked codex:") {
		t.Fatalf("stdout = %q, want no codex link (surface not detected)", stdout)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "skills", "a2ahub")); err != nil {
		t.Fatalf(".claude/skills/a2ahub not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".codex")); !os.IsNotExist(err) {
		t.Fatal(".codex was invented despite no detected surface")
	}
}

// TestInitNoSkillLinkOptsOut: --no-skill-link suppresses the link step even
// when a surface is detected.
func TestInitNoSkillLinkOptsOut(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runInitFullOnboarding(t, dir, "--no-skill-link")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if strings.Contains(stdout, "linked claude:") {
		t.Fatalf("stdout = %q, want no link report under --no-skill-link", stdout)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "skills", "a2ahub")); !os.IsNotExist(err) {
		t.Fatal("skill was linked despite --no-skill-link")
	}
}

// TestInitLinkStepNoopWithoutInstalledSkill: --no-skill (skill never
// installed) means the link step has nothing to point at — it must not
// create a dangling link.
func TestInitLinkStepNoopWithoutInstalledSkill(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runInitFullOnboarding(t, dir, "--no-skill")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if _, err := os.Lstat(filepath.Join(dir, ".claude", "skills", "a2ahub")); !os.IsNotExist(err) {
		t.Fatal("a dangling link was created despite the skill never being installed")
	}
}

// --- CLAUDE.md three-way (spec 32 §2.3) -----------------------------------

// TestInitClaudeMd_AlreadyBridgedIsNoop: an existing CLAUDE.md that already
// imports @AGENTS.md is left byte-for-byte untouched.
func TestInitClaudeMd_AlreadyBridgedIsNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claudeMd := filepath.Join(dir, "CLAUDE.md")
	existing := "# My CLAUDE.md\n@AGENTS.md\n"
	if err := os.WriteFile(claudeMd, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runInitFullOnboarding(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	b, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != existing {
		t.Fatalf("CLAUDE.md was modified despite already importing @AGENTS.md; got:\n%s", b)
	}
	if !strings.Contains(stdout, "already bridged") {
		t.Fatalf("stdout = %q, want an already-bridged note", stdout)
	}
}

// TestInitClaudeMd_AppendsPointerWhenExisting: an existing CLAUDE.md WITHOUT
// an @AGENTS.md import gets the same marker-fenced pointer block appended,
// preserving its own content.
func TestInitClaudeMd_AppendsPointerWhenExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	claudeMd := filepath.Join(dir, "CLAUDE.md")
	existing := "# My CLAUDE.md\n\nMy own rules that must survive.\n"
	if err := os.WriteFile(claudeMd, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := runInitFullOnboarding(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	b, err := os.ReadFile(claudeMd)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, existing) {
		t.Fatalf("existing CLAUDE.md content not preserved verbatim at the head; got:\n%s", s)
	}
	if !strings.Contains(s, "a2ahub:pointer:start") {
		t.Fatalf("pointer block not appended to CLAUDE.md; got:\n%s", s)
	}

	// Idempotent: a second run adds no second block.
	if code, _, stderr := runInitFullOnboarding(t, dir); code != 0 {
		t.Fatalf("second run exit = %d; stderr=%s", code, stderr)
	}
	b2, _ := os.ReadFile(claudeMd)
	if n := strings.Count(string(b2), "a2ahub:pointer:start"); n != 1 {
		t.Fatalf("pointer block appears %d times after re-run, want exactly 1", n)
	}
}

// TestInitClaudeMd_NeitherExistsSkipsCreation: no CLAUDE.md present ->
// none is created; the skill link alone is discovery-sufficient (spec 32
// §2.3.3).
func TestInitClaudeMd_NeitherExistsSkipsCreation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	code, _, stderr := runInitFullOnboarding(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("CLAUDE.md was created despite neither existing nor being asked for")
	}
}
