package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/operation"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

func TestContractDeprecateOperationUsesOpaqueSemanticBranch(t *testing.T) {
	t.Parallel()

	key, branch := contractDeprecateOperation(
		"alpha",
		"XC-alpha-widget",
		"1.0.0",
		"XC-alpha-widget-successor@2.0.0",
		"2020-01-01",
	)
	if !operation.Valid(key) {
		t.Fatalf("key = %q, want canonical operation key", key)
	}
	if want := space.BranchName("alpha", "contract-deprecate", key); branch != want {
		t.Fatalf("branch = %q, want %q", branch, want)
	}
	if strings.Contains(branch, "XC-alpha-widget") {
		t.Fatalf("branch = %q still exposes the retired artifact-id grammar", branch)
	}
}

func TestRespondOperationMatchesExplicitCLIActor(t *testing.T) {
	t.Parallel()

	key, branch := respondOperation("bravo", "XQ-alpha-parent", "answered")
	if !operation.Valid(key) {
		t.Fatalf("key = %q, want canonical operation key", key)
	}
	if want := space.BranchName("bravo", "respond", key); branch != want {
		t.Fatalf("branch = %q, want %q", branch, want)
	}
	args := strings.Join(respondCommandArgs("XQ-alpha-parent", "answered"), " ")
	for _, want := range []string{
		"--actor-kind " + liveRespondActorKind,
		"--actor-name " + liveRespondActorName,
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("respond args %q do not carry canonical input %q", args, want)
		}
	}
}

func TestOperationArtifactIDUsesFunnelMetadata(t *testing.T) {
	t.Parallel()

	key, _ := contractDeprecateOperation(
		"alpha",
		"XC-alpha-widget",
		"1.0.0",
		"XC-alpha-widget-successor@2.0.0",
		"2020-01-01",
	)
	body := "advisory\n<!-- a2a-operation: {\"key\":\"" + key +
		"\",\"ids\":[\"XA-alpha-generated\",\"XC-alpha-widget\"]} -->"
	got, err := operationArtifactID(body, key, "XC-alpha-widget", "XA-")
	if err != nil {
		t.Fatalf("operationArtifactID: %v", err)
	}
	if got != "XA-alpha-generated" {
		t.Fatalf("artifact = %q, want XA-alpha-generated", got)
	}
}

func TestOperationArtifactIDRefusesUntrustedMetadata(t *testing.T) {
	t.Parallel()

	key, _ := respondOperation("bravo", "XQ-alpha-parent", "answered")
	otherKey, _ := respondOperation("bravo", "XQ-alpha-other", "answered")
	tests := []struct {
		name string
		body string
	}{
		{name: "absent", body: "ordinary pull request body"},
		{name: "malformed", body: "<!-- a2a-operation: not-json -->"},
		{
			name: "wrong key",
			body: "<!-- a2a-operation: {\"key\":\"" + otherKey +
				"\",\"ids\":[\"XQ-alpha-parent\",\"XS-bravo-response\"]} -->",
		},
		{
			name: "missing generated id",
			body: "<!-- a2a-operation: {\"key\":\"" + key +
				"\",\"ids\":[\"XQ-alpha-parent\"]} -->",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := operationArtifactID(tc.body, key, "XQ-alpha-parent", "XS-"); err == nil {
				t.Fatal("operationArtifactID succeeded, want fail-closed refusal")
			}
		})
	}
}

// TestD6LiveConsumersNeverUseCompositeBranchLookup is the structural tooth
// for the live finding from PR #1583. Both D6 operations use opaque op-v1
// branches; matching their artifact IDs with pullForBranchContaining can
// never succeed, regardless of retry duration.
func TestD6LiveConsumersNeverUseCompositeBranchLookup(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob("scenarios_*_live.go")
	if err != nil {
		t.Fatalf("glob live scenarios: %v", err)
	}
	// A structural gate that scans nothing passes forever. This one is a glob
	// over a filename convention, so a rename — `scenarios_x_live.go` to
	// anything else — silently empties it while it keeps reporting green.
	if len(files) == 0 {
		t.Fatal("the scan matched no scenarios_*_live.go files — this gate would pass vacuously; fix the glob or the naming convention it assumes")
	}
	fset := token.NewFileSet()
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "pullForBranchContaining" || len(call.Args) < 3 {
				return true
			}
			lit, ok := call.Args[2].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			verb, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Errorf("%s: unquote verb at %s: %v", path, fset.Position(lit.Pos()), err)
				return true
			}
			if verb == "respond" || verb == "contract-deprecate" {
				t.Errorf("%s still routes D6 verb %q through composite branch lookup at %s",
					path, verb, fset.Position(call.Pos()))
			}
			return true
		})
	}
}
