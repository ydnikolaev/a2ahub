package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
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
// mirrorHoldsArtifact's walk (and so SpaceOfArtifacts) looks for.
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
// `a2a_read` over the local cache, and `a2a_whatsnew` over the embedded
// corpus. Sorted, because ToolNames() is.
//
// It used to be six names — `a2a_inbox`, `a2a_outbox`, `a2a_show`,
// `a2a_thread`, `a2a_search`, `a2a_contracts` — the pre-P15 per-verb read
// tools, kept alive by wire.go's own copy of the registration long after
// BuildRegistry folded them into `a2a_read`'s `view` enum. tools_test's
// `removed` list asserts those exact names are ABSENT from the connected
// surface, so the two registries disagreed on what the read tools are
// CALLED, and the catalogue an agent reads documented only one of them.
var spaceFreeToolNames = []string{"a2a_read", "a2a_whatsnew"}

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

// TestNewServerFromConfigMultipleSpacesRefusesAmbiguousLegacyWrite is P7's
// own regression: two REACHABLE connected spaces used to build the legacy
// write graph (a2a_submit/a2a_lifecycle/a2a_exchange/a2a_contract's legacy
// sub-verbs) against cfg.Spaces[0] alone — a submit naming an id whose
// space no caller can express landed silently in the FIRST configured
// space, never the one the artifact actually declared. This proves the
// funnel wired into the real server (not a hand-built test double) refuses
// instead, and names both connected spaces (US-2) rather than defaulting.
//
// reason: mutates process env through the production credential seam.
func TestNewServerFromConfigMultipleSpacesRefusesAmbiguousLegacyWrite(t *testing.T) {
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
		t.Fatal("expected a2a_submit to refuse an ambiguous multi-space write")
	}
	if !strings.Contains(submitErr.Error(), "space is required when multiple spaces are connected") {
		t.Fatalf("submit error = %v, want an ambiguous-space refusal", submitErr)
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
	wantDegraded := []string{"a2a_read", "a2a_whatsnew", "a2a_work"}
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
	for _, want := range []string{"space-one", "space-two"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("unknown-id error = %v, want it to name %q", err, want)
		}
	}
}
