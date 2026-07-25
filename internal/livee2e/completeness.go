package livee2e

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// completeness.go implements spec 38's AC-983.1 (§T3): "the matrix is
// complete" must be a claim a gate can check, not a feeling. Before this
// file, Catalogue() was a hand-written list with no connection whatsoever to
// the kind corpus internal/fold actually defines — a new envelope kind could
// merge with zero live coverage and nothing would notice.
//
// UNTAGGED on purpose, matching branchmatch.go's and familyfilter.go's own
// precedent: the RULE (foldKindSet, missingCoveredKinds, unknownDeclaredKinds)
// is pure and unit-tested by the plain `go test ./internal/livee2e/` suite
// `make check` runs; nothing here touches the network or the livee2e build
// tag. A completeness gate that only runs during a 40-minute live run is a
// gate nobody runs.
//
// foldKindSet derives the expected kind set by PARSING internal/fold's own
// source (go/ast + go/parser, stdlib, no new dependency — the same technique
// internal/cli/cmd_validate_ci_test.go's own
// TestValidateCIAndContractHaveNoSecondCompatCopy uses for an analogous "the
// real rule, not a copy of it" guard) rather than retyping the eight
// fold.Kind values here by hand. A hand-typed list is exactly the trap this
// wave's brief names: cmd/a2a/catalog_test.go's own name-parity check
// independently RECOMPUTED a wrong expansion rule and agreed with it
// perfectly while seven uninvocable command names shipped. Parsing the
// actual const declarations means a kind added, renamed, or removed in
// internal/fold changes what this gate expects WITHOUT anyone touching this
// file — the one property a restated list can never have.
func foldKindSet() ([]string, error) {
	dir := filepath.Join("..", "fold")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("livee2e: foldKindSet: read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	seen := map[string]bool{}
	var sawKindType bool
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("livee2e: foldKindSet: parse %s: %w", path, perr)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// Only consts explicitly typed `Kind` (every line in
				// internal/fold/types.go's own KindXxx block repeats the
				// type, so this never has to fall back on an untyped
				// "same as previous spec" inference) — this is what keeps
				// State/Role/FlagKind/Verdict's own const blocks in the
				// same file out of this set.
				ident, ok := vs.Type.(*ast.Ident)
				if !ok || ident.Name != "Kind" {
					continue
				}
				sawKindType = true
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					val, uerr := strconv.Unquote(lit.Value)
					if uerr != nil {
						return nil, fmt.Errorf("livee2e: foldKindSet: unquote %s: %w", lit.Value, uerr)
					}
					if val != "" {
						seen[val] = true
					}
				}
			}
		}
	}
	if !sawKindType {
		return nil, fmt.Errorf("livee2e: foldKindSet: no `Kind`-typed const declaration found under %s — internal/fold's own shape changed; this parser needs to change with it, not silently report zero kinds", dir)
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// missingCoveredKinds is AC-983.1's forward half: which of `kinds` (the real
// corpus, from foldKindSet) has NO Catalogue() row naming it in its own
// Kinds field. Kept separate from foldKindSet/Catalogue() so it is
// unit-testable against a SYNTHETIC scenario slice — the TEETH case (a
// kind's rows losing their Kinds entry) does not need a real catalogue
// mutation to prove the gate reds; see completeness_test.go.
func missingCoveredKinds(kinds []string, scenarios []Scenario) []string {
	covered := map[string]bool{}
	for _, sc := range scenarios {
		for _, k := range sc.Kinds {
			covered[k] = true
		}
	}
	var missing []string
	for _, k := range kinds {
		if !covered[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	return missing
}

// unknownDeclaredKinds is AC-983.1's reverse half: a Scenario.Kinds entry
// that is NOT a member of the real corpus — a typo ("Contract" instead of
// "contract"), a kind fold has since renamed or removed out from under a
// stale Kinds entry, or a row that names a kind that never existed. Without
// this half the gate is one-directional: a Kinds field nobody validates can
// rot silently in exactly the way this wave's brief warns about
// (cmd/a2a/catalog_test.go's own name-parity check "independently
// recomputed a wrong expansion rule and agreed with it perfectly"). Reported
// as "scenario %q: %q" pairs so a failing report names both the offending
// row and the bad value.
func unknownDeclaredKinds(kinds []string, scenarios []Scenario) []string {
	known := map[string]bool{}
	for _, k := range kinds {
		known[k] = true
	}
	var offenders []string
	for _, sc := range scenarios {
		for _, k := range sc.Kinds {
			if !known[k] {
				offenders = append(offenders, fmt.Sprintf("%s: %q", sc.Name, k))
			}
		}
	}
	sort.Strings(offenders)
	return offenders
}
