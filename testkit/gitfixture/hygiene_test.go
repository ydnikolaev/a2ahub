// hygiene_test.go is the structural gate that keeps every git-spawning
// test site routed through this package (see gitfixture.go's package doc
// for the flake it exists to prevent). Three rules — R1 and R2 below, and
// R3 (every bare repo a fixture pushes into is hardened) documented at its
// own section further down, next to the code that implements it:
//
//   - R1: every *_test.go anywhere in the module, and every .go under
//     testkit/, that contains a git exec site must route THAT SITE'S OWN
//     subcommand-args argument through gitfixture.Args(...) — not merely
//     "the file mentions gitfixture somewhere". A file-level check would
//     leave every second-and-later site in a multi-site file unguarded:
//     reverting exactly one of two sites in (say)
//     testkit/spacefixture/spacefixture.go would still leave the file
//     "referencing gitfixture" through its other, still-guarded site, and
//     the gate would stay green over a live regression. Per-site detection
//     closes that gap uniformly (all 18 known sites are single-site-per-
//     call already, so this is strictly stronger, not different, for them).
//   - R2: every package whose NON-test .go files contain a git exec site
//     must have a TestMain calling gitfixture.RunTests or gitfixture.HardenEnv
//     (either counts as evidence — internal/e2e satisfies it via HardenEnv
//     called from a delegate runTestMain, not literally inside TestMain's
//     own body, so the search covers the whole package's test sources, not
//     just TestMain's function literal). testkit/** is EXCLUDED from R2's
//     scanned package set: it is test infrastructure, not "production code
//     running under test" — its git sites are fixture helpers hardened at
//     the argv by R1, and R1's Args() flags cover any gc --auto grandchild
//     that specific git invocation forks (git propagates a `-c` flag's
//     effect to its own children via the same GIT_CONFIG_* env mechanism,
//     verified empirically — see gitfixture.go's package doc), so there is
//     no detached-goroutine spawn path there for an environment hook to
//     close. R2 exists specifically for PRODUCTION code invoking git while
//     the process it runs in is under test. internal/e2e and cmd/a2a are
//     added as explicit extras (not discoverable by this file-scan): they
//     exec the real `a2a` binary / drive production wiring (space.CloneOrFetch)
//     in-process, so their git spawn is a GRANDCHILD of code this scan
//     cannot see by grepping for a literal "git" argument.
//
// Detection is factored into pure functions over (path, source string) so
// both rules — and both directions of each (a violation IS reported; a
// compliant site/package is NOT) — are unit-testable without touching the
// repo. See TestFindGitSites_Table and TestR2Missing_Table for the teeth.
package gitfixture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// gitSite is one exec.Command/exec.CommandContext call whose first COMMAND
// argument (arg 0 for Command, arg 1 for CommandContext, since the leading
// arg there is the context.Context) is the literal "git".
type gitSite struct {
	Line    int
	Guarded bool
}

// findGitSites parses source (one Go file's content) and returns every git
// exec site in it, in source order. Pure: no filesystem access.
func findGitSites(path, source string) ([]gitSite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var sites []gitSite
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || pkgIdent.Name != "exec" {
			return true
		}

		var cmdArgIdx int
		switch sel.Sel.Name {
		case "Command":
			cmdArgIdx = 0
		case "CommandContext":
			cmdArgIdx = 1
		default:
			return true
		}
		if len(call.Args) <= cmdArgIdx {
			return true
		}
		lit, ok := call.Args[cmdArgIdx].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil || val != "git" {
			return true
		}

		sites = append(sites, gitSite{
			Line:    fset.Position(call.Pos()).Line,
			Guarded: isGuardedSite(call, cmdArgIdx),
		})
		return true
	})
	return sites, nil
}

// isGuardedSite reports whether any argument AFTER the "git" command
// literal is itself a call to gitfixture.Args(...) — the per-site evidence
// R1 requires.
func isGuardedSite(call *ast.CallExpr, cmdArgIdx int) bool {
	for _, arg := range call.Args[cmdArgIdx+1:] {
		if isGitfixtureCall(arg, "Args") {
			return true
		}
	}
	return false
}

// isGitfixtureCall reports whether expr is a call to gitfixture.<name>(...).
func isGitfixtureCall(expr ast.Expr, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "gitfixture"
}

// isGitfixtureOwnFile reports whether path is inside testkit/gitfixture
// itself — the owner, excluded from both rules' scans (R1 per the brief;
// R2 because gitfixture.go itself never calls exec.Command).
func isGitfixtureOwnFile(path string) bool {
	return strings.HasPrefix(path, "testkit/gitfixture/")
}

// r1Applies reports whether path is in scope for R1: any *_test.go file
// anywhere, or any .go file under testkit/, excluding testkit/gitfixture's
// own files.
func r1Applies(path string) bool {
	if isGitfixtureOwnFile(path) {
		return false
	}
	if strings.HasSuffix(path, "_test.go") {
		return true
	}
	return strings.HasPrefix(path, "testkit/")
}

// r1Violations returns one message per UNGUARDED git exec site in path
// (path must satisfy r1Applies; callers filter before calling).
func r1Violations(path, source string) ([]string, error) {
	sites, err := findGitSites(path, source)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range sites {
		if !s.Guarded {
			out = append(out, fmt.Sprintf("%s:%d: git exec site not routed through gitfixture.Args", path, s.Line))
		}
	}
	return out, nil
}

// r2Applies reports whether path is in scope for R2's production-package
// scan: a NON-test .go file outside testkit/.
func r2Applies(path string) bool {
	if strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !strings.HasPrefix(path, "testkit/")
}

// hasTestMainDecl reports whether source declares `func TestMain(m *testing.M)`.
func hasTestMainDecl(path, source string) (bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" {
			continue
		}
		return true, nil
	}
	return false, nil
}

// hasGitfixtureHookCall reports whether source contains a call to
// gitfixture.RunTests(...) or gitfixture.HardenEnv(...) ANYWHERE — not
// scoped to inside TestMain's own function body, since internal/e2e's
// TestMain delegates to runTestMain, a sibling function in the same file,
// which is where the HardenEnv call actually lives.
func hasGitfixtureHookCall(path, source string) (bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if isGitfixtureCall(call, "RunTests") || isGitfixtureCall(call, "HardenEnv") {
			found = true
			return false
		}
		return true
	})
	return found, nil
}

// r2Missing computes, from already-collected per-package booleans (plus
// any explicit extra package paths), which packages are missing the
// gitfixture hook. Pure over its inputs — no filesystem access — so both
// directions (a qualifying-and-hooked package is NOT reported; a
// qualifying-and-unhooked one IS) are directly unit-testable.
func r2Missing(pkgHasGitSite, pkgHasTestMain, pkgHasHook map[string]bool, extras ...string) []string {
	qualifies := map[string]bool{}
	for pkg, has := range pkgHasGitSite {
		if has {
			qualifies[pkg] = true
		}
	}
	for _, e := range extras {
		qualifies[e] = true
	}

	pkgs := make([]string, 0, len(qualifies))
	for pkg := range qualifies {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)

	var missing []string
	for _, pkg := range pkgs {
		if !pkgHasTestMain[pkg] {
			missing = append(missing, fmt.Sprintf("%s: no TestMain (want one calling gitfixture.RunTests)", pkg))
			continue
		}
		if !pkgHasHook[pkg] {
			missing = append(missing, fmt.Sprintf("%s: has a TestMain but no gitfixture.RunTests/HardenEnv call found anywhere in the package's test files", pkg))
		}
	}
	return missing
}

// --- the gate itself (filesystem-bound harness around the pure functions above) ---

// repoRoot resolves the product repo root from this source file's own
// location — testkit/gitfixture/hygiene_test.go -> ../.. is the repo root
// — the same robust approach internal/e2e/main_test.go:66 uses, rather
// than assuming the test's cwd.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("hygiene: runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("hygiene: resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err != nil {
		t.Fatalf("hygiene: resolved root %s has no go.mod: %v", abs, err)
	}
	return abs
}

// walkGoFiles calls fn(relPath, source) for every .go file in the module,
// skipping .git, testdata, and vendor. This is filesystem-bound harness
// code, not a "detection" function — the pure logic under test lives in
// fn's own body / the functions it delegates to.
func walkGoFiles(t *testing.T, root string, fn func(relPath, source string)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(rel, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("hygiene: walk %s: %v", root, err)
	}
}

func TestHygiene_R1_EveryGitExecSiteIsGuarded(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	var violations []string
	scanned := 0
	walkGoFiles(t, root, func(rel, source string) {
		if !r1Applies(rel) {
			return
		}
		scanned++
		v, err := r1Violations(rel, source)
		if err != nil {
			t.Fatalf("hygiene R1: %v", err)
		}
		violations = append(violations, v...)
	})

	// Without this, the gate reports green when it inspected NOTHING — and it
	// selects its inputs by path convention (r1Applies), so a directory move
	// empties it silently. "No violations found" and "no files examined" are
	// indistinguishable in the output that matters.
	if scanned == 0 {
		t.Fatal("R1 examined no files — r1Applies matched nothing, so this gate would pass vacuously")
	}

	if len(violations) > 0 {
		t.Fatalf("R1: git exec site(s) not routed through gitfixture.Args (%d file(s) examined):\n%s", scanned, strings.Join(violations, "\n"))
	}
}

func TestHygiene_R2_ProductionGitPackagesHaveTheHook(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	pkgHasGitSite := map[string]bool{}
	pkgHasTestMain := map[string]bool{}
	pkgHasHook := map[string]bool{}

	walkGoFiles(t, root, func(rel, source string) {
		pkg := filepath.ToSlash(filepath.Dir(rel))

		if r2Applies(rel) {
			sites, err := findGitSites(rel, source)
			if err != nil {
				t.Fatalf("hygiene R2: %v", err)
			}
			if len(sites) > 0 {
				pkgHasGitSite[pkg] = true
			}
		}

		if strings.HasSuffix(rel, "_test.go") {
			hasMain, err := hasTestMainDecl(rel, source)
			if err != nil {
				t.Fatalf("hygiene R2: %v", err)
			}
			if hasMain {
				pkgHasTestMain[pkg] = true
			}
			hasHook, err := hasGitfixtureHookCall(rel, source)
			if err != nil {
				t.Fatalf("hygiene R2: %v", err)
			}
			if hasHook {
				pkgHasHook[pkg] = true
			}
		}
	})

	// internal/e2e execs the real `a2a` binary and internal/e2e /
	// cmd/a2a both drive production wiring (space.CloneOrFetch) in-process
	// — their git spawn is a GRANDCHILD this file-scan structurally cannot
	// see (see this file's package doc).
	missing := r2Missing(pkgHasGitSite, pkgHasTestMain, pkgHasHook, "internal/e2e", "cmd/a2a")
	if len(missing) > 0 {
		t.Fatalf("R2: package(s) with a production git exec site missing the gitfixture hook:\n%s", strings.Join(missing, "\n"))
	}
}

// --- teeth: both directions, for both rules ---------------------------

func TestFindGitSites_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		source      string
		wantGuarded []bool // len == number of expected sites
	}{
		{
			name: "unguarded git exec.Command IS reported",
			source: `package fx
import "os/exec"
func f() { exec.Command("git", "status") }
`,
			wantGuarded: []bool{false},
		},
		{
			name: "git exec.Command routed through gitfixture.Args is NOT a violation",
			source: `package fx
import (
	"os/exec"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)
func f() { exec.Command("git", gitfixture.Args("status")...) }
`,
			wantGuarded: []bool{true},
		},
		{
			name: "a non-git exec.Command is NOT reported at all",
			source: `package fx
import "os/exec"
func f() { exec.Command("ls", "-la") }
`,
			wantGuarded: nil,
		},
		{
			name: "unguarded exec.CommandContext (command arg is index 1) IS reported",
			source: `package fx
import (
	"context"
	"os/exec"
)
func f(ctx context.Context) { exec.CommandContext(ctx, "git", "status") }
`,
			wantGuarded: []bool{false},
		},
		{
			name: "guarded exec.CommandContext is NOT a violation",
			source: `package fx
import (
	"context"
	"os/exec"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)
func f(ctx context.Context) { exec.CommandContext(ctx, "git", gitfixture.Args("status")...) }
`,
			wantGuarded: []bool{true},
		},
		{
			name: "two sites in one file are tracked independently",
			source: `package fx
import (
	"os/exec"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)
func a() { exec.Command("git", gitfixture.Args("status")...) }
func b() { exec.Command("git", "log") }
`,
			wantGuarded: []bool{true, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sites, err := findGitSites("fx_test.go", tt.source)
			if err != nil {
				t.Fatalf("findGitSites: %v", err)
			}
			if len(sites) != len(tt.wantGuarded) {
				t.Fatalf("got %d sites, want %d: %+v", len(sites), len(tt.wantGuarded), sites)
			}
			for i, want := range tt.wantGuarded {
				if sites[i].Guarded != want {
					t.Fatalf("site %d Guarded = %v, want %v", i, sites[i].Guarded, want)
				}
			}
		})
	}
}

func TestR1Violations_ReportsFileAndLine(t *testing.T) {
	t.Parallel()

	source := "package fx\n" +
		"import \"os/exec\"\n" +
		"func f() {\n" +
		"\texec.Command(\"git\", \"status\")\n" +
		"}\n"

	got, err := r1Violations("internal/example/fx_test.go", source)
	if err != nil {
		t.Fatalf("r1Violations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d violations, want 1: %v", len(got), got)
	}
	if !strings.Contains(got[0], "internal/example/fx_test.go:4:") {
		t.Fatalf("violation %q does not name file and line 4", got[0])
	}
}

func TestHasGitfixtureHookCall_Table(t *testing.T) {
	t.Parallel()

	directCall := `package cache
import (
	"os"
	"testing"
	"github.com/ydnikolaev/a2ahub/testkit/gitfixture"
)
func TestMain(m *testing.M) { os.Exit(gitfixture.RunTests(m)) }
`
	noHook := `package cache
import (
	"os"
	"testing"
)
func TestMain(m *testing.M) { os.Exit(m.Run()) }
`
	delegated := `package e2e
import "os"
import "testing"
func TestMain(m *testing.M) { os.Exit(runTestMain(m)) }
func runTestMain(m *testing.M) int {
	gitfixture.HardenEnv()
	return m.Run()
}
`

	if hasMain, hasHook := mustHooks(t, directCall); !hasMain || !hasHook {
		t.Fatalf("directCall: hasMain=%v hasHook=%v, want true/true", hasMain, hasHook)
	}
	if hasMain, hasHook := mustHooks(t, noHook); !hasMain || hasHook {
		t.Fatalf("noHook: hasMain=%v hasHook=%v, want true/false (qualifying package missing the hook IS reported)", hasMain, hasHook)
	}
	if hasMain, hasHook := mustHooks(t, delegated); !hasMain || !hasHook {
		t.Fatalf("delegated: hasMain=%v hasHook=%v, want true/true (HardenEnv called from a sibling func still counts)", hasMain, hasHook)
	}
}

func mustHooks(t *testing.T, source string) (hasMain, hasHook bool) {
	t.Helper()
	hasMain, err := hasTestMainDecl("main_test.go", source)
	if err != nil {
		t.Fatalf("hasTestMainDecl: %v", err)
	}
	hasHook, err = hasGitfixtureHookCall("main_test.go", source)
	if err != nil {
		t.Fatalf("hasGitfixtureHookCall: %v", err)
	}
	return hasMain, hasHook
}

func TestR2Missing_Table(t *testing.T) {
	t.Parallel()

	missing := r2Missing(
		map[string]bool{"internal/cache": true, "internal/space": true},
		map[string]bool{"internal/cache": true}, // internal/space has no TestMain at all
		map[string]bool{"internal/cache": true},
	)
	if len(missing) != 1 || !strings.Contains(missing[0], "internal/space") {
		t.Fatalf("expected exactly internal/space reported missing, got %v", missing)
	}

	// A package with a TestMain that never calls the hook IS reported too.
	missing = r2Missing(
		map[string]bool{"internal/host": true},
		map[string]bool{"internal/host": true},
		map[string]bool{}, // TestMain exists but never calls RunTests/HardenEnv
	)
	if len(missing) != 1 || !strings.Contains(missing[0], "internal/host") {
		t.Fatalf("expected internal/host reported missing (TestMain present, hook absent), got %v", missing)
	}

	// A fully-compliant package is NOT reported.
	clean := r2Missing(
		map[string]bool{"internal/mcp": true},
		map[string]bool{"internal/mcp": true},
		map[string]bool{"internal/mcp": true},
	)
	if len(clean) != 0 {
		t.Fatalf("expected no violations for a compliant package, got %v", clean)
	}

	// Explicit extras qualify even with an empty gitSite map (internal/e2e,
	// cmd/a2a's own grandchild-spawn case).
	extrasMissing := r2Missing(map[string]bool{}, map[string]bool{}, map[string]bool{}, "internal/e2e")
	if len(extrasMissing) != 1 || !strings.Contains(extrasMissing[0], "internal/e2e") {
		t.Fatalf("expected internal/e2e (explicit extra) reported missing, got %v", extrasMissing)
	}
	extrasClean := r2Missing(map[string]bool{}, map[string]bool{"internal/e2e": true}, map[string]bool{"internal/e2e": true}, "internal/e2e")
	if len(extrasClean) != 0 {
		t.Fatalf("expected internal/e2e (explicit extra, hooked) NOT reported, got %v", extrasClean)
	}
}

func TestR1Applies_ExcludesGitfixtureOwnFiles(t *testing.T) {
	t.Parallel()
	if r1Applies("testkit/gitfixture/gitfixture_test.go") {
		t.Fatal("r1Applies should exclude testkit/gitfixture's own files (it is the owner)")
	}
	if !r1Applies("testkit/spacefixture/spacefixture.go") {
		t.Fatal("r1Applies should cover non-test .go files under testkit/")
	}
	if !r1Applies("internal/cli/cmd_init_test.go") {
		t.Fatal("r1Applies should cover any *_test.go file")
	}
	if r1Applies("internal/cli/cmd_init.go") {
		t.Fatal("r1Applies should NOT cover a production .go file outside testkit/")
	}
}

func TestR2Applies_ExcludesTestFilesAndTestkit(t *testing.T) {
	t.Parallel()
	if r2Applies("internal/space/mirror_test.go") {
		t.Fatal("r2Applies should exclude _test.go files")
	}
	if r2Applies("testkit/spacefixture/spacefixture.go") {
		t.Fatal("r2Applies should exclude testkit/**")
	}
	if !r2Applies("internal/space/mirror.go") {
		t.Fatal("r2Applies should cover a production .go file outside testkit/")
	}
}

// --- R3: every bare repo a fixture pushes into is hardened ------------------
//
// R1 and R2 cover the two spawn paths a client-side setting can reach. The
// third — git-receive-pack's post-push `git maintenance run --auto --detach`
// inside the RECEIVING repo — is reachable only from that repo's own config
// (gitfixture.go's package doc, mechanism 3). It was closed by hand at four
// sites on 2026-08-12, which is precisely the state that needs a gate: a
// fifth `git init --bare` reintroduces the flake with nothing objecting.
//
// The rule is function-scoped rather than line-scoped on purpose. A fixture
// typically creates several repos and hardens them a few lines later, past
// intervening setup; requiring adjacency would force a cosmetic ordering the
// rule does not actually care about.

// bareInitFunctions returns the name and line of every function in source
// that creates a bare git repository — detected as a call carrying the
// literal "--bare" among its arguments, which covers both a direct
// exec.Command("git", ...) and the runGit/fxRunGit helper wrappers every
// fixture in this repo actually uses.
func bareInitFunctions(path, source string) ([]gitSite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []gitSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		bareLine := 0
		hardened := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isGitfixtureCall(call, "HardenRepo") || isGitfixtureCall(call, "HardenRepoDir") {
				hardened = true
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil || val != "--bare" {
					continue
				}
				if bareLine == 0 {
					bareLine = fset.Position(call.Pos()).Line
				}
			}
			return true
		})
		if bareLine != 0 {
			out = append(out, gitSite{Line: bareLine, Guarded: hardened})
		}
	}
	return out, nil
}

// r3Violations returns one message per function that creates a bare repo
// without hardening it (path must satisfy r1Applies; callers filter).
func r3Violations(path, source string) ([]string, error) {
	sites, err := bareInitFunctions(path, source)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range sites {
		if !s.Guarded {
			out = append(out, fmt.Sprintf(
				"%s:%d: creates a bare git repo but never calls gitfixture.HardenRepo on it "+
					"(receive-pack will spawn detached maintenance into it after every push)", path, s.Line))
		}
	}
	return out, nil
}

func TestHygiene_R3_EveryBareRepoIsHardened(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)

	var violations []string
	found := 0
	walkGoFiles(t, root, func(rel, source string) {
		if !r1Applies(rel) {
			return
		}
		sites, err := bareInitFunctions(rel, source)
		if err != nil {
			t.Fatalf("hygiene R3: %v", err)
		}
		found += len(sites)
		v, err := r3Violations(rel, source)
		if err != nil {
			t.Fatalf("hygiene R3: %v", err)
		}
		violations = append(violations, v...)
	})

	// Same reason R1 counts what it scanned: "no violations" and "found no
	// bare-repo sites at all" print identically, and this rule selects its
	// inputs by a string literal that a refactor could rename away.
	if found == 0 {
		t.Fatal("R3 found no bare-repo creation sites — the detection literal no longer matches anything, " +
			"so this gate would pass vacuously")
	}

	if len(violations) > 0 {
		t.Fatalf("R3: bare repo(s) created without gitfixture.HardenRepo (%d site(s) found):\n%s",
			found, strings.Join(violations, "\n"))
	}
}

// TestR3Violations_Table is R3's teeth, both directions.
func TestR3Violations_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source string
		want   int
	}{
		{
			name:   "bare init with no HardenRepo IS reported",
			source: "package p\nfunc f(t *testing.T) {\n\trunGit(t, d, \"init\", \"--bare\", origin)\n}\n",
			want:   1,
		},
		{
			name:   "bare init followed by HardenRepo is NOT a violation",
			source: "package p\nfunc f(t *testing.T) {\n\trunGit(t, d, \"init\", \"--bare\", origin)\n\tgitfixture.HardenRepo(t, origin)\n}\n",
			want:   0,
		},
		{
			name:   "HardenRepo in a DIFFERENT function does not cover this one",
			source: "package p\nfunc g(t *testing.T) { gitfixture.HardenRepo(t, x) }\nfunc f(t *testing.T) {\n\trunGit(t, d, \"init\", \"--bare\", origin)\n}\n",
			want:   1,
		},
		{
			name:   "a non-bare init is out of scope",
			source: "package p\nfunc f(t *testing.T) {\n\trunGit(t, d, \"init\", \"-b\", \"main\", work)\n}\n",
			want:   0,
		},
		{
			name:   "clone --bare counts too — it also makes a pushable bare repo",
			source: "package p\nfunc f(t *testing.T) {\n\trunGit(t, d, \"clone\", \"--bare\", src, origin)\n}\n",
			want:   1,
		},
		{
			name:   "the error-returning HardenRepoDir satisfies the rule as well",
			source: "package p\nfunc f() error {\n\trunCmd(\"git\", \"clone\", \"--bare\", src, origin)\n\treturn gitfixture.HardenRepoDir(origin)\n}\n",
			want:   0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := r3Violations("x_test.go", tc.source)
			if err != nil {
				t.Fatalf("r3Violations: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("r3Violations = %d violation(s) %v, want %d", len(got), got, tc.want)
			}
		})
	}
}
