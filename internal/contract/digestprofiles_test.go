package contract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// TestDigestProfilesMatchTheDeclarations derives the vocabulary from the
// typed declarations themselves rather than copying the three string values
// into a second list — internal/cache's TestReasonCodeDeclarationAndSites is
// the shape this follows. It guards BOTH directions: a constant declared
// without a DigestProfiles() row reds (the case that matters — a profile
// added tomorrow must produce a new cell in every gate that crosses this
// axis, with no Go edit), and a row naming a value nothing declares reds too,
// so the list cannot just accumulate.
func TestDigestProfilesMatchTheDeclarations(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "types.go", nil, 0)
	if err != nil {
		t.Fatalf("parse types.go: %v", err)
	}

	declared := map[DigestProfile]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, raw := range gen.Specs {
			spec, ok := raw.(*ast.ValueSpec)
			if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
				continue
			}
			typ, typeOK := spec.Type.(*ast.Ident)
			literal, literalOK := spec.Values[0].(*ast.BasicLit)
			if !typeOK || typ.Name != "DigestProfile" || !literalOK || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", spec.Names[0].Name, err)
			}
			if prior := declared[DigestProfile(value)]; prior != "" {
				t.Fatalf("profile %q declared by both %s and %s", value, prior, spec.Names[0].Name)
			}
			declared[DigestProfile(value)] = spec.Names[0].Name
		}
	}
	if len(declared) == 0 {
		t.Fatal("parsed no DigestProfile constants from types.go — the extractor is reading nothing, which would make every assertion below vacuous")
	}

	enumerated := map[DigestProfile]bool{}
	for _, profile := range DigestProfiles() {
		if declared[profile] == "" {
			t.Errorf("DigestProfiles() lists %q, which no const in types.go declares", profile)
		}
		if enumerated[profile] {
			t.Errorf("DigestProfiles() lists %q twice", profile)
		}
		enumerated[profile] = true
	}
	for profile, name := range declared {
		if !enumerated[profile] {
			t.Errorf("types.go declares %s = %q but DigestProfiles() does not list it — a gate crossing this axis would not know the value exists", name, profile)
		}
	}
}
