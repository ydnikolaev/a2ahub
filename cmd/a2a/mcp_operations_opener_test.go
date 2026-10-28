package main

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
)

// countingOpener wraps the production opener and records every URL it was
// asked to open.
type countingOpener struct {
	mu     sync.Mutex
	opened []string
}

func (c *countingOpener) open() host.Opener {
	inner := host.NewOpener(host.OpenConfig{GitHubAPIBase: "http://opener-test.invalid"})
	return func(raw string) (host.Host, host.Repo, error) {
		c.mu.Lock()
		c.opened = append(c.opened, raw)
		c.mu.Unlock()
		return inner(raw)
	}
}

func stubMCPActor(mcp.ActorInput) (template.Actor, error) { return template.Actor{}, nil }

// TestMCPOperations_OpenerPerSpace proves the MCP contract- and data-operations
// factories open one host per connected space (inside the per-space loop), not
// one per process, and never open one when no space is connected.
func TestMCPOperations_OpenerPerSpace(t *testing.T) {
	t.Parallel()

	two := space.ProjectConfig{System: "axon", Spaces: []space.Ref{
		{ID: "alpha", RepoURL: "https://github.com/acme/alpha"},
		{ID: "beta", RepoURL: "https://github.com/acme/beta"},
	}}
	for _, tc := range []struct {
		name  string
		build func(p paths, cfg space.ProjectConfig, open host.Opener) error
	}{
		{"contract", func(p paths, cfg space.ProjectConfig, open host.Opener) error {
			_, err := newMCPContractOperationsFactory(p, cfg, space.MachineConfig{}, open)(stubMCPActor)
			return err
		}},
		{"data", func(p paths, cfg space.ProjectConfig, open host.Opener) error {
			_, err := newMCPDataOperationsFactory(p, cfg, space.MachineConfig{}, open)(stubMCPActor)
			return err
		}},
	} {
		t.Run(tc.name+"/one open per space", func(t *testing.T) {
			t.Parallel()
			p := paths{projectRoot: t.TempDir(), staging: t.TempDir()}
			var counter countingOpener
			if err := tc.build(p, two, counter.open()); err != nil {
				t.Fatalf("factory: %v", err)
			}
			want := []string{"https://github.com/acme/alpha", "https://github.com/acme/beta"}
			if len(counter.opened) != len(want) || counter.opened[0] != want[0] || counter.opened[1] != want[1] {
				t.Fatalf("opened %v, want one open per space in order %v", counter.opened, want)
			}
		})
		t.Run(tc.name+"/zero spaces never opens", func(t *testing.T) {
			t.Parallel()
			p := paths{projectRoot: t.TempDir(), staging: t.TempDir()}
			var counter countingOpener
			if err := tc.build(p, space.ProjectConfig{System: "axon"}, counter.open()); err != nil {
				t.Fatalf("factory: %v", err)
			}
			if len(counter.opened) != 0 {
				t.Fatalf("opener called for %v with no connected space", counter.opened)
			}
		})
	}
}

// TestMCPOperations_AMovedLocalSpaceCostsOnlyItself proves a space whose kind
// the opener cannot tell (a local space whose directory was moved) does not
// fail the whole factory, which would stop `a2a mcp` from starting and take
// every other space and the read tools with it. The space answers with its
// open error when a request names it, or when it is the only space; every
// other space routes as before.
func TestMCPOperations_AMovedLocalSpaceCostsOnlyItself(t *testing.T) {
	t.Parallel()

	moved := filepath.Join(t.TempDir(), "moved.git")
	cfg := space.ProjectConfig{System: "axon", Spaces: []space.Ref{
		{ID: "alpha", RepoURL: "https://github.com/acme/alpha"},
		{ID: "lone", RepoURL: moved},
	}}
	only := space.ProjectConfig{System: "axon", Spaces: []space.Ref{{ID: "lone", RepoURL: moved}}}
	open := host.NewOpener(host.OpenConfig{GitHubAPIBase: "http://opener-test.invalid"})
	for _, tc := range []struct {
		name  string
		route func(t *testing.T, cfg space.ProjectConfig) func(spaceID string) (bool, error)
	}{
		{"contract", func(t *testing.T, cfg space.ProjectConfig) func(string) (bool, error) {
			p := paths{projectRoot: t.TempDir(), staging: t.TempDir()}
			ops, err := newMCPContractOperationsFactory(p, cfg, space.MachineConfig{}, open)(stubMCPActor)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			router := ops.Publication.(mcpContractP6Router)
			return func(id string) (bool, error) { core, err := router.coreFor(id); return core != nil, err }
		}},
		{"data", func(t *testing.T, cfg space.ProjectConfig) func(string) (bool, error) {
			p := paths{projectRoot: t.TempDir(), staging: t.TempDir()}
			deps, err := newMCPDataOperationsFactory(p, cfg, space.MachineConfig{}, open)(stubMCPActor)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			router := deps.Operations.(mcpDataRouter)
			return func(id string) (bool, error) { core, err := router.coreFor(id); return core != nil, err }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			route := tc.route(t, cfg)
			if ok, err := route("alpha"); err != nil || !ok {
				t.Fatalf("alpha routes to (%v, %v), want its core", ok, err)
			}
			if _, err := route("lone"); !errors.Is(err, host.ErrSpacePathMissing) {
				t.Fatalf("lone routes to %v, want its open error (ErrSpacePathMissing)", err)
			}
			if _, err := route(""); err == nil || !strings.Contains(err.Error(), "space is required when multiple spaces are connected") {
				t.Fatalf("empty route over two configured spaces = %v, want the ambiguity refusal", err)
			}
			single := tc.route(t, only)
			if _, err := single(""); !errors.Is(err, host.ErrSpacePathMissing) {
				t.Fatalf("empty route over the one moved space = %v, want its open error", err)
			}
		})
	}
}
