package cli_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
)

// runInitPointer runs `a2a init --agents-pointer ...` with AgentsPath wired to
// <dir>/AGENTS.md and returns exit code + the AGENTS.md path.
func runInitPointer(t *testing.T, dir string, extra ...string) (int, string) {
	t.Helper()
	cmd := cli.NewInitCommand(filepath.Join(dir, ".a2a", "config.yaml"))
	agents := filepath.Join(dir, "AGENTS.md")
	cmd.AgentsPath = agents
	io, _, errOut := newIO()
	args := append([]string{"--system", "axon", "--space", "https://example.invalid/org/space.git", "--agents-pointer"}, extra...)
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

// TestInitAgentsPointer_NotWrittenWithoutFlag: no flag => AGENTS.md is never
// created or touched (consent gate).
func TestInitAgentsPointer_NotWrittenWithoutFlag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cmd := cli.NewInitCommand(filepath.Join(dir, ".a2a", "config.yaml"))
	cmd.AgentsPath = filepath.Join(dir, "AGENTS.md")
	io, _, _ := newIO()
	code := cmd.Run(context.Background(),
		[]string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, io)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("AGENTS.md created without --agents-pointer (consent gate breached)")
	}
}
