package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestMirrorRefreshingStructsWireACredentialResolver is the structural
// successor to TestMirrorRefreshingCommandsCarryACredentialResolver's
// hand-written list of three constructors. That list only covers the
// commands someone remembered to add to it — a fourth mirror-refreshing
// command can be written, compile, and pass every existing test while
// wiring no credential resolver at all, silently fetching unauthenticated
// forever and 404ing only once it meets a private space.
//
// So the invariant is stated structurally instead, exactly as
// internal/space/mirror_credential_test.go's
// TestNetworkGitVerbsRouteThroughRunNetworkGit states its own: a
// cloneOrFetch field on a struct IS the definition of "this command
// refreshes a mirror", so every struct declaring one must also declare a
// resolveCredential field, and that field must actually be populated
// somewhere in the same file — a declared-but-never-set field leaves the
// resolver nil at runtime and mirrorCredential silently degrades to the
// empty credential.
func TestMirrorRefreshingStructsWireACredentialResolver(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	structsChecked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}

		for _, structName := range structsWithField(file, "cloneOrFetch") {
			structsChecked++

			if !structHasField(file, structName, "resolveCredential") {
				t.Errorf("%s: struct %s declares cloneOrFetch (it refreshes a mirror) but no "+
					"resolveCredential field — its mirror refresh has no way to authenticate and "+
					"will silently fetch unauthenticated forever, 404ing only on a private space",
					name, structName)
				continue
			}

			if !fieldIsPopulated(file, "resolveCredential") {
				t.Errorf("%s: struct %s declares resolveCredential but never sets it — no "+
					"resolveCredential: key in a composite literal and no .resolveCredential "+
					"assignment — leaving the field nil at runtime, which mirrorCredential "+
					"silently degrades to the empty credential: a mirror refresh that fetches "+
					"unauthenticated forever and 404s only once it meets a private space",
					name, structName)
			}
		}
	}

	if structsChecked == 0 {
		t.Fatal("the gate found no struct declaring a cloneOrFetch field — it would pass vacuously; " +
			"either the field was renamed or this test's discovery is broken")
	}
}

// structsWithField returns the names of every struct type in file that
// declares a field named fieldName.
func structsWithField(file *ast.File, fieldName string) []string {
	var names []string
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			if structDeclaresField(structType, fieldName) {
				names = append(names, typeSpec.Name.Name)
			}
		}
	}
	return names
}

// structHasField reports whether the named struct type in file declares a
// field named fieldName.
func structHasField(file *ast.File, structName, fieldName string) bool {
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return structDeclaresField(structType, fieldName)
		}
	}
	return false
}

// structDeclaresField reports whether structType declares a field named
// fieldName, matching on the field's NAME only — never its type expression,
// since a mirror-refreshing command may spell resolveCredential's type as
// either the named credentialResolver alias or an inline func(...) literal.
func structDeclaresField(structType *ast.StructType, fieldName string) bool {
	if structType.Fields == nil {
		return false
	}
	for _, field := range structType.Fields.List {
		for _, ident := range field.Names {
			if ident.Name == fieldName {
				return true
			}
		}
	}
	return false
}

// fieldIsPopulated reports whether file sets fieldName somewhere: either as
// a composite-literal key (`resolveCredential: ...`) or as an assignment to
// a selector (`c.resolveCredential = ...`).
func fieldIsPopulated(file *ast.File, fieldName string) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		switch node := n.(type) {
		case *ast.KeyValueExpr:
			if ident, ok := node.Key.(*ast.Ident); ok && ident.Name == fieldName {
				found = true
			}
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == fieldName {
					found = true
				}
			}
		}
		return true
	})
	return found
}
