package livee2e

import (
	"errors"
	"fmt"
	"strings"
)

// HostSeam is where a run's host actually lives — the REST/GraphQL root a
// scenario's host.Host calls hit, the git remote `a2a init --space`/`git
// push` write to, and the base a human-facing PR link is built from. Before
// this file, these three strings were rebuilt inline at eleven call sites
// (defaultAPIRoot baked into WaitForRequiredCheck and four
// host.NewGitHubHost(nil, defaultAPIRoot) calls; "https://github.com/" +
// h.Org + "/" + h.Repo[+".git"] reassembled seven more times plus once more
// for the PR link) — one fact restated eleven times is eleven places that
// can each independently drift the moment a second tier (a local host) needs
// a different answer.
//
// UNTAGGED on purpose, matching completeness.go's, branchmatch.go's and
// familyfilter.go's own precedent: the RULE — how a live seam's three
// strings derive from an org and a repo name, and what makes a seam
// well-formed — is pure and has no network dependency, so it belongs where
// the plain `go test ./internal/livee2e/` suite (the one `make check` runs)
// covers it, not behind the livee2e build tag where only a 40-minute live
// run would ever exercise it. The network-touching adapters that CONSUME a
// HostSeam (WaitForRequiredCheck, the scenario families) stay tagged, same
// as completeness.go's foldKindSet stays untagged while the gate that calls
// it does not.
type HostSeam struct {
	// APIRoot is the REST/GraphQL root scenario code hands to
	// host.NewGitHubHost — GitHub's own for the live seam.
	APIRoot string
	// RemoteURL is the space repo's git remote — what `git push`/`git
	// clone` and host.GitHubHost.PushBranch write to. Carries the ".git"
	// suffix a git remote takes; CloneURL is this field's own accessor.
	RemoteURL string
	// WebRoot is the base for human-facing links (the PR URLs
	// PullURL builds, and what `a2a init --space` is handed) — the same
	// host as RemoteURL, but without the ".git" a browser URL never
	// carries.
	WebRoot string
}

// githubWebRootPrefix is the one place "https://github.com/" is spelled, so
// NewLiveHostSeam and Validate's real-GitHub check can never independently
// drift from each other the way MirrorDir's own doc describes as the scar of
// a rule with two homes.
const githubWebRootPrefix = "https://github.com/"

// NewLiveHostSeam builds the seam a live run has always used, reproducing
// today's three strings EXACTLY: defaultAPIRoot (prober_github.go) for
// APIRoot, "https://github.com/<org>/<repo>.git" for RemoteURL, and
// "https://github.com/<org>/<repo>" (no ".git") for WebRoot.
func NewLiveHostSeam(org, repo string) HostSeam {
	webRoot := githubWebRootPrefix + org + "/" + repo
	return HostSeam{
		APIRoot:   defaultAPIRoot,
		RemoteURL: webRoot + ".git",
		WebRoot:   webRoot,
	}
}

// CloneURL is RemoteURL's own accessor — the URL `git push`/`git clone`
// take, WITH the ".git" suffix. Named separately from the field itself so a
// caller reads the distinction from CloneURL()'s and PullURL()'s matching
// method shapes, rather than having to remember which of two similarly-named
// fields carries the suffix.
func (s HostSeam) CloneURL() string {
	return s.RemoteURL
}

// PullURL reproduces scenarios_operational_confidence_live.go's own
// "https://github.com/%s/%s/pull/%d" format exactly, built off WebRoot so it
// can never silently diverge from the same host CloneURL and APIRoot name.
func (s HostSeam) PullURL(number int) string {
	return fmt.Sprintf("%s/pull/%d", s.WebRoot, number)
}

// IsRealGitHub reports whether the seam points at real GitHub — the check a
// later wave's local-tier assertion needs to prove a run never silently
// reached the wrong host. A seam is "real GitHub" exactly when ALL THREE
// fields name it; a half-real seam is caught by Validate below, never by
// this predicate alone.
func (s HostSeam) IsRealGitHub() bool {
	return s.apiIsGitHub() && s.remoteIsGitHub() && s.webIsGitHub()
}

func (s HostSeam) apiIsGitHub() bool {
	return s.APIRoot == defaultAPIRoot
}

func (s HostSeam) remoteIsGitHub() bool {
	return strings.HasPrefix(s.RemoteURL, githubWebRootPrefix)
}

func (s HostSeam) webIsGitHub() bool {
	return strings.HasPrefix(s.WebRoot, githubWebRootPrefix)
}

// ErrHostSeamIncomplete is returned when a seam field that must be set is
// empty.
var ErrHostSeamIncomplete = errors.New("livee2e: host seam field is empty")

// ErrHostSeamMixedHost is returned when a seam's three fields do not agree
// on whether they point at real GitHub — a caller cannot conclude the run
// stayed off (or on) real GitHub if only some of the fields say so.
var ErrHostSeamMixedHost = errors.New("livee2e: host seam mixes real-GitHub and non-GitHub values")

// Validate returns an error for a half-configured seam: any field left
// empty, or a mix of real-GitHub and non-GitHub values across the three
// fields. This is what a later wave's local tier uses to assert a run cannot
// reach the wrong host by construction, so both error cases name the
// offending field(s) rather than a bare "invalid seam".
func (s HostSeam) Validate() error {
	var empty []string
	if s.APIRoot == "" {
		empty = append(empty, "APIRoot")
	}
	if s.RemoteURL == "" {
		empty = append(empty, "RemoteURL")
	}
	if s.WebRoot == "" {
		empty = append(empty, "WebRoot")
	}
	if len(empty) > 0 {
		return fmt.Errorf("%w: %s", ErrHostSeamIncomplete, strings.Join(empty, ", "))
	}

	api, remote, web := s.apiIsGitHub(), s.remoteIsGitHub(), s.webIsGitHub()
	if api != remote || api != web {
		return fmt.Errorf("%w: APIRoot=%q (real GitHub: %t) RemoteURL=%q (real GitHub: %t) WebRoot=%q (real GitHub: %t)",
			ErrHostSeamMixedHost, s.APIRoot, api, s.RemoteURL, remote, s.WebRoot, web)
	}
	return nil
}
