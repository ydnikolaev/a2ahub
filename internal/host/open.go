package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
)

// OpenConfig is everything any host kind needs at construction, supplied by
// the wiring layer. The host package never reads the environment: the API base
// that A2A_GITHUB_API overrides is resolved in cmd/a2a and arrives here.
type OpenConfig struct {
	// HTTPClient performs the host's HTTP calls; nil means http.DefaultClient.
	HTTPClient *http.Client
	// GitHubAPIBase is the REST API root; "" means https://api.github.com.
	GitHubAPIBase string
	// LandCheck judges a write before a local space lands it. Open refuses a
	// local space when it is nil: a marked path never opens as an unvalidated
	// host. GitHub ignores it.
	LandCheck LandCheckFunc
	// LandAuthor is the identity a local land is authored as (local:<system>).
	// It may be empty: Open still succeeds, so a caller that only reads a local
	// space's capabilities or inspection needs no system, and OpenPR refuses
	// before it reads or writes anything.
	LandAuthor string
}

// Open is the one space-addressed host constructor: it builds the host for the
// space loc addresses.
//
// A URL-class locator is always a *GitHubHost with cfg's client and API base.
// A path-class locator is a *LocalHost when the repository at the path carries
// the a2a.host=local marker in its own git config (written only by
// PrepareLocalSpace), and a *GitHubHost otherwise: an unmarked path stays
// GitHub-kind because the e2e rig reaches testkit/fakegithub through a
// local-path repo_url (docs/validator-backlog.md). A marked path with a nil
// cfg.LandCheck is an error, never an unvalidated host. The environment never
// participates in the choice.
func Open(loc Locator, cfg OpenConfig) (Host, error) {
	switch loc.class {
	case classURL:
		return OpenGitHub(cfg), nil
	case classPath:
		gitDir, err := filepath.Abs(loc.path)
		if err == nil && openMarked(gitDir) {
			if cfg.LandCheck == nil {
				return nil, fmt.Errorf("host: %q is a local space but no land check is configured: %w", loc.Raw, errNoLandCheck)
			}
			return newLocalHost(gitDir, cfg.LandCheck, cfg.LandAuthor), nil
		}
		return OpenGitHub(cfg), nil
	default:
		return nil, fmt.Errorf("host: unsupported address class %d for %q", loc.class, loc.Raw)
	}
}

// errNoLandCheck is why Open refused a marked path.
var errNoLandCheck = errors.New("a local space cannot be opened without a land check")

// openMarked reads the local-space marker under a bounded context: Open has no
// context of its own, and the probe is one local git call.
func openMarked(gitDir string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), markerProbeTimeout)
	defer cancel()
	return localMarked(ctx, gitDir)
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
