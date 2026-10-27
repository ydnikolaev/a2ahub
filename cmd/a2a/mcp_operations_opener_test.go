package main

import (
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
