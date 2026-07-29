package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
)

// TestLifecycleScenariosGateEveryPR is the hermetic tooth for the live
// harness bug found by the first v0.16 candidate: lifecycle scenarios on both
// surfaces returned PASS after parsing a final PR number without ever waiting
// for that PR's required check or merge. The MCP product had emitted an
// invalid event, but the row hid the red check; the CLI variants could also
// leave main moving underneath the next scenario. Keep every PR in all four
// multi-step flows behind the same happyLandAndSync gate.
func TestLifecycleScenariosGateEveryPR(t *testing.T) {
	t.Parallel()

	wantByFile := map[string]map[string][]string{
		"scenarios_mcp_live.go": {
			"mcpLifecycle":         {"publishPR", "withdrawPR"},
			"mcpContractLifecycle": {"deprecatePR", "publishPR", "retirePR"},
		},
		"scenarios_happy_live.go": {
			"happyLifecycleTransitions": {"sub.PRNumber", "withdrawPR.Number"},
			"happyContractLifecycle":    {"deprecatePR.Number", "retirePR.Number", "sub.PRNumber"},
		},
	}

	for path, want := range wantByFile {
		assertLifecyclePRGates(t, path, want)
	}
}

func assertLifecyclePRGates(t *testing.T, path string, want map[string][]string) {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	got := make(map[string][]string, len(want))

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, relevant := want[fn.Name.Name]; !relevant {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			callee, ok := call.Fun.(*ast.Ident)
			if !ok || callee.Name != "happyLandAndSync" || len(call.Args) != 4 {
				return true
			}
			pr, ok := prExpressionName(call.Args[3])
			if !ok {
				t.Fatalf("%s passes an unsupported PR expression to happyLandAndSync at %s", fn.Name.Name, fset.Position(call.Pos()))
			}
			got[fn.Name.Name] = append(got[fn.Name.Name], pr)
			return true
		})
	}

	for fn, expected := range want {
		sort.Strings(got[fn])
		if !reflect.DeepEqual(got[fn], expected) {
			t.Errorf("%s gates PRs %v, want %v; every created PR must prove its required check and merge before PASS", fn, got[fn], expected)
		}
	}
}

func prExpressionName(expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name, true
	case *ast.SelectorExpr:
		base, ok := v.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		return base.Name + "." + v.Sel.Name, true
	default:
		return "", false
	}
}
