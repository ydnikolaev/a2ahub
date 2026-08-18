package spacenotify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// failOnAnyRequest is the httptest fake AC11's own testing requirement
// names: "a httptest fake that fails the test on any request".
func failOnAnyRequest(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected API call: %s %s", r.Method, r.URL.String())
	}))
}

// TestSend_DryRunMakesZeroAPICalls is AC11: --dry-run produces the SAME
// record shape with outcome:"dry-run" and makes ZERO API calls.
func TestSend_DryRunMakesZeroAPICalls(t *testing.T) {
	t.Parallel()
	srv := failOnAnyRequest(t)
	defer srv.Close()
	c := fastClient(t, srv)

	msg := sampleMessage()
	records := Send(context.Background(), c, []Message{msg}, true)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0].Outcome != "dry-run" {
		t.Fatalf("Outcome = %q, want dry-run", records[0].Outcome)
	}
	if records[0].Artifact != msg.Artifact.ID || records[0].Class != msg.Class || records[0].Chat != msg.Route.Chat {
		t.Fatalf("record = %+v, want the message's own artifact/class/chat carried through", records[0])
	}
}

// TestSend_MissingTokenRefusesBeforeSending is AC5: a missing token refuses
// BEFORE any send is attempted, and names the missing secret.
//
// MUTATION EVIDENCE: with sendOne's `if raw == ""` guard removed (falling
// straight through to client.SendHTML with an empty token), this test reds
// two ways at once: the httptest fake (which fails the test on ANY
// request) fires, and the record's outcome is no longer "failed" with the
// secret's own name in Error. Confirmed by hand; guard restored before
// finalizing this file.
func TestSend_MissingTokenRefusesBeforeSending(t *testing.T) {
	// reason: t.Setenv/t.Chdir mutate process-global state, which Go
	// refuses to combine with t.Parallel().
	srv := failOnAnyRequest(t)
	defer srv.Close()
	c := fastClient(t, srv)

	t.Setenv("TG_BOT_TOKEN_UNSET_FOR_TEST", "")
	msg := sampleMessage()
	msg.Route.Secret = "TG_BOT_TOKEN_UNSET_FOR_TEST"

	records := Send(context.Background(), c, []Message{msg}, false)
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.Outcome != "failed" {
		t.Fatalf("Outcome = %q, want failed", rec.Outcome)
	}
	if !strings.Contains(rec.Error, "TG_BOT_TOKEN_UNSET_FOR_TEST") {
		t.Fatalf("Error = %q, want it to name the missing secret", rec.Error)
	}
}

// TestSend_MixedOutcomes is AC10: one delivery record per message, in send
// order, over a run mixing sent/dry-run-equivalent/failed outcomes — the
// exact facts P5's job summary prints.
func TestSend_MixedOutcomes(t *testing.T) {
	// reason: t.Setenv/t.Chdir mutate process-global state, which Go
	// refuses to combine with t.Parallel().
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "sendRichMessage") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := fastClient(t, srv)

	t.Setenv("TG_BOT_TOKEN", "test-token-value")

	sent := sampleMessage()
	sent.Route.Secret = "TG_BOT_TOKEN"
	sent.Route.Renderer = "html"

	failed := sampleMessage()
	failed.Artifact = &Artifact{ID: "XW-2", Type: "question", Title: "will fail"}
	failed.Route.Secret = "TG_BOT_TOKEN"
	failed.Route.Renderer = "rich"

	records := Send(context.Background(), c, []Message{sent, failed}, false)
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if records[0].Outcome != "sent" || records[0].Artifact != "XW-1" {
		t.Fatalf("records[0] = %+v, want sent/XW-1", records[0])
	}
	if records[1].Outcome != "failed" || records[1].Artifact != "XW-2" {
		t.Fatalf("records[1] = %+v, want failed/XW-2", records[1])
	}
	if records[1].Error == "" {
		t.Fatalf("failed record carries no Error text")
	}
}

// TestSend_DigestArtifactLabel checks spec 04 T1's own "the artifact id, or
// `digest` plus its count" rule.
func TestSend_DigestArtifactLabel(t *testing.T) {
	t.Parallel()
	msg := Message{
		Route: Route{Chat: "-100", Renderer: "html"},
		Class: ClassDigest,
		Digest: &Digest{Count: 3, Items: []DigestItem{
			{ID: "a", Type: "t", Title: "x"},
		}},
	}
	records := Send(context.Background(), nil, []Message{msg}, true)
	if !strings.Contains(records[0].Artifact, "3") {
		t.Fatalf("Artifact = %q, want the digest's own count carried", records[0].Artifact)
	}
}

// TestSend_DryRunCarriesTheRenderedPayload is the half a dry run exists for.
//
// An earlier version returned the record BEFORE the renderer ran, so
// `--dry-run` printed a manifest of what would be skipped and nothing about
// what it would look like — while spec 04 T1 promised "renders and prints
// exactly what would be sent" and spec 05's requirement 3 promised the
// payloads reach the job summary so a layout can be reviewed before it
// reaches a chat. The operator's own review path is exactly that.
func TestSend_DryRunCarriesTheRenderedPayload(t *testing.T) {
	t.Parallel()
	msg := sampleMessage()
	msg.Route.Renderer = "html"

	recs := Send(context.Background(), nil, []Message{msg}, true)
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1", len(recs))
	}
	if recs[0].Outcome != "dry-run" {
		t.Fatalf("Outcome = %q, want dry-run", recs[0].Outcome)
	}
	if recs[0].Payload == "" {
		t.Fatal("Payload is empty — a dry run that renders nothing proves nothing about the layout it exists to show")
	}
	// The payload must be what the LIVE path would send, not a second
	// formatting of the same facts.
	if want := RenderHTML(msg, msg.Route.Locale); recs[0].Payload != want {
		t.Errorf("Payload is not byte-identical to RenderHTML's own output:\n got: %q\nwant: %q", recs[0].Payload, want)
	}

	rich := msg
	rich.Route.Renderer = "rich"
	richRecs := Send(context.Background(), nil, []Message{rich}, true)
	if richRecs[0].Payload == "" {
		t.Fatal("rich Payload is empty")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(richRecs[0].Payload), &decoded); err != nil {
		t.Fatalf("rich payload is not valid JSON: %v", err)
	}
	if _, ok := decoded["blocks"]; !ok {
		t.Errorf("rich payload carries no blocks: %s", richRecs[0].Payload)
	}
}
