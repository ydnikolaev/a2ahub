package cache

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

// TestReasonCodeDeclarationAndSites derives the vocabulary from the typed
// declarations themselves. It guards both directions without copying the
// twelve string values into a second test list: every enumerated value is
// declared, every declaration is used, and no production site writes or reads
// one of the values as a bare string literal.
func TestReasonCodeDeclarationAndSites(t *testing.T) {
	t.Parallel()

	files := []string{
		"reasons.go", "inbox.go", "outbox.go", "overdue.go",
		"notifications.go", "statusline.go",
		filepath.Join("..", "notification", "projector.go"),
	}
	fset := token.NewFileSet()
	parsed := make(map[string]*ast.File, len(files))
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		parsed[path] = file
	}

	declared := map[ReasonCode]string{}
	for _, decl := range parsed["reasons.go"].Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, raw := range gen.Specs {
			spec := raw.(*ast.ValueSpec)
			if len(spec.Names) != 1 || len(spec.Values) != 1 {
				continue
			}
			typ, ok := spec.Type.(*ast.Ident)
			literal, literalOK := spec.Values[0].(*ast.BasicLit)
			if !ok || typ.Name != "ReasonCode" || !literalOK || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", spec.Names[0].Name, err)
			}
			code := ReasonCode(value)
			if prior := declared[code]; prior != "" {
				t.Fatalf("reason %q declared by both %s and %s", code, prior, spec.Names[0].Name)
			}
			declared[code] = spec.Names[0].Name
		}
	}
	if len(declared) != 12 {
		t.Fatalf("declared %d typed reason codes, want the twelve production codes", len(declared))
	}

	enumerated := map[ReasonCode]bool{}
	for _, code := range ReasonCodes() {
		if declared[code] == "" {
			t.Errorf("ReasonCodes includes undeclared %q", code)
		}
		if enumerated[code] {
			t.Errorf("ReasonCodes repeats %q", code)
		}
		enumerated[code] = true
	}
	for code := range declared {
		if !enumerated[code] {
			t.Errorf("declared reason %q is absent from ReasonCodes", code)
		}
	}

	uses := map[string]int{}
	for path, file := range parsed {
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.Ident:
				uses[value.Name]++
			case *ast.BasicLit:
				if path == "reasons.go" || value.Kind != token.STRING {
					break
				}
				text, err := strconv.Unquote(value.Value)
				if err == nil && declared[ReasonCode(text)] != "" {
					t.Errorf("%s:%d uses reason %q as a bare production literal; use %s",
						path, fset.Position(value.Pos()).Line, text, declared[ReasonCode(text)])
				}
			}
			return true
		})
	}
	for code, name := range declared {
		if uses[name] < 2 { // declaration plus at least one enumerator/production reference
			t.Errorf("%s (%q) has no reference outside its declaration", name, code)
		}
	}
}
