package release

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSwap_AtomicReplace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	target := filepath.Join(dir, "a2a")
	if err := os.WriteFile(target, []byte("old binary"), 0o644); err != nil {
		t.Fatalf("seed target: %v", err)
	}

	newBinary := filepath.Join(dir, "a2a.new")
	if err := os.WriteFile(newBinary, []byte("new binary"), 0o644); err != nil {
		t.Fatalf("seed newBinary: %v", err)
	}

	if err := Swap(target, newBinary); err != nil {
		t.Fatalf("Swap: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile(target): %v", err)
	}
	if string(got) != "new binary" {
		t.Fatalf("target content = %q, want %q", got, "new binary")
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat(target): %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("target mode = %v, want 0755", info.Mode().Perm())
	}

	if _, err := os.Stat(newBinary); !os.IsNotExist(err) {
		t.Fatalf("newBinary still exists after rename: err=%v", err)
	}
}

// TestSwap_UnwritableTargetDirFails exercises Swap's rename-failure branch
// without depending on uid or platform permission semantics (a t.Geteuid==0
// check would mask the branch under a root CI container, which is exactly
// the kind of environment-dependent skip this repo forbids): a target whose
// PARENT directory does not exist makes os.Rename fail ENOENT for every
// caller, root included, so ErrSwapFailed is exercised deterministically.
func TestSwap_UnwritableTargetDirFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	newBinary := filepath.Join(dir, "a2a.new")
	if err := os.WriteFile(newBinary, []byte("new binary"), 0o644); err != nil {
		t.Fatalf("seed newBinary: %v", err)
	}

	target := filepath.Join(dir, "nope", "a2a") // parent dir absent
	err := Swap(target, newBinary)
	if !errors.Is(err, ErrSwapFailed) {
		t.Fatalf("Swap() error = %v, want ErrSwapFailed", err)
	}
}
