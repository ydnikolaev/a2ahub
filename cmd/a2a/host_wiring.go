package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contractwiring"
	"github.com/ydnikolaev/a2ahub/internal/host"
)

// newHostOpenConfig is the one host.OpenConfig a process hands to the host
// factory. internal/host never reads the environment, so everything it needs at
// construction is resolved here: the HTTP client and the GitHub API base
// (A2A_GITHUB_API, githubAPIBase).
//
// The API base is captured when newHostOpenerFor runs, not at process start:
// per command for CLI verbs, once per MCP server. An in-process test that sets
// A2A_GITHUB_API after startup but before the command is built must therefore
// reach the host it configured, exactly as the per-site githubAPIBase() reads
// this replaced did.
func newHostOpenConfig() host.OpenConfig {
	return host.OpenConfig{HTTPClient: http.DefaultClient, GitHubAPIBase: githubAPIBase()}
}

// newHostOpener builds the production host.Opener for a caller that never
// lands a write on a local space: the doctor (it only reads a local space's
// capabilities and inspection) and the feedback hub (GitHub by product). A
// marked local path opened through it is refused at OpenPR, not landed under an
// empty author. It is newHostOpenerFor("").
func newHostOpener() host.Opener {
	return newHostOpenerFor("")
}

// newHostOpenerFor builds the production host.Opener over newHostOpenConfig,
// with the local-space land check and the caller's own system. It is the single
// place a space URL becomes a host in cmd/a2a: every verb, the doctor and the
// MCP server reach a host through it (forbidigo refuses a direct
// host.NewGitHubHost elsewhere).
//
// system is the caller's configured system, the project config's `system`. A
// local land is attributed to "local:<system>", the author the validator maps
// through space.yaml's owners. An empty system leaves LandAuthor empty, and a
// local OpenPR then refuses before it reads or writes anything.
//
// The land check is lazy (newLazyLandCheck): building the validation engine
// costs a schema load, and a GitHub open, the avatar refresher and notify must
// not pay it or fail on it.
func newHostOpenerFor(system string) host.Opener {
	cfg := newHostOpenConfig()
	cfg.LandCheck = newLazyLandCheck()
	if system != "" {
		cfg.LandAuthor = "local:" + system
	}
	return host.NewOpener(cfg)
}

// newLazyLandCheck is the host.LandCheckFunc a local space's land runs. It
// builds the validation engine on its first call, once per opener, and shares
// the result between calls (an opener serves concurrent writes in one MCP
// server). An engine error is returned as the check's own error, which the
// local host reports as "could not judge", never as a pass.
func newLazyLandCheck() host.LandCheckFunc {
	engine := sync.OnceValues(newEngine)
	return func(ctx context.Context, candidate host.LandCandidate) (host.LandVerdict, error) {
		e, err := engine()
		if err != nil {
			return host.LandVerdict{}, fmt.Errorf("build the validation engine for the land check: %w", err)
		}
		return cli.NewLandCheck(e)(ctx, candidate)
	}
}

// openContractHost opens the host for raw and asserts it satisfies
// contractwiring.Host, the narrow capability the contract assembly needs. A
// host that does not is a named error, never a panic.
func openContractHost(open host.Opener, raw string) (contractwiring.Host, host.Repo, error) {
	h, repo, err := open(raw)
	if err != nil {
		return nil, host.Repo{}, err
	}
	ch, ok := h.(contractwiring.Host)
	if !ok {
		return nil, host.Repo{}, fmt.Errorf("contract operations: the host for %q (%T) does not provide the remote-tree readers contract publication needs", raw, h)
	}
	return ch, repo, nil
}
