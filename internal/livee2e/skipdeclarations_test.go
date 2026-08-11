package livee2e

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// skipdeclarations_test.go is skipdeclarations.go's own gate — P8's AC5
// (spec 08-paths.md §8, US-3). Deliberately UNTAGGED, same reasoning
// skipdeclarations.go's own doc comment states: this runs on the plain
// `go test ./internal/livee2e/...` suite `make check` already executes on
// every commit, never only under a `-tags=livee2e` run nobody triggers by
// hand.
//
// scanSkipSites is a static AST scan, not a text grep, so it cannot be
// fooled by a Skip-shaped string literal (this very file has several, used
// as synthetic fixture source below) and cannot miss a Skip call reformatted
// across lines. parser.ParseFile reads Go syntax only — it does not
// evaluate `//go:build` constraints — so a skip hidden behind the `livee2e`
// tag is found exactly as readily as an untagged one, which is the point:
// AC5 is about honesty, and a tag nobody runs by hand is not an excuse to
// look away.

// skipSite is one Skip-family call this scanner actually found in source.
type skipSite struct {
	File       string
	Func       string
	Occurrence int
	Line       int
}

func (s skipSite) key() string { return s.File + "\x00" + s.Func + "\x00" + fmt.Sprint(s.Occurrence) }

func (d skipDeclaration) key() string {
	return d.File + "\x00" + d.Func + "\x00" + fmt.Sprint(d.Occurrence)
}

// skipCallNames is every *testing.T/B/F method that skips a test without
// running the rest of it. SkipNow is the most silent of the three — it
// carries no message at all — so it is matched exactly as Skip/Skipf are,
// not treated as a lesser case.
var skipCallNames = map[string]bool{
	"Skip":    true,
	"Skipf":   true,
	"SkipNow": true,
}

// scanSkipSites walks every "*_test.go" file directly under dir (never
// recursive — internal/livee2e is a flat, single-directory package, checked
// against `find` before this scanner was written) and returns one skipSite
// per Skip-family call found inside a top-level function declaration,
// including calls nested in subtest closures (`t.Run(name, func(t
// *testing.T) {...})`) — ast.Inspect descends into the closure literal
// without leaving the enclosing FuncDecl's body, so the call is still
// attributed to the outer, named Test function.
//
// Named limitations, checked rather than assumed away. (1) A Skip call
// inside a closure assigned to a package-level var (not a FuncDecl at all)
// would not be attributed to any Func name and so would not be found. No
// such construct exists in this package today (grep confirmed exactly one
// Skip-family call, inside a plain top-level Test function) — if one is
// added later, this scanner needs widening, not just a new registry row.
// (2) The match is on selector NAME alone — any `x.Skip()`/`x.Skipf()`/
// `x.SkipNow()`, not only a call on a `*testing.T`/`*testing.B`/`*testing.F`
// receiver — so a same-named method on an unrelated type would demand a
// (harmless, honest) declaration rather than silently pass unseen. No such
// type exists in this package today; a `go/types`-checked match would close
// this precisely but adds a whole-package type-check to a gate that must
// stay cheap enough to run on every commit.
func scanSkipSites(dir string) ([]skipSite, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("livee2e: scanSkipSites: read %s: %w", dir, err)
	}

	fset := token.NewFileSet()
	var sites []skipSite
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil, fmt.Errorf("livee2e: scanSkipSites: parse %s: %w", path, perr)
		}

		// Per-function raw hits, sorted into source order before Occurrence
		// numbers are assigned, so Occurrence always means "Nth Skip call by
		// line" regardless of ast.Inspect's own traversal order.
		type hit struct {
			fn   string
			line int
		}
		var hits []hit
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if !skipCallNames[sel.Sel.Name] {
					return true
				}
				hits = append(hits, hit{fn: fn.Name.Name, line: fset.Position(call.Pos()).Line})
				return true
			})
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].line < hits[j].line })

		occurrence := map[string]int{}
		for _, h := range hits {
			occurrence[h.fn]++
			sites = append(sites, skipSite{
				File:       e.Name(),
				Func:       h.fn,
				Occurrence: occurrence[h.fn],
				Line:       h.line,
			})
		}
	}

	sort.Slice(sites, func(i, j int) bool {
		if sites[i].File != sites[j].File {
			return sites[i].File < sites[j].File
		}
		if sites[i].Func != sites[j].Func {
			return sites[i].Func < sites[j].Func
		}
		return sites[i].Occurrence < sites[j].Occurrence
	})
	return sites, nil
}

// writeFixture is the synthetic-source helper the scanner's own unit tests
// use below — an isolated t.TempDir() rather than the real package
// directory, so these tests never depend on (or risk mutating) this
// package's own live skip inventory.
func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

const fixturePackageHeader = "package fixture\n\nimport \"testing\"\n\n"

func TestScanSkipSitesFindsSkipSkipfAndSkipNow(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a_test.go", fixturePackageHeader+`
func TestA(t *testing.T) {
	t.Skip("reason a")
}

func TestB(t *testing.T) {
	t.Skipf("reason %d", 1)
}

func TestC(t *testing.T) {
	t.SkipNow()
}
`)
	sites, err := scanSkipSites(dir)
	if err != nil {
		t.Fatalf("scanSkipSites: %v", err)
	}
	if len(sites) != 3 {
		t.Fatalf("scanSkipSites found %d site(s), want 3: %+v", len(sites), sites)
	}
	want := map[string]bool{"TestA": true, "TestB": true, "TestC": true}
	for _, s := range sites {
		if s.Occurrence != 1 {
			t.Errorf("%s: Occurrence = %d, want 1", s.Func, s.Occurrence)
		}
		delete(want, s.Func)
	}
	if len(want) != 0 {
		t.Errorf("scanSkipSites did not find: %v", want)
	}
}

func TestScanSkipSitesIgnoresNonTestGoFiles(t *testing.T) {
	dir := t.TempDir()
	// A plain .go file (no "_test.go" suffix) that takes *testing.T and
	// calls Skip — mirrors pathdriver_live.go's own shape (a helper that
	// takes *testing.T without itself being a test file). Must NOT be
	// scanned: it is not part of "this package's test surface" as this
	// scope is defined (skipdeclarations.go's own doc comment).
	writeFixture(t, dir, "helper.go", fixturePackageHeader+`
func Helper(t *testing.T) {
	t.Skip("should never be found")
}
`)
	sites, err := scanSkipSites(dir)
	if err != nil {
		t.Fatalf("scanSkipSites: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("scanSkipSites found %d site(s) in a non-_test.go file, want 0: %+v", len(sites), sites)
	}
}

func TestScanSkipSitesIgnoresSkipShapedStringLiterals(t *testing.T) {
	dir := t.TempDir()
	// The exact hazard a text grep (not an AST scan) would fall into: a
	// string that reads like a Skip call but is not one.
	writeFixture(t, dir, "a_test.go", fixturePackageHeader+`
func TestA(t *testing.T) {
	msg := "call t.Skip(\"x\") if this fails"
	t.Log(msg)
}
`)
	sites, err := scanSkipSites(dir)
	if err != nil {
		t.Fatalf("scanSkipSites: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("scanSkipSites found %d site(s) in a Skip-shaped string literal, want 0: %+v", len(sites), sites)
	}
}

func TestScanSkipSitesAssignsOccurrenceInSourceOrderWithinOneFunc(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a_test.go", fixturePackageHeader+`
func TestA(t *testing.T) {
	if true {
		t.Skip("first")
	}
	t.Skip("second")
}
`)
	sites, err := scanSkipSites(dir)
	if err != nil {
		t.Fatalf("scanSkipSites: %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("scanSkipSites found %d site(s), want 2: %+v", len(sites), sites)
	}
	if sites[0].Occurrence != 1 || sites[1].Occurrence != 2 {
		t.Fatalf("occurrences = %d, %d; want 1, 2 (source order): %+v", sites[0].Occurrence, sites[1].Occurrence, sites)
	}
	if sites[0].Line >= sites[1].Line {
		t.Fatalf("site order does not match source line order: %+v", sites)
	}
}

func TestScanSkipSitesAttributesASubtestClosureSkipToTheOuterFunc(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a_test.go", fixturePackageHeader+`
func TestA(t *testing.T) {
	t.Run("sub", func(t *testing.T) {
		t.Skip("in a subtest closure")
	})
}
`)
	sites, err := scanSkipSites(dir)
	if err != nil {
		t.Fatalf("scanSkipSites: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("scanSkipSites found %d site(s), want 1: %+v", len(sites), sites)
	}
	if sites[0].Func != "TestA" {
		t.Fatalf("subtest closure Skip attributed to %q, want %q", sites[0].Func, "TestA")
	}
}

// TestSkipDeclarationsCoverEverySkip is the union gate: every Skip-family
// call site scanSkipSites finds in this package's real "*_test.go" files
// must have exactly one matching entry in skipDeclarations() carrying a
// non-empty reason, and skipDeclarations() must name no site that is not
// actually there — the same shape TestUndrivenScenariosNameOnlyDeclaredIdsWithReasons
// (scenariocoverage_test.go) and TestPathDrivabilityCoversEveryPath
// (pathdrivability_test.go) already apply to their own universes.
//
// MUTATION PROOF, run by hand and reported in the wave that added this test
// (never asserted, always executed and reverted):
//   - blank an entry's Reason -> reds, naming the site
//   - delete the one declared entry -> reds, naming the now-undeclared site
//   - rename a declared entry's Func to one that does not exist -> reds,
//     naming the stale declaration
//   - add a second, undeclared t.Skip to protection_test.go's already-
//     declared function -> reds on Occurrence 2, proving the per-occurrence
//     key (not per-function) actually catches a second skip hiding behind
//     an already-accepted first one
//   - add a brand-new t.Skip with no declaration anywhere to an allowlisted
//     _test.go file -> reds, naming the new file and function
func TestSkipDeclarationsCoverEverySkip(t *testing.T) {
	sites, err := scanSkipSites(".")
	if err != nil {
		t.Fatalf("scanSkipSites: %v", err)
	}

	declared := map[string]skipDeclaration{}
	for _, d := range skipDeclarations() {
		if strings.TrimSpace(d.Reason) == "" {
			t.Errorf("skipDeclarations() entry %s#%s[%d] carries an empty reason", d.File, d.Func, d.Occurrence)
		}
		key := d.key()
		if _, dup := declared[key]; dup {
			t.Errorf("skipDeclarations() names %s#%s[%d] twice", d.File, d.Func, d.Occurrence)
			continue
		}
		declared[key] = d
	}

	found := map[string]bool{}
	for _, s := range sites {
		found[s.key()] = true
		if _, ok := declared[s.key()]; !ok {
			t.Errorf("%s: %s contains a Skip-family call (occurrence %d, line %d) with no matching entry "+
				"in skipDeclarations() — every skip in this tier's test surface must be declared, with the "+
				"reason (P8 AC5, spec 08-paths.md §8)", s.File, s.Func, s.Occurrence, s.Line)
		}
	}

	for key, d := range declared {
		if !found[key] {
			t.Errorf("skipDeclarations() names %s#%s[%d], but no Skip-family call was found there — a stale declaration", d.File, d.Func, d.Occurrence)
		}
		_ = key
	}
}
