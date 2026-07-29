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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

//go:embed scaffold/contract.schema.json
var contractScaffoldSchemaRaw []byte

//go:embed scaffold/contract.fixture.json
var contractScaffoldFixtureRaw []byte

//go:embed scaffold/contract.fixture.invalid.json
var contractScaffoldInvalidFixtureRaw []byte

// contractScaffoldSlugToken is the literal placeholder ContractScaffold
// fills with the contract's own slug. The embedded scaffold files are
// plain JSON (not YAML frontmatter), so this is a simple substring fill
// rather than Render's yaml.Node walk — the same "<...>" placeholder
// convention this package's canonical templates already use, applied to a
// document shape Render's own machinery does not parse.
const contractScaffoldSlugToken = "<slug>"

// ContractScaffold renders decision D-D's starter JSON Schema and matching
// valid and invalid fixtures for a contract named slug: a minimal but real 2020-12
// schema with no `$ref` outside the document (so it compiles under
// internal/schema.CompileExternal's refusing loader, which treats any
// unresolved reference as a hard compile error by design), one instance
// that validates against it, and one that it rejects.
//
// This package has no opinion on WHETHER a caller should write these
// files — that decision (schema_format is a JSON-Schema dialect, per
// validate.IsJSONSchemaFormat) belongs to the caller (cmd_new.go); this
// package never imports internal/validate (§9 "Coupling: soft") and only
// fills and returns bytes.
//
// D-E's valid-fixture->schema mapping matches by base name
// (fixtures/valid/<stem>.json -> schema/<stem>.schema.json); callers are
// expected to name both returned files after slug so the mapping is
// unambiguous the moment the contract publishes.
func ContractScaffold(slug string) (schema, validFixture, invalidFixture []byte) {
	token := []byte(contractScaffoldSlugToken)
	schema = bytes.ReplaceAll(contractScaffoldSchemaRaw, token, []byte(slug))
	validFixture = bytes.ReplaceAll(contractScaffoldFixtureRaw, token, []byte(slug))
	invalidFixture = bytes.ReplaceAll(contractScaffoldInvalidFixtureRaw, token, []byte(slug))
	return schema, validFixture, invalidFixture
}

// ScaffoldContractInStaging writes decision D-D's starter schema plus valid
// and invalid fixtures for slug under stagingDir, at the SAME relative shape
// internal/space.Layout will place them at once submitted — so the author
// edits the paths a later publish actually looks for, and
// internal/cli/cmd_submit.go's sidecar carry finds them where it expects.
// D-E's fixture->schema stem mapping is satisfied by naming all three files
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
// Never overwrites: an existing file at any path is left exactly as it
// is, so a re-run cannot clobber an author's own schema or fixture.
// Returns the paths it actually wrote, in order.
func ScaffoldContractInStaging(stagingDir, ownSystem, slug string, writeFile func(string, []byte, os.FileMode) error) ([]string, error) {
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		return nil, err
	}

	schemaBytes, validFixtureBytes, invalidFixtureBytes := ContractScaffold(slug)
	candidates := []struct {
		path string
		data []byte
	}{
		{filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesSchemaDir(slug)), slug+".schema.json"), schemaBytes},
		{filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesValidDir(slug)), slug+".json"), validFixtureBytes},
		{filepath.Join(stagingDir, filepath.FromSlash(layout.ProvidesFixturesInvalidDir(slug)), slug+".json"), invalidFixtureBytes},
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

// contractSidecarMaxBytes bounds every sidecar leaf file read below — this
// package's own copy of the repo-wide bounded-read idiom, matching
// internal/cli/adapters.go's maxMirrorEventBytes and
// internal/artifact/digesttree.go's maxDigestTreeFileBytes exactly (both 1
// MiB, both themselves unexported to their own package —
// internal/artifact/digesttree.go's own doc comment states the rule this
// follows: "each package owns its own cap constant... none shared across
// package boundaries"). There is no principled reason a contract's staged
// schema/fixture file should tolerate a larger read than any other
// artifact-adjacent file this codebase reads.
const contractSidecarMaxBytes = 1 << 20 // 1 MiB

// contractSidecarDirs are decision D-D's own scaffold subtrees under a
// contract's provides/<slug>/ section (internal/space.Layout's
// ProvidesSchemaDir/ProvidesFixturesValidDir/ProvidesFixturesInvalidDir).
// fixtures/invalid is included even though §5.4b's compat core
// deliberately never feeds it to the new schema — an authored invalid
// fixture is still part of the published contract.
func contractSidecarDirs(layout space.Layout, slug string) []string {
	return []string{
		layout.ProvidesSchemaDir(slug),
		layout.ProvidesFixturesValidDir(slug),
		layout.ProvidesFixturesInvalidDir(slug),
	}
}

// ContractSidecarsFromStaging collects a staged contract's schema/** and
// fixtures/**/* sidecar files (D-D's scaffold, ScaffoldContractInStaging
// above) from stagingDir, at the SAME space-relative paths internal/
// space.Layout would place them at once submitted.
//
// It lives here, beside ScaffoldContractInStaging, for the SAME reason:
// BOTH `a2a submit` and `a2a contract publish` need to read this staged
// baseline back — `submit` to carry a scaffolded contract's first schema/
// fixture into the space at all, `contract publish` to carry a LATER
// staged schema/fixture edit into the version it is declaring (P37 Wave I:
// a published contract's mirror working tree is unconditionally hard-reset
// on every `a2a` invocation before any verb runs — internal/space/
// mirror.go's checkoutRemoteHead — so staging, never the mirror, is the
// only place an author's OWN schema/fixture edit can still be read from by
// the time a verb gets to look). internal/mcp may never import internal/
// cli (ADR-001, P14's proven invariant), so leaving this in internal/cli
// would have meant a second copy in the MCP handler — moved here instead,
// this file's own ScaffoldContractInStaging doc comment already explains
// why that copy would have been forced. This function is that writer's
// mirror: it reads back exactly what ScaffoldContractInStaging (or an
// author's own edit at the same path) wrote.
//
// Walks each subtree recursively (filepath.WalkDir) so a nested schema
// layout (e.g. schema/common/types.schema.json, a normal $ref split) is
// carried in full, not just its top-level files.
//
// Absent is not an error: a contract authored before the scaffold existed
// (or a non-JSON-Schema contract that legitimately has no schema) simply
// has nothing under schema/ or fixtures/ in staging — this returns (nil,
// nil), so neither caller starts refusing artifacts it accepted yesterday.
//
// Bounded and safe: every leaf read goes through readBoundedContractSidecar
// at contractSidecarMaxBytes. A symlink anywhere in the walk is refused
// outright, never followed or silently skipped — the one way a name that
// stayed inside the directory listing could still resolve to content, or
// imply a destination, outside the contract's own directory. A relative
// path that would resolve outside its subtree root is refused for the same
// reason (defense in depth alongside the symlink refusal, since WalkDir
// itself never emits one without a symlink already having crossed the
// boundary).
func ContractSidecarsFromStaging(stagingDir, ownSystem, slug string) ([]space.FileWrite, error) {
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		return nil, err
	}

	var out []space.FileWrite
	for _, spacePath := range contractSidecarDirs(layout, slug) {
		localDir := filepath.Join(stagingDir, filepath.FromSlash(spacePath))
		info, err := os.Stat(localDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("cannot stat %s: %w", localDir, err)
		}
		if !info.IsDir() {
			continue
		}

		type sidecarFile struct {
			rel string
			raw []byte
		}
		var found []sidecarFile
		walkErr := filepath.WalkDir(localDir, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if p == localDir {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("sidecar %s is a symlink, refused (escapes the contract's own directory)", p)
			}
			if d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(localDir, p)
			if relErr != nil {
				return relErr
			}
			relSlash := filepath.ToSlash(rel)
			if relSlash == ".." || strings.HasPrefix(relSlash, "../") {
				return fmt.Errorf("sidecar %s escapes the contract's own directory %s", p, localDir)
			}
			raw, rerr := readBoundedContractSidecar(p)
			if rerr != nil {
				return rerr
			}
			found = append(found, sidecarFile{rel: relSlash, raw: raw})
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}

		sort.Slice(found, func(i, j int) bool { return found[i].rel < found[j].rel })
		for _, f := range found {
			out = append(out, space.FileWrite{Path: path.Join(spacePath, f.rel), Content: f.raw})
		}
	}
	return out, nil
}

// readBoundedContractSidecar reads p with the same LimitReader-plus-
// explicit-cap-check idiom every other package's own bounded read uses
// (see contractSidecarMaxBytes's doc comment).
func readBoundedContractSidecar(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only fd, close error is not actionable here

	raw, err := io.ReadAll(io.LimitReader(f, contractSidecarMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > contractSidecarMaxBytes {
		return nil, fmt.Errorf("template: %s exceeds %d byte read bound", p, contractSidecarMaxBytes)
	}
	return raw, nil
}
