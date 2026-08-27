package cli_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
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
	if !strings.Contains(string(spaceYAML), "replace-with-space-id") {
		t.Error("space.yaml no longer carries the replace-with-space-id sentinel")
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

	notify, err := fs.ReadFile(spacetemplate.Files, ".github/workflows/a2a-notify.yml")
	if err != nil {
		t.Fatalf("read .github/workflows/a2a-notify.yml: %v", err)
	}
	if !strings.Contains(string(notify), "a2a-notify-reusable.yml@v") {
		t.Error("a2a-notify.yml no longer carries an a2a-notify-reusable.yml@vX.Y.Z ref")
	}
}

// TestSpaceEmbedNotifyCallerHasNoLogic is spec 05 AC1: the notification
// caller carries no logic beyond `uses:`, `with:` and `secrets:` — no `run:`
// step at all (US-1: a space carries no notification logic of its own).
func TestSpaceEmbedNotifyCallerHasNoLogic(t *testing.T) {
	t.Parallel()

	raw, err := fs.ReadFile(spacetemplate.Files, ".github/workflows/a2a-notify.yml")
	if err != nil {
		t.Fatalf("read .github/workflows/a2a-notify.yml: %v", err)
	}
	content := string(raw)

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "run:") {
			t.Errorf("a2a-notify.yml carries a run: step (%q) — spec 05 AC1 requires no logic beyond uses:/with:/secrets:", line)
		}
	}
	for _, want := range []string{"uses:", "with:", "secrets:"} {
		if !strings.Contains(content, want) {
			t.Errorf("a2a-notify.yml is missing %q — the thin-caller shape (spec 05 AC1):\n%s", want, content)
		}
	}
	if strings.Contains(content, "pull_request_target:") {
		t.Error("a2a-notify.yml must never trigger on pull_request_target — it would expose TG_BOT_TOKEN to fork code")
	}
	if !strings.Contains(content, "secrets:\n") || !strings.Contains(content, "TG_BOT_TOKEN:") {
		t.Errorf("a2a-notify.yml does not declare TG_BOT_TOKEN explicitly (never secrets: inherit — spec 05 US-5):\n%s", content)
	}
	if strings.Contains(content, "secrets: inherit") {
		t.Error("a2a-notify.yml uses `secrets: inherit` — forbidden (spec 05 US-5: a space may be private)")
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

	t.Run("notify caller version substitution pins the running binary's version, not the baked template version", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "9.9.9")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "myspace", "--dir", target}, io)
		if code != 0 {
			t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}

		notify, err := os.ReadFile(filepath.Join(target, ".github", "workflows", "a2a-notify.yml"))
		if err != nil {
			t.Fatalf("read scaffolded a2a-notify.yml: %v", err)
		}
		if !strings.Contains(string(notify), "a2a-notify-reusable.yml@v9.9.9") {
			t.Errorf("scaffolded notify caller does not pin @v9.9.9:\n%s", notify)
		}
		if strings.Contains(string(notify), "@v0.23.0") {
			t.Errorf("scaffolded notify caller still pins the BAKED template version (@v0.23.0), not the running binary's version:\n%s", notify)
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
		if strings.Contains(string(spaceYAML), "replace-with-space-id") {
			t.Errorf("scaffolded space.yaml still carries the replace-with-space-id sentinel:\n%s", spaceYAML)
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

	// D8 (no-silent-yes-2026-08/P3 stage 2, spec 03 §8 AC 6/14): once the
	// sentinel is renamed to the conforming "replace-with-space-id", that
	// exact string ALSO parses as an ordinary (if unlikely) real space id
	// — bytes.ReplaceAll alone can no longer tell "the caller typed the
	// placeholder" apart from "the caller wants a space literally named
	// that". `a2a space init` must refuse rather than silently scaffold a
	// template that still carries its own sentinel.
	t.Run("space id equal to the sentinel refuses to scaffold", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "1.2.3")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "replace-with-space-id", "--dir", target}, io)
		if code == 0 {
			t.Fatalf("Run: code = 0, want non-zero for spaceID == the sentinel; stdout=%s", out.String())
		}
		if !strings.Contains(errOut.String(), "sentinel") {
			t.Errorf("expected stderr to name the sentinel, got: %s", errOut.String())
		}
		if _, err := os.Stat(target); !os.IsNotExist(err) {
			t.Errorf("expected nothing written when spaceID == the sentinel; target stat err = %v", err)
		}
	})

	// A space id that merely CONTAINS the sentinel as a substring (rather
	// than equalling it) is an ordinary, legal id — spec 03 §6's own edge
	// case ("a space whose id merely CONTAINS the sentinel substring").
	t.Run("space id containing the sentinel as a substring is legal", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		target := filepath.Join(dir, "out")
		cmd := cli.NewSpaceCommand(spacetemplate.Files, "1.2.3")

		io, out, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "my-replace-with-space-id-fork", "--dir", target}, io)
		if code != 0 {
			t.Fatalf("Run: code = %d, want 0 for an id merely CONTAINING the sentinel; stdout=%s stderr=%s", code, out.String(), errOut.String())
		}
		spaceYAML, err := os.ReadFile(filepath.Join(target, "space.yaml"))
		if err != nil {
			t.Fatalf("read scaffolded space.yaml: %v", err)
		}
		if !strings.Contains(string(spaceYAML), "space: my-replace-with-space-id-fork") {
			t.Errorf("scaffolded space.yaml does not carry the full id:\n%s", spaceYAML)
		}
	})
}

// TestSpaceInitResidualStepsNameAutoMerge is WAVE M2 / spec 45 AC-1050.6: the
// printed residual manual steps must name enabling auto-merge alongside the
// existing CODEOWNERS/push/branch-protection items — GitHub's
// `allow_auto_merge` repo setting is OFF by default on a freshly created
// repo, which is exactly what `space init` produces, and the operator has no
// remote to check it against yet at scaffold time (the directive must print
// regardless of whether the repo exists on GitHub already).
func TestSpaceInitResidualStepsNameAutoMerge(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "out")
	cmd := cli.NewSpaceCommand(spacetemplate.Files, "1.2.3")

	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"init", "myspace", "--dir", target}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "Allow auto-merge") {
		t.Fatalf("stdout = %q, want the residual steps to name enabling auto-merge", out.String())
	}
}

// TestSpaceSubcommandsMatchesDispatch is the parity-gate tripwire mirroring
// ContractSubcommands' own role: SpaceSubcommands() must name exactly the
// sub-verbs SpaceCommand.Run's switch actually dispatches, so a future
// sub-verb added to one but not the other reds here instead of drifting
// silently into a real CLI/MCP parity gap.
func TestSpaceSubcommandsMatchesDispatch(t *testing.T) {
	t.Parallel()
	subs := cli.SpaceSubcommands()
	if len(subs) != 2 || subs[0].Name != "init" || subs[1].Name != "update" {
		t.Fatalf("SpaceSubcommands() = %+v, want exactly two rows named \"init\", \"update\"", subs)
	}
	for _, s := range subs {
		if s.Synopsis == "" {
			t.Errorf("SpaceSubcommands() row %q has an empty Synopsis", s.Name)
		}
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

// --- `a2a space update` (spec 35) ---------------------------------------

// spaceUpdateTemplateFS is a byte-exact, hand-built template fixture
// (testing/fstest.MapFS, per plan 35's advice: the diff logic is
// template-content-agnostic, so a synthetic fixture is the right tool —
// it gives full control of both sides of the diff and turns AC-951.1 into
// a two-line addition instead of mutating the real embed). Deliberately
// mirrors the REAL template's disposition-relevant paths (space.yaml,
// CODEOWNERS, the three "managed" files) with minimal bodies.
func spaceUpdateTemplateFS() fstest.MapFS {
	return fstest.MapFS{
		// The template's own declared floor is deliberately BELOW the
		// binary versions these tests run at (9.9.9) — that gap is what
		// proves the floor tracks the TEMPLATE, never the running binary
		// (spaceUpdateFloor).
		"space.yaml": &fstest.MapFile{Data: []byte(
			"schema: manifest/v1\n" +
				"space: replace-with-space-id\n" +
				"min_binary_version: 0.4.0\n" +
				"participants: []\n",
		)},
		"CODEOWNERS": &fstest.MapFile{Data: []byte(
			"/space.yaml @REPLACE_WITH_ORG/space-owners\n",
		)},
		"README.md": &fstest.MapFile{Data: []byte("# space template\n")},
		".github/workflows/a2a-validate.yml": &fstest.MapFile{Data: []byte(
			"jobs:\n  a2a-validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v0.1.0\n",
		)},
		".github/dependabot.yml": &fstest.MapFile{Data: []byte("version: 2\n")},
		"BRANCH-PROTECTION.md":   &fstest.MapFile{Data: []byte("# branch protection checklist\n")},
	}
}

// spaceUpdateHostCfg is a non-zero-value SubmitHostConfig fixture — every
// field is compared against the zero value by spaceUpdateWired's "not
// wired" gate, so an update test that DOES want the command wired must set
// something in every one of these.
func spaceUpdateHostCfg() cli.SubmitHostConfig {
	return cli.SubmitHostConfig{
		RemoteURL:         "https://example.invalid/getvisa.git",
		Repo:              host.Repo{Owner: "acme", Name: "getvisa"},
		BaseBranch:        "main",
		Credential:        host.Credential{Token: "tok"},
		CommitAuthorName:  "a2a-bot",
		CommitAuthorEmail: "bot@example.invalid",
	}
}

// spaceUpdateFullyWiredCommand builds a *cli.SpaceCommand with every
// update-only DI field set (the "wired" baseline every test below starts
// from and then deliberately un-sets one field of, or leaves fully intact).
func spaceUpdateFullyWiredCommand(tmpl fstest.MapFS, mirrorDir string, funnel *fakeSubmitFunnel, version string) *cli.SpaceCommand {
	return &cli.SpaceCommand{
		TemplateFiles: tmpl,
		Version:       version,
		Funnel:        funnel,
		MirrorDir:     mirrorDir,
		SpaceID:       "getvisa",
		OwnSystem:     "a2a-hub",
		HostCfg:       spaceUpdateHostCfg(),
	}
}

// writeSpaceMirrorFile is a small test helper: write content under
// mirrorDir at relPath (slash-separated), creating parent dirs as needed.
func writeSpaceMirrorFile(t *testing.T, mirrorDir, relPath, content string) {
	t.Helper()
	full := filepath.Join(mirrorDir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// TestSpaceUpdateNotWired is the nil-panic-vs-clear-refusal guard: every
// update-only DI field is nil-able so `space init` keeps working
// (TestSpaceCommandStructLiteralWorks already proves that for
// TemplateFiles/Version); `space update` must fail with a clear message,
// exit 1, rather than nil-panic, when it is not fully wired.
func TestSpaceUpdateNotWired(t *testing.T) {
	t.Parallel()
	cmd := &cli.SpaceCommand{TemplateFiles: spaceUpdateTemplateFS(), Version: "1.2.3"}
	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"update"}, io)
	if code != 1 {
		t.Fatalf("Run: code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "not wired") {
		t.Errorf("stderr = %q, want it to say the command is not wired", errOut.String())
	}
}

// TestSpaceUpdateDevVersionRefuses mirrors `space init`'s own dev-version
// refusal (fail closed): a dev build must not pin a caller ref or
// min_binary_version to a release that does not exist. Asserts the funnel
// is never reached.
func TestSpaceUpdateDevVersionRefuses(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "dev")

	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"update"}, io)
	if code == 0 {
		t.Fatalf("Run: code = 0, want non-zero for a dev-version binary; stdout=%s", out.String())
	}
	if errOut.Len() == 0 {
		t.Error("expected an actionable error message on stderr")
	}
	if len(funnel.calls) != 0 {
		t.Errorf("funnel.calls = %d, want 0 — a refused dev-version build must never reach the funnel", len(funnel.calls))
	}
}

// TestSpaceUpdateSpaceFlagMismatch asserts `--space <id>` naming a space
// other than the one wired is refused (today exactly one space is
// connectable; spec 35 §6 defers `--all`).
func TestSpaceUpdateSpaceFlagMismatch(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "1.2.3")

	io, _, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"update", "--space", "other-space"}, io)
	if code != 1 {
		t.Fatalf("Run: code = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), `"other-space"`) {
		t.Errorf("stderr = %q, want it to name the unrecognized space", errOut.String())
	}
	if len(funnel.calls) != 0 {
		t.Error("funnel must not be called for a space-flag mismatch")
	}
}

// spaceUpdateSeedMirror populates mirrorDir the way a pre-existing
// (pre-P33) space would look: a real, customized space.yaml/CODEOWNERS,
// and NONE of the three "managed" infra files yet.
func spaceUpdateSeedMirror(t *testing.T, mirrorDir string) {
	t.Helper()
	writeSpaceMirrorFile(t, mirrorDir, "space.yaml",
		"schema: manifest/v1\n"+
			"space: getvisa\n"+
			"min_binary_version: 0.1.0\n"+
			"participants:\n"+
			"  - system: getvisa-core\n"+
			"    org: acme\n"+
			"    section: getvisa-core\n"+
			"    owners: [alice]\n"+
			"    status: active\n"+
			"    joined: \"2026-01-01\"\n",
	)
	writeSpaceMirrorFile(t, mirrorDir, "CODEOWNERS", "/space.yaml @acme/space-owners\n")
}

// TestSpaceUpdateDryRunWritesNothing is AC-950.1: --dry-run reports the
// exact diff and writes/pushes nothing — the funnel fake is never called
// and no file is written under mirrorDir.
func TestSpaceUpdateDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"update", "--dry-run"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 0 {
		t.Errorf("funnel.calls = %d, want 0 for --dry-run", len(funnel.calls))
	}

	for _, want := range []string{"add .github/workflows/a2a-validate.yml", "add .github/dependabot.yml", "add BRANCH-PROTECTION.md", "field-update space.yaml (min_binary_version 0.1.0 -> 0.4.0, the template's floor)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout does not report %q; stdout=%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "add CODEOWNERS") {
		t.Errorf("CODEOWNERS is present in the space and must never be scheduled to write; stdout=%s", out.String())
	}

	// Directive text asserted verbatim (AC-952.1): a2a prints the exact
	// admin-only commands, never runs them.
	wantDirectives := []string{
		// Phrased as a CONDITION. a2a calls no admin API, so it cannot observe
		// which context protection requires and prints this every time; stating
		// "this PR cannot merge" as fact was therefore a claim beyond what the
		// code knows, and false for every already-migrated space — it told an
		// operator whose protection was already compound to go fix it, and
		// would repeat that on every future `space update`.
		"scope: space directive — prerequisite (spec 35 §T3: IF protection still requires the old flat context, this PR cannot merge until that is changed)",
		"  skip if: your required check is already \"a2a-validate / validate\" — a2a calls no admin API, so it cannot check this for you",
		// Must stay byte-identical to the runbook's own command.
		`  run: gh api -X PUT repos/acme/getvisa/branches/main/protection/required_status_checks -f 'checks[][context]=a2a-validate / validate'`,
		"scope: space directive — cleanup (optional: P33 removed the need for this secret)",
		"  run: gh secret delete A2A_BINARY_FETCH_TOKEN --repo acme/getvisa",
	}
	for _, want := range wantDirectives {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout does not contain the exact directive line %q; stdout=%s", want, out.String())
		}
	}

	// Nothing written under mirrorDir beyond the seed fixture.
	if _, err := os.Stat(filepath.Join(mirrorDir, ".github", "workflows", "a2a-validate.yml")); !os.IsNotExist(err) {
		t.Errorf("expected .github/workflows/a2a-validate.yml NOT to be written by --dry-run; stat err = %v", err)
	}
	spaceYAML, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		t.Fatalf("read seeded space.yaml: %v", err)
	}
	if !strings.Contains(string(spaceYAML), "min_binary_version: 0.1.0") {
		t.Errorf("--dry-run must not have rewritten the on-disk space.yaml; got:\n%s", spaceYAML)
	}
}

// TestSpaceUpdateSubmitsOncePreservingSpaceOwnedContent is AC-950.2 plus
// the field-disposition acceptance criteria: exactly one SubmitRequest,
// AllowSpaceInfrastructure true, CODEOWNERS never in Files, space.yaml's
// diff touches ONLY min_binary_version (participants survive verbatim),
// and a template file absent from the space (a synthetic new one, for
// AC-951.1) appears in the request.
func TestSpaceUpdateSubmitsOncePreservingSpaceOwnedContent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)

	tmpl := spaceUpdateTemplateFS()
	// AC-951.1: a brand-new template file (not in the disposition table,
	// not yet in the space) must propagate with NO code change — within the
	// space-infrastructure paths the funnel can carry, which is what a
	// STRUCTURAL template change actually is (a new workflow, a new
	// .github/ config). See the root-level case below for the bound.
	tmpl[".github/workflows/a2a-nightly.yml"] = &fstest.MapFile{Data: []byte("this file is new in the template\n")}
	// A new template file OUTSIDE that set cannot be carried by the funnel
	// (it would be refused as outside the authoring system's section), so it
	// must be REPORTED, never silently dropped and never planned.
	tmpl["NOTICE.md"] = &fstest.MapFile{Data: []byte("root-level, not infrastructure\n")}

	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(tmpl, mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"update"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel.calls = %d, want exactly 1", len(funnel.calls))
	}
	req := funnel.calls[0]

	if !req.AllowSpaceInfrastructure {
		t.Error("SubmitRequest.AllowSpaceInfrastructure = false, want true")
	}
	if req.Verb != "space-update" {
		t.Errorf("req.Verb = %q, want \"space-update\"", req.Verb)
	}
	if req.MinBinaryVersion != "0.1.0" {
		t.Errorf("req.MinBinaryVersion = %q, want the space's own pin \"0.1.0\"", req.MinBinaryVersion)
	}

	byPath := map[string]space.FileWrite{}
	for _, f := range req.Files {
		if _, dup := byPath[f.Path]; dup {
			t.Fatalf("SubmitRequest.Files carries %q twice", f.Path)
		}
		byPath[f.Path] = f
	}

	if _, ok := byPath["CODEOWNERS"]; ok {
		t.Error("CODEOWNERS is present in the space and must NEVER appear in the request's Files")
	}

	sy, ok := byPath["space.yaml"]
	if !ok {
		t.Fatal("space.yaml missing from request Files (min_binary_version must be raised to the template's floor)")
	}
	syText := string(sy.Content)
	// The TEMPLATE's floor (0.4.0), NOT the running binary's version
	// (9.9.9) — see spaceUpdateFloor for why the binary must never be the
	// source of a fleet-wide write floor.
	if !strings.Contains(syText, "min_binary_version: 0.4.0") {
		t.Errorf("space.yaml write does not carry the template's floor 0.4.0:\n%s", syText)
	}
	if strings.Contains(syText, "min_binary_version: 9.9.9") {
		t.Errorf("space.yaml write raised the floor to the RUNNING BINARY's version — that would lock out every participant on an older build:\n%s", syText)
	}
	for _, want := range []string{"space: getvisa", "system: getvisa-core", "owners: [alice]"} {
		if !strings.Contains(syText, want) {
			t.Errorf("space.yaml write dropped space-owned content %q — must survive verbatim:\n%s", want, syText)
		}
	}

	for _, path := range []string{".github/workflows/a2a-validate.yml", ".github/dependabot.yml", "BRANCH-PROTECTION.md"} {
		if _, ok := byPath[path]; !ok {
			t.Errorf("managed file %q missing from request Files", path)
		}
	}

	nightly, ok := byPath[".github/workflows/a2a-nightly.yml"]
	if !ok {
		t.Fatal("AC-951.1: a new template file under .github/ did not propagate into the request")
	}
	if string(nightly.Content) != "this file is new in the template\n" {
		t.Errorf("new workflow content = %q, want the exact template content", nightly.Content)
	}
	if _, ok := byPath["NOTICE.md"]; ok {
		t.Error("NOTICE.md is not a space-infrastructure path — planning it would make the funnel refuse the ENTIRE write")
	}
	if !strings.Contains(out.String(), "not propagated") || !strings.Contains(out.String(), "NOTICE.md") {
		t.Errorf("the un-carryable template file was dropped silently; stdout=%s", out.String())
	}

	workflow := byPath[".github/workflows/a2a-validate.yml"]
	if !strings.Contains(string(workflow.Content), "a2a-validate-reusable.yml@v9.9.9") {
		t.Errorf("workflow write does not pin the running binary's version 9.9.9:\n%s", workflow.Content)
	}
}

// TestSpaceUpdateAlreadyCurrentIsNoop is AC-950.4: re-running against an
// already-current space is a no-op that says so, with the funnel never
// called.
func TestSpaceUpdateAlreadyCurrentIsNoop(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	tmpl := spaceUpdateTemplateFS()

	// Seed the mirror to EXACTLY the substituted-template content for
	// every managed/field-managed file, and the template's own untouched
	// bytes for the seeded CODEOWNERS — i.e. "already migrated".
	writeSpaceMirrorFile(t, mirrorDir, "space.yaml",
		"schema: manifest/v1\n"+
			"space: getvisa\n"+
			"min_binary_version: 0.4.0\n"+
			"participants: []\n",
	)
	codeowners, _ := fs.ReadFile(tmpl, "CODEOWNERS")
	writeSpaceMirrorFile(t, mirrorDir, "CODEOWNERS", string(codeowners))
	for _, p := range []string{".github/workflows/a2a-validate.yml", ".github/dependabot.yml", "BRANCH-PROTECTION.md", "README.md"} {
		data, err := fs.ReadFile(tmpl, p)
		if err != nil {
			t.Fatalf("read template fixture %s: %v", p, err)
		}
		writeSpaceMirrorFile(t, mirrorDir, p, string(data))
	}
	// The workflow file needs the RUNNING binary's version substituted in,
	// same as the write path would produce.
	writeSpaceMirrorFile(t, mirrorDir, ".github/workflows/a2a-validate.yml",
		"jobs:\n  a2a-validate:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-validate-reusable.yml@v9.9.9\n",
	)

	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(tmpl, mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), []string{"update"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 0 {
		t.Errorf("funnel.calls = %d, want 0 for an already-current space", len(funnel.calls))
	}
	if !strings.Contains(out.String(), "already current") {
		t.Errorf("stdout = %q, want it to say the space is already current", out.String())
	}
}

// TestSpaceUpdateFloorIsBinaryVersionIndependent pins the convergence
// property spaceUpdateFloor exists for: two participants running
// `space update` from DIFFERENT binary builds must produce the SAME
// space.yaml. If the floor were sourced from the running binary they would
// overwrite each other forever and AC-950.4's "re-running is a no-op" would
// hold only per binary version, which is not idempotence at all.
func TestSpaceUpdateFloorIsBinaryVersionIndependent(t *testing.T) {
	t.Parallel()

	floorFor := func(binaryVersion string) string {
		t.Helper()
		mirrorDir := t.TempDir()
		spaceUpdateSeedMirror(t, mirrorDir)
		funnel := &fakeSubmitFunnel{}
		cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, binaryVersion)

		io, out, errOut := newSpaceIO()
		if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
			t.Fatalf("Run(%s): code = %d; stdout=%s stderr=%s", binaryVersion, code, out.String(), errOut.String())
		}
		if len(funnel.calls) != 1 {
			t.Fatalf("funnel.calls = %d, want 1", len(funnel.calls))
		}
		for _, f := range funnel.calls[0].Files {
			if f.Path == "space.yaml" {
				return string(f.Content)
			}
		}
		t.Fatalf("space.yaml missing from the request for binary %s", binaryVersion)
		return ""
	}

	older, newer := floorFor("0.5.0"), floorFor("9.9.9")
	if older != newer {
		t.Errorf("space.yaml content depends on the RUNNING BINARY's version — two participants would fight forever.\nfrom 0.5.0:\n%s\nfrom 9.9.9:\n%s", older, newer)
	}
	if !strings.Contains(older, "min_binary_version: 0.4.0") {
		t.Errorf("floor is not the template's 0.4.0:\n%s", older)
	}
}

// TestSpaceUpdateNeverLowersTheFloor: a space pinned ABOVE the template's
// floor keeps its own. Converging downward would silently weaken the CC-085
// write guard — a drift-sync must never do that.
func TestSpaceUpdateNeverLowersTheFloor(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	// Re-pin the seeded space ABOVE the template's 0.4.0.
	writeSpaceMirrorFile(t, mirrorDir, "space.yaml",
		"schema: manifest/v1\n"+
			"space: getvisa\n"+
			"min_binary_version: 5.0.0\n"+
			"participants: []\n",
	)

	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("Run: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel.calls = %d, want 1 (the managed infra files still need adding)", len(funnel.calls))
	}
	for _, f := range funnel.calls[0].Files {
		if f.Path == "space.yaml" {
			t.Errorf("space.yaml was rewritten though the space's floor 5.0.0 already exceeds the template's 0.4.0:\n%s", f.Content)
		}
	}
	if !strings.Contains(out.String(), "above the template's") {
		t.Errorf("stdout does not report the above-template floor as advisory drift; stdout=%s", out.String())
	}
	// The funnel's CC-085 guard must be handed the floor IN FORCE.
	if got := funnel.calls[0].MinBinaryVersion; got != "5.0.0" {
		t.Errorf("req.MinBinaryVersion = %q, want the space's floor in force \"5.0.0\"", got)
	}
}

// TestSpaceUpdateRefusesUnflooredManifest: a mirror space.yaml with no
// min_binary_version line must be a loud error, not a silent no-op that
// reports success while leaving the space permanently un-floored.
func TestSpaceUpdateRefusesUnflooredManifest(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	writeSpaceMirrorFile(t, mirrorDir, "space.yaml",
		"schema: manifest/v1\nspace: getvisa\nparticipants: []\n")

	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 1 {
		t.Fatalf("Run: code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "min_binary_version") {
		t.Errorf("stderr = %q, want it to name the missing min_binary_version", errOut.String())
	}
	if len(funnel.calls) != 0 {
		t.Error("funnel must not be called when the manifest is unusable")
	}
}

// TestSpaceUpdateSeededFileAtAnAlternateLocation: the template ships
// CODEOWNERS at the repo root, but GitHub also resolves it from .github/ and
// docs/ — and the live getvisa space keeps its REAL owners in
// .github/CODEOWNERS. Adding a root copy full of @REPLACE_WITH_ORG
// placeholders next to it is inert today (GitHub prefers .github/) but it is
// junk in the repo, an error in the CODEOWNERS UI, and a gates-nothing
// takeover the day the real file moves. Found by a dry-run against the live
// space; every fixture had CODEOWNERS at the template's own path.
func TestSpaceUpdateSeededFileAtAnAlternateLocation(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	// Move the space's CODEOWNERS to where GitHub looks FIRST.
	if err := os.Remove(filepath.Join(mirrorDir, "CODEOWNERS")); err != nil {
		t.Fatalf("remove root CODEOWNERS: %v", err)
	}
	writeSpaceMirrorFile(t, mirrorDir, ".github/CODEOWNERS", "/space.yaml @acme/real-owners\n")

	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("Run: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel.calls = %d, want 1", len(funnel.calls))
	}
	for _, f := range funnel.calls[0].Files {
		if f.Path == "CODEOWNERS" {
			t.Errorf("planned a root CODEOWNERS though the space keeps a real one at .github/CODEOWNERS:\n%s", f.Content)
		}
	}
	if !strings.Contains(out.String(), ".github/CODEOWNERS") {
		t.Errorf("the alternate location was not reported as advisory drift; stdout=%s", out.String())
	}
}

// --- `a2a-notify.yml` disposition (space-notify-2026-08 P5, spec 05
// "Template and adoption") ------------------------------------------------

// spaceUpdateNotifyTemplateFS is spaceUpdateTemplateFS() plus the
// notification caller, added locally (never by mutating the shared helper —
// TestSpaceUpdateSubmitsOncePreservingSpaceOwnedContent already establishes
// this exact pattern for its own synthetic `a2a-nightly.yml`) so these two
// tests cannot perturb every other test sharing spaceUpdateTemplateFS().
func spaceUpdateNotifyTemplateFS(pin string) fstest.MapFS {
	tmpl := spaceUpdateTemplateFS()
	tmpl[".github/workflows/a2a-notify.yml"] = &fstest.MapFile{Data: []byte(
		"jobs:\n  a2a-notify:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-notify-reusable.yml@" + pin + "\n",
	)}
	return tmpl
}

// TestSpaceUpdateAddsNotifyCallerWhenAbsent is spec 05 AC7: `a2a space
// update` adds the notification caller to a space that lacks it, pinned to
// the running binary's own version — the same e2e shape AC-951.1's synthetic
// `a2a-nightly.yml` case already proves for an UNLISTED path, run here
// against the disposition-table row this phase added.
func TestSpaceUpdateAddsNotifyCallerWhenAbsent(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir) // no a2a-notify.yml in the mirror at all

	tmpl := spaceUpdateNotifyTemplateFS("v0.23.0")
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(tmpl, mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel.calls = %d, want 1", len(funnel.calls))
	}
	var notify *space.FileWrite
	for i, f := range funnel.calls[0].Files {
		if f.Path == ".github/workflows/a2a-notify.yml" {
			notify = &funnel.calls[0].Files[i]
		}
	}
	if notify == nil {
		t.Fatalf("a2a-notify.yml missing from the request's Files — AC7 (add when absent) failed")
	}
	if !strings.Contains(string(notify.Content), "a2a-notify-reusable.yml@v9.9.9") {
		t.Errorf("notify caller write does not pin the running binary's version 9.9.9:\n%s", notify.Content)
	}
	if !strings.Contains(out.String(), "add .github/workflows/a2a-notify.yml") {
		t.Errorf("stdout does not report the add; stdout=%s", out.String())
	}
}

// TestSpaceUpdateNotifyCallerPropagatesTemplateChanges is the disposition-
// decision's own proof (spec 05's "answer this before writing code"):
// `.github/workflows/a2a-notify.yml` must stay spaceDispositionManaged, NOT
// rely on the "unlisted path" default, because that default's OTHER half —
// spaceComputeUpdatePlanFor's own doc comment, ":702-717" — is "once
// present, an unlisted path is presumed space-owned and this command never
// touches it again". Dependabot bumps an ALREADY-ADOPTED caller's `uses:`
// TAG per space, but nothing rewrites the file's SHAPE if it drifts from the
// template any other way; only `spaceDispositionManaged` does. This test
// seeds the mirror with the caller ALREADY present (simulating a space that
// adopted it earlier) and a template that has since changed — content that
// is NOT just the version pin, so a Dependabot-only story could not explain
// a green run here.
func TestSpaceUpdateNotifyCallerPropagatesTemplateChanges(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	// The space already adopted the caller at an EARLIER template shape.
	writeSpaceMirrorFile(t, mirrorDir, ".github/workflows/a2a-notify.yml",
		"jobs:\n  a2a-notify:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-notify-reusable.yml@v0.20.0\n",
	)

	// The template's CURRENT shape: not just a version bump — an added
	// `with:` line, so a green result here cannot be explained by
	// Dependabot's own tag-only bump.
	tmpl := spaceUpdateTemplateFS()
	tmpl[".github/workflows/a2a-notify.yml"] = &fstest.MapFile{Data: []byte(
		"jobs:\n  a2a-notify:\n    uses: ydnikolaev/a2ahub/.github/workflows/a2a-notify-reusable.yml@v0.23.0\n    with:\n      limit: 5\n",
	)}

	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(tmpl, mirrorDir, funnel, "9.9.9")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel.calls = %d, want 1 — without spaceDispositionManaged the drifted caller is left "+
			"untouched forever (the 'unlisted, present' branch), and this update would be a no-op; "+
			"stdout=%s", len(funnel.calls), out.String())
	}
	var notify *space.FileWrite
	for i, f := range funnel.calls[0].Files {
		if f.Path == ".github/workflows/a2a-notify.yml" {
			notify = &funnel.calls[0].Files[i]
		}
	}
	if notify == nil {
		t.Fatalf("a2a-notify.yml missing from the request's Files — the drifted caller was never resynced")
	}
	if !strings.Contains(string(notify.Content), "limit: 5") {
		t.Errorf("notify caller write did not pick up the template's new `with: limit` line:\n%s", notify.Content)
	}
	if !strings.Contains(string(notify.Content), "a2a-notify-reusable.yml@v9.9.9") {
		t.Errorf("notify caller write does not pin the running binary's version 9.9.9:\n%s", notify.Content)
	}
	if !strings.Contains(out.String(), "update .github/workflows/a2a-notify.yml") {
		t.Errorf("stdout does not report the update; stdout=%s", out.String())
	}
}

// TestSpaceUpdateRefusesWithoutWorkflowScopeNamesBothManagedFiles is AC 7b:
// with a SECOND managed workflow, the scope refusal must name which file(s)
// it was blocked on rather than a bare ".github/workflows/" that no longer
// identifies one specific file an operator may not have asked for.
func TestSpaceUpdateRefusesWithoutWorkflowScopeNamesBothManagedFiles(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateNotifyTemplateFS("v0.23.0"), mirrorDir, funnel, "9.9.9")
	cmd.Scopes = &fakeScopeChecker{scopes: []string{"repo", "read:org"}, reported: true}

	io, _, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 1 {
		t.Fatalf("Run: code = %d, want 1; stderr=%s", code, errOut.String())
	}
	for _, want := range []string{".github/workflows/a2a-validate.yml", ".github/workflows/a2a-notify.yml"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr does not name the blocked file %q; stderr=%s", want, errOut.String())
		}
	}
}

// fakeScopeChecker drives the workflow-scope pre-flight.
type fakeScopeChecker struct {
	scopes   []string
	reported bool
	err      error
	calls    int
}

func (f *fakeScopeChecker) TokenScopes(context.Context, host.Credential) ([]string, bool, error) {
	f.calls++
	return f.scopes, f.reported, f.err
}

// TestSpaceUpdateRefusesWithoutWorkflowScope: `space update` rewrites
// .github/workflows/, which GitHub refuses from a token lacking the
// `workflow` scope. Before this check the command printed the whole plan and
// both directives and THEN died on a raw git push rejection — it read as "it
// worked, then broke". Observed performing the live getvisa migration.
func TestSpaceUpdateRefusesWithoutWorkflowScope(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	cmd.Scopes = &fakeScopeChecker{scopes: []string{"repo", "read:org"}, reported: true}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 1 {
		t.Fatalf("Run: code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 0 {
		t.Error("funnel must not be called when the credential provably cannot carry the write")
	}
	for _, want := range []string{"workflow", "gh auth refresh", "repo, read:org"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr does not name %q — the remedy must be actionable; stderr=%s", want, errOut.String())
		}
	}
	// The refusal must come BEFORE the plan, not after it.
	if strings.Contains(out.String(), "scaffolding diff") {
		t.Errorf("printed a plan it cannot execute:\n%s", out.String())
	}
}

// A fine-grained PAT or App token does not advertise scopes at all. Treating
// that silence as refusal would block exactly the most narrowly-scoped
// credentials, so it must proceed. However, the user must be warned that the
// write may still fail at GitHub's end if the token lacks Workflows permission.
func TestSpaceUpdateProceedsWhenScopesAreNotReported(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	probe := &fakeScopeChecker{reported: false}
	cmd.Scopes = probe

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if probe.calls == 0 {
		t.Error("the probe was never consulted")
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel.calls = %d, want 1 — an unreported scope set must not block the write", len(funnel.calls))
	}

	// The key new behavior: the can't-verify note must surface to stdout.
	if !strings.Contains(out.String(), "scope could not be verified") {
		t.Errorf("stdout does not surface the can't-verify note; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "does not advertise scopes") {
		t.Errorf("stdout does not explain why (fine-grained PAT/App); stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "may still be refused") {
		t.Errorf("stdout does not warn that the write may still fail; stdout=%s", out.String())
	}
}

// --dry-run still shows the plan (that is what it is for) but must say the
// real run would be refused, so the scope is learned BEFORE it matters.
func TestSpaceUpdateDryRunWarnsAboutMissingScope(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	cmd.Scopes = &fakeScopeChecker{scopes: []string{"repo"}, reported: true}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--dry-run"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "scaffolding diff") {
		t.Errorf("--dry-run stopped showing the plan; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "WOULD BE REFUSED") {
		t.Errorf("--dry-run did not warn that the real run would be refused; stdout=%s", out.String())
	}
	if len(funnel.calls) != 0 {
		t.Error("--dry-run must never call the funnel")
	}
}

// Dry-run with unreported scopes must show the note but NOT say "WOULD BE REFUSED"
// (because it may well succeed — the credential might have the permission).
func TestSpaceUpdateDryRunWithUnreportedScopes(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	cmd.Scopes = &fakeScopeChecker{reported: false}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--dry-run"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "scaffolding diff") {
		t.Errorf("--dry-run stopped showing the plan; stdout=%s", out.String())
	}
	if !strings.Contains(out.String(), "scope could not be verified") {
		t.Errorf("--dry-run did not surface the can't-verify note; stdout=%s", out.String())
	}
	if strings.Contains(out.String(), "WOULD BE REFUSED") {
		t.Errorf("--dry-run should not say WOULD BE REFUSED for a can't-verify note (it may succeed); stdout=%s", out.String())
	}
	if len(funnel.calls) != 0 {
		t.Error("--dry-run must never call the funnel")
	}
}

// --- `--wait`, all four directions ------------------------------------------
//
// `space update` opens a PR and writes nothing to the space; even after that
// PR merges, the local mirror is unchanged until something fetches it, and
// `a2a version`, `a2a doctor` and the dashboard all read the mirror. On
// 2026-08-23 that gap told an operator their merged update had not happened.
// `--wait` closes it, and these hold every edge that closing it introduced.

func TestSpaceUpdateWaitRefusesWithDryRun(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, &fakeSubmitFunnel{}, "9.9.9")
	cmd.AwaitPR = func(context.Context, string) (space.AwaitResult, error) {
		t.Fatal("--wait must not poll when the run opens no PR")
		return space.AwaitResult{}, nil
	}

	io, _, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--wait", "--dry-run"}, io); code != 2 {
		t.Fatalf("Run: code = %d, want 2 for contradictory flags", code)
	}
	if !strings.Contains(errOut.String(), "contradictory") {
		t.Errorf("stderr = %q, want it to name the contradiction", errOut.String())
	}
}

// A flag that is accepted and ignored is worse than one that does not exist.
func TestSpaceUpdateWaitRefusesWhenUnwired(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	cmd.AwaitPR = nil

	io, _, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--wait"}, io); code != 1 {
		t.Fatalf("Run: code = %d, want 1 when --wait is unwired", code)
	}
	if len(funnel.calls) != 0 {
		t.Errorf("funnel was called %d time(s); an unwired --wait must refuse BEFORE writing", len(funnel.calls))
	}
	if !strings.Contains(errOut.String(), "a2a sync") {
		t.Errorf("stderr = %q, want the remedy that closes the loop without --wait", errOut.String())
	}
}

func TestSpaceUpdateWaitRefreshesTheMirrorOnMerge(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	var awaitedBranch string
	cmd.AwaitPR = func(_ context.Context, branch string) (space.AwaitResult, error) {
		awaitedBranch = branch
		return space.AwaitResult{PRURL: "https://example.invalid/pr/7"}, nil
	}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--wait"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if awaitedBranch == "" {
		t.Fatal("--wait polled no branch; it must observe the branch the write pushed")
	}
	if !strings.Contains(out.String(), "mirror is refreshed") {
		t.Errorf("stdout = %q, want it to say the mirror was refreshed — that is the whole point", out.String())
	}
	// The no-wait epilogue tells you to run `a2a sync`; after --wait that has
	// already happened, and repeating it would be the same wrong instruction
	// this flag exists to remove.
	if strings.Contains(out.String(), "after it merges, run") {
		t.Errorf("stdout = %q, want no post-merge instruction after --wait already did it", out.String())
	}
}

// A refusal from the poller is about the PR, not about the update: the branch
// is pushed and the PR is open either way, so the remedy must not be "run this
// command again".
func TestSpaceUpdateWaitReportsAPRThatNeverMerged(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, &fakeSubmitFunnel{}, "9.9.9")
	cmd.AwaitPR = func(context.Context, string) (space.AwaitResult, error) {
		return space.AwaitResult{}, space.ErrAwaitClosed
	}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--wait"}, io); code != 1 {
		t.Fatalf("Run: code = %d, want 1 when the PR never merges; stdout=%s", code, out.String())
	}
	if !strings.Contains(errOut.String(), "still open and unchanged") {
		t.Errorf("stderr = %q, want it to say the PR is untouched", errOut.String())
	}
	if !strings.Contains(errOut.String(), "a2a sync") {
		t.Errorf("stderr = %q, want the remedy after the PR is resolved", errOut.String())
	}
}

// Without --wait the command must NAME the step that closes the loop. Its last
// line otherwise reads as completion, and it is not one.
func TestSpaceUpdateNamesTheStepThatClosesTheLoop(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, &fakeSubmitFunnel{}, "9.9.9")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stderr=%s", code, errOut.String())
	}
	for _, want := range []string{"changed nothing in the space yet", "a2a sync", "--wait"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout does not mention %q:\n%s", want, out.String())
		}
	}
}
