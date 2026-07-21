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
