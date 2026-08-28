package contract

import (
	"bytes"
	"testing"
)

func TestBuildCandidateIntentDeclaredGoldenAndNormalizedSemantics(t *testing.T) {
	t.Parallel()

	rawA := declaredIntentDescriptor(`schema: envelope/v2
id: XC-atlas-demo
type: contract
title: Demo contract
space: demo-space
from: atlas
to: [consumer]
actor: {kind: agent, name: first-agent, model: gpt-5}
created: 2026-08-03T10:00:00Z
category: api
priority: p2
blocking: false
classification: internal
version: 1.2.3
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:atlas-20260803-c5h9
generated_from:
  tool: exporter/2
  source_digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
artifacts:
  - path: schema/order.schema.json
    role: schema
    normative: true
    media_type: application/schema+json
  - path: fixtures/valid/order.json
    role: valid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
  - path: fixtures/invalid/missing-id.json
    role: invalid-fixture
    normative: true
    media_type: application/json
    conforms_to: schema/order.schema.json
`, "# Demo\r\n\r\nSame semantic Markdown.\r\n\r\n")
	rawB := declaredIntentDescriptor(`title: Demo contract
type: contract
id: XC-atlas-demo
schema: envelope/v2
space: demo-space
from: atlas
to:
  - consumer
actor: {kind: human, name: retry-operator}
created: 2030-01-01T00:00:00Z
priority: p2
category: api
blocking: false
classification: internal
version: 99.0.0
schema_format: json-schema-2020-12
compat_policy: default
thread: thread:atlas-20260803-c5h9
generated_from:
  source_digest: sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
  tool: exporter/2
artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/missing-id.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`, "# Demo\n\nSame semantic Markdown.\n")

	first, issues := BuildCandidateIntent(CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: rawA},
		Files:      validCandidates()[:3],
	})
	assertNoIssues(t, issues)
	second, issues := BuildCandidateIntent(CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: rawB},
		Files:      validCandidates()[:3],
	})
	assertNoIssues(t, issues)

	if first.InventoryMode() != InventoryDeclaredV2 {
		t.Fatalf("inventory mode = %q", first.InventoryMode())
	}
	if first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatalf("cosmetic YAML/generated fields/body newline changes changed semantic intent:\n%s\n%s", first.CanonicalBytes(), second.CanonicalBytes())
	}
	const want = "sha256:e9eea7dab436b7ce32fa9e32285eebc1c8bdb70f208d3cdca4f390bd1c563794"
	if first.Digest() != want {
		t.Fatalf("candidate intent digest = %q, want golden %q\ncanonical=%s", first.Digest(), want, first.CanonicalBytes())
	}
}

// TestBuildCandidateIntentCarriesExportSourceProjectionForDeclaredV2 pins
// spec §10's required shape (AC-11): the intent builder computes
// export-source-v1 once, from the CANDIDATE-side v2 set it already builds
// and would otherwise discard, and the planner must read that carried value
// rather than re-deriving a second one. The cross-check against an
// independently built CarriedSet.ExportSource() over the identical bytes
// proves the carried value is not a drifted second implementation.
func TestBuildCandidateIntentCarriesExportSourceProjectionForDeclaredV2(t *testing.T) {
	t.Parallel()

	raw := declaredIntentDescriptor(`schema: envelope/v2
id: XC-atlas-demo
type: contract
title: Demo contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/missing-id.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`, "# Demo\n")
	snapshot := CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files:      cloneCandidates(validCandidates()[:3]),
	}
	intent, issues := BuildCandidateIntent(snapshot)
	assertNoIssues(t, issues)

	projection, ok := intent.ExportSource()
	if !ok {
		t.Fatal("declared-v2 candidate intent carries no export-source-v1 projection")
	}
	if projection.Profile != ProfileExportSourceV1 {
		t.Fatalf("projection profile = %q, want %q", projection.Profile, ProfileExportSourceV1)
	}

	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatalf("ParseDescriptor: %v", err)
	}
	set, issues := BuildCarriedSet(ProfileContractSetV2, raw, descriptor, cloneCandidates(validCandidates()[:3]))
	assertNoIssues(t, issues)
	want, err := set.ExportSource()
	if err != nil {
		t.Fatalf("set.ExportSource(): %v", err)
	}
	if projection.Digest != want.Digest {
		t.Fatalf("carried export-source projection = %q, want %q", projection.Digest, want.Digest)
	}
}

// TestBuildCandidateIntentCarriesNoExportSourceProjectionForLegacy pins the
// other half: a legacy (non-declared-v2) candidate has no export-source-v1
// shape at all, so the intent carries none — the planner's own refusal for
// a legacy candidate asserting generated_from (AC-10) depends on this being
// false rather than a zero-value projection that could be mistaken for one.
func TestBuildCandidateIntentCarriesNoExportSourceProjectionForLegacy(t *testing.T) {
	t.Parallel()

	raw := declaredIntentDescriptor(`schema: envelope/v1
id: XC-atlas-demo
type: contract
title: Demo contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
`, "# Demo\n")
	intent, issues := BuildCandidateIntent(CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files:      cloneCandidates(validCandidates()[:3]),
	})
	assertNoIssues(t, issues)
	if intent.InventoryMode() != InventoryLegacyFixedV1 {
		t.Fatalf("inventory mode = %q", intent.InventoryMode())
	}
	if _, ok := intent.ExportSource(); ok {
		t.Fatal("legacy candidate intent carries an export-source-v1 projection, want none")
	}
}

// TestDetectInventoryModeMatchesBuildCandidateIntent pins criterion 9: the
// predicate deciding inventory mode from raw descriptor bytes has exactly
// one definition, shared by the publisher (BuildCandidateIntent, via the
// unexported inventoryModeFromSemantic) and DetectInventoryMode (the
// verifier's entry point, since verify-export's local-candidate check has
// no CandidateIntentSnapshot of its own to read a mode off). The
// present-but-EMPTY-list row is the one that would catch a caller using
// `len(Artifacts) > 0` instead of key presence — that predicate answers
// "declared-v2" as false here, which is wrong: an explicit `artifacts: []`
// is still a declared-v2 candidate (BuildCarriedSet's own
// IssueTooFewEntries is what actually catches the empty inventory, as a
// content defect rather than a mode question).
func TestDetectInventoryModeMatchesBuildCandidateIntent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  []byte
		want InventoryMode
	}{
		{
			name: "artifacts key absent (legacy)",
			raw: declaredIntentDescriptor(`schema: envelope/v1
id: XC-atlas-demo
type: contract
title: Demo contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
`, "# Demo\n"),
			want: InventoryLegacyFixedV1,
		},
		{
			name: "artifacts key present and populated (declared-v2)",
			raw: declaredIntentDescriptor(`schema: envelope/v2
id: XC-atlas-demo
type: contract
title: Demo contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/missing-id.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`, "# Demo\n"),
			want: InventoryDeclaredV2,
		},
		{
			name: "artifacts key present but an empty list (still declared-v2)",
			raw: declaredIntentDescriptor(`schema: envelope/v2
id: XC-atlas-demo
type: contract
title: Demo contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
artifacts: []
`, "# Demo\n"),
			want: InventoryDeclaredV2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := DetectInventoryMode(test.raw)
			if err != nil {
				t.Fatalf("DetectInventoryMode: %v", err)
			}
			if got != test.want {
				t.Fatalf("DetectInventoryMode() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildCandidateIntentIncludesOrderBodyFieldsAndBytes(t *testing.T) {
	t.Parallel()

	baseRaw := declaredIntentDescriptor(`schema: envelope/v2
id: XC-atlas-demo
type: contract
title: Demo contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
artifacts:
  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}
  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
  - {path: fixtures/invalid/missing-id.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}
`, "# Demo\n")
	baseSnapshot := CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: baseRaw},
		Files:      validCandidates()[:3],
	}
	base, issues := BuildCandidateIntent(baseSnapshot)
	assertNoIssues(t, issues)

	tests := []struct {
		name   string
		mutate func(*CandidateSnapshot)
	}{
		{
			name: "descriptor semantic field",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Descriptor.Raw = bytes.Replace(snapshot.Descriptor.Raw, []byte("title: Demo contract"), []byte("title: Changed contract"), 1)
			},
		},
		{
			name: "Markdown body",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Descriptor.Raw = bytes.Replace(snapshot.Descriptor.Raw, []byte("# Demo"), []byte("# Changed"), 1)
			},
		},
		{
			name: "entry order",
			mutate: func(snapshot *CandidateSnapshot) {
				first := []byte("  - {path: schema/order.schema.json, role: schema, normative: true, media_type: application/schema+json}\n")
				second := []byte("  - {path: fixtures/valid/order.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/order.schema.json}\n")
				raw := bytes.Replace(snapshot.Descriptor.Raw, first, []byte("__FIRST__\n"), 1)
				raw = bytes.Replace(raw, second, first, 1)
				snapshot.Descriptor.Raw = bytes.Replace(raw, []byte("__FIRST__\n"), second, 1)
			},
		},
		{
			name: "entry bytes",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Files[0].Raw = append(snapshot.Files[0].Raw, '\n')
			},
		},
		{
			name: "entry role",
			mutate: func(snapshot *CandidateSnapshot) {
				snapshot.Descriptor.Raw = bytes.Replace(snapshot.Descriptor.Raw, []byte("role: schema"), []byte("role: other"), 1)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changedSnapshot := cloneCandidateSnapshot(baseSnapshot)
			test.mutate(&changedSnapshot)
			changed, changedIssues := BuildCandidateIntent(changedSnapshot)
			if test.name == "entry role" {
				if len(changedIssues) == 0 {
					t.Fatal("invalid role/root mutation was accepted")
				}
				return
			}
			assertNoIssues(t, changedIssues)
			if changed.Digest() == base.Digest() {
				t.Fatalf("changed %s collided with candidate intent %q", test.name, base.Digest())
			}
		})
	}
}

func TestBuildCandidateIntentCarriesExplicitFalseAndEmptyDeclaredFields(t *testing.T) {
	t.Parallel()

	snapshot := plannerDeclaredSnapshot("XC-atlas-demo", "1.0.0", "json-schema-2020-12", nil)
	snapshot.Files = append(snapshot.Files, regular("artifacts/changelog.md", "notes\n"))
	snapshot.Descriptor.Raw = bytes.Replace(snapshot.Descriptor.Raw,
		[]byte("artifacts:\n"),
		[]byte("artifacts:\n  - {path: artifacts/changelog.md, role: changelog, normative: false, media_type: text/markdown}\n"), 1)
	intent, issues := BuildCandidateIntent(snapshot)
	assertNoIssues(t, issues)
	canonical := intent.CanonicalBytes()
	for _, want := range [][]byte{
		[]byte(`"role":"changelog"`),
		[]byte(`"normative":false`),
		[]byte(`"conforms_to":""`),
	} {
		if !bytes.Contains(canonical, want) {
			t.Fatalf("declared candidate record omitted %s: %s", want, canonical)
		}
	}
}

func TestBuildCandidateIntentLegacyGoldenAndSourceOrderIndependence(t *testing.T) {
	t.Parallel()

	raw := declaredIntentDescriptor(`schema: envelope/v1
id: XC-atlas-legacy
type: contract
title: Legacy contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
`, "# Legacy\n")
	files := []CandidateFile{
		regular("fixtures/valid/a.json", `{}`),
		regular("schema/a.json", `{}`),
		regular("fixtures/invalid/a.json", `{}`),
		regular("artifacts/not-carried.txt", "ignored by legacy intent"),
	}
	first, issues := BuildCandidateIntent(CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files:      files,
	})
	assertNoIssues(t, issues)
	second, issues := BuildCandidateIntent(CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files:      []CandidateFile{files[2], files[1], files[0], files[3]},
	})
	assertNoIssues(t, issues)

	if first.InventoryMode() != InventoryLegacyFixedV1 || first.Digest() != second.Digest() {
		t.Fatalf("legacy candidate is not canonical: mode=%q digests=%q/%q", first.InventoryMode(), first.Digest(), second.Digest())
	}
	const want = "sha256:36eba2e0f96e9edcde5c20bde04612fe3569b0f86fb5838ae706bf90b4361d61"
	if first.Digest() != want {
		t.Fatalf("legacy candidate intent digest = %q, want golden %q\ncanonical=%s", first.Digest(), want, first.CanonicalBytes())
	}
}

func TestCandidateIntentSnapshotHasBidirectionalMutationIndependence(t *testing.T) {
	t.Parallel()

	raw := declaredIntentDescriptor(`schema: envelope/v1
id: XC-atlas-legacy
type: contract
title: Legacy contract
version: 1.0.0
schema_format: json-schema-2020-12
compat_policy: default
`, "# Legacy\n")
	input := CandidateSnapshot{
		Descriptor: CandidateFile{Path: DescriptorPath, Kind: CandidateRegular, Raw: raw},
		Files: []CandidateFile{
			regular("schema/a.json", `{}`),
			regular("fixtures/valid/a.json", `{}`),
			regular("fixtures/invalid/a.json", `{}`),
		},
	}
	intent, issues := BuildCandidateIntent(input)
	assertNoIssues(t, issues)
	wantDigest := intent.Digest()
	wantCanonical := intent.CanonicalBytes()

	input.Descriptor.Raw[0] = 'x'
	input.Files[0].Raw[0] = 'x'
	returned := intent.Snapshot()
	returned.Descriptor.Raw[0] = 'x'
	returned.Files[0].Raw[0] = 'x'
	returned.Files = append(returned.Files, regular("schema/extra.json", `{}`))
	canonical := intent.CanonicalBytes()
	canonical[0] = 'x'

	if intent.Digest() != wantDigest || !bytes.Equal(intent.CanonicalBytes(), wantCanonical) {
		t.Fatal("caller mutation changed frozen candidate intent")
	}
	fresh := intent.Snapshot()
	if fresh.Descriptor.Raw[0] != '-' || fresh.Files[0].Raw[0] != '{' || len(fresh.Files) != 3 {
		t.Fatalf("returned snapshot mutation leaked back into intent: %+v", fresh)
	}
}

func declaredIntentDescriptor(frontmatter, body string) []byte {
	return []byte("---\n" + frontmatter + "---\n" + body)
}
