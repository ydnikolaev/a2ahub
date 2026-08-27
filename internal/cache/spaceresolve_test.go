package cache

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// seedArtifact writes an "<id>.md" file at relDir (a directory relative to
// root) — the on-disk shape mirrorHoldsArtifact's walk looks for.
func seedArtifact(t *testing.T, root, relDir, id string) {
	t.Helper()
	dir := filepath.Join(root, relDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mirrorDirByID resolves each space.Ref to the matching entry in byID — a
// stand-in for space.ResolveMirrorLocation that needs neither a project
// root nor a machine config.
func mirrorDirByID(byID map[string]string) func(space.Ref) string {
	return func(ref space.Ref) string {
		return byID[ref.ID]
	}
}

// TestResolveArtifactSpaceFindsSecondSpace is the exact defect REF-025
// exists for: an id held by the SECOND connected space must resolve to
// THAT space, never silently to the first.
func TestResolveArtifactSpaceFindsSecondSpace(t *testing.T) {
	root := t.TempDir()
	oneDir := filepath.Join(root, "one")
	twoDir := filepath.Join(root, "two")
	if err := os.MkdirAll(oneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	seedArtifact(t, twoDir, "beta/exchanges", "XQ-beta-20260827-aaaa")

	refs := []space.Ref{{ID: "space-one"}, {ID: "space-two"}}
	ref, err := ResolveArtifactSpace(refs, mirrorDirByID(map[string]string{
		"space-one": oneDir, "space-two": twoDir,
	}), "XQ-beta-20260827-aaaa")
	if err != nil {
		t.Fatalf("ResolveArtifactSpace: %v", err)
	}
	if ref.ID != "space-two" {
		t.Fatalf("ResolveArtifactSpace resolved %q, want space-two", ref.ID)
	}
}

// TestResolveArtifactSpaceNotFoundNamesIDAndSearched is REF-025's own
// contract: an id in NEITHER connected space refuses by name, naming the
// id AND every space searched — never a bare "artifact not found".
func TestResolveArtifactSpaceNotFoundNamesIDAndSearched(t *testing.T) {
	root := t.TempDir()
	oneDir := filepath.Join(root, "one")
	twoDir := filepath.Join(root, "two")
	if err := os.MkdirAll(oneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(twoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	refs := []space.Ref{{ID: "space-one"}, {ID: "space-two"}}
	_, err := ResolveArtifactSpace(refs, mirrorDirByID(map[string]string{
		"space-one": oneDir, "space-two": twoDir,
	}), "XQ-beta-20260827-zzzz")
	if err == nil {
		t.Fatal("expected a not-found refusal")
	}
	var notFound *ArtifactSpaceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v (%T), want *ArtifactSpaceNotFoundError", err, err)
	}
	if notFound.ID != "XQ-beta-20260827-zzzz" {
		t.Fatalf("notFound.ID = %q, want XQ-beta-20260827-zzzz", notFound.ID)
	}
	wantSearched := []string{"space-one", "space-two"}
	if len(notFound.Searched) != len(wantSearched) {
		t.Fatalf("notFound.Searched = %v, want %v", notFound.Searched, wantSearched)
	}
	for i, want := range wantSearched {
		if notFound.Searched[i] != want {
			t.Fatalf("notFound.Searched = %v, want %v", notFound.Searched, wantSearched)
		}
	}

	msg := err.Error()
	for _, want := range []string{"REF-025", "XQ-beta-20260827-zzzz", "space-one", "space-two"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message %q does not name %q", msg, want)
		}
	}
}

// TestResolveArtifactSpaceSkipsGitObjectStore proves the walk does not
// treat a bare `.git` object store's own contents as artifact files — a
// git object pack occasionally contains a byte sequence that happens to
// look like a filename to a naive scan, and walking it also wastes work
// that grows with mirror history for no reason (both former copies of this
// rule documented the same skip).
func TestResolveArtifactSpaceSkipsGitObjectStore(t *testing.T) {
	root := t.TempDir()
	mirrorDir := filepath.Join(root, "mirror")
	// A file that would match "<id>.md", planted UNDER .git — never a real
	// artifact location, and must not be found.
	seedArtifact(t, mirrorDir, ".git/objects/aa", "XQ-beta-20260827-aaaa")

	refs := []space.Ref{{ID: "only-space"}}
	_, err := ResolveArtifactSpace(refs, mirrorDirByID(map[string]string{
		"only-space": mirrorDir,
	}), "XQ-beta-20260827-aaaa")
	var notFound *ArtifactSpaceNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want a not-found refusal — the .git-planted file must not count", err)
	}
}
