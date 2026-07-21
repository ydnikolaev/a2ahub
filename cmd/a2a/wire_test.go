package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestParseGitHubRepo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		url, owner, name string
		wantErr          bool
	}{
		{"https://github.com/getvisa/getvisa", "getvisa", "getvisa", false},
		{"https://github.com/getvisa/getvisa.git", "getvisa", "getvisa", false},
		{"git@github.com:r22d222/a2a.git", "r22d222", "a2a", false},
		{"https://github.com/org/repo", "org", "repo", false},
		{"not-a-url", "", "", true},
		{"https://github.com/onlyowner", "", "", true},
	}
	for _, tc := range cases {
		owner, name, err := parseGitHubRepo(tc.url)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseGitHubRepo(%q): want error, got %s/%s", tc.url, owner, name)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGitHubRepo(%q): unexpected error %v", tc.url, err)
			continue
		}
		if owner != tc.owner || name != tc.name {
			t.Errorf("parseGitHubRepo(%q) = %s/%s, want %s/%s", tc.url, owner, name, tc.owner, tc.name)
		}
	}
}

func TestFindSpace(t *testing.T) {
	t.Parallel()
	cfg := space.ProjectConfig{
		System: "axon",
		Spaces: []space.Ref{
			{ID: "getvisa", RepoURL: "https://github.com/getvisa/getvisa"},
			{ID: "other", RepoURL: "https://github.com/o/other"},
		},
	}
	if r, ok := findSpace(cfg, "getvisa"); !ok || r.RepoURL != "https://github.com/getvisa/getvisa" {
		t.Errorf("findSpace(getvisa) = %+v, %v", r, ok)
	}
	if _, ok := findSpace(cfg, "nope"); ok {
		t.Errorf("findSpace(nope): want not found")
	}
}

func TestReadEnvelopeFacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "XQ-axon-20260721-abcd.md")
	if err := os.WriteFile(good, []byte("---\nschema: envelope/v1\nid: XQ-axon-20260721-abcd\nfrom: axon\nspace: getvisa\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := readEnvelopeFacts(good)
	if err != nil {
		t.Fatalf("readEnvelopeFacts: %v", err)
	}
	if f.from != "axon" || f.space != "getvisa" {
		t.Errorf("readEnvelopeFacts = %+v, want from=axon space=getvisa", f)
	}

	// Missing `from`/`space` is an error, not a silent empty.
	bad := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(bad, []byte("---\nschema: envelope/v1\nid: x\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvelopeFacts(bad); err == nil {
		t.Errorf("readEnvelopeFacts(missing fields): want error")
	}
}

// TestBuildStoreConfigTolerance guards the wave-4 MED-4 fix: buildStore
// TOLERATES an absent config (pre-onboarding, CC-092) but SURFACES a
// malformed one (bad YAML / bad credential ref) rather than silently
// degrading to zero connected spaces.
func TestBuildStoreConfigTolerance(t *testing.T) {
	t.Parallel()

	// pathsIn builds a paths set rooted at dir with the standard layout.
	pathsIn := func(dir string) paths {
		return paths{
			projectConfig: filepath.Join(dir, ".a2a", "config.yaml"),
			machineConfig: filepath.Join(dir, "machine.yaml"),
			projectRoot:   dir,
			staging:       filepath.Join(dir, ".a2a", "staging"),
		}
	}

	t.Run("absent config is tolerated (empty store, no error)", func(t *testing.T) {
		t.Parallel()
		store, err := buildStore(pathsIn(t.TempDir()))
		if err != nil {
			t.Fatalf("buildStore(absent): unexpected error %v", err)
		}
		if store == nil {
			t.Fatal("buildStore(absent): want non-nil store over zero mirrors")
		}
	})

	t.Run("malformed project config surfaces loudly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".a2a"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Unterminated flow sequence — a real YAML parse error, not an
		// absent file.
		if err := os.WriteFile(filepath.Join(dir, ".a2a", "config.yaml"), []byte("system: [axon\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := buildStore(pathsIn(dir)); err == nil {
			t.Error("buildStore(malformed project config): want error, got nil")
		}
	})

	t.Run("malformed machine config surfaces loudly", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Absent project config (tolerated) but a machine config carrying a
		// credential reference that is neither env: nor cmd: — LoadMachineConfig
		// rejects it at load, and buildStore must NOT swallow that.
		if err := os.WriteFile(filepath.Join(dir, "machine.yaml"), []byte("credentials:\n  getvisa: \"literal-secret-not-a-ref\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := buildStore(pathsIn(dir)); err == nil {
			t.Error("buildStore(malformed machine config): want error, got nil")
		}
	})
}
