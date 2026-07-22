package release

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// defaultAPIBaseURL is GitHub's production REST API host. Tests override
// GitHubSource.BaseURL with an httptest server — no live network call ever
// happens in this package's unit tests.
const defaultAPIBaseURL = "https://api.github.com"

// DefaultUpdateRepo is the compiled-in "<owner>/<name>" of the product repo
// `a2a update` resolves releases from, overridable via machine config
// defaults["update_repo"] (the publish-prep public-repo transition is a
// one-line default flip). SSOT for the CLI verb and the notice checker wiring.
const DefaultUpdateRepo = "ydnikolaev/a2ahub"

// maxListResponseBytes bounds the releases-list JSON response read (rails:
// "bounded reads everywhere" — internal/host's own idiom).
const maxListResponseBytes = 4 << 20 // 4 MiB

// tagPattern is the v*.*.* grammar a usable release tag must match (spec 19
// T3/§6: "pre-release/malformed tags skipped"). Bare-version parsing itself
// is internal/version's job; this is only the fetch-time filter.
var tagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// Asset is one release asset (a platform binary, SHA256SUMS, or a
// per-asset .cosign.bundle).
type Asset struct {
	// Name is the asset's file name, e.g. "a2a-linux-amd64" or
	// "SHA256SUMS".
	Name string
	// URL is the GitHub REST asset API URL (used for the private,
	// tokened download path with Accept: application/octet-stream).
	URL string
	// BrowserDownloadURL is the public, tokenless download URL.
	BrowserDownloadURL string
	// Size is the asset's byte size as reported by the API.
	Size int64
}

// Release is one product-repo release: the bare Version (Tag minus a
// leading "v", release.yml's `main.version` stamp convention), the target
// commitish, and its assets.
type Release struct {
	// Tag is the release's git tag, e.g. "v0.3.0".
	Tag string
	// Version is Tag with its leading "v" stripped, e.g. "0.3.0" — the
	// bare form release.yml stamps into main.version and the form every
	// internal/version.OlderThan comparison expects.
	Version string
	// Commit is the release's target commitish (branch or SHA the tag
	// points at).
	Commit string
	// Assets lists every asset attached to the release.
	Assets []Asset
}

// Source fetches the latest usable release of the update repo. GitHubSource
// is the v1 implementation (GitHub releases REST); a v2 Hub-backed source
// (OP-108, D-030) is a second implementation behind the same interface, no
// call-site change.
type Source interface {
	// Latest returns the newest usable release (draft/pre-release/
	// malformed-tag candidates skipped). No usable release => ErrNoRelease.
	Latest(ctx context.Context) (Release, error)
	// Name identifies this source for the update-check cache's "source"
	// field (T3 CheckState shape) — e.g. "github".
	Name() string
}

// GitHubSource is the v1 Source: GitHub releases REST over an injected
// *http.Client and base URL (so httptest fakes the API in tests).
type GitHubSource struct {
	// Client performs every HTTP call. Required; NewGitHubSource defaults
	// it to http.DefaultClient.
	Client *http.Client
	// BaseURL is the REST API root (default "https://api.github.com").
	BaseURL string
	// Repo is "<owner>/<name>" of the update repo (compiled default
	// "ydnikolaev/a2ahub", overridable via machine config
	// defaults.update_repo — the CLI wave wires that, not this package).
	Repo string
	// Token authenticates requests when non-empty (Authorization: Bearer
	// <Token>). Left "" for the tokenless (public-repo) path. This field
	// is a plain value — NewGitHubSource resolves it from the T3 env
	// order for convenience, but tests may set it directly to assert the
	// tokened/tokenless header behavior without touching the environment.
	Token string
}

// NewGitHubSource constructs a GitHubSource. client may be nil (defaults to
// http.DefaultClient); baseURL may be "" (defaults to the real GitHub API).
// The token is resolved once, here, via ResolveToken's T3 env order
// (A2A_UPDATE_TOKEN -> GH_TOKEN -> GITHUB_TOKEN -> tokenless) — callers that
// need to bypass env resolution (tests) should construct GitHubSource{}
// directly instead.
func NewGitHubSource(client *http.Client, baseURL, repo string) *GitHubSource {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	return &GitHubSource{Client: client, BaseURL: baseURL, Repo: repo, Token: ResolveToken()}
}

// ResolveToken resolves the update-fetch credential per the T3 env order:
// A2A_UPDATE_TOKEN -> GH_TOKEN -> GITHUB_TOKEN -> "" (tokenless). Env-only
// (space.ResolveCredential's env-first, never-in-config stance) — no
// literal secret ever lives in a config file.
func ResolveToken() string {
	for _, name := range []string{"A2A_UPDATE_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// Name implements Source.Name: the provenance tag written to CheckState.Source.
func (s *GitHubSource) Name() string { return "github" }

// ghRelease/ghAsset decode the GitHub REST releases-list response shape
// (only the fields this package needs).
type ghRelease struct {
	TagName         string    `json:"tag_name"`
	TargetCommitish string    `json:"target_commitish"`
	Draft           bool      `json:"draft"`
	Prerelease      bool      `json:"prerelease"`
	Assets          []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// Latest implements Source.Latest: GET /repos/<repo>/releases (list, newest
// first per the GitHub API's own ordering) and returns the first entry that
// is not a draft, not a pre-release, and whose tag matches v*.*.* — skipping
// the rest (spec 19 T3/§6). No usable entry (including an empty list) =>
// ErrNoRelease.
func (s *GitHubSource) Latest(ctx context.Context) (Release, error) {
	const op = "Latest"
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := s.BaseURL
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}

	url := fmt.Sprintf("%s/repos/%s/releases", baseURL, s.Repo)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: build request: %v", ErrDownloadFailed, err)}
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	if s.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+s.Token)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: %v", ErrDownloadFailed, err)}
	}
	defer func() { _ = resp.Body.Close() }() // reason: response already fully read/discarded below

	limited := io.LimitReader(resp.Body, maxListResponseBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: read response: %v", ErrDownloadFailed, err)}
	}
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, &Error{Op: op, Input: s.Repo, Err: ErrNoRelease}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: status %d: %s", ErrDownloadFailed, resp.StatusCode, strings.TrimSpace(string(raw)))}
	}

	var list []ghRelease
	if err := json.Unmarshal(raw, &list); err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: decode response: %v", ErrDownloadFailed, err)}
	}

	for _, r := range list {
		if r.Draft || r.Prerelease || !tagPattern.MatchString(r.TagName) {
			continue
		}
		assets := make([]Asset, 0, len(r.Assets))
		for _, a := range r.Assets {
			assets = append(assets, Asset(a))
		}
		return Release{
			Tag:     r.TagName,
			Version: strings.TrimPrefix(r.TagName, "v"),
			Commit:  r.TargetCommitish,
			Assets:  assets,
		}, nil
	}
	return Release{}, &Error{Op: op, Input: s.Repo, Err: ErrNoRelease}
}

// findAsset returns the named asset from rel, if present.
func findAsset(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}
