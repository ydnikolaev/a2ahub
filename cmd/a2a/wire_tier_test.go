// wire_tier_test.go is THE WIRE TIER: the real cmd/a2a entry points —
// runLifecycle and runContract — driven end to end against a LOCAL bare
// repository standing in for GitHub. No network, no token service, no
// binary build, no fake host.
//
// Why a tier here at all, between the hermetic unit tests and the hour-long
// live matrix. Every defect this project shipped in July lived in the SEAM
// between wiring and core, not in either: a version floor the funnel checks
// but no caller populated; a mirror the writer left on its own branch that
// only a re-resolve moved back; a branch grammar two binaries rendered
// differently. Unit tests construct the core directly and so cannot see any
// of that. The live matrix sees all of it and takes 85 minutes against real
// GitHub, which means it runs once before a tag rather than on every commit.
//
// This tier runs in `make check`, in seconds, and exercises the actual
// resolution chain a user's invocation takes: resolvePaths ->
// LoadProjectConfig -> resolveTargetSpaceRef -> ResolveCredential ->
// ResolveMirrorLocation -> CloneOrFetch -> NewWriteFunnel -> the verb.
//
// Two constraints shape everything below, both verified rather than assumed:
//   - resolvePaths reads os.Getwd() and os.UserHomeDir(), so a case must
//     t.Chdir and t.Setenv("HOME", ...). Both forbid t.Parallel(). The
//     precedent for that rails exemption is help_test.go.
//   - space.ResolveCredential REFUSES when neither the env var nor a
//     machine-config reference resolves, so the fixture exports
//     A2A_TOKEN_<SPACE> or every case dies before reaching the verb.
//
// The local origin works because parseGitHubRepo accepts a filesystem path
// (/tmp/x/origin.git -> owner "x", name "origin"), so dependency resolution
// completes without a GitHub URL. Writes therefore reach the funnel and the
// commit; only the HOST call (open PR) has nowhere real to go, which is
// exactly the boundary this tier means to stop at — the live matrix owns
// what happens past it.
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// wireFixture is one fully-wired project: a local origin, a clone the
// mirror resolves to, a project config, a HOME, and a credential.
type wireFixture struct {
	SpaceID     string
	System      string
	ProjectRoot string
	OriginDir   string
}

// newWireFixture builds a project the real entry points can resolve, and
// chdirs into it. Serial by construction (t.Chdir / t.Setenv).
func newWireFixture(t *testing.T, system string, others ...string) *wireFixture {
	t.Helper()

	systems := append([]string{system}, others...)
	fx := spacefixture.New(t, systems...)

	// spacefixture seeds a space.yaml that is NOT a parseable
	// space.Manifest (`id:`/`schema_version:` and a participants MAP, where
	// the real schema has `space:` and a participants LIST). Every read on
	// this path parses the manifest for real, so push a valid one. Fixing
	// the shared seed is a separate change with its own blast radius; this
	// tier does not get to make it as a side effect.
	seedDir := t.TempDir()
	runGitFixture(t, "", "clone", fx.OriginDir, seedDir)
	manifest := "schema: space/v1\nspace: fixture-space\nmin_binary_version: 0.0.0\nparticipants:\n"
	for _, sys := range systems {
		manifest += "  - system: " + sys + "\n    section: " + sys + "\n    status: active\n    owners: [" + sys + "-bot]\n"
	}
	if err := os.WriteFile(filepath.Join(seedDir, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	runGitFixture(t, seedDir, "add", "space.yaml")
	runGitFixture(t, seedDir, "commit", "-m", "wire tier: a parseable manifest")
	runGitFixture(t, seedDir, "push", "origin", "HEAD")

	projectRoot := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectRoot, ".a2a"), 0o755); err != nil {
		t.Fatalf("mkdir .a2a: %v", err)
	}
	cfg := "schema: project/v1\nsystem: " + system + "\nspaces:\n" +
		"  - id: fixture-space\n    repo_url: " + fx.OriginDir + "\n"
	if err := os.WriteFile(filepath.Join(projectRoot, ".a2a", "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write project config: %v", err)
	}

	t.Setenv("HOME", home)
	t.Setenv("A2A_TOKEN_FIXTURE_SPACE", "wire-tier-token")
	t.Chdir(projectRoot)

	return &wireFixture{
		SpaceID: "fixture-space", System: system,
		ProjectRoot: projectRoot, OriginDir: fx.OriginDir,
	}
}

func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v (dir=%q): %v\n%s", args, dir, err, out)
	}
}

// TestWireLifecycleResolvesTheWholeChain drives a real lifecycle verb
// through runLifecycle and asserts the resolution chain completed — the
// verb reached its own argument handling rather than dying in wiring.
//
// The assertion is deliberately about WHERE it got to, not about a
// successful write: the local origin has no host to open a PR against, so
// a write cannot complete here and pretending otherwise would make the
// case a lie. What this proves is the half that kept breaking — paths,
// config, target-space resolution, credential, mirror, funnel construction.
func TestWireLifecycleResolvesTheWholeChain(t *testing.T) {
	newWireFixture(t, "axon", "beta")

	var stdout, stderr strings.Builder
	code := runLifecycle([]string{"XQ-axon-20260726-wire"}, &stdout, &stderr, lifecycleVerbs()["ack"])
	out := stdout.String() + stderr.String()

	for _, wiringFailure := range []string{
		"no project config",
		"no connected space",
		"credential",
		"mirror sync failed",
		"cannot read space.yaml",
	} {
		if strings.Contains(strings.ToLower(out), wiringFailure) {
			t.Fatalf("runLifecycle died in WIRING, not in the verb: %q\ncode=%d\noutput:\n%s",
				wiringFailure, code, out)
		}
	}
	// It must reach the verb, which refuses an artifact that does not exist.
	// That refusal IS the proof: only the verb can produce it, and only a
	// fully-resolved deps struct reaches the verb.
	if !strings.Contains(out, "XQ-axon-20260726-wire") {
		t.Fatalf("output never names the artifact, so the verb was never reached; code=%d\noutput:\n%s", code, out)
	}
}

// TestWireContractResolvesTheWholeChain is the same proof for runContract,
// which has its OWN resolution path (it builds a NewCommand from the
// project config before resolving deps) and so can break independently.
func TestWireContractResolvesTheWholeChain(t *testing.T) {
	newWireFixture(t, "axon", "beta")

	var stdout, stderr strings.Builder
	code := runContract([]string{"publish", "--version", "1.0.0", "XC-axon-nothing"}, &stdout, &stderr)
	out := stdout.String() + stderr.String()

	for _, wiringFailure := range []string{
		"no project config",
		"no connected space",
		"credential",
		"mirror sync failed",
	} {
		if strings.Contains(strings.ToLower(out), wiringFailure) {
			t.Fatalf("runContract died in WIRING, not in the verb: %q\ncode=%d\noutput:\n%s",
				wiringFailure, code, out)
		}
	}
	if !strings.Contains(out, "XC-axon-nothing") {
		t.Fatalf("output never names the contract, so the verb was never reached; code=%d\noutput:\n%s", code, out)
	}
}

// TestWireMirrorIsFreshAfterResolution proves the resolution chain actually
// SYNCS the mirror rather than merely locating it — the defect class
// RN-0800-1 fixed for reads and RN-0900-2 fixed for MCP. A commit pushed to
// the origin AFTER the project was set up must be visible in the mirror the
// next invocation resolves.
func TestWireMirrorIsFreshAfterResolution(t *testing.T) {
	fx := newWireFixture(t, "axon", "beta")

	// A second party publishes something directly to the origin.
	peer := t.TempDir()
	runGitFixture(t, "", "clone", fx.OriginDir, peer)
	marker := filepath.Join(peer, "beta", "exchanges", "XQ-beta-20260726-late.md")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(marker, []byte("---\nid: XQ-beta-20260726-late\n---\nbody\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGitFixture(t, peer, "add", "-A")
	runGitFixture(t, peer, "commit", "-m", "peer publishes after our setup")
	runGitFixture(t, peer, "push", "origin", "HEAD")

	// Any resolving invocation must fetch it.
	var stdout, stderr strings.Builder
	_ = runLifecycle([]string{"XQ-beta-20260726-late"}, &stdout, &stderr, lifecycleVerbs()["ack"])

	mirrors, err := filepath.Glob(filepath.Join(fx.ProjectRoot, ".a2a", "cache", "mirrors", "*",
		"beta", "exchanges", "XQ-beta-20260726-late.md"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(mirrors) == 0 {
		t.Fatal("the mirror never fetched a commit the peer pushed after setup — " +
			"a resolving invocation that does not sync is how a counterparty's publish stays invisible")
	}
}

// TestWireEveryLifecycleVerbResolves is the parity guard: EVERY verb in
// lifecycleVerbs() is driven through the real resolution chain, so a verb
// added later cannot quietly have no wire-level coverage.
//
// It asserts the wiring, not the outcome — each verb is handed an artifact
// that does not exist, so each legitimately refuses. What must never happen
// is a refusal that comes from the plumbing instead of the verb.
func TestWireEveryLifecycleVerbResolves(t *testing.T) {
	verbs := lifecycleVerbs()
	if len(verbs) == 0 {
		t.Fatal("lifecycleVerbs() is empty — the parity guard is guarding nothing")
	}

	names := make([]string, 0, len(verbs))
	for name := range verbs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			newWireFixture(t, "axon", "beta")

			var stdout, stderr strings.Builder
			// Verbs that require a flag still parse args before touching the
			// artifact; the id is enough to prove the chain resolved.
			code := runLifecycle([]string{"XQ-axon-20260726-parity"}, &stdout, &stderr, verbs[name])
			out := strings.ToLower(stdout.String() + stderr.String())

			for _, wiringFailure := range []string{
				"no project config", "no connected space",
				"unreadable machine config", "mirror sync failed",
			} {
				if strings.Contains(out, wiringFailure) {
					t.Fatalf("%s died in WIRING (%q), not in the verb; code=%d\noutput:\n%s",
						name, wiringFailure, code, out)
				}
			}
		})
	}
}

// TestWireWriteNeedsNoMachineConfig pins the fix the wire tier found on its
// first run: a write path must treat an ABSENT machine config as an empty
// one, exactly as the read path and the MCP wiring already do.
//
// `.a2a/config.yaml` is committed to the PROJECT, so a second person who
// clones it onto a fresh machine has a project config and no machine config
// until they run `a2a init`. Before this, that person could read the space
// and be refused when writing to it, over a raw "no such file or directory".
func TestWireWriteNeedsNoMachineConfig(t *testing.T) {
	fx := newWireFixture(t, "axon", "beta")

	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "a2a", "config.yaml")); !os.IsNotExist(err) {
		t.Fatalf("this case is only meaningful with NO machine config; stat err = %v", err)
	}

	var stdout, stderr strings.Builder
	code := runLifecycle([]string{"XQ-axon-20260726-nomc"}, &stdout, &stderr, lifecycleVerbs()["ack"])
	out := stdout.String() + stderr.String()

	if strings.Contains(strings.ToLower(out), "machine config") {
		t.Fatalf("a write was refused for want of a machine config; code=%d\noutput:\n%s", code, out)
	}
	if !strings.Contains(out, "XQ-axon-20260726-nomc") {
		t.Fatalf("the verb was never reached; code=%d\noutput:\n%s", code, out)
	}
	_ = fx
}

// TestWireCorruptMachineConfigStillRefuses is the other direction, and the
// reason the fix is "tolerate ABSENT" rather than "tolerate any error": a
// machine config that exists and does not parse is a broken file, and
// ignoring it would silently drop a configured mirror_root or credential
// reference.
func TestWireCorruptMachineConfigStillRefuses(t *testing.T) {
	newWireFixture(t, "axon", "beta")

	mcDir := filepath.Join(os.Getenv("HOME"), ".config", "a2a")
	if err := os.MkdirAll(mcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mcDir, "config.yaml"), []byte("\tthis: [is not\n  valid yaml\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var stdout, stderr strings.Builder
	code := runLifecycle([]string{"XQ-axon-20260726-corrupt"}, &stdout, &stderr, lifecycleVerbs()["ack"])
	out := stdout.String() + stderr.String()

	if code == 0 || !strings.Contains(strings.ToLower(out), "machine config") {
		t.Fatalf("a CORRUPT machine config must still refuse by name; code=%d\noutput:\n%s", code, out)
	}
}

// wireMirrorDir returns the one mirror the project resolved to, failing if
// there is not exactly one — a glob that silently matched none is how a test
// asserting on the mirror can pass having looked at nothing.
func wireMirrorDir(t *testing.T, projectRoot string) string {
	t.Helper()
	dirs, err := filepath.Glob(filepath.Join(projectRoot, ".a2a", "cache", "mirrors", "*"))
	if err != nil {
		t.Fatalf("glob mirrors: %v", err)
	}
	if len(dirs) != 1 {
		t.Fatalf("want exactly one mirror under the project, found %d: %v", len(dirs), dirs)
	}
	return dirs[0]
}

// wireAgeMirror backdates dir's sync-age markers — the same two files
// internal/cache/staleness.go reads (.git/FETCH_HEAD and .git/HEAD) — so a
// case can place the mirror at an exact age without sleeping.
func wireAgeMirror(t *testing.T, dir string, past time.Duration) {
	t.Helper()
	old := time.Now().Add(-past)
	var touched int
	for _, rel := range []string{filepath.Join(".git", "FETCH_HEAD"), filepath.Join(".git", "HEAD")} {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatalf("wireAgeMirror: Chtimes %s: %v", p, err)
		}
		touched++
	}
	if touched == 0 {
		t.Fatalf("wireAgeMirror: neither sync-age marker exists under %s — the case would run against "+
			"an unaged mirror and prove nothing", dir)
	}
}

// TestWireReadVerbSeesAMergeItDidNotSyncFor is the wire-level half of
// RN-0910-1, and it exists to guard the one link the hermetic tests cannot
// reach: that a read verb's wired closure ACTUALLY calls the refresh.
//
// The chain has three links and, before this, only two were held:
//
//	internal/cache        — what the refresh does, and in which window
//	                        (TestSyncIfStale_RevealsAMergeInsideTheStatuslineWindow)
//	wire_freshness_test   — WHICH verbs are supposed to refresh (the set)
//	the wired closure     — that the call is there at all  <- nothing
//
// Delete the `if freshReadVerbs[name] { store.SyncIfStale(ctx) }` block from
// wire.go and both existing guards stay green: the set is still correct and
// the cache still behaves. The product ships the v0.9.0 defect again.
//
// The sequence is the real one, in the real order:
//
//  1. a resolving invocation fetches the mirror   (this is `a2a submit`)
//  2. the counterparty's pull request merges       (the peer pushes)
//  3. the mirror is a minute old — recently fetched, so "fresh" to a window
//     sized for a shell prompt
//  4. `a2a inbox` is asked, with NO explicit `a2a sync` anywhere
//
// Step 3's age is the whole point and is asserted, not assumed: a minute must
// sit outside the read window and inside the statusline's, or the case proves
// nothing. That is how the live matrix stayed green either side of this defect
// — no row asked the question in the window where the answer differed.
func TestWireReadVerbSeesAMergeItDidNotSyncFor(t *testing.T) {
	fx := newWireFixture(t, "axon", "beta")

	const age = time.Minute
	if age <= cache.ReadRefreshTTLForTest || age >= cache.DefaultStatuslineTTL {
		t.Fatalf("this case needs an age OUTSIDE the read window and INSIDE the statusline's; %s no "+
			"longer sits between %s and %s, so it would pass without exercising the defect",
			age, cache.ReadRefreshTTLForTest, cache.DefaultStatuslineTTL)
	}

	// (1) A resolving invocation creates and fetches the mirror. In production
	// this is the user's own `a2a submit` — which is exactly why the mirror is
	// young at step 4, and exactly why a statusline-sized window called it
	// fresh.
	var w1, w2 strings.Builder
	_ = runLifecycle([]string{"XQ-beta-20260726-seed"}, &w1, &w2, lifecycleVerbs()["ack"])
	mirror := wireMirrorDir(t, fx.ProjectRoot)

	// (2) The counterparty publishes and their PR merges. Written from an
	// independent clone straight to the origin, so this side's mirror knows
	// nothing about it.
	peer := t.TempDir()
	runGitFixture(t, "", "clone", fx.OriginDir, peer)
	const id = "XQ-beta-20260726-merged"
	envelope := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: question\n" +
		"title: a question that merged while we were not looking\n" +
		"space: fixture-space\n" +
		"from: beta\n" +
		"to: [axon]\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-26T10:00:00Z\n" +
		"priority: p2\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"---\nbody\n"
	event := "schema: event/v1\nevent: 01J40A7M9P1S3V5W7Y9A1C3E5G\nspace: fixture-space\n" +
		"subject: " + id + "\ntransition: submit\n" +
		"actor: {kind: agent, name: bot, system: beta}\nat: 2026-07-26T10:00:00Z\n"
	for rel, content := range map[string]string{
		"beta/exchanges/" + id + ".md":                     envelope,
		"beta/events/2026/01J40A7M9P1S3V5W7Y9A1C3E5G.yaml": event,
	} {
		p := filepath.Join(peer, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	runGitFixture(t, peer, "add", "-A")
	runGitFixture(t, peer, "commit", "-m", "beta publishes; the PR merges")
	runGitFixture(t, peer, "push", "origin", "HEAD")

	// (3) A minute old: freshly fetched by our own write, moments ago.
	wireAgeMirror(t, mirror, age)

	// (4) The read, through the REAL wired closure — the one that decides
	// whether to refresh — and with no explicit sync anywhere.
	inbox, ok := buildCommands()["inbox"]
	if !ok {
		t.Fatal("buildCommands() has no \"inbox\" — this case is dispatching nothing")
	}
	var stdout, stderr strings.Builder
	code := inbox([]string{}, &stdout, &stderr)
	out := stdout.String()

	if !strings.Contains(out, id) {
		t.Fatalf("`a2a inbox` did not see an artifact whose PR merged after our last fetch; code=%d\n"+
			"stdout:\n%s\nstderr:\n%s\n\n"+
			"The mirror was %s old — young enough that a window sized for the shell prompt calls it "+
			"fresh. This is RN-0910-1 through the real entry point: on v0.9.0 `outbox` printed nothing "+
			"and `show` answered \"artifact not found\", and the documented session-start loop reads an "+
			"empty inbox as permission to proceed.\n"+
			"If internal/cache's own row is green and this one is not, the refresh is no longer WIRED: "+
			"check the freshReadVerbs branch in buildCommands().", code, out, stderr.String(), age)
	}
}

// runGitFixtureOutput is runGitFixture's output-returning twin — needed here
// (unlike every other case in this file) to read back WHICH branch the
// funnel pushed and what it committed there.
func runGitFixtureOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=a2a-fixture", "GIT_AUTHOR_EMAIL=fixture@a2ahub.invalid",
		"GIT_COMMITTER_NAME=a2a-fixture", "GIT_COMMITTER_EMAIL=fixture@a2ahub.invalid",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%q): %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// stubFindPRAlwaysEmpty answers WriteFunnel.Submit's step-0
// FindPRByHeadBranch call (`GET .../pulls?...`) with an empty JSON array —
// "no existing PR for this branch" — for ANY method/path. A real
// api.github.com answers the same GET with 401 (this fixture's token is a
// fixture value, not a real credential), which host.GitHubHost's
// restCall treats as fatal and returns BEFORE the funnel ever commits or
// pushes — the one host round-trip this tier cannot let reach the real
// network. Every other verb in this file avoids the problem by refusing on
// a nonexistent artifact before Submit is ever called; TestWireRespond
// PropagatesParentThread is the first row that needs Submit to actually
// run, so it is the first to need this stub. OpenPR (step 4, the POST this
// stub does NOT answer) still has nowhere real to go and still fails —
// exactly the boundary this file's own package doc names ("only the HOST
// call (open PR) has nowhere real to go"); the commit and the PushBranch
// (a REAL local `git push` against the filesystem origin, host/github.go's
// own doc comment) both complete before that failure.
func stubFindPRAlwaysEmpty(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestWireRespondPropagatesParentThread drives `a2a respond` through the
// REAL entry point (runLifecycle -> resolvePaths -> ... -> NewWriteFunnel
// -> RespondCommand) against the local bare-repo origin every other case in
// this file already sets up, and asserts the COMMITTED response's own
// frontmatter carries the parent's thread VERBATIM (spec 46 §T1 R4/R5,
// cmd_lifecycle.go's own RespondCommand.Run) — the wire-level half of
// show_thread.txtar / TestThreadChainFixtureOrdersQuestionBeforeResponse's
// direct-construction proof that thread propagation survives the real
// write path, not just a hand-built fixture.
//
// beta is the fixture's own acting system (not axon, as most of this
// file's other cases use): `respond` is RoleTarget-authorized
// (cmd_lifecycle.go's own lifecycleCheckLegality), so the parent must be
// addressed TO the acting system — a question FROM axon TO beta.
func TestWireRespondPropagatesParentThread(t *testing.T) {
	t.Setenv(githubAPIEnv, stubFindPRAlwaysEmpty(t))

	// funnelBinaryVersion feeds NewWriteFunnel's CC-085 min_binary_version
	// guard a BARE dotted version (funnelBinaryVersion's own doc comment,
	// wire.go); main.version's own zero-ldflags default is the literal
	// "dev", which is not a parseable semver and refuses every write in
	// this in-process (no -ldflags) test binary — every other case in this
	// file never reaches that guard (they refuse earlier, on a nonexistent
	// artifact). internal/e2e's own exec'd-binary tier stamps the same
	// value via `-ldflags -X main.version=0.1.0` (main_test.go); this is
	// its in-process equivalent.
	origVersion := version
	version = "0.1.0"
	t.Cleanup(func() { version = origVersion })

	fx := newWireFixture(t, "beta", "axon")

	// The parent already exists, submitted and acknowledged — a peer clone
	// pushes that history directly, simulating state that predates this
	// invocation (the same idiom TestWireMirrorIsFreshAfterResolution and
	// TestWireReadVerbSeesAMergeItDidNotSyncFor already use in this file).
	peer := t.TempDir()
	runGitFixture(t, "", "clone", fx.OriginDir, peer)
	const parentID = "XQ-axon-20260726-wr01"
	const parentThread = "thread:axon-20260726-wr01"
	envelope := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + parentID + "\n" +
		"type: question\n" +
		"title: does the export include soft-deleted rows\n" +
		"space: fixture-space\n" +
		"from: axon\n" +
		"to: [beta]\n" +
		"thread: " + parentThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-26T09:00:00Z\n" +
		"category: clarification\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"---\nbody\n"
	submitEvent := "schema: event/v1\nevent: 01J40A7M9P1S3V5W7Y9A1C3E5F\nspace: fixture-space\n" +
		"subject: " + parentID + "\ntransition: submit\n" +
		"actor: {kind: agent, name: bot, system: axon}\nat: 2026-07-26T09:00:00Z\n"
	ackEvent := "schema: event/v1\nevent: 01J40A7M9P1S3V5W7Y9A1C3E5G\nspace: fixture-space\n" +
		"subject: " + parentID + "\ntransition: acknowledge\n" +
		"actor: {kind: agent, name: bot, system: beta}\nat: 2026-07-26T09:05:00Z\n"
	for rel, content := range map[string]string{
		filepath.Join("axon", "exchanges", parentID+".md"):                         envelope,
		filepath.Join("axon", "events", "2026", "01J40A7M9P1S3V5W7Y9A1C3E5F.yaml"): submitEvent,
		filepath.Join("beta", "events", "2026", "01J40A7M9P1S3V5W7Y9A1C3E5G.yaml"): ackEvent,
	} {
		p := filepath.Join(peer, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	runGitFixture(t, peer, "add", "-A")
	runGitFixture(t, peer, "commit", "-m", "axon asks; beta acknowledges")
	runGitFixture(t, peer, "push", "origin", "HEAD")

	var stdout, stderr strings.Builder
	code := runLifecycle([]string{"--result", "answered", parentID}, &stdout, &stderr, lifecycleVerbs()["respond"])
	out := stdout.String() + stderr.String()

	for _, wiringFailure := range []string{
		"no project config", "no connected space", "credential", "mirror sync failed", "cannot read space.yaml",
	} {
		if strings.Contains(strings.ToLower(out), wiringFailure) {
			t.Fatalf("respond died in WIRING, not in the verb: %q; code=%d\noutput:\n%s", wiringFailure, code, out)
		}
	}
	// The write itself is expected to end in OpenPR's own refusal (no real
	// GitHub repo exists behind the local-path origin) — that boundary is
	// this tier's own documented one (see stubFindPRAlwaysEmpty's doc
	// comment), not a wiring failure. Pinned by name, not just by the
	// commit having landed: a future change that instead fails EARLIER
	// (e.g. back at FindPRByHeadBranch, or the version guard) would also
	// leave no branch to find, but for a different, silently-regressed
	// reason findRespondBranch's own failure message would not distinguish.
	if code == 0 || !strings.Contains(out, "OpenPR") {
		t.Fatalf("want the OpenPR boundary specifically (code != 0, output naming OpenPR); code=%d\noutput:\n%s", code, out)
	}
	// What this case exists to prove is that the COMMIT reached the branch
	// before that refusal, so the committed content is read back from
	// origin's own ephemeral branch — never main, which nothing here ever
	// merges to (no host to do it).
	mirror := wireMirrorDir(t, fx.ProjectRoot)
	runGitFixture(t, mirror, "fetch", "origin", "+refs/heads/*:refs/remotes/origin/*")
	branch := findRespondBranch(t, mirror, parentID)
	responsePath := findResponseFilePath(t, mirror, branch)
	committed := runGitFixtureOutput(t, mirror, "show", "origin/"+branch+":"+responsePath)

	if !strings.Contains(committed, "thread: "+parentThread) {
		t.Fatalf("committed response (branch %s, %s) does not carry the parent's thread (%s) verbatim; code=%d\noutput:\n%s\ncommitted:\n%s",
			branch, responsePath, parentThread, code, out, committed)
	}
}

// findRespondBranch locates the ONE remote branch the funnel pushed for
// parentID's `respond` write (space.BranchName's own grammar:
// a2a/<system>/<verb>/<artifactID> — artifactID here is
// "<parentID>+<responseID>", D-024's multi-id write, so a prefix match on
// parentID is the only stable anchor: the content-derived responseID
// itself cannot be predicted ahead of the write).
func findRespondBranch(t *testing.T, mirror, parentID string) string {
	t.Helper()
	out := runGitFixtureOutput(t, mirror, "branch", "-r")
	prefix := "origin/a2a/beta/respond/" + parentID
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, "origin/")
		}
	}
	t.Fatalf("findRespondBranch: no remote branch with prefix %q under %s; branch -r:\n%s", prefix, mirror, out)
	return ""
}

// findResponseFilePath locates the response's own committed path
// (beta/exchanges/XS-*.md, space.Layout.Exchange's own shape) within
// branch's tip commit.
func findResponseFilePath(t *testing.T, mirror, branch string) string {
	t.Helper()
	out := runGitFixtureOutput(t, mirror, "ls-tree", "-r", "--name-only", "origin/"+branch)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "/exchanges/XS-") {
			return line
		}
	}
	t.Fatalf("findResponseFilePath: no beta/exchanges/XS-*.md under origin/%s; ls-tree:\n%s", branch, out)
	return ""
}
