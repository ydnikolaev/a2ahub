package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

// fixValidManifest overwrites and pushes a structurally-valid space.yaml
// (space.Manifest's own []Participant shape) onto the fixture's origin —
// spacefixture.go (testkit, off this phase's allowlist) seeds a
// map-shaped `participants:` block that space.ParseManifest cannot decode
// into []Participant; this test-local fix pushes a corrected manifest
// rather than editing the shared fixture helper.
func fixValidManifest(t *testing.T, fx *spacefixture.Fixture, system string) {
	t.Helper()
	dir := fx.Clone(system)
	manifest := "schema: space/v1\nspace: fixture-space\nmin_binary_version: \"0.19.0\"\nparticipants:\n" +
		"  - system: axon\n    org: fixture\n    section: axon\n    owners: [axon-bot]\n    status: active\n    joined: \"2026-01-01\"\n" +
		"  - system: beta\n    org: fixture\n    section: beta\n    owners: [beta-bot]\n    status: active\n    joined: \"2026-01-01\"\n"
	if err := os.WriteFile(filepath.Join(dir, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "space.yaml")
	runGitTest(t, dir, "-c", "user.name=fixture", "-c", "user.email=fixture@a2ahub.invalid", "commit", "-m", "fix manifest shape")
	runGitTest(t, dir, "push", "origin", "HEAD:main")
}

// reason: mutates process env through the production credential seam.
func TestProductionDegradedContractSurfaceFailsLegacyWritesClosed(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_FIXTURE_SPACE", "test-token")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	unreachable := filepath.Join(t.TempDir(), "no-such-origin.git")
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n  - id: fixture-space\n    repo_url: "+unreachable+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  fixture-space: \"env:A2A_TOKEN_FIXTURE_SPACE\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	publication := &mcpP6PublicationFake{result: space.ContractPublicationResult{
		Status: space.ContractPublicationPlanned,
		Plan:   contract.PublicationPlan{Contract: "XC-axon-orders", TargetVersion: "1.0.0", PlanDigest: "sha256:plan"},
	}}
	build := func(ActorResolver) (ContractToolOperations, error) {
		return ContractToolOperations{
			Publication: publication, Materialize: &mcpP6MaterializeFake{},
			Check: &mcpP6CheckFake{}, Inspection: &mcpP6InspectionFake{},
		}, nil
	}
	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfigWithContractOperations(t.Context(), p, "0.19.0", unavailableWorkToolDeps(), build)
	if err != nil {
		t.Fatalf("degraded production server: %v", err)
	}
	spec, ok := server.registry.Get("a2a_contract")
	if !ok {
		t.Fatal("degraded production server omitted safe contract reads")
	}
	if _, _, err := spec.Handler(t.Context(), json.RawMessage(`{"action":"deprecate","id":"XC-axon-orders","successor":"XC-axon-next@1.0.0","sunset":"2026-12-01"}`)); err == nil || !strings.Contains(err.Error(), "legacy contract write unavailable while space write dependencies are offline") {
		t.Fatalf("degraded legacy write error = %v", err)
	}
	if _, _, err := spec.Handler(t.Context(), json.RawMessage(`{"action":"preflight","id":"XC-axon-orders","version":"1.0.0"}`)); err != nil {
		t.Fatalf("safe degraded preflight unavailable: %v", err)
	}
}

// commitArtifactFile commits and pushes a placeholder file named
// "<id>.md" onto system's fixture clone — the on-disk shape
// cache.ResolveArtifactSpace's walk (and so SpaceOfArtifacts) looks for.
func commitArtifactFile(t *testing.T, fx *spacefixture.Fixture, system, id string) {
	t.Helper()
	dir := fx.Clone(system)
	rel := filepath.Join(system, "exchanges", id+".md")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rel), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", rel)
	runGitTest(t, dir, "-c", "user.name=fixture", "-c", "user.email=fixture@a2ahub.invalid", "commit", "-m", "add artifact "+id)
	runGitTest(t, dir, "push", "origin", "HEAD:main")
}

// mirrorHoldsArtifact is a test-local, single-space probe over
// cache.ResolveArtifactSpace — this package's own production
// mirrorHoldsArtifact walk was deleted with SpaceOfArtifacts' delegation to
// that shared rule (no-silent-yes-2026-08 P2a; wire.go's SpaceOfArtifacts
// closure calls cache.ResolveArtifactSpace directly and no longer needs a
// package-level helper of this shape). tools_lifecycle_test.go — off P2a's
// allowlist — still builds its own two-space WriteDeps fixture by calling
// this name directly (testTwoSpaceWriteDepsWithSystem), so it stays here
// rather than being deleted outright: a single-ref ResolveArtifactSpace
// call with a synthetic id either resolves (mirror holds it) or refuses
// (mirror does not), and the boolean collapses that either way.
func mirrorHoldsArtifact(mirrorDir, id string) bool {
	_, err := cache.ResolveArtifactSpace([]space.Ref{{ID: "probe"}}, func(space.Ref) string { return mirrorDir }, id)
	return err == nil
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", gitfixture.Args(args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
}

func TestParseGitHubRepoHTTPS(t *testing.T) {
	t.Parallel()
	owner, name, err := parseGitHubRepo("https://github.com/acme/space.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme" || name != "space" {
		t.Fatalf("got %q/%q", owner, name)
	}
}

func TestParseGitHubRepoSSH(t *testing.T) {
	t.Parallel()
	owner, name, err := parseGitHubRepo("git@github.com:acme/space")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "acme" || name != "space" {
		t.Fatalf("got %q/%q", owner, name)
	}
}

func TestParseGitHubRepoMalformed(t *testing.T) {
	t.Parallel()
	if _, _, err := parseGitHubRepo("not-a-url"); err == nil {
		t.Fatal("expected an error for a malformed repo URL")
	}
}

func TestResolvePaths(t *testing.T) {
	t.Parallel()
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.ProjectConfig == "" || p.MachineConfig == "" || p.ProjectRoot == "" || p.Staging == "" {
		t.Fatalf("expected all Paths fields populated, got %+v", p)
	}
}

// reason: mutates process env (t.Setenv) — not run in parallel.
func TestNewServerFromConfigFullHappyPath(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	fx := spacefixture.New(t, "axon", "beta")
	fixValidManifest(t, fx, "beta")
	t.Setenv("A2A_TOKEN_FIXTURE_SPACE", "test-token")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n  - id: fixture-space\n    repo_url: "+fx.RemoteURL()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  fixture-space: \"env:A2A_TOKEN_FIXTURE_SPACE\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	if server == nil {
		t.Fatal("expected a non-nil server")
	}
	if server.preCall == nil {
		t.Fatal("expected connected server pre-call hook")
	}
	offlineOrigin := fx.OriginDir + ".offline"
	if err := os.Rename(fx.OriginDir, offlineOrigin); err != nil {
		t.Fatal(err)
	}
	if err := server.preCall(context.Background(), WorkToolName); err != nil {
		t.Fatalf("a2a_work invoked generic mirror refresh: %v", err)
	}
	if err := server.preCall(context.Background(), "a2a_read"); err == nil {
		t.Fatal("control tool unexpectedly skipped unavailable mirror refresh")
	}
}

// reason: mutates process env indirectly via machine config credential
// lookup failure path — kept sequential alongside the happy-path test.
func TestNewServerFromConfigNoConnectedSpaces(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte("system: beta\nspaces: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	assertSpaceFreeSurface(t, server.registry.ToolNames())
}

// spaceFreeToolNames is what a session with no usable space must offer:
// `a2a_read` over the local cache, `a2a_whatsnew` over the embedded release-
// notes corpus, and `a2a_adapt` over the embedded corpus plus the project's
// own `.a2a/config.yaml` (P13, answers-that-hold-2026-08: neither input needs
// a connected space). Sorted, because ToolNames() is.
//
// It used to be six names — `a2a_inbox`, `a2a_outbox`, `a2a_show`,
// `a2a_thread`, `a2a_search`, `a2a_contracts` — the pre-P15 per-verb read
// tools, kept alive by wire.go's own copy of the registration long after
// BuildRegistry folded them into `a2a_read`'s `view` enum. tools_test's
// `removed` list asserts those exact names are ABSENT from the connected
// surface, so the two registries disagreed on what the read tools are
// CALLED, and the catalogue an agent reads documented only one of them.
var spaceFreeToolNames = []string{"a2a_adapt", "a2a_read", "a2a_whatsnew"}

// assertSpaceFreeSurface checks what a degraded session offers, TWICE, and the
// second check is the one that catches a new fork.
//
// The literal comparison is the readable half: it says out loud which two tools
// a session with no usable space serves. On its own it is weak — a literal can
// always be edited to match whatever the code now does, which is how the six
// retired names survived every release since v0.1.0 here.
//
// So the second half resolves the connected surface from BuildRegistry and
// requires every offered name to exist there too. A degraded session must offer
// FEWER tools than a healthy one, never DIFFERENT ones: an agent that learns a
// tool name with no space connected must still find it after connecting, and
// `skill/a2ahub/reference/commands.md` — the skill-drift-gated catalogue an
// agent actually reads — is generated from the connected registry alone, so a
// name only the degraded path serves is a name nothing documents.
//
// It matters that this runs on `names` from a server built by
// NewServerFromConfig, not on a registry the test assembles itself. Asserting
// the subset against a registry built by calling registerSpaceFree would be a
// TAUTOLOGY, since BuildRegistry composes that same function — it could never
// fail. Reading the real wire path is what makes a second registration added
// there visible, and that was verified by planting one.
//
// The ceiling, stated so nobody credits this with more than it does: for the
// same compositional reason, the subset half cannot see a wrong name added
// INSIDE registerSpaceFree — BuildRegistry would then serve it too. That case
// is the literal's, which is why both halves are here.
func assertSpaceFreeSurface(t *testing.T, names []string) {
	t.Helper()

	if len(names) != len(spaceFreeToolNames) {
		t.Fatalf("space-free surface = %v, want exactly %v", names, spaceFreeToolNames)
	}
	for i, want := range spaceFreeToolNames {
		if names[i] != want {
			t.Fatalf("space-free surface = %v, want exactly %v", names, spaceFreeToolNames)
		}
	}

	connected := BuildRegistry(nil, WriteDeps{}, "", nil, NewDeps{})
	for _, name := range names {
		if _, ok := connected.Get(name); !ok {
			t.Errorf("this session offers %q and a CONNECTED session does not — a degraded surface "+
				"must be a subset of the healthy one, never a different set of names. That is the "+
				"defect P15 left behind: wire.go kept registering a2a_inbox/outbox/show/thread/"+
				"search/contracts after BuildRegistry folded them into a2a_read's view enum, so the "+
				"onboarding session was served six tool names nothing documents.", name)
		}
	}
}

func TestNewServerFromConfigNoProjectConfig(t *testing.T) {
	t.Parallel()
	p := Paths{
		ProjectConfig: t.TempDir() + "/does-not-exist/config.yaml",
		MachineConfig: t.TempDir() + "/machine.yaml",
		ProjectRoot:   t.TempDir(),
		Staging:       t.TempDir(),
	}
	_, err := NewServerFromConfig(context.Background(), p, "0.0.1-test")
	if err == nil {
		t.Fatal("expected an error when no project config exists")
	}
}

// TestNewServerFromConfigMultipleSpacesRefusesUnconnectedSubmitSpace is
// P7's own regression, now proving the STRONGER answer that replaced its
// first one. Two reachable connected spaces used to build the legacy write
// graph against cfg.Spaces[0] alone, so a submit landed silently in the
// FIRST configured space rather than the one the artifact declared. P7's
// first fix refused every such write outright; the residual drain replaced
// that with per-call resolution, so a2a_submit now reads the draft's own
// `space:` field and refuses only when it names a space this session is not
// connected to — naming the ones it IS connected to.
//
// The safety property is unchanged and still asserted below: nothing
// reaches any space's real history. What changed is that a draft naming a
// CONNECTED space is no longer collateral damage, which is the point of the
// drain. Its positive half is TestNewServerFromConfigMultipleSpacesSubmit-
// ResolvesAConnectedSpace, directly below.
//
// reason: mutates process env through the production credential seam.
func TestNewServerFromConfigMultipleSpacesRefusesUnconnectedSubmitSpace(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fx := spacefixture.New(t, "beta")
	fixValidManifest(t, fx, "beta")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	// Both space entries share ONE origin — this test is about the
	// AMBIGUITY of having two connected spaces, not about their content,
	// so a second throwaway git fixture buys nothing.
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n"+
			"  - id: space-one\n    repo_url: "+fx.RemoteURL()+"\n"+
			"  - id: space-two\n    repo_url: "+fx.RemoteURL()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  space-one: \"env:A2A_TOKEN_SPACE_ONE\"\n  space-two: \"env:A2A_TOKEN_SPACE_TWO\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stagingDir := filepath.Join(projectRoot, ".a2a", "staging")
	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: stagingDir}

	beforeSHA := fx.HeadSHA(fx.Clone("beta"), "main")

	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}

	// Q3: reads are unaffected — the multi-space surface still offers
	// a2a_read (backed by cache.Store, which already loops every connected
	// space in buildStore, not by the write graph this test is about).
	if _, ok := server.registry.Get("a2a_read"); !ok {
		t.Fatal("a multi-space session must still offer a2a_read")
	}

	id := "XQ-beta-20260721-a900"
	writeStagedDraft(t, stagingDir, id, "question")

	spec, ok := server.registry.Get("a2a_submit")
	if !ok {
		t.Fatal("expected a2a_submit to be registered for a reachable multi-space session")
	}
	args, err := json.Marshal(SubmitInput{IDs: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, submitErr := spec.Handler(t.Context(), args)
	if submitErr == nil {
		t.Fatal("expected a2a_submit to refuse a draft naming an unconnected space")
	}
	// writeStagedDraft's fixture names `fixture-space`, which this session
	// is not connected to. The refusal must say so and name what IS
	// connected — never silently pick one.
	if !strings.Contains(submitErr.Error(), "is not connected") {
		t.Fatalf("submit error = %v, want a not-connected refusal naming the draft's space", submitErr)
	}
	for _, want := range []string{"space-one", "space-two"} {
		if !strings.Contains(submitErr.Error(), want) {
			t.Fatalf("submit error = %v, want it to name connected space %q", submitErr, want)
		}
	}

	// The safety property US-1 exists for: nothing was pushed anywhere,
	// least of all the wrong space's real history.
	afterSHA := fx.HeadSHA(fx.Clone("beta"), "main")
	if afterSHA != beforeSHA {
		t.Fatalf("origin HEAD moved from %s to %s — the ambiguous write was NOT refused before it reached the space", beforeSHA, afterSHA)
	}

	// The positive half, in the SAME real server: a draft naming a
	// CONNECTED space must get PAST resolution. It still fails afterwards —
	// this fixture has no host to push to — and that is the point: the
	// error must no longer be a space refusal. Before the drain, this call
	// could not get past the blanket funnel no matter what the draft said.
	connectedID := "XQ-beta-20260721-a901"
	writeStagedDraftForSpace(t, stagingDir, connectedID, "question", "space-two")
	connectedArgs, err := json.Marshal(SubmitInput{IDs: []string{connectedID}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, connectedErr := spec.Handler(t.Context(), connectedArgs)
	if connectedErr != nil {
		for _, refusal := range []string{"is not connected", "space is required when multiple spaces are connected"} {
			if strings.Contains(connectedErr.Error(), refusal) {
				t.Fatalf("a draft naming connected space-two was refused on space grounds: %v", connectedErr)
			}
		}
	}
}

// TestNewServerFromConfigMultipleSpacesPreCallRefreshesEverySpace is the
// read-refresh half of the same Spaces[0]-only root cause (P7): the
// pre-call hook used to refresh only write.MirrorDir — the FIRST connected
// space — leaving every other connected space's mirror frozen at server
// start for the life of the session. This proves BOTH connected spaces'
// mirrors are attempted, by making the SECOND one unreachable and watching
// the refresh surface that failure.
//
// reason: mutates process env through the production credential seam.
func TestNewServerFromConfigMultipleSpacesPreCallRefreshesEverySpace(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")
	fxTwo := spacefixture.New(t, "beta")
	fixValidManifest(t, fxTwo, "beta")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n"+
			"  - id: space-one\n    repo_url: "+fxOne.RemoteURL()+"\n"+
			"  - id: space-two\n    repo_url: "+fxTwo.RemoteURL()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  space-one: \"env:A2A_TOKEN_SPACE_ONE\"\n  space-two: \"env:A2A_TOKEN_SPACE_TWO\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("NewServerFromConfig: %v", err)
	}
	if server.preCall == nil {
		t.Fatal("expected connected server pre-call hook")
	}

	// space-one stays reachable; only space-two (the SECOND connected
	// space — never write.MirrorDir, the pre-fix refresh target) goes
	// offline. If the pre-call hook only ever refreshed the first space,
	// this call would report success.
	offlineOrigin := fxTwo.OriginDir + ".offline"
	if err := os.Rename(fxTwo.OriginDir, offlineOrigin); err != nil {
		t.Fatal(err)
	}
	if err := server.preCall(context.Background(), "a2a_read"); err == nil {
		t.Fatal("expected the pre-call refresh to surface the SECOND connected space's unreachable mirror")
	}
}

// TestNewServerFromConfigUnreachableSpaceStillServesReads is the regression
// for a full outage where a partial degradation belongs.
//
// buildWriteDeps clones or fetches the mirror, so a network blip, an expired
// credential, or a laptop on a plane made `a2a mcp` fail to START — while
// every read tool needs nothing but the local mirror already on disk. The
// CLI's read path has made the opposite choice since CC-092 (buildStore is
// explicitly tolerant of absent config, absent spaces, absent machine
// config); this brings the second surface in line.
//
// A session that can still tell you what your counterparty sent beats a
// session that will not open.
func TestNewServerFromConfigUnreachableSpaceStillServesReads(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_FIXTURE_SPACE", "test-token")

	projectRoot := t.TempDir()
	projectConfig := filepath.Join(projectRoot, ".a2a", "config.yaml")
	machineConfig := filepath.Join(t.TempDir(), "machine-config.yaml")
	if err := os.MkdirAll(filepath.Dir(projectConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	// A repo_url that resolves to nothing — the shape of an unreachable
	// space without needing the network to be down.
	unreachable := filepath.Join(t.TempDir(), "no-such-origin.git")
	if err := os.WriteFile(projectConfig, []byte(
		"system: beta\nspaces:\n  - id: fixture-space\n    repo_url: "+unreachable+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineConfig, []byte(
		"credentials:\n  fixture-space: \"env:A2A_TOKEN_FIXTURE_SPACE\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := Paths{ProjectConfig: projectConfig, MachineConfig: machineConfig, ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}
	server, err := NewServerFromConfig(context.Background(), p, "0.0.1-test", unavailableWorkToolDeps())
	if err != nil {
		t.Fatalf("an unreachable space must not stop the server from starting: %v", err)
	}
	names := server.registry.ToolNames()
	wantDegraded := []string{"a2a_adapt", "a2a_read", "a2a_whatsnew", "a2a_work"}
	if !slices.Equal(names, wantDegraded) {
		t.Fatalf("unreachable-space surface = %v, want %v", names, wantDegraded)
	}
	// Exact names, not substrings: `a2a_contracts` (plural) is a READ tool
	// and `a2a_contract` (singular) is the write one — a substring match
	// cannot tell them apart and would fail on a correct registry.
	writeTools := map[string]bool{
		"a2a_submit": true, "a2a_lifecycle": true, "a2a_exchange": true,
		"a2a_contract": true, "a2a_new": true,
	}
	for _, n := range names {
		if writeTools[n] {
			t.Errorf("write tool %q was registered over an unreachable space — a write that cannot "+
				"reach the space would fail deep inside the funnel instead of not being offered", n)
		}
	}
}

// --- buildWriteDeps: per-space builder + the resolution seam -------------

// reason: mutates process env through the production credential seam.
func TestBuildWriteDepsTwoSpacesYieldDistinctMirrorDirsAndResolveSpace(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")
	fxTwo := spacefixture.New(t, "beta")
	fixValidManifest(t, fxTwo, "beta")

	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: fxOne.RemoteURL()},
		{ID: "space-two", RepoURL: fxTwo.RemoteURL()},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE", "space-two": "env:A2A_TOKEN_SPACE_TWO",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err != nil {
		t.Fatalf("buildWriteDeps: %v", err)
	}
	if write.SpaceID != "space-one" || write.MirrorDir == "" {
		t.Fatalf("Spaces[0] set = %+v, want space-one with a non-empty MirrorDir", write)
	}
	if write.ResolveSpace == nil || write.SpaceOfArtifacts == nil {
		t.Fatal("a two-space session must install both ResolveSpace and SpaceOfArtifacts")
	}

	other, err := write.ResolveSpace("space-two")
	if err != nil {
		t.Fatalf("ResolveSpace(space-two): %v", err)
	}
	if other.SpaceID != "space-two" {
		t.Fatalf("ResolveSpace(space-two).SpaceID = %q, want space-two", other.SpaceID)
	}
	if other.MirrorDir == write.MirrorDir {
		t.Fatalf("space-one and space-two share MirrorDir %q — each connected space must build its own mirror", write.MirrorDir)
	}

	if _, err := write.ResolveSpace("space-three"); err == nil {
		t.Fatal("expected an error resolving an unconnected space id")
	} else if !strings.Contains(err.Error(), "space-three") {
		t.Fatalf("unconnected-space error = %v, want it to name %q", err, "space-three")
	}
}

// mcpWireBareOrigin creates a bare origin with NO branch ever created and NO
// commit ever pushed — verified empirically (no-silent-yes-2026-08 P2b's own
// brief) that such an origin's clone resolves no refs/remotes/origin/HEAD at
// all. This is the "a remote publishes no HEAD" shape ResolveBaseBranch (and
// therefore buildWriteDepsForSpace) must refuse with REF-026.
func mcpWireBareOrigin(t *testing.T) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGitTest(t, "", "init", "--bare", "-q", origin)
	gitfixture.HardenRepo(t, origin)
	return origin
}

// mcpWireOriginOnBranch creates a bare origin whose default branch is
// branch (never "main"), with one commit pushed to it — verified
// empirically that a plain `git clone` of such an origin resolves
// refs/remotes/origin/HEAD to "origin/<branch>".
func mcpWireOriginOnBranch(t *testing.T, branch string) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	runGitTest(t, "", "init", "--bare", "-q", "-b", branch, origin)
	gitfixture.HardenRepo(t, origin)

	seed := t.TempDir()
	runGitTest(t, "", "init", "-q", "-b", branch, seed)
	// A structurally-valid space.Manifest (see fixValidManifest's own doc
	// comment) — buildWriteDepsForSpace parses space.yaml right after
	// resolving the base branch, so a seed with none never reaches this
	// test's own assertion.
	manifest := "schema: space/v1\nspace: fixture-space\nmin_binary_version: \"0.19.0\"\nparticipants:\n" +
		"  - system: beta\n    org: fixture\n    section: beta\n    owners: [beta-bot]\n    status: active\n    joined: \"2026-01-01\"\n"
	if err := os.WriteFile(filepath.Join(seed, "space.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write seed manifest: %v", err)
	}
	runGitTest(t, seed, "add", "-A")
	runGitTest(t, seed, "-c", "user.name=fixture", "-c", "user.email=fixture@a2ahub.invalid", "commit", "-q", "-m", "seed")
	runGitTest(t, seed, "remote", "add", "origin", origin)
	runGitTest(t, seed, "push", "-q", "origin", branch)
	return origin
}

// TestBuildWriteDepsForSpaceRefusesREF026WhenRemotePublishesNoHead is this
// phase's own acceptance: a remote publishing no refs/remotes/origin/HEAD
// must be REFUSED by name (REF-026), never silently built with the write
// funnel pointed at "main" — the exact defect no-silent-yes-2026-08 exists
// to end. This is ALSO the reachability proof check-error-codes.sh's
// obligation 2 requires: REF-026 firing through a real production entry
// point (buildWriteDeps, one of this phase's four repointed write-funnel
// sites), not only from internal/space's own unit tests.
//
// reason: mutates process env through the production credential seam.
func TestBuildWriteDepsForSpaceRefusesREF026WhenRemotePublishesNoHead(t *testing.T) {
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")

	origin := mcpWireBareOrigin(t)
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: origin},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	_, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err == nil {
		t.Fatal("buildWriteDeps = nil error, want a REF-026 refusal — an unresolvable remote HEAD must never silently build write deps pointed at \"main\"")
	}
	if !strings.Contains(err.Error(), "REF-026") {
		t.Fatalf("error = %q, want it to name REF-026 (schemas/errors/v1/registry.yaml)", err.Error())
	}
	if strings.Contains(err.Error(), `"main"`) {
		t.Fatalf("error = %q, must never itself suggest \"main\" as a fallback", err.Error())
	}
}

// TestBuildWriteDepsForSpaceDerivesNonMainBaseBranch is this phase's own
// acceptance: a space whose remote default is "master" gets "master" as its
// derived HostCfg.BaseBranch — not the literal "main" no-silent-yes-2026-08
// exists to stop trusting.
//
// reason: mutates process env through the production credential seam.
func TestBuildWriteDepsForSpaceDerivesNonMainBaseBranch(t *testing.T) {
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")

	origin := mcpWireOriginOnBranch(t, "master")
	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: origin},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err != nil {
		t.Fatalf("buildWriteDeps: %v", err)
	}
	if write.HostCfg.BaseBranch != "master" {
		t.Fatalf("HostCfg.BaseBranch = %q, want %q", write.HostCfg.BaseBranch, "master")
	}
}

// TestBuildWriteDepsFailingSpaceSurfacesErrorOnlyWhenResolved is the P7
// per-space-error contract: a space that fails to build must not cost the
// session anything else — the failure is stored and returned only when
// THAT space is the one a call targets, never at server start, never for a
// different (working) space.
//
// reason: mutates process env through the production credential seam.
func TestBuildWriteDepsFailingSpaceSurfacesErrorOnlyWhenResolved(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")
	unreachable := filepath.Join(t.TempDir(), "no-such-origin.git")

	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: fxOne.RemoteURL()},
		{ID: "space-two", RepoURL: unreachable},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE", "space-two": "env:A2A_TOKEN_SPACE_TWO",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err != nil {
		t.Fatalf("Spaces[0] (space-one) is reachable — buildWriteDeps must not fail: %v", err)
	}

	sameSpace, err := write.ResolveSpace("space-one")
	if err != nil {
		t.Fatalf("resolving the already-built Spaces[0] must not fail: %v", err)
	}
	if sameSpace.MirrorDir != write.MirrorDir {
		t.Fatalf("ResolveSpace(space-one).MirrorDir = %q, want %q", sameSpace.MirrorDir, write.MirrorDir)
	}

	if _, err := write.ResolveSpace("space-two"); err == nil {
		t.Fatal("expected space-two's build failure to surface when space-two is targeted")
	}
}

// TestBuildWriteDepsSpaceFetchIsBoundedAndNonPrimaryTimeoutStaysStoredNotFatal
// is agent-exchange-2026-08 spec 04 AC 6's own proof: a per-space mirror
// fetch that stalls no longer blocks buildWriteDeps forever, and — mirroring
// TestBuildWriteDepsFailingSpaceSurfacesErrorOnlyWhenResolved's own contract
// exactly, immediately above — a NON-PRIMARY space's timeout is stored, not
// fatal: buildWriteDeps still succeeds over the reachable primary, and the
// timeout surfaces only once space-two is actually resolved. A stall on the
// PRIMARY space remains fatal, unchanged from before this bound existed
// (buildWriteDeps' own primary.err branch, untouched by this phase).
//
// The stalling fetch is INJECTED (SetCloneOrFetchForBuildTest) rather than a
// real unreachable git remote: a real stall would have to actually wait out
// whatever bound is under test, which defeats a fast, deterministic unit
// test. SetBuildWriteDepsSpaceTimeoutForTest shrinks the bound itself so
// this test's own wall-clock cost stays well under a second regardless of
// the production 30s value.
func TestBuildWriteDepsSpaceFetchIsBoundedAndNonPrimaryTimeoutStaysStoredNotFatal(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")

	// 2s, not 50ms. The stalling fake below blocks until ctx.Done(), so ANY
	// bound proves it is bounded — but this SAME global timeout also applies to
	// space-one, whose fetch is a real `git clone` of a local fixture. Under
	// -race with the package's other tests running, that legitimately exceeds
	// 50ms, and this test failed roughly half of four consecutive runs with
	// `signal: killed` on the PRIMARY's clone — the bound killing the thing it
	// was never about. The elapsed ceiling below (5s) is what actually asserts
	// boundedness; this constant only has to be a number a local clone always
	// beats and an infinite stall never does.
	defer SetBuildWriteDepsSpaceTimeoutForTest(2 * time.Second)()

	const stallingRepoURL = "stall://space-two"
	defer SetCloneOrFetchForBuildTest(func(ctx context.Context, dir, repoURL string, credential host.Credential) error {
		if repoURL == stallingRepoURL {
			<-ctx.Done() // simulate a hung network fetch that never returns on its own
			return ctx.Err()
		}
		return space.CloneOrFetch(ctx, dir, repoURL, credential)
	})()

	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: fxOne.RemoteURL()},
		{ID: "space-two", RepoURL: stallingRepoURL},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE", "space-two": "env:A2A_TOKEN_SPACE_TWO",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	start := time.Now()
	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Spaces[0] (space-one) is reachable — buildWriteDeps must not fail: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("buildWriteDeps took %v — a stalling non-primary space's fetch was not bounded", elapsed)
	}

	_, err = write.ResolveSpace("space-two")
	if err == nil {
		t.Fatal("expected space-two's bounded-timeout failure to surface when space-two is targeted")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("space-two error = %v, want it to wrap context.DeadlineExceeded", err)
	}
}

// TestBuildWriteDepsResolveSpaceIsNotAliasedByTheAmbiguousFunnelInstall pins
// the aliasing hazard wire.go:249 creates: newServerFromConfig mutates the
// RETURNED write's Funnel field to ambiguousSpaceFunnel AFTER buildWriteDeps
// returns. If bySpace stored pointers instead of values, that mutation
// would leak into every space's own resolved dep set, and a wave-2 handler
// resolving ANY space would get a funnel that refuses everything instead of
// the real one — permanently, for the life of the session.
func TestBuildWriteDepsResolveSpaceIsNotAliasedByTheAmbiguousFunnelInstall(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")
	fxTwo := spacefixture.New(t, "beta")
	fixValidManifest(t, fxTwo, "beta")

	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: fxOne.RemoteURL()},
		{ID: "space-two", RepoURL: fxTwo.RemoteURL()},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE", "space-two": "env:A2A_TOKEN_SPACE_TWO",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err != nil {
		t.Fatalf("buildWriteDeps: %v", err)
	}
	if _, ok := write.Funnel.(ambiguousSpaceFunnel); ok {
		t.Fatal("buildWriteDeps must never install the ambiguous funnel itself")
	}

	// Mirrors wire.go:249's own install, exactly.
	write.Funnel = ambiguousSpaceFunnel{connected: spaceIDs(cfg.Spaces)}

	resolvedOne, err := write.ResolveSpace("space-one")
	if err != nil {
		t.Fatalf("ResolveSpace(space-one): %v", err)
	}
	if _, ok := resolvedOne.Funnel.(ambiguousSpaceFunnel); ok {
		t.Fatal("ResolveSpace(space-one)'s Funnel is the ambiguous refusal — the wire.go:249 " +
			"mutation leaked into the resolved dependency set through a shared/aliased build")
	}

	resolvedTwo, err := write.ResolveSpace("space-two")
	if err != nil {
		t.Fatalf("ResolveSpace(space-two): %v", err)
	}
	if _, ok := resolvedTwo.Funnel.(ambiguousSpaceFunnel); ok {
		t.Fatal("ResolveSpace(space-two)'s Funnel is the ambiguous refusal — the wire.go:249 " +
			"mutation leaked into the resolved dependency set through a shared/aliased build")
	}
}

// reason: mutates process env through the production credential seam.
func TestBuildWriteDepsSpaceOfArtifactsRefusesSplitBatch(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")
	fxTwo := spacefixture.New(t, "beta")
	fixValidManifest(t, fxTwo, "beta")
	commitArtifactFile(t, fxOne, "beta", "XQ-beta-20260817-aaaa")
	commitArtifactFile(t, fxTwo, "beta", "XQ-beta-20260817-bbbb")

	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: fxOne.RemoteURL()},
		{ID: "space-two", RepoURL: fxTwo.RemoteURL()},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE", "space-two": "env:A2A_TOKEN_SPACE_TWO",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err != nil {
		t.Fatalf("buildWriteDeps: %v", err)
	}

	_, err = write.SpaceOfArtifacts([]string{"XQ-beta-20260817-aaaa", "XQ-beta-20260817-bbbb"})
	if err == nil {
		t.Fatal("expected a split-batch refusal")
	}
	for _, want := range []string{"space-one", "space-two"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("split-batch error = %v, want it to name %q", err, want)
		}
	}

	// A single-space id still resolves cleanly — the guard is about a
	// batch spanning spaces, not about deriving a space at all.
	only, err := write.SpaceOfArtifacts([]string{"XQ-beta-20260817-bbbb"})
	if err != nil {
		t.Fatalf("SpaceOfArtifacts(single id): %v", err)
	}
	if only != "space-two" {
		t.Fatalf("SpaceOfArtifacts(single id) = %q, want space-two", only)
	}
}

// reason: mutates process env through the production credential seam.
func TestBuildWriteDepsSpaceOfArtifactsRefusesUnknownID(t *testing.T) {
	// reason: t.Setenv below forbids t.Parallel() (AGENTS.md testing rails).
	t.Setenv("A2A_TOKEN_SPACE_ONE", "test-token-one")
	t.Setenv("A2A_TOKEN_SPACE_TWO", "test-token-two")

	fxOne := spacefixture.New(t, "beta")
	fixValidManifest(t, fxOne, "beta")
	fxTwo := spacefixture.New(t, "beta")
	fixValidManifest(t, fxTwo, "beta")

	cfg := space.ProjectConfig{System: "beta", Spaces: []space.Ref{
		{ID: "space-one", RepoURL: fxOne.RemoteURL()},
		{ID: "space-two", RepoURL: fxTwo.RemoteURL()},
	}}
	machine := space.MachineConfig{Credentials: map[string]string{
		"space-one": "env:A2A_TOKEN_SPACE_ONE", "space-two": "env:A2A_TOKEN_SPACE_TWO",
	}}
	projectRoot := t.TempDir()
	p := Paths{ProjectRoot: projectRoot, Staging: filepath.Join(projectRoot, ".a2a", "staging")}

	write, _, _, err := buildWriteDeps(context.Background(), cfg, machine, p, "0.0.1-test")
	if err != nil {
		t.Fatalf("buildWriteDeps: %v", err)
	}

	_, err = write.SpaceOfArtifacts([]string{"XQ-beta-20260817-zzzz"})
	if err == nil {
		t.Fatal("expected a refusal for an id no connected mirror holds")
	}
	// REF-025 (schemas/errors/v1/registry.yaml): the refusal names its own
	// registry code, not just the bare "no connected space's mirror holds"
	// phrase the pre-P2a version of this closure constructed itself.
	for _, want := range []string{"REF-025", "XQ-beta-20260817-zzzz", "space-one", "space-two"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unknown-id error = %v, want it to name %q", err, want)
		}
	}
}
