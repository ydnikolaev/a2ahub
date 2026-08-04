package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestContractPathsFollowReturnedRunScopedID(t *testing.T) {
	t.Parallel()

	got, err := contractPathsForID("XC-alpha-ac973-registered-consumer-run-1669")
	if err != nil {
		t.Fatalf("contractPathsForID: %v", err)
	}
	if want := "alpha/provides/ac973-registered-consumer-run-1669/schema/ac973-registered-consumer-run-1669.schema.json"; got.SchemaFile != want {
		t.Fatalf("SchemaFile = %q, want %q", got.SchemaFile, want)
	}
	if want := ".a2a/staging/alpha/provides/ac973-registered-consumer-run-1669"; got.StagingRoot != want {
		t.Fatalf("StagingRoot = %q, want %q", got.StagingRoot, want)
	}
	if want := "fixtures/valid/ac973-registered-consumer-run-1669.json"; got.ValidFixtureKey != want {
		t.Fatalf("ValidFixtureKey = %q, want %q", got.ValidFixtureKey, want)
	}
}

func TestContractPathsRefuseNonContractIDs(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"not-an-id",
		"XQ-alpha-20260729-55ew",
		"XR-alpha-some-requirement",
	} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if _, err := contractPathsForID(id); err == nil {
				t.Fatalf("contractPathsForID(%q) succeeded, want refusal", id)
			}
		})
	}
}

// TestContractIntegrityPathsAreWiredToReturnedIDs is the permanent tooth for
// the v0.16.0 live finding: run-scoped contracts were drafted correctly, but
// both scenario families edited the scaffold under their unscoped base slug.
func TestContractIntegrityPathsAreWiredToReturnedIDs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		file      string
		function  string
		callee    string
		arg       string
		wantCalls int
	}{
		{"scenarios_contract_integrity_live.go", "ac973BreakSchema", "contractPathsForID", "id", 1},
		{"scenarios_contract_integrity_live.go", "ac973ContractIntegrity", "contractPathsForID", "sub.ID", 1},
		{"scenarios_rolling_window_live.go", "rwWriteSchema", "contractPathsForID", "contractID", 1},
		{"scenarios_rolling_window_live.go", "rwRollingWindow", "rwWriteSchema", "sub.ID", 2},
	} {
		t.Run(tc.function, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, tc.file, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse %s: %v", tc.file, err)
			}
			gotCalls := 0
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != tc.function {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					callee, ok := call.Fun.(*ast.Ident)
					if !ok || callee.Name != tc.callee {
						return true
					}
					for _, arg := range call.Args {
						if expressionName(arg) == tc.arg {
							gotCalls++
						}
					}
					return true
				})
			}
			if gotCalls != tc.wantCalls {
				t.Fatalf("%s/%s calls %s with %s %d time(s), want %d", tc.file, tc.function, tc.callee, tc.arg, gotCalls, tc.wantCalls)
			}
		})
	}
}

func expressionName(expr ast.Expr) string {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return expressionName(value.X) + "." + value.Sel.Name
	default:
		return ""
	}
}
