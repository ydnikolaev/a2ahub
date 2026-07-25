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
