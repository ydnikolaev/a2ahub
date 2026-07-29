package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestBoundedGitHubHTTPClientHasRequestTimeout(t *testing.T) {
	t.Parallel()

	client := newBoundedGitHubHTTPClient()
	if client.Timeout != githubRequestTimeout {
		t.Fatalf("client timeout = %s, want %s", client.Timeout, githubRequestTimeout)
	}
	if client.Timeout <= 0 {
		t.Fatal("live GitHub client must not permit an unbounded round trip")
	}
}

// TestRequiredCheckPollUsesBoundedTransport is the permanent tooth for the
// targeted v0.16 live run that stalled after PR #1568 had already merged.
// The five-minute poll ceiling did not apply because CheckStatus was using
// http.DefaultClient: one round trip could therefore consume the full
// 110-minute matrix budget before the next poll condition was evaluated.
func TestRequiredCheckPollUsesBoundedTransport(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "harness_live.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse harness_live.go: %v", err)
	}

	var found bool
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "WaitForRequiredCheck" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "NewGitHubHost" || len(call.Args) != 2 {
				return true
			}
			clientCall, ok := call.Args[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := clientCall.Fun.(*ast.Ident)
			found = ok && ident.Name == "newBoundedGitHubHTTPClient"
			return !found
		})
	}

	if !found {
		t.Fatal("WaitForRequiredCheck must construct the product host with newBoundedGitHubHTTPClient")
	}
}
