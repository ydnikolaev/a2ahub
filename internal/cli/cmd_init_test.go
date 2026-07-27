package cli_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func newIO() (cli.IO, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return cli.IO{Stdin: bytes.NewReader(nil), Stdout: &out, Stderr: &errOut}, &out, &errOut
}

// TestInitNonInteractiveWritesConfig is AC row 6 (`a2a init --system
// --space ...` never blocks on stdin): with stdin closed/non-TTY and every
// required flag present, init writes a valid config and exits 0.
func TestInitNonInteractiveWritesConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	cmd := cli.NewInitCommand(cfgPath)

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s", code, out.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if cfg.System != "axon" {
		t.Fatalf("System = %q, want axon", cfg.System)
	}
	if len(cfg.Spaces) != 1 || cfg.Spaces[0].RepoURL != "https://example.invalid/org/space.git" {
		t.Fatalf("Spaces = %+v, want one entry with the given repo URL", cfg.Spaces)
	}
	// AC-3.6 (agent-ops-2026-07 spec 03-conventions §8): the closing advice
	// names the three convention reference pages — nothing else in the
	// output points an agent at them, so a silently-dropped line here is a
	// silently-undiscoverable page.
	if !strings.Contains(out.String(), "status-announcements.md") ||
		!strings.Contains(out.String(), "retraction.md") ||
		!strings.Contains(out.String(), "bindings.md") {
		t.Fatalf("expected the closing advice to name the three convention reference pages; got %q", out.String())
	}
}

// TestInitIdempotentRerun: re-running init with identical flags is a
// no-op "already configured" (§7.2 tail; spec 06 §6 table).
func TestInitIdempotentRerun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	cmd := cli.NewInitCommand(cfgPath)

	io1, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, io1); code != 0 {
		t.Fatalf("first run: code = %d", code)
	}
	info1, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat after first run: %v", err)
	}

	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/space.git"}, io2); code != 0 {
		t.Fatalf("second (identical) run: code = %d", code)
	}
	if !bytes.Contains(out2.Bytes(), []byte("already configured")) {
		t.Fatalf("expected an 'already configured' message on identical re-run; got %q", out2.String())
	}
	info2, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat after second run: %v", err)
	}
	if info1.ModTime() != info2.ModTime() {
		t.Fatal("expected the config file to be untouched on an idempotent re-run")
	}
}

// TestInitMissingSystemNeverHangs is AC row 6's negative case: a missing
// required flag with stdin closed/non-TTY errors immediately (exit 2),
// never blocks reading stdin for a prompt.
func TestInitMissingSystemNeverHangs(t *testing.T) {
	t.Parallel()
	cmd := cli.NewInitCommand(filepath.Join(t.TempDir(), ".a2a", "config.yaml"))
	io, _, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--space", "https://example.invalid/org/space.git"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error, missing --system)", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("expected an actionable error message on stderr")
	}
}

func TestInitMissingSpaceNeverHangs(t *testing.T) {
	t.Parallel()
	cmd := cli.NewInitCommand(filepath.Join(t.TempDir(), ".a2a", "config.yaml"))
	io, _, _ := newIO()
	code := cmd.Run(context.Background(), []string{"--system", "axon"}, io)
	if code != 2 {
		t.Fatalf("code = %d, want 2 (usage error, missing --space)", code)
	}
}

// --- connect / disconnect --------------------------------------------------

func TestConnectRegistersSpaceAndClonesMirror(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	projectRoot := dir

	cmd := cli.NewConnectCommand(cfgPath, machinePath, projectRoot)
	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; output=%s", code, out.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 {
		t.Fatalf("Spaces = %+v, want exactly one connected space", cfg.Spaces)
	}
	mirrorDir := space.ResolveMirrorLocation(projectRoot, cfg.Spaces[0], space.MachineConfig{})
	if _, err := os.Stat(filepath.Join(mirrorDir, ".git")); err != nil {
		t.Fatalf("expected a cloned mirror at %s: %v", mirrorDir, err)
	}
}

// TestConnectIdempotent: connecting an already-connected space re-fetches
// rather than duplicating the config entry.
func TestConnectIdempotent(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)

	io1, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io1); code != 0 {
		t.Fatalf("first connect: code = %d", code)
	}
	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io2); code != 0 {
		t.Fatalf("second connect: code = %d", code)
	}
	if !bytes.Contains(out2.Bytes(), []byte("already connected")) {
		t.Fatalf("expected an 'already connected' message; got %q", out2.String())
	}
	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 {
		t.Fatalf("Spaces = %+v, want exactly one entry (no duplicate)", cfg.Spaces)
	}
}

// TestConnectRecordsCredentialReferenceUnderTheAuthoritativeID: the space
// id the credentials map must be keyed by comes out of the mirror's own
// space.yaml, which only connect ever reads — so connect is the one place
// that can record a reference under the RIGHT key. It appends to the
// operator's existing machine config without disturbing what is already
// there (comments and all).
func TestConnectRecordsCredentialReferenceUnderTheAuthoritativeID(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	existing := "# my own notes\nmirror_root: " + filepath.Join(dir, "mirrors") + "\ncredentials:\n  other-space: env:A2A_TOKEN_OTHER_SPACE\n"
	if err := os.WriteFile(machinePath, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed machine config: %v", err)
	}

	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string {
		return "env:A2A_TOKEN_" + strings.ToUpper(id)
	})
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io); code != 0 {
		t.Fatalf("connect: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	id := cfg.Spaces[0].ID

	machine, err := space.LoadMachineConfig(machinePath)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if machine.Credentials[id] == "" {
		t.Fatalf("expected a credential reference under the authoritative id %q; got %+v", id, machine.Credentials)
	}
	if machine.Credentials["other-space"] != "env:A2A_TOKEN_OTHER_SPACE" {
		t.Fatalf("the operator's existing entry must survive; got %+v", machine.Credentials)
	}
	raw, err := os.ReadFile(machinePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Contains(raw, []byte("# my own notes")) {
		t.Fatalf("the operator's comments must survive the edit; got:\n%s", raw)
	}

	// Idempotent: a second connect leaves the file byte-identical.
	before := string(raw)
	io2, _, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io2); code != 0 {
		t.Fatalf("second connect: code = %d", code)
	}
	after, err := os.ReadFile(machinePath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(after) != before {
		t.Fatalf("second connect rewrote the machine config:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

// TestConnectRepairsAStaleURLDerivedID: `a2a init -space <url>` cannot
// know the real space id (it never clones), so it registers the URL's
// basename. connect is the first moment the manifest is readable — it must
// CORRECT that entry, not leave a config naming a space that does not
// exist while `doctor` reports green.
func TestConnectRepairsAStaleURLDerivedID(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")

	// What init writes: the id guessed from the URL, which the fixture's own
	// space.yaml disagrees with.
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := "system: axon\nspaces:\n  - id: wrong-guess\n    repo_url: " + fx.RemoteURL() + "\n"
	if err := os.WriteFile(cfgPath, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string { return "env:A2A_TOKEN_" + id })
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io); code != 0 {
		t.Fatalf("connect: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 {
		t.Fatalf("Spaces = %+v, want the SAME single entry, repaired (never a duplicate)", cfg.Spaces)
	}
	if cfg.Spaces[0].ID == "wrong-guess" {
		t.Fatalf("the stale id survived: %+v", cfg.Spaces[0])
	}
	if !strings.Contains(out.String(), "corrected space id") {
		t.Fatalf("expected connect to REPORT the correction, got %q", out.String())
	}

	// Idempotent: a second connect finds nothing to repair.
	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io2); code != 0 {
		t.Fatalf("second connect: code = %d", code)
	}
	if strings.Contains(out2.String(), "corrected space id") {
		t.Fatalf("second connect must find nothing to correct, got %q", out2.String())
	}
}

// --- AC-1050.8: connect converges the persisted ref's MirrorLocation with
// the directory it actually cloned into --------------------------------

// countMirrorGitDirs counts ".git" directories found anywhere under root —
// used to assert a mirror clone landed in exactly ONE place, never both the
// project-relative default AND a configured mirror_root.
func countMirrorGitDirs(t testing.TB, root string) int {
	t.Helper()
	n := 0
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil //nolint:nilerr // reason: best-effort walk — an entry this test cannot stat is by definition not a mirror it is counting, and aborting the whole walk over one would turn a permissions quirk into a spurious failure (same shape and same reason as cmd/a2a/wire.go's mirrorHoldsArtifact)
		}
		if info.IsDir() && info.Name() == ".git" {
			n++
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk %s: %v", root, err)
	}
	return n
}

// TestConnectRepairsMissingMirrorLocationOnAlreadyRegisteredRef is AC-1050.8
// (this phase's Defect 1): `a2a init --space <url>` persists a ref with NO
// MirrorLocation (init never clones, so it cannot know one) — `connect` on
// that SAME url must set/repair MirrorLocation on the ref it finds already
// registered, not only on a freshly-registered one, so the persisted ref
// converges with the directory the clone actually landed in.
// space.ResolveMirrorLocation ignores mirror_root entirely when
// MirrorLocation is empty, so this is exercised both with and without a
// configured mirror_root — the exact bug shape: with mirror_root set, an
// unrepaired ref would resolve project-relatively while the clone landed
// under mirror_root, leaving an orphan mirror `disconnect` never removes.
func TestConnectRepairsMissingMirrorLocationOnAlreadyRegisteredRef(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		mirrorRoot bool
	}{
		{name: "mirror_root unset"},
		{name: "mirror_root set", mirrorRoot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := spacefixture.New(t, "axon")
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
			machinePath := filepath.Join(dir, "machine.yaml")

			initCmd := cli.NewInitCommand(cfgPath)
			ioInit, outInit, _ := newIO()
			if code := initCmd.Run(context.Background(), []string{"--system", "axon", "--space", fx.RemoteURL()}, ioInit); code != 0 {
				t.Fatalf("init: code = %d; output=%s", code, outInit.String())
			}
			cfg0, err := space.LoadProjectConfig(cfgPath)
			if err != nil {
				t.Fatalf("LoadProjectConfig after init: %v", err)
			}
			if len(cfg0.Spaces) != 1 || cfg0.Spaces[0].MirrorLocation != "" {
				t.Fatalf("precondition: init must persist a ref with no MirrorLocation, got %+v", cfg0.Spaces)
			}

			if tc.mirrorRoot {
				machineYAML := "mirror_root: " + filepath.Join(dir, "mirrors") + "\n"
				if err := os.WriteFile(machinePath, []byte(machineYAML), 0o644); err != nil {
					t.Fatalf("seed machine config: %v", err)
				}
			}

			connect := cli.NewConnectCommand(cfgPath, machinePath, dir)
			connect.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string { return "env:A2A_TOKEN_" + id })
			io1, out1, errOut1 := newIO()
			if code := connect.Run(context.Background(), []string{fx.RemoteURL()}, io1); code != 0 {
				t.Fatalf("connect: code = %d; stdout=%s stderr=%s", code, out1.String(), errOut1.String())
			}

			cfg1, err := space.LoadProjectConfig(cfgPath)
			if err != nil {
				t.Fatalf("LoadProjectConfig after connect: %v", err)
			}
			if len(cfg1.Spaces) != 1 {
				t.Fatalf("Spaces = %+v, want exactly one entry (repair, never a duplicate)", cfg1.Spaces)
			}
			if cfg1.Spaces[0].MirrorLocation == "" {
				t.Fatalf("expected connect to repair the missing MirrorLocation, got %+v", cfg1.Spaces[0])
			}

			machine, _ := space.LoadMachineConfig(machinePath)
			mirrorDir := space.ResolveMirrorLocation(dir, cfg1.Spaces[0], machine)
			if _, err := os.Stat(filepath.Join(mirrorDir, ".git")); err != nil {
				t.Fatalf("persisted ref must resolve to the real clone at %s: %v", mirrorDir, err)
			}
			if n := countMirrorGitDirs(t, dir); n != 1 {
				t.Fatalf("expected exactly ONE mirror clone under %s, found %d", dir, n)
			}

			// Idempotent: a ref that already carries the right MirrorLocation
			// is not rewritten and connect prints no repair message.
			io2, out2, errOut2 := newIO()
			if code := connect.Run(context.Background(), []string{fx.RemoteURL()}, io2); code != 0 {
				t.Fatalf("second connect: code = %d; stdout=%s stderr=%s", code, out2.String(), errOut2.String())
			}
			if strings.Contains(out2.String(), "repaired missing mirror location") || strings.Contains(out2.String(), "corrected mirror location") {
				t.Fatalf("second connect must find nothing to repair, got %q", out2.String())
			}
			cfg2, err := space.LoadProjectConfig(cfgPath)
			if err != nil {
				t.Fatalf("LoadProjectConfig after second connect: %v", err)
			}
			if len(cfg2.Spaces) != 1 || cfg2.Spaces[0].MirrorLocation != cfg1.Spaces[0].MirrorLocation {
				t.Fatalf("second connect must not change the repaired ref, got %+v (was %+v)", cfg2.Spaces, cfg1.Spaces)
			}
		})
	}
}

// TestConnectRepairsStaleIDAndMissingMirrorLocationTogether combines both
// AC-1050.8 repair paths this phase calls out: a config entry keyed under
// the WRONG id (as `a2a init --space <url>` guesses it, mirroring
// TestConnectRepairsAStaleURLDerivedID) AND missing MirrorLocation (also
// init's doing) — connect's single write must fix both in one pass.
func TestConnectRepairsStaleIDAndMissingMirrorLocationTogether(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := "system: axon\nspaces:\n  - id: wrong-guess\n    repo_url: " + fx.RemoteURL() + "\n"
	if err := os.WriteFile(cfgPath, []byte(stale), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string { return "env:A2A_TOKEN_" + id })
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io); code != 0 {
		t.Fatalf("connect: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 {
		t.Fatalf("Spaces = %+v, want the SAME single entry, repaired (never a duplicate)", cfg.Spaces)
	}
	if cfg.Spaces[0].ID == "wrong-guess" {
		t.Fatalf("the stale id survived: %+v", cfg.Spaces[0])
	}
	if cfg.Spaces[0].MirrorLocation == "" {
		t.Fatalf("expected MirrorLocation repaired alongside the id, got %+v", cfg.Spaces[0])
	}
	if !strings.Contains(out.String(), "corrected space id") {
		t.Fatalf("expected connect to report the id correction, got %q", out.String())
	}
	if !strings.Contains(out.String(), "mirror location") {
		t.Fatalf("expected connect to report the mirror location repair, got %q", out.String())
	}

	// Idempotent: a second connect finds nothing left to repair.
	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io2); code != 0 {
		t.Fatalf("second connect: code = %d", code)
	}
	if strings.Contains(out2.String(), "corrected space id") || strings.Contains(out2.String(), "mirror location") {
		t.Fatalf("second connect must find nothing to correct, got %q", out2.String())
	}
}

// TestConnectLeavesMissingMachineConfigToInit: connect never CREATES the
// machine config (that is `a2a init`'s job) — a missing one is not an error.
func TestConnectLeavesMissingMachineConfigToInit(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	machinePath := filepath.Join(dir, "machine.yaml")

	cmd := cli.NewConnectCommand(filepath.Join(dir, ".a2a", "config.yaml"), machinePath, dir)
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string { return "env:A2A_TOKEN_" + id })
	io, out, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io); code != 0 {
		t.Fatalf("connect: code = %d; output=%s", code, out.String())
	}
	if _, err := os.Stat(machinePath); !os.IsNotExist(err) {
		t.Fatalf("connect must not create the machine config itself (stat err = %v)", err)
	}
}

func TestDisconnectRemovesConfigAndMirror(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")

	connect := cli.NewConnectCommand(cfgPath, machinePath, dir)
	io1, _, _ := newIO()
	if code := connect.Run(context.Background(), []string{fx.RemoteURL()}, io1); code != 0 {
		t.Fatalf("connect: code = %d", code)
	}
	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	spaceID := cfg.Spaces[0].ID
	mirrorDir := space.ResolveMirrorLocation(dir, cfg.Spaces[0], space.MachineConfig{})

	disconnect := cli.NewDisconnectCommand(cfgPath, machinePath, dir, cli.NewNoopCacheRemover())
	io2, _, _ := newIO()
	if code := disconnect.Run(context.Background(), []string{spaceID}, io2); code != 0 {
		t.Fatalf("disconnect: code = %d", code)
	}

	cfg2, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig after disconnect: %v", err)
	}
	if len(cfg2.Spaces) != 0 {
		t.Fatalf("Spaces = %+v, want none after disconnect", cfg2.Spaces)
	}
	if _, err := os.Stat(mirrorDir); !os.IsNotExist(err) {
		t.Fatalf("expected mirror dir %s to be removed, stat err = %v", mirrorDir, err)
	}
}

// TestDisconnectNeverConnectedIsIdempotentNoop: disconnecting a space
// that was never connected is a no-op success, per §7.2 tail's "every
// mutating command is safe to re-run".
func TestDisconnectNeverConnectedIsIdempotentNoop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	cmd := cli.NewDisconnectCommand(cfgPath, machinePath, dir, cli.NewNoopCacheRemover())

	io, out, _ := newIO()
	code := cmd.Run(context.Background(), []string{"never-connected"}, io)
	if code != 0 {
		t.Fatalf("code = %d, want 0 (idempotent no-op)", code)
	}
	if !bytes.Contains(out.Bytes(), []byte("not connected")) {
		t.Fatalf("expected a 'not connected' message; got %q", out.String())
	}
}

// --- FIX A: connect resolves the real space id from the manifest ---------

// runGitInDir runs `git <args...>` with cwd=dir, explicit argv (never
// sh -c), a fixed commit identity (so tests never depend on the host
// machine's global git config), failing the test loudly on error. Mirrors
// testkit/spacefixture's own unexported git helper — needed here because
// this test rewrites a fixture's seeded space.yaml after spacefixture.New
// and must commit + push that change itself.
func runGitInDir(t testing.TB, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture",
		"GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture",
		"GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out.String())
	}
}

// TestConnectResolvesSpaceIDFromManifest is FIX A's primary case: the
// mirror's space.yaml `space:` field ("getvisa") differs from the
// URL-derived id (spacefixture always names its bare repo "origin.git" ->
// "origin") — connect must register the manifest's id, not the URL's, and
// the persisted ref must still resolve back to the one physical mirror
// clone (submit/doctor/disconnect all key off ProjectConfig.Spaces[].ID).
func TestConnectResolvesSpaceIDFromManifest(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	clone := fx.Clone("axon")

	// Overwrite (not prepend to) the fixture's default manifest: its
	// seeded `participants:` block is a map (spacefixture's own minimal
	// shape), which does not decode into space.Manifest.Participants
	// ([]Participant) — ParseManifest would (correctly) error on that
	// mismatch and this test would only ever exercise the fallback path.
	// A manifest carrying just `space:` (participants omitted, structurally
	// optional) isolates the one thing this test cares about.
	manifestPath := filepath.Join(clone, "space.yaml")
	if err := os.WriteFile(manifestPath, []byte("schema: manifest/v1\nspace: getvisa\nmin_binary_version: \"0.0.0\"\n"), 0o644); err != nil {
		t.Fatalf("rewrite fixture manifest: %v", err)
	}
	runGitInDir(t, clone, "add", "-A")
	runGitInDir(t, clone, "commit", "-m", "manifest: add explicit space id")
	runGitInDir(t, clone, "push", "origin", "main")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 || cfg.Spaces[0].ID != "getvisa" {
		t.Fatalf("Spaces = %+v, want exactly one entry with ID \"getvisa\" (the manifest's own id)", cfg.Spaces)
	}

	// The mirror must actually be reachable via the persisted ref (this
	// is what submit/doctor/disconnect resolve against) — not just via
	// the URL-derived id.
	mirrorDir := space.ResolveMirrorLocation(dir, cfg.Spaces[0], space.MachineConfig{})
	if _, err := os.Stat(filepath.Join(mirrorDir, ".git")); err != nil {
		t.Fatalf("expected a cloned mirror reachable from the persisted ref at %s: %v", mirrorDir, err)
	}

	// Idempotent re-run must still key off the resolved id ("getvisa"),
	// not the URL-derived one, and must not duplicate the config entry.
	io2, out2, _ := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io2); code != 0 {
		t.Fatalf("second connect: code = %d", code)
	}
	if !bytes.Contains(out2.Bytes(), []byte(`"getvisa"`)) || !bytes.Contains(out2.Bytes(), []byte("already connected")) {
		t.Fatalf("expected an 'already connected' message naming the resolved id \"getvisa\"; got %q", out2.String())
	}
	cfg2, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig after second connect: %v", err)
	}
	if len(cfg2.Spaces) != 1 {
		t.Fatalf("Spaces = %+v, want exactly one entry (no duplicate)", cfg2.Spaces)
	}
}

// TestConnectMirrorRootWithManifestID guards the fix A regression the P11
// smoke setup would hit: a configured machine `mirror_root` (which fix B now
// seeds by default) combined with a manifest id ≠ the repo basename. The
// clone lands ONCE (before the id is known) and every later resolveMirror
// must find THAT directory — so the persisted ref's ResolveMirrorLocation,
// evaluated with the real machine config, must point at the actual clone.
func TestConnectMirrorRootWithManifestID(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	clone := fx.Clone("axon")
	manifestPath := filepath.Join(clone, "space.yaml")
	if err := os.WriteFile(manifestPath, []byte("schema: manifest/v1\nspace: getvisa\nmin_binary_version: \"0.0.0\"\n"), 0o644); err != nil {
		t.Fatalf("rewrite fixture manifest: %v", err)
	}
	runGitInDir(t, clone, "add", "-A")
	runGitInDir(t, clone, "commit", "-m", "manifest: add explicit space id")
	runGitInDir(t, clone, "push", "origin", "main")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	mirrorRoot := filepath.Join(dir, "custom-mirrors")
	if err := os.WriteFile(machinePath, []byte("mirror_root: "+mirrorRoot+"\n"), 0o644); err != nil {
		t.Fatalf("write machine config: %v", err)
	}

	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)
	io, out, errOut := newIO()
	if code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io); code != 0 {
		t.Fatalf("Run: code = %d; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	machine, err := space.LoadMachineConfig(machinePath)
	if err != nil {
		t.Fatalf("LoadMachineConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 || cfg.Spaces[0].ID != "getvisa" {
		t.Fatalf("Spaces = %+v, want one entry ID \"getvisa\"", cfg.Spaces)
	}
	// The persisted ref, resolved with the REAL machine config (mirror_root
	// set), must land on the directory that was actually cloned into.
	mirrorDir := space.ResolveMirrorLocation(dir, cfg.Spaces[0], machine)
	if _, err := os.Stat(filepath.Join(mirrorDir, ".git")); err != nil {
		t.Fatalf("persisted ref (id=getvisa) with mirror_root must resolve to the real clone at %s: %v", mirrorDir, err)
	}
}

// TestConnectFallsBackToURLIDWhenManifestAbsent: a mirror with no
// space.yaml at all must not crash connect — it falls back to the
// URL-derived id.
func TestConnectFallsBackToURLIDWhenManifestAbsent(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	clone := fx.Clone("axon")

	if err := os.Remove(filepath.Join(clone, "space.yaml")); err != nil {
		t.Fatalf("remove fixture manifest: %v", err)
	}
	runGitInDir(t, clone, "add", "-A")
	runGitInDir(t, clone, "commit", "-m", "manifest: remove space.yaml for fallback test")
	runGitInDir(t, clone, "push", "origin", "main")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0 (fallback, no crash); stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 || cfg.Spaces[0].ID != "origin" {
		t.Fatalf("Spaces = %+v, want the URL-derived fallback id \"origin\"", cfg.Spaces)
	}
}

// TestConnectFallsBackToURLIDWhenManifestUnparseable: a mirror whose
// space.yaml is syntactically invalid YAML must not crash connect — it
// falls back to the URL-derived id, same as the absent-manifest case.
func TestConnectFallsBackToURLIDWhenManifestUnparseable(t *testing.T) {
	t.Parallel()
	fx := spacefixture.New(t, "axon")
	clone := fx.Clone("axon")

	if err := os.WriteFile(filepath.Join(clone, "space.yaml"), []byte("not: [valid: yaml"), 0o644); err != nil {
		t.Fatalf("corrupt fixture manifest: %v", err)
	}
	runGitInDir(t, clone, "add", "-A")
	runGitInDir(t, clone, "commit", "-m", "manifest: corrupt space.yaml for fallback test")
	runGitInDir(t, clone, "push", "origin", "main")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine.yaml")
	cmd := cli.NewConnectCommand(cfgPath, machinePath, dir)

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{fx.RemoteURL()}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0 (fallback, no crash); stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	cfg, err := space.LoadProjectConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadProjectConfig: %v", err)
	}
	if len(cfg.Spaces) != 1 || cfg.Spaces[0].ID != "origin" {
		t.Fatalf("Spaces = %+v, want the URL-derived fallback id \"origin\"", cfg.Spaces)
	}
}

// --- FIX B: init seeds a machine-config skeleton --------------------------

// TestInitSeedsMachineConfigSkeleton: init with a wired MachineConfigPath
// and no existing machine config writes a valid, LoadMachineConfig-parseable
// skeleton carrying a WORKING credential REFERENCE per connected space (a
// reference, never a literal secret) and prints the exact env var that
// overrides it.
func TestInitSeedsMachineConfigSkeleton(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine", "config.yaml")
	cmd := cli.NewInitCommand(cfgPath)
	cmd.MachineConfigPath = machinePath
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string {
		return "env:A2A_TOKEN_" + strings.ToUpper(id)
	})

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/getvisa.git"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	machine, err := space.LoadMachineConfig(machinePath)
	if err != nil {
		t.Fatalf("LoadMachineConfig(%s): %v", machinePath, err)
	}
	if machine.MirrorRoot == "" {
		t.Fatal("expected the skeleton to set a non-empty mirror_root")
	}
	if got := machine.Credentials["getvisa"]; got != "env:A2A_TOKEN_GETVISA" {
		t.Fatalf("Credentials[getvisa] = %q, want the seeded reference", got)
	}
	if _, err := space.ParseCredentialReference(machine.Credentials["getvisa"]); err != nil {
		t.Fatalf("the seeded value must be a parseable credential REFERENCE: %v", err)
	}

	raw, err := os.ReadFile(machinePath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", machinePath, err)
	}
	if !bytes.Contains(raw, []byte("A2A_TOKEN_")) {
		t.Fatalf("expected the skeleton to name the A2A_TOKEN_ convention; got:\n%s", raw)
	}
	if strings.Contains(out.String(), "A2A_TOKEN_GETVISA") {
		t.Fatalf("init must NOT emit a specific A2A_TOKEN_ export with the provisional space id; got %q", out.String())
	}
	if !strings.Contains(out.String(), "a2a connect") {
		t.Fatalf("init must point the user to run `a2a connect`; got %q", out.String())
	}
}

// TestInitNeverOverwritesExistingMachineConfig: init must never clobber an
// operator's already-configured machine config, even though it still
// prints the credential hint.
func TestInitNeverOverwritesExistingMachineConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(machinePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	existing := "mirror_root: /custom/mirrors\ncredentials:\n  getvisa: env:A2A_TOKEN_GETVISA\n"
	if err := os.WriteFile(machinePath, []byte(existing), 0o600); err != nil {
		t.Fatalf("seed existing machine config: %v", err)
	}

	cmd := cli.NewInitCommand(cfgPath)
	cmd.MachineConfigPath = machinePath
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string {
		return "env:A2A_TOKEN_" + strings.ToUpper(id)
	})

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/getvisa.git"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	raw, err := os.ReadFile(machinePath)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", machinePath, err)
	}
	if string(raw) != existing {
		t.Fatalf("existing machine config was modified:\n--- want ---\n%s\n--- got ---\n%s", existing, raw)
	}
	if !strings.Contains(out.String(), "a2a connect") {
		t.Fatalf("init must point the user to run `a2a connect`; got %q", out.String())
	}
}

// TestInitSkipsMachineConfigWhenPathEmpty: MachineConfigPath left empty
// (the catalog/test construction path) is FIX B's documented no-op — no
// machine config file, no hint, no behavior change from before FIX B.
func TestInitSkipsMachineConfigWhenPathEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	cmd := cli.NewInitCommand(cfgPath) // MachineConfigPath left at its zero value ("")

	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/getvisa.git"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}
	if strings.Contains(out.String(), "A2A_TOKEN_") {
		t.Fatalf("expected no credential hint when MachineConfigPath is empty; got %q", out.String())
	}
}

// TestInitDoesNotEmitProvisionalCredentialExport validates the fix for the
// defect where init printed `export A2A_TOKEN_<provisional-id>=<token>` based
// on the URL-derived id. Init never clones so it cannot know the real space id
// (which lives in the manifest and only connect can read) — so it must defer
// the credential instruction to `a2a connect`, which registers the space and
// knows the authoritative id.
func TestInitDoesNotEmitProvisionalCredentialExport(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".a2a", "config.yaml")
	machinePath := filepath.Join(dir, "machine", "config.yaml")
	cmd := cli.NewInitCommand(cfgPath)
	cmd.MachineConfigPath = machinePath
	cmd.SetDefaultCredentialRefForTest(func(_ context.Context, id string) string {
		return "env:A2A_TOKEN_" + strings.ToUpper(id)
	})

	// Use a URL whose basename contains a hyphen to test that init does not emit
	// an invalid shell identifier. The provisional id derived from
	// "https://example.invalid/org/live-e2e-space.git" is "live-e2e-space",
	// and the old code would emit `export A2A_TOKEN_LIVE_E2E_SPACE=...` in the
	// init output.
	io, out, errOut := newIO()
	code := cmd.Run(context.Background(), []string{"--system", "axon", "--space", "https://example.invalid/org/live-e2e-space.git"}, io)
	if code != 0 {
		t.Fatalf("Run: code = %d, want 0; stdout=%s stderr=%s", code, out.String(), errOut.String())
	}

	// Init must NOT emit a specific A2A_TOKEN_ export based on the provisional id.
	if strings.Contains(out.String(), "A2A_TOKEN_LIVE_E2E_SPACE") || strings.Contains(out.String(), "A2A_TOKEN_") {
		t.Fatalf("init must NOT emit a specific A2A_TOKEN_<id> export with the provisional space id; got %q", out.String())
	}

	// Init must point the user to run `a2a connect` to register and get the
	// correct credential instruction.
	if !strings.Contains(out.String(), "a2a connect") {
		t.Fatalf("init must point the user to run `a2a connect`; got %q", out.String())
	}
}

// --- AC-1050.10: the schema's `space` pattern pins the id grammar -------

// spaceManifestInstanceViolations decodes a minimal-but-valid space
// manifest carrying the given id and runs it through the REAL schema
// corpus (schemas/manifest/v1/space.schema.json), mirroring
// TestDoctorSpaceTemplateManifestValidatesWithZeroParticipants's own
// corpus-loading shape.
func spaceManifestInstanceViolations(t *testing.T, id string) []schema.FieldViolation {
	t.Helper()
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	raw := []byte("schema: space/v1\nspace: " + id + "\nmin_binary_version: \"0.0.0\"\nparticipants: []\n")
	instance, err := schema.DecodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("DecodeYAMLInstance(%q): %v", id, err)
	}
	violations, err := corpus.ValidateManifest("space/v1", instance)
	if err != nil {
		t.Fatalf("ValidateManifest(%q): %v", id, err)
	}
	return violations
}

// TestSpaceSchemaPatternAcceptsEveryIDInUse is AC-1050.10's positive half:
// the pattern must accept every space id actually in use across the repo
// (this phase's sweep) — a real space (getvisa), the live-e2e ids
// (livee2e, live-e2e-space), the spacefixture default (fixture-space), and
// the scaffolding sentinel itself (REPLACE_WITH_SPACE_ID, carved into the
// pattern rather than the template being renamed — see this phase's
// reported deviation and the pattern's own `description`).
func TestSpaceSchemaPatternAcceptsEveryIDInUse(t *testing.T) {
	t.Parallel()
	for _, id := range []string{
		"getvisa",
		"a2a",
		"livee2e",
		"live-e2e-space",
		"fixture-space",
		"REPLACE_WITH_SPACE_ID",
	} {
		if v := spaceManifestInstanceViolations(t, id); len(v) != 0 {
			t.Errorf("id %q: expected the schema to ACCEPT it, got violations: %+v", id, v)
		}
	}
}

// TestSpaceSchemaPatternRejectsInvalidShapes is AC-1050.10's negative half:
// the pattern must reject an id carrying underscores, uppercase, or a
// leading/trailing hyphen — exactly the shapes that would let
// CredentialEnvVar's `-`->`_` mapping collide (P41 variant c).
func TestSpaceSchemaPatternRejectsInvalidShapes(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"My_Space", "my_space", "-lead", "trail-", "Up"} {
		if v := spaceManifestInstanceViolations(t, id); len(v) == 0 {
			t.Errorf("id %q: expected the schema to REJECT it, got no violations", id)
		}
	}
}
