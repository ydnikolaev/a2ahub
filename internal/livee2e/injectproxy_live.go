//go:build livee2e

package livee2e

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// injectedFaultMarker is the literal substring stamped into the injected
// response body. A row asserts on THIS string in the CLI's own stderr,
// never on a bare "status 5xx" — so a genuine, unrelated 5xx from real
// GitHub (unlikely, but this rig has observed stranger things — spec 36
// §T6-d) is never mistaken for the fault this row injected on purpose.
const injectedFaultMarker = "livee2e-injected-fault(spec38-AC-982.1)"

// injectingProxy is an in-process reverse proxy in front of a real API root
// (spec 38 §6-Q1, plan D-G): "a proxy in front of A2A_GITHUB_API for the
// injected rows only". Every request is genuinely forwarded to target — a
// write this proxy sits in front of really reaches GitHub — but the FIRST
// request the caller-supplied predicate matches never lets the CALLER see
// that the write succeeded: the proxy fabricates a 5xx instead, discarding
// the real (successful) upstream response. This is exactly the class of
// defect spec 36 §T6-d records as observed against real GitHub on
// 2026-07-24 (a 504 on OpenPR that had in fact created the PR).
//
// Deliberately NOT net/http/httputil.ReverseProxy + ModifyResponse: that
// shape forwards the caller's own request headers verbatim to the upstream,
// and a real GitHub response can come back gzip-encoded — replacing the
// BODY in ModifyResponse while leaving Content-Encoding: gzip in the
// response headers hands the caller's own Transport plaintext labelled as
// gzip, which it fails to decode. That would surface as an unrelated
// transport error masking the very fault this proxy exists to inject. This
// handler sidesteps the whole class by asking the real API for `identity`
// encoding explicitly on the OUTBOUND leg, so nothing gzip-encoded ever
// enters this process in the first place — verified by
// injectproxy_live_test.go against a local fake upstream; NOT re-verified
// against real GitHub's own willingness to honour `identity` by this wave
// (see wave H's own report, teeth_proof).
//
// Only the FIRST request the predicate matches is faulted (an atomic.Bool
// latch) — every other request, before or after, is a faithful proxy. This
// is deliberate: `a2a submit` issues more than one API call (the funnel's
// own step 0, FindPRByHeadBranch, is a GET on the same collection endpoint
// the injected POST targets), and this file's own row re-runs `a2a submit`
// a second time through the SAME harness to prove recovery — neither call
// may be faulted a second time, or the row would be proving something about
// a retried retry, not about one 5xx mid-write.
type injectingProxy struct {
	srv   *httptest.Server
	fired *atomic.Bool
}

// URL is the proxy's own root — what a caller points A2A_GITHUB_API (via
// checkout.RunWithAPIRoot) or a ghClient.APIRoot (this file's own unit
// test) at.
func (p *injectingProxy) URL() string { return p.srv.URL }

// Fired reports whether the injected fault has fired yet — a caller asserts
// on this to confirm the row's own premise (the fault actually fired
// exactly once) rather than trusting silence.
func (p *injectingProxy) Fired() bool { return p.fired.Load() }

// Close stops the proxy's own local listener. It never touches target —
// nothing this package owns.
func (p *injectingProxy) Close() { p.srv.Close() }

// newInjectingProxy starts the proxy. target is the real API root every
// request is forwarded to (defaultAPIRoot in a live row; a local
// httptest.Server's own URL in this file's unit test). inject decides which
// ONE request is faulted — the caller supplies a method+path predicate,
// never a blanket "fault everything" default, so a request the row did not
// mean to touch can never be faulted by accident.
func newInjectingProxy(target string, inject func(*http.Request) bool, injectedStatus int) (*injectingProxy, error) {
	targetURL, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("livee2e: newInjectingProxy: parse target %q: %w", target, err)
	}

	fired := &atomic.Bool{}
	client := &http.Client{Timeout: 30 * time.Second}

	handler := func(w http.ResponseWriter, r *http.Request) {
		outURL := *targetURL
		outURL.Path = r.URL.Path
		outURL.RawQuery = r.URL.RawQuery

		outReq, err := http.NewRequestWithContext(r.Context(), r.Method, outURL.String(), r.Body)
		if err != nil {
			http.Error(w, "livee2e: injectingProxy: build outbound request: "+err.Error(), http.StatusBadGateway)
			return
		}
		outReq.Header = r.Header.Clone()
		outReq.ContentLength = r.ContentLength
		// Explicit `identity` — see this type's own doc comment on why:
		// keeps every response this process ever reads plaintext, so the
		// injected leg never has to reconcile a fabricated body against an
		// inherited Content-Encoding header.
		outReq.Header.Set("Accept-Encoding", "identity")

		resp, err := client.Do(outReq)
		if err != nil {
			http.Error(w, "livee2e: injectingProxy: forward to "+target+": "+err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			http.Error(w, "livee2e: injectingProxy: read upstream body: "+readErr.Error(), http.StatusBadGateway)
			return
		}

		if !fired.Load() && inject(r) {
			fired.Store(true)
			injected := fmt.Sprintf("%s: upstream's own real response was status %d for %s %s (forwarded for real, then discarded on purpose)",
				injectedFaultMarker, resp.StatusCode, r.Method, r.URL.Path)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(injected)))
			w.WriteHeader(injectedStatus)
			_, _ = w.Write([]byte(injected))
			return
		}

		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
	}

	return &injectingProxy{srv: httptest.NewServer(http.HandlerFunc(handler)), fired: fired}, nil
}

// injectOnCreatePR matches the ONE write this wave's own row injects on:
// POST .../pulls (host.GitHubHost.OpenPR's create call, internal/host/
// github.go) — never the funnel's own step-0 GET FindPRByHeadBranch (a LIST
// call on the SAME collection endpoint, distinguished only by method), so
// the idempotency check that runs before this write is never the one
// faulted.
//
// Matching on a path SUFFIX (after trimming a trailing slash) rather than
// exact equality to "/repos/{owner}/{repo}/pulls" — this proxy is handed
// only the incoming request's path (see newInjectingProxy's Director), and
// keeping the predicate owner/repo-agnostic means a row never has to thread
// the harness's own org/repo strings through to this file just to build a
// literal match.
func injectOnCreatePR(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasSuffix(strings.TrimSuffix(r.URL.Path, "/"), "/pulls")
}
