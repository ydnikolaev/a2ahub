package cli_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	spacetemplate "github.com/ydnikolaev/a2ahub/space-template"
)

// TestSpaceEmbedIncludesDotfiles is the dotfile-embed guard (spec 33 §12
// trap #1, AC-936.3): `//go:embed` skips dot-prefixed entries unless the
// pattern uses the `all:` prefix, and `all:*` alone still would not match
// `.github` — a template edit that drops the explicit `.github`/`all:`
// pattern from space-template/embed.go would silently vanish these files
// from spacetemplate.Files without failing the build. This test is the
// tripwire.
func TestSpaceEmbedIncludesDotfiles(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	if err := fs.WalkDir(spacetemplate.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			seen[p] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}

	for _, want := range []string{
		".github/workflows/a2a-validate.yml",
		".github/dependabot.yml",
	} {
		if !seen[want] {
			t.Errorf("embedded space-template is missing %q (dotfile-embed guard tripped) — seen: %v", want, seen)
		}
	}
}

// TestSpaceEmbedSentinelsPresent is the sentinel guard (spec 33 §12 trap
// #2): the embedded template must still carry the exact tokens `a2a space
// init` substitutes, so a future template edit cannot silently break
// substitution (it would fail THIS test instead of shipping a broken
// scaffold).
func TestSpaceEmbedSentinelsPresent(t *testing.T) {
	t.Parallel()

	spaceYAML, err := fs.ReadFile(spacetemplate.Files, "space.yaml")
	if err != nil {
		t.Fatalf("read space.yaml: %v", err)
	}
	if !strings.Contains(string(spaceYAML), "REPLACE_WITH_SPACE_ID") {
		t.Error("space.yaml no longer carries the REPLACE_WITH_SPACE_ID sentinel")
	}
	if !strings.Contains(string(spaceYAML), "min_binary_version:") {
		t.Error("space.yaml no longer carries a min_binary_version: field")
	}

	workflow, err := fs.ReadFile(spacetemplate.Files, ".github/workflows/a2a-validate.yml")
	if err != nil {
		t.Fatalf("read .github/workflows/a2a-validate.yml: %v", err)
	}
	if !strings.Contains(string(workflow), "a2a-validate-reusable.yml@v") {
		t.Error("a2a-validate.yml no longer carries an a2a-validate-reusable.yml@vX.Y.Z ref")
	}
}

func newSpaceIO() (cli.IO, *strings.Builder, *strings.Builder) {
	var out, errOut strings.Builder
	return cli.IO{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut}, &out, &errOut
}

// TestSpaceInitScaffolds is the table-driven core of AC-936.1/.2/.4: version
// substitution (never the baked template version), space-id substitution,
// the dev-version refusal, and the non-empty-dir refusal.
func TestSpaceInitScaffolds(t *testing.T) {
	t.Parallel()

	t.Run("version substitution pins the running binary's version, not the baked template version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "9.9.9")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "myspace", "--dir", target}, io)
		if code != 0 {
			t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}

		workflow, err := os.ReadFile(filepath.Join(target, ".github", "workflows", "a2a-validate.yml"))
		if err != nil {
			t.Fatalf("read scaffolded workflow: %v", err)
		}
		if !strings.Contains(string(workflow), "a2a-validate-reusable.yml@v9.9.9") {
			t.Errorf("scaffolded workflow does not pin @v9.9.9:\n%s", workflow)
		}
		if strings.Contains(string(workflow), "@v0.5.0") {
			t.Errorf("scaffolded workflow still pins the BAKED template version (@v0.5.0), not the running binary's version:\n%s", workflow)
		}

		spaceYAML, err := os.ReadFile(filepath.Join(target, "space.yaml"))
		if err != nil {
			t.Fatalf("read scaffolded space.yaml: %v", err)
		}
		if !strings.Contains(string(spaceYAML), "min_binary_version: 9.9.9") {
			t.Errorf("scaffolded space.yaml does not carry min_binary_version: 9.9.9:\n%s", spaceYAML)
		}
	})

	t.Run("space id substitution", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "1.2.3")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "getvisa", "--dir", target}, io)
		if code != 0 {
			t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}

		spaceYAML, err := os.ReadFile(filepath.Join(target, "space.yaml"))
		if err != nil {
			t.Fatalf("read scaffolded space.yaml: %v", err)
		}
		if !strings.Contains(string(spaceYAML), "space: getvisa") {
			t.Errorf("scaffolded space.yaml does not carry space: getvisa:\n%s", spaceYAML)
		}
		if strings.Contains(string(spaceYAML), "REPLACE_WITH_SPACE_ID") {
			t.Errorf("scaffolded space.yaml still carries the REPLACE_WITH_SPACE_ID sentinel:\n%s", spaceYAML)
		}
	})

	t.Run("dev-version build refuses to scaffold", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "dev")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "myspace", "--dir", target}, io)
		if code == 0 {
			t.Fatalf("Run: code = 0, want non-zero for a dev-version binary; stdout=%s", out.String())
		}
		if errOut.Len() == 0 {
			t.Error("expected an actionable error message on stderr")
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("expected nothing written for a refused dev-version build; target stat err = %v", err)
		}
	})

	t.Run("non-empty target dir refuses to scaffold", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(target, "preexisting.txt"), []byte("hi"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "1.2.3")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "myspace", "--dir", target}, io)
		if code == 0 {
			t.Fatalf("Run: code = 0, want non-zero for a non-empty target dir; stdout=%s", out.String())
		}
		if errOut.Len() == 0 {
			t.Error("expected an actionable error message on stderr")
		}
		if _, err := os.Stat(filepath.Join(target, "space.yaml")); !os.IsNotExist(err) {
			t.Error("expected space.yaml NOT to be written into a refused non-empty target dir")
		}
	})
}

// TestSpaceSubcommandsMatchesDispatch is the parity-gate tripwire mirroring
// ContractSubcommands' own role: SpaceSubcommands() must name exactly the
// sub-verbs SpaceCommand.Run's switch actually dispatches, so a future
// sub-verb added to one but not the other reds here instead of drifting
// silently into a real CLI/MCP parity gap.
func TestSpaceSubcommandsMatchesDispatch(t *testing.T) {
	t.Parallel()
	subs := cli.SpaceSubcommands()
	if len(subs) != 1 || subs[0].Name != "init" {
		t.Fatalf("SpaceSubcommands() = %+v, want exactly one row named \"init\"", subs)
	}
	if subs[0].Synopsis == "" {
		t.Error("SpaceSubcommands()[0].Synopsis is empty")
	}
}

// TestSpaceCommandStructLiteralWorks proves SpaceCommand's DI seams
// (writeFile/mkdirAll) are nil-safe: the lead's wiring may construct
// SpaceCommand as a struct literal (TemplateFiles/Version set directly)
// rather than through NewSpaceCommand.
func TestSpaceCommandStructLiteralWorks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "out")
	cmd := &cli.SpaceCommand{TemplateFiles: spacetemplate.Files, Version: "1.2.3"}

	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"init", "myspace", "--dir", target}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(target, "space.yaml")); err != nil {
		t.Fatalf("expected space.yaml to be written via struct-literal construction: %v", err)
	}
}

// TestSpaceUnknownSubcommand is the usage/dispatch guard mirroring
// ContractCommand's own convention.
func TestSpaceUnknownSubcommand(t *testing.T) {
	t.Parallel()
	cmd := cli.NewSpaceCommand(spacetemplate.Files, "1.2.3")
	io, _, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"bogus"}, io)
	if code != 2 {
		t.Fatalf("Run: code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), `unknown subcommand "bogus"`) {
		t.Errorf("stderr = %q, want it to name the unknown subcommand", errOut.String())
	}
}
