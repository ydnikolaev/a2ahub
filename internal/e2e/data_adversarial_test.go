package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/contract"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
)

// This file is spec 05a §6.4's adversarial ladder proven at the COMMAND
// level: a real EXEC of the built `a2a` binary (via hostRig, host_loop_test.go),
// never a direct construction of dataCore or internal/datapackage. Every
// case already has a unit-level receipt in internal/datapackage
// (safefs_test.go, entryset_test.go, fetch_test.go, pack_test.go,
// verify_test.go) or at the cmd/a2a wiring seam (data_wiring_test.go's
// TestReadStagedPackageRefusesAManifestNamingAPathOutsideTheRoot); this
// file's own value is proving the SAME guard fires when a caller invokes
// `a2a data pack|deliver|fetch|verify` exactly as an operator would, with
// real flags, against a real fixture space and a real fake GitHub host —
// and that a refusal at the command layer leaves BOTH the local filesystem
// and the remote space exactly as they were.
//
// Every one of §6.4's nine listed conditions is present here:
//   - a manifest naming a path outside the package root         -> B1
//   - a symlink entry in the source directory                   -> A1
//   - an entry that exceeds the per-entry byte bound             -> A2
//   - an expired package at fetch                                -> C1
//   - a manifest whose locator carries a token or an absolute path -> B2
//   - a payload that is valid JSON but not the declared format
//     (json where ndjson is declared, and the reverse)           -> A3, A4
//   - an ndjson entry whose final line is truncated mid-record   -> A5
//   - data verify against a package id that does not exist       -> C2
//   - data pack --profile production                             -> A6
//
// "a symlink swapped between check and read" (spec §6.4's own prose,
// distinct from the plain symlink-entry case above) is a TOCTOU race that
// can only be driven deterministically with the dataPackageHooks seam
// internal/datapackage's own tests use (safefs_test.go's
// TestReadDataPackageFileRefusesSymlinkSwappedBetweenLstatAndRead) — that
// seam is unexported and not reachable from an exec'd binary at all, so it
// is out of this file's reach by construction, not by omission; its
// receipt already exists at the unit level.

// dataAdversarialSchema is deliberately permissive (no "required", any
// object passes) — every refusal this file proves is about FRAMING (a
// symlink, an oversized entry, a wrong format, a bad locator, a bad path,
// a missing package, a forbidden profile), never about a dataset entry's
// own field-level conformance, so the schema itself is not what is under
// test here.
const dataAdversarialSchema = `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"example":{"type":"string"}},"additionalProperties":true}`

// publishDataAdversarialContract lands ONE real, resolvable contract-set-v2
// publication straight onto the rig's fixture origin (one commit: the
// finalized descriptor, its three required-role carried files, and its
// `event/v2` publish event — the exact shape
// internal/space/contract_history_test.go's own commitContractPublication
// produces, and exactly what `a2a data pack` needs: ResolveDataContractSchemas
// (internal/space/data_resolve.go) only ever returns carried entries whose
// Role is contract.RoleSchema, and ONLY the contract-set-v2 digest profile
// ever sets one — internal/contract/set.go's buildLegacySet (what
// host_loop_test.go's own stageContract produces through the real funnel,
// TestHostLoopContractFamily's whole point) leaves every entry's Role at its
// zero value.
//
// DEVIATION, recorded because it departs from "drive every step through
// hostRig.mustRun": the real `a2a contract publish` cannot reach a
// contract-set-v2 publication in THIS harness at all.
// internal/contract/publication_plan.go's own ContractPublicationFloor is
// "0.19.0" — a space's min_binary_version must be at or above it before
// publish will even ATTEMPT the v2 profile (belowFloor forces
// contract-tree-v1 otherwise) — but internal/e2e's own TestMain
// (main_test.go, off this brief's allowlist) hard-codes the exec'd binary's
// self-reported version to "0.1.0" via `-ldflags -X main.version=0.1.0", and
// the SAME min_binary_version field also gates the write funnel's own
// CC-085 guard ("local binary version older than space.yaml
// min_binary_version"). Raising the floor to unlock contract-set-v2 publish
// necessarily raises it past what the harness's own pinned binary version
// can ever write through — verified empirically: a first attempt at
// min_binary_version "1.0.0" refused EVERY `a2a submit` in the rig with
// exactly that CC-085 message. There is no floor value that satisfies both
// constraints in this harness. Publishing the contract is arrangement, not
// the thing §6.4 tests — `a2a data pack|deliver|fetch|verify` refusing is —
// and seeding it by a direct commit is the same technique
// helpers_test.go's own fixOriginManifest/seedOriginExtras already use for
// arrangement in this package. The digest itself is never hand-computed:
// contract.BuildCarriedSet (the same pure builder `contract publish` itself
// calls) is called over the exact bytes this function then commits, so the
// carried-set discipline spec 05a §5 protects is not bypassed, only the
// funnel write that would have produced the identical bytes is.
func publishDataAdversarialContract(t *testing.T, r *hostRig, slug, eventID string) (id string) {
	t.Helper()
	id = "XC-" + r.system + "-" + slug
	schemaPath := "schema/" + slug + ".schema.json"
	validPath := "fixtures/valid/" + slug + ".json"
	invalidPath := "fixtures/invalid/" + slug + ".json"

	descriptorRaw := []byte("---\n" +
		"schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: contract\n" +
		"title: the data export contract under test\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [beta]\n" +
		"thread: " + e2eFixtureThread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-07-23T10:00:00Z\n" +
		"category: api\n" +
		"priority: p3\n" +
		"blocking: false\n" +
		"classification: internal\n" +
		"version: \"1.0.0\"\n" +
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
		"at: 2026-07-23T11:00:00Z\n" +
		"version: \"1.0.0\"\n" +
		"digest: " + set.AggregateDigest + "\n" +
		"digest_profile: contract-set-v2\n" +
		"publication:\n" +
		"  intent_key: op-v1-" + strings.Repeat("d", 64) + "\n" +
		"  candidate_intent_digest: sha256:" + strings.Repeat("a", 64) + "\n" +
		"  version_selector: explicit:1.0.0\n" +
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
	gitRun(t, dir, "commit", "-m", "publish: "+id+"@1.0.0 (test fixture, direct commit — see this function's own deviation note)")
	gitRun(t, dir, "push", "origin", "main")
	return id
}

// stageWorkRequest writes a schema-valid work_request (category "data" —
// spec 05a's own opening move, "A ... opens a data request on a thread")
// addressed to `to`, mirroring stageQuestion/stageContract's own
// convention (host_loop_test.go).
func (r *hostRig) stageWorkRequest(id, thread, to string) (path string) {
	r.t.Helper()
	content := "---\n" +
		"schema: envelope/v1\n" +
		"id: " + id + "\n" +
		"type: work_request\n" +
		"title: deliver a synthetic export\n" +
		"space: " + r.spaceID + "\n" +
		"from: " + r.system + "\n" +
		"to: [" + to + "]\n" +
		"thread: " + thread + "\n" +
		"actor: {kind: agent, name: e2e}\n" +
		"created: 2026-08-04T09:00:00Z\n" +
		"category: data\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"acceptance_criteria: [\"delivered payload conforms to the pinned contract\"]\n" +
		"---\n" +
		"Please deliver a synthetic export of the order feed.\n"
	path = filepath.Join(r.projectDir, ".a2a", "staging", id+".md")
	mustWrite(r.t, path, content)
	return path
}

// mustSparseFile creates path as a SPARSE file of exactly size logical
// bytes, writing no actual content. bounds.CheckEntryBytes (bounds.go) is
// checked against os.Lstat's reported size before the walker ever opens or
// reads the file (safefs.go's readDataPackageFile), so a sparse file trips
// the per-entry bound just as cheaply as a real 128 MiB write would refuse
// it — without this test package or its CI runner actually paying for
// 128 MiB of I/O.
func mustSparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate %s to %d: %v", path, size, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// patchStagedManifestProvenance sets provenance.origin_system and
// provenance.extracted_at on a staged manifest.json IN PLACE, compensating
// for a CONFIRMED PRODUCT DEFECT this file's own deviations report names
// explicitly: cmd/a2a/data_wiring.go's dataCore.pack never populates
// datapackage.PackRequest.Provenance at all — cli.DataPackRequest (the
// frozen seam) carries no field for it, and `a2a data pack`'s own flag
// table (spec 05a §T1) has none either — so EVERY manifest a REAL `a2a
// data pack` invocation produces carries provenance.origin_system: "" (Go's
// zero value), which data-package/v1's own schema requires non-empty
// (minLength: 1). `a2a data pack` itself never validates its own output
// against the schema (Pack builds the Document structurally and returns
// it), so this is invisible until `a2a data deliver` refuses it via
// readStagedPackage's schema-validation step — which refuses EVERY real
// delivery, today, unconditionally. Patching the staged file here is this
// TEST's own arrangement workaround so the fetch/verify cases below (which
// need a package that actually landed) can still run; it is not a product
// fix, and the defect is reported in this brief's deviations, not silently
// routed around.

// backdateStagedManifestExpiry pushes a staged attempt's expires_at into the
// past so the fetch-side expiry guard can be exercised.
//
// It replaced `--expires=-1h`, which stopped working once `data pack` began
// refusing a non-positive expiry outright — a change made because the flag
// used to DEFAULT to zero, so a producer who simply forgot it minted a
// package that was expired the instant it existed and that its counterpart
// could never fetch. The refusal is the product being right; the test needs
// another way in, and editing the staged manifest is honest about being a
// test arrangement rather than a thing a producer can do.
func backdateStagedManifestExpiry(t *testing.T, stagingRoot string) {
	t.Helper()
	manifestPath := filepath.Join(stagingRoot, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read staged manifest: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode staged manifest: %v", err)
	}
	doc["expires_at"] = "2020-01-01T00:00:00Z"
	patched, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode patched manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		t.Fatalf("write patched manifest: %v", err)
	}
}

func patchStagedManifestProvenance(t *testing.T, stagingRoot string) {
	t.Helper()
	manifestPath := filepath.Join(stagingRoot, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read staged manifest: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode staged manifest: %v", err)
	}
	doc["provenance"] = map[string]any{
		"origin_system": "axon",
		"extracted_at":  "2026-08-04T12:00:00Z",
	}
	patched, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode patched manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, patched, 0o644); err != nil {
		t.Fatalf("write patched manifest: %v", err)
	}
}

// stagingDataDirNames snapshots the set of package ids currently staged
// under the rig project's own `.a2a/staging/data/` — the "nothing was
// staged" half of every pack-level refusal's assertion (spec 05a: "a2a
// data pack ... is write-free, like `contract preflight`"). A directory
// that does not exist yet (no pack has ever succeeded in this rig) is not
// an error: it is the same as an empty set.
func stagingDataDirNames(t *testing.T, r *hostRig) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(r.projectDir, ".a2a", "staging", "data"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read staging/data: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// dataSpaceSnapshot is the "nothing was written to the space" half of
// every refusal's assertion: the exact SHA main points to in the fake
// host's own bare origin, plus how many pull requests the fake host has
// ever recorded. Neither can change from a command that refused before
// ever reaching the write funnel — main only moves on a real merge, and
// PRs only open on a real push+open call.
type dataSpaceSnapshot struct {
	mainSHA string
	prCount int
}

func snapshotDataSpace(t *testing.T, r *hostRig) dataSpaceSnapshot {
	t.Helper()
	return dataSpaceSnapshot{
		mainSHA: strings.TrimSpace(gitOutput(t, r.fx.RemoteURL(), "rev-parse", "main")),
		prCount: len(r.gh.PRs()),
	}
}

func assertDataSpaceUnchanged(t *testing.T, r *hostRig, before dataSpaceSnapshot) {
	t.Helper()
	after := snapshotDataSpace(t, r)
	if after != before {
		t.Fatalf("the space changed on a refusal: before=%+v after=%+v", before, after)
	}
}

// --- A: `a2a data pack` refusals — write-free, so the source directory is
// the only surface that can be tampered with, and staging + the space must
// both stay untouched (spec 05a: "Write-free, like `contract preflight`").

func TestDataAdversarialPackRefusals(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	contractID := publishDataAdversarialContract(t, r, "packrefusal", "01K1A2B3C4D5E6F7G8H9J0KAP1")
	r.mustRun("sync")
	contractRef := contractID + "@1.0.0"

	run := func(t *testing.T, from string, extraArgs ...string) (stdout, stderr string, code int) {
		t.Helper()
		beforeStaging := stagingDataDirNames(t, r)
		beforeSpace := snapshotDataSpace(t, r)
		args := append([]string{
			"data", "pack", "--contract", contractRef, "--from", from,
			"--profile", "synthetic", "--format", "json",
		}, extraArgs...)
		stdout, stderr, code = r.run(args...)
		if code == 0 {
			t.Fatalf("pack %v: exit 0, want a refusal\nstdout: %s\nstderr: %s", extraArgs, stdout, stderr)
		}
		if got := stagingDataDirNames(t, r); !equalStrings(got, beforeStaging) {
			t.Fatalf("pack staged something despite refusing: before=%v after=%v", beforeStaging, got)
		}
		assertDataSpaceUnchanged(t, r, beforeSpace)
		return stdout, stderr, code
	}

	// §6.4 case: "a symlink entry in the source directory".
	t.Run("SymlinkEntryInSource", func(t *testing.T) {
		from := t.TempDir()
		mustWrite(t, filepath.Join(from, "entry.json"), `{"example":"a"}`+"\n")
		if err := os.Symlink(filepath.Join(from, "entry.json"), filepath.Join(from, "link")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		_, stderr, _ := run(t, from)
		if !strings.Contains(stderr, "symlinks are refused") {
			t.Errorf("refusal does not name the symlink guard: %s", stderr)
		}
		if !strings.Contains(stderr, "link") {
			t.Errorf("refusal does not name the offending entry: %s", stderr)
		}
	})

	// §6.4 case: "an entry that exceeds the per-entry byte bound". The
	// refusal fires at the walker's Lstat-only pre-check
	// (safefs.go's readDataPackageFile: `bounds.CheckEntryBytes(before.
	// Size())`, before the file is ever opened) — that specific call site
	// wraps ErrBoundExceeded with no path, unlike the second, read-time
	// check a few lines below it in the same file; the bound and the byte
	// counts are still named, which is what a caller acts on.
	t.Run("EntryExceedsPerEntryByteBound", func(t *testing.T) {
		from := t.TempDir()
		bound := datapackage.DefaultBounds().MaxEntryBytes
		mustSparseFile(t, filepath.Join(from, "big.dat"), bound+1)
		_, stderr, _ := run(t, from)
		if !strings.Contains(stderr, "byte per-entry bound") {
			t.Errorf("refusal does not name the per-entry bound guard: %s", stderr)
		}
		if !strings.Contains(stderr, "134217729 bytes") {
			t.Errorf("refusal does not name the offending size: %s", stderr)
		}
	})

	// §6.4 case: "a payload that is valid JSON but not the declared format"
	// — direction one: format json declared, the bytes are two concatenated
	// JSON documents (what an ndjson-shaped payload actually looks like),
	// which is not one valid JSON document. This is refused one step
	// EARLIER than the conformance checker: pack.go's own record-count step
	// (§T2.5/plan D-11's second row) runs CountRecords before the
	// EntryChecker ever sees the entry, and for FormatJSON, CountRecords
	// itself must decode the bytes to count them — so a malformed json
	// entry is named "not valid json" (records.go's own wording) rather
	// than reaching checkJSONEntry's "entry is not valid JSON" at all. Both
	// wrap the SAME ErrEntryNonConforming sentinel ("entry does not
	// conform"); only the detail differs by which step caught it first.
	t.Run("JSONDeclaredButPayloadIsConcatenatedDocuments", func(t *testing.T) {
		from := t.TempDir()
		mustWrite(t, filepath.Join(from, "entry.dat"), "{\"example\":\"a\"}\n{\"example\":\"b\"}\n")
		_, stderr, _ := run(t, from, "--format", "json")
		if !strings.Contains(stderr, "entry does not conform") {
			t.Errorf("refusal is not the conformance guard: %s", stderr)
		}
		if !strings.Contains(stderr, "not valid json") {
			t.Errorf("refusal does not name why: %s", stderr)
		}
		if !strings.Contains(stderr, "entry.dat") {
			t.Errorf("refusal does not name the offending entry: %s", stderr)
		}
	})

	// §6.4 case: "a payload that is valid JSON but not the declared format"
	// — direction two: format ndjson declared, the bytes are one
	// pretty-printed JSON document (each individual line is not, on its
	// own, valid JSON).
	t.Run("NDJSONDeclaredButPayloadIsOnePrettyPrintedDocument", func(t *testing.T) {
		from := t.TempDir()
		mustWrite(t, filepath.Join(from, "entry.dat"), "{\n  \"example\": \"a\"\n}\n")
		_, stderr, _ := run(t, from, "--format", "ndjson")
		if !strings.Contains(stderr, "entry does not conform") {
			t.Errorf("refusal is not the conformance guard: %s", stderr)
		}
		if !strings.Contains(stderr, "record 1") {
			t.Errorf("refusal does not name the failing record by line number: %s", stderr)
		}
	})

	// §6.4 case: "an ndjson entry whose final line is truncated
	// mid-record" — the first record is well-formed and passes; the
	// second, final record has no closing brace and no trailing newline.
	t.Run("NDJSONFinalLineTruncatedMidRecord", func(t *testing.T) {
		from := t.TempDir()
		mustWrite(t, filepath.Join(from, "entry.dat"), "{\"example\":\"a\"}\n{\"example\":\"b\"")
		_, stderr, _ := run(t, from, "--format", "ndjson")
		if !strings.Contains(stderr, "entry does not conform") {
			t.Errorf("refusal is not the conformance guard: %s", stderr)
		}
		if !strings.Contains(stderr, "record 2") {
			t.Errorf("refusal does not name the truncated record by line number: %s", stderr)
		}
	})

	// §6.4 case: "data pack --profile production".
	t.Run("ProfileProduction", func(t *testing.T) {
		from := t.TempDir()
		mustWrite(t, filepath.Join(from, "entry.json"), `{"example":"a"}`+"\n")
		beforeStaging := stagingDataDirNames(t, r)
		beforeSpace := snapshotDataSpace(t, r)
		stdout, stderr, code := r.run(
			"data", "pack", "--contract", contractRef, "--from", from,
			"--profile", "production", "--format", "json",
		)
		if code == 0 {
			t.Fatalf("pack --profile production: exit 0, want a refusal\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		if !strings.Contains(stderr, "data_profile production is refused") {
			t.Errorf("refusal is not the production-profile guard: %s", stderr)
		}
		if got := stagingDataDirNames(t, r); !equalStrings(got, beforeStaging) {
			t.Fatalf("pack --profile production staged something despite refusing: before=%v after=%v", beforeStaging, got)
		}
		assertDataSpaceUnchanged(t, r, beforeSpace)
	})
}

// --- B: `a2a data deliver` refusals against a HAND-AUTHORED staging root —
// spec 05a §6.4's own framing for why this matters: "deliver is a separate
// invocation from pack, so nothing proves the manifest sitting in a
// staging root is the one pack wrote." `--fulfills` never needs to resolve
// for either case below: readStagedPackage's own schema-validation refusal
// fires before deliver ever looks the fulfilled request up
// (cmd/a2a/data_wiring.go's deliver reads the staging root FIRST), so a
// syntactically well-formed but never-committed work_request id is enough
// to reach the command's usage gate.

const dataAdversarialFulfillsPlaceholder = "XW-axon-20260804-w000"

// dataAdversarialManifest renders a schema-shaped data-package/v1 manifest
// whose one entry declares entryPath and whose locator is locatorValue —
// every OTHER field valid, so the ONE tampered field is what the schema
// refuses on. Mirrors data_wiring_test.go's own
// TestReadStagedPackageRefusesAManifestNamingAPathOutsideTheRoot fixture,
// generalized to vary either axis.
func dataAdversarialManifest(entryPath, locatorValue string) string {
	return `{
  "schema": "data-package/v1",
  "id": "DP-axon-20260804-k3f9",
  "thread": "thread:axon-20260804-k3f9",
  "contract": "XC-axon-widget@1.0.0#sha256:` + strings.Repeat("a", 64) + `",
  "mode": "snapshot",
  "classification": "internal",
  "data_profile": "synthetic",
  "format": "json",
  "transport_driver": "space-git",
  "locator": "` + locatorValue + `",
  "entries": [{
    "path": "` + entryPath + `",
    "role": "dataset",
    "media_type": "application/json",
    "conforms_to": "schema/widget.schema.json",
    "size_bytes": 7,
    "digest": "sha256:` + strings.Repeat("b", 64) + `",
    "record_count": 1
  }],
  "aggregate_digest": "sha256:` + strings.Repeat("c", 64) + `",
  "size_bytes": 7,
  "record_count": 1,
  "attempt": 1,
  "created_at": "2026-08-04T12:00:00Z",
  "expires_at": "2026-09-04T12:00:00Z",
  "provenance": {"origin_system": "axon", "extracted_at": "2026-08-04T12:00:00Z"}
}`
}

func TestDataAdversarialDeliverRefusals(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")

	runDeliver := func(t *testing.T, manifest string) (stdout, stderr string, code int, secretPath string) {
		t.Helper()
		parent := t.TempDir()
		secretPath = filepath.Join(parent, "secret.txt")
		mustWrite(t, secretPath, "private-payload-must-never-leak")
		root := filepath.Join(parent, "staging")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
		mustWrite(t, filepath.Join(root, "manifest.json"), manifest)

		beforeSpace := snapshotDataSpace(t, r)
		stdout, stderr, code = r.run("data", "deliver", root, "--fulfills", dataAdversarialFulfillsPlaceholder)
		if code == 0 {
			t.Fatalf("deliver: exit 0, want a refusal\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		assertDataSpaceUnchanged(t, r, beforeSpace)

		got, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatalf("re-read secret file: %v", err)
		}
		if string(got) != "private-payload-must-never-leak" {
			t.Fatalf("the out-of-root file was modified by a refused deliver: %q", got)
		}
		return stdout, stderr, code, secretPath
	}

	// §6.4 case: "a manifest naming a path outside the package root".
	t.Run("ManifestEntryPathEscapesPackageRoot", func(t *testing.T) {
		manifest := dataAdversarialManifest("../secret.txt", "axon/data/DP-axon-20260804-k3f9")
		stdout, stderr, _, _ := runDeliver(t, manifest)
		combined := stdout + stderr
		if strings.Contains(combined, "private-payload-must-never-leak") {
			t.Fatalf("the refusal leaked the escaped file's contents: %s", combined)
		}
		if !strings.Contains(combined, "entries.0.path") {
			t.Errorf("refusal does not name the offending entry path: %s", combined)
		}
	})

	// §6.4 case: "a manifest whose locator carries ... an absolute path".
	t.Run("ManifestLocatorIsAnAbsolutePath", func(t *testing.T) {
		manifest := dataAdversarialManifest("orders.json", "/etc/passwd")
		stdout, stderr, _, _ := runDeliver(t, manifest)
		combined := stdout + stderr
		if !strings.Contains(combined, "locator") {
			t.Errorf("refusal does not name the locator field: %s", combined)
		}
	})

	// §6.4 case: "a manifest whose locator carries a token".
	t.Run("ManifestLocatorIsCredentialShaped", func(t *testing.T) {
		manifest := dataAdversarialManifest("orders.json", "user:token@host/path")
		stdout, stderr, _, _ := runDeliver(t, manifest)
		combined := stdout + stderr
		if !strings.Contains(combined, "locator") {
			t.Errorf("refusal does not name the locator field: %s", combined)
		}
	})
}

// --- C: `a2a data fetch`/`a2a data verify` refusals against a REAL
// delivered package — unlike A and B, these two cases need something
// genuinely committed to the space to refuse AGAINST: an already-expired
// package (fetch), and a package id nothing ever delivered (verify).

func TestDataAdversarialFetchAndVerifyRefusals(t *testing.T) {
	t.Parallel()

	r := newHostRig(t, "axon", "axon", "beta")
	contractID := publishDataAdversarialContract(t, r, "expirecheck", "01K1A2B3C4D5E6F7G8H9J0KAX1")
	r.mustRun("sync")

	thread := "thread:axon-20260804-w900"
	requestID := "XW-axon-20260804-w900"
	draft := r.stageWorkRequest(requestID, thread, "beta")
	r.mustRun("submit", draft)
	r.mustRun("sync")

	from := t.TempDir()
	mustWrite(t, filepath.Join(from, "orders.json"), `{"example":"x"}`+"\n")

	// §6.4 case: "an expired package at fetch". The attempt is packed
	// normally and its staged manifest is then backdated — `data pack` now
	// refuses a non-positive --expires, so a package cannot be minted
	// already-expired through the product's own surface.
	packOut := r.mustRun(
		"data", "pack", "--contract", contractID+"@1.0.0", "--from", from,
		"--profile", "synthetic", "--format", "json", "--fulfills", requestID,
		"--json",
	)
	var packResult cli.DataResult
	if err := json.Unmarshal([]byte(packOut), &packResult); err != nil {
		t.Fatalf("decode pack --json output: %v\n%s", err, packOut)
	}
	if packResult.Manifest == nil {
		t.Fatalf("pack --json produced no manifest: %s", packOut)
	}
	packageID := packResult.Manifest.ID
	stagingRoot := filepath.Join(r.projectDir, ".a2a", "staging", "data", packageID)
	patchStagedManifestProvenance(t, stagingRoot)
	backdateStagedManifestExpiry(t, stagingRoot)

	r.mustRun("data", "deliver", stagingRoot, "--fulfills", requestID)
	r.mustRun("sync")

	t.Run("ExpiredPackageAtFetch", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "out")
		beforeSpace := snapshotDataSpace(t, r)
		stdout, stderr, code := r.run("data", "fetch", packageID, "--to", dest)
		if code == 0 {
			t.Fatalf("fetch: exit 0, want a refusal\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "package has expired") {
			t.Errorf("refusal is not the expiry guard: %s", stdout+stderr)
		}
		if _, err := os.Stat(dest); err == nil {
			t.Fatalf("destination %s was created despite the refusal", dest)
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", dest, err)
		}
		assertDataSpaceUnchanged(t, r, beforeSpace)
	})

	// §6.4 case: "data verify against a package id that does not exist".
	// Well-formed enough to reach ErrDataPackageNotFound rather than a
	// bare id-shape usage refusal: a real system name, a real date, and a
	// rand4 drawn from the id alphabet (which excludes i/l/o/u) — nothing
	// this rig, or anything else, ever delivered under this exact id.
	t.Run("VerifyAgainstNonexistentPackageID", func(t *testing.T) {
		beforeSpace := snapshotDataSpace(t, r)
		stdout, stderr, code := r.run("data", "verify", "DP-axon-20260101-abcd")
		if code == 0 {
			t.Fatalf("verify: exit 0, want a refusal\nstdout: %s\nstderr: %s", stdout, stderr)
		}
		if !strings.Contains(stdout+stderr, "not present in the local mirror") {
			t.Errorf("refusal is not the not-found guard: %s", stdout+stderr)
		}
		assertDataSpaceUnchanged(t, r, beforeSpace)
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
