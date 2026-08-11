package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCPConstructsNoPendencyVerdict is the gate spec 02's 2026-08-09
// amendment said already existed.
//
// That amendment reads: "a test in `internal/mcp` asserts that every read
// handler obtains its pendency-bearing values from a `cache.Store` call and
// constructs no pendency verdict of its own", and it is the whole reason MCP
// was NOT added as a fifth surface to P2's behavioural comparison — the
// structural guarantee was accepted in its place. The P11 coherence audit
// looked for the test and found none: the architectural fact held, but only
// trivially and unguarded, so the amendment was a rule that was true and
// inert. This is that test, written where the claim said it was.
//
// The check is structural because the claim is: no file in this package
// (outside tests) may reference the pendency package at all. A pendency
// verdict this package built itself would be a SECOND derivation of the
// one relation P2 exists to keep single — the four surfaces would agree
// with each other and MCP would quietly disagree with all of them.
//
// It reads the package's own sources rather than an import list from `go
// list`, so a reference through a dot-import or an aliased import is caught
// the same way a plain one is.
func TestMCPConstructsNoPendencyVerdict(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go sources found in internal/mcp — the check would pass vacuously")
	}

	var checked int
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		checked++
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, f, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}

		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasSuffix(path, "/internal/pendency") || strings.HasSuffix(path, "/internal/cache/pendency") {
				t.Errorf("%s imports %s — MCP must read pendency-bearing values from cache.Store, never derive them (spec 02 §11)", f, path)
			}
		}

		// Belt and braces: a selector like `pendency.Resolve(...)` reached
		// through an aliased or dot import would not be caught above.
		ast.Inspect(parsed, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == "pendency" {
				t.Errorf("%s:%d references pendency.%s — MCP must read pendency-bearing values from cache.Store, never derive them (spec 02 §11)",
					f, fset.Position(sel.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("every source in internal/mcp is a test file — the check passed vacuously")
	}
	t.Logf("checked %d non-test source(s) in internal/mcp", checked)
}
