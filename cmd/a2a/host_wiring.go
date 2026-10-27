package main

import (
	"fmt"
	"net/http"

	"github.com/ydnikolaev/a2ahub/internal/contractwiring"
	"github.com/ydnikolaev/a2ahub/internal/host"
)

// newHostOpenConfig is the one host.OpenConfig a process hands to the host
// factory. internal/host never reads the environment, so everything it needs at
// construction is resolved here: the HTTP client and the GitHub API base
// (A2A_GITHUB_API, githubAPIBase).
//
// The API base is captured when newHostOpener() runs, not at process start:
// per command for CLI verbs, once per MCP server. An in-process test that sets
// A2A_GITHUB_API after startup but before the command is built must therefore
// reach the host it configured, exactly as the per-site githubAPIBase() reads
// this replaced did.
func newHostOpenConfig() host.OpenConfig {
	return host.OpenConfig{HTTPClient: http.DefaultClient, GitHubAPIBase: githubAPIBase()}
}

// newHostOpener builds the production host.Opener over newHostOpenConfig. It is
// the single place a space URL becomes a host in cmd/a2a: every verb, the doctor
// and the MCP server reach a host through it (forbidigo refuses a direct
// host.NewGitHubHost elsewhere).
func newHostOpener() host.Opener {
	return host.NewOpener(newHostOpenConfig())
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
