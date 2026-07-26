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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

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
