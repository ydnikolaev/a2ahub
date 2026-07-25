package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
			// Pin the GitHub API root at a closed local port so NO script can
			// reach the real api.github.com. P45 added `a2a doctor`'s
			// auto-merge row, which is host-backed: without this the exec'd
			// binary dialled api.github.com with FIXTURE_TOKEN on every run of
			// this suite and got a real 401 back. That made `make check` depend
			// on the network (slow, and red in any sandbox without egress) and
			// leaked a dummy token to a third party. A refused connection
			// exercises the same code path — the read cannot be answered — and
			// resolves in microseconds.
			env.Setenv("A2A_GITHUB_API", "http://127.0.0.1:1")
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
