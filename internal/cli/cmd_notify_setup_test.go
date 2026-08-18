package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// cmd_notify_setup_test.go exercises `a2a notify setup|discover|verify`
// (P6, spec 06) through runNotifySetup/runNotifyDiscover/runNotifyVerify
// directly, and the notifySetupAdapter through an httptest fake — never
// the real GitHub/Telegram network and never a real `gh`/`stty` subprocess.

// notifySentinelToken is the value every token-hygiene test plants and then
// greps for. It is distinctive enough that an accidental substring match
// against unrelated output is not plausible.
// The colon is deliberate, not decorative: a real Telegram token has the
// shape "123456789:AAH...", and the colon is exactly the character Go's
// url.URL could re-encode when a raw value is assigned into .Path without
// .RawPath — a sentinel with no interesting characters would test nothing
// about the URL BUILDER, only that a plain string round-trips.
const notifySentinelToken = "123456789:SENTINEL-TG-TOKEN-DO-NOT-LEAK-9f31c2"

func fakeScopeChecker(scopes []string, reported bool, err error) spaceScopeChecker {
	return fakeScopes{scopes: scopes, reported: reported, err: err}
}

type fakeScopes struct {
	scopes   []string
	reported bool
	err      error
}

func (f fakeScopes) TokenScopes(context.Context, host.Credential) ([]string, bool, error) {
	return f.scopes, f.reported, f.err
}

// noopSleep is injected everywhere a poll loop would otherwise cost real
// wall-clock time.
func noopSleep(time.Duration) {}

// newTestNotifyCommand builds a NotifyCommand with every P6 seam faked,
// pointed at srv (if non-nil) for every GitHub REST call.
func newTestNotifyCommand(t *testing.T, srv *httptest.Server) *NotifyCommand {
	t.Helper()
	c := &NotifyCommand{
		now:         func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) },
		ghAvailable: func(context.Context) bool { return true },
		ghToken:     func(context.Context) (string, bool) { return "gh-token", true },
		scopes:      fakeScopeChecker([]string{"repo", "workflow"}, true, nil),
		gitRemote: func(context.Context, string) (string, error) {
			return "https://github.com/acme/space-demo.git", nil
		},
		sleep: noopSleep,
	}
	if srv != nil {
		c.setupAdapter = newNotifySetupAdapter(srv.Client(), srv.URL)
	} else {
		c.setupAdapter = newNotifySetupAdapter(http.DefaultClient, "http://127.0.0.1:0")
	}
	return c
}

// ---------------------------------------------------------------------
// AC-2: --non-interactive refuses and prints the human's one-liner.
// ---------------------------------------------------------------------

func TestNotifySetup_NonInteractiveRefuses(t *testing.T) {
	t.Parallel()
	c := newTestNotifyCommand(t, nil)
	c.promptToken = func(IO) (redactingToken, error) {
		t.Fatal("--non-interactive must never prompt for a token")
		return redactingToken{}, nil
	}
	c.ghSecretSet = func(context.Context, string, string, redactingToken) error {
		t.Fatal("--non-interactive must never call gh secret set")
		return nil
	}
	var stdout, stderr bytes.Buffer
	code := runNotifySetup(context.Background(), c, t.TempDir(), []string{"--non-interactive"}, IO{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (refusal)", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "--non-interactive") {
		t.Errorf("output does not name --non-interactive: %q", out)
	}
	if !strings.Contains(out, "gh secret set") || !strings.Contains(out, "acme/space-demo") {
		t.Errorf("output does not print the human's one-liner naming the repo: %q", out)
	}
}

// ---------------------------------------------------------------------
// AC-1/AC-3: the token appears in NO stream, file or error the flow
// produces — the capture test over the whole flow.
// ---------------------------------------------------------------------

func TestNotifySetup_TokenNeverLeaksAcrossFullFlow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Every request this flow's adapter makes MUST be token-free in its
		// URL and body — the gh token is sent as a Bearer header, never a
		// query string, and the TELEGRAM token never reaches this server at
		// all (it goes to Telegram's own host, which this test does not
		// point discovery at — see the discovery-failure assertion below).
		if strings.Contains(r.URL.String(), notifySentinelToken) {
			t.Errorf("sentinel token leaked into GitHub API request URL: %s", r.URL)
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/repos/acme/space-demo"):
			_, _ = w.Write([]byte(`{"permissions":{"admin":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	var capturedRepo, capturedName string
	var capturedBytes []byte
	c := newTestNotifyCommand(t, srv)
	c.promptToken = func(IO) (redactingToken, error) {
		return redactingToken{value: notifySentinelToken}, nil
	}
	c.ghSecretSet = func(_ context.Context, name, repo string, value redactingToken) error {
		capturedName, capturedRepo = name, repo
		raw, err := io.ReadAll(value.reader())
		if err != nil {
			t.Fatal(err)
		}
		capturedBytes = raw
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := runNotifySetup(context.Background(), c, dir, nil, IO{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")})
	// No space.yaml in the temp dir: the flow stops after step 6, exit 0
	// ("no route to prove yet").
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}

	// Positive control: the token DID travel through the legitimate stdin
	// channel to gh secret set.
	if string(capturedBytes) != notifySentinelToken {
		t.Fatalf("gh secret set never received the token via stdin: got %q", capturedBytes)
	}
	if capturedName != notifyTelegramSecretName || capturedRepo != "acme/space-demo" {
		t.Fatalf("gh secret set name/repo = %q/%q, want %q/acme/space-demo", capturedName, capturedRepo, notifyTelegramSecretName)
	}

	// Negative: the sentinel appears NOWHERE else — not stdout, not
	// stderr, not any error string either buffer would have captured.
	combined := stdout.String() + "\n" + stderr.String()
	if strings.Contains(combined, notifySentinelToken) {
		t.Fatalf("token leaked into stdout/stderr:\nSTDOUT:\n%s\nSTDERR:\n%s", stdout.String(), stderr.String())
	}

	// AC-3: the token is never written to any local file. Walk every file
	// this run's own working directory holds (the one place this process
	// could have persisted it) and assert none contains it.
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(raw), notifySentinelToken) {
			t.Errorf("token leaked into local file %s", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
}

// TestRedactingToken_NeverFormatsRawValue is the redaction primitive's own
// receipt: every fmt verb and json.Marshal produce the fixed string, never
// the value.
func TestRedactingToken_NeverFormatsRawValue(t *testing.T) {
	t.Parallel()
	tok := redactingToken{value: notifySentinelToken}
	forms := []string{
		fmt.Sprintf("%v", tok),
		fmt.Sprintf("%s", tok), //nolint:staticcheck // reason: exercising the %s verb specifically, not just %v — gosimple merged into staticcheck in golangci-lint v2, so the old linter name suppressed nothing
		fmt.Sprintf("%+v", tok),
		fmt.Sprintf("%#v", tok),
	}
	raw, err := json.Marshal(tok)
	if err != nil {
		t.Fatal(err)
	}
	forms = append(forms, string(raw))
	for _, f := range forms {
		if strings.Contains(f, notifySentinelToken) {
			t.Fatalf("a format verb leaked the raw token: %q", f)
		}
	}
}

// ---------------------------------------------------------------------
// admin/workflow-scope preconditions, reported separately (spec 06 step 4).
// ---------------------------------------------------------------------

func TestNotifySetup_RefusesWithoutAdmin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"permissions":{"admin":false}}`))
	}))
	defer srv.Close()

	c := newTestNotifyCommand(t, srv)
	c.promptToken = func(IO) (redactingToken, error) {
		t.Fatal("must not prompt for a token before the admin check passes")
		return redactingToken{}, nil
	}
	var stdout, stderr bytes.Buffer
	code := runNotifySetup(context.Background(), c, dir, nil, IO{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "admin") {
		t.Errorf("stderr does not name the admin refusal: %q", stderr.String())
	}
}

func TestNotifySetup_WorkflowScopeReportedSeparatelyFromAdmin(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"permissions":{"admin":true}}`))
	}))
	defer srv.Close()

	c := newTestNotifyCommand(t, srv)
	c.scopes = fakeScopeChecker([]string{"repo"}, true, nil) // admin yes, workflow scope NO
	c.promptToken = func(IO) (redactingToken, error) { return redactingToken{value: "x"}, nil }
	c.ghSecretSet = func(context.Context, string, string, redactingToken) error { return nil }

	var stdout, stderr bytes.Buffer
	code := runNotifySetup(context.Background(), c, dir, nil, IO{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")})
	if code != 0 {
		t.Fatalf("missing workflow scope must NOT block the secret write (admin is the only hard precondition); exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "admin on acme/space-demo: yes") {
		t.Errorf("admin fact not reported: %q", out)
	}
	if !strings.Contains(out, "workflow scope: no") {
		t.Errorf("workflow-scope fact not reported separately: %q", out)
	}
}

// ---------------------------------------------------------------------
// AC-6/AC-6b: unproven vs configured, and the artifacts dispatch input.
// ---------------------------------------------------------------------

// notifyProofFakeAPI serves the dispatch/runs/jobs/logs sequence
// notifyProveDelivery drives. delivered controls whether the job log the
// fake serves contains a "sent" outcome marker.
func notifyProofFakeAPI(t *testing.T, delivered bool) (*httptest.Server, *[]string) {
	t.Helper()
	var dispatchedInputs []string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/space-demo/actions/workflows/a2a-notify.yml/dispatches", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ref    string            `json:"ref"`
			Inputs map[string]string `json:"inputs"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		dispatchedInputs = append(dispatchedInputs, body.Inputs["artifacts"])
		if body.Inputs["artifacts"] == "" {
			t.Error("workflow_dispatch was sent with no `artifacts` input (AC-6b)")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/repos/acme/space-demo/actions/workflows/a2a-notify.yml/runs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":42,"status":"completed","conclusion":"success","html_url":"https://github.com/acme/space-demo/actions/runs/42","created_at":"2026-08-18T12:00:01Z"}]}`))
	})
	mux.HandleFunc("/repos/acme/space-demo/actions/runs/42/jobs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"jobs":[{"id":7}]}`))
	})
	mux.HandleFunc("/repos/acme/space-demo/actions/jobs/7/logs", func(w http.ResponseWriter, r *http.Request) {
		if delivered {
			_, _ = w.Write([]byte("2026-08-18T12:00:05Z | artifact | class | chat | renderer | outcome |\n2026-08-18T12:00:05Z | XW-1 | published | -100 | text | sent |\n"))
		} else {
			_, _ = w.Write([]byte("2026-08-18T12:00:05Z 0 messages — no routes configured, or nothing qualified.\n"))
		}
	})
	srv := httptest.NewServer(mux)
	return srv, &dispatchedInputs
}

func TestNotifyProveDelivery_ConfiguredWhenDelivered(t *testing.T) {
	t.Parallel()
	srv, inputs := notifyProofFakeAPI(t, true)
	defer srv.Close()
	c := newTestNotifyCommand(t, srv)

	result := c.notifyProveDelivery(context.Background(), "gh-token", "acme", "space-demo", "XW-1")
	if result.Status != notifyProofConfigured {
		t.Fatalf("status = %q, want configured; detail=%s", result.Status, result.Detail)
	}
	if len(*inputs) == 0 || (*inputs)[0] != "XW-1" {
		t.Fatalf("dispatch artifacts input = %v, want [XW-1]", *inputs)
	}
}

// TestNotifyProveDelivery_UnprovenOnEmptyDeliverySet is AC-6: a green run
// with an EMPTY delivery set must report unproven, never configured.
func TestNotifyProveDelivery_UnprovenOnEmptyDeliverySet(t *testing.T) {
	t.Parallel()
	srv, _ := notifyProofFakeAPI(t, false)
	defer srv.Close()
	c := newTestNotifyCommand(t, srv)

	result := c.notifyProveDelivery(context.Background(), "gh-token", "acme", "space-demo", "XW-1")
	if result.Status != notifyProofUnproven {
		t.Fatalf("status = %q, want unproven for a green run with nothing delivered; detail=%s", result.Status, result.Detail)
	}
}

func TestNotifyProveDelivery_FailedOnRedConclusion(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/space-demo/actions/workflows/a2a-notify.yml/dispatches", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/repos/acme/space-demo/actions/workflows/a2a-notify.yml/runs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":9,"status":"completed","conclusion":"failure","html_url":"https://x/9","created_at":"2026-08-18T12:00:01Z"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := newTestNotifyCommand(t, srv)

	result := c.notifyProveDelivery(context.Background(), "gh-token", "acme", "space-demo", "XW-1")
	if result.Status != notifyProofFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
}

// ---------------------------------------------------------------------
// discover: my_chat_member alone is sufficient (US-3, group privacy mode
// stays on).
// ---------------------------------------------------------------------

func TestNotifyTelegramGetUpdates_MyChatMemberOnly(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Expected: the token (including its colon) IS part of the
		// Telegram URL path by that API's own design — assert it arrived
		// intact rather than re-encoded or truncated.
		if !strings.Contains(r.URL.Path, notifySentinelToken) {
			t.Errorf("request path does not carry the token intact: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[
			{"update_id":1,"my_chat_member":{"chat":{"id":-1001,"title":"Ops"}}},
			{"update_id":2,"message":{"text":"unrelated update, no my_chat_member"}},
			{"update_id":3,"my_chat_member":{"chat":{"id":-1001,"title":"Ops"}}}
		]}`))
	}))
	defer srv.Close()

	chats, err := notifyTelegramGetUpdates(context.Background(), srv.Client(), srv.URL, redactingToken{value: notifySentinelToken}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].ID != "-1001" || chats[0].Title != "Ops" {
		t.Fatalf("chats = %+v, want exactly one deduplicated -1001/Ops entry", chats)
	}
}

func TestNotifyTelegramGetUpdates_NoUpdatesYet(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	chats, err := notifyTelegramGetUpdates(context.Background(), srv.Client(), srv.URL, redactingToken{value: "t"}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 0 {
		t.Fatalf("chats = %+v, want none", chats)
	}
}

// TestNotifyTelegramGetUpdates_TransportErrorNeverNamesTheURL is the
// discover-specific half of the token-hygiene requirement: a network
// failure's error string must never embed the token-bearing request URL.
func TestNotifyTelegramGetUpdates_TransportErrorNeverNamesTheURL(t *testing.T) {
	t.Parallel()
	badClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})}
	_, err := notifyTelegramGetUpdates(context.Background(), badClient, "https://api.telegram.org", redactingToken{value: notifySentinelToken}, time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), notifySentinelToken) {
		t.Fatalf("transport error leaked the token: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ---------------------------------------------------------------------
// verify + the three shared checks (also doctor's).
// ---------------------------------------------------------------------

func writeNotifySpaceYAML(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const notifyManifestFixture = `schema: space/v1
space: demo
min_binary_version: 0.22.0
gates: default
participants:
  - system: axon
    org: acme
    section: axon/
    owners: [alice]
    status: active
    joined: "2026-01-01"
notification_routes:
  - channel: telegram
    chat: "-1001"
    for: axon
    events: [human-gate, blocking]
    secret: TG_BOT_TOKEN
`

func TestNotifyVerify_GreenPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNotifySpaceYAML(t, dir, notifyManifestFixture)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"status":"completed","conclusion":"success"}]}`))
	}))
	defer srv.Close()
	c := newTestNotifyCommand(t, srv)
	c.ghSecretSet = func(context.Context, string, string, redactingToken) error { return nil }
	// notifyCheckSecret shells to `gh` for real by default; verify's own
	// call site (runNotifyVerify) hard-codes runGHSecretList, so this test
	// exercises the code path through notifyCheckSecret directly instead
	// (see TestNotifyCheckSecret_* below) and drives runNotifyVerify only
	// for the routes+delivery halves it can fake via c.

	var stdout, stderr bytes.Buffer
	code := runNotifyVerify(context.Background(), c, dir, nil, IO{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")})
	var report notifyVerifyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !report.RoutesOK {
		t.Errorf("routes_ok = false, detail=%s", report.RoutesDetail)
	}
	if !report.DeliveryOK {
		t.Errorf("delivery_ok = false, detail=%s", report.DeliveryDetail)
	}
	_ = code // secret_ok depends on the real `gh` binary in this environment; not asserted here.
}

// TestNotifyVerify_NoRoutesAgreesWithDoctor is spec 06 T1's own requirement
// ("verify... the same facts doctor reports"): a space with NO
// notification_routes must be silently green on all three facts, exactly
// as doctorCheckNotifySecret/doctorCheckNotifyDelivery already short-circuit
// (both `continue` on len(routes)==0) — never a false red naming a secret
// or a run that was never expected to exist.
func TestNotifyVerify_NoRoutesAgreesWithDoctor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeNotifySpaceYAML(t, dir, strings.ReplaceAll(notifyManifestFixture,
		"notification_routes:\n  - channel: telegram\n    chat: \"-1001\"\n    for: axon\n    events: [human-gate, blocking]\n    secret: TG_BOT_TOKEN\n", ""))

	c := newTestNotifyCommand(t, nil)
	c.ghToken = func(context.Context) (string, bool) {
		t.Fatal("a no-route space must never resolve a gh token (nothing to check)")
		return "", false
	}
	var stdout, stderr bytes.Buffer
	code := runNotifyVerify(context.Background(), c, dir, nil, IO{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader("")})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", code, stderr.String())
	}
	var report notifyVerifyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !report.RoutesOK || !report.SecretOK || !report.DeliveryOK {
		t.Fatalf("a no-route space must be all-green: %+v", report)
	}
}

func TestNotifyCheckRoutes(t *testing.T) {
	t.Parallel()
	// malformed: empty chat.
	m := mustParseNotifyManifest(t, strings.ReplaceAll(notifyManifestFixture, `chat: "-1001"`, `chat: ""`))
	if ok, _ := notifyCheckRoutes(m); ok {
		t.Error("a route with no chat must be reported malformed")
	}
	// orphaned: `for` names no active participant.
	m = mustParseNotifyManifest(t, strings.ReplaceAll(notifyManifestFixture, "for: axon", "for: ghost-system"))
	if ok, msg := notifyCheckRoutes(m); ok {
		t.Error("a route naming an unknown participant must be reported orphaned")
	} else if !strings.Contains(msg, "orphaned") {
		t.Errorf("message does not say orphaned: %q", msg)
	}
	// green.
	m = mustParseNotifyManifest(t, notifyManifestFixture)
	if ok, msg := notifyCheckRoutes(m); !ok {
		t.Errorf("a well-formed route must pass: %s", msg)
	}
}

func TestNotifyCheckSecret(t *testing.T) {
	t.Parallel()
	m := mustParseNotifyManifest(t, notifyManifestFixture)
	present := func(context.Context, string) ([]string, error) { return []string{"TG_BOT_TOKEN"}, nil }
	if ok, msg := notifyCheckSecret(context.Background(), present, "acme/demo", m); !ok {
		t.Errorf("secret present must pass: %s", msg)
	}
	absent := func(context.Context, string) ([]string, error) { return []string{"OTHER"}, nil }
	if ok, msg := notifyCheckSecret(context.Background(), absent, "acme/demo", m); ok {
		t.Error("a missing route secret must fail")
	} else if !strings.Contains(msg, "TG_BOT_TOKEN") {
		t.Errorf("message does not name the missing secret: %q", msg)
	}
}

func TestNotifyCheckDelivery(t *testing.T) {
	t.Parallel()
	green := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"status":"completed","conclusion":"success"}]}`))
	}))
	defer green.Close()
	adapter := newNotifySetupAdapter(green.Client(), green.URL)
	if ok, msg := notifyCheckDelivery(context.Background(), adapter, "t", "acme", "demo"); !ok {
		t.Errorf("a green last run must pass: %s", msg)
	}

	red := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[{"id":1,"status":"completed","conclusion":"failure"}]}`))
	}))
	defer red.Close()
	adapter = newNotifySetupAdapter(red.Client(), red.URL)
	if ok, _ := notifyCheckDelivery(context.Background(), adapter, "t", "acme", "demo"); ok {
		t.Error("a failed last run must fail")
	}

	none := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"workflow_runs":[]}`))
	}))
	defer none.Close()
	adapter = newNotifySetupAdapter(none.Client(), none.URL)
	if ok, msg := notifyCheckDelivery(context.Background(), adapter, "t", "acme", "demo"); ok {
		t.Error("no run ever must fail")
	} else if !strings.Contains(msg, "has ever completed") {
		t.Errorf("message does not say so: %q", msg)
	}
}

// TestNotifyBotInstructions_LocaleAndLength is AC-5: printed in the human's
// locale, in <= 6 lines, and the ru/en variants actually differ.
func TestNotifyBotInstructions_LocaleAndLength(t *testing.T) {
	t.Parallel()
	en := notifyBotInstructions("en")
	ru := notifyBotInstructions("ru")
	for locale, text := range map[string]string{"en": en, "ru": ru, "unknown-falls-back-to-en": notifyBotInstructions("xx")} {
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) > 6 {
			t.Errorf("%s instructions are %d lines, want <= 6:\n%s", locale, len(lines), text)
		}
		if !strings.Contains(text, "BotFather") {
			t.Errorf("%s instructions do not mention BotFather: %q", locale, text)
		}
	}
	if en == ru {
		t.Fatal("ru and en instructions must differ — --locale ru must actually switch the text")
	}
	if !strings.Contains(ru, "Откройте") {
		t.Errorf("ru instructions are not actually in Russian: %q", ru)
	}
}

func mustParseNotifyManifest(t *testing.T, raw string) space.Manifest {
	t.Helper()
	m, err := space.ParseManifest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestNotifySetupPrintsARouteTheValidatorAccepts runs the exact stanza
// `a2a notify setup` prints through the REAL manifest policy validator.
//
// This is the assertion whose absence let a defect ship: the stanza hardcoded
// `events: [state-change]`, which is not one of the legal classes
// (human-gate | blocking | published), so the route `notify setup` handed a
// human to paste into space.yaml could never merge — and the test that covered
// this output asserted the literal string instead, freezing the bug rather than
// catching it. The notify fixtures carried the same illegal value and no test
// in this package ever ran a validator over them.
//
// Asserting a literal proves the code still does what it does. Asserting the
// output VALIDATES proves it does what it is for. It also keeps
// internal/spacenotify's class constants and internal/validate's own switch
// honest with each other without either package importing the other — ADR-001
// freezes validate's import set, so that agreement cannot be a shared symbol
// and has to be a test.
func TestNotifySetupPrintsARouteTheValidatorAccepts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		flag string
		want string
	}{
		{name: "default", flag: "", want: "human-gate, blocking"},
		{name: "explicit single", flag: "published", want: "published"},
		{name: "explicit set", flag: "human-gate,published", want: "human-gate, published"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			events, err := notifyRouteEvents(test.flag)
			if err != nil {
				t.Fatalf("notifyRouteEvents(%q) error = %v", test.flag, err)
			}
			if got := strings.Join(events, ", "); got != test.want {
				t.Fatalf("events = %q, want %q", got, test.want)
			}

			manifest := "schema: space/v1\nspace: demo\nmin_binary_version: 0.22.0\ngates: default\n" +
				"participants:\n  - system: axon\n    org: acme\n    section: axon/\n    owners: [alice]\n" +
				"    status: active\n    joined: \"2026-01-01\"\n" +
				"notification_routes:\n  - channel: telegram\n    chat: \"-1001\"\n    for: axon\n" +
				"    events: [" + strings.Join(events, ", ") + "]\n    secret: TG_BOT_TOKEN\n"

			corpus, err := schema.Load()
			if err != nil {
				t.Fatalf("schema.Load: %v", err)
			}
			engine := validate.New(corpus)
			result, err := engine.ValidateManifestPolicy([]byte(manifest))
			if err != nil {
				t.Fatalf("ValidateManifestPolicy: %v", err)
			}
			for _, v := range result.Violations {
				t.Errorf("the stanza `a2a notify setup` prints does not validate: %s %s %s", v.Code, v.Path, v.Message)
			}
		})
	}
}

// TestNotifyRouteEventsRefusesWhatThePolicyWouldRefuse keeps the flag from
// promising a subscription the route policy will not accept — the shape the
// discarded `--events` flag had, where the help text claimed a control the
// code never read.
func TestNotifyRouteEventsRefusesWhatThePolicyWouldRefuse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		flag string
		want string
	}{
		{name: "unknown class", flag: "state-change", want: "is not an event class"},
		{name: "the digest class is not subscribable", flag: "digest", want: "is not an event class"},
		{name: "duplicate", flag: "blocking,blocking", want: "named twice"},
		{name: "only separators", flag: ",,", want: "named no event class"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := notifyRouteEvents(test.flag)
			if err == nil {
				t.Fatalf("notifyRouteEvents(%q) = nil error, want a refusal naming %q", test.flag, test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("notifyRouteEvents(%q) error = %q, want it to name %q", test.flag, err, test.want)
			}
		})
	}
}
