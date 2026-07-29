package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"testing"
)

func TestLiveRunSlugSeparatesDestructiveSpaceGenerations(t *testing.T) {
	t.Parallel()

	first := liveRunSlug("matrix-contract-lifecycle", 1552)
	retry := liveRunSlug("matrix-contract-lifecycle", 1552)
	next := liveRunSlug("matrix-contract-lifecycle", 1559)
	if first != retry {
		t.Fatalf("same generation slug changed: %q != %q", first, retry)
	}
	if first == next {
		t.Fatalf("different generations reused slug %q", first)
	}
	if want := "matrix-contract-lifecycle-run-1552"; first != want {
		t.Fatalf("slug = %q, want %q", first, want)
	}
	if !regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`).MatchString(first) {
		t.Fatalf("slug %q is outside the standing-id grammar", first)
	}
}

func TestSemanticContractScenariosUseTheLiveRunSlug(t *testing.T) {
	t.Parallel()

	required := map[string]string{
		"scenarios_happy_live.go":              "happyContractLifecycle",
		"scenarios_mcp_live.go":                "mcpContractLifecycle",
		"scenarios_contract_integrity_live.go": "ac973DraftContractExcludingPeer",
		"scenarios_rolling_window_live.go":     "rwDraftContractExcludingPeer",
	}
	for path, funcName := range required {
		t.Run(funcName, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			foundFunc := false
			foundRunSlug := false
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != funcName {
					continue
				}
				foundFunc = true
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					id, ok := call.Fun.(*ast.Ident)
					if ok && id.Name == "liveRunSlug" {
						foundRunSlug = true
					}
					return true
				})
			}
			if !foundFunc {
				t.Fatalf("%s no longer contains %s; the regression's function map is stale", path, funcName)
			}
			if !foundRunSlug {
				t.Fatalf("%s/%s does not call liveRunSlug — a retained merged semantic-operation PR can impersonate a new destructive-space generation", path, funcName)
			}
		})
	}
}
