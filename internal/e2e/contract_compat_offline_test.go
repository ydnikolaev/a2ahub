// This file answers one question for the 2026-08-05 live GitHub matrix
// finding: can the POL-007 refusal it burned ~2 hours to surface — a
// contract's rolling-window scenario rewriting a version line's JSON Schema
// to a narrower type WITHOUT rewriting the declared valid fixture the prior
// version already published — be reached OFFLINE, through the real built
// binary, against testkit/fakegithub and a real local bare repo, in
// seconds? See TestContractCompatOfflinePOL007RollingWindow's own doc
// comment for the answer, ONE deviation from the brief's literal version
// numbers, and ONE genuine wall this investigation found in the process
// (a second `contract publish` of any kind is blocked by an unrelated
// remote-recovery defect unless the prior publish's own now-merged branch
// is pruned first — worked around here, never inside product code).
//
// Built on this package's EXISTING rig (host_loop_test.go's newHostRig/
// peer/mustRun/stageContract — TestHostLoopContractFamily there is the
// worked example this file's submit+publish-1.0.0 prologue copies exactly)
// rather than a new one, per the brief.
package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// contractCompatOverlayDescriptor renders the SAME identity
// (id/from/space/schema) stageContract's own draft carries — the fields
// cmd/a2a's contractPublicationEventBuilder.BuildContractPublicationEvent
// refuses on mismatch — so the staging overlay's own contract.md (required
// unconditionally by space.ContractCandidateReader.Read, never optional
// like schema/fixtures/artifacts) is accepted as the SAME contract's later
// candidate. The frontmatter `version:` field is irrelevant: internal/
// contract/publication_plan.go's finalizePublicationDescriptor
// unconditionally overwrites it with the --version target before planning,
// so the value here never surfaces in the published result.
func contractCompatOverlayDescriptor(system, id string) string {
	return "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: the export contract under test\n" +
		"space: " + hostRigSpaceID + "\n" +
		"from: " + system + "\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-05T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"0.0.0\"\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"---\n# Export\n\nWhat this contract covers.\n"
}

// pruneMergedContractPublishBranch deletes a contract-publish operation's
// now-merged head branch straight from the fixture origin — a throwaway
// clone + `git push --delete`, the same "arrange state directly, outside
// any `a2a` verb" idiom this package's own gitRun/mergeBranchToMain already
// use for state the CLI has no verb to reach.
//
// This exists ONLY because of a genuine wall this investigation found (see
// TestContractCompatOfflinePOL007RollingWindow's own doc comment): no `a2a`
// verb prunes a completed publication's branch, and leaving it in place
// blocks EVERY subsequent `contract publish` call outright, for a reason
// that has nothing to do with compatibility. `a2a sync` does not touch
// remote branches at all — it only fast-forwards the local mirror.
func pruneMergedContractPublishBranch(t *testing.T, r *hostRig, branch string) {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, "", "clone", r.fx.RemoteURL(), dir)
	gitRun(t, dir, "push", "origin", "--delete", branch)
}

// TestContractCompatOfflinePOL007RollingWindow drives spec 05a/§5.4b's
// computed-compatibility refusal (D-010, CC-080) end to end through the
// REAL built `a2a` binary: a real local bare-repo space
// (testkit/spacefixture), a real in-process GitHub stand-in
// (testkit/fakegithub), real `submit`/`contract publish` verbs — the same
// rig and the same worked publish sequence as host_loop_test.go's
// TestHostLoopContractFamily.
//
// ANSWER: yes, reachable offline, in a few seconds — CONDITIONALLY. The
// condition is the headline, not a footnote: it requires pruning the prior
// publish's own now-merged branch out of band first, because no `a2a` verb
// does it and an unpruned branch blocks EVERY subsequent `contract
// publish` outright (see WALL FOUND below). With that done, `a2a contract
// publish` genuinely refuses with POL-007, naming the exact offending
// fixture, through nothing but the real CLI + a real local git remote —
// ~3.3-3.7s for the refusal-and-control segment alone (t.Logf below),
// ~5.9-7.1s end to end including the 1.0.0 prologue (`go test`'s own
// per-test elapsed time); TestMain's one-time `go build` for the whole
// package is not part of either number. The two-tier plan's central
// premise holds for this defect class — with two caveats below, both found
// empirically rather than assumed, and both worth more to the plan than a
// test that only proved "it refuses".
//
// DEVIATION 1 — the brief's literal version numbers cannot reach POL-007
// at all. internal/contract/publication_plan.go's inferBump(baseline,
// target) reports "major" the moment the leading version component
// differs (1 -> 2), and internal/validate/compat.go's
// CheckComputedCompatibility explicitly skips computed compatibility for a
// major bump ("declares the change incompatible; computed compatibility is
// not checked for a major bump (§5.4b)") — by design, a major bump is
// presumed breaking and the check does not run at all. version.Baseline
// (internal/version/version.go) picks max{v in published : v < target}
// regardless of major line, so with only 1.0.0 published, 2.0.0
// unconditionally selects baseline=1.0.0 and unconditionally infers
// "major". This is a product fact, not a harness limitation: no candidate
// content, staging arrangement, or CLI flag can make a real major-version
// publish reach POL-007. The live incident's own framing — a "rolling-
// window" scenario, a schema rewrite WITHIN a version line — matches a
// minor bump (1.0.0 -> 1.1.0), which this test drives instead, to actually
// exercise the check the brief is asking about.
//
// WALL FOUND (worked around, not fixed — internal/space is off this
// brief's allowlist) — a second `contract publish` of ANY kind, for ANY
// reason, is unconditionally blocked while the FIRST publish's own
// now-merged branch is still on the remote:
//   - what: `a2a contract publish` refuses with "space: operation-conflict:
//     remote publication-head probe failed: space: operation-conflict:
//     remote recovery publication-plan-recompute" — BEFORE the compatibility
//     check is ever consulted. Reproduced with NO schema change at all
//     (publish 1.0.0, then publish 1.1.0 unmodified): same refusal, same
//     stage.
//   - where: internal/space/contract_operation_recovery.go's
//     ContractPublicationRecovery.ProbeContractPublicationHeads (line ~74)
//     calls proveHead for EVERY branch `git ls-remote --heads` returns under
//     the SYSTEM-WIDE (not contract- or version-scoped) namespace
//     "a2a/<system>/contract-publish/op-v1-*", and returns the FIRST error
//     from ANY of them immediately (line ~89: `if err != nil { return
//     ContractPublicationHeadListing{}, err }`), before the per-head
//     `relevant` filter (by contract ID) is even consulted. proveHead's own
//     verifyRecomputedPublication (line ~268) recomputes contract.
//     PlanPublication for that OLD, already-merged branch and refuses with
//     "publication-plan-recompute" the moment the recomputed plan disagrees
//     with what the branch's own trailer recorded — which it does here for
//     the very first (1.0.0) publish's own branch, never having been
//     pruned. cmd/a2a/contract_p6_wiring.go's publish() (line ~154) has no
//     branch-pruning step of its own, and no `a2a` verb performs one either
//     — `a2a sync` only fast-forwards the local mirror, never touches
//     remote refs.
//   - impact, verified half: this is not fakegithub-specific in the one way
//     checked — internal/host/remote_heads.go's ListContractPublishHeads
//     lists heads via `git ls-remote --heads` (a real ref query, not a
//     GitHub "open PR" API state query), so a merged-but-undeleted branch
//     is visible to the probe regardless of host. Most GitHub repos do NOT
//     have "automatically delete head branches" enabled by default, and
//     nothing in this codebase asserts it must be.
//   - impact, UNVERIFIED half: whether the recompute itself
//     (verifyRecomputedPublication) would also disagree with the recorded
//     plan digest against a REAL GitHub-authored commit/trailer shape is
//     not established here — only that the candidate it recomputes from is
//     re-derived from the probed branch's own tree, so it SHOULD
//     reconstruct deterministically in principle. This test traced the
//     mismatch far enough to locate it (file:line above) and to prove it
//     reproduces with zero schema involvement, not far enough to name the
//     exact byte/field that disagrees. That is the next investigation, not
//     this one's claim.
//   - workaround used here: pruneMergedContractPublishBranch (below)
//     deletes the 1.0.0 publish's own head branch from the fixture origin,
//     via a throwaway clone + `git push --delete` — never a product verb,
//     because none exists; never a change to internal/space, which is off
//     this brief's allowlist.
//   - what would have to change: either `a2a contract publish` (or the
//     underlying WriteFunnel) deletes/closes its own head branch once the
//     PR is merged, or ProbeContractPublicationHeads skips a branch whose
//     recorded target version is already resolvable in main's own
//     published history (it already trusts history for the EXPLICIT-target
//     fast path a few lines earlier in Publish() — the probe just never
//     asks the same question) instead of unconditionally re-verifying
//     every historical branch it happens to still see.
func TestContractCompatOfflinePOL007RollingWindow(t *testing.T) {
	t.Parallel()

	provider := newHostRig(t, "axon", "axon", "beta")

	// 1+2: author, submit, and publish 1.0.0 — IDENTICAL to
	// host_loop_test.go's own TestHostLoopContractFamily steps 1-2 (real
	// submit through the funnel, real preflight, real publish).
	// stageContract's own schema admits {"example": <string>} and its
	// valid fixture is a string ("replace-me") — the permissive baseline
	// the brief's step 1 asks for.
	draft, id := provider.stageContract("export", "0.0.0")
	provider.mustRun("submit", draft)
	if got := gitOutput(t, provider.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main"); !strings.Contains(got, "provides/export/contract.md") {
		t.Fatalf("origin main does not carry the contract at its §4.2 path:\n%s", got)
	}
	provider.mustRun("sync")
	provider.mustRun("contract", "publish", "--version", "1.0.0", id)
	provider.mustRun("sync")

	// WALL WORKAROUND (see this test's own doc comment): prune the 1.0.0
	// publish's own now-merged branch so the NEXT `contract publish` call
	// (whatever its outcome) is not blocked by an unrelated remote-recovery
	// refusal before it ever reaches the compatibility check.
	var publishBranch string
	for _, pr := range provider.gh.PRs() {
		if strings.Contains(pr.Head, "/contract-publish/") {
			publishBranch = pr.Head
		}
	}
	if publishBranch == "" {
		t.Fatalf("no contract-publish PR observed after publishing 1.0.0 (host calls: %v)", provider.gh.Requests())
	}
	pruneMergedContractPublishBranch(t, provider, publishBranch)

	bodyStart := time.Now()

	// step 3: rewrite the schema to a NARROWER type, leaving the
	// ALREADY-PUBLISHED 1.0.0 valid fixture (fixtures/valid/export.json:
	// {"example":"replace-me"}) exactly as it was published — the defect
	// class itself.
	//
	// The mirror's own working tree is unconditionally hard-reset before
	// every `a2a` invocation (internal/space/mirror.go's
	// checkoutRemoteHead, per internal/template/contract_scaffold.go's own
	// ContractSidecarsFromStaging doc comment), so staging is the only
	// place a LATER schema edit can survive to be read by a later verb.
	// `contract publish --staging <project-relative-dir>` is the real verb
	// for exactly this: it overlays a staging candidate atop the committed
	// mirror candidate (cmd/a2a/contract_p6_wiring.go's own
	// freezePublicationCandidate). The overlay directory needs its own
	// contract.md (space.ContractCandidateReader.Read reads it
	// unconditionally) at the same layout `a2a contract new`'s own
	// ScaffoldContractCandidateInStaging produces
	// (internal/template/contract_scaffold.go) — written here directly
	// because this test authors the descriptor by hand (stageContract's
	// own idiom), not through `contract new`'s scaffold.
	stagingArg := ".a2a/staging/axon/provides/export"
	stagingRoot := filepath.Join(provider.projectDir, filepath.FromSlash(stagingArg))
	mustWrite(t, filepath.Join(stagingRoot, "contract.md"), contractCompatOverlayDescriptor("axon", id))
	mustMkdirAll(t, filepath.Join(stagingRoot, "schema"))
	mustWrite(t, filepath.Join(stagingRoot, "schema", "export.schema.json"),
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"example":{"type":"integer"}},"additionalProperties":true}`+"\n")

	// step 4: publish 1.1.0 (a real minor bump within the SAME major line —
	// see this test's own doc comment, deviation 1, for why 1.1.0, not the
	// brief's literal 2.0.0) and assert the refusal: non-zero exit, POL-007,
	// and the exact offending fixture path named.
	stdout, stderr, code := provider.run("contract", "publish", "--version", "1.1.0", "--staging", stagingArg, id)
	combined := stdout + stderr
	if code == 0 {
		t.Fatalf("narrower-schema publish succeeded, want a POL-007 refusal\nstdout=%s\nstderr=%s", stdout, stderr)
	}
	if !strings.Contains(combined, "POL-007") {
		t.Fatalf("refusal does not cite POL-007:\n%s", combined)
	}
	if !strings.Contains(combined, "fixtures/valid/export.json") {
		t.Fatalf("refusal does not name the offending fixture:\n%s", combined)
	}

	// step 5, literal: pairing the narrower schema with a MATCHING new
	// fixture in the CANDIDATE does NOT help, and this is proven rather
	// than assumed. internal/validate/contract_adapters.go's
	// ContractCompatibilityAdapter.CheckCompatibility reads PriorFixtures
	// from the IMMUTABLE baseline (1.0.0)'s own already-published carried
	// set (CompatibilityCheckInput.BaselineEntries/BaselineBytes), never
	// from the candidate's own new fixture bytes (NewEntries/NewBytes feed
	// NewSchemas only) — so a "matching" fixture on the NEW candidate
	// cannot repair compatibility with a fixture that was already
	// published immutably under the OLD, wider schema. Publishing this
	// candidate must refuse identically.
	mustMkdirAll(t, filepath.Join(stagingRoot, "fixtures", "valid"))
	mustWrite(t, filepath.Join(stagingRoot, "fixtures", "valid", "export.json"), `{"example":7}`+"\n")
	stdout2, stderr2, code2 := provider.run("contract", "publish", "--version", "1.1.0", "--staging", stagingArg, id)
	combined2 := stdout2 + stderr2
	if code2 == 0 {
		t.Fatalf("narrower-schema + matching-new-fixture publish succeeded, want it to STILL refuse (the baseline is immutable)\nstdout=%s\nstderr=%s", stdout2, stderr2)
	}
	if !strings.Contains(combined2, "POL-007") {
		t.Fatalf("refusal does not cite POL-007:\n%s", combined2)
	}
	if !strings.Contains(combined2, "fixtures/valid/export.json") {
		t.Fatalf("refusal does not name the offending fixture:\n%s", combined2)
	}

	// step 5, the real control: this rig's publish path is not broken for
	// an unrelated reason. Widen the schema so the immutable 1.0.0 STRING
	// fixture still validates (union type), keep the new integer fixture
	// as evidence of the widened shape, and publish 1.1.0 for real.
	//
	// Asserted on the decoded --json status/target_version, not merely a
	// zero exit code: space.ContractPublicationService.Publish also returns
	// exit 0 for ContractPublicationAlreadyPublished and
	// ContractPublicationRepaired (a completion/recovery shortcut), neither
	// of which would prove a REAL new 1.1.0 publication landed — exactly
	// the "passes for an unrelated reason" failure mode the brief's own
	// step 5 rationale warns about.
	mustWrite(t, filepath.Join(stagingRoot, "schema", "export.schema.json"),
		`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"example":{"type":["string","integer"]}},"additionalProperties":true}`+"\n")
	published := provider.mustRun("contract", "publish", "--version", "1.1.0", "--staging", stagingArg, "--json", id)
	if !strings.Contains(published, `"status":"submitted"`) || !strings.Contains(published, `"target_version":"1.1.0"`) {
		t.Fatalf("control publish did not submit a real new 1.1.0 publication: %s", published)
	}

	t.Logf("TestContractCompatOfflinePOL007RollingWindow: refusal-and-control body wall clock (excludes TestMain's one-time go build): %s", time.Since(bodyStart))
}
