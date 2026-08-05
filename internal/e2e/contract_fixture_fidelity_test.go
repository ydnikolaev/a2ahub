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

// TestContractFixtureFidelity guards writeContractDescriptorFor
// (helpers_test.go) — the hand-rolled contract descriptor ~16 call sites
// across this package depend on — against silently diverging from what the
// product actually renders (schemas/templates/v1/contract.md, the template
// `a2a new` scaffolds a real contract from).
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
func TestContractFixtureFidelity(t *testing.T) {
	t.Parallel()

	t.Run("ValidatesAsContractEnvelope", testContractFixtureValidatesAsEnvelope)
	t.Run("FrontmatterKeysMatchTemplate", testContractFixtureFrontmatterKeysMatchTemplate)
}

// testContractFixtureValidatesAsEnvelope asserts writeContractDescriptorFor's
// output passes the same schema-class check (V1/ValidateDraft) the real
// authoring path runs, against the SAME compiled corpus every other test in
// this package uses (contract_write_test.go's own `schema.Load` +
// `validate.New` precedent).
func testContractFixtureValidatesAsEnvelope(t *testing.T) {
	t.Parallel()

	mirrorDir := t.TempDir()
	writeContractDescriptorFor(t, mirrorDir, "axon", "widget", "1.0.0")

	path := filepath.Join(mirrorDir, "axon", "provides", "widget", "contract.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	engine := validate.New(corpus)

	result, err := engine.ValidateDraft(validate.Draft{
		Path: "axon/provides/widget/contract.md",
		Raw:  raw,
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	if !result.Valid {
		t.Fatalf("writeContractDescriptorFor's own output does not validate as a contract envelope "+
			"(V1/schema class) against the canonical validator — the fixture has drifted from what "+
			"the product accepts:\n%+v", result.Violations)
	}
}

// testContractFixtureFrontmatterKeysMatchTemplate asserts the fixture's
// frontmatter key set is not missing any key the shipped v1 template
// (`schemas/templates/v1/contract.md`, what `a2a contract new` scaffolds)
// declares. A template key absent from the fixture is drift the offline
// suite would never see, because nothing in this package renders through
// the template — see repoRootForTest (e2e4_test.go) for the repo-root
// resolution precedent this reuses.
func testContractFixtureFrontmatterKeysMatchTemplate(t *testing.T) {
	t.Parallel()

	mirrorDir := t.TempDir()
	writeContractDescriptorFor(t, mirrorDir, "axon", "widget", "1.0.0")

	fixturePath := filepath.Join(mirrorDir, "axon", "provides", "widget", "contract.md")
	fixtureRaw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	fixtureKeys, err := frontmatterKeySet(fixtureRaw)
	if err != nil {
		t.Fatalf("parse fixture frontmatter: %v", err)
	}

	root := repoRootForTest(t)
	templatePath := filepath.Join(root, "schemas", "templates", "v1", "contract.md")
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
		t.Fatalf("writeContractDescriptorFor's fixture is missing frontmatter key(s) that "+
			"schemas/templates/v1/contract.md declares: %s — the fixture has drifted from the "+
			"template it stands in for; add the missing key(s) to writeContractDescriptorFor "+
			"(helpers_test.go)", strings.Join(missing, ", "))
	}
}

// frontmatterKeySet splits raw into its YAML frontmatter block
// (internal/artifact.ParseFrontmatter — the same structural split every
// other frontmatter-reading path in this repo uses) and returns the set of
// its top-level keys. Commented-out optional keys (e.g. the template's
// `# generated_from:`) are YAML comments, not keys, and correctly do not
// appear.
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
