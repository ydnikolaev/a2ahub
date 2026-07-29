package livee2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// pullFilterExemptFuncs are the ONLY functions allowed to call
// ghClient.ListPulls directly. Every other lookup must go through
// harness.runPulls, which narrows the listing to the pull requests THIS run
// opened.
//
//   - runPulls IS the filter.
//   - maxPRNumber ESTABLISHES it (ResetSpace's watermark) and so must see
//     the unfiltered listing.
//   - refusalOutOfSectionWriteScenario compares its OWN before/after
//     snapshots taken seconds apart. That delta is self-contained — a ghost
//     PR from a prior run appears in both halves and cancels — and the
//     unfiltered form is the STRONGER claim it wants to make: "the refused
//     write left no new pull request anywhere in the repo", not merely
//     "none above this run's floor".
var pullFilterExemptFuncs = map[string]string{
	"runPulls":                         "is the filter",
	"maxPRNumber":                      "establishes the watermark the filter uses",
	"refusalOutOfSectionWriteScenario": "self-contained before/after delta; unfiltered is the stronger claim",
}

// TestEveryPullLookupGoesThroughRunPulls is live run 3's (2026-07-25)
// regression, kept as structure rather than as a comment.
//
// `contract-publish-deprecate-retire` failed with "more than one branch
// matches this artifact id" on a space that had JUST been reset. ResetSpace
// prunes every non-main branch and always did; what it cannot prune is pull
// requests — GitHub offers no delete, and a closed PR keeps its head ref
// forever. This tier's branch names are deterministic per (system, verb,
// artifact id), so run N+1's write reuses the exact head ref run N's ghost
// PR still points at, and matchCompositeBranch correctly refused an
// ambiguity the harness had manufactured.
//
// The fix is a watermark, and the failure mode of a watermark is a lookup
// that forgets to apply it — which is invisible until the next live run,
// costs minutes to observe, and reads as a product defect when it lands.
// So the discipline is enforced here, at compile-cost, instead.
//
// TEETH: change any of pullForBranch / pullForBranchContaining /
// countPRsForBranch back to `h.Prov.ListPulls(...)` and this test reds,
// naming the function.
func TestEveryPullLookupGoesThroughRunPulls(t *testing.T) {
	t.Parallel()

	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found — this test would pass vacuously")
	}

	fset := token.NewFileSet()
	var offenders []string
	var sawFilter bool

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if fn.Name.Name == "runPulls" {
				sawFilter = true
			}
			if _, exempt := pullFilterExemptFuncs[fn.Name.Name]; exempt {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "ListPulls" {
					return true
				}
				offenders = append(offenders,
					filepath.Base(path)+":"+fn.Name.Name+" (line "+
						fset.Position(sel.Pos()).String()+")")
				return true
			})
		}
	}

	// Guards the guard: if runPulls is renamed away, the exemption map is
	// stale and this test would silently stop describing anything real.
	if !sawFilter {
		t.Fatal("no runPulls function found in this package — the filter this test enforces no longer exists under that name, so the exemption list is stale")
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("these functions call ListPulls directly instead of harness.runPulls:\n  %s\n\n"+
			"A reset cannot delete the pull requests of the branches it prunes, and this tier's branch names repeat across runs — "+
			"an unfiltered lookup therefore sees a PRIOR run's ghost PR on the exact head ref this run is writing to. "+
			"Route the lookup through runPulls, or add a documented exemption to pullFilterExemptFuncs if it genuinely needs the whole listing.",
			strings.Join(offenders, "\n  "))
	}
}

// TestBranchLookupsAwaitProviderVisibility is the structural half of the
// 2026-07-29 PR #1549 regression. awaitLookupVisibility's unit tests prove
// the retry state machine, while this test proves the two production branch
// lookups actually route through it. Without this tooth the helper could stay
// green and unused while a future simplification restored the one-shot list
// query that produced the false live red.
func TestBranchLookupsAwaitProviderVisibility(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "harness_live.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse harness_live.go: %v", err)
	}
	required := map[string]bool{
		"pullForBranch":           false,
		"pullForBranchContaining": false,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if _, tracked := required[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if ok && id.Name == "awaitLookupVisibility" {
				required[fn.Name.Name] = true
			}
			return true
		})
	}
	for fn, guarded := range required {
		if !guarded {
			t.Errorf("%s does not call awaitLookupVisibility — a newly-created PR omitted from the first LIST /pulls response will be reported missing", fn)
		}
	}
}
