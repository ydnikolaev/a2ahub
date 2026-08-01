package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubHostFetchAvatarResolvesThenUsesConditionalImageRequest(t *testing.T) {
	t.Parallel()
	var userCalls, avatarCalls int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/ydnikolaev":
			userCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"avatar_url":%q}`, server.URL+"/avatar")
		case "/avatar":
			avatarCalls++
			if r.URL.Query().Get("s") != "64" {
				t.Errorf("avatar size = %q, want 64", r.URL.Query().Get("s"))
			}
			w.Header().Set("ETag", `"avatar-v1"`)
			if r.Header.Get("If-None-Match") == `"avatar-v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-avatar"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	h := NewGitHubHost(server.Client(), server.URL)
	data, media, source, etag, unchanged, err := h.FetchAvatar(context.Background(), "ydnikolaev", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "png-avatar" || media != "image/png" || etag != `"avatar-v1"` || unchanged {
		t.Fatalf("first fetch = data %q media %q etag %q unchanged %v", data, media, etag, unchanged)
	}
	if !strings.Contains(source, "/avatar?") {
		t.Fatalf("source = %q, want normalized avatar URL", source)
	}

	data, media, _, _, unchanged, err = h.FetchAvatar(context.Background(), "ydnikolaev", source, etag)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 || media != "" || !unchanged {
		t.Fatalf("conditional fetch = data %q media %q unchanged %v", data, media, unchanged)
	}
	if userCalls != 1 || avatarCalls != 2 {
		t.Fatalf("calls = users %d avatars %d, want 1 and 2", userCalls, avatarCalls)
	}
}

func TestGitHubHostFetchAvatarRejectsUntrustedImageHost(t *testing.T) {
	t.Parallel()
	h := NewGitHubHost(http.DefaultClient, "https://api.github.test")
	_, _, _, _, _, err := h.FetchAvatar(context.Background(), "ydnikolaev", "https://example.com/avatar.png", "")
	if !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("FetchAvatar error = %v, want ErrRequestFailed", err)
	}
}
