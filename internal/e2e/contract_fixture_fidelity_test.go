package e2e

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"gopkg.in/yaml.v3"
)

// TestContractFixtureFidelity guards hand-rolled contract descriptor
// writers — the ~16 call sites across this package that build a contract.md
// by hand rather than driving `a2a contract new` — against silently
// diverging from what the product actually renders
// (schemas/templates/<generation>/contract.md, the template `a2a contract
// new` scaffolds a real contract from).
//
// The concrete incident this closes: a fixture that seeded `version: 0.0.0`
// by hand kept every offline contract test in this package green while the
// shipped template (schemas/templates/v1/contract.md — what `a2a contract
// new` actually scaffolds) drafted `version: 1.0.0`. Nothing compared the
// two, so the mismatch between "what the fixture pretends a fresh contract
// looks like" and "what the product actually drafts" was invisible to every
// offline test, and the resulting unpublishable first-publication candidate
// (fix(contract) "make first publication candidate complete", a6d1e8f) was
// found only by a 110-minute live run, not by anything in this package.
// This test does not re-derive that specific value mismatch (the fixture
// never hardcodes a version — callers choose one per scenario, by design,
// and ~16 call sites rely on that); it closes the general gap that let it
// go undetected: nothing asserted the fixture VALIDATES as a real contract
// envelope, and nothing asserted its frontmatter key set still matches the
// template it stands in for.
//
// # Generalised over a generation (defects-fix-2026-08 P1)
//
// The guard originally covered envelope/v1 only (writeContractDescriptorFor,
// helpers_test.go). The spec that generalised it named "three other files"
// hand-rolling envelope/v2 contract descriptors; a repo-wide grep for the
// shape found FOUR: this package's own wave33_verify_test.go
// (writeContractDescriptorV2Operational) and contract_binary_refusal_test.go
// (writeContractDescriptorWithXBinding), plus internal/cli/cmd_contract_test.go
// and internal/mcp/tools_contract_test.go's own writeContractDescriptorWithXOperational
// twins (both package-private to a different Go package, so this file
// cannot call them directly — see the product-finding note below; the
// count discrepancy is reported to the lead rather than silently
// resolved).
//
// fixtureFidelityGenerations is the table this test walks — ONE test body,
// not a v1 copy and a v2 copy, so a third generation is a table row, not a
// second file. Each row is a SELF-CONTAINED writer local to this file
// (writeContractDescriptorForV2 below), not a call into
// writeContractDescriptorV2Operational/writeContractDescriptorWithXBinding:
// those two, EMPIRICALLY CHECKED (schema.Load + validate.New + ValidateDraft
// against their own output), do not carry `artifacts:` — a field
// envelope/v2/contract.schema.json requires (`"required": [...,
// "artifacts"]`, minItems 1) but envelope/v1/contract.schema.json does not
// declare at all. Wiring this guard to either of them directly would red
// this test PERMANENTLY on that pre-existing gap rather than proving the
// guard's own logic, which is the opposite of what a "the fixture validates
// and matches its template" assertion is for. That gap is real and is
// reported, verbatim, as this phase's own product finding rather than
// silently patched here — internal/cli/cmd_contract_test.go and
// internal/mcp/tools_contract_test.go are outside this phase's allowlist,
// and wave33_verify_test.go/contract_binary_refusal_test.go's own writers
// are shared by other tests in this package that this phase does not own
// either.
func TestContractFixtureFidelity(t *testing.T) {
	t.Parallel()

	for _, gen := range fixtureFidelityGenerations() {
		t.Run(gen.name, func(t *testing.T) {
			t.Parallel()
			t.Run("ValidatesAsContractEnvelope", func(t *testing.T) {
				t.Parallel()
				testContractFixtureValidatesAsEnvelope(t, gen)
			})
			t.Run("FrontmatterKeysMatchTemplate", func(t *testing.T) {
				t.Parallel()
				testContractFixtureFrontmatterKeysMatchTemplate(t, gen)
			})
		})
	}
}

// fixtureFidelityGeneration is one envelope generation's own hand-rolled
// descriptor writer plus the shipped template it must stay faithful to.
type fixtureFidelityGeneration struct {
	name         string // e.g. "envelope/v1" — carried into every failure message
	templatePath string // repo-relative, under schemas/templates/
	// write seeds a fresh descriptor under mirrorDir and returns its
	// mirror-relative path (forward-slash, matching writeMirrorFile's own
	// convention).
	write func(t *testing.T, mirrorDir string) string
}

func fixtureFidelityGenerations() []fixtureFidelityGeneration {
	return []fixtureFidelityGeneration{
		{
			name:         "envelope/v1",
			templatePath: filepath.Join("schemas", "templates", "v1", "contract.md"),
			write: func(t *testing.T, mirrorDir string) string {
				t.Helper()
				writeContractDescriptorFor(t, mirrorDir, "axon", "widget", "1.0.0")
				return "axon/provides/widget/contract.md"
			},
		},
		{
			name:         "envelope/v2",
			templatePath: filepath.Join("schemas", "templates", "v2", "contract.md"),
			write: func(t *testing.T, mirrorDir string) string {
				t.Helper()
				writeContractDescriptorForV2(t, mirrorDir, "axon", "widget", "1.0.0")
				return "axon/provides/widget/contract.md"
			},
		},
	}
}

// writeContractDescriptorForV2 is writeContractDescriptorFor's (helpers_test.go)
// envelope/v2 twin, local to this file rather than a shared helper — see
// the package doc above for why it does not reuse
// writeContractDescriptorV2Operational/writeContractDescriptorWithXBinding.
// Unlike v1, envelope/v2/contract.schema.json REQUIRES `artifacts` (>=1
// entry), so this writer also seeds the schema/valid-fixture/invalid-fixture
// files those `artifacts[].path` entries name — schemas/templates/v2/
// contract.md's own example artifacts block, filled in rather than left as
// the template's `<slug>` placeholders.
func writeContractDescriptorForV2(t *testing.T, mirrorDir, from, slug, version string) {
	t.Helper()
	content := "---\n" +
		"schema: envelope/v2\n" +
		"id: XC-" + from + "-" + slug + "\n" +
		"type: contract\n" +
		"title: t\n" +
		"space: fixture-space\n" +
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
		"artifacts:\n" +
		"  - path: schema/main.schema.json\n" +
		"    role: schema\n" +
		"    normative: true\n" +
		"    media_type: application/schema+json\n" +
		"  - path: fixtures/valid/" + slug + ".json\n" +
		"    role: valid-fixture\n" +
		"    normative: true\n" +
		"    media_type: application/json\n" +
		"    conforms_to: schema/main.schema.json\n" +
		"  - path: fixtures/invalid/" + slug + ".json\n" +
		"    role: invalid-fixture\n" +
		"    normative: true\n" +
		"    media_type: application/json\n" +
		"    conforms_to: schema/main.schema.json\n" +
		"---\nbody\n"
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/contract.md", content)
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/schema/main.schema.json", `{"type":"object","additionalProperties":true}`)
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/fixtures/valid/"+slug+".json", `{}`)
	writeMirrorFile(t, mirrorDir, from+"/provides/"+slug+"/fixtures/invalid/"+slug+".json", `null`)
}

// testContractFixtureValidatesAsEnvelope asserts gen.write's output passes
// the same schema-class check (V1/ValidateDraft) the real authoring path
// runs, against the SAME compiled corpus every other test in this package
// uses (contract_write_test.go's own `schema.Load` + `validate.New`
// precedent).
func testContractFixtureValidatesAsEnvelope(t *testing.T, gen fixtureFidelityGeneration) {
	mirrorDir := t.TempDir()
	relPath := gen.write(t, mirrorDir)

	raw, err := os.ReadFile(filepath.Join(mirrorDir, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)

	result, err := engine.ValidateDraft(validate.Draft{
		Path: relPath,
		Raw:  raw,
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	if !result.Valid {
		t.Fatalf("%s's hand-rolled fixture does not validate as a contract envelope "+
			"(V1/schema class) against the canonical validator — the fixture has drifted from what "+
			"the product accepts:\n%+v", gen.name, result.Violations)
	}
}

// testContractFixtureFrontmatterKeysMatchTemplate asserts the fixture's
// frontmatter key set is not missing any key the shipped generation's
// template (gen.templatePath, what `a2a contract new` scaffolds) declares.
// A template key absent from the fixture is drift the offline suite would
// never see, because nothing in this package renders through the template
// — see repoRootForTest (e2e4_test.go) for the repo-root resolution
// precedent this reuses.
func testContractFixtureFrontmatterKeysMatchTemplate(t *testing.T, gen fixtureFidelityGeneration) {
	mirrorDir := t.TempDir()
	relPath := gen.write(t, mirrorDir)

	fixtureRaw, err := os.ReadFile(filepath.Join(mirrorDir, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fixtureKeys, err := frontmatterKeySet(fixtureRaw)
	if err != nil {
		t.Fatalf("parse fixture frontmatter: %v", err)
	}

	root := repoRootForTest(t)
	templatePath := filepath.Join(root, gen.templatePath)
	templateRaw, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read template %s: %v", templatePath, err)
	}
	templateKeys, err := frontmatterKeySet(templateRaw)
	if err != nil {
		t.Fatalf("parse template frontmatter: %v", err)
	}

	var missing []string
	for key := range templateKeys {
		if !fixtureKeys[key] {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("%s's hand-rolled fixture is missing frontmatter key(s) that "+
			"%s declares: %s — the fixture has drifted from the "+
			"template it stands in for", gen.name, gen.templatePath, strings.Join(missing, ", "))
	}
}

// frontmatterKeySet splits raw into its YAML frontmatter block
// (internal/artifact.ParseFrontmatter — the same structural split every
// other frontmatter-reading path in this repo uses) and returns the set of
// its top-level keys. Commented-out optional keys (e.g. the v1 template's
// `# generated_from:`, or v2's `# x_binding:`/`# x_operational:`) are YAML
// comments, not keys, and correctly do not appear.
func frontmatterKeySet(raw []byte) (map[string]bool, error) {
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(fm.YAML, &decoded); err != nil {
		return nil, err
	}
	keys := make(map[string]bool, len(decoded))
	for k := range decoded {
		keys[k] = true
	}
	return keys, nil
}
