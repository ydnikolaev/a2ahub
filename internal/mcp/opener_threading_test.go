package mcp

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestBuildWriteDepsThreadsTheInjectedOpener pins that the host.Opener cmd/a2a
// injects is the one the write path opens a space's host through: a counting
// opener wrapping the production one must be called for the connected space.
// Replacing the threaded `open` with defaultHostOpener() inside the write-dep
// builders would leave the counter at zero.
//
// reason: mutates process env through the production credential seam, so
// t.Parallel() is unavailable (AGENTS.md testing rails).
func TestBuildWriteDepsThreadsTheInjectedOpener(t *testing.T) {
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")

	fx := spacefixture.New(t, "beta")
	fixValidManifest(t, fx, "beta")
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{{ID: "space-one", RepoURL: fx.RemoteURL()}}}
	machine := space.MachineConfig{Credentials: map[string]string{"space-one": "env:A2A_TOKEN_SPACE_ONE"}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	var calls atomic.Int32
	var gotURL atomic.Value
	prod := defaultHostOpener()
	counting := host.Opener(func(raw string) (host.Host, host.Repo, error) {
		calls.Add(1)
		gotURL.Store(raw)
		return prod(raw)
	})

	if _, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, testBuild("0.0.1-test"), counting); err != nil {
		t.Fatalf("buildWriteDeps: %v", err)
	}
	if calls.Load() == 0 {
		t.Fatal("the injected opener was never called: the write path must open a space's host through it, not a package default")
	}
	if got, _ := gotURL.Load().(string); got != fx.RemoteURL() {
		t.Errorf("opener asked for %q, want the connected space's repo_url %q", got, fx.RemoteURL())
	}
}
