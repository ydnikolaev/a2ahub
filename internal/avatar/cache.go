// Package avatar owns the disposable, machine-local GitHub avatar cache.
// Network access is injected through Fetcher and remains implemented by the
// host adapter; dashboard reads never fetch and only consume validated bytes.
package avatar

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	recordSchema         = "avatar-cache/v1"
	defaultRefreshTTL    = 24 * time.Hour
	defaultRefreshBudget = 20 * time.Second
	maxAvatarBytes       = 512 << 10
	maxAvatarRecord      = 1 << 20
)

var (
	loginPattern        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	errInvalidLogin     = errors.New("avatar: invalid GitHub login")
	errInvalidRecord    = errors.New("avatar: invalid cache record")
	errTooLarge         = errors.New("avatar: image exceeds size limit")
	errUnsupportedMedia = errors.New("avatar: unsupported image media type")
)

// Fetcher is the host-owned network seam. sourceURL is empty on the first
// fetch and points at GitHub's validated avatar host thereafter; etag enables
// a conditional request that returns notModified without downloading bytes.
type Fetcher func(ctx context.Context, login, sourceURL, etag string) (data []byte, contentType, nextSourceURL, nextETag string, notModified bool, err error)

// Cache refreshes records under <cacheDir>/avatars and reads them back as data
// URIs. The one-file record keeps bytes, validator and checked time atomic.
type Cache struct {
	dir   string
	fetch Fetcher
	now   func() time.Time
	ttl   time.Duration
}

type record struct {
	Schema      string    `json:"schema"`
	Login       string    `json:"login"`
	SourceURL   string    `json:"source_url"`
	ETag        string    `json:"etag,omitempty"`
	ContentType string    `json:"content_type"`
	CheckedAt   time.Time `json:"checked_at"`
	Data        []byte    `json:"data"`
}

// NewCache constructs a cache with required dependencies. cacheDir is the
// project's existing .a2a/cache root, never a space mirror or shared repo.
func NewCache(cacheDir string, fetch Fetcher, now func() time.Time) (*Cache, error) {
	if cacheDir == "" || fetch == nil || now == nil {
		return nil, fmt.Errorf("avatar: NewCache: required dependency is empty")
	}
	return &Cache{dir: filepath.Join(cacheDir, "avatars"), fetch: fetch, now: now, ttl: defaultRefreshTTL}, nil
}

// Refresh updates each unique login at most once. A record younger than the
// TTL makes no request; an older record uses its ETag and rewrites only the
// checked time when GitHub answers 304. One failed owner does not prevent the
// others from refreshing, and the caller receives the joined diagnostics.
func (c *Cache) Refresh(ctx context.Context, logins []string) error {
	ctx, cancel := context.WithTimeout(ctx, defaultRefreshBudget)
	defer cancel()

	uniq := make(map[string]string, len(logins))
	for _, raw := range logins {
		login := strings.TrimSpace(raw)
		if !loginPattern.MatchString(login) {
			continue
		}
		key := strings.ToLower(login)
		if _, exists := uniq[key]; !exists {
			uniq[key] = login
		}
	}
	keys := make([]string, 0, len(uniq))
	for key := range uniq {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var refreshErrs []error
	for _, key := range keys {
		if err := c.refreshOne(ctx, uniq[key]); err != nil {
			refreshErrs = append(refreshErrs, fmt.Errorf("%s: %w", uniq[key], err))
		}
	}
	return errors.Join(refreshErrs...)
}

func (c *Cache) refreshOne(ctx context.Context, login string) error {
	path, err := c.path(login)
	if err != nil {
		return err
	}
	old, _ := readRecord(path)
	now := c.now().UTC()
	if old.Schema == recordSchema && !old.CheckedAt.IsZero() {
		age := now.Sub(old.CheckedAt)
		if age >= 0 && age < c.ttl {
			return nil
		}
	}

	data, media, sourceURL, etag, notModified, err := c.fetch(ctx, login, old.SourceURL, old.ETag)
	if err != nil {
		return err
	}
	if notModified {
		if err := validateRecord(old, login); err != nil {
			return fmt.Errorf("304 without a usable cached image: %w", err)
		}
		old.CheckedAt = now
		if etag != "" {
			old.ETag = etag
		}
		return writeRecord(path, old)
	}
	if len(data) > maxAvatarBytes {
		return errTooLarge
	}
	next := record{Schema: recordSchema, Login: login, SourceURL: sourceURL, ETag: etag, ContentType: media, CheckedAt: now, Data: data}
	if err := validateRecord(next, login); err != nil {
		return err
	}
	return writeRecord(path, next)
}

// DataURL reads one validated record without network access. Invalid, missing
// or oversized records degrade to ok=false so the UI keeps its monogram.
func DataURL(cacheDir, login string) (value string, ok bool) {
	if cacheDir == "" || !loginPattern.MatchString(login) {
		return "", false
	}
	path := filepath.Join(cacheDir, "avatars", strings.ToLower(login)+".json")
	r, err := readRecord(path)
	if err != nil || validateRecord(r, login) != nil {
		return "", false
	}
	return "data:" + r.ContentType + ";base64," + base64.StdEncoding.EncodeToString(r.Data), true
}

func (c *Cache) path(login string) (string, error) {
	if !loginPattern.MatchString(login) {
		return "", errInvalidLogin
	}
	return filepath.Join(c.dir, strings.ToLower(login)+".json"), nil
}

func validateRecord(r record, login string) error {
	if r.Schema != recordSchema || !strings.EqualFold(r.Login, login) || r.SourceURL == "" || len(r.Data) == 0 {
		return errInvalidRecord
	}
	if len(r.Data) > maxAvatarBytes {
		return errTooLarge
	}
	switch r.ContentType {
	case "image/png", "image/jpeg", "image/webp":
		return nil
	default:
		return errUnsupportedMedia
	}
}

func readRecord(path string) (record, error) {
	// #nosec G304 -- path is constructed only from the configured cache root
	// and a GitHub login that passed loginPattern; callers never pass artifact data.
	f, err := os.Open(path)
	if err != nil {
		return record{}, err
	}
	defer func() { _ = f.Close() }() // reason: read-only disposable cache handle
	raw, err := io.ReadAll(io.LimitReader(f, maxAvatarRecord+1))
	if err != nil {
		return record{}, err
	}
	if len(raw) > maxAvatarRecord {
		return record{}, errTooLarge
	}
	var r record
	if err := json.Unmarshal(raw, &r); err != nil {
		return record{}, fmt.Errorf("%w: %w", errInvalidRecord, err)
	}
	return r, nil
}

func writeRecord(path string, r record) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("avatar: encode cache record: %w", err)
	}
	if len(raw) > maxAvatarRecord {
		return errTooLarge
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("avatar: create cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".avatar-*.tmp")
	if err != nil {
		return fmt.Errorf("avatar: create cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // reason: cleanup only; rename removes it on success
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("avatar: protect cache temp file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("avatar: write cache record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("avatar: close cache record: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("avatar: replace cache record: %w", err)
	}
	return nil
}
