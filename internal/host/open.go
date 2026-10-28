package host

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
// A path-class locator has three outcomes, read from the repository's own git
// config:
//
//   - marked (a2a.host=local, written only by PrepareLocalSpace): a *LocalHost.
//     A marked path with a nil cfg.LandCheck is an error, never an
//     unvalidated host.
//   - a readable repository without the marker: a *GitHubHost. An unmarked
//     path stays GitHub-kind because the e2e rig reaches testkit/fakegithub
//     through a local-path repo_url (docs/validator-backlog.md).
//   - could not tell: an error wrapping ErrSpaceKindUnknown, and no host. A
//     path that does not exist (a local space that was moved or deleted) also
//     wraps ErrSpacePathMissing. A failed probe is not a resolved "unmarked"
//     (AGENTS.md anti-pattern #22): opening it as GitHub would demand a GitHub
//     credential for a space that needs none and judge it with GitHub's rules.
//
// The environment never participates in the choice.
func Open(loc Locator, cfg OpenConfig) (Host, error) {
	switch loc.class {
	case classURL:
		return OpenGitHub(cfg), nil
	case classPath:
		gitDir, err := filepath.Abs(loc.path)
		if err != nil {
			return nil, fmt.Errorf("host: %q: %w: %w", loc.Raw, ErrSpaceKindUnknown, err)
		}
		marked, err := probeMarker(gitDir)
		if err != nil {
			return nil, fmt.Errorf("host: %q: %w", loc.Raw, err)
		}
		if !marked {
			return OpenGitHub(cfg), nil
		}
		if cfg.LandCheck == nil {
			return nil, fmt.Errorf("host: %q is a local space but no land check is configured: %w", loc.Raw, errNoLandCheck)
		}
		return newLocalHost(gitDir, cfg.LandCheck, cfg.LandAuthor), nil
	default:
		return nil, fmt.Errorf("host: unsupported address class %d for %q", loc.class, loc.Raw)
	}
}

// errNoLandCheck is why Open refused a marked path.
var errNoLandCheck = errors.New("a local space cannot be opened without a land check")

// probeMarker tells Open whether the repository at gitDir is a local space. It
// reads the same key localMarked reads, in that repository's own config only
// (--git-dir and --local), under a bounded context: Open has no context of its
// own, and the probe is one local git call.
//
// Unlike localMarked, which PrepareLocalSpace uses to decide whether to write
// the marker, it keeps "unmarked" apart from "could not read": marked is
// meaningful only when err is nil. git config --get exits 0 with the value
// when the key is set and 1 when it is not; every other answer (a path that is
// missing, a path git does not read as a repository, a timeout, a git that
// could not run) is an error wrapping ErrSpaceKindUnknown.
func probeMarker(gitDir string) (marked bool, err error) {
	if _, statErr := os.Stat(gitDir); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			return false, fmt.Errorf("%w (%w)", ErrSpacePathMissing, ErrSpaceKindUnknown)
		}
		return false, fmt.Errorf("%w: %w", ErrSpaceKindUnknown, statErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), markerProbeTimeout)
	defer cancel()
	res := runRecoveryGit(ctx, 256, "--git-dir="+gitDir, "config", "--local", "--get", localMarkerKey)
	switch {
	case ctx.Err() != nil:
		return false, fmt.Errorf("%w: %s: the %s marker probe did not finish: %w", ErrSpaceKindUnknown, gitDir, localMarkerKey, ctx.Err())
	case res.err != nil:
		return false, fmt.Errorf("%w: %s: the %s marker probe could not run: %w", ErrSpaceKindUnknown, gitDir, localMarkerKey, res.err)
	case res.exitCode == 0:
		return strings.TrimSpace(string(res.stdout)) == localMarkerValue, nil
	case res.exitCode == 1:
		return false, nil
	default:
		return false, fmt.Errorf("%w: %s: git could not read this repository's config (the %s marker probe exited %d)", ErrSpaceKindUnknown, gitDir, localMarkerKey, res.exitCode)
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
