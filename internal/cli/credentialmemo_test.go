package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// --- Unit tests of the decorator itself ---

func TestMemoizeCredentialResolverCallsOncePerDistinctInput(t *testing.T) {
	t.Parallel()
	var calls int
	raw := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		calls++
		return host.Credential{Token: "tok"}, nil
	}
	memo := memoizeCredentialResolver(raw)

	ref := space.CredentialReference{Kind: "env", Env: "A2A_TOKEN_GETVISA"}
	c1, err1 := memo(context.Background(), "A2A_TOKEN_GETVISA", ref)
	c2, err2 := memo(context.Background(), "A2A_TOKEN_GETVISA", ref)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if c1 != c2 || c1.Token != "tok" {
		t.Fatalf("same-input calls returned different credentials: %+v, %+v", c1, c2)
	}
	if calls != 1 {
		t.Fatalf("wrapped resolver called %d times for the same input, want 1", calls)
	}

	other := space.CredentialReference{Kind: "env", Env: "A2A_TOKEN_OTHER"}
	if _, err := memo(context.Background(), "A2A_TOKEN_OTHER", other); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("wrapped resolver called %d times across two distinct inputs, want 2", calls)
	}
}

func TestMemoizeCredentialResolverCachesTheErrorToo(t *testing.T) {
	t.Parallel()
	var calls int
	wantErr := errors.New("cmd:gh auth token: exit status 1")
	raw := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		calls++
		return host.Credential{}, wantErr
	}
	memo := memoizeCredentialResolver(raw)
	ref := space.CredentialReference{Kind: "cmd", Argv: []string{"gh", "auth", "token"}}

	for i := 0; i < 6; i++ {
		_, err := memo(context.Background(), "A2A_TOKEN_GETVISA", ref)
		if !errors.Is(err, wantErr) {
			t.Fatalf("call %d: err = %v, want %v", i, err, wantErr)
		}
	}
	if calls != 1 {
		t.Fatalf("a failing resolver was invoked %d times for the same input, want 1 (the error must be cached, not retried)", calls)
	}
}

func TestMemoizeCredentialResolverKeysOnKindEnvVarAndArgv(t *testing.T) {
	t.Parallel()
	// Two inputs that differ only in Kind/Env/Argv must resolve
	// independently — this is the "different spaces resolve to different
	// credentials" guarantee the memoiser must preserve.
	var calls []space.CredentialReference
	raw := func(_ context.Context, _ string, ref space.CredentialReference) (host.Credential, error) {
		calls = append(calls, ref)
		return host.Credential{Token: ref.Env + strings.Join(ref.Argv, "-")}, nil
	}
	memo := memoizeCredentialResolver(raw)

	envRef := space.CredentialReference{Kind: "env", Env: "A2A_TOKEN_GETVISA"}
	cmdRef := space.CredentialReference{Kind: "cmd", Argv: []string{"gh", "auth", "token"}}
	otherCmdRef := space.CredentialReference{Kind: "cmd", Argv: []string{"gh", "auth", "token", "--hostname", "example.com"}}

	if _, err := memo(context.Background(), "", envRef); err != nil {
		t.Fatal(err)
	}
	if _, err := memo(context.Background(), "", cmdRef); err != nil {
		t.Fatal(err)
	}
	if _, err := memo(context.Background(), "", otherCmdRef); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("wrapped resolver called %d times across three distinct references, want 3 (per-space resolution must stay independent)", len(calls))
	}

	// Repeating the FIRST reference must still hit the cache rather than
	// resolving a fourth time.
	if _, err := memo(context.Background(), "", envRef); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("wrapped resolver called %d times after a repeat of the first input, want 3", len(calls))
	}
}

func TestMemoizeCredentialResolverIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		calls int
	)
	raw := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		return host.Credential{Token: "tok"}, nil
	}
	memo := memoizeCredentialResolver(raw)
	ref := space.CredentialReference{Kind: "env", Env: "A2A_TOKEN_GETVISA"}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := memo(context.Background(), "A2A_TOKEN_GETVISA", ref); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("wrapped resolver called %d times under concurrent use, want 1", calls)
	}
}

func TestMemoizeCredentialResolverNilInNilOut(t *testing.T) {
	t.Parallel()
	if got := memoizeCredentialResolver(nil); got != nil {
		t.Fatal("memoizeCredentialResolver(nil) must return nil, so mirrorCredential's own nil check keeps working")
	}
}

// --- Constructor wiring: pins that NewDoctorCommand actually installs the
// memoising wrapper, not just that the wrapper works in isolation. ---

func TestNewDoctorCommandMemoizesCredentialResolver(t *testing.T) {
	// t.Setenv forbids t.Parallel().
	t.Setenv("A2A_TOKEN_GETVISA", "first")
	cmd := NewDoctorCommand(host.NewFakeHost(), "0.1.0", "/unused/.a2a/config.yaml", "/unused/machine.yaml", t.TempDir())

	c1, err := cmd.resolveCredential(context.Background(), "A2A_TOKEN_GETVISA", space.CredentialReference{})
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if c1.Token != "first" {
		t.Fatalf("first resolve token = %q, want %q", c1.Token, "first")
	}

	t.Setenv("A2A_TOKEN_GETVISA", "second")
	c2, err := cmd.resolveCredential(context.Background(), "A2A_TOKEN_GETVISA", space.CredentialReference{})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	// A non-memoised constructor would read the environment again and
	// return "second" here. NewDoctorCommand's resolveCredential field must
	// be the memoising wrapper, so the second call is served from cache.
	if c2.Token != c1.Token {
		t.Fatalf("resolveCredential re-resolved instead of returning the memoised value: first=%q second=%q", c1.Token, c2.Token)
	}
}

// --- Acceptance test: the claim this wave exists to pin. A full DoctorCommand
// Run over N configured spaces must resolve each space's credential exactly
// N times total, not once per credential-consuming check — one resolution
// per space across the six call sites this brief names in cmd_doctor.go
// (credentials, space access, scaffolding-current's "who can fix it",
// auto-merge, stuck-green-PRs, CODEOWNERS), not once per site.
//
// A SEVENTH site was found empirically while building this test:
// visibility.go's doctorVisibilityRows (the "repository visibility" report
// row) reads the same c.resolveCredential field and, with the
// RepoVisibilityReader capability wired (as host.GitHubHost is here), Run
// exercises it unconditionally whenever cfg.Spaces is non-empty. It was out
// of this brief's allowlist to edit, but it needed no edit: it goes through
// the exact same seam this decorator wraps, so it is fixed for free and this
// test's callCount below is proof of that, not merely the six named sites.

// credentialMemoAcceptanceServer answers every GitHub REST endpoint
// doctor's six credential-consuming checks exercise, for exactly the two
// repos this test connects: GET /repos/{owner}/{repo} (AutoMergeAllowed,
// RepoPermissions — reports push access so scaffolding names "run `a2a
// space update`"), GET .../pulls (ListOpenPRs — no open PRs), and GET
// .../codeowners/errors (CodeownersErrors — no errors). Reusing one fake
// server for all three keeps every optional Host capability
// (doctorRepoSettingsReader, doctorSpaceScaffoldingReader,
// host.OpenPRLister, doctorCodeownersReader) simultaneously wired, which is
// what makes all six call sites reachable from one Run.
func credentialMemoAcceptanceServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/codeowners/errors"):
			_, _ = w.Write([]byte(`{"errors":[]}`))
		default:
			_, _ = w.Write([]byte(`{"allow_auto_merge":true,"permissions":{"admin":false,"push":true,"pull":true}}`))
		}
	}))
}

// credentialMemoAcceptanceReadFile fakes every connected space's mirror
// working tree with ONLY space.yaml present (every other managed file
// os.ErrNotExist) — a guaranteed-behind scaffolding plan (matching this
// package's own doctorScaffoldingMirrorReadFile precedent), which is what
// makes doctorCheckScaffoldingCurrent reach its resolveCredential call at
// all: that site only fires when the plan actually has writes.
func credentialMemoAcceptanceReadFile(spaceIDs ...string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		slash := filepath.ToSlash(path)
		for _, id := range spaceIDs {
			if strings.Contains(slash, "/"+id+"/") && strings.HasSuffix(slash, "space.yaml") {
				return []byte(fmt.Sprintf(
					"schema: manifest/v1\nspace: %s\nmin_binary_version: 0.0.1\nparticipants: []\n", id,
				)), nil
			}
		}
		return nil, os.ErrNotExist
	}
}

func TestDoctorRunResolvesCredentialOncePerSpaceNotOncePerCheck(t *testing.T) {
	t.Parallel()
	srv := credentialMemoAcceptanceServer(t)
	defer srv.Close()

	var callCount int32
	var mu sync.Mutex
	raw := func(context.Context, string, space.CredentialReference) (host.Credential, error) {
		mu.Lock()
		callCount++
		mu.Unlock()
		return host.Credential{Token: "tok"}, nil
	}

	cmd := newTestDoctorCommand()
	cmd.h = host.NewGitHubHost(nil, srv.URL)
	cmd.resolveCredential = memoizeCredentialResolver(raw)
	cmd.lookupGit = func() error { return nil }
	cmd.binaryVersion = "0.5.0"
	cmd.TemplateFiles = doctorScaffoldingTemplateFS()
	cmd.readFile = credentialMemoAcceptanceReadFile("getvisa", "other")
	cmd.cloneOrFetch = func(context.Context, string, string, host.Credential) error { return nil }

	spaces := []space.Ref{
		{ID: "getvisa", RepoURL: "https://github.com/acme/getvisa.git"},
		{ID: "other", RepoURL: "https://github.com/acme/other.git"},
	}
	cmd.loadProjectConfig = func(string) (space.ProjectConfig, error) {
		return space.ProjectConfig{Spaces: spaces}, nil
	}
	cmd.loadMachineConfig = func(string) (space.MachineConfig, error) { return space.MachineConfig{}, nil }

	var stdout, stderr bytes.Buffer
	// Exit code is deliberately not asserted: credentialMemoAcceptanceReadFile
	// reports every file except space.yaml as absent, so "CI presence"
	// legitimately FAILs (missing a2a-validate.yml) — that is unrelated to
	// credential resolution and Run keeps executing every remaining check
	// regardless (Run's loop never short-circuits on an earlier FAIL).
	cmd.Run(context.Background(), nil, IO{Stdout: &stdout, Stderr: &stderr})
	out := stdout.String()

	// Pin that each of the six credential-consuming checks actually reached
	// its resolveCredential call, not just that SOME of them did — a broken
	// link anywhere in this chain (TemplateFiles unwired, plan computed with
	// no drift, an interface the fake host doesn't satisfy) would silently
	// undercount and still pass a bare "== 2" assertion for the wrong reason.
	if !strings.Contains(out, "credentials: PASS") {
		t.Fatalf("stdout missing credentials PASS; got %q", out)
	}
	if !strings.Contains(out, "space access: PASS") {
		t.Fatalf("stdout missing space access PASS; got %q", out)
	}
	if !strings.Contains(out, "auto-merge enabled: PASS") || strings.Contains(out, "auto-merge unverified") {
		t.Fatalf("stdout must show auto-merge PASS with no unverified advisory (proves AutoMergeAllowed reached a credential); got %q", out)
	}
	if !strings.Contains(out, "stuck green PRs: PASS") || strings.Contains(out, "stuck-green state unverified") {
		t.Fatalf("stdout must show stuck green PRs PASS with no unverified advisory (proves ListOpenPRs reached a credential); got %q", out)
	}
	if !strings.Contains(out, "codeowners resolvable: PASS") || strings.Contains(out, "CODEOWNERS unverified") {
		t.Fatalf("stdout must show codeowners PASS with no unverified advisory (proves CodeownersErrors reached a credential); got %q", out)
	}
	if !strings.Contains(out, "behind the current template") || !strings.Contains(out, "run `a2a space update`") {
		t.Fatalf("stdout must show scaffolding behind with push-access wording (proves doctorScaffoldingCanFix reached a credential); got %q", out)
	}

	mu.Lock()
	defer mu.Unlock()
	// The pinned claim: exactly one resolution per configured space (2), not
	// one per (check, space) pair — at least 7 checks reach this seam per
	// space in this exact test setup (verified empirically; see this
	// function's doc comment), so an unmemoised resolver would report at
	// least 14 here, not 2.
	if callCount != int32(len(spaces)) {
		t.Fatalf("resolveCredential invoked %d times over %d spaces, want exactly %d (one per space, not one per credential-consuming check)",
			callCount, len(spaces), len(spaces))
	}
}
