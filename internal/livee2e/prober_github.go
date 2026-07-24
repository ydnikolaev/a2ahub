package livee2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultAPIRoot is GitHub's REST root. Overridable on GitHubProber for tests
// (httptest) — the same seam shape cmd/a2a uses for A2A_GITHUB_API, kept
// local because this is harness introspection, not a product code path.
const defaultAPIRoot = "https://api.github.com"

// GitHubProber answers the two identity questions RunPreflight asks, against
// real GitHub.
//
// It is deliberately its own tiny client rather than a widening of
// internal/host: the product's 5-primitive Host interface exists to be the
// contract every host profile meets, and growing it with "who am I" and "what
// may I do here" — questions only a test harness asks — would push harness
// concerns into the product's contract.
type GitHubProber struct {
	// APIRoot defaults to https://api.github.com when empty.
	APIRoot string
	// Client defaults to a 30s-timeout client when nil. Bounded by default:
	// a probe that hangs would stall the run before a single scenario, with
	// no verdict to show for it.
	Client *http.Client
}

func (g *GitHubProber) root() string {
	if g.APIRoot == "" {
		return defaultAPIRoot
	}
	return strings.TrimSuffix(g.APIRoot, "/")
}

func (g *GitHubProber) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// get performs an authenticated GET and decodes the JSON body into out.
// Non-2xx is always an error: a probe that could not read the answer must
// never be mistaken for an answer (that is how a guard passes vacuously).
func (g *GitHubProber) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.root()+path, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// GitHub's error body carries the actionable part ("Resource not
		// accessible by personal access token" names a missing permission),
		// so it is surfaced rather than reduced to a status code.
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// Login reports the account a credential authenticates as.
func (g *GitHubProber) Login(ctx context.Context, token string) (string, error) {
	var payload struct {
		Login string `json:"login"`
	}
	if err := g.get(ctx, token, "/user", &payload); err != nil {
		return "", err
	}
	return payload.Login, nil
}

// RepoPermission reports the credential's OWN permission on owner/name, as
// GitHub computes it — the effective access, not what the token's declared
// scopes suggest.
//
// The flags are read most-privileged first, because GitHub reports them
// cumulatively (an admin also has push and pull). Reading them in any other
// order would report an admin as a writer, which is precisely the
// misclassification AC-960.4 exists to catch.
func (g *GitHubProber) RepoPermission(ctx context.Context, owner, name, token string) (Permission, error) {
	var payload struct {
		Permissions struct {
			Admin bool `json:"admin"`
			Push  bool `json:"push"`
			Pull  bool `json:"pull"`
		} `json:"permissions"`
	}
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	if err := g.get(ctx, token, path, &payload); err != nil {
		return "", err
	}
	switch p := payload.Permissions; {
	case p.Admin:
		return PermissionAdmin, nil
	case p.Push:
		return PermissionWrite, nil
	case p.Pull:
		return PermissionRead, nil
	default:
		return PermissionNone, nil
	}
}
