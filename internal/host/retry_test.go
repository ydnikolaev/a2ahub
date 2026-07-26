package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetry shrinks the retry schedule so a case exercises the LOGIC without
// waiting on RetryDelay's real 2s/4s/8s. Same seam, and the same reason, as
// ForkReadyDelay: a suite that waits fourteen seconds per case is a suite
// people stop running.
func fastRetry(h *GitHubHost) *GitHubHost {
	h.RetryDelayFn = func(int) time.Duration { return time.Millisecond }
	return h
}

// countingServer answers with the given statuses in order, repeating the last
// one forever, and counts how many requests it received.
func countingServer(t *testing.T, hdr map[string]string, statuses ...int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1))
		status := statuses[len(statuses)-1]
		if n <= len(statuses) {
			status = statuses[n-1]
		}
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = w.Write([]byte(`{"number":1}`))
			return
		}
		// The outage answered with an EMPTY body, which is why the error had
		// nothing in it to read. Reproduced deliberately.
		if status >= 500 {
			return
		}
		_, _ = fmt.Fprintf(w, `{"message":"refused %d"}`, status)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func retryProbe(t *testing.T, srv *httptest.Server) func() error {
	t.Helper()
	h := fastRetry(NewGitHubHost(srv.Client(), srv.URL))
	return func() error {
		var out struct {
			Number int `json:"number"`
		}
		return h.doJSON(context.Background(), "Probe", http.MethodPost, srv.URL+"/probe",
			Credential{Token: "t"}, map[string]any{"title": "x"}, &out)
	}
}

// TestRetrySucceedsAfterTransientFailures is AC-1040.1: the caller of a call
// answered 500, 500, then 200 sees success and never learns the first two
// happened.
//
// This is the outage of 2026-07-24 with the fix in place. Then, every
// `a2a submit` died on the FIRST 500 with an empty body: the push had landed
// and only the pull request had not, and nothing in the message distinguished a
// platform outage from a refusal.
func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	t.Parallel()

	srv, calls := countingServer(t, nil, 500, 500, 200)
	if err := retryProbe(t, srv)(); err != nil {
		t.Fatalf("a call answered 500, 500, 200 returned %v — a transient failure must not surface "+
			"as a failed exchange", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("server saw %d requests, want 3 (two transient, one success)", got)
	}
}

// TestRetryOnTransportErrorAndOn503 covers the other two shapes that mean
// "did not confirm" rather than "did not happen": a 503, and a connection that
// never produced a status at all.
func TestRetryOnTransportErrorAndOn503(t *testing.T) {
	t.Parallel()

	t.Run("503 then 200", func(t *testing.T) {
		t.Parallel()
		srv, calls := countingServer(t, nil, 503, 200)
		if err := retryProbe(t, srv)(); err != nil {
			t.Fatalf("503 then 200 returned %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("server saw %d requests, want 2", got)
		}
	})

	t.Run("a dead endpoint is transient, not a refusal", func(t *testing.T) {
		t.Parallel()
		// A server that is closed before the call: the transport fails with no
		// status, which is the case ClassifyStatus treats as unknown BLANKET,
		// without inspecting error types.
		srv, _ := countingServer(t, nil, 200)
		url := srv.URL
		srv.Close()

		h := fastRetry(NewGitHubHost(nil, url))
		err := h.doJSON(context.Background(), "Probe", http.MethodGet, url+"/probe", Credential{Token: "t"}, nil, nil)
		if !errors.Is(err, ErrTransient) {
			t.Fatalf("a transport failure must classify as transient, got %v — a dropped connection "+
				"says nothing about whether the write landed", err)
		}
	})
}

// TestRetryExhaustionNamesTheClassAndTheNextMove is AC-1040.2 plus the message
// requirement, and the message IS the deliverable here rather than decoration.
//
// The old error was `host: OpenPR: github api request failed: status 500:` —
// an empty body and no hint. An agent could not tell a platform outage from a
// refusal, and the one correct next move (re-run, which is safe because the
// write funnel is idempotent by head branch) was named nowhere.
func TestRetryExhaustionNamesTheClassAndTheNextMove(t *testing.T) {
	t.Parallel()

	srv, calls := countingServer(t, nil, 500)
	start := time.Now()
	err := retryProbe(t, srv)()
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTransient) {
		t.Fatalf("a permanent 5xx must wrap ErrTransient so errors.Is resolves the class; got %v", err)
	}
	if got := calls.Load(); got != int64(MaxAttempts) {
		t.Errorf("server saw %d requests, want MaxAttempts (%d) — the budget must be BOUNDED",
			got, MaxAttempts)
	}
	if elapsed > RetryTotalCap {
		t.Errorf("the call took %s, over the RetryTotalCap of %s", elapsed, RetryTotalCap)
	}

	msg := err.Error()
	for _, want := range []struct{ substr, why string }{
		{"transiently", "name the CLASS — this is the word that distinguishes an outage from a refusal"},
		{"may or may not have landed", "state the uncertainty; the push can land while the PR does not"},
		{"re-running is safe", "name the next move, or the agent has to guess it"},
		{"idempotent by head branch", "say WHY it is safe — otherwise it reads as reassurance rather than a property"},
		{"status 500", "keep the observed evidence, so the message is diagnosable and not just advice"},
	} {
		if !strings.Contains(msg, want.substr) {
			t.Errorf("the surviving error is missing %q — %s\ngot: %s", want.substr, want.why, msg)
		}
	}
}

// TestRetryScheduleFitsItsOwnCap asserts the SCHEDULE rather than a wall clock,
// because the wall-clock assertion above runs with shrunk delays and so cannot
// see the real numbers at all. This is where the production values are checked.
func TestRetryScheduleFitsItsOwnCap(t *testing.T) {
	t.Parallel()

	var total time.Duration
	for attempt := 1; attempt < MaxAttempts; attempt++ { // MaxAttempts attempts => MaxAttempts-1 delays
		total += RetryDelay(attempt)
	}
	if total >= RetryTotalCap {
		t.Fatalf("retrying to exhaustion sleeps %s, which does not fit inside RetryTotalCap (%s) — the "+
			"cap would then be the thing that stops the retry, so the schedule an operator reads is not "+
			"the schedule they get", total, RetryTotalCap)
	}
	// A command that hangs is worse than one that reports a transient failure
	// and says re-running is safe.
	if RetryTotalCap > 30*time.Second {
		t.Errorf("RetryTotalCap is %s — long enough that a user assumes the command hung", RetryTotalCap)
	}
}

// TestNoRetryOnARefusal is AC/US-4: a genuine refusal fails on the FIRST
// response, exactly as fast as before the retry existed. Burning the budget on
// a 403 or a 422 delays an answer that will not change.
func TestNoRetryOnARefusal(t *testing.T) {
	t.Parallel()

	for _, status := range []int{401, 403, 404, 422} {
		t.Run(fmt.Sprintf("%d", status), func(t *testing.T) {
			t.Parallel()
			srv, calls := countingServer(t, nil, status)
			err := retryProbe(t, srv)()
			if err == nil {
				t.Fatalf("status %d returned no error", status)
			}
			if errors.Is(err, ErrTransient) {
				t.Errorf("status %d was classified as transient — a refusal is not an outage", status)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("server saw %d requests for status %d, want exactly 1", got, status)
			}
		})
	}
}

// TestA403ThatIsReallyARateLimitIsRetried is the case ClassifyStatus's own doc
// comment refuses to guess at: GitHub uses 403 for BOTH a secondary rate limit
// and a hard permission refusal, and the status code alone cannot tell them
// apart. The headers can, so the caller that holds them decides.
//
// Getting this backwards is costly in both directions — retrying a permission
// problem wastes the budget on an unchangeable answer, while refusing a rate
// limit turns "wait a moment" into a failed exchange.
func TestA403ThatIsReallyARateLimitIsRetried(t *testing.T) {
	t.Parallel()

	t.Run("Retry-After present: retried", func(t *testing.T) {
		t.Parallel()
		srv, calls := countingServer(t, map[string]string{"Retry-After": "1"}, 403, 200)
		if err := retryProbe(t, srv)(); err != nil {
			t.Fatalf("a rate-limited 403 then 200 returned %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("server saw %d requests, want 2", got)
		}
	})

	t.Run("x-ratelimit-remaining zero: retried", func(t *testing.T) {
		t.Parallel()
		srv, calls := countingServer(t, map[string]string{"X-RateLimit-Remaining": "0"}, 403, 200)
		if err := retryProbe(t, srv)(); err != nil {
			t.Fatalf("a primary-rate-limited 403 then 200 returned %v", err)
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("server saw %d requests, want 2", got)
		}
	})

	t.Run("neither header: a permission refusal, not retried", func(t *testing.T) {
		t.Parallel()
		srv, calls := countingServer(t, nil, 403)
		if err := retryProbe(t, srv)(); err == nil {
			t.Fatal("a bare 403 must still refuse")
		}
		if got := calls.Load(); got != 1 {
			t.Errorf("server saw %d requests, want 1 — a bare 403 is the pending-PAT case "+
				"(observed 2026-07-24), and retrying it cannot help", got)
		}
	})
}

// TestRetryAfterIsHonouredButNeverBeyondTheCap: a header is a request, not an
// instruction to hang. GitHub asking for two minutes and this CLI blocking for
// two minutes are different products.
func TestRetryAfterIsHonouredButNeverBeyondTheCap(t *testing.T) {
	t.Parallel()

	srv, calls := countingServer(t, map[string]string{"Retry-After": "600"}, 429)
	start := time.Now()
	err := retryProbe(t, srv)()
	elapsed := time.Since(start)

	if !errors.Is(err, ErrTransient) {
		t.Fatalf("a persistent 429 must surface as transient; got %v", err)
	}
	if elapsed > RetryTotalCap {
		t.Fatalf("a Retry-After of 600s was honoured for %s — past RetryTotalCap (%s) the retry must "+
			"stop and report, not keep waiting", elapsed, RetryTotalCap)
	}
	// It must also have STOPPED rather than silently retried with the short
	// fallback delay: the first oversized wait ends the loop.
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d requests, want 1 — a Retry-After larger than the remaining budget ends "+
			"the retry instead of being replaced by a shorter delay the server did not agree to", got)
	}
}

// TestARetriedWriteRefusedAsDuplicateSaysTheFirstAttemptLanded covers the one
// case the retry itself introduces, and it is a case worth naming rather than
// leaving as a puzzle.
//
// A POST that answers 5xx may have landed anyway. The retry is then refused as
// a duplicate, and without this the caller would see a bare 422 "already
// exists" for a write it believes it never made. Named, it becomes the most
// useful fact available: the write is IN.
func TestARetriedWriteRefusedAsDuplicateSaysTheFirstAttemptLanded(t *testing.T) {
	t.Parallel()

	srv, calls := countingServer(t, nil, 500, 422)
	err := retryProbe(t, srv)()

	if !errors.Is(err, ErrRetriedWriteWasDuplicate) {
		t.Fatalf("a 422 AFTER a retried 5xx must say the first attempt landed; got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d requests, want 2", got)
	}
	// And a 422 on the FIRST attempt is an ordinary validation failure — it
	// must NOT claim a phantom earlier attempt.
	srv2, _ := countingServer(t, nil, 422)
	if err := retryProbe(t, srv2)(); errors.Is(err, ErrRetriedWriteWasDuplicate) {
		t.Errorf("a first-attempt 422 was reported as a duplicate of an earlier attempt that never "+
			"happened: %v", err)
	}
}
