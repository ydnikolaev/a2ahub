package space

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestCloneOrFetchFreshClone(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("CloneOrFetch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "space.yaml")); err != nil {
		t.Fatalf("expected cloned space.yaml: %v", err)
	}
}

func TestCloneOrFetchRerunIsFetchNotReClone(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := filepath.Join(t.TempDir(), "mirror")

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("first CloneOrFetch: %v", err)
	}
	// Mark the working tree with a sentinel file a re-clone would wipe;
	// a fetch must leave it alone.
	sentinel := filepath.Join(dest, "untracked-local-sentinel")
	if err := os.WriteFile(sentinel, []byte("still here"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	if err := CloneOrFetch(context.Background(), dest, fx.RemoteURL()); err != nil {
		t.Fatalf("second CloneOrFetch: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("expected sentinel to survive a fetch-not-reclone rerun: %v", err)
	}
}

func TestCloneOrFetchNonGitNonEmptyTargetRejected(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "unrelated.txt"), []byte("pre-existing"), 0o644); err != nil {
		t.Fatalf("seed unrelated file: %v", err)
	}

	err := CloneOrFetch(context.Background(), dest, fx.RemoteURL())
	if !errors.Is(err, ErrNonGitTarget) {
		t.Fatalf("CloneOrFetch error = %v, want ErrNonGitTarget", err)
	}
}
