// P37 wave C2 (spec 37 §2 T1, decision D-D): `a2a contract new` scaffolds a
// starter JSON Schema + matching valid fixture alongside a freshly-drafted
// contract.md, so a JSON-Schema contract is publishable (validate.
// CheckContractPublishable, POL-009) and §5.4b's computed compatibility
// check has an actual baseline to compute against the moment the contract
// exists, rather than opting in later. This file extends the package's
// existing embed-and-fill mechanism (Render's own template.Show/rawTemplate
// idiom) instead of inlining the scaffold's JSON as Go string literals in
// cmd_new.go — one rendering seam, not two.
package template

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

//go:embed scaffold/contract.schema.json
var contractScaffoldSchemaRaw []byte

//go:embed scaffold/contract.fixture.json
var contractScaffoldFixtureRaw []byte

// contractScaffoldSlugToken is the literal placeholder ContractScaffold
// fills with the contract's own slug. The embedded scaffold files are
// plain JSON (not YAML frontmatter), so this is a simple substring fill
// rather than Render's yaml.Node walk — the same "<...>" placeholder
// convention this package's canonical templates already use, applied to a
// document shape Render's own machinery does not parse.
const contractScaffoldSlugToken = "<slug>"

// ContractScaffold renders decision D-D's starter JSON Schema and matching
// valid fixture for a contract named slug: a minimal but real 2020-12
// schema with no `$ref` outside the document (so it compiles under
// internal/schema.CompileExternal's refusing loader, which treats any
// unresolved reference as a hard compile error by design), and one
// instance that validates against it.
//
// This package has no opinion on WHETHER a caller should write these
// files — that decision (schema_format is a JSON-Schema dialect, per
// validate.IsJSONSchemaFormat) belongs to the caller (cmd_new.go); this
// package never imports internal/validate (§9 "Coupling: soft") and only
// fills and returns bytes.
//
// D-E's fixture->schema mapping matches by base name
// (fixtures/valid/<stem>.json -> schema/<stem>.schema.json); callers are
// expected to name both returned files after slug so the mapping is
// unambiguous the moment the contract publishes.
func ContractScaffold(slug string) (schema []byte, fixture []byte) {
	token := []byte(contractScaffoldSlugToken)
	schema = bytes.ReplaceAll(contractScaffoldSchemaRaw, token, []byte(slug))
	fixture = bytes.ReplaceAll(contractScaffoldFixtureRaw, token, []byte(slug))
	return schema, fixture
}

// ScaffoldContractInStaging writes decision D-D's starter schema and valid
// fixture for slug under stagingDir, at the SAME relative shape
// internal/space.Layout will place them at once submitted — so the author
// edits the paths a later publish actually looks for, and
// internal/cli/cmd_submit.go's sidecar carry finds them where it expects.
// D-E's fixture->schema stem mapping is satisfied by naming both files
// after slug.
//
// It lives here, beside ContractScaffold, because BOTH surfaces need it:
// `a2a contract new` and the MCP a2a_new tool. internal/mcp may never
// import internal/cli (P14's proven invariant), so a helper left in the
// CLI would have meant a second copy in the MCP handler — and the parity
// suite exists precisely to make that impossible. It caught this: after
// wave C2 shipped the scaffold CLI-side only, TestEquivContractNew went
// red on a real capability asymmetry, not a stale fixture.
//
// Never overwrites: an existing file at either path is left exactly as it
// is, so a re-run cannot clobber an author's own schema or fixture.
// Returns the paths it actually wrote, in order.
func ScaffoldContractInStaging(stagingDir, ownSystem, slug string, writeFile func(string, []byte, os.FileMode) error) ([]string, error) {
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		return nil, err
	}

	schemaBytes, fixtureBytes := ContractScaffold(slug)
	candidates := []struct {
		path string
		data []byte
	}{
		{filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesSchemaDir(slug)), slug+".schema.json"), schemaBytes},
		{filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesValidDir(slug)), slug+".json"), fixtureBytes},
	}

	var written []string
	for _, cand := range candidates {
		if _, statErr := os.Stat(cand.path); statErr == nil {
			continue // D-D: never overwrite an existing scaffold or the author's own edit
		} else if !os.IsNotExist(statErr) {
			return written, statErr
		}
		if err := os.MkdirAll(filepath.Dir(cand.path), 0o755); err != nil {
			return written, err
		}
		if err := writeFile(cand.path, cand.data, 0o644); err != nil {
			return written, err
		}
		written = append(written, cand.path)
	}
	return written, nil
}

// ContractDraftSchemaFormat decodes schema_format from a just-rendered
// contract draft's OWN frontmatter — what Render produced, never the
// template's literal default — so it reflects whatever `schema_format`
// override actually landed. Shared by both surfaces for the same reason
// ScaffoldContractInStaging is.
func ContractDraftSchemaFormat(draft []byte) (string, error) {
	fm, err := artifact.ParseFrontmatter(draft)
	if err != nil {
		return "", err
	}
	var probe struct {
		SchemaFormat string `yaml:"schema_format"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return "", err
	}
	return probe.SchemaFormat, nil
}
