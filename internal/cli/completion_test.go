package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// sampleCmds/sampleSubs are a representative injected inventory: it includes
// `contract` and `feedback` (two sub-verb families, whose subs gate on their
// own verb) plus verbs that sort before and after, so ordering and the
// N-family sub-verb branches are both exercised.
var (
	sampleCmds = []string{"submit", "contract", "init", "completion"}
	sampleSubs = map[string][]string{
		"contract": {"publish", "diff", "new"},
		"feedback": {"submit", "status", "new"},
	}
)

// allSubs flattens sampleSubs to every sub-verb name (dedup not needed — the
// containment assertions only check presence).
func allSubs() []string {
	var out []string
	for _, subs := range sampleSubs {
		out = append(out, subs...)
	}
	return out
}

// markerFor is the shell-specific string that proves the script is wired to
// register completion for `a2a` (not just a list of words).
var markerFor = map[string]string{
	"bash": "complete -F _a2a a2a",
	"zsh":  "#compdef a2a",
	"fish": "complete -c a2a",
}

func TestRenderCompletion_ContainsEveryName(t *testing.T) {
	t.Parallel()
	for _, shell := range CompletionShells {
		script, err := RenderCompletion(shell, sampleCmds, sampleSubs)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", shell, err)
		}
		if strings.TrimSpace(script) == "" {
			t.Fatalf("%s: empty script", shell)
		}
		if !strings.Contains(script, markerFor[shell]) {
			t.Errorf("%s: missing register marker %q", shell, markerFor[shell])
		}
		for _, name := range sampleCmds {
			if !strings.Contains(script, name) {
				t.Errorf("%s: top-level verb %q not in script", shell, name)
			}
		}
		for _, sub := range allSubs() {
			if !strings.Contains(script, sub) {
				t.Errorf("%s: sub-verb %q not in script", shell, sub)
			}
		}
	}
}

func TestRenderCompletion_UnknownShell(t *testing.T) {
	t.Parallel()
	if _, err := RenderCompletion("powershell", sampleCmds, sampleSubs); err == nil {
		t.Fatal("expected an error for an unknown shell, got nil")
	}
}

func TestRenderCompletion_Deterministic(t *testing.T) {
	t.Parallel()
	// Same inventory in a DIFFERENT order must render byte-identically: the
	// renderer sorts, so completion scripts don't churn across binary builds.
	shuffled := []string{"init", "completion", "submit", "contract"}
	for _, shell := range CompletionShells {
		a, err := RenderCompletion(shell, sampleCmds, sampleSubs)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		b, err := RenderCompletion(shell, shuffled, sampleSubs)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		if a != b {
			t.Errorf("%s: render not order-independent:\n--- a ---\n%s\n--- b ---\n%s", shell, a, b)
		}
	}
}

func TestCompletionCommand_Metadata(t *testing.T) {
	t.Parallel()
	cmd := NewCompletionCommand(nil, nil)
	if cmd.Name() != "completion" {
		t.Errorf("Name() = %q, want %q", cmd.Name(), "completion")
	}
	if cmd.Synopsis() == "" {
		t.Error("Synopsis() is empty")
	}
}

// TestCompletionCommand_Run covers the three exit paths of the command wrapper
// (advisor #4) so cmd_completion.go carries its own coverage weight.
func TestCompletionCommand_Run(t *testing.T) {
	t.Parallel()
	cmd := NewCompletionCommand(sampleCmds, sampleSubs)
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"ok", []string{"bash"}, 0},
		{"no-arg-usage", nil, 2},
		{"extra-arg-usage", []string{"bash", "zsh"}, 2},
		{"unknown-shell", []string{"powershell"}, 1},
	}
	for _, tt := range tests {
		var out, errBuf bytes.Buffer
		got := cmd.Run(context.Background(), tt.args, IO{Stdout: &out, Stderr: &errBuf})
		if got != tt.want {
			t.Errorf("%s: exit = %d, want %d (stderr=%q)", tt.name, got, tt.want, errBuf.String())
		}
		if tt.want == 0 && out.Len() == 0 {
			t.Errorf("%s: exit 0 but empty stdout", tt.name)
		}
		if tt.want != 0 && out.Len() != 0 {
			t.Errorf("%s: non-zero exit wrote to stdout: %q", tt.name, out.String())
		}
	}
}

// TestRenderCompletion_ShellSyntax is teeth: when a real shell is on PATH, the
// generated script must parse. Each shell is skipped independently when absent
// (CI/dev boxes vary), so this hardens without becoming a hard dependency.
func TestRenderCompletion_ShellSyntax(t *testing.T) {
	t.Parallel()
	// shell -> (binary, parse-only argv builder). fish uses --no-execute.
	checks := map[string]func(path, file string) *exec.Cmd{
		"bash": func(p, f string) *exec.Cmd { return exec.Command(p, "-n", f) },
		"zsh":  func(p, f string) *exec.Cmd { return exec.Command(p, "-n", f) },
		"fish": func(p, f string) *exec.Cmd { return exec.Command(p, "--no-execute", f) },
	}
	dir := t.TempDir()
	for _, shell := range CompletionShells {
		path, err := exec.LookPath(shell)
		if err != nil {
			t.Logf("%s not on PATH — skipping syntax check", shell)
			continue
		}
		script, err := RenderCompletion(shell, sampleCmds, sampleSubs)
		if err != nil {
			t.Fatalf("%s: render: %v", shell, err)
		}
		file := filepath.Join(dir, "a2a-completion-"+shell)
		if err := os.WriteFile(file, []byte(script), 0o644); err != nil {
			t.Fatalf("%s: write: %v", shell, err)
		}
		if out, err := checks[shell](path, file).CombinedOutput(); err != nil {
			t.Errorf("%s: generated script fails %s syntax check: %v\n%s", shell, shell, err, out)
		}
	}
}
