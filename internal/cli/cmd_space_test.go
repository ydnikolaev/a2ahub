package cli_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	spacetemplate "github.com/ydnikolaev/a2ahub/space-template"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
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
	if len(subs) != 4 || subs[0].Name != "init" || subs[1].Name != "update" || subs[2].Name != "join" || subs[3].Name != "set" {
		t.Fatalf("SpaceSubcommands() = %+v, want exactly four rows named \"init\", \"update\", \"join\", \"set\"", subs)
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
		// The baseline is a GitHub space: its host declares that it reviews
		// gated writes in a pull request, so a manifest change needs no
		// --approve. A test of a space that lives on this machine overrides it.
		Caps: host.Capabilities{ReviewsWrites: true},
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

// --- `a2a space init --local` (local-space-2026-10 spec 03) --------------

// localSpaceOpener is the production opener's shape for a path: a marked
// repository opens as a local host, an unmarked one as a GitHub-kind host. The
// land check is a stub because these tests never land.
func localSpaceOpener() host.Opener {
	return host.NewOpener(host.OpenConfig{
		LandCheck: func(context.Context, host.LandCandidate) (host.LandVerdict, error) {
			return host.LandVerdict{OK: true}, nil
		},
	})
}

// newLocalSpaceCommand wires `space init --local` the way cmd/a2a does: the
// real embedded template, the real manifest engine, an injected data root and
// the production-shaped opener.
func newLocalSpaceCommand(t *testing.T, dataRoot string) *cli.SpaceCommand {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	cmd := cli.NewSpaceCommand(spacetemplate.Files, "9.9.9")
	cmd.DataRoot = dataRoot
	cmd.Engine = validate.New(corpus)
	cmd.Open = localSpaceOpener()
	return cmd
}

func spaceGit(t *testing.T, gitDir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", gitfixture.Args(append([]string{"--git-dir=" + gitDir}, args...)...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// canonicalTempDir is t.TempDir() with symlinks resolved (macOS /var is a
// symlink to /private/var), which is the spelling the command prints.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSpaceInitLocalCreatesAGuardedBareRepoWithOnlyTheLocalSubset(t *testing.T) {
	t.Parallel()
	root := canonicalTempDir(t)
	target := filepath.Join(root, "acme.git")
	cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--local", "--dir", target}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	if want := "space init: created local space acme at " + target + "\n"; !strings.HasPrefix(out.String(), want) {
		t.Errorf("stdout = %q, want it to start with %q", out.String(), want)
	}
	// The next steps are named in order: connect from each repo, THEN join.
	connectAt := strings.Index(out.String(), "a2a connect "+target)
	joinAt := strings.Index(out.String(), "a2a space join")
	if connectAt < 0 || joinAt < 0 || connectAt > joinAt {
		t.Errorf("stdout = %q, want `a2a connect %s` before `a2a space join`", out.String(), target)
	}

	tree := spaceGit(t, target, "ls-tree", "-r", "--name-only", "main")
	if want := "decisions/.gitkeep\nspace.yaml\nvendored/.gitkeep"; tree != want {
		t.Errorf("main's tree = %q, want exactly the local subset %q", tree, want)
	}
	if got := spaceGit(t, target, "rev-list", "--count", "main"); got != "1" {
		t.Errorf("main has %s commits, want one root commit", got)
	}
	if got := spaceGit(t, target, "symbolic-ref", "HEAD"); got != "refs/heads/main" {
		t.Errorf("HEAD = %q, want refs/heads/main", got)
	}
	if got := spaceGit(t, target, "show", "main:space.yaml"); !strings.Contains(got, "space: acme") ||
		!strings.Contains(got, "min_binary_version: 9.9.9") || strings.Contains(got, "replace-with-space-id") {
		t.Errorf("seeded space.yaml was not rendered for this space and binary:\n%s", got)
	}

	// The guard is armed: the opener now sees a local host, and the inspector
	// reports the hook present, executable and current.
	h, _, err := localSpaceOpener()(target)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Capabilities().OnThisMachine {
		t.Fatal("the new space did not open as a local host: the marker was not written")
	}
	insp, ok := h.(host.LocalSpaceInspector)
	if !ok {
		t.Fatal("a local host does not implement host.LocalSpaceInspector")
	}
	got, err := insp.InspectLocalSpace(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.HookPresent || !got.HookExecutable || !got.HookCurrent || len(got.MainNonLandCommits) != 0 {
		t.Errorf("inspection = %+v, want a present, executable, current hook and a clean history", got)
	}
}

func TestSpaceInitLocalDefaultTargetIsUnderTheInjectedDataRoot(t *testing.T) {
	t.Parallel()
	root := canonicalTempDir(t)
	dataRoot := filepath.Join(root, "not", "yet", "created")
	cmd := newLocalSpaceCommand(t, dataRoot)

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--local"}, io); code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	want := filepath.Join(dataRoot, "spaces", "acme.git")
	if !filepath.IsAbs(want) || !strings.HasPrefix(out.String(), "space init: created local space acme at "+want+"\n") {
		t.Errorf("stdout = %q, want the absolute default target %q", out.String(), want)
	}
	if got := spaceGit(t, want, "rev-list", "--count", "main"); got != "1" {
		t.Errorf("the default target is not the new space (main has %s commits)", got)
	}
}

func TestSpaceInitLocalCanonicalizesTheTarget(t *testing.T) {
	t.Parallel()
	root := canonicalTempDir(t)
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))

	io, out, errOut := newSpaceIO()
	// A target under a symlinked parent, spelled with a trailing slash.
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--local", "--dir", filepath.Join(link, "sub", "acme.git") + "/"}, io); code != 0 {
		t.Fatalf("Run: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	want := filepath.Join(real, "sub", "acme.git")
	if !strings.Contains(out.String(), " at "+want+"\n") {
		t.Errorf("stdout = %q, want the symlink-resolved path %q", out.String(), want)
	}
	if got := spaceGit(t, want, "rev-list", "--count", "main"); got != "1" {
		t.Errorf("the space was not created at the resolved path (main has %s commits)", got)
	}
}

// TestSpaceInitLocalRelativeDirBecomesAbsolute is deliberately NOT parallel and
// not a subtest of a parallel test: t.Chdir changes the process working
// directory, which a test running beside it would observe.
func TestSpaceInitLocalRelativeDirBecomesAbsolute(t *testing.T) {
	root := canonicalTempDir(t)
	cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))
	t.Chdir(root)
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "rel", "--local", "--dir", "rel.git"}, io); code != 0 {
		t.Fatalf("Run: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if want := "created local space rel at " + filepath.Join(root, "rel.git") + "\n"; !strings.Contains(out.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", out.String(), want)
	}
}

// requireNextStep asserts a rendered NewRefusal ("<attempted>: <found> —
// <nextStep>") carries want as its third part: present, and after the found
// text, so it cannot be satisfied by the attempted/found parts echoing it.
func requireNextStep(t *testing.T, rendered, found, want string) {
	t.Helper()
	f, n := strings.Index(rendered, found), strings.LastIndex(rendered, want)
	if f < 0 || n < 0 || n < f {
		t.Errorf("stderr = %q, want its next step %q after the found part %q", rendered, want, found)
	}
	if !strings.Contains(rendered, " — ") {
		t.Errorf("stderr = %q is not rendered as a three-part refusal (no \" — \" before the next step)", rendered)
	}
}

func TestSpaceInitLocalRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    func(target string) []string
		prep    func(t *testing.T, target string)
		mutate  func(cmd *cli.SpaceCommand)
		wantErr string
		// wantNext, when set, is the instruction the refusal's third part
		// must carry (the migrated NewRefusal sites): a command the caller
		// can run, after the refusal's "— " separator.
		wantNext string
	}{
		{
			name: "the template's own sentinel id",
			args: func(target string) []string {
				return []string{"init", "replace-with-space-id", "--local", "--dir", target}
			},
			wantErr: "sentinel",
		},
		{
			name:     "a dev build",
			args:     func(target string) []string { return []string{"init", "acme", "--local", "--dir", target} },
			mutate:   func(cmd *cli.SpaceCommand) { cmd.Version = "dev" },
			wantErr:  `a2a binary is version "dev"`,
			wantNext: "install a tagged release of a2a",
		},
		{
			name: "a non-empty foreign target",
			args: func(target string) []string { return []string{"init", "acme", "--local", "--dir", target} },
			prep: func(t *testing.T, target string) {
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "mine.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantErr: "already exists and is not empty",
		},
		{
			name:    "an id the manifest schema rejects",
			args:    func(target string) []string { return []string{"init", "Not_A_Valid_Id", "--local", "--dir", target} },
			wantErr: "does not validate",
		},
		{
			name:    "no manifest engine wired",
			args:    func(target string) []string { return []string{"init", "acme", "--local", "--dir", target} },
			mutate:  func(cmd *cli.SpaceCommand) { cmd.Engine = nil },
			wantErr: "not wired",
		},
		{
			name:    "no data root and no --dir",
			args:    func(string) []string { return []string{"init", "acme", "--local"} },
			mutate:  func(cmd *cli.SpaceCommand) { cmd.DataRoot = "" },
			wantErr: "not wired",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := canonicalTempDir(t)
			target := filepath.Join(root, "acme.git")
			if tc.prep != nil {
				tc.prep(t, target)
			}
			cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))
			if tc.mutate != nil {
				tc.mutate(cmd)
			}
			io, out, errOut := newSpaceIO()
			if code := cmd.Run(context.Background(), tc.args(target), io); code != 1 {
				t.Fatalf("Run: code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}
			if tc.wantNext != "" {
				requireNextStep(t, errOut.String(), tc.wantErr, tc.wantNext)
			}
			if _, err := os.Stat(filepath.Join(target, "HEAD")); !os.IsNotExist(err) {
				t.Errorf("a refused init still created a repository at %s (stat err %v)", target, err)
			}
			if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
				t.Errorf("a refused init still created the data root (stat err %v)", err)
			}
		})
	}
}

// TestSpaceInitLocalUninspectableTargetNamesTheNextStep: a --dir that is a
// file cannot be listed, so the refusal says what could not be inspected and
// the command that re-runs it, and nothing is created.
func TestSpaceInitLocalUninspectableTargetNamesTheNextStep(t *testing.T) {
	t.Parallel()
	root := canonicalTempDir(t)
	target := filepath.Join(root, "acme.git")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--local", "--dir", target}, io); code != 1 {
		t.Fatalf("code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	found := "cannot inspect " + target
	if !strings.Contains(errOut.String(), found) {
		t.Errorf("stderr = %q, want its found part to carry %q", errOut.String(), found)
	}
	requireNextStep(t, errOut.String(), found, "re-run `a2a space init acme --local --dir <path>`")
	if _, err := os.Stat(filepath.Join(root, "data")); !os.IsNotExist(err) {
		t.Errorf("a refused init still created the data root (stat err %v)", err)
	}
}

// TestSpaceInitLocalRerun is the init-rerun case table (AC18): the verb is
// re-runnable because a crash between the root commit and the guard leaves an
// unguarded main that nothing else can repair.
func TestSpaceInitLocalRerun(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	runInit := func(t *testing.T, id, target string) (int, string, string) {
		t.Helper()
		cmd := newLocalSpaceCommand(t, filepath.Join(filepath.Dir(target), "data"))
		io, out, errOut := newSpaceIO()
		code := cmd.Run(ctx, []string{"init", id, "--local", "--dir", target}, io)
		return code, out.String(), errOut.String()
	}
	inspect := func(t *testing.T, target string) host.LocalSpaceInspection {
		t.Helper()
		h, _, err := localSpaceOpener()(target)
		if err != nil {
			t.Fatal(err)
		}
		insp, ok := h.(host.LocalSpaceInspector)
		if !ok {
			t.Fatalf("%T is not a local space inspector: the repository is not marked", h)
		}
		got, err := insp.InspectLocalSpace(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	t.Run("a completed space is left as it is and its edited hook is restored", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(canonicalTempDir(t), "acme.git")
		if code, out, errOut := runInit(t, "acme", target); code != 0 {
			t.Fatalf("first run: code = %d; %s%s", code, out, errOut)
		}
		before := spaceGit(t, target, "rev-parse", "main")
		hook := filepath.Join(target, "hooks", "pre-receive")
		if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if inspect(t, target).HookCurrent {
			t.Fatal("setup: the edited hook still reads as current")
		}

		code, out, errOut := runInit(t, "acme", target)
		if code != 0 {
			t.Fatalf("re-run: code = %d; stdout=%s stderr=%s", code, out, errOut)
		}
		if want := "space init: local space acme already created at " + target + "; guard installed\n"; out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
		if after := spaceGit(t, target, "rev-parse", "main"); after != before {
			t.Errorf("main moved %s -> %s: a re-run must add no commit", before, after)
		}
		if got := inspect(t, target); !got.HookPresent || !got.HookExecutable || !got.HookCurrent {
			t.Errorf("inspection after re-run = %+v, want the hook restored", got)
		}
	})

	t.Run("a repository left after the root commit gets its marker and hook", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(canonicalTempDir(t), "acme.git")
		if _, err := space.InitLocalSpaceRepo(ctx, target, []space.FileWrite{
			{Path: "space.yaml", Content: []byte("schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\nparticipants: []\n")},
		}); err != nil {
			t.Fatal(err)
		}
		if h, _, err := localSpaceOpener()(target); err != nil || h.Capabilities().OnThisMachine {
			t.Fatalf("setup: the half-created repository already reads as local (err %v)", err)
		}
		root := spaceGit(t, target, "rev-parse", "main")

		code, out, errOut := runInit(t, "acme", target)
		if code != 0 {
			t.Fatalf("re-run: code = %d; stdout=%s stderr=%s", code, out, errOut)
		}
		if !strings.Contains(out, "already created at "+target+"; guard installed") {
			t.Errorf("stdout = %q, want the already-created line", out)
		}
		if got := spaceGit(t, target, "rev-parse", "main"); got != root {
			t.Errorf("main moved: a re-run must add no commit")
		}
		if got := inspect(t, target); !got.HookCurrent {
			t.Errorf("inspection = %+v, want the hook installed", got)
		}
	})

	t.Run("a target whose space.yaml declares another id is refused", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(canonicalTempDir(t), "acme.git")
		if code, out, errOut := runInit(t, "acme", target); code != 0 {
			t.Fatalf("first run: code = %d; %s%s", code, out, errOut)
		}
		code, _, errOut := runInit(t, "other", target)
		if code != 1 || !strings.Contains(errOut, "already exists and is not empty") {
			t.Fatalf("code = %d, stderr = %q; want 1 and the non-empty refusal", code, errOut)
		}
	})

	t.Run("an unmarked repository with history beyond the root is refused", func(t *testing.T) {
		t.Parallel()
		target := filepath.Join(canonicalTempDir(t), "acme.git")
		root, err := space.InitLocalSpaceRepo(ctx, target, []space.FileWrite{
			{Path: "space.yaml", Content: []byte("schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\nparticipants: []\n")},
		})
		if err != nil {
			t.Fatal(err)
		}
		tree := spaceGit(t, target, "rev-parse", root+"^{tree}")
		child := exec.Command("git", gitfixture.Args("--git-dir="+target, "commit-tree", tree, "-p", root, "-m", "x")...)
		child.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		sha, err := child.Output()
		if err != nil {
			t.Fatal(err)
		}
		spaceGit(t, target, "update-ref", "refs/heads/main", strings.TrimSpace(string(sha)))

		code, _, errOut := runInit(t, "acme", target)
		if code != 1 || !strings.Contains(errOut, "already exists and is not empty") {
			t.Fatalf("code = %d, stderr = %q; want a refusal: this directory is not provably the half-created space", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(target, "hooks", "pre-receive")); !os.IsNotExist(err) {
			t.Errorf("a refused re-run still installed a hook (stat err %v)", err)
		}
	})
}

// TestSpaceInitWithoutLocalIsUnchanged pins that the local branch did not leak
// into the default one (US-8): nine files and the GitHub steps.
func TestSpaceInitWithoutLocalIsUnchanged(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "out")
	cmd := newLocalSpaceCommand(t, t.TempDir())
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"init", "acme", "--dir", target}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	if !strings.HasPrefix(out.String(), "space init: wrote 9 files to "+target+"\n") ||
		!strings.Contains(out.String(), "remaining steps, IN THIS ORDER:") {
		t.Errorf("stdout = %q, want the GitHub scaffold output", out.String())
	}
	if _, err := os.Stat(filepath.Join(target, "HEAD")); !os.IsNotExist(err) {
		t.Errorf("the default form created a git repository (stat err %v)", err)
	}
}

// TestSpaceUpdateLocalPlanProposesNothingForALocalSpace is AC4: the local plan
// is derived from the same subset `init --local` seeds, so a freshly created
// local space is already current, while the GitHub plan over the same mirror
// would want the whole infrastructure tree.
func TestSpaceUpdateLocalPlanProposesNothingForALocalSpace(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := canonicalTempDir(t)
	bare := filepath.Join(root, "acme.git")
	cmd := newLocalSpaceCommand(t, filepath.Join(root, "data"))
	io, out, errOut := newSpaceIO()
	if code := cmd.Run(ctx, []string{"init", "acme", "--local", "--dir", bare}, io); code != 0 {
		t.Fatalf("init: code = %d; %s%s", code, out.String(), errOut.String())
	}
	mirror := filepath.Join(root, "mirror")
	if outp, err := exec.Command("git", gitfixture.Args("clone", "--quiet", bare, mirror)...).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, outp)
	}

	plan := func(t *testing.T, local bool) (int, string, *fakeSubmitFunnel) {
		t.Helper()
		funnel := &fakeSubmitFunnel{}
		upd := &cli.SpaceCommand{
			TemplateFiles: spacetemplate.Files, Version: "9.9.9", Funnel: funnel, MirrorDir: mirror,
			SpaceID: "acme", OwnSystem: "billing", HostCfg: spaceUpdateHostCfg(),
		}
		upd.Caps = host.Capabilities{OnThisMachine: local}
		if local {
			upd.Scopes = failingScopeChecker{t: t}
		}
		io, out, errOut := newSpaceIO()
		code := upd.Run(ctx, []string{"update", "--dry-run"}, io)
		return code, out.String() + errOut.String(), funnel
	}

	code, text, funnel := plan(t, true)
	if code != 0 || !strings.Contains(text, "already current with the embedded template — nothing to do") {
		t.Fatalf("local plan: code = %d, output = %s; want zero proposed writes", code, text)
	}
	if strings.Contains(text, "not propagated") || len(funnel.calls) != 0 {
		t.Errorf("local plan reported skipped template paths or reached the funnel: %s", text)
	}

	code, text, _ = plan(t, false)
	if code != 0 || strings.Contains(text, "already current") || !strings.Contains(text, "add CODEOWNERS") {
		t.Fatalf("GitHub plan over the same mirror: code = %d, output = %s; want it to propose the infrastructure tree", code, text)
	}
}

// failingScopeChecker fails the test if the token-scope probe is consulted: a
// space on this machine has no token and no workflow files.
type failingScopeChecker struct{ t *testing.T }

func (f failingScopeChecker) TokenScopes(context.Context, host.Credential) ([]string, bool, error) {
	f.t.Helper()
	f.t.Error("the token-scope probe ran for a space on this machine")
	return nil, false, nil
}

func TestSpaceUpdateOnALocalSpacePrintsNoGitHubDirectives(t *testing.T) {
	t.Parallel()
	mirror := t.TempDir()
	spaceUpdateSeedMirror(t, mirror)
	// The one local-subset path the funnel can carry is space.yaml, whose floor
	// the seed mirror holds below the template's, so a write is planned.
	tmpl := fstest.MapFS{
		"space.yaml":         spaceUpdateTemplateFS()["space.yaml"],
		"decisions/.gitkeep": &fstest.MapFile{Data: []byte{}},
		"README.md":          &fstest.MapFile{Data: []byte("# template\n")},
		"CODEOWNERS":         &fstest.MapFile{Data: []byte("x\n")},
	}
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(tmpl, mirror, funnel, "9.9.9")
	cmd.Caps = host.Capabilities{OnThisMachine: true}
	cmd.Scopes = failingScopeChecker{t: t}

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"update", "--approve"}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel calls = %d, want 1 (the mirror's floor is below the template's)", len(funnel.calls))
	}
	var paths []string
	for _, f := range funnel.calls[0].Files {
		paths = append(paths, f.Path)
	}
	if got := strings.Join(paths, ","); got != "space.yaml" {
		t.Errorf("planned paths = %s, want only the local subset's carryable file", got)
	}
	if strings.Contains(out.String(), "not propagated") {
		t.Errorf("a local update reported template paths as skipped: %s", out.String())
	}
	if strings.Contains(out.String(), "gh api") || strings.Contains(out.String(), "branch-protection") ||
		strings.Contains(funnel.calls[0].PRBody, "gh api") {
		t.Errorf("a local update printed GitHub-only directives:\n%s\nPR body: %s", out.String(), funnel.calls[0].PRBody)
	}
}

// --- `a2a space join` (local-space-2026-10 spec 03 §T1.2) ----------------

// joinTemplateManifest is the embedded template's space.yaml rendered for a
// space id, exactly as `space init --local` seeds it.
func joinTemplateManifest(t *testing.T, id string) string {
	t.Helper()
	raw, err := fs.ReadFile(spacetemplate.Files, "space.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(raw), "replace-with-space-id", id)
}

func joinCommand(t *testing.T, manifest string, funnel *fakeSubmitFunnel) *cli.SpaceCommand {
	t.Helper()
	mirror := t.TempDir()
	writeSpaceMirrorFile(t, mirror, "space.yaml", manifest)
	cmd := spaceUpdateFullyWiredCommand(fstest.MapFS{}, mirror, funnel, "9.9.9")
	cmd.SpaceID = "acme"
	cmd.OwnSystem = "billing"
	cmd.Caps = host.Capabilities{OwnerIdentitySelfAsserted: true, NeedsCredential: false, OnThisMachine: true, LandsSynchronously: true}
	cmd.Now = func() time.Time { return time.Date(2026, 10, 28, 23, 59, 0, 0, time.UTC) }
	return cmd
}

func TestSpaceJoinWritesOneRequestWithRowAndSection(t *testing.T) {
	t.Parallel()
	manifest := joinTemplateManifest(t, "acme")
	funnel := &fakeSubmitFunnel{}
	cmd := joinCommand(t, manifest, funnel)

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"join", "--approve"}, io); code != 0 {
		t.Fatalf("code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if want := "space join: billing joined acme (section billing/)\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if len(funnel.calls) != 1 {
		t.Fatalf("funnel calls = %d, want exactly one write", len(funnel.calls))
	}
	req := funnel.calls[0]
	if !req.AllowSpaceInfrastructure || req.System != "billing" || req.Verb != "space-join" {
		t.Errorf("request = %+v, want an AllowSpaceInfrastructure space-join write by billing", req)
	}
	var paths []string
	byPath := map[string]string{}
	for _, f := range req.Files {
		paths = append(paths, f.Path)
		byPath[f.Path] = string(f.Content)
	}
	want := []string{
		"space.yaml",
		"billing/provides/.gitkeep", "billing/requires/.gitkeep", "billing/exchanges/.gitkeep",
		"billing/events/.gitkeep", "billing/docs/.gitkeep", "billing/consumes.yaml",
	}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("file set = %v, want %v", paths, want)
	}
	if got, want := byPath["billing/consumes.yaml"], "schema: consumes/v1\nsystem: billing\ndependencies: []\n"; got != want {
		t.Errorf("consumes.yaml = %q, want %q", got, want)
	}

	// Manifest bytes: the original with the `[]` flow marker gone from the
	// header and exactly one row inserted, after the comment lines that
	// continue the header's comment and before the next key. Every other byte,
	// every comment included, is untouched.
	row := "  - {system: billing, org: local, section: billing, owners: [local:billing], status: active, joined: 2026-10-28}"
	got := byPath["space.yaml"]
	gotLines := strings.Split(got, "\n")
	origLines := strings.Split(manifest, "\n")
	if len(gotLines) != len(origLines)+1 {
		t.Fatalf("manifest has %d lines, want %d (original + the one row)", len(gotLines), len(origLines)+1)
	}
	rowAt := -1
	for i, l := range gotLines {
		if l == row {
			rowAt = i
		}
	}
	if rowAt < 0 {
		t.Fatalf("the participant row is not in the manifest:\n%s", got)
	}
	if !strings.HasPrefix(gotLines[rowAt+1], "notification_routes:") {
		t.Errorf("the row is followed by %q, want the next top-level key (the header's comment lines must stay above the row)", gotLines[rowAt+1])
	}
	rest := append(append([]string(nil), gotLines[:rowAt]...), gotLines[rowAt+1:]...)
	for i, l := range rest {
		if strings.HasPrefix(origLines[i], "participants:") {
			if strings.Contains(l, "[]") || !strings.HasPrefix(l, "participants:") || !strings.Contains(l, "# zero participants at template time") {
				t.Errorf("header line = %q, want the flow marker gone and the comment kept", l)
			}
			continue
		}
		if l != origLines[i] {
			t.Errorf("line %d changed:\n  was  %q\n  now  %q", i+1, origLines[i], l)
		}
	}
	parsed, err := space.ParseManifest([]byte(got))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Participants) != 1 {
		t.Fatalf("participants = %+v, want one", parsed.Participants)
	}
	p := parsed.Participants[0]
	if p.System != "billing" || p.Org != "local" || p.Section != "billing" || len(p.Owners) != 1 || p.Owners[0] != "local:billing" || p.Joined != "2026-10-28" {
		t.Errorf("participant = %+v, want the spec row", p)
	}
}

func TestSpaceJoinAppendsToAnExistingList(t *testing.T) {
	t.Parallel()
	manifest := "schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\nparticipants:\n" +
		"  - {system: orders, org: local, section: orders, owners: [local:orders], status: active, joined: 2026-10-01}\n" +
		"  # a note between items\n" +
		"  - system: shipping\n    org: local\n    section: shipping\n    owners: [local:shipping]\n    status: active\n    joined: 2026-10-02\n" +
		"vendored: []\n"
	funnel := &fakeSubmitFunnel{}
	cmd := joinCommand(t, manifest, funnel)

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"join", "--approve"}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	got := string(funnel.calls[0].Files[0].Content)
	if !strings.Contains(got, "  # a note between items\n") {
		t.Errorf("the comment between items was lost:\n%s", got)
	}
	if !strings.Contains(got, "joined: 2026-10-02\n  - {system: billing") || !strings.HasSuffix(got, "joined: 2026-10-28}\nvendored: []\n") {
		t.Errorf("the row was not appended after the last existing item:\n%s", got)
	}
	parsed, err := space.ParseManifest([]byte(got))
	if err != nil || len(parsed.Participants) != 3 {
		t.Fatalf("participants = %+v, err %v; want three", parsed.Participants, err)
	}
}

func TestSpaceJoinIsIdempotent(t *testing.T) {
	t.Parallel()
	manifest := "schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\nparticipants:\n" +
		"  - {system: billing, org: local, section: billing, owners: [local:billing], status: active, joined: 2026-10-01}\n"
	for _, asJSON := range []bool{false, true} {
		funnel := &fakeSubmitFunnel{}
		cmd := joinCommand(t, manifest, funnel)
		args := []string{"join"}
		if asJSON {
			args = append(args, "--json")
		}
		io, out, errOut := newSpaceIO()
		if code := cmd.Run(context.Background(), args, io); code != 0 {
			t.Fatalf("json=%v: code = %d; %s%s", asJSON, code, out.String(), errOut.String())
		}
		if len(funnel.calls) != 0 {
			t.Errorf("json=%v: an already-active participant reached the funnel %d time(s)", asJSON, len(funnel.calls))
		}
		want := "space join: billing is already an active participant of acme\n"
		if asJSON {
			want = `{"status":"already-active","system":"billing","space":"acme","section":"billing/"}` + "\n"
		}
		if out.String() != want {
			t.Errorf("json=%v: stdout = %q, want %q", asJSON, out.String(), want)
		}
	}
}

func TestSpaceJoinRefusals(t *testing.T) {
	t.Parallel()
	left := "schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\nparticipants:\n" +
		"  - {system: billing, org: local, section: billing, owners: [local:billing], status: left, joined: 2026-10-01}\n"
	cases := []struct {
		name     string
		manifest string
		mutate   func(*cli.SpaceCommand)
		args     []string
		wantCode int
		wantErr  string
	}{
		{"a host whose owners are not self-asserted", joinTemplateManifest(t, "acme"),
			func(c *cli.SpaceCommand) { c.Caps = host.Capabilities{NeedsCredential: true, ReviewsWrites: true} },
			[]string{"join"}, 1, "onboarding manifest PR"},
		{"an unwired capability set", joinTemplateManifest(t, "acme"),
			func(c *cli.SpaceCommand) { c.Caps = host.Capabilities{} },
			[]string{"join"}, 1, "onboarding manifest PR"},
		{"a participant who left", left, nil, []string{"join"}, 1, "reactivating a participant is a manifest decision"},
		{"another space", joinTemplateManifest(t, "acme"), nil, []string{"join", "--space", "other"}, 1, `unknown space "other"`},
		{"a stray argument", joinTemplateManifest(t, "acme"), nil, []string{"join", "extra"}, 2, "usage: a2a space join"},
		{"an invalid system id", joinTemplateManifest(t, "acme"),
			func(c *cli.SpaceCommand) { c.OwnSystem = "Bill ing" },
			[]string{"join"}, 1, "not a valid system id"},
		{"an unwired command", joinTemplateManifest(t, "acme"),
			func(c *cli.SpaceCommand) { c.Funnel = nil },
			[]string{"join"}, 1, "not wired"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			funnel := &fakeSubmitFunnel{}
			cmd := joinCommand(t, tc.manifest, funnel)
			if tc.mutate != nil {
				tc.mutate(cmd)
			}
			io, out, errOut := newSpaceIO()
			if code := cmd.Run(context.Background(), tc.args, io); code != tc.wantCode {
				t.Fatalf("code = %d, want %d; stdout=%s stderr=%s", code, tc.wantCode, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}
			if len(funnel.calls) != 0 {
				t.Errorf("a refused join reached the funnel %d time(s)", len(funnel.calls))
			}
		})
	}
}

// failingWriter is a stdout that refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// TestSpaceJoinRefusalsNameTheNextStep pins the migrated refusal paths of
// `space join`: each one prints what was met AND a command the caller runs
// next, after the found part, with the exit code unchanged.
func TestSpaceJoinRefusalsNameTheNextStep(t *testing.T) {
	t.Parallel()
	active := "schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\nparticipants:\n" +
		"  - {system: billing, org: local, section: billing, owners: [local:billing], status: active, joined: 2026-10-01}\n"
	flowList := "schema: space/v1\nspace: acme\nmin_binary_version: 9.9.9\n" +
		"participants: [{system: other, org: local, section: other, owners: [local:other], status: active, joined: 2026-10-01}]\n"
	cases := []struct {
		name      string
		manifest  string
		prep      func(t *testing.T, cmd *cli.SpaceCommand, funnel *fakeSubmitFunnel)
		args      []string
		stdout    io.Writer
		wantFound string
		wantNext  string
	}{
		{name: "an unwired command", manifest: joinTemplateManifest(t, "acme"),
			prep:      func(_ *testing.T, cmd *cli.SpaceCommand, _ *fakeSubmitFunnel) { cmd.Funnel = nil },
			args:      []string{"join"},
			wantFound: "not wired for a connected space", wantNext: "a2a connect <path-to-the-space-repository>"},
		{name: "a mirror with no space.yaml", manifest: joinTemplateManifest(t, "acme"),
			prep: func(t *testing.T, cmd *cli.SpaceCommand, _ *fakeSubmitFunnel) {
				if err := os.Remove(filepath.Join(cmd.MirrorDir, "space.yaml")); err != nil {
					t.Fatal(err)
				}
			},
			args:      []string{"join"},
			wantFound: "cannot read the mirror's space.yaml", wantNext: "run `a2a sync` to refresh the mirror"},
		{name: "a mirror whose space.yaml does not parse", manifest: "participants: [unclosed\n  : : :\n",
			args:      []string{"join"},
			wantFound: "the mirror's space.yaml is unreadable", wantNext: "repair space.yaml in the space repository"},
		{name: "a participants list join cannot edit in place", manifest: flowList,
			args:      []string{"join"},
			wantFound: "a form `join` cannot edit in place", wantNext: "add the row for billing by hand"},
		{name: "a funnel that fails", manifest: joinTemplateManifest(t, "acme"),
			prep: func(_ *testing.T, _ *cli.SpaceCommand, funnel *fakeSubmitFunnel) {
				funnel.err = errors.New("push rejected")
			},
			args:      []string{"join", "--approve"},
			wantFound: "push rejected", wantNext: "`a2a doctor` checks the credential"},
		{name: "a stdout that cannot take the --json result", manifest: active,
			args: []string{"join", "--json"}, stdout: failingWriter{},
			wantFound: "could not be written to stdout", wantNext: "re-run `a2a space join`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			funnel := &fakeSubmitFunnel{}
			cmd := joinCommand(t, tc.manifest, funnel)
			if tc.prep != nil {
				tc.prep(t, cmd, funnel)
			}
			stdio, out, errOut := newSpaceIO()
			if tc.stdout != nil {
				stdio.Stdout = tc.stdout
			}
			if code := cmd.Run(context.Background(), tc.args, stdio); code != 1 {
				t.Fatalf("code = %d, want 1; stdout=%s stderr=%s", code, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantFound) {
				t.Errorf("stderr = %q, want its found part to carry %q", errOut.String(), tc.wantFound)
			}
			requireNextStep(t, errOut.String(), tc.wantFound, tc.wantNext)
			if !strings.HasPrefix(errOut.String(), "space join") {
				t.Errorf("stderr = %q, want it to open with the attempted verb", errOut.String())
			}
		})
	}
}

func TestSpaceJoinKeepsASectionPathTheMirrorAlreadyHas(t *testing.T) {
	t.Parallel()
	funnel := &fakeSubmitFunnel{}
	cmd := joinCommand(t, joinTemplateManifest(t, "acme"), funnel)
	writeSpaceMirrorFile(t, cmd.MirrorDir, "billing/consumes.yaml", "schema: consumes/v1\nsystem: billing\ndependencies:\n  - system: orders\n")

	io, out, errOut := newSpaceIO()
	if code := cmd.Run(context.Background(), []string{"join", "--approve"}, io); code != 0 {
		t.Fatalf("code = %d; %s%s", code, out.String(), errOut.String())
	}
	for _, f := range funnel.calls[0].Files {
		if f.Path == "billing/consumes.yaml" {
			t.Errorf("join overwrote a consumes.yaml the section already had")
		}
	}
}

// --- gates and flow policy (local-space-2026-10 spec 04 §T1) --------------

var (
	spaceLocalCaps    = host.Capabilities{OwnerIdentitySelfAsserted: true, OnThisMachine: true, LandsSynchronously: true}
	spaceReviewedCaps = host.Capabilities{ReviewsWrites: true, NeedsCredential: true, OwnerIdentitySelfAsserted: true}
)

// policyManifest is the embedded template's space.yaml with its `gates:` line
// replaced by lines, so a test chooses the mode the tip declares.
func policyManifest(t *testing.T, lines string) string {
	t.Helper()
	m := joinTemplateManifest(t, "acme")
	const def = "gates: default"
	if !strings.Contains(m, def) {
		t.Fatalf("template space.yaml no longer has a %q line:\n%s", def, m)
	}
	return strings.Replace(m, def, lines, 1)
}

// policyCommand is `space set` / `join` / `update` wired the way cmd/a2a wires
// them: the real manifest engine, a mirror carrying manifest, and the host's
// declared capabilities.
func policyCommand(t *testing.T, manifest string, caps host.Capabilities, funnel *fakeSubmitFunnel) *cli.SpaceCommand {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	cmd := joinCommand(t, manifest, funnel)
	cmd.Engine = validate.New(corpus)
	cmd.Caps = caps
	return cmd
}

// runSpace runs one space sub-verb and returns its exit code and both streams.
func runSpace(t *testing.T, cmd *cli.SpaceCommand, args ...string) (int, string, string) {
	t.Helper()
	stdio, out, errOut := newSpaceIO()
	code := cmd.Run(context.Background(), args, stdio)
	return code, out.String(), errOut.String()
}

// TestSpaceSetWritesTheKeyAndJudgesG4UnderTheTipsMode is spec 04 AC10 and the
// `space set` row of §T1: the edit keeps the line's comment, a change is a G4
// write refused without --approve in a confirm space (even the flip to
// autonomous, judged under the TIP's mode), and the approval is recorded on the
// request as exactly G4.
func TestSpaceSetWritesTheKeyAndJudgesG4UnderTheTipsMode(t *testing.T) {
	t.Parallel()
	confirm := policyManifest(t, "gates: confirm")
	autonomous := policyManifest(t, "gates: autonomous")
	cases := []struct {
		name         string
		manifest     string
		caps         host.Capabilities
		args         []string
		wantCode     int
		wantStdout   string
		wantStderr   string
		wantCalls    int
		wantApproved []string
		wantLine     string
	}{
		{name: "the flip to autonomous is itself refused without --approve", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"set", "gates", "autonomous"}, wantCode: 1, wantStderr: "POL-029: G4 requires --approve in a confirm space"},
		{name: "the flip to autonomous with --approve lands and records G4", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"set", "gates", "autonomous", "--approve"}, wantStdout: "space set: gates confirm -> autonomous\n",
			wantCalls: 1, wantApproved: []string{"G4"}, wantLine: "gates: autonomous"},
		{name: "--approve before the key parses the same", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"set", "--approve", "gates", "autonomous"}, wantStdout: "space set: gates confirm -> autonomous\n",
			wantCalls: 1, wantApproved: []string{"G4"}},
		{name: "a template default reads as confirm on a local host", manifest: policyManifest(t, "gates: default"), caps: spaceLocalCaps,
			args: []string{"set", "flow", "full"}, wantStdout: "space set: flow already full\n"},
		{name: "a template default on a local host is judged as confirm", manifest: policyManifest(t, "gates: default"), caps: spaceLocalCaps,
			args: []string{"set", "flow", "express"}, wantCode: 1, wantStderr: "POL-029: G4 requires --approve in a confirm space"},
		{name: "under autonomous a manifest change lands without --approve and records nothing", manifest: autonomous, caps: spaceLocalCaps,
			args: []string{"set", "gates", "confirm"}, wantStdout: "space set: gates autonomous -> confirm\n", wantCalls: 1},
		{name: "an absent flow key is added", manifest: autonomous, caps: spaceLocalCaps,
			args: []string{"set", "flow", "express"}, wantStdout: "space set: flow full -> express\n", wantCalls: 1, wantLine: "flow: express"},
		{name: "an unchanged value exits 0 and writes nothing, even without --approve", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"set", "gates", "confirm"}, wantStdout: "space set: gates already confirm\n"},
		{name: "an absent key is already its default", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"set", "flow", "full"}, wantStdout: "space set: flow already full\n"},
		{name: "a reviewed host changes the manifest as before, with no approval", manifest: policyManifest(t, "gates: default"), caps: spaceReviewedCaps,
			args: []string{"set", "flow", "express"}, wantStdout: "space set: flow full -> express\n", wantCalls: 1},
		{name: "a reviewed host refuses --approve with exit 2", manifest: confirm, caps: spaceReviewedCaps,
			args: []string{"set", "flow", "express", "--approve"}, wantCode: 2,
			wantStderr: "--approve has no effect here: this space's host reviews gated writes in a pull request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			funnel := &fakeSubmitFunnel{}
			cmd := policyCommand(t, tc.manifest, tc.caps, funnel)
			code, stdout, stderr := runSpace(t, cmd, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d; stdout=%q stderr=%q", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantStdout != "" && stdout != tc.wantStdout {
				t.Fatalf("stdout = %q, want %q", stdout, tc.wantStdout)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.wantStderr)
			}
			if len(funnel.calls) != tc.wantCalls {
				t.Fatalf("funnel calls = %d, want %d", len(funnel.calls), tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				return
			}
			req := funnel.calls[0]
			if !slices.Equal(req.ApprovedGates, tc.wantApproved) {
				t.Fatalf("ApprovedGates = %v, want %v", req.ApprovedGates, tc.wantApproved)
			}
			if len(req.Files) != 1 || req.Files[0].Path != "space.yaml" || !req.AllowSpaceInfrastructure {
				t.Fatalf("request = %+v, want one space.yaml write marked as space infrastructure", req)
			}
			if tc.wantLine != "" && !strings.Contains(string(req.Files[0].Content), tc.wantLine) {
				t.Fatalf("written space.yaml lacks %q:\n%s", tc.wantLine, req.Files[0].Content)
			}
		})
	}
}

// TestSpaceSetKeepsTheTemplateComments is the "comment-preserving" half of the
// space set row: only the value changes, and the template's trailing comment
// stays on the same line.
func TestSpaceSetKeepsTheTemplateComments(t *testing.T) {
	t.Parallel()
	funnel := &fakeSubmitFunnel{}
	cmd := policyCommand(t, policyManifest(t, "gates: autonomous"), spaceLocalCaps, funnel)
	if code, _, stderr := runSpace(t, cmd, "set", "gates", "confirm"); code != 0 {
		t.Fatalf("code = %d: %s", code, stderr)
	}
	want := strings.Replace(policyManifest(t, "gates: autonomous"), "gates: autonomous", "gates: confirm   ", 1)
	got := string(funnel.calls[0].Files[0].Content)
	// Everything but the edited line is byte-identical to the tip.
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	if len(gotLines) != len(wantLines) {
		t.Fatalf("line count %d != %d:\n%s", len(gotLines), len(wantLines), got)
	}
	for i := range gotLines {
		if strings.HasPrefix(gotLines[i], "gates:") {
			if !strings.HasPrefix(gotLines[i], "gates: confirm") || !strings.Contains(gotLines[i], "# G1-G5 per §3.7") {
				t.Fatalf("gates line = %q, want the new value with the template's comment kept", gotLines[i])
			}
			continue
		}
		if gotLines[i] != wantLines[i] {
			t.Fatalf("line %d changed: %q -> %q", i+1, wantLines[i], gotLines[i])
		}
	}
}

// TestSpaceSetRefusesWhatTheManifestWriterMayNotWrite is spec 04's reachability
// for POL-027 and POL-028 at the cli tier: a value outside the vocabulary is
// refused by the engine that owns the vocabulary, and `autonomous` is refused
// on a host that reviews writes, both before any write is prepared and neither
// needing --approve.
func TestSpaceSetRefusesWhatTheManifestWriterMayNotWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		caps     host.Capabilities
		manifest string
		args     []string
		wantCode int
		want     string
	}{
		{name: "gates outside its vocabulary (POL-027)", caps: spaceLocalCaps, manifest: policyManifest(t, "gates: confirm"),
			args: []string{"set", "gates", "sometimes"}, wantCode: 1, want: "POL-027"},
		{name: "flow outside its vocabulary (POL-027)", caps: spaceLocalCaps, manifest: policyManifest(t, "gates: autonomous"),
			args: []string{"set", "flow", "turbo"}, wantCode: 1, want: "POL-027"},
		{name: "autonomous on a host that reviews writes (POL-028)", caps: spaceReviewedCaps, manifest: policyManifest(t, "gates: default"),
			args: []string{"set", "gates", "autonomous"}, wantCode: 1, want: "POL-028"},
		{name: "an unknown key is a usage error", caps: spaceLocalCaps, manifest: policyManifest(t, "gates: confirm"),
			args: []string{"set", "min_binary_version", "9.9.9"}, wantCode: 2, want: "usage: a2a space set <gates|flow> <value>"},
		{name: "a missing value is a usage error", caps: spaceLocalCaps, manifest: policyManifest(t, "gates: confirm"),
			args: []string{"set", "gates"}, wantCode: 2, want: "usage: a2a space set <gates|flow> <value>"},
		{name: "another space is refused", caps: spaceLocalCaps, manifest: policyManifest(t, "gates: confirm"),
			args: []string{"set", "gates", "autonomous", "--space", "other"}, wantCode: 1, want: `unknown space "other"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			funnel := &fakeSubmitFunnel{}
			cmd := policyCommand(t, tc.manifest, tc.caps, funnel)
			code, stdout, stderr := runSpace(t, cmd, tc.args...)
			if code != tc.wantCode || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code = %d stderr = %q stdout = %q, want code %d naming %q", code, stderr, stdout, tc.wantCode, tc.want)
			}
			if len(funnel.calls) != 0 {
				t.Fatalf("a refused set reached the funnel: %+v", funnel.calls)
			}
		})
	}
}

// TestSpaceJoinAndUpdateAreG4WritesInAConfirmSpace is spec 04 AC13 and the
// join/update row of §T1: a write whose changed set includes space.yaml is
// refused without --approve in a confirm space, records exactly G4 with it,
// lands ungated and unrecorded under autonomous, and a write that leaves
// space.yaml alone is not a G4 write.
func TestSpaceJoinAndUpdateAreG4WritesInAConfirmSpace(t *testing.T) {
	t.Parallel()
	confirm := policyManifest(t, "gates: confirm")
	autonomous := policyManifest(t, "gates: autonomous")
	cases := []struct {
		name         string
		manifest     string
		caps         host.Capabilities
		args         []string
		wantCode     int
		wantStderr   string
		wantCalls    int
		wantApproved []string
	}{
		{name: "join in a confirm space is refused without --approve", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"join"}, wantCode: 1, wantStderr: "POL-029: G4 requires --approve in a confirm space"},
		{name: "join with --approve lands and records G4", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"join", "--approve"}, wantCalls: 1, wantApproved: []string{"G4"}},
		{name: "join under autonomous lands and records nothing", manifest: autonomous, caps: spaceLocalCaps,
			args: []string{"join"}, wantCalls: 1},
		{name: "join under autonomous with --approve records nothing", manifest: autonomous, caps: spaceLocalCaps,
			args: []string{"join", "--approve"}, wantCalls: 1},
		{name: "join with --approve on a reviewed host exits 2", manifest: confirm, caps: spaceReviewedCaps,
			args: []string{"join", "--approve"}, wantCode: 2, wantStderr: "--approve has no effect here: this space's host reviews gated writes in a pull request"},
		{name: "update that bumps the floor is refused without --approve", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"update"}, wantCode: 1, wantStderr: "POL-029: G4 requires --approve in a confirm space"},
		{name: "update that bumps the floor with --approve records G4", manifest: confirm, caps: spaceLocalCaps,
			args: []string{"update", "--approve"}, wantCalls: 1, wantApproved: []string{"G4"}},
		{name: "update on a reviewed host lands as before", manifest: confirm, caps: spaceReviewedCaps,
			args: []string{"update"}, wantCalls: 1},
		{name: "update with --approve on a reviewed host exits 2", manifest: confirm, caps: spaceReviewedCaps,
			args: []string{"update", "--approve"}, wantCode: 2, wantStderr: "--approve has no effect here: this space's host reviews gated writes in a pull request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			funnel := &fakeSubmitFunnel{}
			cmd := policyCommand(t, tc.manifest, tc.caps, funnel)
			if tc.args[0] == "update" {
				// The mirror's floor is below the template's, so the plan
				// carries space.yaml (the floor bump).
				writeSpaceMirrorFile(t, cmd.MirrorDir, "space.yaml", regexp.MustCompile(`(?m)^min_binary_version: *\S+`).ReplaceAllString(tc.manifest, "min_binary_version: 0.0.1"))
				cmd.TemplateFiles = spacetemplate.Files
			}
			code, stdout, stderr := runSpace(t, cmd, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d; stdout=%q stderr=%q", code, tc.wantCode, stdout, stderr)
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr, tc.wantStderr)
			}
			if len(funnel.calls) != tc.wantCalls {
				t.Fatalf("funnel calls = %d, want %d", len(funnel.calls), tc.wantCalls)
			}
			if tc.wantCalls == 1 && !slices.Equal(funnel.calls[0].ApprovedGates, tc.wantApproved) {
				t.Fatalf("ApprovedGates = %v, want %v", funnel.calls[0].ApprovedGates, tc.wantApproved)
			}
		})
	}
}

// TestSpaceUpdateThatLeavesTheManifestAloneIsNotG4 pins the other half of the
// G4 condition: only a changed set that includes space.yaml is gated.
func TestSpaceUpdateThatLeavesTheManifestAloneIsNotG4(t *testing.T) {
	t.Parallel()
	mirrorDir := t.TempDir()
	spaceUpdateSeedMirror(t, mirrorDir)
	writeSpaceMirrorFile(t, mirrorDir, "space.yaml",
		"schema: manifest/v1\nspace: getvisa\nmin_binary_version: 5.0.0\nparticipants: []\n")
	funnel := &fakeSubmitFunnel{}
	cmd := spaceUpdateFullyWiredCommand(spaceUpdateTemplateFS(), mirrorDir, funnel, "9.9.9")
	cmd.Caps = host.Capabilities{} // a confirm space: the mirror declares no gates
	code, stdout, stderr := runSpace(t, cmd, "update")
	if code != 0 || len(funnel.calls) != 1 {
		t.Fatalf("code=%d calls=%d stdout=%q stderr=%q, want a landed write that never touched space.yaml", code, len(funnel.calls), stdout, stderr)
	}
	if len(funnel.calls[0].ApprovedGates) != 0 {
		t.Fatalf("ApprovedGates = %v, want none for a write without G4", funnel.calls[0].ApprovedGates)
	}
}

// TestSpaceInitLocalWritesTheExplicitPolicy is the `init --local` row of §T1:
// the explicit gates and flow replace the template's `gates: default` line
// (flow defaults to express), a value outside the vocabulary is refused before
// the repository exists, and the policy flags mean nothing without --local.
func TestSpaceInitLocalWritesTheExplicitPolicy(t *testing.T) {
	t.Parallel()
	read := func(t *testing.T, dir string) string {
		t.Helper()
		return spaceGit(t, dir, "show", "main:space.yaml")
	}
	cases := []struct {
		name      string
		args      []string
		wantGates string
		wantFlow  string
	}{
		{name: "defaults", args: nil, wantGates: "gates: confirm", wantFlow: "flow: express"},
		{name: "explicit autonomous and full", args: []string{"--gates", "autonomous", "--flow", "full"}, wantGates: "gates: autonomous", wantFlow: "flow: full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dataRoot := canonicalTempDir(t)
			cmd := newLocalSpaceCommand(t, dataRoot)
			stdio, _, errOut := newSpaceIO()
			args := append([]string{"init", "acme", "--local"}, tc.args...)
			if code := cmd.Run(context.Background(), args, stdio); code != 0 {
				t.Fatalf("code = %d: %s", code, errOut.String())
			}
			got := read(t, filepath.Join(dataRoot, "spaces", "acme.git"))
			for _, want := range []string{tc.wantGates, tc.wantFlow} {
				if !strings.Contains(got, want) {
					t.Fatalf("seeded space.yaml lacks %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "gates: default") {
				t.Fatalf("the template's default line survived:\n%s", got)
			}
		})
	}

	t.Run("a value outside the vocabulary is refused before anything is created", func(t *testing.T) {
		t.Parallel()
		dataRoot := canonicalTempDir(t)
		cmd := newLocalSpaceCommand(t, dataRoot)
		stdio, _, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "acme", "--local", "--gates", "sometimes"}, stdio)
		if code != 1 || !strings.Contains(errOut.String(), "POL-027") {
			t.Fatalf("code = %d stderr = %q, want 1 naming POL-027", code, errOut.String())
		}
		if _, err := os.Stat(filepath.Join(dataRoot, "spaces", "acme.git")); err == nil {
			t.Fatal("a refused init created the repository")
		}
	})
	t.Run("a host that reviews writes refuses autonomous before the repository exists", func(t *testing.T) {
		t.Parallel()
		dataRoot := canonicalTempDir(t)
		cmd := newLocalSpaceCommand(t, dataRoot)
		cmd.Caps = host.Capabilities{ReviewsWrites: true}
		stdio, _, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "acme", "--local", "--gates", "autonomous"}, stdio)
		if code != 1 || !strings.Contains(errOut.String(), "POL-028") {
			t.Fatalf("code = %d stderr = %q, want 1 naming POL-028", code, errOut.String())
		}
		if _, err := os.Stat(filepath.Join(dataRoot, "spaces", "acme.git")); err == nil {
			t.Fatal("a refused init created the repository")
		}
	})
	t.Run("the policy flags are refused without --local", func(t *testing.T) {
		t.Parallel()
		cmd := newLocalSpaceCommand(t, canonicalTempDir(t))
		stdio, _, errOut := newSpaceIO()
		code := cmd.Run(context.Background(), []string{"init", "acme", "--gates", "autonomous", "--dir", filepath.Join(t.TempDir(), "x")}, stdio)
		if code != 2 || !strings.Contains(errOut.String(), "--gates only applies with --local") {
			t.Fatalf("code = %d stderr = %q, want 2 naming --gates", code, errOut.String())
		}
	})
}
