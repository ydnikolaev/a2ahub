package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/spacenotify"
)

// cmd_notify_send_test.go exercises `a2a notify send` (P4, spec
// 04-render-and-transport.md) through runNotifySend directly — the same
// "explicit parameters, no fixed root" shape cmd_notify_test.go's own
// runNotifyRender tests already use, so a test can inject an httptest fake
// Telegram without touching the real network.

func sendFixtureMessage(secret, renderer string) spacenotify.Message {
	return spacenotify.Message{
		Route:    spacenotify.Route{Chat: "-100", Locale: "en", Renderer: renderer, Secret: secret},
		Class:    spacenotify.ClassPublished,
		Artifact: &spacenotify.Artifact{ID: "XW-1", Type: "work_request", Title: "fixture", Space: "fixture-space"},
		Parties:  &spacenotify.Parties{From: "axon", To: []string{"seomatrix"}},
	}
}

func fastTestClient(srv *httptest.Server) *spacenotify.Client {
	return &spacenotify.Client{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

// TestNotifySend_DryRunProducesRecordsWithoutAPICalls is AC11 through the
// CLI surface: --dry-run never reaches the injected client.
func TestNotifySend_DryRunProducesRecordsWithoutAPICalls(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected API call in --dry-run: %s", r.URL)
	}))
	defer srv.Close()

	stdin, err := json.Marshal([]spacenotify.Message{sendFixtureMessage("TG_BOT_TOKEN", "html")})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runNotifySend(context.Background(), fastTestClient(srv), []string{"--dry-run"}, IO{
		Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0, stderr=%s", code, stderr.String())
	}

	var records []spacenotify.DeliveryRecord
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		t.Fatalf("stdout is not a valid record array: %v\n%s", err, stdout.String())
	}
	if len(records) != 1 || records[0].Outcome != "dry-run" {
		t.Fatalf("records = %+v, want exactly one dry-run record", records)
	}
}

// TestNotifySend_MissingTokenExitsNonZero is AC5 through the CLI surface.
func TestNotifySend_MissingTokenExitsNonZero(t *testing.T) {
	t.Setenv("A2A_NOTIFY_TEST_UNSET_TOKEN", "")

	stdin, err := json.Marshal([]spacenotify.Message{sendFixtureMessage("A2A_NOTIFY_TEST_UNSET_TOKEN", "html")})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected API call with no token: %s", r.URL)
	}))
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	code := runNotifySend(context.Background(), fastTestClient(srv), nil, IO{
		Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	})
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero for a missing token")
	}
	if !strings.Contains(stdout.String(), "A2A_NOTIFY_TEST_UNSET_TOKEN") {
		t.Fatalf("stdout does not name the missing secret: %s", stdout.String())
	}
}

// TestNotifySend_TokenNeverInOutput is AC6 through the CLI surface: the
// token value appears in NEITHER stdout NOR stderr, across a run that
// deliberately fails (the path most likely to leak it).
func TestNotifySend_TokenNeverInOutput(t *testing.T) {
	const secretValue = "AAAA:VERY-SECRET-TELEGRAM-TOKEN-VALUE"
	t.Setenv("A2A_NOTIFY_TEST_TOKEN", secretValue)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
	}))
	defer srv.Close()

	stdin, err := json.Marshal([]spacenotify.Message{sendFixtureMessage("A2A_NOTIFY_TEST_TOKEN", "html")})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runNotifySend(context.Background(), fastTestClient(srv), nil, IO{
		Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	})
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero for a Telegram ok:false refusal")
	}
	if strings.Contains(stdout.String(), secretValue) {
		t.Fatalf("token leaked into stdout: %s", stdout.String())
	}
	if strings.Contains(stderr.String(), secretValue) {
		t.Fatalf("token leaked into stderr: %s", stderr.String())
	}
}

// TestNotifySend_OKFalseExitsNonZero is AC4 through the CLI surface.
func TestNotifySend_OKFalseExitsNonZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	defer srv.Close()

	stdin, err := json.Marshal([]spacenotify.Message{sendFixtureMessage("A2A_NOTIFY_TEST_OKFALSE", "html")})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	t.Setenv("A2A_NOTIFY_TEST_OKFALSE", "unused-value")

	var stdout, stderr bytes.Buffer
	code := runNotifySend(context.Background(), fastTestClient(srv), nil, IO{
		Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	})
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero")
	}
}

// TestNotifySend_UsageErrors covers a bad flag and malformed stdin.
func TestNotifySend_UsageErrors(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := runNotifySend(context.Background(), nil, []string{"--not-a-flag"}, IO{
		Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("bad flag: exit = %d, want 2", code)
	}

	stdout.Reset()
	stderr.Reset()
	code = runNotifySend(context.Background(), nil, nil, IO{
		Stdin: strings.NewReader("not json"), Stdout: &stdout, Stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("malformed stdin: exit = %d, want 2", code)
	}
}

// TestNotifySend_NoSecondSpaceYAMLRead is AC13: `send` never reads
// space.yaml — it has no filesystem root parameter at all, unlike
// runNotifyRender. A dry run in an empty directory containing no
// space.yaml still succeeds, proving the route facts all come off stdin.
func TestNotifySend_NoSecondSpaceYAMLRead(t *testing.T) {
	t.Chdir(t.TempDir()) // deliberately no space.yaml here

	stdin, err := json.Marshal([]spacenotify.Message{sendFixtureMessage("TG_BOT_TOKEN", "html")})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runNotifySend(context.Background(), nil, []string{"--dry-run"}, IO{
		Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (no space.yaml needed), stderr=%s", code, stderr.String())
	}
}
