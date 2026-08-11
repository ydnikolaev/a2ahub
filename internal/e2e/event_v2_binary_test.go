// B33 (epic-backlog.md, wave 37a): this package's real-binary tier could
// never reach event/v2 at all — main_test.go's exec'd `a2a` was pinned
// below contract.ContractPublicationFloor (see main_test.go's
// harnessStampedVersion), which lifecycleEventSchema (internal/cli/
// cmd_lifecycle.go) and internal/contract/publication_plan.go both gate on.
// wave33_verify_test.go proved the three event/v2 verbs work via DIRECT
// CONSTRUCTION instead (real WriteFunnel + host.NewFakeHost) because
// raising the pin was off that phase's own allowlist — and documented, as
// its own finding, that this left cmd/a2a/wire.go's config load, credential
// resolution and mirror handling (the tier B28/B29/B30 all lived in)
// unreached for exactly these verbs.
//
// Now that the stamp is raised, this file drives the same three verbs
// through hostRig — the real exec'd binary, via cmd/a2a/wire.go — reusing
// wave33_verify_test.go's own fixtures/types/constants directly (same
// package): wave33Floor, writeContractDescriptorV2Operational,
// wave33ActivateEventProbe/wave33ContractActivationProbe.
//
// `contract publish` itself is NOT driven through the real binary here:
// data_adversarial_test.go/data_loop_test.go already recorded, empirically,
// that a hostRig raised to the event/v2 floor refuses a real contract-set-v2
// publication (a different, unrelated wall) — the contract descriptor and
// its `publish` event are seeded by a direct commit instead (the "arrange"
// idiom TestWave33ContractActivationDebtSurfacesToProducerOnlyUnderActionable
// and TestWave33OperationalItemsOnInboxAndOutboxJSON already use), and only
// `activate` itself is under test through the binary.
package e2e

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"gopkg.in/yaml.v3"
)

// raiseHostRigFloor overwrites r's origin space.yaml min_binary_version to
// floor via a direct commit+push (the "arrange" idiom
// TestWave33OperationalItemsOnInboxAndOutboxJSON already uses) — deliberately
// NOT applied to the shared properManifestYAML/fixOriginManifest
// (helpers_test.go), which stays at "0.0.0" for every OTHER hostRig/
// TestT3Scripts caller: raising it there would put the WHOLE package into
// event/v2 authoring at once, exactly the cascade B33 (epic-backlog.md)
// says to stop on if it spreads beyond this row. Call this BEFORE any
// rig's first `sync`, so that sync already reads the raised floor rather
// than needing a second one.
func raiseHostRigFloor(t *testing.T, r *hostRig, floor string, systems ...string) {
	t.Helper()
	arrange := t.TempDir()
	gitRun(t, "", "clone", r.fx.RemoteURL(), arrange)
	manifest := strings.Replace(properManifestYAML(r.spaceID, systems...),
		`min_binary_version: "0.0.0"`, `min_binary_version: "`+floor+`"`, 1)
	mustWrite(t, filepath.Join(arrange, "space.yaml"), manifest)
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "raise min_binary_version to "+floor+" (wave 37 B33 fixture)")
	gitRun(t, arrange, "push", "origin", "main")
}

// hostRigMirrorDir is the local path `a2a sync` clones/refreshes r's own
// mirror into (space.ResolveMirrorLocation's documented default,
// ".a2a/cache/mirrors/<space-id>", relative to the project root).
func hostRigMirrorDir(r *hostRig) string {
	return filepath.Join(r.projectDir, ".a2a", "cache", "mirrors", r.spaceID)
}

// listCommittedPaths returns every committed, non-.gitkeep file under
// prefix at ref in dir's git history, sorted.
func listCommittedPaths(t *testing.T, dir, ref, prefix string) []string {
	t.Helper()
	out := gitOutput(t, dir, "ls-tree", "-r", "--name-only", ref, "--", prefix)
	var found []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" || strings.HasSuffix(line, ".gitkeep") {
			continue
		}
		found = append(found, line)
	}
	sort.Strings(found)
	return found
}

// soleAddedPath returns the one path present in after but not in before —
// used to find the ONE event a single hostRig write call minted, in a
// mirror whose git history may already carry other, unrelated committed
// events (unlike wave33_verify_test.go's direct-construction tier, whose
// mirrorDir never has a prior COMMITTED event at all — see this file's own
// package doc comment on why the fixture here must be a real commit).
func soleAddedPath(t *testing.T, before, after []string) string {
	t.Helper()
	seen := make(map[string]bool, len(before))
	for _, p := range before {
		seen[p] = true
	}
	var added []string
	for _, p := range after {
		if !seen[p] {
			added = append(added, p)
		}
	}
	if len(added) != 1 {
		t.Fatalf("soleAddedPath: want exactly 1 newly committed path, got %d: %v (before=%v after=%v)", len(added), added, before, after)
	}
	return added[0]
}

// TestT3VerifyVerdictRefusedBelowContractPublicationFloor is the OTHER side
// of the boundary TestT3CloseAndVerifyVerdictThroughRealBinary proves from
// above: `--verdict` against a space whose min_binary_version has NOT been
// raised (properManifestYAML's default "0.0.0", left untouched here —
// never raiseHostRigFloor) is refused BEFORE any mirror read at all
// (cmd_lifecycle.go:1604-1610's eventSchema check runs on the already-
// loaded manifest, ahead of any per-target response resolution), naming
// contract.ContractPublicationFloor and the space's own floor — the exact
// guard B33 exists to make reachable through this tier's real binary, now
// proved refusing on the below-floor side as well as authoring on the
// at-floor side.
func TestT3VerifyVerdictRefusedBelowContractPublicationFloor(t *testing.T) {
	t.Parallel()
	axon := newHostRig(t, "axon", "axon", "beta")
	axon.mustRun("sync")

	stdout, stderr, code := axon.run("verify", "XQ-axon-20260811-nonexistent", "--verdict", "0:met:beta")
	if code == 0 {
		t.Fatalf("verify --verdict below the publication floor: succeeded, want a refusal\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "requires this space's min_binary_version to be at or above") {
		t.Fatalf("refusal does not name the floor condition (cmd_lifecycle.go:1604-1610): %s", stderr)
	}
	if !strings.Contains(stderr, wave33Floor) {
		t.Fatalf("refusal does not name the required floor %s: %s", wave33Floor, stderr)
	}
}

// extractExchangeID pulls the sole XS- id out of `respond`'s own stdout
// (cmd_lifecycle.go's shared submit(): "respond: opened PR <url> for
// <parentID>, <XS-id> (<state>)") — the real binary's own success message,
// never re-derived by walking git the way the direct-construction tier
// must (wave33_verify_test.go's own wave33FindResponseID, which has no PR
// body to read there).
func extractExchangeID(t *testing.T, stdout string) string {
	t.Helper()
	m := regexp.MustCompile(`XS-\S+`).FindString(stdout)
	if m == "" {
		t.Fatalf("respond stdout carries no XS- id: %s", stdout)
	}
	return strings.TrimRight(m, ",")
}

// TestT3ContractActivateThroughRealBinary is B33's item 1: `contract
// activate` driven through the real exec'd `a2a` binary via hostRig —
// cmd/a2a/wire.go's real config load, credential resolution and mirror
// handling, the tier wave33_verify_test.go's own direct-construction route
// could not reach for this verb. Asserts the committed event decodes as
// event/v2 with an `activation` block (wave33ActivateEventProbe, reused
// from wave33_verify_test.go — same package), AND that folding the space's
// real committed history through the real binary's own `thread` read
// raises no flag for it.
func TestT3ContractActivateThroughRealBinary(t *testing.T) {
	t.Parallel()
	axon := newHostRig(t, "axon", "axon", "beta")
	raiseHostRigFloor(t, axon, wave33Floor, "axon", "beta")

	contractID := "XC-axon-opact"
	arrange := t.TempDir()
	gitRun(t, "", "clone", axon.fx.RemoteURL(), arrange)
	writeContractDescriptorV2Operational(t, arrange, "axon", "opact", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")
	writeLifecycleEventVersion(t, arrange, "axon", 0, contractID, "publish", "axon", "1.0.0")
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: "+contractID+"@1.0.0 publish (wave 37 B33 fixture)")
	gitRun(t, arrange, "push", "origin", "main")

	axon.mustRun("sync")
	mirrorDir := hostRigMirrorDir(axon)
	before := listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")

	stdout := axon.mustRun("contract", "activate", contractID, "--version", "1.0.0", "--satisfies", "endpoint", "--note", "live now")
	if !strings.Contains(stdout, contractID) {
		t.Fatalf("contract activate: stdout does not name %s: %s", contractID, stdout)
	}

	axon.mustRun("sync")
	after := listCommittedPaths(t, mirrorDir, "HEAD", "axon/events")
	eventPath := soleAddedPath(t, before, after)

	raw := gitOutput(t, mirrorDir, "show", "HEAD:"+eventPath)
	var ev wave33ActivateEventProbe
	if err := yaml.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatalf("decode activate event %s: %v\nraw=%s", eventPath, err, raw)
	}
	if ev.Schema != "event/v2" {
		t.Fatalf("event.schema = %q, want event/v2 (mutation check: this must fail if the selector regresses to event/v1)", ev.Schema)
	}
	if ev.Transition != "activate" || ev.Subject != contractID || ev.Space != hostRigSpaceID {
		t.Fatalf("event = %+v, want transition activate, subject %s, space %s", ev, contractID, hostRigSpaceID)
	}
	if ev.Activation == nil {
		t.Fatal("event carries no `activation` block")
	}
	if ev.Activation.Version != "1.0.0" || ev.Activation.Status != "live" {
		t.Fatalf("activation = %+v, want version 1.0.0 status live", ev.Activation)
	}
	if len(ev.Activation.Satisfies) != 1 || ev.Activation.Satisfies[0] != "endpoint" {
		t.Fatalf("activation.satisfies = %v, want [endpoint]", ev.Activation.Satisfies)
	}

	// Fold acceptance through the REAL binary's own `thread` read
	// (cmd/a2a/wire.go's wiring) — the exact surface `a2a thread` uses in
	// production, never internal package state.
	threadJSON := axon.mustRun("thread", contractID, "--json")
	var result cache.ThreadResult
	if err := json.Unmarshal([]byte(threadJSON), &result); err != nil {
		t.Fatalf("decode thread --json: %v\nstdout=%s", err, threadJSON)
	}
	if len(result.Flags) != 0 {
		t.Fatalf("thread carries %d flags after activate, want 0 (fold refused/flagged the event): %+v", len(result.Flags), result.Flags)
	}
}

// TestT3CloseAndVerifyVerdictThroughRealBinary is B33's item 2:
// `verify --verdict` and `close --verdict` driven through the real exec'd
// `a2a` binary via hostRig, plus the refusals this tier had never asserted
// for these verbs: a malformed `--verdict` (a local, pre-floor,
// pre-fixture refusal) and `verify` against a parent carrying more than
// one tracked response, refused as ambiguous
// (internal/cli/cmd_lifecycle.go:1786, "disambiguate with --refs").
func TestT3CloseAndVerifyVerdictThroughRealBinary(t *testing.T) {
	t.Parallel()
	axon := newHostRig(t, "axon", "axon", "beta")
	beta := axon.peer("beta")
	raiseHostRigFloor(t, axon, wave33Floor, "axon", "beta")

	questionID := "XQ-axon-20260811-w902"
	arrange := t.TempDir()
	gitRun(t, "", "clone", axon.fx.RemoteURL(), arrange)
	seedAcceptedQuestion(t, arrange, questionID, "beta")
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: "+questionID+" accepted by beta (wave 37 B33 fixture)")
	gitRun(t, arrange, "push", "origin", "main")

	axon.mustRun("sync")
	beta.mustRun("sync")

	// A malformed --verdict is refused locally, BEFORE any floor/fixture
	// check even runs (lifecycleParseVerdicts, cmd_lifecycle.go) — asserted
	// first, against this same rig, needing no response state at all.
	stdout, stderr, code := axon.run("verify", questionID, "--verdict", "not-even-three-parts")
	if code != 2 {
		t.Fatalf("verify with a malformed --verdict: exit %d, want 2\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "want <index>:<met|unmet|not_warranted|not_exercised>:<cause_owner>") {
		t.Fatalf("malformed --verdict refusal does not name the expected shape: %s", stderr)
	}

	// First response.
	r1Stdout := beta.mustRun("respond", "--actor-name", "bot1", "--result", "answered", questionID)
	firstResponseID := extractExchangeID(t, r1Stdout)
	axon.mustRun("sync")

	// Second response — table.go admits StateResponded as a legal fromState
	// for `respond` (D-017's multi-response allowance). A DIFFERENT actor
	// name than the first call: wave33_verify_test.go's own documented
	// finding is that the operation key this repo's idempotency-by-design
	// derives from the call's own content is otherwise byte-identical to
	// the first response, so a second `--result answered` from the SAME
	// actor reads as a RETRY, not a second response.
	beta.mustRun("respond", "--actor-name", "bot2", "--result", "answered", questionID)
	axon.mustRun("sync")

	// `verify` against the BARE parent id, with two responses now tracked,
	// is refused as ambiguous — never silently guesses which one.
	stdout, stderr, code = axon.run("verify", questionID, "--verdict", "0:met:beta")
	if code == 0 {
		t.Fatalf("verify (bare parent id, 2 responses tracked): succeeded, want an ambiguous refusal\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "multiple responses") || !strings.Contains(stderr, "disambiguate with --refs") {
		t.Fatalf("refusal does not name the ambiguity condition (cmd_lifecycle.go:1786): %s", stderr)
	}

	// axon verifies the FIRST response with a verdict, disambiguated via
	// --refs — RoleOwner (applyResponseScoped). The success message names
	// the RESPONSE id (result.ArtifactIDs, cmd_lifecycle.go's shared
	// submit()), not the parent — mustRun already fails the test on a
	// non-zero exit, so a non-empty stdout is the only shape to check here.
	verifyStdout := axon.mustRun("verify", questionID, "--refs", firstResponseID, "--verdict", "0:met:beta")
	if !strings.Contains(verifyStdout, firstResponseID) {
		t.Fatalf("verify --verdict: stdout does not name %s: %s", firstResponseID, verifyStdout)
	}
	beta.mustRun("sync")

	// axon closes with a verdict — RoleOwner, legal from `responded`, driven
	// as its own standalone command (two responses tracked keeps D-024's
	// single-response auto-close convenience from having already fired, the
	// same reason wave33_verify_test.go's direct-construction twin needs a
	// second response at all).
	axon.mustRun("close", questionID, "--verdict", "0:met:beta")

	// `a2a thread --json`, through the real binary, shows both events'
	// verdicts[] — unlike `activation` (TestT3ContractActivateThroughRealBinary,
	// above), `verdicts[]` IS promoted onto TranscriptEvent (mirror.go's own
	// doc comment), so no raw-git read is needed for this assertion.
	axon.mustRun("sync")
	threadJSON := axon.mustRun("thread", questionID, "--json")
	var result cache.ThreadResult
	if err := json.Unmarshal([]byte(threadJSON), &result); err != nil {
		t.Fatalf("decode thread --json: %v\nstdout=%s", err, threadJSON)
	}
	if len(result.Flags) != 0 {
		t.Fatalf("thread carries %d flags, want 0: %+v", len(result.Flags), result.Flags)
	}
	var verifyVerdicts, closeVerdicts []cache.TranscriptVerdict
	for _, te := range result.Transcript {
		if te.Kind != "event" || te.Event == nil {
			continue
		}
		switch te.Event.Transition {
		case "verify":
			verifyVerdicts = te.Event.Verdicts
		case "close":
			closeVerdicts = te.Event.Verdicts
		}
	}
	for name, got := range map[string][]cache.TranscriptVerdict{"verify": verifyVerdicts, "close": closeVerdicts} {
		if len(got) != 1 {
			t.Fatalf("thread's %s event carries %d verdicts, want 1: %+v", name, len(got), got)
		}
		if got[0].Index != 0 || got[0].Verdict != "met" || got[0].CauseOwner != "beta" {
			t.Fatalf("thread's %s verdict = %+v, want {0 met beta}", name, got[0])
		}
	}
}
