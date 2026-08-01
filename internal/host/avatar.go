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
)

const (
	avatarRequestTimeout   = 12 * time.Second
	maxAvatarResponseBytes = 512 << 10
)

var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

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
		if err := h.restCall(ctx, op, http.MethodGet, "/users/"+url.PathEscape(login), Credential{}, nil, &user); err != nil {
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
	q.Set("s", "64")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
