package e2e

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// TestT3Scripts is AC-1's testscript half: every T3 `.txtar` script under
// testdata/ runs the BUILT a2a binary (via `exec`, plan 10's binding
// mechanism) against a throwaway, per-script clone of ONE shared fixture
// space. Scripts assert OP-contract level ONLY (exit codes, stdout/stderr,
// `cmp` golden) — never internal package state.
//
// This covers every v1-min-shipped OP-2xx verb whose success path does NOT
// require a real GitHub host connection: version, init, connect, new,
// template list, validate, sync, inbox, outbox, show, thread, search,
// contracts, statusline, doctor, plus submit's CC-002 local refusal (which
// runs — and must run — BEFORE any network call, per cmd/a2a/wire.go's
// runSubmit ordering). The write-path verbs that mutate a space (submit's
// success path, every lifecycle verb, every contract sub-verb) are NOT
// exec-able against this fixture: cmd/a2a/wire.go hard-codes
// `githubAPIBaseURL = "https://api.github.com"` (no env/flag override) and
// `parseGitHubRepo` requires a github.com-shaped remote URL, so an exec'd
// `a2a submit`/`ack`/`contract publish` against a local-path fixture cannot
// reach a real OpenPR call without editing cmd/a2a (off-limits — see this
// phase's reported deviation). Those verbs are covered instead by
// TestT3LifecycleVerbs / TestT3ContractVerbs / TestT3Submit in this same
// package, using the plan's OWN binding mechanism for the write path (real
// space.WriteFunnel + host.NewFakeHost + testkit/spacefixture, direct
// construction — the cmd_submit_test.go/cmd_lifecycle_test.go idiom), which
// is the only way to inject a FakeHost at all.
func TestT3Scripts(t *testing.T) {
	fx := spacefixture.New(t, "axon", "beta", "gamma")
	origin := fx.RemoteURL()
	fixOriginManifest(t, origin, "fixture-space")
	seedOriginExtras(t, origin)

	// Host-backed read checks need a deterministic "credential cannot read
	// settings" answer. A closed port used to model that as a transport
	// outage, so production's correct 2s+4s+8s retry schedule ran once per
	// doctor row and made this script spend ~72 seconds sleeping. A local
	// fatal 403 exercises the same doctor advisory branch without conflating
	// permission denial with an unknown transport outcome. Retry semantics
	// have their own host tests; no script reaches the network.
	deniedAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"fixture denies repository metadata"}`))
	}))
	t.Cleanup(deniedAPI.Close)

	testscript.Run(t, testscript.Params{
		Dir: "testdata/t3",
		Setup: func(env *testscript.Env) error {
			mirrorDir := filepath.Join(env.WorkDir, ".a2a", "cache", "mirrors", "fixture-space")
			if err := cloneFreshErr(origin, mirrorDir); err != nil {
				return err
			}

			home := filepath.Join(env.WorkDir, "home")
			cfgDir := filepath.Join(home, ".config", "a2a")
			if err := os.MkdirAll(cfgDir, 0o755); err != nil {
				return err
			}
			machineCfg := "credentials:\n  fixture-space: \"env:FIXTURE_TOKEN\"\n"
			if err := os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(machineCfg), 0o644); err != nil {
				return err
			}

			projDir := filepath.Join(env.WorkDir, ".a2a")
			if err := os.MkdirAll(projDir, 0o755); err != nil {
				return err
			}
			projCfg := fmt.Sprintf("system: axon\nspaces:\n  - id: fixture-space\n    repo_url: %s\n", origin)
			if err := os.WriteFile(filepath.Join(projDir, "config.yaml"), []byte(projCfg), 0o644); err != nil {
				return err
			}

			env.Setenv("HOME", home)
			env.Setenv("FIXTURE_TOKEN", "dummy-token")
			env.Setenv("ORIGIN", origin)
			// The version this package stamps into the exec'd `a2a` binary
			// (main_test.go's harnessStampedVersion, the SINGLE home for the
			// value) — a plain form for `exec` args and a regexp.QuoteMeta'd
			// form for `stdout`/`grep` patterns, which need the dots escaped
			// literally. A .txtar assertion concatenates a quoted (literal,
			// unexpanded) chunk with an unquoted (expanded) $VAR chunk —
			// go-internal/testscript's own parse(): single quotes disable
			// expansion, so the two must be separate, adjacent tokens (the
			// same idiom cmd/go's own script tests use for $GOVERSION).
			env.Setenv("A2A_STAMPED_VERSION", harnessStampedVersion)
			env.Setenv("A2A_STAMPED_VERSION_RE", regexp.QuoteMeta(harnessStampedVersion))
			// Pin the API root to the local denial fixture so NO script can
			// reach api.github.com or leak FIXTURE_TOKEN to a third party.
			env.Setenv("A2A_GITHUB_API", deniedAPI.URL)
			env.Setenv("PATH", binDir+string(os.PathListSeparator)+env.Getenv("PATH"))
			// testscript builds its own curated Env.Vars — it does NOT
			// inherit the outer test process's os.Environ() (see HOME/PATH
			// being re-set above; if it did, that would be unnecessary). So
			// runTestMain's gitfixture.HardenEnv() call, which only hardens
			// THIS process's env, never reaches a git process the exec'd
			// `a2a` binary spawns from inside a script. Copy the
			// GIT_CONFIG_* trio HardenEnv already set on this process into
			// the script's env explicitly, so the binary's own git children
			// (e.g. a mirror clone/fetch) are hardened too.
			for _, kv := range os.Environ() {
				name, val, ok := strings.Cut(kv, "=")
				if ok && strings.HasPrefix(name, "GIT_CONFIG_") {
					env.Setenv(name, val)
				}
			}
			leasePath := filepath.Join(env.WorkDir, ".a2a", "cache", "statusline-refresh.lease")
			env.Defer(func() {
				deadline := time.Now().Add(10 * time.Second)
				for {
					if _, err := os.Stat(leasePath); errors.Is(err, os.ErrNotExist) {
						return
					}
					if time.Now().After(deadline) {
						t.Errorf("detached statusline refresh did not release %s", leasePath)
						return
					}
					// The lease is the child-completion signal; this wait owns
					// fixture teardown instead of racing the child's git fetch.
					time.Sleep(10 * time.Millisecond)
				}
			})
			return nil
		},
	})
}

// cloneFreshErr is cloneFresh's error-returning twin (testscript.Setup
// takes no *testing.T — it runs inside the script engine's own goroutine,
// not the outer test's).
func cloneFreshErr(originDir, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return gitCloneErr(originDir, dest)
}

func gitCloneErr(originDir, dest string) error {
	cmd := exec.Command("git", gitfixture.Args("clone", originDir, dest)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s %s: %w\n%s", originDir, dest, err, out)
	}
	return nil
}
