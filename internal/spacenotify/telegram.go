// telegram.go is P4's Transport (spec 04's own Transport rules table): the
// binary's first outbound HTTP call that is neither GitHub nor a release
// download.
//
// It reuses rather than reinvents: internal/host's own ClassifyStatus,
// RetryDelay, MaxAttempts and RetryTotalCap (spec 04 §5) drive the retry
// schedule, and the injected-*http.Client-plus-BaseURL shape mirrors
// internal/release/source.go's own GitHubSource (Client/BaseURL fields, so
// httptest fakes the API and no test here ever reaches the real network).
//
// One reuse the plan's own ground truth named turned out not to hold:
// internal/host/retry.go's retryAfterDelay is UNEXPORTED (used only by
// host/github.go) and internal/host is off this phase's allowlist, so it
// cannot be called from here. This file re-reads the 429 delay itself —
// see retryAfterFromResponse's own doc comment for why it reads the
// response BODY rather than the HTTP header host's retryAfterDelay reads,
// which is the other half of that deviation (verified against the live
// Bot API docs, not assumed).
package spacenotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/sensitive"
)

// defaultTelegramBaseURL is the Bot API's production host (verified live
// 2026-08-18: "All queries... must be presented... https://api.telegram.org/
// bot<token>/METHOD_NAME").
const defaultTelegramBaseURL = "https://api.telegram.org"

// maxTelegramResponseBytes bounds every API response read (rails: bounded
// reads everywhere — internal/release/source.go's own maxListResponseBytes
// idiom).
const maxTelegramResponseBytes = 1 << 20 // 1 MiB

// Token holds the bot token this verb resolves from the env var
// msg.Route.Secret names. Its String() is fixed — never the real value —
// so no %v/%s format verb, log line, or fmt.Sprintf built from a Token can
// print it (US-4/AC6). The value is held in an unexported field with no
// json tag path, so json.Marshal cannot reach it either (Token is a
// construction input, never itself put on the wire).
type Token struct {
	value string
}

// NewToken wraps a raw token value.
func NewToken(value string) Token { return Token{value: value} }

// String implements fmt.Stringer. Deliberately constant.
func (Token) String() string { return "[redacted]" }

// Empty reports whether no token value was resolved.
func (t Token) Empty() bool { return t.value == "" }

// Client sends rendered messages to Telegram's Bot API. HTTPClient and
// BaseURL are injected (release.GitHubSource's own convention) so every
// test in this package drives an httptest fake; RetryDelayFn is injected
// separately so a test exercising the 429/5xx retry path does not pay
// RetryDelay's real multi-second schedule (internal/host/github.go's own
// RetryDelayFn convention, httpDo's doc comment).
type Client struct {
	HTTPClient   *http.Client
	BaseURL      string
	RetryDelayFn func(attempt int) time.Duration
}

// NewClient constructs a Client wired to the real Telegram API and the
// real (multi-second) retry schedule.
func NewClient() *Client {
	return &Client{HTTPClient: http.DefaultClient, BaseURL: defaultTelegramBaseURL}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultTelegramBaseURL
}

func (c *Client) retryDelay(attempt int) time.Duration {
	if c.RetryDelayFn != nil {
		return c.RetryDelayFn(attempt)
	}
	return host.RetryDelay(attempt)
}

// apiResponse decodes the Bot API's own response envelope (verified live:
// "always has a Boolean field 'ok'... an Integer 'error_code' field...
// optional field 'parameters' of type ResponseParameters").
type apiResponse struct {
	OK          bool           `json:"ok"`
	Description string         `json:"description,omitempty"`
	ErrorCode   int            `json:"error_code,omitempty"`
	Parameters  *apiParameters `json:"parameters,omitempty"`
}

// apiParameters is ResponseParameters, restricted to the field this
// package reads: "retry_after — Optional. In case of exceeding flood
// control, the number of seconds left to wait" (verified live).
type apiParameters struct {
	RetryAfter int `json:"retry_after,omitempty"`
}

type linkPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

// sendMessageRequest is sendMessage's own request body, restricted to the
// fields this renderer needs (verified live against the method's own
// parameter table).
type sendMessageRequest struct {
	ChatID             string              `json:"chat_id"`
	MessageThreadID    *int                `json:"message_thread_id,omitempty"`
	Text               string              `json:"text"`
	ParseMode          string              `json:"parse_mode"`
	LinkPreviewOptions *linkPreviewOptions `json:"link_preview_options,omitempty"`
}

// sendRichMessageRequest is sendRichMessage's own request body (verified
// live 2026-08-18 — see render_rich.go's package doc comment for the full
// parameter table this was checked against).
type sendRichMessageRequest struct {
	ChatID          string           `json:"chat_id"`
	MessageThreadID *int             `json:"message_thread_id,omitempty"`
	RichMessage     inputRichMessage `json:"rich_message"`
}

// SendHTML renders msg with RenderHTML and sends it via sendMessage.
// message_thread_id carries msg.Route.Topic UNCHANGED — present iff the
// route names one, never coerced to 0 when it does not (AC12; spec 04
// Transport table's own "route.topic absent" row).
func (c *Client) SendHTML(ctx context.Context, token Token, msg Message) error {
	body := sendMessageRequest{
		ChatID:             msg.Route.Chat,
		MessageThreadID:    msg.Route.Topic,
		Text:               RenderHTML(msg, msg.Route.Locale),
		ParseMode:          "HTML",
		LinkPreviewOptions: &linkPreviewOptions{IsDisabled: true},
	}
	return c.sendJSON(ctx, token, "sendMessage", body)
}

// SendRich renders msg with RenderRich and sends it via sendRichMessage —
// see render_rich.go's package doc comment for why message_thread_id is
// wired here identically to SendHTML rather than refused.
func (c *Client) SendRich(ctx context.Context, token Token, msg Message) error {
	body := sendRichMessageRequest{
		ChatID:          msg.Route.Chat,
		MessageThreadID: msg.Route.Topic,
		RichMessage:     RenderRich(msg, msg.Route.Locale),
	}
	return c.sendJSON(ctx, token, "sendRichMessage", body)
}

func (c *Client) sendJSON(ctx context.Context, token Token, method string, body any) error {
	if token.Empty() {
		// Callers (deliver.go) refuse before ever reaching here — this is a
		// second, defensive refusal, never the primary one, so the primary
		// error text (which names the missing secret) is deliver.go's job.
		return fmt.Errorf("notify send: %s: no token", method)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("notify send: %s: encode request: %w", method, err)
	}
	return c.send(ctx, method, token, payload)
}

// errTransportFailed is a fixed, contentless sentinel — never the real
// error http.Client.Do returned. http.Client.Do wraps EVERY transport
// failure (dial, TLS, timeout, context cancellation) in a *url.Error whose
// Error() text embeds the full request URL, which embeds the bot token
// (spec 04's own "https://api.telegram.org/bot<token>/METHOD_NAME" shape;
// AC6). The real error is therefore never read past the `err != nil` check
// in attempt() below — not logged, not wrapped, not passed to
// host.ClassifyStatus (which only tests err != nil, never err.Error()).
var errTransportFailed = errors.New("spacenotify: request failed")

type attemptResult struct {
	status int
	api    apiResponse
}

// attempt performs ONE HTTP call and reports either a decoded apiResponse
// or a safe (token-free) error — see errTransportFailed's own doc comment
// for why a raw transport error is never surfaced.
func (c *Client) attempt(ctx context.Context, method string, token Token, payload []byte) (attemptResult, error) {
	// Built against a REDACTED placeholder path, then the real path is set on
	// the parsed URL — the token never reaches url.Parse.
	//
	// errTransportFailed below explains why client.Do's error is never
	// surfaced. NewRequestWithContext has the SAME defect and it was missed
	// here for a wave: it calls url.Parse, whose error text embeds the raw URL
	// verbatim, and a bot token pasted from a GitHub Actions secret with a
	// trailing newline is enough to trigger it. Wrapping that error with %w —
	// which this function did — put the whole token into the delivery record,
	// then into stdout, then into $GITHUB_STEP_SUMMARY, readable by anyone
	// with read access to the run. internal/cli/cmd_notify_setup.go already
	// documented and fixed the identical vector; this is the same mitigation,
	// applied to the path that runs in every CI job.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/bot[redacted]/"+method, bytes.NewReader(payload))
	if err != nil {
		// Fixed and contentless: the placeholder URL is the only thing that
		// could have failed to parse, and naming it adds nothing.
		return attemptResult{}, fmt.Errorf("notify send: %s: build request failed", method)
	}
	req.URL.Path = "/bot" + token.value + "/" + method
	req.Header.Set("Content-Type", "application/json")

	resp, httpErr := c.httpClient().Do(req)
	if httpErr != nil {
		return attemptResult{}, errTransportFailed
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxTelegramResponseBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return attemptResult{status: resp.StatusCode}, fmt.Errorf("notify send: %s: read response: %w", method, err)
	}
	var api apiResponse
	if err := json.Unmarshal(raw, &api); err != nil {
		return attemptResult{status: resp.StatusCode}, fmt.Errorf("notify send: %s: decode response: %w", method, err)
	}
	return attemptResult{status: resp.StatusCode, api: api}, nil
}

// send drives the bounded retry (spec 04 Transport table): honour a 429's
// own retry_after (retryAfterFromResponse), classify every other outcome
// through internal/host.ClassifyStatus (never a second policy), and fail
// loudly — never silently — once the budget or MaxAttempts is exhausted.
func (c *Client) send(ctx context.Context, method string, token Token, payload []byte) error {
	deadline := time.Now().Add(host.RetryTotalCap)
	var lastErr error

	for attempt := 1; attempt <= host.MaxAttempts; attempt++ {
		result, attemptErr := c.attempt(ctx, method, token, payload)

		if attemptErr == nil && result.api.OK {
			return nil
		}

		var outcome host.Outcome
		if attemptErr != nil {
			lastErr = attemptErr
			// transportErr != nil short-circuits host.ClassifyStatus to
			// OutcomeUnknown regardless of status — the correct read for
			// "the request may or may not have reached Telegram".
			outcome = host.ClassifyStatus(0, errTransportFailed)
		} else {
			lastErr = apiError(method, result.api)
			outcome = host.ClassifyStatus(result.status, nil)
		}

		if outcome == host.OutcomeFatal {
			return lastErr
		}
		if attempt == host.MaxAttempts {
			break
		}

		delay := c.retryDelay(attempt)
		if attemptErr == nil && result.status == http.StatusTooManyRequests {
			if ra, ok := retryAfterFromResponse(result.api); ok {
				delay = ra
			}
		}
		if time.Now().Add(delay).After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("notify send: %s: exhausted retries: %w", method, lastErr)
}

// retryAfterFromResponse reads Telegram's own 429 delay from the response
// BODY's `parameters.retry_after` (ResponseParameters, verified live
// 2026-08-18) rather than an HTTP `Retry-After` header — Telegram's
// documented shape puts the flood-control delay in the JSON envelope, not
// the header internal/host/retry.go's own (GitHub-specific, unexported)
// retryAfterDelay reads. A non-positive or absent value yields ok=false so
// the caller falls back to its own RetryDelay schedule.
func retryAfterFromResponse(api apiResponse) (time.Duration, bool) {
	if api.Parameters == nil || api.Parameters.RetryAfter <= 0 {
		return 0, false
	}
	return time.Duration(api.Parameters.RetryAfter) * time.Second, true
}

// apiError builds a caller-facing error from a `ok:false` API response,
// redacting Description through internal/sensitive first (spec 04 §5: "for
// redacting API error text before it is printed") — a defensive second
// layer, since Telegram's own description text is not expected to ever
// carry a credential shape, but this package makes no such assumption.
func apiError(method string, api apiResponse) error {
	desc := api.Description
	if desc == "" {
		desc = fmt.Sprintf("status %d", api.ErrorCode)
	} else if sensitive.ContainsCredentialText(desc) {
		desc = "[redacted]"
	}
	return fmt.Errorf("notify send: %s: telegram refused: %s", method, desc)
}
