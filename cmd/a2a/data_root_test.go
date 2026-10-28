package main

import (
	"path/filepath"
	"testing"
)

// The machine data root is defined once. These pin its spelling and prove the
// notification component root is built on it rather than on a second copy of
// the literal.
func TestA2ADataRootIsUnderTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := a2aDataRoot()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "share", "a2a"); got != want {
		t.Fatalf("a2aDataRoot() = %q, want %q", got, want)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("a2aDataRoot() = %q, want an absolute path", got)
	}
}

func TestNotificationComponentRootStaysUnderTheDataRoot(t *testing.T) {
	t.Setenv("A2A_COMPONENTS_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, err := a2aDataRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := notificationComponentRoot(home, "1.2.3"), filepath.Join(root, "components", "1.2.3"); got != want {
		t.Fatalf("notificationComponentRoot = %q, want %q (the data root's components dir, byte-identical to before)", got, want)
	}
	// The developer override is untouched by the data-root move.
	override := filepath.Join(t.TempDir(), "unsigned")
	t.Setenv("A2A_COMPONENTS_DIR", override)
	if got := notificationComponentRoot(home, "dev"); got != filepath.Clean(override) {
		t.Fatalf("dev root = %q, want the override %q", got, override)
	}
}
