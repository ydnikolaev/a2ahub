package spacefixture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSeedsSpaceTree(t *testing.T) {
	t.Parallel()

	fx := New(t, "axon", "seomatrix")

	if _, err := os.Stat(filepath.Join(fx.OriginDir, "HEAD")); err != nil {
		t.Fatalf("origin bare repo missing HEAD: %v", err)
	}

	for _, sys := range []string{"axon", "seomatrix"} {
		clone := fx.Clone(sys)
		for _, rel := range []string{
			"space.yaml",
			filepath.Join(sys, "provides"),
			filepath.Join(sys, "requires"),
			filepath.Join(sys, "consumes.yaml"),
			filepath.Join(sys, "exchanges"),
			filepath.Join(sys, "events"),
			filepath.Join(sys, "docs"),
			"decisions",
			"vendored",
		} {
			if _, err := os.Stat(filepath.Join(clone, rel)); err != nil {
				t.Errorf("system %s: expected seeded path %s: %v", sys, rel, err)
			}
		}
	}
}

func TestRemoteURLIsOriginDir(t *testing.T) {
	t.Parallel()

	fx := New(t, "axon")
	if fx.RemoteURL() != fx.OriginDir {
		t.Fatalf("RemoteURL() = %q, want %q", fx.RemoteURL(), fx.OriginDir)
	}
}

func TestHeadSHAMatchesAcrossClones(t *testing.T) {
	t.Parallel()

	fx := New(t, "axon", "seomatrix")
	a := fx.HeadSHA(fx.Clone("axon"), "main")
	b := fx.HeadSHA(fx.Clone("seomatrix"), "main")
	if a == "" || a != b {
		t.Fatalf("expected identical non-empty HEAD sha across clones, got %q and %q", a, b)
	}
}
