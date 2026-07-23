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

// TestInitDefaultOnboarding: a bare `a2a init` (no opt-out flags) with both
// seams wired does the FULL setup — config + skill tree + AGENTS.md pointer —
// and preserves existing AGENTS.md content. This is the Option-1 default.
func TestInitDefaultOnboarding(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agents := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# Consumer\nkeep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := cli.NewInitCommand(filepath.Join(dir, ".a2a", "config.yaml"))
	cmd.AgentsPath = agents
	cmd.SkillFiles = skill.Files
	cmd.SkillTarget = filepath.Join(dir, ".a2ahub", "skill")
	cmd.Version = "test"

	io, _, errOut := newIO()
	code := cmd.Run(context.Background(),
		[]string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, io)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errOut.String())
	}
	// Skill tree installed.
	if _, err := os.Stat(filepath.Join(dir, ".a2ahub", "skill", "SKILL.md")); err != nil {
		t.Fatalf("skill not installed by default: %v", err)
	}
	// Pointer written, existing content preserved.
	b, _ := os.ReadFile(agents)
	if !strings.HasPrefix(string(b), "# Consumer\nkeep me\n") {
		t.Fatalf("existing AGENTS.md content lost: %q", b)
	}
	if !strings.Contains(string(b), pointerStartSub) {
		t.Fatalf("pointer not written by default: %q", b)
	}
}

// runInitPointer runs `a2a init ...` (pointer is default-on) with AgentsPath
// wired to <dir>/AGENTS.md and returns exit code + the AGENTS.md path. It does
// NOT wire SkillFiles, so the skill step is a no-op — this isolates the pointer.
func runInitPointer(t *testing.T, dir string, extra ...string) (int, string) {
	t.Helper()
	cmd := cli.NewInitCommand(filepath.Join(dir, ".a2a", "config.yaml"))
	agents := filepath.Join(dir, "AGENTS.md")
	cmd.AgentsPath = agents
	io, _, errOut := newIO()
	args := append([]string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, extra...)
	code := cmd.Run(context.Background(), args, io)
	if code != 0 {
		t.Logf("stderr: %s", errOut.String())
	}
	return code, agents
}

const (
	pointerStartSub = "a2ahub:pointer:start"
	pointerEndSub   = "a2ahub:pointer:end"
	pointerSkillRef = ".a2ahub/skill/SKILL.md"
)

func TestInitAgentsPointer_CreatesWhenAbsent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, agents := runInitPointer(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	b, err := os.ReadFile(agents)
	if err != nil {
		t.Fatalf("AGENTS.md not created: %v", err)
	}
	s := string(b)
	for _, want := range []string{pointerStartSub, pointerEndSub, pointerSkillRef, "a2a skill install"} {
		if !strings.Contains(s, want) {
			t.Fatalf("AGENTS.md missing %q; got:\n%s", want, s)
		}
	}
}

// TestInitAgentsPointer_AppendsPreservingExisting is the no-clobber guarantee:
// an existing AGENTS.md keeps every byte it had; the block is appended after.
func TestInitAgentsPointer_AppendsPreservingExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agents := filepath.Join(dir, "AGENTS.md")
	existing := "# Consumer AGENTS\n\nMy own rules that must survive.\n"
	if err := os.WriteFile(agents, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _ := runInitPointer(t, dir)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	b, _ := os.ReadFile(agents)
	s := string(b)
	if !strings.HasPrefix(s, existing) {
		t.Fatalf("existing content was not preserved verbatim at the head; got:\n%s", s)
	}
	if !strings.Contains(s, pointerStartSub) || !strings.Contains(s, "My own rules that must survive.") {
		t.Fatalf("want both existing content and the pointer block; got:\n%s", s)
	}
}

// TestInitAgentsPointer_Idempotent: a second run adds no second block.
func TestInitAgentsPointer_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if code, _ := runInitPointer(t, dir); code != 0 {
		t.Fatalf("first run exit = %d", code)
	}
	code, agents := runInitPointer(t, dir)
	if code != 0 {
		t.Fatalf("second run exit = %d", code)
	}
	b, _ := os.ReadFile(agents)
	if n := strings.Count(string(b), pointerStartSub); n != 1 {
		t.Fatalf("pointer block appears %d times, want exactly 1 (idempotent)", n)
	}
}

// TestInitAgentsPointer_OptOut: --no-agents-pointer suppresses the default
// pointer write — AGENTS.md is never created or touched.
func TestInitAgentsPointer_OptOut(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	code, agents := runInitPointer(t, dir, "--no-agents-pointer")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := os.Stat(agents); !os.IsNotExist(err) {
		t.Fatal("AGENTS.md created despite --no-agents-pointer")
	}
}
