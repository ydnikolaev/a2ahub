package avatar

import (
	"context"
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRefreshIsIdempotentAndConditional(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	var calls int
	fetch := func(_ context.Context, login, sourceURL, etag string) ([]byte, string, string, string, bool, error) {
		calls++
		if calls == 1 {
			if login != "ydnikolaev" || sourceURL != "" || etag != "" {
				t.Fatalf("first fetch = login %q source %q etag %q", login, sourceURL, etag)
			}
			return []byte("png-bytes"), "image/png", "https://avatars.githubusercontent.com/u/1?s=64", `"v1"`, false, nil
		}
		if sourceURL == "" || etag != `"v1"` {
			t.Fatalf("conditional fetch = source %q etag %q", sourceURL, etag)
		}
		return nil, "", sourceURL, `"v1"`, true, nil
	}
	c, err := NewCache(t.TempDir(), fetch, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(context.Background(), []string{"ydnikolaev", "YDNIKOLAEV", "ydnikolaev"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fetch calls = %d, want one unique login", calls)
	}
	got, ok := DataURL(filepath.Dir(c.dir), "ydnikolaev")
	if !ok {
		t.Fatal("DataURL did not read the cached image")
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	if got != want {
		t.Fatalf("DataURL = %q, want %q", got, want)
	}

	if err := c.Refresh(context.Background(), []string{"ydnikolaev"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("fresh cache made %d calls, want 1 total", calls)
	}

	now = now.Add(25 * time.Hour)
	if err := c.Refresh(context.Background(), []string{"ydnikolaev"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("stale cache made %d calls, want conditional second call", calls)
	}
}

func TestCacheRejectsOversizedAndUnsupportedImages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		data  []byte
		media string
		want  error
	}{
		{name: "oversized", data: make([]byte, maxAvatarBytes+1), media: "image/png", want: errTooLarge},
		{name: "svg", data: []byte("<svg/>"), media: "image/svg+xml", want: errUnsupportedMedia},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fetch := func(context.Context, string, string, string) ([]byte, string, string, string, bool, error) {
				return tt.data, tt.media, "https://avatars.githubusercontent.com/u/1?s=64", "", false, nil
			}
			c, err := NewCache(t.TempDir(), fetch, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.Refresh(context.Background(), []string{"octocat"}); !errors.Is(err, tt.want) {
				t.Fatalf("Refresh error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCacheIgnoresInvalidLoginAndDataURLTraversal(t *testing.T) {
	t.Parallel()
	var calls int
	c, err := NewCache(t.TempDir(), func(context.Context, string, string, string) ([]byte, string, string, string, bool, error) {
		calls++
		return nil, "", "", "", false, nil
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(context.Background(), []string{"../../secret", ""}); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("invalid login made %d fetch calls", calls)
	}
	if _, ok := DataURL(t.TempDir(), "../../secret"); ok {
		t.Fatal("DataURL accepted traversal login")
	}
}
