package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryStandingDraftCallSitePassesASlug is the gate that would have saved
// a live matrix.
//
// `a2a new` mints a standing id from the slug and refuses without one
// ("--slug (or --field slug=<slug>) is required for standing types"). The
// data-loop family shipped `h.DraftAndSubmit(ctx, a, "contract")` with no
// slug while all four sibling scenarios passed one. Because that family's
// only earlier live run died on an earlier step, the line had never
// executed — so the FIRST authoring step of v0.19.0's headline feature was
// impossible, and the way we found out was 2h19m into a matrix against real
// GitHub, at roughly 80 seconds per pull request.
//
// checkout.Draft now refuses the same call in microseconds, but a run-time
// refusal still costs the run that reaches it. This test costs nothing and
// runs in `make check`, before a candidate is ever published.
//
// It reads the tier's own source rather than exercising it: the call sites
// live behind `//go:build livee2e`, and the property under test — "this
// call passes a slug" — is a property of the source text, not of a runtime
// value. go/parser is used instead of a regexp because these calls wrap
// across lines.
func TestEveryStandingDraftCallSitePassesASlug(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Draft" && sel.Sel.Name != "DraftAndSubmit") {
				return true
			}
			artifactType, ok := standingTypeArgument(call.Args)
			if !ok {
				return true
			}
			checked++
			if callPassesASlug(call.Args) {
				return true
			}
			t.Errorf("%s: %s(..., %q, ...) passes no slug — `a2a new` refuses a standing type without one, "+
				"so this call can never succeed. Scope it: --slug liveRunSlug(<base>, h.PRFloor).",
				fset.Position(call.Pos()), sel.Sel.Name, artifactType)
			return true
		})
	}
	// A zero count would mean the shapes moved and this gate silently stopped
	// watching anything — the exact failure mode it exists to prevent.
	if checked == 0 {
		t.Fatal("found no standing-type Draft/DraftAndSubmit call sites; the gate is no longer watching anything")
	}
	t.Logf("checked %d standing-type draft call site(s)", checked)
}

// standingTypeArgument returns the standing artifact type named by a literal
// argument of the call, if any.
func standingTypeArgument(args []ast.Expr) (string, bool) {
	for _, arg := range args {
		value, ok := stringLiteral(arg)
		if ok && standingDraftTypes[value] {
			return value, true
		}
	}
	return "", false
}

// callPassesASlug reports whether the call carries a slug in any shape
// hasSlugArg accepts, including a `--slug` flag whose value is a call such as
// liveRunSlug(...) rather than a literal.
func callPassesASlug(args []ast.Expr) bool {
	for _, arg := range args {
		value, ok := stringLiteral(arg)
		if !ok {
			continue
		}
		if value == "--slug" || strings.HasPrefix(value, "--slug=") || strings.HasPrefix(value, "slug=") {
			return true
		}
	}
	return false
}

func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func TestHasSlugArgAcceptsEveryShapeTheCLIDoes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		extra []string
		want  bool
	}{
		{"flag with value", []string{"--slug", "widget"}, true},
		{"flag with equals", []string{"--slug=widget"}, true},
		{"field form", []string{"--field", "slug=widget"}, true},
		{"no slug at all", []string{"--field", "to=[bravo]"}, false},
		{"dangling flag", []string{"--slug"}, false},
		{"slug only in a value", []string{"--field", "title=slug=not-a-slug"}, false},
	} {
		if got := hasSlugArg(tc.extra); got != tc.want {
			t.Errorf("%s: hasSlugArg(%q) = %v, want %v", tc.name, tc.extra, got, tc.want)
		}
	}
}
