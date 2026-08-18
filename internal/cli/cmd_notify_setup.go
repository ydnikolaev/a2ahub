// cmd_notify_setup.go implements `a2a notify setup|discover|verify`
// (space-notify-2026-08 P6, spec 06). It walks a human from "no bot" to a
// green workflow run that ACTUALLY delivered, without the token ever
// passing through an agent's context (spec 06 US-2).
//
// # The host boundary (spec 06 §"Where the host calls live")
//
// This flow needs four host operations the binary had never made: read
// repo admin permission, set a repository secret, trigger a
// workflow_dispatch, and read a run's conclusion. internal/host.Host stays
// SIX methods — it gains none here. Three of the four live in
// notifySetupAdapter below, a setup-only adapter in THIS package, with the
// same injectable *http.Client/BaseURL discipline internal/release/
// source.go uses, so httptest fakes the API rather than the process. The
// fourth — setting the secret — cannot live there at all: GitHub's REST
// secret-write API requires a NaCl sealed-box encryption of the value
// before it is sent, which needs a crypto dependency this binary does not
// carry, and adding one to encrypt a value `gh` already encrypts for free
// would be exactly the "workaround when a documented mechanism exists"
// this repo's own framework-first rule forbids. `gh secret set` (already
// discovered through internal/ghauth, no second discovery path) does the
// sealing; this file only feeds it the value over stdin.
//
// The one-line consequence for spec 06 §"Where the host calls live": its
// own adapter-operations list names "set a repository secret" as one of
// the four calls the adapter makes. That line is imprecise — the adapter
// never calls the secret-write REST endpoint at all; `gh` does, through an
// exec seam (ghSecretSet), colocated here because it belongs to the same
// flow, not because it shares the adapter's transport.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/spacenotify"
)

// notifyGitHubAPIEnv lets an operator/e2e harness point every GitHub REST
// call this file makes at a fake host, mirroring cmd/a2a/wire.go's and
// internal/mcp/wire.go's own A2A_GITHUB_API knob. The duplication across
// these wiring points is ADR-001's own deliberate one (internal/mcp/
// wire.go's doc comment) applied a third time, not a new pattern.
const notifyGitHubAPIEnv = "A2A_GITHUB_API"

func notifyGitHubAPIBase() string {
	if v := os.Getenv(notifyGitHubAPIEnv); v != "" {
		return v
	}
	return "https://api.github.com"
}

// notifyTelegramSecretName is the secret space-template's a2a-notify-
// reusable.yml declares (`secrets: TG_BOT_TOKEN`, §T5) — the ONE name this
// flow ever writes or checks for presence.
// The secret's NAME, never its value — the premise of this whole flow is that
// the value never touches this process's memory except in transit to `gh`'s
// stdin. G101 cannot tell a name from a credential, so the suppression is
// declaration-scoped, exactly as internal/spacenotify's DeclaredSecret and
// internal/livee2e's env-var block already are.
//
//nolint:gosec // G101: an env-var/secret NAME, not a credential — rationale above.
const notifyTelegramSecretName = "TG_BOT_TOKEN"

// notifyWorkflowFile is the space-side caller workflow's file name — the
// `workflow_id` GitHub's REST dispatch/runs-list endpoints accept in place
// of a numeric id.
const notifyWorkflowFile = "a2a-notify.yml"

// notifyDefaultBranch is the branch `notify setup`'s proof dispatch and
// doctor's `notify-delivery` check both target.
const notifyDefaultBranch = "main"

// maxNotifyAPIResponseBytes bounds every GitHub REST response this file
// reads (rails: bounded reads everywhere — release/source.go's own
// maxListResponseBytes idiom).
const maxNotifyAPIResponseBytes = 4 << 20 // 4 MiB

// ---------------------------------------------------------------------
// redactingToken: the token never reaches a log line, an error string, or
// JSON by accident.
// ---------------------------------------------------------------------

// redactingToken holds a secret value read from the TTY. Its String()/
// GoString() are fixed so no %v/%s/%+v format verb can print the value —
// mirrors spacenotify.Token's own shape exactly (that type cannot be
// reused here: its value field is unexported to ITS package, and this
// package legitimately needs to hand the raw bytes to `gh secret set`'s
// stdin, which spacenotify.Token structurally never allows any caller to
// do). MarshalJSON is defined for the same reason: an accidental
// json.Marshal of a struct embedding this must not leak it either.
type redactingToken struct {
	value string
}

// String implements fmt.Stringer. Deliberately constant — mutate this to
// return t.value and watch TestNotifyTokenNeverLeaks_MutationRedaction
// redden (this phase's mutation-evidence receipt).
func (redactingToken) String() string { return "[redacted]" }

// GoString implements fmt.GoStringer (the %#v verb reads this instead of
// reflecting the struct, so a stray %#v cannot print the field directly).
func (redactingToken) GoString() string { return "redactingToken{[redacted]}" }

// MarshalJSON implements json.Marshaler. A struct embedding a
// redactingToken and json.Marshal'd by accident gets the fixed string,
// never the value.
func (redactingToken) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }

// Empty reports whether no value was read.
func (t redactingToken) Empty() bool { return t.value == "" }

// reader returns an io.Reader over the raw value — the ONLY way the value
// ever leaves this type, and it is used exactly once, to feed
// `gh secret set`'s stdin (runGHSecretSet) and the Telegram getUpdates
// call (notifyTelegramGetUpdates). Never logged, never placed in argv.
func (t redactingToken) reader() io.Reader { return strings.NewReader(t.value) }

// ---------------------------------------------------------------------
// Step 3: read the token from the TTY with echo disabled.
// ---------------------------------------------------------------------

// readTokenFromTTY is the real implementation of NotifyCommand.promptToken.
// Go's standard library has no terminal-echo primitive (no `golang.org/x/
// term`, and promoting the indirect one already sitting in go.mod to a
// direct import is a go.mod edit this phase's allowlist does not grant —
// see this phase's own reported deviation), so this shells out to `stty`
// with an explicit argv — the same "executed, never interpreted" rail
// internal/ghauth.Token states for `gh auth token` — disabling echo on the
// controlling terminal, reading one line, and restoring echo in a defer
// that runs even if the read fails.
func readTokenFromTTY(stdio IO) (redactingToken, error) {
	_, _ = fmt.Fprint(stdio.Stdout, "paste the bot token (input hidden): ")
	restore := notifyDisableTTYEcho()
	defer restore()
	reader := bufio.NewReader(stdio.Stdin)
	line, err := reader.ReadString('\n')
	_, _ = fmt.Fprintln(stdio.Stdout)
	if err != nil && !errors.Is(err, io.EOF) {
		return redactingToken{}, fmt.Errorf("a2a notify: read token: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return redactingToken{}, fmt.Errorf("a2a notify: no token entered")
	}
	return redactingToken{value: value}, nil
}

// notifyDisableTTYEcho best-effort disables echo on the controlling
// terminal and returns a func that restores it. It is a no-op (returning a
// no-op restore) when `stty` is unavailable or stdin is not a character
// device — piping a token through stdin in a script is still possible, it
// simply will not be echo-suppressed by THIS mechanism (the caller's own
// shell/redirect is responsible for that case).
func notifyDisableTTYEcho() func() {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return func() {}
	}
	sttyPath, err := exec.LookPath("stty")
	if err != nil {
		return func() {}
	}
	set := func(arg string) {
		cmd := exec.Command(sttyPath, arg)
		cmd.Stdin = os.Stdin
		_ = cmd.Run() // best-effort: a failed stty must never block token entry
	}
	set("-echo")
	return func() { set("echo") }
}

// ---------------------------------------------------------------------
// Step 4 (secret half): `gh secret set <name> --repo <repo>` fed from
// stdin.
// ---------------------------------------------------------------------

// runGHSecretSet is the real implementation of NotifyCommand.ghSecretSet.
// The token is written to the subprocess's STDIN ONLY — never appended to
// argv (which `ps`/shell history could observe) and never interpolated
// into an error string. `gh` is located through internal/ghauth's own
// discovery (no second `exec.LookPath("gh")` call here — see ghauth.go's
// own doc comment for why one leaf package serves both).
func runGHSecretSet(ctx context.Context, secretName, repo string, value redactingToken) error {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("a2a notify: gh not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, ghPath, "secret", "set", secretName, "--repo", repo)
	cmd.Stdin = value.reader()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// stderr is gh's own diagnostic text — it names the secret/repo,
		// never the value (gh does not echo a secret's plaintext on
		// failure; this call feeds it over stdin exactly as a human typing
		// it interactively would).
		return fmt.Errorf("a2a notify: gh secret set %s --repo %s: %w: %s", secretName, repo, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ---------------------------------------------------------------------
// gitRemote: `notify setup/verify` run at the space-repo checkout (cwd),
// exactly like `notify render` — the repo identity comes from the local
// git remote, never from `.a2a/config.yaml` (a CONSUMER project's own
// connected-space list, a different concept).
// ---------------------------------------------------------------------

type gitRemoteURLFunc func(ctx context.Context, root string) (string, error)

func gitRemoteOriginURL(ctx context.Context, root string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("a2a notify: git remote get-url origin: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------------------------------------------------------------------
// notifySetupAdapter: the CLI-package-only GitHub REST adapter.
// ---------------------------------------------------------------------

// notifySetupAdapter performs the three GitHub REST operations host.Host
// does not offer: read repo admin permission, trigger a workflow_dispatch,
// and read a run's conclusion plus best-effort delivery evidence.
// Injectable Client/BaseURL (internal/release/source.go's own convention)
// so httptest fakes the API in every test; no live network call happens
// from this package's tests.
type notifySetupAdapter struct {
	Client  *http.Client
	BaseURL string
}

func newNotifySetupAdapter(client *http.Client, baseURL string) *notifySetupAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &notifySetupAdapter{Client: client, BaseURL: baseURL}
}

func (a *notifySetupAdapter) do(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("a2a notify: encode request: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.BaseURL+path, reqBody)
	if err != nil {
		return 0, nil, fmt.Errorf("a2a notify: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("a2a notify: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxNotifyAPIResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("a2a notify: read response: %w", err)
	}
	return resp.StatusCode, raw, nil
}

// RepoAdmin reports whether token holds admin permission on owner/name —
// the precondition spec 06 step 4 checks before ever attempting `gh secret
// set`. Uses GET /repos/{owner}/{repo}'s own `permissions` object, which
// GitHub populates for the authenticated caller.
func (a *notifySetupAdapter) RepoAdmin(ctx context.Context, token, owner, name string) (bool, error) {
	status, raw, err := a.do(ctx, http.MethodGet, "/repos/"+owner+"/"+name, token, nil)
	if err != nil {
		return false, err
	}
	if status < 200 || status >= 300 {
		return false, fmt.Errorf("a2a notify: GET /repos/%s/%s: status %d: %s", owner, name, status, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		Permissions struct {
			Admin bool `json:"admin"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, fmt.Errorf("a2a notify: decode repo response: %w", err)
	}
	return decoded.Permissions.Admin, nil
}

// DispatchWorkflow triggers `workflow_dispatch` on notifyWorkflowFile with
// the given inputs (spec 06 AC 6b: the proof run passes `artifacts`
// through the dispatch INPUT rather than relying on the range fallback).
func (a *notifySetupAdapter) DispatchWorkflow(ctx context.Context, token, owner, name, ref string, inputs map[string]string) error {
	path := "/repos/" + owner + "/" + name + "/actions/workflows/" + notifyWorkflowFile + "/dispatches"
	status, raw, err := a.do(ctx, http.MethodPost, path, token, map[string]any{"ref": ref, "inputs": inputs})
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && (status < 200 || status >= 300) {
		return fmt.Errorf("a2a notify: workflow_dispatch: status %d: %s", status, strings.TrimSpace(string(raw)))
	}
	return nil
}

// notifyRun is the subset of a GitHub Actions run this file reads.
type notifyRun struct {
	ID         int64     `json:"id"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	HTMLURL    string    `json:"html_url"`
	CreatedAt  time.Time `json:"created_at"`
}

// LatestDispatchedRun returns the newest workflow_dispatch run created at
// or after since — the run `DispatchWorkflow` just triggered. found=false
// means no such run is visible yet (the caller polls).
func (a *notifySetupAdapter) LatestDispatchedRun(ctx context.Context, token, owner, name string, since time.Time) (run notifyRun, found bool, err error) {
	path := "/repos/" + owner + "/" + name + "/actions/workflows/" + notifyWorkflowFile + "/runs?event=workflow_dispatch&per_page=5"
	status, raw, err := a.do(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return notifyRun{}, false, err
	}
	if status < 200 || status >= 300 {
		return notifyRun{}, false, fmt.Errorf("a2a notify: list workflow runs: status %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		WorkflowRuns []notifyRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return notifyRun{}, false, fmt.Errorf("a2a notify: decode runs response: %w", err)
	}
	for _, r := range decoded.WorkflowRuns {
		if !r.CreatedAt.Before(since) {
			return r, true, nil
		}
	}
	return notifyRun{}, false, nil
}

// LatestRunOnBranch is doctor's `notify-delivery` read: the most recent
// a2a-notify run on branch, regardless of trigger event.
func (a *notifySetupAdapter) LatestRunOnBranch(ctx context.Context, token, owner, name, branch string) (run notifyRun, found bool, err error) {
	path := "/repos/" + owner + "/" + name + "/actions/workflows/" + notifyWorkflowFile + "/runs?branch=" + branch + "&per_page=1"
	status, raw, err := a.do(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return notifyRun{}, false, err
	}
	if status < 200 || status >= 300 {
		return notifyRun{}, false, fmt.Errorf("a2a notify: list workflow runs: status %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		WorkflowRuns []notifyRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return notifyRun{}, false, fmt.Errorf("a2a notify: decode runs response: %w", err)
	}
	if len(decoded.WorkflowRuns) == 0 {
		return notifyRun{}, false, nil
	}
	return decoded.WorkflowRuns[0], true, nil
}

// RunConclusion re-reads one run by id (the poll target while status is
// not yet "completed").
func (a *notifySetupAdapter) RunConclusion(ctx context.Context, token, owner, name string, runID int64) (notifyRun, error) {
	path := "/repos/" + owner + "/" + name + "/actions/runs/" + strconv.FormatInt(runID, 10)
	status, raw, err := a.do(ctx, http.MethodGet, path, token, nil)
	if err != nil {
		return notifyRun{}, err
	}
	if status < 200 || status >= 300 {
		return notifyRun{}, fmt.Errorf("a2a notify: get run: status %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var run notifyRun
	if err := json.Unmarshal(raw, &run); err != nil {
		return notifyRun{}, fmt.Errorf("a2a notify: decode run response: %w", err)
	}
	return run, nil
}

// DeliveredMessages reports whether runID's own job log shows at least one
// delivery record with outcome "sent" — the AC-6 "actually delivered"
// evidence.
//
// Known limitation (reported in this phase's product_findings): the
// reusable workflow (a2a-notify-reusable.yml, off this phase's allowlist)
// writes its delivery table to $GITHUB_STEP_SUMMARY, which GitHub's REST
// API does not expose today; it does NOT echo the record array to the
// job's own stdout (the `a2a notify send` step redirects it straight to a
// file). This method therefore reads the job's plain-text log — the one
// artifact the API DOES expose (`GET .../actions/jobs/{id}/logs`) — and
// looks for evidence a `sent` outcome occurred. Against the CURRENT
// reusable workflow this signal is not guaranteed to be present even on a
// real delivery, because the summary table (which DOES carry it) is never
// echoed to the captured log stream. A P5 amendment that also prints the
// records array to stdout (or a `jobs/{id}` step named for a definitive
// per-step conclusion) would close this gap; until then, `notify setup`
// treats an inconclusive log read as "unproven", never as "configured" —
// the honest-third-state discipline the spec itself asks for, applied to
// this method's own uncertainty as well as the space's.
func (a *notifySetupAdapter) DeliveredMessages(ctx context.Context, token, owner, name string, runID int64) (bool, error) {
	jobsPath := "/repos/" + owner + "/" + name + "/actions/runs/" + strconv.FormatInt(runID, 10) + "/jobs"
	status, raw, err := a.do(ctx, http.MethodGet, jobsPath, token, nil)
	if err != nil {
		return false, err
	}
	if status < 200 || status >= 300 {
		return false, fmt.Errorf("a2a notify: list run jobs: status %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		Jobs []struct {
			ID int64 `json:"id"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false, fmt.Errorf("a2a notify: decode jobs response: %w", err)
	}
	for _, job := range decoded.Jobs {
		logsPath := "/repos/" + owner + "/" + name + "/actions/jobs/" + strconv.FormatInt(job.ID, 10) + "/logs"
		logStatus, logRaw, err := a.do(ctx, http.MethodGet, logsPath, token, nil)
		if err != nil {
			continue // best-effort: one job's logs failing to fetch does not fail the whole read
		}
		if logStatus < 200 || logStatus >= 300 {
			continue
		}
		if strings.Contains(string(logRaw), `"outcome":"sent"`) || strings.Contains(string(logRaw), "| sent |") {
			return true, nil
		}
	}
	return false, nil
}

// ---------------------------------------------------------------------
// Step 7: trigger + poll + prove.
// ---------------------------------------------------------------------

// notifyProofStatus is the three-state outcome AC-6 requires: a green run
// that delivered nothing is `unproven`, never `configured`.
type notifyProofStatus string

const (
	notifyProofConfigured notifyProofStatus = "configured"
	notifyProofUnproven   notifyProofStatus = "unproven"
	notifyProofFailed     notifyProofStatus = "failed"
)

type notifyProofResult struct {
	Status     notifyProofStatus
	RunURL     string
	Conclusion string
	Detail     string
}

// notifyMaxProofPollAttempts / notifyProofPollInterval bound the wait for
// the dispatched run to appear and to complete. Tests inject a no-op sleep
// so this never costs real wall-clock time.
const (
	notifyMaxProofPollAttempts = 30
	notifyProofPollInterval    = 2 * time.Second
)

// notifyProveDelivery is step 7 in full: dispatch the workflow with the
// `artifacts` input naming a known artifact id (AC-6b), poll until the run
// appears and completes, then read whether it actually delivered (AC-6).
func (c *NotifyCommand) notifyProveDelivery(ctx context.Context, ghTok, owner, name, artifactID string) notifyProofResult {
	since := c.now().Add(-1 * time.Second)
	if err := c.setupAdapter.DispatchWorkflow(ctx, ghTok, owner, name, notifyDefaultBranch, map[string]string{"artifacts": artifactID}); err != nil {
		return notifyProofResult{Status: notifyProofFailed, Detail: "could not trigger workflow_dispatch: " + err.Error()}
	}

	var run notifyRun
	found := false
	for attempt := 0; attempt < notifyMaxProofPollAttempts; attempt++ {
		var err error
		run, found, err = c.setupAdapter.LatestDispatchedRun(ctx, ghTok, owner, name, since)
		if err != nil {
			return notifyProofResult{Status: notifyProofFailed, Detail: "could not list workflow runs: " + err.Error()}
		}
		if found {
			break
		}
		c.sleep(notifyProofPollInterval)
	}
	if !found {
		return notifyProofResult{Status: notifyProofFailed, Detail: "the triggered run never appeared within the poll window"}
	}

	for attempt := 0; attempt < notifyMaxProofPollAttempts && run.Status != "completed"; attempt++ {
		c.sleep(notifyProofPollInterval)
		var err error
		run, err = c.setupAdapter.RunConclusion(ctx, ghTok, owner, name, run.ID)
		if err != nil {
			return notifyProofResult{Status: notifyProofFailed, RunURL: run.HTMLURL, Detail: "could not read run status: " + err.Error()}
		}
	}
	if run.Status != "completed" {
		return notifyProofResult{Status: notifyProofFailed, RunURL: run.HTMLURL, Detail: "the run did not complete within the poll window"}
	}
	if run.Conclusion != "success" {
		return notifyProofResult{Status: notifyProofFailed, RunURL: run.HTMLURL, Conclusion: run.Conclusion, Detail: "the run concluded " + run.Conclusion}
	}

	delivered, err := c.setupAdapter.DeliveredMessages(ctx, ghTok, owner, name, run.ID)
	if err != nil {
		return notifyProofResult{Status: notifyProofUnproven, RunURL: run.HTMLURL, Conclusion: run.Conclusion, Detail: "the run is green but delivery could not be confirmed: " + err.Error()}
	}
	if !delivered {
		return notifyProofResult{Status: notifyProofUnproven, RunURL: run.HTMLURL, Conclusion: run.Conclusion, Detail: "the run is green but sent no message — a green run with an empty delivery set is not configuration proof"}
	}
	return notifyProofResult{Status: notifyProofConfigured, RunURL: run.HTMLURL, Conclusion: run.Conclusion, Detail: "the run delivered at least one message"}
}

// ---------------------------------------------------------------------
// The three doctor-shared checks: notify-routes, notify-secret,
// notify-delivery. Defined here (not cmd_doctor.go) so `notify verify`
// and doctor read the SAME facts through the SAME functions (spec 06
// T1: "verify... the same facts doctor reports") rather than two
// hand-synced implementations.
// ---------------------------------------------------------------------

// notifyCheckRoutes is a lightweight structural check (doctor's read path
// deliberately stays lightweight — see internal/validate/manifest.go's own
// "where it deliberately is not [enforced]" note: the full schema/policy
// engine is a CI-gate concern, not a read-path one). A route is malformed
// when it names no chat or no events; orphaned when it names a `for`
// participant with no matching ACTIVE participant in the manifest.
func notifyCheckRoutes(manifest space.Manifest) (bool, string) {
	if len(manifest.NotificationRoutes) == 0 {
		return true, ""
	}
	active := make(map[string]bool, len(manifest.Participants))
	for _, p := range manifest.Participants {
		if p.Status == "active" {
			active[p.System] = true
		}
	}
	var problems []string
	for i, route := range manifest.NotificationRoutes {
		if strings.TrimSpace(route.Chat) == "" || len(route.Events) == 0 {
			problems = append(problems, fmt.Sprintf("route[%d]: malformed (chat and at least one event are required)", i))
			continue
		}
		if route.For != "" && !active[route.For] {
			problems = append(problems, fmt.Sprintf("route[%d]: orphaned (names participant %q, not an active manifest participant)", i, route.For))
		}
	}
	if len(problems) == 0 {
		return true, ""
	}
	return false, strings.Join(problems, "; ")
}

// notifyGHSecretListFunc is the DI seam over `gh secret list --repo <repo>
// --json name`. Real implementation shells to `gh`; tests inject a fake so
// no test needs a real gh login. Never returns a secret VALUE — presence
// only (spec 06 doctor table: "presence via `gh secret list`, never the
// value").
type notifyGHSecretListFunc func(ctx context.Context, repo string) ([]string, error)

func runGHSecretList(ctx context.Context, repo string) ([]string, error) {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return nil, fmt.Errorf("a2a notify: gh not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, ghPath, "secret", "list", "--repo", repo, "--json", "name")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("a2a notify: gh secret list --repo %s: %w", repo, err)
	}
	var decoded []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		return nil, fmt.Errorf("a2a notify: decode gh secret list output: %w", err)
	}
	names := make([]string, 0, len(decoded))
	for _, s := range decoded {
		names = append(names, s.Name)
	}
	return names, nil
}

// notifyCheckSecret reports whether every secret a route names is present
// on repo (existence only — never the value).
func notifyCheckSecret(ctx context.Context, list notifyGHSecretListFunc, repo string, manifest space.Manifest) (bool, string) {
	wanted := map[string]bool{}
	for _, route := range manifest.NotificationRoutes {
		name := route.Secret
		if name == "" {
			name = notifyTelegramSecretName
		}
		wanted[name] = true
	}
	if len(wanted) == 0 {
		return true, ""
	}
	present, err := list(ctx, repo)
	if err != nil {
		return false, "could not read secrets: " + err.Error()
	}
	have := make(map[string]bool, len(present))
	for _, n := range present {
		have[n] = true
	}
	var missing []string
	for name := range wanted {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return true, ""
	}
	return false, "missing secret(s) on " + repo + ": " + strings.Join(missing, ", ")
}

// notifyCheckDelivery reports whether the most recent a2a-notify run on
// notifyDefaultBranch concluded green.
func notifyCheckDelivery(ctx context.Context, adapter *notifySetupAdapter, token, owner, name string) (bool, string) {
	run, found, err := adapter.LatestRunOnBranch(ctx, token, owner, name, notifyDefaultBranch)
	if err != nil {
		return false, "could not read the last a2a-notify run: " + err.Error()
	}
	if !found {
		return false, "no a2a-notify run has ever completed on " + notifyDefaultBranch
	}
	if run.Status != "completed" {
		return false, "the last a2a-notify run has not completed yet (" + run.Status + ")"
	}
	if run.Conclusion != "success" {
		return false, "the last a2a-notify run concluded " + run.Conclusion
	}
	return true, ""
}

// ---------------------------------------------------------------------
// Bot instructions (AC-5: locale, <= 6 lines).
// ---------------------------------------------------------------------

func notifyBotInstructions(locale string) string {
	switch locale {
	case "ru":
		return "1. Откройте https://t.me/BotFather в Telegram.\n" +
			"2. Отправьте /newbot и следуйте подсказкам.\n" +
			"3. Скопируйте выданный токен — он понадобится на следующем шаге.\n" +
			"4. Не публикуйте токен нигде, кроме этой подсказки."
	default:
		return "1. Open https://t.me/BotFather in Telegram.\n" +
			"2. Send /newbot and follow its prompts.\n" +
			"3. Copy the token it gives you — the next step asks for it.\n" +
			"4. Do not paste the token anywhere except this prompt."
	}
}

func notifyNonInteractiveRefusal(repo string) string {
	target := repo
	if target == "" {
		target = "<owner>/<repo>"
	}
	return "a2a notify setup: refusing to prompt for a token in --non-interactive mode.\n" +
		"run this yourself once you have the bot token from @BotFather:\n" +
		"  gh secret set " + notifyTelegramSecretName + " --repo " + target
}

// ---------------------------------------------------------------------
// The three sub-verbs.
// ---------------------------------------------------------------------

// notifyRouteEvents resolves the `events:` list the printed route stanza
// carries, from --events when given and from a default otherwise.
//
// It exists because the stanza used to hardcode `events: [state-change]` — a
// value `internal/validate`'s route policy does not accept (the legal set is
// human-gate | blocking | published), so `a2a notify setup`'s whole output, a
// route for a human to paste into space.yaml, could not merge. The flag was
// parsed and discarded at the same time, its own help text promising a
// subscription control the code never read. Found 2026-08-18 by enumerating
// the shipped flags against the prose, which is what release-runbook step 7
// asks for and what sampling would have missed.
//
// The vocabulary comes from internal/spacenotify's own constants rather than a
// third copy of the three strings. It is still a SECOND copy relative to
// internal/validate's switch — validate is a core package whose import set
// ADR-001 freezes, so importing spacenotify there is not a drive-by. What
// keeps the two honest is the test that runs this function's output through
// the real validator.
//
// Default: the two classes that mean a human is actually needed. `published`
// is deliberately excluded — plan §11.3 marks ordinary publication chat-✘ and
// calls widening to it a per-route, opt-in decision, so setup does not make it
// for anybody.
func notifyRouteEvents(flagValue string) ([]string, error) {
	legal := map[string]bool{
		spacenotify.ClassHumanGate: true,
		spacenotify.ClassBlocking:  true,
		spacenotify.ClassPublished: true,
	}
	if strings.TrimSpace(flagValue) == "" {
		return []string{spacenotify.ClassHumanGate, spacenotify.ClassBlocking}, nil
	}
	seen := make(map[string]bool)
	out := make([]string, 0, 3)
	for _, raw := range strings.Split(flagValue, ",") {
		event := strings.TrimSpace(raw)
		if event == "" {
			continue
		}
		if !legal[event] {
			return nil, fmt.Errorf("--events: %q is not an event class; legal classes are %s, %s, %s",
				event, spacenotify.ClassHumanGate, spacenotify.ClassBlocking, spacenotify.ClassPublished)
		}
		if seen[event] {
			return nil, fmt.Errorf("--events: %q named twice; the route policy refuses a duplicate", event)
		}
		seen[event] = true
		out = append(out, event)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--events: named no event class; omit the flag for the default, or name at least one")
	}
	return out, nil
}

const notifySetupUsage = "usage: a2a notify setup [--space <id>] [--for <participant>] [--events <list>] [--locale <ru|en>] [--non-interactive]"

func runNotifySetup(ctx context.Context, c *NotifyCommand, root string, args []string, stdio IO) int {
	fs := flag.NewFlagSet("notify setup", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	_ = fs.String("space", "", "the space id this route belongs to (cosmetic; the repo identity comes from the local git remote)")
	forParticipant := fs.String("for", "", "the participant this route serves")
	events := fs.String("events", "", "comma-separated event classes this route subscribes to (human-gate|blocking|published); default human-gate,blocking")
	locale := fs.String("locale", "en", "the human's locale for the printed instructions (ru|en)")
	nonInteractive := fs.Bool("non-interactive", false, "refuse to prompt for a token; print the one-liner instead")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, notifySetupUsage)
		return 2
	}

	_, _ = fmt.Fprintln(stdio.Stdout, notifyBotInstructions(*locale))

	remoteURL, remoteErr := c.gitRemote(ctx, root)
	repo := ""
	var owner, name string
	if remoteErr == nil {
		owner, name, _ = doctorRepoOwnerName(remoteURL)
		if owner != "" && name != "" {
			repo = owner + "/" + name
		}
	}

	if *nonInteractive {
		_, _ = fmt.Fprintln(stdio.Stdout, notifyNonInteractiveRefusal(repo))
		return 1
	}

	if remoteErr != nil || repo == "" {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify setup: could not resolve this checkout's space repo: %v\n", remoteErr)
		return 1
	}
	if !c.ghAvailable(ctx) {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a notify setup: gh is not installed or not authenticated — `gh auth login`, or re-run with --non-interactive")
		return 1
	}
	ghTok, ok := c.ghToken(ctx)
	if !ok {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a notify setup: could not read a gh token")
		return 1
	}

	// Step 4: admin + workflow scope, checked and reported SEPARATELY
	// (spec 06 §T1 step 4).
	adminOK, adminErr := c.setupAdapter.RepoAdmin(ctx, ghTok, owner, name)
	if adminErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify setup: could not check admin permission on %s: %v\n", repo, adminErr)
		return 1
	}
	if !adminOK {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify setup: this token has no admin permission on %s — a repo admin must run setup, or set %s themselves\n", repo, notifyTelegramSecretName)
		return 1
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "admin on %s: yes\n", repo)

	if c.scopes != nil {
		scopes, reported, err := c.scopes.TokenScopes(ctx, host.Credential{Token: ghTok})
		switch {
		case err != nil || !reported:
			_, _ = fmt.Fprintln(stdio.Stdout, "workflow scope: could not be verified for this credential type")
		case containsScope(scopes, spaceWorkflowScope):
			_, _ = fmt.Fprintln(stdio.Stdout, "workflow scope: yes")
		default:
			_, _ = fmt.Fprintln(stdio.Stdout, "workflow scope: no — `a2a space update` (which writes the caller workflow file) would be refused; grant it with `gh auth refresh -h github.com -s workflow` if you also own this space's scaffolding")
		}
	}

	// Step 3 (continued): prompt for the token.
	token, err := c.promptToken(stdio)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify setup: %v\n", err)
		return 1
	}

	// Step 4 (secret half).
	if err := c.ghSecretSet(ctx, notifyTelegramSecretName, repo, token); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify setup: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "secret %s set on %s\n", notifyTelegramSecretName, repo)

	// Step 5: chat discovery, using the SAME token this process just
	// read — never persisted, never re-prompted.
	_, _ = fmt.Fprintln(stdio.Stdout, "add the bot to the target chat (or forum topic) now.")
	chats, discErr := notifyTelegramGetUpdates(ctx, http.DefaultClient, defaultTelegramAPIBase, token, 10*time.Second)
	switch {
	case discErr != nil:
		_, _ = fmt.Fprintf(stdio.Stdout, "could not discover chats yet: %v — once the bot is in the chat, run `a2a notify discover`\n", discErr)
	case len(chats) == 0:
		_, _ = fmt.Fprintln(stdio.Stdout, "no chats visible yet — add the bot, then run `a2a notify discover`")
	default:
		_, _ = fmt.Fprintln(stdio.Stdout, "chats the bot can see:")
		for _, ch := range chats {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %s  %s\n", ch.ID, ch.Title)
		}
	}

	// Step 6: SURFACED, never written by this process (cmd_space.go's own
	// "the agent prints, the human runs" style — see spec 06 §5 "existing
	// patterns to reuse").
	forName := *forParticipant
	if forName == "" {
		forName = "<participant>"
	}
	routeEvents, evErr := notifyRouteEvents(*events)
	if evErr != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a notify setup: "+evErr.Error())
		return 2
	}
	_, _ = fmt.Fprintln(stdio.Stdout, "add this route to space.yaml's notification_routes and submit it through the normal write funnel (a reviewable PR):")
	_, _ = fmt.Fprintf(stdio.Stdout, "  - channel: telegram\n    chat: \"<chat id from above>\"\n    for: %s\n    events: [%s]\n    secret: %s\n", forName, strings.Join(routeEvents, ", "), notifyTelegramSecretName)

	// Step 7: proof, ONLY once the route already exists in the manifest —
	// "after the route merges" (spec 06 step 7). Before that this
	// invocation stops here; re-running `a2a notify setup` once the PR
	// has merged reaches this branch.
	raw, readErr := os.ReadFile(filepath.Join(root, "space.yaml"))
	if readErr != nil {
		_, _ = fmt.Fprintln(stdio.Stdout, "no space.yaml route to prove yet — re-run `a2a notify setup` once the route above has merged")
		return 0
	}
	manifest, parseErr := space.ParseManifest(raw)
	if parseErr != nil || len(manifest.NotificationRoutes) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "no notification route in space.yaml yet — re-run `a2a notify setup` once the route above has merged")
		return 0
	}

	artifactID, ok := notifyKnownArtifactID(ctx, c, root, manifest)
	if !ok {
		_, _ = fmt.Fprintln(stdio.Stdout, "unproven: this space has no artifact yet to prove delivery against — re-run `a2a notify setup` once one exists")
		return 0
	}

	result := c.notifyProveDelivery(ctx, ghTok, owner, name, artifactID)
	_, _ = fmt.Fprintf(stdio.Stdout, "%s: %s\n", result.Status, result.Detail)
	if result.RunURL != "" {
		_, _ = fmt.Fprintln(stdio.Stdout, result.RunURL)
	}
	if result.Status != notifyProofConfigured {
		return 1
	}
	return 0
}

// containsScope reports whether scopes names scope.
func containsScope(scopes []string, scope string) bool {
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// notifyKnownArtifactID reuses spacenotify.Render(mode=all) — the SAME
// enumeration `notify render --all` already performs — to find a real,
// currently-qualifying artifact id to prove delivery against, rather than
// writing a second directory walk (spec 06 step 7's own "why the artifact
// id is not optional" note; this is the "known artifact" it refers to).
func notifyKnownArtifactID(ctx context.Context, c *NotifyCommand, root string, manifest space.Manifest) (string, bool) {
	messages, err := spacenotify.Render(ctx, root, manifest, spacenotify.Options{Mode: spacenotify.ModeAll, Limit: 5, Now: c.now()})
	if err != nil {
		return "", false
	}
	for _, m := range messages {
		if m.Artifact != nil && m.Artifact.ID != "" {
			return m.Artifact.ID, true
		}
	}
	return "", false
}

const notifyDiscoverUsage = "usage: a2a notify discover [--timeout <duration>]"

func runNotifyDiscover(ctx context.Context, c *NotifyCommand, args []string, stdio IO) int {
	fs := flag.NewFlagSet("notify discover", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	timeout := fs.Duration("timeout", 10*time.Second, "how long to wait for the getUpdates call")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, notifyDiscoverUsage)
		return 2
	}

	var token redactingToken
	if env := os.Getenv(notifyTelegramSecretName); env != "" {
		token = redactingToken{value: env}
	} else {
		var err error
		token, err = c.promptToken(stdio)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify discover: %v\n", err)
			return 1
		}
	}

	chats, err := notifyTelegramGetUpdates(ctx, http.DefaultClient, defaultTelegramAPIBase, token, *timeout)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify discover: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(chats); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify discover: cannot encode JSON output: %v\n", err)
		return 1
	}
	return 0
}

const notifyVerifyUsage = "usage: a2a notify verify [--space <id>]"

// notifyVerifyReport is `notify verify`'s JSON output — local and unstable
// in v1 (spec 06 §7: "None beyond P1's route shape... unstable in v1").
type notifyVerifyReport struct {
	Repo           string `json:"repo,omitempty"`
	RoutesOK       bool   `json:"routes_ok"`
	RoutesDetail   string `json:"routes_detail,omitempty"`
	SecretOK       bool   `json:"secret_ok"`
	SecretDetail   string `json:"secret_detail,omitempty"`
	DeliveryOK     bool   `json:"delivery_ok"`
	DeliveryDetail string `json:"delivery_detail,omitempty"`
}

func runNotifyVerify(ctx context.Context, c *NotifyCommand, root string, args []string, stdio IO) int {
	fs := flag.NewFlagSet("notify verify", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	_ = fs.String("space", "", "cosmetic label; the repo identity comes from the local git remote")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, notifyVerifyUsage)
		return 2
	}

	raw, err := os.ReadFile(filepath.Join(root, "space.yaml"))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify verify: cannot read space.yaml: %v\n", err)
		return 1
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify verify: %v\n", err)
		return 1
	}

	report := notifyVerifyReport{}
	report.RoutesOK, report.RoutesDetail = notifyCheckRoutes(manifest)

	// A space with no routes is silently green on ALL three facts — the
	// same short-circuit doctorCheckNotifySecret/doctorCheckNotifyDelivery
	// apply (both `continue` on len(routes)==0). Without this, verify and
	// doctor would disagree on the exact space spec 06 T1 says they must
	// report identically: "verify... the same facts doctor reports".
	if len(manifest.NotificationRoutes) == 0 {
		report.SecretOK, report.DeliveryOK = true, true
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify verify: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}

	remoteURL, remoteErr := c.gitRemote(ctx, root)
	if remoteErr == nil {
		owner, name, ownerErr := doctorRepoOwnerName(remoteURL)
		if ownerErr == nil {
			report.Repo = owner + "/" + name
			report.SecretOK, report.SecretDetail = notifyCheckSecret(ctx, runGHSecretList, report.Repo, manifest)
			ghTok, ok := c.ghToken(ctx)
			if ok {
				report.DeliveryOK, report.DeliveryDetail = notifyCheckDelivery(ctx, c.setupAdapter, ghTok, owner, name)
			} else {
				report.DeliveryDetail = "could not read a gh token"
			}
		} else {
			report.SecretDetail = "could not resolve the space repo: " + ownerErr.Error()
			report.DeliveryDetail = report.SecretDetail
		}
	} else {
		report.SecretDetail = "could not resolve the local git remote: " + remoteErr.Error()
		report.DeliveryDetail = report.SecretDetail
	}

	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify verify: cannot encode JSON output: %v\n", err)
		return 1
	}
	if !report.RoutesOK || !report.SecretOK || !report.DeliveryOK {
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------
// Telegram getUpdates (discover's own transport — a raw call, not
// spacenotify.Client: that type's send-only API has no getUpdates method,
// and internal/spacenotify is off this phase's allowlist — "finished").
// ---------------------------------------------------------------------

const defaultTelegramAPIBase = "https://api.telegram.org"

// notifyChat is one chat the bot can currently see.
type notifyChat struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// notifyTelegramGetUpdates makes ONE getUpdates call restricted to
// `my_chat_member` — sufficient to see which chats the bot was added to
// without requiring the bot's group-privacy mode to be disabled (spec 06
// US-3 / T1 discover row).
func notifyTelegramGetUpdates(ctx context.Context, client *http.Client, baseURL string, token redactingToken, timeout time.Duration) ([]notifyChat, error) {
	if token.Empty() {
		return nil, fmt.Errorf("a2a notify discover: no bot token available")
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Built against a REDACTED placeholder path first, on purpose: Go's
	// net/http wraps both a malformed-URL parse failure (NewRequestWithContext)
	// and a transport failure (client.Do) in a *url.Error whose Error()
	// method includes the FULL REQUEST URL verbatim — and the Telegram Bot
	// API's own URL grammar embeds the token in the path (spacenotify/
	// telegram.go's own doc comment: "must be presented... bot<token>/
	// METHOD_NAME"). Formatting either error with %w or %v would put the
	// token in this process's own stderr/log — the exact leak this file
	// exists to prevent (spec 06 US-2, mutation-evidence item 1). So no
	// error returned below ever wraps or stringifies the underlying error;
	// every one is a fixed, token-free message.
	placeholderURL := baseURL + "/bot[redacted]/getUpdates?allowed_updates=%5B%22my_chat_member%22%5D"
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, placeholderURL, nil)
	if err != nil {
		return nil, fmt.Errorf("a2a notify discover: build request failed")
	}
	req.URL.Path = "/bot" + tokenValueForURLOnly(token) + "/getUpdates"
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("a2a notify discover: getUpdates request failed (network or DNS)")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxNotifyAPIResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("a2a notify discover: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("a2a notify discover: getUpdates status %d", resp.StatusCode)
	}
	var decoded struct {
		OK     bool `json:"ok"`
		Result []struct {
			MyChatMember *struct {
				Chat struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				} `json:"chat"`
			} `json:"my_chat_member"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("a2a notify discover: decode response: %w", err)
	}
	seen := map[string]bool{}
	var chats []notifyChat
	for _, u := range decoded.Result {
		if u.MyChatMember == nil {
			continue
		}
		id := strconv.FormatInt(u.MyChatMember.Chat.ID, 10)
		if seen[id] {
			continue
		}
		seen[id] = true
		chats = append(chats, notifyChat{ID: id, Title: u.MyChatMember.Chat.Title})
	}
	return chats, nil
}

// tokenValueForURLOnly returns token's raw value for exactly one legitimate
// purpose: building the Telegram Bot API URL, which embeds the token by
// that API's own design (verified live, spacenotify/telegram.go's own doc
// comment). It is never logged and the URL itself is never printed.
func tokenValueForURLOnly(token redactingToken) string {
	var b strings.Builder
	_, _ = io.Copy(&b, token.reader())
	return b.String()
}
