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

	"github.com/ydnikolaev/a2ahub/internal/ghauth"
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

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// PageURL constructs a trusted GitHub release page only from the
// configured owner/repository grammar and a validated canonical release
// version. Invalid cache/config data yields no link.
func PageURL(repo, productVersion string) string {
	if !repoPattern.MatchString(repo) || !tagPattern.MatchString("v"+productVersion) {
		return ""
	}
	return "https://github.com/" + repo + "/releases/tag/v" + productVersion
}

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

// lookupGitHubCLIToken is ghauth.Token behind a package var so tests can drive
// both branches without a `gh` binary, a login, or a network — and so no test
// can accidentally read the operator's real token and print it in a failure
// message, which is how a live credential once reached a log file.
var lookupGitHubCLIToken = ghauth.Token

// ResolveToken resolves the EXPLICIT update token per the T3 env order:
// A2A_UPDATE_TOKEN -> GH_TOKEN -> GITHUB_TOKEN -> "" (none).
//
// Deliberately env-only, and it must stay that way. Callers thread this value
// into fetchAsset, which SELECTS A DOWNLOAD ROUTE on it: empty picks the
// public browser_download_url (CDN-served, effectively unmetered), non-empty
// picks the private release-asset API (metered). Setting one of these
// variables is an operator saying "use this identity, including the private
// path"; the mere existence of a gh login on the machine says no such thing,
// and inferring it would move every developer machine off the CDN.
//
// Rate-limit relief for the api.github.com JSON read lives in apiToken below,
// which is the only place it belongs.
func ResolveToken() string {
	for _, name := range []string{"A2A_UPDATE_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// apiToken is the credential the releases-list read authenticates with: the
// explicit token if one is set, otherwise the machine's own GitHub CLI login.
//
// The fallback exists because an unauthenticated api.github.com read is not a
// degraded read — it draws on a 60-per-hour budget shared by every anonymous
// caller on the IP. `a2a update` failed with "403 API rate limit exceeded" on
// a machine whose `gh` was logged in the whole time, because the only resolver
// in this package looked at environment variables and nothing else.
//
// Its old doc comment justified that as matching
// "space.ResolveCredential's env-first, never-in-config stance — no literal
// secret ever lives in a config file". That did not survive reading the
// function it cited: space.ResolveCredential resolves `cmd:<argv>`
// machine-config references too, and space.ParseCredentialReference
// structurally accepts ONLY `env:<VAR>` and `cmd:<argv>` — so a config
// reference cannot BE a literal secret, and the stance it appealed to permits
// exactly what it was being used to forbid.
//
// Asking `gh` rather than the machine config is deliberate: that config's
// `credentials:` map is keyed by SPACE ID and these reads are not
// space-scoped — they fetch a2ahub's own public releases. Handing a
// counterparty-scoped space token to an unrelated release lookup would be a
// scope decision nobody made. The gh login is the machine's general GitHub
// identity, which is the right authority for "I am not anonymous", and it is
// the same source space.DefaultCredentialReference already probes.
func (s *GitHubSource) apiToken() string {
	if s.Token != "" {
		return s.Token
	}
	if token, ok := lookupGitHubCLIToken(context.Background()); ok {
		return token
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
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: build request: %w", ErrDownloadFailed, err)}
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	// The gh fallback applies HERE and nowhere else, and the distinction is
	// load-bearing rather than fastidious.
	//
	// This is a JSON read of api.github.com, whose ANONYMOUS budget is 60/hour
	// per IP shared with every other anonymous caller on the address. That is
	// what 403'd `a2a update` on a machine holding a perfectly good gh login.
	//
	// But s.Token is ALSO read back by callers — notifications_platform.go and
	// cache/update.go hand it to fetchers — and fetchAsset switches route on
	// it: empty means the public browser_download_url (CDN, effectively
	// unmetered), non-empty means the private release-asset API (metered).
	// Seeding s.Token from an ambient gh login would therefore move every
	// machine with gh installed off the CDN and onto the rate-limited API for
	// its DOWNLOADS — the opposite of the fix, and it is what 11 update tests
	// caught when the fallback was first put in ResolveToken.
	//
	// An explicitly exported token means "use this identity, including the
	// private asset path". A gh login that merely exists means "I am not
	// anonymous". Only the second is inferred here.
	if token := s.apiToken(); token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: %w", ErrDownloadFailed, err)}
	}
	defer func() { _ = resp.Body.Close() }() // reason: response already fully read/discarded below

	limited := io.LimitReader(resp.Body, maxListResponseBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: read response: %w", ErrDownloadFailed, err)}
	}
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, &Error{Op: op, Input: s.Repo, Err: ErrNoRelease}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: status %d: %s", ErrDownloadFailed, resp.StatusCode, strings.TrimSpace(string(raw)))}
	}

	var list []ghRelease
	if err := json.Unmarshal(raw, &list); err != nil {
		return Release{}, &Error{Op: op, Err: fmt.Errorf("%w: decode response: %w", ErrDownloadFailed, err)}
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
