package spacenotify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		HTTPClient:   srv.Client(),
		BaseURL:      srv.URL,
		RetryDelayFn: func(int) time.Duration { return time.Millisecond },
	}
}

// TestClient_SendHTML_Success is the happy path every other test in this
// file is a variation on: a 2xx `ok:true` response is not an error, and the
// request reaches sendMessage with the token in the URL path (never a
// header, never the body — matching Telegram's own documented request
// shape).
func TestClient_SendHTML_Success(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv)
	err := c.SendHTML(context.Background(), NewToken("TESTTOKEN"), sampleMessage())
	if err != nil {
		t.Fatalf("SendHTML: %v", err)
	}
	if want := "/botTESTTOKEN/sendMessage"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

// TestClient_SendHTML_OKFalseFailsLoud is AC4: a Telegram `ok:false` makes
// the send fail — never a silent success.
//
// MUTATION EVIDENCE: with the `attemptErr == nil && result.api.OK` success
// check in telegram.go's send() changed to ignore api.OK entirely (i.e.
// treating any 2xx HTTP response as success regardless of the JSON body),
// this test reds with "SendHTML: want a non-nil error for ok:false, got
// nil" — confirmed by hand before this file was finalized (see this
// phase's own report, mutation_evidence).
func TestClient_SendHTML_OKFalseFailsLoud(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv)
	err := c.SendHTML(context.Background(), NewToken("TESTTOKEN"), sampleMessage())
	if err == nil {
		t.Fatalf("SendHTML: want a non-nil error for ok:false, got nil")
	}
	if !strings.Contains(err.Error(), "chat not found") {
		t.Fatalf("error = %q, want it to carry Telegram's own description", err.Error())
	}
}

// TestClient_Send_429RetryAfterHonoured is AC8's success half: a 429
// carrying `parameters.retry_after` is honoured (the retry actually waits
// approximately that long, not the RetryDelayFn override), and a
// subsequent 2xx succeeds.
func TestClient_Send_429RetryAfterHonoured(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 1","parameters":{"retry_after":1}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv) // RetryDelayFn is fast; only retry_after should make this take ~1s
	start := time.Now()
	err := c.SendHTML(context.Background(), NewToken("TESTTOKEN"), sampleMessage())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("SendHTML: %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want exactly 2 (one 429, one success)", attempts.Load())
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("elapsed = %s, want >= ~1s — retry_after was not honoured", elapsed)
	}
}

// TestClient_Send_429ExhaustsRetriesThenFailsLoud is AC8's failure half: a
// 429 that never clears is retried a bounded number of times and then
// fails loudly — never hangs, never silently gives up with a 0 exit.
//
// MUTATION EVIDENCE: with host.MaxAttempts's retry loop changed to `for
// attempt := 1; ; attempt++` (unbounded), this test times out rather than
// completing — confirmed by hand: the bounded loop is what makes "fails
// loudly" observable in finite time at all.
func TestClient_Send_429ExhaustsRetriesThenFailsLoud(t *testing.T) {
	t.Parallel()
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"Too Many Requests"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv)
	err := c.SendHTML(context.Background(), NewToken("TESTTOKEN"), sampleMessage())
	if err == nil {
		t.Fatalf("want a non-nil error once retries are exhausted")
	}
	if int(attempts.Load()) != 4 { // host.MaxAttempts
		t.Fatalf("attempts = %d, want 4 (host.MaxAttempts)", attempts.Load())
	}
}

// TestClient_Send_TokenNeverInErrorText is AC6: a transport failure (here,
// a connection refusal to a closed port) must never surface the token —
// http.Client.Do's own *url.Error would otherwise embed
// "https://.../bot<token>/sendMessage" verbatim in err.Error().
//
// MUTATION EVIDENCE: replacing errTransportFailed with the real httpErr
// (`return attemptResult{}, httpErr` instead of
// `return attemptResult{}, errTransportFailed`) makes this test red with
// the token found in the error text — confirmed by hand; restored before
// finalizing this file (see mutation_evidence in this phase's own report).
func TestClient_Send_TokenNeverInErrorText(t *testing.T) {
	t.Parallel()
	// A server that immediately closes the connection without responding —
	// guaranteed to produce a transport-level error (never an HTTP status),
	// deterministically, with no network flakiness.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	const token = "SUPER-SECRET-TOKEN-VALUE"
	c := fastClient(t, srv)
	err := c.SendHTML(context.Background(), NewToken(token), sampleMessage())
	if err == nil {
		t.Fatalf("want a non-nil transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked into error text: %q", err.Error())
	}
}

// TestClient_MessageThreadID_PresentIffTopicSet is AC12: message_thread_id
// is present on the wire iff route.topic is set, and ABSENT (never 0) when
// it is not — checked for BOTH renderer paths.
func TestClient_MessageThreadID_PresentIffTopicSet(t *testing.T) {
	t.Parallel()

	send := func(t *testing.T, rich bool, topic *int) string {
		t.Helper()
		var body []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			body = b
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		c := fastClient(t, srv)
		msg := sampleMessage()
		msg.Route.Topic = topic
		var err error
		if rich {
			err = c.SendRich(context.Background(), NewToken("T"), msg)
		} else {
			err = c.SendHTML(context.Background(), NewToken("T"), msg)
		}
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		return string(body)
	}

	topic := 42
	for _, rich := range []bool{false, true} {
		withTopic := send(t, rich, &topic)
		if !strings.Contains(withTopic, `"message_thread_id":42`) {
			t.Errorf("rich=%v: want message_thread_id:42 present, got: %s", rich, withTopic)
		}

		withoutTopic := send(t, rich, nil)
		if strings.Contains(withoutTopic, "message_thread_id") {
			t.Errorf("rich=%v: want message_thread_id ABSENT (not 0) when route.topic is unset, got: %s", rich, withoutTopic)
		}
	}
}

// TestClient_SendRich_Success mirrors TestClient_SendHTML_Success for the
// rich path, and asserts the request body is a well-formed
// sendRichMessage payload with the confirmed `rich_message.blocks` shape.
func TestClient_SendRich_Success(t *testing.T) {
	t.Parallel()
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv)
	if err := c.SendRich(context.Background(), NewToken("T"), sampleMessage()); err != nil {
		t.Fatalf("SendRich: %v", err)
	}
	rm, ok := raw["rich_message"].(map[string]any)
	if !ok {
		t.Fatalf("want a rich_message object, got: %#v", raw)
	}
	if _, ok := rm["blocks"].([]any); !ok {
		t.Fatalf("want rich_message.blocks to be an array, got: %#v", rm)
	}
}

// TestToken_StringNeverLeaksTheValue is AC6 at the type boundary: no fmt
// verb through Token can ever print the real value — %v/%s both route
// through Token's own fixed String(), and json.Marshal cannot reach the
// unexported field at all.
func TestToken_StringNeverLeaksTheValue(t *testing.T) {
	t.Parallel()
	const secret = "SUPER-SECRET-TOKEN-VALUE"
	tok := NewToken(secret)

	if got := tok.String(); got != "[redacted]" {
		t.Fatalf("Token.String() = %q, want [redacted]", got)
	}
	if got := fmt.Sprintf("%v token=%s", tok, tok); strings.Contains(got, secret) {
		t.Fatalf("token leaked through fmt formatting: %q", got)
	}
	// Wrapped in a struct with an exported field (rather than marshaled
	// directly) so this exercises the realistic case — a caller embedding a
	// Token in a JSON-tagged struct — without staticcheck's SA9005 flagging
	// a bare `json.Marshal(tok)` as pointless (Token itself has zero
	// exported fields, which is the WHOLE POINT here, not a bug).
	wrapped := struct{ T Token }{tok}
	marshaled, err := json.Marshal(wrapped)
	if err != nil {
		t.Fatalf("json.Marshal(Token): %v", err)
	}
	if strings.Contains(string(marshaled), secret) {
		t.Fatalf("token leaked through json.Marshal: %s", marshaled)
	}
}

// TestClient_Send_TokenNeverInErrorText_MalformedURL closes the half the
// original test missed, found by the closing audit.
//
// TestClient_Send_TokenNeverInErrorText covers client.Do's transport failure.
// http.NewRequestWithContext has the SAME defect and a different trigger: it
// calls url.Parse, whose error embeds the raw URL verbatim. A bot token pasted
// from a GitHub Actions secret with a trailing newline is enough — and that
// error used to be wrapped with %w, so it reached the delivery record, stdout,
// and $GITHUB_STEP_SUMMARY.
//
// The token below carries a control character for exactly that reason.
func TestClient_Send_TokenNeverInErrorText_MalformedURL(t *testing.T) {
	t.Parallel()
	const secret = "123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv)
	err := c.SendHTML(context.Background(), NewToken(secret+"\n"), sampleMessage())
	if err == nil {
		return // a request that somehow succeeds leaks nothing either
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("token leaked into the error text: %q", err.Error())
	}
}
