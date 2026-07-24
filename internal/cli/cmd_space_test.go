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
				"space: REPLACE_WITH_SPACE_ID\n" +
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
		"scope: space directive — prerequisite (spec 35 §T3: this PR cannot merge until protection stops requiring the old flat context)",
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
