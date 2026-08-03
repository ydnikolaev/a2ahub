//go:build livee2e

package livee2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The 2026-07-25 run is why these exist. GitHub answered `GET /pulls` with a
// 200 and a truncated body; the client returned the decode error straight
// out; every family's first `pullForBranch` inherited an opaque "unexpected
// end of JSON input"; and the report showed FIFTEEN rows as product
// failures for a transport-level cause. For a pre-release ritual that is the
// worst error the tier can make — it either blocks a good release, or trains
// the operator to discount reds.
//
// A body we cannot parse tells us nothing about the product. It is the same
// "we do not know" a 5xx already routes through, so it takes the same road.

func TestDoRetriesAnUnparseableBodyAndNeverCallsItDecisive(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"number": 1, "head`)) // truncated mid-object
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	var out []struct{ Number int }
	_, err := c.do(context.Background(), http.MethodGet, "/repos/o/r/pulls", nil, &out)

	if err == nil {
		t.Fatal("an unparseable body must not read as success")
	}
	// TEETH: return the decode error directly from the OutcomeOK branch (the
	// pre-fix shape) and this assertion reds — the error is a *json.SyntaxError
	// chain, not ErrUnknownOutcome, so verdictForError renders FAIL.
	if !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("a body we could not parse must be UNKNOWN, not decisive; got %v", err)
	}
	if v, ok := verdictForError(err); !ok || v != VerdictTimedOut {
		t.Fatalf("verdictForError = (%v, %v), want (%v, true) — a transport-level cause must never render as a product failure", v, ok, VerdictTimedOut)
	}
	if got := calls.Load(); int(got) != MaxAttempts {
		t.Fatalf("attempted %d times, want %d — an unparseable body is retried like any other unknown outcome", got, MaxAttempts)
	}
	// The diagnosis has to survive into the message, or the next occurrence
	// is as undiagnosable as this one was.
	if !strings.Contains(err.Error(), "byte body") || !strings.Contains(err.Error(), "body starts:") {
		t.Fatalf("error must carry the body's size and a prefix for diagnosis; got %v", err)
	}
}

func TestDoAcceptsABodyThatParsesOnARetry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`[{"number": 1, "head`))
			return
		}
		_, _ = w.Write([]byte(`[{"number": 7}]`))
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	var out []struct {
		Number int `json:"number"`
	}
	if _, err := c.do(context.Background(), http.MethodGet, "/repos/o/r/pulls", nil, &out); err != nil {
		t.Fatalf("a retry that parses must succeed: %v", err)
	}
	if len(out) != 1 || out[0].Number != 7 {
		t.Fatalf("decoded %+v, want the SECOND response's content — a partially-decoded first attempt must not leak through", out)
	}
}

// An empty body on a 200 is not a decode failure and must not be retried:
// GitHub answers a no-content read that way, and "no pull requests" is a
// real answer.
func TestDoTreatsAnEmptyBodyAsAnEmptyAnswer(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	var out []struct{ Number int }
	if _, err := c.do(context.Background(), http.MethodGet, "/repos/o/r/pulls", nil, &out); err != nil {
		t.Fatalf("an empty body on a 200 is an empty answer, not a failure: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("out = %+v, want empty", out)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("attempted %d times, want 1 — an empty answer must not be retried", got)
	}
}

func TestPullCarriesTheBaseReachableMergeCommit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"number": 17,
			"head": {"ref": "a2a/alpha/submit/XA-alpha-example", "sha": "head-sha"},
			"merge_commit_sha": "base-result-sha",
			"merged": true,
			"state": "closed"
		}`))
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	got, err := c.Pull(context.Background(), "o", "r", 17)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got.HeadSHA != "head-sha" || got.MergeCommitSHA != "base-result-sha" {
		t.Fatalf("Pull = %+v, want distinct head and base-reachable merge commits", got)
	}
}

func TestListPullsCarriesOperationMetadataBody(t *testing.T) {
	t.Parallel()

	const body = `generated artifacts
<!-- a2a-operation: {"key":"op-v1-example","ids":["XQ-alpha-parent","XS-bravo-response"]} -->`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, `[{"number":17,"state":"open","body":%q,"head":{"ref":"a2a/bravo/respond/op-v1-example"}}]`, body); err != nil {
			t.Errorf("write fake pull response: %v", err)
		}
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	got, err := c.ListPulls(context.Background(), "o", "r", "open")
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if len(got) != 1 || got[0].Body != body {
		t.Fatalf("ListPulls = %+v, want one pull carrying operation metadata body %q", got, body)
	}
}

// TestReadBoundRefusesRatherThanTruncates is the root cause of the
// 2026-07-25 run, kept as a regression.
//
// The client read the body through a bare io.LimitReader, which truncates
// SILENTLY. The throwaway space had accumulated ~100 pull requests, so
// `GET /pulls?state=all&per_page=100` crossed the 1 MiB ceiling, every
// response arrived as valid JSON cut off mid-object, and 17 rows reported
// an unparseable body. It read as a GitHub fault; it was ours.
//
// A bound that produces invalid data is worse than no bound — it converts
// "too big", which is diagnosable, into "malformed", which is not.
//
// TEETH: restore `io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))`
// and this test reds — the oversized body comes back as a decode failure
// instead of a bound refusal that names the limit.
func TestReadBoundRefusesRatherThanTruncates(t *testing.T) {
	t.Parallel()

	// Valid JSON, comfortably past the bound.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"number":1,"pad":"`))
		chunk := strings.Repeat("x", 1<<16)
		for written := 0; written < int(maxResponseBytes)+(1<<16); written += len(chunk) {
			_, _ = w.Write([]byte(chunk))
		}
		_, _ = w.Write([]byte(`"}]`))
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	var out []struct{ Number int }
	_, err := c.do(context.Background(), http.MethodGet, "/repos/o/r/pulls", nil, &out)

	if err == nil {
		t.Fatal("a body past the bound must not read as success")
	}
	if !strings.Contains(err.Error(), "exceeds") || !strings.Contains(err.Error(), "byte bound") {
		t.Fatalf("the error must name the bound so the cause is diagnosable, not report a decode failure; got %v", err)
	}
	// It is still an UNKNOWN outcome, not a product verdict.
	if v, ok := verdictForError(err); !ok || v != VerdictTimedOut {
		t.Fatalf("verdictForError = (%v, %v), want (%v, true)", v, ok, VerdictTimedOut)
	}
}

// TestCheckRunsPassesFilterAll is AC-984.1's own regression (spec 38 wave G):
// the pre-fix CheckRuns built its query with NO `filter` param, on the
// documented-but-wrong assumption that omitting it meant "unfiltered".
// GitHub's own default for this endpoint is `filter=latest` even when the
// caller never asks for it (internal/host/github.go's own CheckStatus states
// this explicitly, previously verified for that function's own selection
// logic) — so the omitted param silently collapsed every listing to "one run
// per check name", meaning a caller counting entries to detect "a re-trigger
// added a new run" could never see the count exceed 1, before OR after any
// re-trigger. This is why cross-section-retrigger-stays-red timed out
// identically on four consecutive live runs with "no new check run appeared
// after the re-trigger": the condition it polled for was unsatisfiable from
// the day it was written, not a timing race.
//
// TEETH: drop `filter=all` from CheckRuns' path (or revert to no filter
// param at all) and this test reds on the missing query parameter.
func TestCheckRunsPassesFilterAll(t *testing.T) {
	t.Parallel()

	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"check_runs":[]}`))
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	if _, err := c.CheckRuns(context.Background(), "o", "r", "deadbeef"); err != nil {
		t.Fatalf("CheckRuns: %v", err)
	}
	if !strings.Contains(gotQuery, "filter=all") {
		t.Fatalf("CheckRuns query = %q, want it to contain filter=all — GitHub's own default (filter=latest) collapses the listing to one run per check name, which can never detect a re-trigger's new run", gotQuery)
	}
}

// TestListPullsPaginates guards the other half: `state=all` over a space
// that is reset but never emptied grows every run, so one unpaginated call
// is a question that stops being answerable as the space ages. A page cap
// that is reached must ERROR — a truncated listing would make
// pullForBranch answer "no PR on that branch" for a PR that exists, which
// is the same silent-wrong-answer class one layer up.
func TestListPullsPaginates(t *testing.T) {
	t.Parallel()

	var pages atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages.Add(1)
		w.WriteHeader(http.StatusOK)
		switch page {
		case "1":
			// A FULL page — the caller must ask for another.
			var b strings.Builder
			b.WriteString("[")
			for i := 0; i < 100; i++ {
				if i > 0 {
					b.WriteString(",")
				}
				fmt.Fprintf(&b, `{"number":%d,"state":"closed","head":{"ref":"a2a/x/submit/ID-%d"}}`, i+1, i+1)
			}
			b.WriteString("]")
			_, _ = w.Write([]byte(b.String()))
		default:
			// A short page ends the walk.
			_, _ = w.Write([]byte(`[{"number":101,"state":"open","head":{"ref":"a2a/x/submit/ID-101"}}]`))
		}
	}))
	defer srv.Close()

	c := &ghClient{Token: "t", APIRoot: srv.URL}
	got, err := c.ListPulls(context.Background(), "o", "r", "all")
	if err != nil {
		t.Fatalf("ListPulls: %v", err)
	}
	if len(got) != 101 {
		t.Fatalf("got %d pulls, want 101 — a full page must not be mistaken for the last one", len(got))
	}
	if p := pages.Load(); p != 2 {
		t.Fatalf("requested %d pages, want 2", p)
	}
	// The PR that only exists on page 2 must be findable, which is the
	// whole point: pullForBranch's answer depends on the listing being
	// complete.
	var found bool
	for _, p := range got {
		if p.HeadRef == "a2a/x/submit/ID-101" && p.Number == 101 {
			found = true
		}
	}
	if !found {
		t.Fatal("the last page's PR is missing — a partial listing answers \"no PR on that branch\" for a PR that exists")
	}
}
