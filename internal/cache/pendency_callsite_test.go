package cache

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// requiredInputFields are the pendency.Input fields whose ABSENCE at a call
// site is silent and wrong rather than loud and wrong.
//
// The Go zero value is a legitimate answer for most of Input's fields — an
// artifact with no required approvers really does have none. These two are
// different: they carry facts this package resolved elsewhere and that
// internal/pendency structurally cannot recover, so omitting one does not
// produce an error, it produces a DIFFERENT ANSWER from the call site next
// door. P11 W1 shipped with exactly that divergence for one commit —
// inbox.go passed ExtraAddressees, threadview.go did not, and the two
// disagreed about whom a frozen deprecation `to:` addresses. That is the
// wave's own defect (one question, two answers) reappearing one layer down.
//
//   - ExtraAddressees — P4 Edge 3's registry-derived late adopter.
//   - ActiveParticipants — the broadcast expansion set; without it a
//     broadcast announcement owes an acknowledge to nobody.
//   - LeftParticipants — CC-062's departed counterparty. Added in P2 after
//     a mutation showed nothing in this package caught its omission: with
//     it the relation transfers the obligation to the sender, without it
//     the relation names a system that can no longer write. Both answers
//     leave SOMEBODY owing, so every boolean consumer agreed either way
//     and only an owner-set assertion could see the difference
//     (TestExchangeActiveMatchesTheRelation's wantOwners).
//
// This gate is deliberately syntactic. It cannot prove a call site passes
// the RIGHT value, only that it made a decision about the field at all —
// which is the whole failure mode, since both omissions compile and pass
// every behavioural test that does not happen to exercise them.
var requiredInputFields = []string{"ActiveParticipants", "ExtraAddressees", "LeftParticipants"}

// TestEveryPendencyInputCallSiteDecidesTheCarriedFacts refuses a
// pendency.Input literal in this package that leaves a carried fact unset.
func TestEveryPendencyInputCallSiteDecidesTheCarriedFacts(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var missing []string
	sites := 0

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Input" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "pendency" {
				return true
			}
			sites++

			set := map[string]bool{}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					// A positional literal names no field at all and would
					// break on the next field added to Input; refuse it here
					// rather than let it decide these facts by accident.
					missing = append(missing, fset.Position(lit.Pos()).String()+
						": positional pendency.Input literal — use field names")
					return true
				}
				if id, ok := kv.Key.(*ast.Ident); ok {
					set[id.Name] = true
				}
			}
			for _, field := range requiredInputFields {
				if !set[field] {
					missing = append(missing, fset.Position(lit.Pos()).String()+
						": pendency.Input literal does not set "+field)
				}
			}
			return true
		})
	}

	if sites == 0 {
		t.Fatal("found no pendency.Input literal in this package — this gate " +
			"is watching nothing, which is worse than a red gate. Either the " +
			"call sites moved (point this test at them) or the projection was " +
			"reverted (that is the regression)")
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("a pendency.Input call site left a carried fact undecided, so two "+
			"call sites can answer the same question differently (%d site(s) "+
			"examined):\n  %s", sites, strings.Join(missing, "\n  "))
	}
}

// resolverHome is the one function in this package allowed to call
// internal/pendency.Resolve.
const resolverHome = "resolveVerdict"

// TestOnlyOneFunctionResolvesThePendencyRelation is the structural half of
// I7 inside this package.
//
// The field gate above proves each call site DECIDED the carried facts. It
// cannot prove there should be only one, and for two waves there were two —
// inbox.go and threadview.go, byte-identical apart from one field, kept in
// step by a comment asking the next author to remember.
//
// A third derivation grew anyway, in a shape neither gate could see:
// `exchangeActive` answered "does anybody still owe an acknowledgement" from
// `ack_requested`, `to:` and the folded ack set directly, never touching
// pendency at all — and reproduced a defect pendency had already fixed. It
// took a differential test to find it, which is exactly the cost this gate
// exists to stop paying.
//
// So the rule is placement, not shape: one function resolves, everything
// else reads its verdict.
func TestOnlyOneFunctionResolvesThePendencyRelation(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string
	found := false

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Resolve" {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "pendency" {
					return true
				}
				if fn.Name.Name == resolverHome {
					found = true
					return true
				}
				offenders = append(offenders, fset.Position(call.Pos()).String()+
					": pendency.Resolve called from "+fn.Name.Name)
				return true
			})
		}
	}

	if !found {
		t.Fatalf("no pendency.Resolve call inside %s — either the resolver was "+
			"renamed (point this gate at it) or this package stopped asking the "+
			"relation at all, and this gate is watching nothing", resolverHome)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("the relation has more than one home in this package; every "+
			"consumer must read %s's verdict instead:\n  %s",
			resolverHome, strings.Join(offenders, "\n  "))
	}
}
