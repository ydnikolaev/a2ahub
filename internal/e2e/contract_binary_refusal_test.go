// AC4's residual gap (docs/features/active/agent-exchange-2026-08/specs/
// 11-verification.md §3): every verb this epic added or changed must
// assert at least one refusal at the exec'd-binary tier. The lead measured
// the family and found `contract activate`, `contract adopt` and
// `contract deprecate` each had a binary-tier HAPPY path
// (event_v2_binary_test.go's TestT3ContractActivateThroughRealBinary,
// host_loop_test.go's TestHostLoopContractFamily steps 4/5) but no
// binary-tier REFUSAL. This file closes that gap, one test per verb, each
// exec'ing the real built binary through hostRig (host_loop_test.go)
// against a genuine domain guard — never a usage/parse error — cited at
// the guard's own file:line.
package e2e

import (
	"strings"
	"testing"
)

// writeContractDescriptorWithXBinding seeds a from-owned envelope/v2
// contract descriptor at slug/version carrying xBindingRaw verbatim — a
// file-local twin of wave33_verify_test.go's own
// writeContractDescriptorV2Operational (same package, same shape, the
// x_binding field instead of x_operational — that helper has no parameter
// for this field, so this is a sibling, not a copy of its body's logic).
func writeContractDescriptorWithXBinding(t *testing.T, mirrorDir, from, slug, version, xBindingRaw string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: " + hostRigSpaceID + "\n" +
		"from: " + from + "\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: bot}\n" +
		"created: 2026-07-21T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"compat_policy: strict-semver\n" +
		"schema_format: json-schema-2020-12\n" +
		xBindingRaw +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/contract.md", content)
}

// TestContractBinaryRefusalAdoptNonAdoptable drives `a2a contract adopt`
// through the real binary against a contract descriptor that declares
// itself non-adoptable via the bare `x_binding: none` sentinel —
// internal/cli/cmd_contract.go:1305-1321's P5 counterweight ("nobody may
// pin it"), which runs BEFORE any --major resolution. That ordering is
// exactly why this test needs no published version at all: a single
// committed descriptor is enough to reach the guard.
func TestContractBinaryRefusalAdoptNonAdoptable(t *testing.T) {
	t.Parallel()

	provider := newHostRig(t, "axon", "axon", "beta")
	consumer := provider.peer("beta")

	arrange := t.TempDir()
	gitRun(t, "", "clone", provider.fx.RemoteURL(), arrange)
	writeContractDescriptorWithXBinding(t, arrange, "axon", "nonbind", "0.0.0", "x_binding: none\n")
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: XC-axon-nonbind declares x_binding: none (P11 refusal fixture)")
	gitRun(t, arrange, "push", "origin", "main")

	consumer.mustRun("sync")
	stdout, stderr, code := consumer.run("contract", "adopt", "XC-axon-nonbind")
	if code == 0 {
		t.Fatalf("contract adopt against a non-adoptable (x_binding: none) contract: succeeded, want a refusal\nstdout=%s", stdout)
	}
	if !strings.Contains(stdout+stderr, "non-adoptable") {
		t.Fatalf("refusal does not name the non-adoptable condition (cmd_contract.go:1318): stdout=%s stderr=%s", stdout, stderr)
	}
}

// TestContractBinaryRefusalActivateBeforePublish drives `a2a contract
// activate` through the real binary against a version that was never
// published — internal/cli/cmd_contract.go:1501-1512's guard ("activation
// names a published version's operational readiness, it is not a way to
// publish one"), which runs before the x_operational[] declared-item
// check. Reuses event_v2_binary_test.go's own raiseHostRigFloor/
// wave33Floor (this verb only exists on event/v2) and
// wave33_verify_test.go's writeContractDescriptorV2Operational (same
// package) — but unlike TestT3ContractActivateThroughRealBinary, never
// seeds the publish event.
func TestContractBinaryRefusalActivateBeforePublish(t *testing.T) {
	t.Parallel()

	axon := newHostRig(t, "axon", "axon", "beta")
	raiseHostRigFloor(t, axon, wave33Floor, "axon", "beta")

	contractID := "XC-axon-unpub"
	arrange := t.TempDir()
	gitRun(t, "", "clone", axon.fx.RemoteURL(), arrange)
	writeContractDescriptorV2Operational(t, arrange, "axon", "unpub", "1.0.0",
		"x_operational:\n  - name: endpoint\n    state: absent\n")
	gitRun(t, arrange, "add", "-A")
	gitRun(t, arrange, "commit", "-m", "seed: "+contractID+" descriptor only, no publish event (P11 refusal fixture)")
	gitRun(t, arrange, "push", "origin", "main")

	axon.mustRun("sync")
	stdout, stderr, code := axon.run("contract", "activate", contractID, "--version", "1.0.0", "--satisfies", "endpoint")
	if code == 0 {
		t.Fatalf("contract activate against an unpublished version: succeeded, want a refusal\nstdout=%s", stdout)
	}
	if !strings.Contains(stderr, "has not been published") {
		t.Fatalf("refusal does not name the not-published condition (cmd_contract.go:1510): %s", stderr)
	}
}

// TestContractBinaryRefusalDeprecateUnpublishedVersion drives `a2a
// contract deprecate --version <v>` through the real binary against a
// version that was never itself published — illegal per the fold's own
// per-version predicate (internal/fold/contract.go:52-58's
// contractVersionVerdict: a specific-version deprecate requires
// versions[v] == StatePublished; any other state, including "never
// recorded", is VerdictIllegalTransition), reached through
// internal/cli/cmd_contract.go:723-758's legality check and reported as
// the stable LFC-001 illegal-transition code
// (internal/cli/cmd_lifecycle.go's verdictRefusalMessage).
//
// `a2a submit` on a contract type authors its OWN version-LESS publish
// event (submitFirstTransition's "contract" -> fold.TPublish,
// cmd_submit.go:625-627 passes no Version) — the legacy whole-subject
// form, which contractPublishedVersions (cmd_contract.go:575-596)
// deliberately excludes. So reaching the per-version guard needs at least
// one REAL versioned publish on record first (`contract publish
// --version 1.0.0`, the same call TestHostLoopContractFamily step 2
// makes) — deprecating any OTHER version then finds no matching entry in
// prior.Versions at all.
func TestContractBinaryRefusalDeprecateUnpublishedVersion(t *testing.T) {
	t.Parallel()

	provider := newHostRig(t, "axon", "axon", "beta")
	draft, id := provider.stageContract("undeprecated", "0.0.0")
	provider.mustRun("submit", draft)
	provider.mustRun("sync")
	provider.mustRun("contract", "publish", "--version", "1.0.0", id)
	provider.mustRun("sync")

	stdout, stderr, code := provider.run("contract", "deprecate",
		"--version", "9.9.9", "--successor", id+"@2.0.0", "--sunset", "2027-01-01", id)
	if code == 0 {
		t.Fatalf("contract deprecate against a never-published version: succeeded, want a refusal\nstdout=%s", stdout)
	}
	if !strings.Contains(stdout+stderr, "LFC-001") {
		t.Fatalf("refusal is not the illegal-transition code LFC-001: stdout=%s stderr=%s", stdout, stderr)
	}
}
