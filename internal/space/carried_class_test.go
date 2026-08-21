package space

import (
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

// v2Descriptor renders an envelope/v2 contract descriptor whose `artifacts:`
// inventory declares the schema/fixture trio plus a companion changelog —
// the exact shape fb-20260820-d1e370 was filed against.
func v2Descriptor(extra ...string) []byte {
	body := "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-axon-content-feed\n" +
		"type: contract\n" +
		"schema_format: json-schema-2020-12\n" +
		"artifacts:\n" +
		"  - {path: schema/content-feed.schema.json, role: schema, normative: true, media_type: application/schema+json}\n" +
		"  - {path: fixtures/valid/ok.json, role: valid-fixture, normative: true, media_type: application/json, conforms_to: schema/content-feed.schema.json}\n" +
		"  - {path: fixtures/invalid/bad.json, role: invalid-fixture, normative: true, media_type: application/json, conforms_to: schema/content-feed.schema.json}\n" +
		"  - {path: artifacts/CHANGELOG.md, role: changelog, normative: false, media_type: text/markdown}\n"
	for _, line := range extra {
		body += line + "\n"
	}
	return []byte(body + "---\nBody.\n")
}

// v1Descriptor is the legacy floor: an envelope/v1 descriptor carries no
// `artifacts:` inventory at all, so the classifier has nothing to consult
// and must fall back to the path shape IsContractBaselinePath computes.
func v1Descriptor() []byte {
	return []byte("---\n" +
		"schema: envelope/v1\n" +
		"id: XC-axon-content-feed\n" +
		"type: contract\n" +
		"schema_format: json-schema-2020-12\n" +
		"---\nBody.\n")
}

func TestClassifyCarriedFiveClasses(t *testing.T) {
	t.Parallel()
	const dir = "axon/provides/content-feed/"
	cases := []struct {
		name       string
		path       string
		descriptor []byte
		want       CarriedClass
	}{
		{"descriptor itself is an artifact", dir + "contract.md", v2Descriptor(), CarriedArtifact},
		{"declared companion changelog", dir + "artifacts/CHANGELOG.md", v2Descriptor(), CarriedDeclaredCompanion},
		{"declared schema", dir + "schema/content-feed.schema.json", v2Descriptor(), CarriedDeclaredCompanion},
		{"declared valid fixture", dir + "fixtures/valid/ok.json", v2Descriptor(), CarriedDeclaredCompanion},
		{"undeclared companion", dir + "artifacts/NOTES.md", v2Descriptor(), CarriedUndeclaredCompanion},
		{"undeclared schema file", dir + "schema/extra.schema.json", v2Descriptor(), CarriedUndeclaredCompanion},
		{"descriptor absent", dir + "artifacts/CHANGELOG.md", nil, CarriedUnclassifiable},
		{"descriptor unparseable", dir + "artifacts/CHANGELOG.md", []byte("not frontmatter at all\n"), CarriedUnclassifiable},
		{"legacy v1 descriptor falls back to the path shape", dir + "artifacts/CHANGELOG.md", v1Descriptor(), CarriedDeclaredCompanion},
		{"legacy v1 descriptor, schema file", dir + "schema/main.schema.json", v1Descriptor(), CarriedDeclaredCompanion},
		{"an exchange is not a contract path", "axon/exchanges/XQ-axon-20260730-h2k8.md", v2Descriptor(), CarriedNotAContractPath},
		{"a data package payload is not a contract path", "axon/data/DP-axon-x/manifest.json", v2Descriptor(), CarriedNotAContractPath},
		{"docs under the section are not a contract path", "axon/docs/readme.md", v2Descriptor(), CarriedNotAContractPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyCarried(tc.path, tc.descriptor)
			if got.Class != tc.want {
				t.Fatalf("ClassifyCarried(%q).Class = %q, want %q", tc.path, got.Class, tc.want)
			}
		})
	}
}

// TestClassifyCarriedDoesNotReadmitTraversal proves the wrapper adds no hole
// ContractForPath already closed: a `..`, `.` or empty segment is refused
// there, and a classifier that reconstructed the path itself could quietly
// re-admit it.
func TestClassifyCarriedDoesNotReadmitTraversal(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		"axon/provides/content-feed/artifacts/../../../etc/passwd",
		"axon/provides/../provides/content-feed/artifacts/CHANGELOG.md",
		"axon//provides/content-feed/artifacts/CHANGELOG.md",
		"axon/provides/content-feed/./artifacts/CHANGELOG.md",
	} {
		got := ClassifyCarried(p, v2Descriptor())
		if got.Class != CarriedNotAContractPath {
			t.Fatalf("ClassifyCarried(%q).Class = %q, want %q — ContractForPath already refuses this shape", p, got.Class, CarriedNotAContractPath)
		}
	}
}

// TestClassifyCarriedCarriesTheEntry proves a declared companion's answer
// carries the inventory entry that classified it, so a caller can report the
// role without a second descriptor read.
func TestClassifyCarriedCarriesTheEntry(t *testing.T) {
	t.Parallel()
	got := ClassifyCarried("axon/provides/content-feed/artifacts/CHANGELOG.md", v2Descriptor())
	if got.Entry.Role != contract.RoleChangelog {
		t.Fatalf("Entry.Role = %q, want %q", got.Entry.Role, contract.RoleChangelog)
	}
	if got.Entry.MediaType != "text/markdown" {
		t.Fatalf("Entry.MediaType = %q, want text/markdown", got.Entry.MediaType)
	}
	if got.ContractID != "XC-axon-content-feed" {
		t.Fatalf("ContractID = %q", got.ContractID)
	}
	if got.DescriptorPath != "axon/provides/content-feed/contract.md" {
		t.Fatalf("DescriptorPath = %q", got.DescriptorPath)
	}
}

// TestClassifyCarriedNoDescriptorForAnotherSlug proves a companion under a
// slug whose descriptor was never supplied is unclassifiable rather than
// silently declared.
func TestClassifyCarriedNoDescriptorForAnotherSlug(t *testing.T) {
	t.Parallel()
	got := ClassifyCarried("axon/provides/other-feed/artifacts/CHANGELOG.md", nil)
	if got.Class != CarriedUnclassifiable {
		t.Fatalf("Class = %q, want %q", got.Class, CarriedUnclassifiable)
	}
	if got.DescriptorPath != "axon/provides/other-feed/contract.md" {
		t.Fatalf("DescriptorPath = %q — the caller needs to be told WHICH descriptor is missing", got.DescriptorPath)
	}
}

// --- membership, both directions (S-3 / S-4) --------------------------

func companionBatch(t *testing.T, extra map[string]string, drop ...string) []FileWrite {
	t.Helper()
	const dir = "axon/provides/content-feed/"
	files := map[string]string{
		dir + "contract.md":                          string(v2Descriptor()),
		dir + "schema/content-feed.schema.json":      `{"type":"object"}`,
		dir + "fixtures/valid/ok.json":               `{}`,
		dir + "fixtures/invalid/bad.json":            `null`,
		dir + "artifacts/CHANGELOG.md":               "# Changelog\n",
		"axon/events/2026/01JQ0000000000000000.yaml": "schema: event/v1\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	for _, d := range drop {
		delete(files, d)
	}
	out := make([]FileWrite, 0, len(files))
	for p, c := range files {
		out = append(out, FileWrite{Path: p, Content: []byte(c)})
	}
	return out
}

func TestContractCarriedMembershipCleanBatchIsSilent(t *testing.T) {
	t.Parallel()
	if got := ContractCarriedMembership(companionBatch(t, nil)); len(got) != 0 {
		t.Fatalf("a batch whose carried set equals its inventory must produce no violation, got %+v", got)
	}
}

func TestContractCarriedMembershipRefusesUndeclaredFile(t *testing.T) {
	t.Parallel()
	got := ContractCarriedMembership(companionBatch(t, map[string]string{
		"axon/provides/content-feed/artifacts/NOTES.md": "scratch\n",
	}))
	if len(got) != 1 {
		t.Fatalf("want exactly one violation, got %+v", got)
	}
	v := got[0]
	if v.Code != "POL-013" || v.Severity != "reject" {
		t.Fatalf("want POL-013/reject, got %+v", v)
	}
	for _, want := range []string{"artifacts/NOTES.md", "axon/provides/content-feed/contract.md", "role"} {
		if !strings.Contains(v.Message+" "+v.Path, want) {
			t.Fatalf("refusal must name the path, the descriptor and the role vocabulary; %q missing from %+v", want, v)
		}
	}
}

func TestContractCarriedMembershipRefusesDeclaredButAbsentFile(t *testing.T) {
	t.Parallel()
	got := ContractCarriedMembership(companionBatch(t, nil, "axon/provides/content-feed/artifacts/CHANGELOG.md"))
	if len(got) != 1 {
		t.Fatalf("want exactly one violation, got %+v", got)
	}
	v := got[0]
	if v.Code != "REF-014" || v.Severity != "reject" {
		t.Fatalf("want REF-014/reject, got %+v", v)
	}
	if !strings.Contains(v.Message, "artifacts/CHANGELOG.md") {
		t.Fatalf("refusal must name the inventory entry, got %+v", v)
	}
}

// TestContractCarriedMembershipSilentWithoutADescriptor proves the
// unclassifiable case adds NO second red line: one broken descriptor is one
// refusal, and the descriptor's own violation is the verdict.
func TestContractCarriedMembershipSilentWithoutADescriptor(t *testing.T) {
	t.Parallel()
	got := ContractCarriedMembership([]FileWrite{
		{Path: "axon/provides/content-feed/artifacts/CHANGELOG.md", Content: []byte("# Changelog\n")},
	})
	if len(got) != 0 {
		t.Fatalf("a companion whose descriptor is not in the batch gets no membership verdict, got %+v", got)
	}
}

// TestContractCarriedMembershipLegacyDescriptorIsSilent proves an
// envelope/v1 contract — no inventory to compare against — keeps exactly
// today's behaviour rather than being refused by a rule its grammar predates.
func TestContractCarriedMembershipLegacyDescriptorIsSilent(t *testing.T) {
	t.Parallel()
	got := ContractCarriedMembership([]FileWrite{
		{Path: "axon/provides/content-feed/contract.md", Content: v1Descriptor()},
		{Path: "axon/provides/content-feed/schema/main.schema.json", Content: []byte(`{}`)},
		{Path: "axon/provides/content-feed/artifacts/CHANGELOG.md", Content: []byte("# Changelog\n")},
	})
	if len(got) != 0 {
		t.Fatalf("an envelope/v1 contract has no inventory; membership cannot be judged, got %+v", got)
	}
}

// TestContractCarriedMembershipIsDeterministic proves the violation order
// does not depend on the batch's own file order — a report a human diffs
// must not shuffle.
func TestContractCarriedMembershipIsDeterministic(t *testing.T) {
	t.Parallel()
	batch := companionBatch(t, map[string]string{
		"axon/provides/content-feed/artifacts/NOTES.md": "scratch\n",
		"axon/provides/content-feed/artifacts/TODO.md":  "scratch\n",
	}, "axon/provides/content-feed/fixtures/valid/ok.json")
	first := ContractCarriedMembership(batch)
	for i := 0; i < 8; i++ {
		batch = append(batch[1:], batch[0])
		got := ContractCarriedMembership(batch)
		if len(got) != len(first) {
			t.Fatalf("violation count changed with input order: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j].Code != first[j].Code || got[j].Path != first[j].Path {
				t.Fatalf("violation order changed with input order: %+v vs %+v", got, first)
			}
		}
	}
}
