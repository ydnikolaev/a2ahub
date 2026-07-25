//go:build livee2e

package livee2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestInjectingProxyForwardsAndAsksUpstreamForIdentityEncoding is the
// proxy's baseline: a request the predicate never matches is a faithful
// passthrough, and — this is the injectingProxy doc comment's own load-
// bearing claim — the OUTBOUND leg always asks the real API for `identity`
// encoding, never gzip, which is what keeps a fabricated injected body from
// ever having to reconcile against an inherited Content-Encoding header.
func TestInjectingProxyForwardsAndAsksUpstreamForIdentityEncoding(t *testing.T) {
	t.Parallel()
	var gotAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("X-Upstream", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy, err := newInjectingProxy(upstream.URL, func(*http.Request) bool { return false }, http.StatusBadGateway)
	if err != nil {
		t.Fatalf("newInjectingProxy: %v", err)
	}
	defer proxy.Close()

	resp, err := http.Get(proxy.URL() + "/repos/o/r/pulls")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (predicate never matches, so this must be a plain passthrough)", resp.StatusCode)
	}
	if got := strings.TrimSpace(string(body)); got != `{"ok":true}` {
		t.Fatalf("body = %q, want the upstream's own real body", got)
	}
	if resp.Header.Get("X-Upstream") != "yes" {
		t.Fatal("the upstream's own response header did not survive the proxy")
	}
	if gotAcceptEncoding != "identity" {
		t.Fatalf("outbound Accept-Encoding = %q, want %q — nothing gzip-encoded may ever enter this process", gotAcceptEncoding, "identity")
	}
	if proxy.Fired() {
		t.Fatal("Fired() is true, but inject() always returned false")
	}
}

// TestInjectingProxyFiresOnceOnTheMatchingRequest is the proxy's whole
// point: the FIRST matching request is genuinely forwarded (the upstream
// really receives it — this is what makes "the write landed anyway" true)
// but the caller is handed a fabricated 5xx instead of the real response;
// every later request — including a second one the SAME predicate would
// also match — is a faithful passthrough.
func TestInjectingProxyFiresOnceOnTheMatchingRequest(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7}`))
	}))
	defer upstream.Close()

	proxy, err := newInjectingProxy(upstream.URL, injectOnCreatePR, http.StatusGatewayTimeout)
	if err != nil {
		t.Fatalf("newInjectingProxy: %v", err)
	}
	defer proxy.Close()

	resp1, err := http.Post(proxy.URL()+"/repos/o/r/pulls", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	body1, err := io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()
	if err != nil {
		t.Fatalf("read body 1: %v", err)
	}
	if resp1.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d (injected)", resp1.StatusCode, http.StatusGatewayTimeout)
	}
	if !strings.Contains(string(body1), injectedFaultMarker) {
		t.Fatalf("body = %q, want it to carry the injected-fault marker", body1)
	}
	if !proxy.Fired() {
		t.Fatal("Fired() is false after the matching request was sent")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream received %d requests, want 1 — the injected leg must still forward for real (spec 36 §T6-d)", got)
	}

	resp2, err := http.Post(proxy.URL()+"/repos/o/r/pulls", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	body2, err := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if err != nil {
		t.Fatalf("read body 2: %v", err)
	}
	if resp2.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want %d — the fault fires ONCE, not on every matching request", resp2.StatusCode, http.StatusCreated)
	}
	if got := strings.TrimSpace(string(body2)); got != `{"number":7}` {
		t.Fatalf("body = %q, want the upstream's own real body on the second call", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream received %d requests total, want 2", got)
	}
}

// TestInjectOnCreatePRMatchesOnlyThePullsCreateCall is the predicate's own
// unit test: it must fault the ONE write (POST .../pulls) and nothing else
// — not the funnel's own step-0 GET on the same collection, not a
// sub-resource write, not an unrelated endpoint.
func TestInjectOnCreatePRMatchesOnlyThePullsCreateCall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, method, path string
		want               bool
	}{
		{"create-pr", http.MethodPost, "/repos/o/r/pulls", true},
		{"list-pulls-step0", http.MethodGet, "/repos/o/r/pulls", false},
		{"merge-subpath", http.MethodPost, "/repos/o/r/pulls/7/merge", false},
		{"unrelated-endpoint", http.MethodPost, "/repos/o/r/issues/7/comments", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, err := http.NewRequest(tc.method, "http://x"+tc.path, nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			if got := injectOnCreatePR(r); got != tc.want {
				t.Errorf("injectOnCreatePR(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}
