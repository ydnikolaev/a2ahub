package space

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// contractEventV2WithID is contractEventV2's own format
// (contract_history_test.go), parameterized by event id: contractEventV2
// hardcodes contractTestEventPath's own id, so a second v2 establishment on
// a distinct event path needs its own matching id in both the filename and
// the document's own `event:` field, or ResolveContractVersion refuses it as
// a mismatched owner/actor/event id before this file's own assertions ever
// run.
func contractEventV2WithID(eventID, version, digest string) string {
	return fmt.Sprintf(
		"schema: event/v2\nevent: %s\nspace: test\nsubject: %s\ntransition: publish\nactor: {kind: agent, name: publisher, system: axon}\nat: 2026-08-03T12:00:00Z\nversion: %q\ndigest: %s\ndigest_profile: contract-set-v2\npublication:\n  intent_key: op-v1-%s\n  candidate_intent_digest: sha256:%s\n  version_selector: explicit:%s\n  operation_key: op-v1-%s\n",
		eventID, contractTestID, version, digest, strings.Repeat("a", 64), strings.Repeat("b", 64), version, strings.Repeat("c", 64),
	)
}

// commitLegacyContractVersionForTest is a corrected variant of
// commitLegacyContractVersion (contract_history_test.go, off-limits to this
// brief): that helper's own generated event id is 25 characters, one short
// of the 26 the shipped ULID grammar requires
// ("01K1A2B3C4D5E6F7G8H9J0K1" + one appended digit), which every existing
// caller of it happens not to notice because it only ever asserts
// errors.Is(err, ErrContractHistoryInvalid) — the SAME sentinel a malformed-
// event validation failure wraps, so a 25-character id and a genuine
// duplicate-establishment refusal are indistinguishable to those tests. This
// file's own assertions ARE more specific (ErrDataContractNoSchemas), so
// they need a legacy fixture whose event id is actually valid.
func commitLegacyContractVersionForTest(t *testing.T, repo, version, schemaRaw string) string {
	t.Helper()
	const eventID = "01K1A2B3C4D5E6F7G8H9J0K1M9"
	files := map[string]string{"schema/main.schema.json": schemaRaw}
	eventPath := "axon/events/2026/" + eventID + ".yaml"
	return commitContractPublicationAtPath(t, repo, eventPath, contractV1Descriptor(version), files, contractEventV1WithID(eventID, version, legacyContractDigest(t, files)))
}

func TestResolveDataContractSchemasReturnsOnlySchemaRoleEntries(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "schema bytes\n")
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))

	schemas, pinnedRef, err := ResolveDataContractSchemas(t.Context(), repo, contractTestID+"@1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("ResolveDataContractSchemas: %v", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("schemas = %d entries, want exactly the one schema-role entry: %v", len(schemas), schemas)
	}
	raw, ok := schemas["schema/order.schema.json"]
	if !ok || string(raw) != "schema bytes\n" {
		t.Fatalf("schemas[schema/order.schema.json] = %q, ok=%v", raw, ok)
	}
	want := contractTestID + "@1.0.0#" + digest
	if pinnedRef != want {
		t.Fatalf("pinnedRef = %q, want %q", pinnedRef, want)
	}
}

func TestResolveDataContractSchemasCallsResolveContractVersionExactlyOnce(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "schema bytes\n")
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))

	// A run that only re-verifies the contract's carried set once (plan
	// D-2) requires this function to walk the historical Git tree only
	// once per call, never once per returned schema entry — proven here by
	// counting HEAD-relative rev-list/ls-tree walks is impractical without
	// instrumenting git itself, so this asserts the cheaper, equivalent
	// property this function's own contract offers a caller: ONE
	// ResolveDataContractSchemas call is sufficient to answer every entry's
	// conformance question for the whole run (multiple schema entries
	// resolved from ONE call, not one call per entry).
	descriptor2, files2, digest2 := contractV2PublicationTwoSchemas(t, "2.0.0")
	const secondEventID = "01K1A2B3C4D5E6F7G8H9J0K1M8"
	commitContractPublicationAtPath(t, repo, "axon/events/2026/"+secondEventID+".yaml", descriptor2, files2, contractEventV2WithID(secondEventID, "2.0.0", digest2))

	schemas, _, err := ResolveDataContractSchemas(t.Context(), repo, contractTestID+"@2.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("ResolveDataContractSchemas: %v", err)
	}
	if len(schemas) != 2 {
		t.Fatalf("schemas = %d entries, want 2 schema-role entries resolved from ONE call: %v", len(schemas), schemas)
	}
}

// contractV2PublicationTwoSchemas is a small variant of contractV2Publication
// declaring TWO schema-role entries, so a resolve that only ever returned
// the FIRST schema it found (rather than every declared one) would be
// caught by TestResolveDataContractSchemasCallsResolveContractVersionExactlyOnce.
func contractV2PublicationTwoSchemas(t *testing.T, version string) (string, map[string]string, string) {
	t.Helper()
	descriptor := `---
schema: envelope/v2
id: ` + contractTestID + `
type: contract
title: Widget contract
space: test
from: axon
to: [seomatrix]
actor: {kind: agent, name: publisher}
created: 2026-08-03T09:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: "` + version + `"
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:axon-20260803-c5h9
artifacts:
  - path: schema/order.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: schema/customer.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/order.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: fixtures/invalid/order.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: artifacts/errors.yaml
    role: errors
    normative: true
    media_type: application/yaml
---
contract
`
	files := map[string]string{
		"schema/order.schema.json":    "order schema\n",
		"schema/customer.schema.json": "customer schema\n",
		"fixtures/valid/order.json":   "{}\n",
		"fixtures/invalid/order.json": "null\n",
		"artifacts/errors.yaml":       "errors: []\n",
	}
	descriptor, files, digest := carriedSetDigestForFixture(t, descriptor, files)
	return descriptor, files, digest
}

// TestResolveDataContractSchemasResolvesALegacyContract is the tooth for the
// second defect the two-party e2e found.
//
// A legacy contract-tree-v1 carried set never populates Entry.Role
// (buildLegacySet, set.go), so a role-only filter finds ZERO schemas in it
// and reports the contract as having none. That is not a legacy edge case —
// it is every contract published before the v2 profile, and it made the data
// loop unusable against all of them. The shipped conformance core already
// solved this by classifying on the path prefix under the legacy profile
// (conformanceSchemas, conformance.go); the data resolver now does the same.
//
// An earlier revision of this test asserted the broken behaviour as though it
// were the specification.
func TestResolveDataContractSchemasResolvesALegacyContract(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	commitLegacyContractVersionForTest(t, repo, "1.0.0", "legacy schema\n")

	schemas, _, err := ResolveDataContractSchemas(t.Context(), repo, contractTestID+"@1.0.0", contractHistoryValidator(t))
	if err != nil {
		t.Fatalf("ResolveDataContractSchemas(legacy contract) = %v, want nil", err)
	}
	if len(schemas) != 1 {
		t.Fatalf("schemas = %d entries, want the one schema/ entry: %v", len(schemas), schemas)
	}
	if string(schemas["schema/main.schema.json"]) != "legacy schema\n" {
		t.Fatalf("schema bytes = %q", schemas["schema/main.schema.json"])
	}
}

// TestResolveDataContractSchemasNamesSyncForAStaleMirror is B42's tooth for
// the "absent local copy" branch: the exact shape the wave lost a run to —
// a contract version that WAS published and merged upstream, while the
// packing checkout's own local mirror simply had not synced past the
// commit that published it. That must read as "run a2a sync", never as
// "was never published" (ErrContractNotPublished is a different sentinel
// this test explicitly refuses).
func TestResolveDataContractSchemasNamesSyncForAStaleMirror(t *testing.T) {
	t.Parallel()

	publisher, mirror := newContractHistoryRepoPair(t)

	descriptor, files, digest := contractV2Publication(t, "1.0.0", "schema bytes v1\n")
	commitContractPublication(t, publisher, descriptor, files, contractEventV2("1.0.0", digest))
	contractTestGit(t, mirror, "fetch", "-q", "origin", "main")

	// Published to origin AFTER the mirror last synced — the mirror never
	// sees this commit unless it fetches again, which this test
	// deliberately never does.
	descriptor2, files2, digest2 := contractV2Publication(t, "2.0.0", "schema bytes v2\n")
	const secondEventID = "01K1A2B3C4D5E6F7G8H9J0K1M8"
	commitContractPublicationAtPath(t, publisher, "axon/events/2026/"+secondEventID+".yaml", descriptor2, files2, contractEventV2WithID(secondEventID, "2.0.0", digest2))

	_, _, err := ResolveDataContractSchemas(t.Context(), mirror, contractTestID+"@2.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrDataContractMirrorStale) {
		t.Fatalf("error = %v, want ErrDataContractMirrorStale", err)
	}
	if errors.Is(err, ErrContractNotPublished) {
		t.Fatalf("error = %v must NOT also satisfy errors.Is(ErrContractNotPublished) — the two diagnoses must diverge", err)
	}
	if !strings.Contains(err.Error(), "a2a sync") {
		t.Fatalf("error = %q, want it to name a2a sync", err.Error())
	}
}

// TestResolveDataContractSchemasReportsAGenuinelyUnpublishedVersion is
// B42's other branch: the mirror's own current descriptor already reaches
// PAST the requested version (2.0.0 was skipped straight to 3.0.0), so the
// mirror has positive evidence the version was never published — it is not
// merely behind. This must keep the original ErrContractNotPublished
// sentence, and must NOT also read as ErrDataContractMirrorStale.
func TestResolveDataContractSchemasReportsAGenuinelyUnpublishedVersion(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	descriptor, files, digest := contractV2Publication(t, "1.0.0", "schema bytes v1\n")
	commitContractPublication(t, repo, descriptor, files, contractEventV2("1.0.0", digest))

	// 2.0.0 is skipped entirely; the mirror jumps straight to 3.0.0, so
	// its own current descriptor already proves it has synced past the
	// point where 2.0.0 would have appeared.
	descriptor2, files2, digest2 := contractV2Publication(t, "3.0.0", "schema bytes v3\n")
	const secondEventID = "01K1A2B3C4D5E6F7G8H9J0K1M8"
	commitContractPublicationAtPath(t, repo, "axon/events/2026/"+secondEventID+".yaml", descriptor2, files2, contractEventV2WithID(secondEventID, "3.0.0", digest2))

	_, _, err := ResolveDataContractSchemas(t.Context(), repo, contractTestID+"@2.0.0", contractHistoryValidator(t))
	if !errors.Is(err, ErrContractNotPublished) {
		t.Fatalf("error = %v, want ErrContractNotPublished", err)
	}
	if errors.Is(err, ErrDataContractMirrorStale) {
		t.Fatalf("error = %v must NOT also satisfy errors.Is(ErrDataContractMirrorStale) — the two diagnoses must diverge", err)
	}
}

// newContractHistoryRepoPair is newContractHistoryRepo's two-clone variant:
// a bare origin plus two independent working clones of it ("publisher" and
// "mirror"), so a test can push from one and prove the other's own
// refs/remotes/origin/main only advances on an explicit fetch — the exact
// "local mirror has not synced" shape B42 names, which a single shared
// clone (push updates its own remote-tracking ref immediately) cannot
// reproduce.
func newContractHistoryRepoPair(t *testing.T) (publisher, mirror string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	contractTestGit(t, root, "init", "-q", "--bare", "-b", "main", origin)
	publisher = filepath.Join(root, "publisher")
	contractTestGit(t, root, "clone", "-q", origin, publisher)
	mirror = filepath.Join(root, "mirror")
	contractTestGit(t, root, "clone", "-q", origin, mirror)
	return publisher, mirror
}

func TestSplitDataContractReferenceRefusesNonCanonicalForms(t *testing.T) {
	t.Parallel()

	for _, ref := range []string{
		"", "XC-axon-widget", "XC-axon-widget@", "@1.0.0",
		"XC-axon-widget@v1.0.0", "XC-axon-widget@1.0", "XR-axon-widget@1.0.0",
		"XC-axon-widget@1.0.0@extra",
		"XC-axon-widget@1.0.0#", // a "#" with nothing after it pins nothing
	} {
		if _, _, _, err := splitDataContractReference(ref); !errors.Is(err, ErrContractInvalidReference) {
			t.Fatalf("splitDataContractReference(%q) error = %v, want ErrContractInvalidReference", ref, err)
		}
	}
}

// TestSplitDataContractReferenceRoundTripsItsOwnPinnedForm is the tooth for
// the defect the two-party e2e found: ResolveDataContractSchemas emits
// "<id>@<version>#<digest>", pack stores it as the manifest's contract
// field exactly as T2.1 requires, and verify hands it straight back — so a
// parser that refused the digest suffix made `data verify` fail against
// every package `data pack` had ever produced.
func TestSplitDataContractReferenceRoundTripsItsOwnPinnedForm(t *testing.T) {
	t.Parallel()

	const digest = "sha256:635aca57fdb66474eb5b4d52df0500f855d5aed06162cb4366da170b3cae5382"
	id, canonical, pinned, err := splitDataContractReference("XC-axon-widget@1.0.0#" + digest)
	if err != nil {
		t.Fatalf("splitDataContractReference(pinned form) = %v, want nil", err)
	}
	if id != "XC-axon-widget" || canonical != "1.0.0" || pinned != digest {
		t.Fatalf("= (%q, %q, %q), want (XC-axon-widget, 1.0.0, %s)", id, canonical, pinned, digest)
	}

	// The digest-free form stays valid: that is what a human types.
	id, canonical, pinned, err = splitDataContractReference("XC-axon-widget@1.0.0")
	if err != nil || id != "XC-axon-widget" || canonical != "1.0.0" || pinned != "" {
		t.Fatalf("= (%q, %q, %q, %v), want (XC-axon-widget, 1.0.0, \"\", nil)", id, canonical, pinned, err)
	}
}

func TestResolveDataPackageReturnsManifestAndPayload(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	packageID := "DP-axon-20260804-ab12"
	commitDataPackageFixture(t, repo, "axon", packageID, map[string]string{
		"manifest.json": `{"schema":"data-package/v1","id":"` + packageID + `"}`,
		"orders.json":   `[{"id":1}]`,
	})

	got, err := ResolveDataPackage(t.Context(), repo, packageID)
	if err != nil {
		t.Fatalf("ResolveDataPackage: %v", err)
	}
	if got.System != "axon" || got.Root != "axon/data/"+packageID {
		t.Fatalf("System/Root = %q/%q", got.System, got.Root)
	}
	if string(got.ManifestRaw) != `{"schema":"data-package/v1","id":"`+packageID+`"}` {
		t.Fatalf("ManifestRaw = %q", got.ManifestRaw)
	}
	if len(got.Payload) != 1 || string(got.Payload["orders.json"]) != `[{"id":1}]` {
		t.Fatalf("Payload = %v", got.Payload)
	}
}

func TestResolveDataPackageRefusesUnknownPackage(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	commitDataPackageFixture(t, repo, "axon", "DP-axon-20260804-ab12", map[string]string{
		"manifest.json": `{}`,
	})

	_, err := ResolveDataPackage(t.Context(), repo, "DP-axon-20260804-zz99")
	if !errors.Is(err, ErrDataPackageNotFound) {
		t.Fatalf("error = %v, want ErrDataPackageNotFound", err)
	}
}

func TestResolveDataPackageRefusesMissingManifest(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	packageID := "DP-axon-20260804-ab12"
	// Deliberately only the payload lands, never manifest.json — proves
	// this function does not treat "the directory exists" as "the package
	// is resolvable" (this is the seeded-red receipt: with the
	// manifestRaw == nil guard removed, this case would return a
	// DataPackageBytes with an empty ManifestRaw and a nil error instead
	// of ErrDataPackageNotFound).
	commitDataPackageFixture(t, repo, "axon", packageID, map[string]string{
		"orders.json": `[]`,
	})

	_, err := ResolveDataPackage(t.Context(), repo, packageID)
	if !errors.Is(err, ErrDataPackageNotFound) {
		t.Fatalf("error = %v, want ErrDataPackageNotFound", err)
	}
}

func TestResolveDataPackageRefusesMalformedID(t *testing.T) {
	t.Parallel()

	repo := newContractHistoryRepo(t)
	for _, id := range []string{"", "not-an-id", "XC-axon-widget", "XD-axon-20260804-ab12"} {
		if _, err := ResolveDataPackage(t.Context(), repo, id); !errors.Is(err, ErrDataPackageInvalidReference) {
			t.Fatalf("ResolveDataPackage(%q) error = %v, want ErrDataPackageInvalidReference", id, err)
		}
	}
}

// commitDataPackageFixture commits files (relative to dataPackageDir(system,
// packageID)) to origin/main, exactly the layout DeliverDataPackage's own
// FileWrite assembly produces — so a resolve test proves this function reads
// back what a deliver actually writes, not an invented shape.
func commitDataPackageFixture(t *testing.T, repo, system, packageID string, files map[string]string) {
	t.Helper()
	root := dataPackageDir(system, packageID)
	for name, raw := range files {
		writeContractCandidateFile(t, repo, root+"/"+name, raw)
	}
	contractTestGit(t, repo, "add", "--", root)
	contractTestGit(t, repo, "commit", "-q", "-m", "deliver "+packageID)
	contractTestGit(t, repo, "push", "-q", "-u", "origin", "main")
}

// carriedSetDigestForFixture computes the contract-set-v2 aggregate digest
// for a hand-authored descriptor/files pair, the same way contractV2Publication
// (contract_history_test.go) does, so a second fixture with a different
// artifacts[] shape does not have to re-derive that math a third time.
func carriedSetDigestForFixture(t *testing.T, descriptor string, files map[string]string) (string, map[string]string, string) {
	t.Helper()
	parsed, err := contract.ParseDescriptor([]byte(descriptor))
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]contract.CandidateFile, 0, len(files))
	for path, raw := range files {
		candidates = append(candidates, contract.CandidateFile{Path: path, Kind: contract.CandidateRegular, Raw: []byte(raw)})
	}
	set, issues := contract.BuildCarriedSet(contract.ProfileContractSetV2, []byte(descriptor), parsed, candidates)
	if len(issues) != 0 {
		t.Fatalf("test v2 carried set: %v", issues)
	}
	return descriptor, files, set.AggregateDigest
}
