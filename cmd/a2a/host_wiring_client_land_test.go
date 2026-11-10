package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

const clientLandTestURL = "https://github.com/acme/space"

func clientLandTestPaths(t *testing.T, repoURL, manifest string) paths {
	t.Helper()
	root := t.TempDir()
	p := paths{projectConfig: filepath.Join(root, ".a2a", "config.yaml"), machineConfig: filepath.Join(root, "absent.yaml"), projectRoot: root}
	if err := os.MkdirAll(filepath.Dir(p.projectConfig), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	config := "system: axon\nspaces:\n  - id: acme\n    repo_url: " + repoURL + "\n"
	if err := os.WriteFile(p.projectConfig, []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if manifest != "" {
		mirror := space.ResolveMirrorLocation(root, space.Ref{ID: "acme", RepoURL: repoURL}, space.MachineConfig{})
		if err := os.MkdirAll(mirror, 0o755); err != nil {
			t.Fatalf("mkdir mirror: %v", err)
		}
		if err := os.WriteFile(filepath.Join(mirror, "space.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
	}
	return p
}

func TestHostOpenerChoosesTheHostFromTheSpaceManifest(t *testing.T) {
	t.Parallel()
	const base = "schema: space/v1\nspace: acme\nmin_binary_version: 0.1.0\nparticipants: []\n"
	tests := []struct {
		name       string
		repoURL    string
		manifest   string
		wantClient bool
	}{
		{name: "validation local", repoURL: clientLandTestURL, manifest: base + "validation: local\n", wantClient: true},
		{name: "validation actions is GitHub", repoURL: clientLandTestURL, manifest: base + "validation: actions\n"},
		{name: "no key is GitHub", repoURL: clientLandTestURL, manifest: base},
		{name: "no mirror yet is GitHub", repoURL: clientLandTestURL},
		{name: "an unreadable manifest is GitHub", repoURL: clientLandTestURL, manifest: ":\n  - ["},
		{name: "another connected space does not lend its mode", repoURL: "https://github.com/acme/other", manifest: base + "validation: local\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, repo, err := newHostOpenerAt(clientLandTestPaths(t, tt.repoURL, tt.manifest), "axon")(clientLandTestURL)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			if repo != (host.Repo{Owner: "acme", Name: "space"}) {
				t.Errorf("repo = %+v", repo)
			}
			if _, isClient := h.(*host.ClientLandHost); isClient != tt.wantClient {
				t.Fatalf("host = %T, wantClient = %v", h, tt.wantClient)
			}
			if got := h.Capabilities().LandsSynchronously; got != tt.wantClient {
				t.Errorf("LandsSynchronously = %v, want %v", got, tt.wantClient)
			}
			if h.Capabilities().OnThisMachine {
				t.Error("a GitHub space declared OnThisMachine")
			}
		})
	}

	t.Run("no project config is GitHub", func(t *testing.T) {
		t.Parallel()
		h, _, err := newHostOpenerAt(paths{}, "")(clientLandTestURL)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if _, isClient := h.(*host.ClientLandHost); isClient {
			t.Fatalf("host = %T", h)
		}
	})
}
