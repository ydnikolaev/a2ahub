package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
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
