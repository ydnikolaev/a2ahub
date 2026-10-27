package host

import (
	"fmt"
	"net/http"
)

// OpenConfig is everything any host kind needs at construction, supplied by
// the wiring layer. The host package never reads the environment: the API base
// that A2A_GITHUB_API overrides is resolved in cmd/a2a and arrives here.
type OpenConfig struct {
	// HTTPClient performs the host's HTTP calls; nil means http.DefaultClient.
	HTTPClient *http.Client
	// GitHubAPIBase is the REST API root; "" means https://api.github.com.
	GitHubAPIBase string
}

// Open is the one space-addressed host constructor: it builds the host for the
// space loc addresses.
//
// In P1 both address classes yield a *GitHubHost with cfg's client and API
// base; a path-class locator stays GitHub-kind because the e2e rig reaches
// testkit/fakegithub through a local-path repo_url (docs/validator-backlog.md).
// The environment never participates in the choice.
func Open(loc Locator, cfg OpenConfig) (Host, error) {
	switch loc.class {
	case classURL, classPath:
		return OpenGitHub(cfg), nil
	default:
		return nil, fmt.Errorf("host: unsupported address class %d for %q", loc.class, loc.Raw)
	}
}

// OpenGitHub is the provider-scoped constructor, for the two consumers whose
// concern is GitHub by definition and that have no space locator: the avatar
// refresher (avatars are GitHub login pictures) and notify (it drives GitHub
// Actions secrets).
func OpenGitHub(cfg OpenConfig) *GitHubHost {
	return NewGitHubHost(cfg.HTTPClient, cfg.GitHubAPIBase)
}

// Opener resolves one space URL to its host and owner/name identity. It is the
// value injected into internal/cli (doctor) and internal/mcp, built in cmd/a2a.
// A nil error means a non-nil Host: callers may use the returned Host without
// a nil check.
type Opener func(raw string) (Host, Repo, error)

// NewOpener is the production Opener: ParseLocator, then Open.
func NewOpener(cfg OpenConfig) Opener {
	return func(raw string) (Host, Repo, error) {
		loc, err := ParseLocator(raw)
		if err != nil {
			return nil, Repo{}, err
		}
		h, err := Open(loc, cfg)
		if err != nil {
			return nil, Repo{}, err
		}
		return h, loc.Repo, nil
	}
}
