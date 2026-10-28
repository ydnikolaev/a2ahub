package livee2e

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
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
	// Kind says which shape of host the seam fronts. The zero value is the
	// GitHub shape, so every seam literal written before the kind existed
	// keeps meaning exactly what it meant.
	Kind HostKind
	// APIRoot is the REST/GraphQL root scenario code hands to
	// host.NewGitHubHost — GitHub's own for the live seam. Empty, and
	// required to be, for a bare-repository seam: that host has no API.
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

// HostKind is the shape of host a HostSeam fronts. It is a validation shape,
// not the source of what the host can do: what a host declares about itself is
// read from the host the product opens for the seam (OpenHost), never from
// this value, so a third kind is a new constant and a new Validate branch, and
// nothing that asks a capability changes.
type HostKind int

const (
	// HostKindGitHub is a GitHub API root fronting the git remote: real
	// GitHub for the live seam, a loopback testkit/fakegithub for the logic
	// seam (NewLocalHostSeam). It is the zero value.
	HostKindGitHub HostKind = iota
	// HostKindBareRepo is a bare git repository on this machine's disk, made
	// by `a2a space init --local`, with no API root at all. It is deliberately
	// NOT named "local": NewLocalHostSeam already means the loopback fake
	// GitHub, and reusing the word would make the next reader guess.
	HostKindBareRepo
)

// String names the kind for an error message.
func (k HostKind) String() string {
	switch k {
	case HostKindGitHub:
		return "github"
	case HostKindBareRepo:
		return "bare-repo"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
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

// newBareRepoHostSeam builds the seam of a bare repository at originDir: the
// directory is both the git remote and the web root (a local space has no
// browser-facing host to tell the two apart), and there is no API root.
func newBareRepoHostSeam(originDir string) HostSeam {
	return HostSeam{Kind: HostKindBareRepo, RemoteURL: originDir, WebRoot: originDir}
}

// OpenHost opens the host the product itself would open for this seam: the
// one constructor every space-addressed consumer uses (host.Open), fed the
// seam's own remote and API root. What the seam's host is, and what it
// declares, is therefore the product's answer for the same address, never a
// second opinion kept here. Opening makes no network call and writes nothing.
//
// The land check handed to a local host is a refusal: a seam only ever READS
// through the host it opens here (its capabilities, the record of a landed
// write), and a write that reached this host would be a bug in the harness,
// not a land to judge.
func (s HostSeam) OpenHost() (host.Host, error) {
	loc, err := host.ParseLocator(s.RemoteURL)
	if err != nil {
		return nil, fmt.Errorf("livee2e: open the seam's host: %w", err)
	}
	h, err := host.Open(loc, host.OpenConfig{
		GitHubAPIBase: s.APIRoot,
		LandCheck: func(context.Context, host.LandCandidate) (host.LandVerdict, error) {
			return host.LandVerdict{}, errSeamHostIsReadOnly
		},
	})
	if err != nil {
		return nil, fmt.Errorf("livee2e: open the seam's host: %w", err)
	}
	return h, nil
}

// errSeamHostIsReadOnly is the land check OpenHost installs.
var errSeamHostIsReadOnly = errors.New("livee2e: a seam's host is opened to read, never to land a write")

// Capabilities is what the seam's host declares about itself.
func (s HostSeam) Capabilities() (host.Capabilities, error) {
	h, err := s.OpenHost()
	if err != nil {
		return host.Capabilities{}, err
	}
	return h.Capabilities(), nil
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
	switch s.Kind {
	case HostKindGitHub:
	case HostKindBareRepo:
		return s.validateBareRepo()
	default:
		return fmt.Errorf("livee2e: host seam kind %s is not one Validate knows how to judge", s.Kind)
	}
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

// validateBareRepo is Validate for a bare-repository seam. Two things must hold
// and both are named when they do not: the remote and the web root are set, and
// nothing in the seam points at an API or at GitHub. A bare repository has no
// API root, so one that carries any is the half-real seam this check exists to
// refuse (a local remote with a GitHub API root would let a run write to GitHub
// while believing it stayed on disk).
func (s HostSeam) validateBareRepo() error {
	var empty []string
	if s.RemoteURL == "" {
		empty = append(empty, "RemoteURL")
	}
	if s.WebRoot == "" {
		empty = append(empty, "WebRoot")
	}
	if len(empty) > 0 {
		return fmt.Errorf("%w: %s (%s seam)", ErrHostSeamIncomplete, strings.Join(empty, ", "), s.Kind)
	}
	if s.APIRoot != "" || s.remoteIsGitHub() || s.webIsGitHub() {
		return fmt.Errorf("%w: a %s seam has no API root and no GitHub address: APIRoot=%q RemoteURL=%q (real GitHub: %t) WebRoot=%q (real GitHub: %t)",
			ErrHostSeamMixedHost, s.Kind, s.APIRoot, s.RemoteURL, s.remoteIsGitHub(), s.WebRoot, s.webIsGitHub())
	}
	return nil
}
