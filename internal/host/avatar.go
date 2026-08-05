package host

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/ghauth"
)

const (
	avatarRequestTimeout   = 12 * time.Second
	maxAvatarResponseBytes = 512 << 10
	// Keep the local source comfortably larger than every avatar currently
	// rendered by the dashboard. The UI can then grow an avatar without
	// stretching a thumbnail that was fetched for today's 38 px timeline node.
	// GitHub currently caps avatar renditions at 460 px. Keep the largest
	// available local source so the same cache can serve compact timeline faces
	// and future large profile surfaces without another lossy up-scale.
	avatarRequestPixels = "460"
)

var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// lookupGitHubCLIToken is ghauth.Token behind a package var so tests drive
// both branches without a `gh` binary, a login, or a network — and so no test
// can accidentally read the operator's real token and print it in a failure
// message, which is how a live credential once reached a log.
var lookupGitHubCLIToken = ghauth.Token

// avatarAPICredential is the credential the login-resolution call carries.
//
// FetchAvatar takes no credential parameter and cannot grow one cheaply: it is
// passed by value to avatar.NewCache, so its signature is a callback contract
// shared with internal/avatar. Reading the machine's own gh login here keeps
// that contract intact and is the right authority anyway — this call reads
// PUBLIC user data and needs a token only to leave the anonymous rate-limit
// budget, which is exactly what a machine-level identity is for. It is not a
// space credential and must never become one: nothing here is space-scoped.
func avatarAPICredential(ctx context.Context) Credential {
	if token, ok := lookupGitHubCLIToken(ctx); ok {
		return Credential{Token: token}
	}
	return Credential{}
}

// FetchAvatar resolves a GitHub login once and then conditionally refreshes
// the returned image URL with ETag. It is deliberately an optional structural
// capability rather than another method on Host: only the local sync adapter
// uses it, while GitLab/Gitea host profiles remain free to omit it.
func (h *GitHubHost) FetchAvatar(ctx context.Context, login, sourceURL, etag string) (data []byte, contentType, nextSourceURL, nextETag string, notModified bool, err error) {
	const op = "FetchAvatar"
	if !githubLoginPattern.MatchString(login) {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: ErrInvalidRequest}
	}
	ctx, cancel := context.WithTimeout(ctx, avatarRequestTimeout)
	defer cancel()

	if sourceURL == "" {
		var user struct {
			AvatarURL string `json:"avatar_url"`
		}
		// Authenticated, and only this call. It hits api.github.com, whose
		// ANONYMOUS budget is 60/hour per IP shared with everything else on
		// that address — so on a machine that had done any ordinary GitHub
		// work, resolving a login 403'd and the avatar never arrived.
		//
		// That was survivable while it only meant a stale image. It stopped
		// being survivable when the cache record schema moved to v3: every
		// avatar written by an older binary is now rejected as invalid, which
		// is correct for a disposable cache ONLY because a refresh repopulates
		// it. An unauthenticated refresh cannot, so two individually
		// reasonable decisions combined into permanently empty avatars with
		// nothing but a doctor advisory to say why.
		//
		// The image fetch below deliberately stays anonymous: it goes to
		// avatars.githubusercontent.com, a CDN that neither needs the token
		// nor should be handed one.
		if err := h.restCall(ctx, op, http.MethodGet, "/users/"+url.PathEscape(login), avatarAPICredential(ctx), nil, &user); err != nil {
			return nil, "", "", "", false, err
		}
		sourceURL = user.AvatarURL
	}
	normalized, err := h.normalizedAvatarURL(sourceURL)
	if err != nil {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: build avatar request: %w", ErrRequestFailed, err)}
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/webp")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: %w", ErrRequestFailed, err)}
	}
	defer func() { _ = resp.Body.Close() }() // reason: response is fully read below
	nextETag = resp.Header.Get("ETag")
	if resp.StatusCode == http.StatusNotModified {
		return nil, "", normalized, nextETag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: avatar status %d", ErrRequestFailed, resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarResponseBytes+1))
	if err != nil {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: read avatar: %w", ErrRequestFailed, err)}
	}
	if len(raw) == 0 || len(raw) > maxAvatarResponseBytes {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: avatar response has invalid size", ErrRequestFailed)}
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: avatar content type: %w", ErrRequestFailed, err)}
	}
	switch media {
	case "image/png", "image/jpeg", "image/webp":
		return raw, media, normalized, nextETag, false, nil
	default:
		return nil, "", "", "", false, &Error{Op: op, Input: login, Err: fmt.Errorf("%w: avatar content type %q", ErrRequestFailed, media)}
	}
}

// normalizedAvatarURL allows only GitHub's dedicated avatar host or the
// configured API host (the latter is what hermetic httptest servers use).
// This prevents a compromised/corrupt cache or API response becoming an SSRF
// primitive during the next sync.
func (h *GitHubHost) normalizedAvatarURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("%w: invalid avatar URL", ErrRequestFailed)
	}
	base, baseErr := url.Parse(h.BaseURL)
	allowedTestHost := baseErr == nil && strings.EqualFold(u.Hostname(), base.Hostname())
	secureScheme := u.Scheme == "https" || allowedTestHost && base.Scheme == "http" && u.Scheme == "http"
	if !secureScheme {
		return "", fmt.Errorf("%w: avatar URL must use HTTPS", ErrRequestFailed)
	}
	if !strings.EqualFold(u.Hostname(), "avatars.githubusercontent.com") && !allowedTestHost {
		return "", fmt.Errorf("%w: refused avatar host %q", ErrRequestFailed, u.Hostname())
	}
	q := u.Query()
	q.Set("s", avatarRequestPixels)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
