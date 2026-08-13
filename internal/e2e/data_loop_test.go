// Spec 05a (contract data exchange loop) §6.3's two-party proof, driven
// through hostRig (host_loop_test.go) — the REAL built binary against a
// real testkit/fakegithub.Server, so a "second party genuinely observing a
// merged write" is a real clone reading a real merge, not two in-process
// FakeHost calls.
//
// Two participants: axon (the requester/consumer — publishes the contract,
// opens the work_request, verifies and closes) and beta (the producer —
// acknowledges the request, packs and delivers). The eight §6.3 scenarios
// are covered by the seven Go tests below (the loop test also carries the
// L-5 inbox-across-the-loop proof, since both are the same timeline).
//
// ALL SEVEN TESTS BELOW PASS. This header said three of them FAIL, on a
// core path, and that has not been true since 2026-08-04 — the same commit
// (9f02f261) that ADDED this file also fixed the headline defect it
// describes, and the paragraph was never rewritten. Corrected 2026-08-13
// after watching the whole set green: 7/7, 78 s, `go test ./internal/e2e/
// -run TestDataLoop -count=1 -v`.
//
// It is worth naming the failure mode rather than just deleting the
// sentence. A stale "this is broken" note is more expensive than a stale
// "this is done" one: it was read, this week, as evidence that P5a's core
// verify path does not work, and a phase that is nearly finished looked
// blocked. Prose does not have to change to become false.
//
// The five product defects below were found by this proof and are kept as
// the record of what it caught. Their CURRENT state is stated per item;
// none is still open on a core path.
//
//  5. FIXED 2026-08-04 by this finding's own commit (9f02f261), verified
//     again 2026-08-13 by watching all three named tests pass.
//     `internal/space`'s splitDataContractReference now cuts on "#" FIRST
//     and CHECKS the digest against the resolved snapshot instead of
//     discarding it (data_resolve.go, whose own comment records the same
//     history). The description below is the DEFECT AS FOUND, kept because
//     it is the clearest statement of what the two-party proof caught.
//
//     AS FOUND: `a2a data verify` cannot resolve the contract of ANY
//     package `a2a data pack` produced. dataCore.pack (cmd/a2a/data_wiring.go)
//     sets the manifest's `contract` field from ResolveDataContractSchemas'
//     own pinnedRef return value — `<XC-id>@<version>#<digest>`
//     (data_resolve.go's own %s@%s#%s format, matching data-package/v1
//     §T2.1's own field definition). dataCore.verify then reads that same
//     field straight back (`document.Contract`) and passes it into the
//     SAME resolver (`c.resolveContractSchemas`), whose
//     splitDataContractReference (data_resolve.go) does
//     `strings.Cut(ref, "@")` and rejects anything after the version as
//     non-canonical — so the digest suffix pack itself wrote makes verify
//     refuse every time: "contract reference must be an exact XC id and
//     canonical version: \"XC-axon-export@1.0.0#sha256:...\"", observed
//     verbatim running TestDataLoopVerifyFailingPayloadNamesEntryAndRule.
//     No other caller of ResolveDataContractSchemas stripped the digest
//     first. It blocked scenarios 5, 6 and 7 —
//     TestDataLoopVerifyFailingPayloadNamesEntryAndRule,
//     TestDataLoopFailSupersedePassCloseInboxNeverAccumulates,
//     TestDataLoopStaleContractDoesNotChangeVerdict — until the fix above.
//     All three pass today.
//
//  1. FIXED — cmd/a2a/data_wiring.go now sets
//     `Provenance: datapackage.Provenance{OriginSystem: c.ownSystem}`.
//     AS FOUND: `cmd/a2a`'s dataCore.pack (data_wiring.go) never populates
//     datapackage.Document.Provenance. Every manifest a real `a2a data
//     pack` produces therefore carries `provenance.origin_system: ""`,
//     which violates data-package/v1's own `minLength: 1`
//     ($defs.provenance, data-package.schema.json) — and `a2a data
//     deliver`'s own schema check (readStagedPackage) refuses EVERY
//     delivery packed by the real CLI as a result. dataLoopFixProvenance
//     below compensates inside this test's own staging root (never
//     touching cmd/a2a or internal/datapackage) so `pack`/`deliver`/`fetch`
//     could be exercised and proven despite it; see its own doc comment.
//
//  3. FIXED at the resolver — dataContractSchemaEntry now classifies a
//     non-contract-set-v2 profile by path prefix ("schema/"), the same
//     rule internal/contract's own conformance core already used for a
//     legacy carried set. The HARNESS-side half below (whether a real
//     `a2a contract publish` reaches contract-set-v2 through this rig)
//     remains UNVERIFIED and is still the reason this file seeds by
//     direct commit.
//     AS FOUND: `internal/space`'s ResolveDataContractSchemas
//     (data_resolve.go) filters a resolved contract's carried set by
//     `entry.Role == contract.RoleSchema`. For the "contract-tree-v1"
//     (legacy) digest profile — the shape EVERY existing fixture and
//     helper in this tree produces, including host_loop_test.go's own
//     stageContract — internal/contract's own buildLegacySet
//     (internal/contract/set.go) never populates Entry.Role at all
//     (`Entry{Path: path}` literals); only the "contract-set-v2" declared
//     profile's buildDeclaredSet copies Role from the descriptor's own
//     `artifacts:` list. The data loop therefore requires a
//     contract-set-v2 contract, which this harness cannot author through
//     the real `a2a contract publish` at all (see finding below) — that
//     is the frame this file's own sibling, data_adversarial_test.go,
//     already reached independently, and this file agrees with it.
//     `contract check --suite` still classifies a legacy carried set
//     correctly, by re-deriving role from the path prefix
//     (internal/contract/conformance.go's "mapping_source":"legacy" path)
//     rather than trusting Entry.Role — so this gap is specific to the
//     data loop's own resolver, not universal.
//     Compounding: internal/contract's ContractPublicationFloor is
//     "0.19.0", so a real `a2a contract publish` can only reach the
//     contract-set-v2 profile when the space's own min_binary_version is
//     at or above it — but raising that floor also tripped CC-085 ("local
//     binary version older than space.yaml min_binary_version") against
//     internal/e2e's own exec binary AS PINNED WHEN THIS WAS WRITTEN
//     ("0.1.0", main_test.go's ldflags, off this brief's allowlist too),
//     refusing every OTHER write in the rig. There was no floor value that
//     satisfied both constraints in this harness AT THAT PIN — verified
//     empirically here, exactly as the sibling records having verified it
//     independently.
//
//     PARTIALLY STALE AS OF epic-backlog.md B33 (wave 37a): main_test.go
//     now stamps the exec'd binary to contract.ContractPublicationFloor
//     itself (main_test.go's harnessStampedVersion,
//     data_adversarial_test.go's own matching note). That resolves ONLY
//     the BINARY-SIDE half above — a hostRig raised to exactly that floor
//     no longer trips CC-085. It does NOT establish that a real `a2a
//     contract publish` now reaches contract-set-v2 through this harness:
//     the funnel's own further gates at the floor (a real V2 submit
//     validator, POL-009's schema/fixture baseline, the declared profile's
//     `artifacts:` requirement — data_adversarial_test.go's own matching
//     note lists them) were never driven through the binary either.
//     Whether publish itself now succeeds end to end is UNVERIFIED. This
//     file still seeds by direct commit regardless, for the independent
//     §6.4-framing reason the sibling file states.
//     publishDataLoopContract below (this file's own generalization, by
//     version, of the sibling's fixed-1.0.0 helper — scenario 7/L-2 needs
//     a second version mid-loop) seeds a real, resolvable contract-set-v2
//     publication as a direct commit, digest computed through
//     contract.BuildCarriedSet (the SAME pure builder `contract publish`
//     itself calls, never hand-computed) — the same arrangement technique
//     helpers_test.go's own fixOriginManifest/seedOriginExtras already use
//     in this package.
//
//  4. RESOLVED, and left here because the shape of the finding is worth
//     more than its absence: `testkit/fakegithub`'s handleFindPR once
//     omitted `body` from its listing, which real GitHub's
//     `GET /pulls?head=...` includes. `internal/space`'s WriteFunnel
//     OperationKey retry short-circuit (submitPreparedRequest, funnel.go)
//     reads the operation key back out of exactly that field
//     (`ParseOperationMetadata(existing.Body)`) to confirm a found branch
//     is the SAME operation before repairing its PR — so with body always
//     "", every OperationKey-based write refused ("operation branch
//     metadata does not match the requested intent") instead of repairing,
//     and TestDataLoopDeliverRerunIsIdempotent asserted that refusal as
//     though it were the specification. The fake now returns `body`, and
//     that test asserts the repair. The lesson it cost: a gap in a test
//     double does not read as a gap — it reads as a product behaviour, and
//     the test written against it makes the wrong behaviour permanent.
//
//  2. `cmd/a2a`'s dataCore.verify never computes or sets
//     ContractSupersededBy on its datapackage.VerifyRequest, so
//     `Observed.ContractSupersededBy` would never be populated even if
//     finding 5 above did not block every real verify first. The pure
//     core supports it (internal/datapackage/verify.go); only the wiring
//     never calls into it.
//
// --- The five inbox steps (L-5, AC-12) --------------------------------
//
// Derived from internal/cache/inbox.go's actionableReasons, not asserted
// as a slogan: axon's own actionable set at five points across the fail →
// supersede → pass → close loop.
//
//  1. delivered (attempt 1 submitted, before ack)   -> {handoff1}     (condition 1: addressed-no-ack)
//  2. acknowledged (attempt 1 acked)                -> {}             (condition 1 no longer matches the state; condition 4 needs p1/blocking, absent)
//  3. rejected (attempt 1 verify-fail recorded)      -> {}             (condition 4 excludes Rejected from the handoff's own open-state set — L-5's "leaves the union")
//  4. superseded (attempt 2 submitted, before ack)   -> {handoff2}     (condition 1 again — proves NON-accumulation: handoff1 does not reappear)
//  5. responded (beta responds, before axon closes)  -> {work_request} (condition 2: responded-awaiting-verify-close, gated to question/work_request)
//
// Steps 2 and 3 are the two legitimately-empty steps T2.5 L-5 names
// ("acknowledged, rejected"); the other three are non-empty, and step 4's
// exact-one-item set is what proves the union never grows to N across
// attempts.
package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// --- shared fixture -------------------------------------------------------

// setupDataLoop builds the two-party fixture every scenario below starts
// from: axon publishes XC-axon-export@1.0.0 and opens a data work_request
// addressed to beta; both systems are synced onto it.
func setupDataLoop(t *testing.T) (axon, beta *hostRig, contractRef, wrID string) {
	t.Helper()
	axon = newHostRig(t, "axon", "axon", "beta")
	beta = axon.peer("beta")

	// See this file's own package doc comment, finding 3: a real `a2a
	// contract publish` cannot reach the contract-set-v2 profile `a2a
	// data pack`/`verify` needs in this harness at all, so the base
	// XC-axon-export@1.0.0 publication is seeded as a direct commit —
	// this file's own generalization (by version) of the sibling
	// data_adversarial_test.go's own publishDataAdversarialContract.
	contractID := publishDataLoopContract(t, axon, "export", "1.0.0", "01K1A2B3C4D5E6F7G8H9J0KAQ1")

	wrID = "XW-axon-20260804-w001"
	wrPath := axon.stageDataLoopWorkRequest(wrID, "beta")
	axon.mustRun("submit", wrPath)

	axon.mustRun("sync")
	beta.mustRun("sync")

	return axon, beta, contractID + "@1.0.0", wrID
}

// publishDataLoopContract lands ONE real, resolvable contract-set-v2
// publication straight onto r's fixture origin as a direct commit —
// this file's own generalization, by version, of the sibling
// data_adversarial_test.go's own publishDataAdversarialContract (which
// is fixed at "1.0.0" and cannot serve scenario 7/L-2's second, later
// version). See this file's own package doc comment, finding 3, for why
// a direct commit is necessary rather than a decoration, and why the
// digest is computed through contract.BuildCarriedSet — the same pure
// builder `contract publish` itself calls — rather than hand-computed.
func publishDataLoopContract(t *testing.T, r *hostRig, slug, version, eventID string) (id string) {
	t.Helper()
	id = "XC-" + r.system + "-" + slug
	schemaPath := "schema/" + slug + ".schema.json"
	validPath := "fixtures/valid/" + slug + ".json"
	invalidPath := "fixtures/invalid/" + slug + ".json"

	descriptorRaw := []byte("---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: the export contract under test\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-04T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"" + version + "\"\n" +
		"schema_format: json-schema-2020-12\n" +
		"compat_policy: default\n" +
		"artifacts:\n" +
		"  - path: " + schemaPath + "\n" +
		"    role: schema\n" +
		"    normative: true\n" +
		"    media_type: application/schema+json\n" +
		"  - path: " + validPath + "\n" +
		"    role: valid-fixture\n" +
		"    normative: true\n" +
		"    media_type: application/json\n" +
		"    conforms_to: " + schemaPath + "\n" +
		"  - path: " + invalidPath + "\n" +
		"    role: invalid-fixture\n" +
		"    normative: true\n" +
		"    media_type: application/json\n" +
		"    conforms_to: " + schemaPath + "\n" +
		"---\n# Export\n\nWhat this contract covers.\n")

	files := map[string]string{
		schemaPath:  dataAdversarialSchema + "\n",
		validPath:   `{"example":"replace-me"}` + "\n",
		invalidPath: "null\n",
	}

	descriptor, err := contract.ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatalf("parse test descriptor: %v", err)
	}
	candidates := make([]contract.CandidateFile, 0, len(files))
	for path, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, descriptorRaw, descriptor, candidates)
	if len(issues) != 0 {
		t.Fatalf("build test carried set: %+v", issues)
	}

	eventRaw := "schema: event/v2\n" +
		"event: " + eventID + "\n" +
		"space: " + r.spaceID + "\n" +
		"subject: " + id + "\n" +
		"transition: publish\n" +
		"actor: {kind: agent, name: publisher, system: " + r.system + "}\n" +
		"at: 2026-08-04T11:00:00Z\n" +
		"version: \"" + version + "\"\n" +
		"digest: " + set.AggregateDigest + "\n" +
		"digest_profile: contract-set-v2\n" +
		"publication:\n" +
		"  intent_key: op-v1-" + strings.Repeat("d", 64) + "\n" +
		"  candidate_intent_digest: sha256:" + strings.Repeat("a", 64) + "\n" +
		"  version_selector: explicit:" + version + "\n" +
		"  operation_key: op-v1-" + strings.Repeat("c", 64) + "\n"

	dir := t.TempDir()
	gitRun(t, "", "clone", r.fx.RemoteURL(), dir)
	root := r.system + "/provides/" + slug
	writeMirrorFile(t, dir, root+"/contract.md", string(descriptorRaw))
	for path, raw := range files {
		writeMirrorFile(t, dir, root+"/"+path, raw)
	}
	writeMirrorFile(t, dir, r.system+"/events/2026/"+eventID+".yaml", eventRaw)
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-m", "publish: "+id+"@"+version+" (test fixture, direct commit — see this file's own package doc comment, finding 3)")
	gitRun(t, dir, "push", "origin", "main")
	return id
}

// stageDataLoopWorkRequest writes a schema-valid work_request into
// staging, mirroring stageQuestion/stageContract's own shape
// (host_loop_test.go): category "data" (work_request.schema.json's own
// enum). DELIBERATELY non-blocking p3 (with the interim_behavior the
// schema requires when blocking is explicitly false) — unlike the
// sibling data_adversarial_test.go's own stageWorkRequest (blocking:
// true), this file's own L-5 inbox-step derivation (package doc comment)
// depends on the work_request NEVER firing actionableReasons' condition 4
// (p1-or-blocking) on its own, so every non-empty step below is
// attributable to exactly the reason this file names for it. A second,
// differently-shaped helper — not a reuse of the sibling's — because the
// two tests need different fixture shapes for different, both correct,
// reasons.
func (r *hostRig) stageDataLoopWorkRequest(id, to string) (path string) {
	r.t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: work_request\n" +
		"title: deliver export data\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [" + to + "]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-04T10:00:00Z\n" +
		"category: data\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"interim_behavior: " + to + " may continue other work until this is answered\n" +
		"classification: internal\n" +
		"acceptance_criteria: [\"delivered payload verifies against the pinned contract\"]\n" +
		"---\n" +
		"Please deliver export data conforming to the pinned contract.\n"
	path = filepath.Join(r.projectDir, ".a2a", "staging", id+".md")
	mustWrite(r.t, path, content)
	return path
}

// --- data verb wrappers -----------------------------------------------

// dataLoopPack runs `a2a data pack` and decodes its --json result,
// returning the staged package's own staging root (data_wiring.go's
// writeStagedPackage convention: <projectRoot>/.a2a/staging/data/<id>/).
// Always passes --expires (Pack's own zero-value default expires
// IMMEDIATELY: ExpiresAt = now.Add(0)), and compensates for the
// Provenance wiring gap this file's own doc comment reports — see
// dataLoopFixProvenance.
func (r *hostRig) dataLoopPack(t *testing.T, contractRef, from, fulfills, supersedes string) (cli.DataResult, string) {
	t.Helper()
	args := []string{
		"data", "pack",
		"--contract", contractRef, "--from", from,
		"--profile", "synthetic", "--format", "json", "--expires", "24h",
		"--fulfills", fulfills, "--json",
	}
	if supersedes != "" {
		args = append(args, "--supersedes", supersedes)
	}
	out := r.mustRun(args...)
	var result cli.DataResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode data pack result: %v\nstdout=%s", err, out)
	}
	if result.Manifest == nil {
		t.Fatalf("data pack result carries no manifest: %s", out)
	}
	stagingRoot := filepath.Join(r.projectDir, ".a2a", "staging", "data", result.Manifest.ID)
	dataLoopFixProvenance(t, stagingRoot, r.system)
	return result, stagingRoot
}

// dataLoopDeliver runs `a2a data deliver` and decodes its --json result.
func (r *hostRig) dataLoopDeliver(t *testing.T, stagingRoot, fulfills string) cli.DataResult {
	t.Helper()
	out := r.mustRun("data", "deliver", stagingRoot, "--fulfills", fulfills, "--json")
	var result cli.DataResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode data deliver result: %v\nstdout=%s", err, out)
	}
	return result
}

// --- manifest patching (this test's own staging root only; never a
// product file) ---------------------------------------------------------

// dataLoopPatchManifest reads stagingRoot/manifest.json, applies mutate to
// its decoded generic form, and writes it back — the same file `a2a data
// deliver` will read moments later. Used for two things this suite needs
// and neither the CLI nor internal/datapackage's own test helpers expose:
// compensating for the Provenance wiring gap, and seeding a
// digest-untouched record_count disagreement as the fail leg of scenarios
// 5 and 6 (see dataLoopCorruptRecordCount).
func dataLoopPatchManifest(t *testing.T, stagingRoot string, mutate func(doc map[string]any)) {
	t.Helper()
	manifestPath := filepath.Join(stagingRoot, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read staged manifest %s: %v", manifestPath, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode staged manifest %s: %v", manifestPath, err)
	}
	mutate(doc)
	patched, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encode patched manifest %s: %v", manifestPath, err)
	}
	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		t.Fatalf("write patched manifest %s: %v", manifestPath, err)
	}
}

// dataLoopFixProvenance compensates, entirely inside this test's own
// staging root, for the Provenance wiring gap this file's own package doc
// comment reports (finding 1): without it, `a2a data deliver`'s own
// data-package/v1 schema check refuses every package the real `a2a data
// pack` produces. Patching it here — never in cmd/a2a or
// internal/datapackage, neither of which this brief grants — is what lets
// the REST of the loop (the one-commit property, idempotent retry, digest
// verification, conformance, the derived verdict direction, the inbox
// invariant, the close) actually run and be proven.
func dataLoopFixProvenance(t *testing.T, stagingRoot, originSystem string) {
	dataLoopPatchManifest(t, stagingRoot, func(doc map[string]any) {
		doc["provenance"] = map[string]any{
			"origin_system": originSystem,
			"extracted_at":  "2026-08-04T10:00:00Z",
		}
	})
}

// dataLoopCorruptRecordCount declares a record_count that disagrees with
// entryPath's actual byte content, by delta — without touching the
// entry's own digest (record_count is manifest metadata; aggregate_digest
// covers path->digest pairs only), so `a2a data deliver`'s digest/shape
// checks still accept the package, and only `a2a data verify`'s
// consumer-side recount (internal/datapackage/verify.go) can catch the
// disagreement — exactly spec 05a §T2.1's "observed" row: "a disagreement
// between the two is recorded as a finding in checks[]".
func dataLoopCorruptRecordCount(t *testing.T, stagingRoot, entryPath string, delta int64) {
	dataLoopPatchManifest(t, stagingRoot, func(doc map[string]any) {
		entries, ok := doc["entries"].([]any)
		if !ok {
			t.Fatalf("staged manifest has no entries[] array")
		}
		found := false
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok || entry["path"] != entryPath {
				continue
			}
			current, ok := entry["record_count"].(float64)
			if !ok {
				t.Fatalf("entry %q has no numeric record_count to corrupt", entryPath)
			}
			entry["record_count"] = current + float64(delta)
			found = true
		}
		if !found {
			t.Fatalf("staged manifest has no entry %q to corrupt", entryPath)
		}
	})
}

// --- read-surface assertions --------------------------------------------

// inboxItemProbe is a2a inbox --json's guaranteed per-item shape
// (internal/cache.Item), narrowed to what these assertions need.
type inboxItemProbe struct {
	ID      string   `json:"id"`
	Reasons []string `json:"reasons,omitempty"`
}

// actionable syncs r and returns its own `a2a inbox --actionable --json`.
func (r *hostRig) actionable(t *testing.T) []inboxItemProbe {
	t.Helper()
	r.mustRun("sync")
	out := r.mustRun("inbox", "--actionable", "--json")
	var items []inboxItemProbe
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("decode inbox --actionable: %v\nstdout=%s", err, out)
	}
	return items
}

// assertActionable asserts r's actionable inbox is EXACTLY want at step —
// not a bound, not "at least" — per this file's own L-5 derivation.
func assertActionable(t *testing.T, r *hostRig, step string, want ...string) {
	t.Helper()
	items := r.actionable(t)
	got := make([]string, len(items))
	for i, it := range items {
		got[i] = it.ID
	}
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Fatalf("actionable(%s) at step %q = %v, want exactly %v (full items: %+v)", r.system, step, got, wantSorted, items)
	}
}

// assertState asserts `a2a show <id>` (after a sync) reports wantState.
func (r *hostRig) assertState(t *testing.T, id, wantState string) {
	t.Helper()
	r.mustRun("sync")
	out := r.mustRun("show", id)
	marker := "(" + wantState + ")"
	if !strings.Contains(out, marker) {
		t.Fatalf("show %s = %q, want state %q", id, out, wantState)
	}
}

// gitCommitCount is the origin's own `git rev-list --count main` — used to
// assert an EXACT commit delta, not merely "a commit landed".
func gitCommitCount(t *testing.T, remote string) int {
	t.Helper()
	out := strings.TrimSpace(gitOutput(t, remote, "rev-list", "--count", "main"))
	n, err := strconv.Atoi(out)
	if err != nil {
		t.Fatalf("parse commit count %q: %v", out, err)
	}
	return n
}

// assertCheckFails asserts result's report carries a failing check on
// entryPath whose id is prefixed rulePrefix — AC-5's "names the exact
// failing file and rule".
func assertCheckFails(t *testing.T, result cli.DataResult, entryPath, rulePrefix string) {
	t.Helper()
	if result.Report == nil {
		t.Fatalf("result carries no report")
	}
	for _, c := range result.Report.Checks {
		if c.Path == entryPath && c.Status == "fail" && strings.HasPrefix(c.ID, rulePrefix) {
			return
		}
	}
	t.Fatalf("report does not name a %q failure on %q: checks=%+v", rulePrefix, entryPath, result.Report.Checks)
}

// --- scenario 1: pack -> deliver, one commit ----------------------------

// TestDataLoopPackDeliverOneCommit proves spec 05a §6.3's first row:
// payload, manifest and handoff land in ONE commit; the handoff references
// the package; the work_request stays open.
func TestDataLoopPackDeliverOneCommit(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	from := filepath.Join(beta.projectDir, "src")
	mustMkdirAll(t, from)
	mustWrite(t, filepath.Join(from, "payload.json"), `{"example":"attempt-1"}`+"\n")

	packed, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")
	pkgID := packed.Manifest.ID

	beforeCommits := gitCommitCount(t, axon.fx.RemoteURL())
	beforePRs := len(beta.gh.PRs())

	delivered := beta.dataLoopDeliver(t, stagingRoot, wrID)
	if delivered.HandoffID == "" {
		t.Fatalf("deliver result carries no handoff_id: %+v", delivered)
	}
	handoffID := delivered.HandoffID

	if got := gitCommitCount(t, axon.fx.RemoteURL()); got != beforeCommits+1 {
		t.Fatalf("deliver landed %d commits, want exactly 1 (before=%d after=%d)", got-beforeCommits, beforeCommits, got)
	}
	if got := len(beta.gh.PRs()); got != beforePRs+1 {
		t.Fatalf("deliver opened %d PRs, want exactly 1 new one (before=%d after=%d)", got-beforePRs, beforePRs, got)
	}

	changed := gitOutput(t, axon.fx.RemoteURL(), "show", "--name-only", "--pretty=format:", "main")
	for _, want := range []string{pkgID + "/manifest.json", pkgID + "/payload.json", handoffID + ".md"} {
		if !strings.Contains(changed, want) {
			t.Errorf("the one-commit diff does not contain %q:\n%s", want, changed)
		}
	}

	handoffRaw := gitOutput(t, axon.fx.RemoteURL(), "show", "main:beta/exchanges/"+handoffID+".md")
	if !strings.Contains(handoffRaw, pkgID) {
		t.Errorf("handoff %s does not reference package %s:\n%s", handoffID, pkgID, handoffRaw)
	}

	axon.assertState(t, wrID, "submitted")
}

// --- scenario 2: re-run deliver is idempotent ---------------------------

// TestDataLoopDeliverRerunIsIdempotent targets spec 05a §6.3's second row:
// a re-run of `a2a data deliver` against the same staging root repairs its
// own pull request — no second PR, no second commit.
//
// Both halves of D-6's property are verified below: the two calls target
// the identical deterministic branch, AND the second one repairs that
// branch's pull request instead of refusing.
//
// The second half was unreachable for a while, and how it came back is the
// part worth remembering. `internal/space.WriteFunnel`'s OperationKey retry
// short-circuit (submitPreparedRequest, funnel.go) finds the existing PR via
// `FindPRByHeadBranch`, then reads the operation key it recorded back out of
// that PR's own BODY (`ParseOperationMetadata(existing.Body)`) to prove the
// found branch is really the same operation and not a name collision. Real
// GitHub returns `body` from that listing; testkit/fakegithub did not, so
// `existing.Body` was always "" and the funnel always refused with
// ErrOperationMismatch. An earlier revision of this test asserted that
// refusal — a gap in the double, written down as though it were the product.
func TestDataLoopDeliverRerunIsIdempotent(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	from := filepath.Join(beta.projectDir, "src")
	mustMkdirAll(t, from)
	mustWrite(t, filepath.Join(from, "payload.json"), `{"example":"attempt-1"}`+"\n")
	_, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")

	first := beta.dataLoopDeliver(t, stagingRoot, wrID)
	if first.Write == nil || first.Write.Branch == "" {
		t.Fatalf("first deliver carries no write/branch result: %+v", first)
	}
	commitsAfterFirst := gitCommitCount(t, axon.fx.RemoteURL())
	prsAfterFirst := len(beta.gh.PRs())

	// AC-3's second half. This assertion only became reachable once
	// testkit/fakegithub's PR listing started returning `body`: the write
	// funnel reads the operation key back out of exactly that field to
	// confirm a found branch belongs to the same operation before repairing
	// its pull request, so with body omitted every keyed retry refused and
	// the repair path could not be proven locally at all. An earlier
	// revision of this test asserted that refusal as though it were the
	// specification.
	stdout, stderr, code := beta.run("data", "deliver", stagingRoot, "--fulfills", wrID, "--json")
	if code != 0 {
		t.Fatalf("re-run deliver = exit %d, want 0 (it must repair its own pull request)\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	if got := gitCommitCount(t, axon.fx.RemoteURL()); got != commitsAfterFirst {
		t.Fatalf("re-run deliver landed a second commit: before=%d after=%d", commitsAfterFirst, got)
	}
	if got := len(beta.gh.PRs()); got != prsAfterFirst {
		t.Fatalf("re-run deliver opened a second pull request: before=%d after=%d", prsAfterFirst, got)
	}
}

// --- scenario 3: fetch into a clean directory ---------------------------

// TestDataLoopFetchIntoCleanDirectory proves spec 05a §6.3's third row:
// digests are verified before the first byte is written. It also proves
// D-5's own extension of the sibling's behaviour: a byte-identical
// re-fetch is accepted, not refused.
func TestDataLoopFetchIntoCleanDirectory(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	from := filepath.Join(beta.projectDir, "src")
	mustMkdirAll(t, from)
	payload := `{"example":"fetchable"}` + "\n"
	mustWrite(t, filepath.Join(from, "payload.json"), payload)
	packed, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")
	pkgID := packed.Manifest.ID
	beta.dataLoopDeliver(t, stagingRoot, wrID)

	axon.mustRun("sync")
	dest := filepath.Join(axon.projectDir, "fetched")
	stdout, stderr, code := axon.run("data", "fetch", pkgID, "--to", dest, "--json")
	if code != 0 {
		t.Fatalf("fetch into a clean directory: exit %d\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var fetched cli.DataResult
	if err := json.Unmarshal([]byte(stdout), &fetched); err != nil {
		t.Fatalf("decode fetch result: %v\nstdout=%s", err, stdout)
	}
	if fetched.Outcome != "installed" {
		t.Fatalf("fetch outcome = %q, want %q", fetched.Outcome, "installed")
	}
	got, err := os.ReadFile(filepath.Join(dest, "payload.json"))
	if err != nil {
		t.Fatalf("read fetched payload: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("fetched payload = %q, want %q", got, payload)
	}

	// D-5: a byte-identical re-fetch into the same destination is a
	// success (AlreadyPresent), not a collision.
	stdout2, stderr2, code2 := axon.run("data", "fetch", pkgID, "--to", dest, "--json")
	if code2 != 0 {
		t.Fatalf("re-fetch into a byte-identical destination: exit %d\nstdout=%s\nstderr=%s", code2, stdout2, stderr2)
	}
	var again cli.DataResult
	if err := json.Unmarshal([]byte(stdout2), &again); err != nil {
		t.Fatalf("decode re-fetch result: %v\nstdout=%s", err, stdout2)
	}
	if again.Outcome != "already-present" {
		t.Fatalf("re-fetch outcome = %q, want %q (D-5)", again.Outcome, "already-present")
	}
}

// --- scenario 4: fetch into a divergent directory -----------------------

// TestDataLoopFetchIntoDivergentDirectoryRefused proves spec 05a §6.3's
// fourth row: a fetch into a destination that already diverges is refused,
// and the destination is left unchanged.
func TestDataLoopFetchIntoDivergentDirectoryRefused(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	from := filepath.Join(beta.projectDir, "src")
	mustMkdirAll(t, from)
	mustWrite(t, filepath.Join(from, "payload.json"), `{"example":"fetchable"}`+"\n")
	packed, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")
	pkgID := packed.Manifest.ID
	beta.dataLoopDeliver(t, stagingRoot, wrID)

	axon.mustRun("sync")
	dest := filepath.Join(axon.projectDir, "divergent")
	mustMkdirAll(t, dest)
	divergentContent := "not the real payload\n"
	mustWrite(t, filepath.Join(dest, "payload.json"), divergentContent)

	stdout, stderr, code := axon.run("data", "fetch", pkgID, "--to", dest, "--json")
	if code == 0 {
		t.Fatalf("fetch into a divergent destination succeeded: stdout=%s stderr=%s", stdout, stderr)
	}

	got, err := os.ReadFile(filepath.Join(dest, "payload.json"))
	if err != nil {
		t.Fatalf("read destination after the refusal: %v", err)
	}
	if string(got) != divergentContent {
		t.Fatalf("destination was mutated despite the refusal: got %q, want unchanged %q", got, divergentContent)
	}
}

// --- scenario 5: verify on a failing payload -----------------------------

// TestDataLoopVerifyFailingPayloadNamesEntryAndRule proves spec 05a §6.3's
// fifth row: the report names the failing entry and rule, and --record
// drives verify-fail.
func TestDataLoopVerifyFailingPayloadNamesEntryAndRule(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	from := filepath.Join(beta.projectDir, "src")
	mustMkdirAll(t, from)
	mustWrite(t, filepath.Join(from, "payload.json"), `{"example":"attempt-1"}`+"\n")
	packed, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")
	pkgID := packed.Manifest.ID
	dataLoopCorruptRecordCount(t, stagingRoot, "payload.json", 1)
	delivered := beta.dataLoopDeliver(t, stagingRoot, wrID)
	handoffID := delivered.HandoffID

	axon.mustRun("sync")

	// Judge-only: no write, no network beyond the resolves verify itself
	// needs.
	stdout, stderr, code := axon.run("data", "verify", pkgID, "--json")
	if code != 1 {
		t.Fatalf("judge-only verify on a failing payload: exit %d, want 1\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var judged cli.DataResult
	if err := json.Unmarshal([]byte(stdout), &judged); err != nil {
		t.Fatalf("decode judge-only verify result: %v\nstdout=%s", err, stdout)
	}
	if judged.Write != nil {
		t.Fatalf("judge-only verify performed a write: %+v", judged.Write)
	}
	if judged.Report == nil || judged.Report.Result() != "fail" {
		t.Fatalf("judge-only report = %+v, want result=fail", judged.Report)
	}
	assertCheckFails(t, judged, "payload.json", "record-count:")

	// --record: the handoff must be acknowledged first (fold table: both
	// verify-pass and verify-fail only fire from `acknowledged`).
	axon.mustRun("ack", handoffID)

	beforePRs := len(axon.gh.PRs())
	stdout2, stderr2, code2 := axon.run("data", "verify", pkgID, "--record", "--json")
	if code2 != 1 {
		t.Fatalf("verify --record on a failing payload: exit %d, want 1\nstdout=%s\nstderr=%s", code2, stdout2, stderr2)
	}
	var recorded cli.DataResult
	if err := json.Unmarshal([]byte(stdout2), &recorded); err != nil {
		t.Fatalf("decode verify --record result: %v\nstdout=%s", err, stdout2)
	}
	if recorded.Write == nil {
		t.Fatalf("verify --record performed no write")
	}
	if got := len(axon.gh.PRs()); got != beforePRs+1 {
		t.Fatalf("verify --record PR count = %d, want %d", got, beforePRs+1)
	}
	axon.assertState(t, handoffID, "rejected")

	// The report's own idempotency (dataReportEntropy keys the report id
	// on package+contract, D-12): a re-run repairs the SAME operation.
	stdout3, stderr3, code3 := axon.run("data", "verify", pkgID, "--record", "--json")
	if code3 != 1 {
		t.Fatalf("re-run verify --record: exit %d, want 1\nstdout=%s\nstderr=%s", code3, stdout3, stderr3)
	}
	if got := len(axon.gh.PRs()); got != beforePRs+1 {
		t.Fatalf("re-run verify --record opened a second PR: %d, want %d", got, beforePRs+1)
	}
}

// --- scenario 6 + inbox (L-5): fail -> supersede -> pass -> close --------

// TestDataLoopFailSupersedePassCloseInboxNeverAccumulates is spec 05a
// §6.3's own decisive scenario: the whole loop, on one thread, ending with
// the ORIGINAL work_request reaching a closed state — not merely a
// second handoff reaching verify-pass. It carries the L-5 inbox proof
// inline (this file's own package doc comment derives the five steps),
// because both are the exact same timeline.
func TestDataLoopFailSupersedePassCloseInboxNeverAccumulates(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	// Baseline: axon authored wrID but is not its own target
	// (actionableReasons' condition 1 keys on `to`), so nothing here is
	// actionable for axon yet.
	assertActionable(t, axon, "baseline")

	beta.mustRun("sync")
	beta.mustRun("ack", wrID)

	// --- attempt 1: fails --------------------------------------------
	from1 := filepath.Join(beta.projectDir, "attempt1")
	mustMkdirAll(t, from1)
	mustWrite(t, filepath.Join(from1, "payload.json"), `{"example":"attempt-1"}`+"\n")
	pack1, staging1 := beta.dataLoopPack(t, contractRef, from1, wrID, "")
	pkg1 := pack1.Manifest.ID
	dataLoopCorruptRecordCount(t, staging1, "payload.json", 1)
	deliver1 := beta.dataLoopDeliver(t, staging1, wrID)
	handoff1 := deliver1.HandoffID

	// step 1: delivered, before ack.
	assertActionable(t, axon, "delivered (attempt 1)", handoff1)

	axon.mustRun("ack", handoff1)
	// step 2: acknowledged — the first of L-5's two named empty steps.
	assertActionable(t, axon, "acknowledged (attempt 1)")

	verify1Out, verify1Err, verify1Code := axon.run("data", "verify", pkg1, "--record", "--json")
	if verify1Code != 1 {
		t.Fatalf("verify --record attempt 1: exit %d, want 1 (a failing verdict)\nstdout=%s\nstderr=%s", verify1Code, verify1Out, verify1Err)
	}
	var verify1Result cli.DataResult
	if err := json.Unmarshal([]byte(verify1Out), &verify1Result); err != nil {
		t.Fatalf("decode verify --record attempt 1: %v\nstdout=%s", err, verify1Out)
	}
	if verify1Result.Report == nil || verify1Result.Report.Result() != "fail" {
		t.Fatalf("attempt 1 verdict = %+v, want fail", verify1Result.Report)
	}
	axon.assertState(t, handoff1, "rejected")
	// step 3: rejected — the second of L-5's two named empty steps
	// (condition 4 excludes Rejected from the handoff's own open states —
	// "a rejected handoff leaves the union").
	assertActionable(t, axon, "rejected (attempt 1)")

	// --- attempt 2: supersedes, passes ---------------------------------
	beta.mustRun("sync")
	from2 := filepath.Join(beta.projectDir, "attempt2")
	mustMkdirAll(t, from2)
	mustWrite(t, filepath.Join(from2, "payload.json"), `{"example":"attempt-2"}`+"\n")
	pack2, staging2 := beta.dataLoopPack(t, contractRef, from2, wrID, pkg1)
	pkg2 := pack2.Manifest.ID
	if pack2.Manifest.Attempt != 2 {
		t.Fatalf("attempt 2 manifest.attempt = %d, want 2", pack2.Manifest.Attempt)
	}
	if pack2.Manifest.Supersedes == "" {
		t.Fatalf("attempt 2 manifest carries no supersedes reference")
	}
	deliver2 := beta.dataLoopDeliver(t, staging2, wrID)
	handoff2 := deliver2.HandoffID

	// step 4: superseded — NON-ACCUMULATION's own proof. Exactly the new
	// handoff; the rejected attempt 1 handoff never reappears.
	assertActionable(t, axon, "superseded (attempt 2)", handoff2)

	axon.mustRun("ack", handoff2)
	verify2Out, verify2Err, verify2Code := axon.run("data", "verify", pkg2, "--record", "--json")
	if verify2Code != 0 {
		t.Fatalf("verify --record attempt 2: exit %d, want 0 (a passing verdict)\nstdout=%s\nstderr=%s", verify2Code, verify2Out, verify2Err)
	}
	var verify2Result cli.DataResult
	if err := json.Unmarshal([]byte(verify2Out), &verify2Result); err != nil {
		t.Fatalf("decode verify --record attempt 2: %v\nstdout=%s", err, verify2Out)
	}
	if verify2Result.Report == nil || verify2Result.Report.Result() != "pass" {
		t.Fatalf("attempt 2 verdict = %+v, want pass", verify2Result.Report)
	}
	axon.assertState(t, handoff2, "accepted")

	// beta discharges its own obligation on the ORIGINAL work_request.
	// The fold table only allows `close` from `responded`; `respond`'s own
	// fromState set includes `acknowledged`, so no accept/start step is
	// required first.
	beta.mustRun("sync")
	beta.mustRun("respond", "--result", "delivered", wrID)

	// step 5: responded — condition 2 ("responded awaiting my
	// verify/close", gated to question/work_request, env.From==me) fires
	// for axon on the ORIGINAL work_request. This is the third non-empty
	// step; L-5's own doc comment names condition 2's gate as exactly why
	// this document (never the handoff) is the one that fires here.
	assertActionable(t, axon, "responded, awaiting close", wrID)

	axon.mustRun("close", wrID)
	// The decisive assertion: the ORIGINAL work_request reached a closed
	// state — not merely that the second handoff got verify-pass.
	axon.assertState(t, wrID, "closed")

	// The loop actually ends: nothing is left on axon's own actionable
	// inbox once the request is closed.
	assertActionable(t, axon, "closed")
}

// --- scenario 7: stale contract (L-2) ------------------------------------

// TestDataLoopStaleContractDoesNotChangeVerdict proves spec 05a §6.3's
// eighth row: publishing a newer contract version mid-loop does not
// change the verdict — verify always uses the version PINNED in the
// manifest, never latest. It also documents (finding 2, this file's own
// package doc comment) that the "recorded observation" half of L-2 is not
// wired: dataCore.verify never sets ContractSupersededBy.
func TestDataLoopStaleContractDoesNotChangeVerdict(t *testing.T) {
	t.Parallel()
	axon, beta, contractRef, wrID := setupDataLoop(t)

	from := filepath.Join(beta.projectDir, "src")
	mustMkdirAll(t, from)
	mustWrite(t, filepath.Join(from, "payload.json"), `{"example":"still-good"}`+"\n")
	packed, stagingRoot := beta.dataLoopPack(t, contractRef, from, wrID, "")
	pkgID := packed.Manifest.ID
	delivered := beta.dataLoopDeliver(t, stagingRoot, wrID)
	handoffID := delivered.HandoffID

	axon.mustRun("sync")
	axon.mustRun("ack", handoffID)

	// Publish a NEWER contract version mid-loop, between deliver and
	// verify — L-2's own precondition. Seeded as a direct commit for the
	// same reason setupDataLoop's own base publication is (this file's
	// own package doc comment, finding 3): a real `a2a contract publish`
	// cannot reach contract-set-v2 in this harness, and this scenario
	// needs a SECOND version, which the sibling's fixed-1.0.0 helper
	// cannot serve either.
	publishDataLoopContract(t, axon, "export", "1.1.0", "01K1A2B3C4D5E6F7G8H9J0KAQ2")
	axon.mustRun("sync")

	stdout, stderr, code := axon.run("data", "verify", pkgID, "--record", "--json")
	if code != 0 {
		t.Fatalf("verify after a mid-loop republish: exit %d, want 0 (an unaffected pass)\nstdout=%s\nstderr=%s", code, stdout, stderr)
	}
	var out cli.DataResult
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode verify result: %v\nstdout=%s", err, stdout)
	}
	if out.Report == nil || out.Report.Result() != "pass" {
		t.Fatalf("verdict after a mid-loop republish = %+v, want pass — L-2 requires the pinned version, never latest", out.Report)
	}
	// The report names the ORIGINALLY pinned version, in the pinned form the
	// manifest carries (<id>@<version>#<digest>) — not the republished one.
	if !strings.HasPrefix(out.Report.Contract, contractRef+"#") {
		t.Fatalf("report.contract = %q, want the originally pinned %q with its digest, not the republished version", out.Report.Contract, contractRef)
	}

	// L-2's other half: the agreement moved, and the consumer is told so.
	republishedRef := strings.TrimSuffix(contractRef, "@1.0.0") + "@1.1.0"
	// The verdict above is unaffected by it — that is structural, since
	// verification can only ever use the version pinned in the manifest —
	// but a consumer judging against 1.0.0 while 1.1.0 is already published
	// needs to know, and an earlier revision of the wiring never looked it
	// up, so the observation was always empty.
	if got := out.Report.Observed.ContractSupersededBy; got != republishedRef {
		t.Errorf("observed.contract_superseded_by = %q, want %q", got, republishedRef)
	}
}
