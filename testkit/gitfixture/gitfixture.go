// Package gitfixture is the SSOT for making every git process a test
// spawns die with the test that spawned it.
//
// `make check` reds at random: git spawns background maintenance
// (`gc --auto`) inside a fixture repo and is still writing to
// .git/objects when t.TempDir()'s cleanup runs RemoveAll on the same
// tree, producing "unlinkat …/.git/objects: directory not empty" — a
// teardown race, not a product assertion failure (observed 2026-07-24 in
// internal/cache's TestStatusline_NoHubSymbol). Nothing in this tree turns
// gc --auto off, so it is one flake away at any fixture site.
//
// There are two spawn paths, handled by two different mechanisms:
//
//  1. Fixture helpers spawn git directly (exec.Command("git", ...)). Fix at
//     the argv: Args prefixes the flags that disable git's own background
//     maintenance for that one invocation.
//
//  2. PRODUCTION code spawns git while under test — a detached goroutine,
//     for instance, invoking a mirror clone/fetch. That cannot be fixed at
//     a fixture argv. Fix it at the test binary's ENVIRONMENT instead: git
//     reads GIT_CONFIG_COUNT / GIT_CONFIG_KEY_<n> / GIT_CONFIG_VALUE_<n>,
//     and every child process inherits the environment — including a git
//     spawned by production code under test, a git spawned by an exec'd
//     `a2a` binary, and git's own gc --auto grandchild (verified
//     empirically: `-c name=value` on a git invocation propagates via that
//     same env-var mechanism to any git process IT forks, so a single
//     hardened invocation's own internal auto-gc inherits the setting too).
//     HardenEnv does this. Older git ignores unknown env, which is exactly
//     why mechanism 1 stays as the belt.
//
//  3. git-receive-pack, inside the repo being PUSHED INTO, spawns
//     `git maintenance run --auto --quiet --detach` after receiving data and
//     updating refs (git-config(1), receive.autogc, default true). Neither
//     mechanism above reaches it, and that gap red the public repo's `check`
//     on 2026-08-12 (PR #24 — a pull request whose entire diff was one YAML
//     file under feedback/), with the same shape reproduced locally the
//     same day:
//
//     --- FAIL: TestE2E4/G2GateAndLinkedDeprecation (1.05s)
//     testing.go:1464: TempDir RemoveAll cleanup: unlinkat
//     /tmp/TestE2E4…/001/origin.git: directory not empty
//
//     Fix at the RECEIVING repo's own config: HardenRepo.
//
// # Two things about mechanism 3 that cost a wrong fix before the right one
//
// First, gc.auto=0 does not subsume it. Measured against git 2.55.0 with
// GIT_TRACE, pushing a real new commit into a local bare repo while
// `-c gc.auto=0 -c maintenance.auto=false` was in force — i.e. exactly the
// hardening this package already had:
//
//	trace: run_command: git maintenance run --auto --quiet --detach
//
// Note the `--detach`. gc.auto=0 does not PREVENT the spawn; it only makes
// the already-forked background process decide to do nothing once it starts.
// That process still outlives the push, still holds the repo directory open,
// and still races t.TempDir()'s RemoveAll. "We already turn gc off" was
// never the same claim as "no background git touches the fixture".
//
// Second, the setting cannot be pushed from the client. Adding
// `-c receive.autogc=false` to Args changes nothing — git does not let a
// pushing client's config steer the receiving side, which is correct of it.
// Setting it in the bare repo's own config gives 0 spawns against a control
// push of the same commit into an unconfigured repo that gives 3. That
// control matters: an earlier "verification" of the client-side flag counted
// 0 spawns from a push that was already up-to-date, so receive-pack never
// ran at all and the zero measured nothing.
package gitfixture

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// gitCoreFlags disables git's own background maintenance (gc --auto, the
// scheduled maintenance daemon) for a single fixture git invocation. This
// is the ONLY place these two flag strings appear in the repo — one owner,
// so a new fixture git site cannot get the flag wrong by copying a
// neighbour's stale copy — see this file's package doc for the flake they
// prevent.
//
// `receive.autogc` is deliberately NOT here: it is a setting of the repo
// that RECEIVES a push, and git does not let a pushing client's config
// steer it. HardenRepo owns that one — see mechanism 3 in the package doc.
var gitCoreFlags = []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}

// Args returns args prefixed with the flags that disable git's background
// maintenance for a single fixture invocation (see gitCoreFlags). Flags
// must land BEFORE the git subcommand, so callers pass this straight to
// exec.Command("git", gitfixture.Args(subcommandArgs...)...) (or
// exec.CommandContext's equivalent). The input slice is never mutated —
// the returned slice is always a fresh allocation.
func Args(args ...string) []string {
	out := make([]string, 0, len(gitCoreFlags)+len(args))
	out = append(out, gitCoreFlags...)
	out = append(out, args...)
	return out
}

// gitConfigTrio is gitCoreFlags's env-based equivalent plus a deterministic
// test-only author/committer identity, expressed as
// GIT_CONFIG_KEY_<n>/GIT_CONFIG_VALUE_<n> pairs — the mechanism that
// reaches the ONE spawn path Args cannot: production code invoking git
// while the process it runs in is under test. See the package doc for why
// this also covers an exec'd `a2a` binary's own git children and git's own
// gc --auto grandchild.
var gitConfigTrio = [][2]string{
	{"gc.auto", "0"},
	{"maintenance.auto", "false"},
	{"user.name", "a2a-fixture"},
	{"user.email", "fixture@a2ahub.invalid"},
}

// HardenRepo turns off the one piece of background maintenance neither Args
// nor HardenEnv can reach: the `git maintenance run --auto --quiet --detach`
// that git-receive-pack spawns inside the RECEIVING repository after every
// push (git-config(1), receive.autogc, default true).
//
// Call it on any bare repo a fixture will push into, right after creating
// it — whether `git init --bare` or `git clone --bare` made it. See
// mechanism 3 in the package doc for the measurement, and for why passing
// the same key through Args does nothing.
func HardenRepo(t testing.TB, gitDir string) {
	t.Helper()
	if err := HardenRepoDir(gitDir); err != nil {
		t.Fatalf("gitfixture.HardenRepo: %v", err)
	}
}

// HardenRepoDir is HardenRepo's error-returning form, for the fixture sites
// that create a repo where no *testing.T is in scope — an HTTP handler
// standing in for a git host, or a provisioning helper that reports through
// its return value.
//
// gitDir is always a path the calling fixture just created itself, under a
// t.TempDir() or a run-scoped work directory: this function is only ever
// reached right after a `git init --bare` / `git clone --bare` whose target
// path the same function chose. It is never a request field and never user
// input — the fakegithub fork handler, the one caller that runs inside an
// HTTP handler, builds its path from `filepath.Dir(s.OriginDir)` and a
// package constant.
func HardenRepoDir(gitDir string) error {
	//nolint:gosec // reason: argv is a fixed literal list plus gitDir, which is always a path the calling fixture created itself (see this function's own doc) — never request-derived.
	cmd := exec.Command("git", Args("-C", gitDir, "config", "receive.autogc", "false")...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gitfixture: harden %s: %w\n%s", gitDir, err, out)
	}
	return nil
}

// HardenEnv sets the GIT_CONFIG_COUNT/GIT_CONFIG_KEY_<n>/GIT_CONFIG_VALUE_<n>
// trio (git's documented env-based config mechanism, see git-config(1)
// "ENVIRONMENT") on the current process, so every git process spawned for
// the rest of this run picks up the same maintenance-disabling config Args
// gives fixture call sites directly.
//
// It COMPOSES rather than clobbers: if GIT_CONFIG_COUNT is already N, the
// four entries are appended at indices N..N+3 and COUNT is set to N+4 —
// an already-configured GIT_CONFIG_KEY_0/VALUE_0 (say) is left untouched.
// It is idempotent: calling it twice does not add duplicate entries, so
// TestMain functions across packages can call it freely without needing to
// coordinate.
func HardenEnv() {
	count := existingGitConfigCount()
	if hasOwnGitConfigEntries(count) {
		return
	}

	// os.Setenv fails only when the NAME is malformed (contains '=' or a NUL).
	// Every name below is a compile-time prefix plus a decimal integer, so the
	// error is unreachable by construction — discarded explicitly rather than
	// returned, so HardenEnv stays callable from a TestMain one-liner.
	next := count
	for _, kv := range gitConfigTrio {
		_ = os.Setenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", next), kv[0])
		_ = os.Setenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", next), kv[1])
		next++
	}
	_ = os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(next))
}

// existingGitConfigCount reads GIT_CONFIG_COUNT, treating an absent,
// empty, non-numeric, or negative value as 0 — git itself rejects a bogus
// count, so there is no pre-existing valid state to preserve in that case.
func existingGitConfigCount() int {
	raw := os.Getenv("GIT_CONFIG_COUNT")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// hasOwnGitConfigEntries reports whether every pair in gitConfigTrio is
// already present, verbatim, among the count GIT_CONFIG_KEY_<n>/VALUE_<n>
// entries — the idempotence check: a prior HardenEnv call (in this process)
// leaves exactly this signature behind.
func hasOwnGitConfigEntries(count int) bool {
	for _, want := range gitConfigTrio {
		found := false
		for i := 0; i < count; i++ {
			key := os.Getenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i))
			val := os.Getenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i))
			if key == want[0] && val == want[1] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// RunTests is the one-liner a package's TestMain calls: HardenEnv, then run
// the package's tests. See HardenEnv's doc for what this hardens and why.
func RunTests(m *testing.M) int {
	HardenEnv()
	return m.Run()
}
