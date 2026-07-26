// mutationguard_test.go guards the invariant mutateTree exists to state:
// every working-tree mutation in this package happens while this process
// holds THAT mirror's lock.
//
// The invariant has two enforcers and this file tests both, because they
// fail in different directions:
//
//   - The COMPILER enforces "you have a lock at all" — a mutating helper
//     takes a *MirrorLock parameter, so an unlocked call does not build.
//     That is the strong half, and it needs no test.
//   - mutateTree enforces "the lock is for THIS mirror" — the half a type
//     cannot express. TestMutateTree* below are its teeth.
//   - The STRUCTURAL rule (TestNoUnguardedMutationSite) catches the case
//     neither of the above can: a future author adding a SIXTH mutation
//     site through a fresh helper that never takes a lock. The compiler is
//     happy with that; only a scan of the sources notices.
//
// What the structural rule does NOT prove, stated plainly so nobody reads
// more into a green run than it earns: it checks that a function running a
// mutating git subcommand ACCEPTS a *MirrorLock, not that the lock it was
// handed is live, nor that the caller acquired it correctly. That last mile
// is mutateTree's runtime check, which is why both halves exist.
package space

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/testkit/spacefixture"
)

func TestMutateTreeRefusesANilLock(t *testing.T) {
	t.Parallel()

	if err := mutateTree(nil, "/some/mirror"); err == nil {
		t.Fatal("mutateTree(nil, dir) = nil — an unlocked mutation must be refused, " +
			"that is the whole point of the guard")
	}
}

func TestMutateTreeRefusesALockForADifferentMirror(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	held := filepath.Join(t.TempDir(), "held")
	other := filepath.Join(t.TempDir(), "other")
	for _, dir := range []string{held, other} {
		if err := CloneOrFetch(context.Background(), dir, fx.RemoteURL()); err != nil {
			t.Fatalf("seed clone %s: %v", dir, err)
		}
	}

	lock, err := AcquireMirrorLock(context.Background(), held)
	if err != nil {
		t.Fatalf("AcquireMirrorLock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	// Holding *a* lock is not the invariant. This is the case the type
	// system cannot see: the call compiles, the lock is real and live, and
	// it guards the wrong mirror.
	if err := mutateTree(lock, other); err == nil {
		t.Fatalf("mutateTree accepted a lock held on %s to mutate %s — "+
			"a lock for a different mirror serialises nothing", held, other)
	}
	if err := mutateTree(lock, held); err != nil {
		t.Fatalf("mutateTree refused the lock actually held on %s: %v", held, err)
	}
}

// TestCommitOneRefusesWithoutTheMirrorsOwnLock drives the guard through
// the real write path rather than calling it directly, so a future
// refactor that keeps mutateTree but stops routing commitOne through it
// still reds here.
func TestCommitOneRefusesWithoutTheMirrorsOwnLock(t *testing.T) {
	t.Parallel()

	fx := spacefixture.New(t, "axon")
	dir := filepath.Join(t.TempDir(), "mirror")
	if err := CloneOrFetch(context.Background(), dir, fx.RemoteURL()); err != nil {
		t.Fatalf("seed clone: %v", err)
	}

	f := &WriteFunnel{}
	req := SubmitRequest{
		RepoDir:    dir,
		System:     "axon",
		Verb:       "submit",
		ArtifactID: "XQ-axon-20260726-guard",
		Files:      []FileWrite{{Path: "axon/exchanges/XQ-axon-20260726-guard.md", Content: []byte("body\n")}},
	}

	_, _, err := f.commitOne(context.Background(), nil, req, "a2a/axon/submit/XQ-axon-20260726-guard")
	if err == nil {
		t.Fatal("commitOne committed with no mirror lock — this is the crossed-write path")
	}
	if !strings.Contains(err.Error(), "without holding its lock") {
		t.Fatalf("commitOne error = %v, want the guard's own refusal", err)
	}
}

// --- the structural rule ----------------------------------------------------

// mutatingSubcommands are the git subcommands that change a working tree,
// an index, or a local ref. A read-only subcommand (log, show, ls-tree,
// rev-parse, diff, symbolic-ref, status) is deliberately absent: it cannot
// destroy a concurrent writer's work, and requiring a lock for it would
// serialise every read behind every write for no gain.
//
// `fetch` and `clone` are absent for a different reason each, and both are
// deliberate: `clone` cannot hold a lock (there is no .git yet — see
// mutateTree's doc), and `fetch` updates remote-tracking refs only, never
// the working tree or the index.
var mutatingSubcommands = map[string]bool{
	"add": true, "am": true, "apply": true, "branch": true, "checkout": true,
	"cherry-pick": true, "clean": true, "commit": true, "merge": true,
	"rebase": true, "reset": true, "restore": true, "rm": true, "stash": true,
	"switch": true, "update-ref": true, "worktree": true,
}

// mutationSite is one function in this package's production sources that
// passes a mutating git subcommand literal to some call.
type mutationSite struct {
	Func       string
	Line       int
	Subcommand string
	TakesLock  bool
}

// findMutationSites parses one Go source file and reports every mutating
// git site in it, attributed to its enclosing function. Pure: no
// filesystem access, so the teeth below can drive it on literals.
func findMutationSites(path, source string) ([]mutationSite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return nil, err
	}

	var sites []mutationSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		takesLock := funcTakesMirrorLock(fn)
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil || !mutatingSubcommands[val] {
					continue
				}
				sites = append(sites, mutationSite{
					Func:       fn.Name.Name,
					Line:       fset.Position(call.Pos()).Line,
					Subcommand: val,
					TakesLock:  takesLock,
				})
			}
			return true
		})
	}
	return sites, nil
}

// funcTakesMirrorLock reports whether fn accepts a *MirrorLock parameter —
// the structural evidence that its mutation is lock-scoped.
func funcTakesMirrorLock(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, field := range fn.Type.Params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "MirrorLock" {
			return true
		}
	}
	return false
}

// mutationGuardExemptFuncs are the functions allowed to run a mutating
// subcommand without taking a lock. Every entry needs a reason that
// survives review, because each one is a hole in the invariant.
var mutationGuardExemptFuncs = map[string]string{
	// mutateTree IS the guard; its "subcommand" mentions are in its own
	// doc-adjacent error text, not a git call.
	"mutateTree": "the guard itself",
}

func TestNoUnguardedMutationSite(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	var checked []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sites, err := findMutationSites(name, string(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, site := range sites {
			checked = append(checked, name+":"+site.Func+"/"+site.Subcommand)
			if site.TakesLock {
				continue
			}
			if _, exempt := mutationGuardExemptFuncs[site.Func]; exempt {
				continue
			}
			offenders = append(offenders,
				name+":"+itoa(site.Line)+" "+site.Func+"() runs `git "+site.Subcommand+"` but takes no *MirrorLock")
		}
	}

	if len(checked) == 0 {
		t.Fatal("scanned the package and found NO mutating git site — the scanner is broken, " +
			"not the package (funnel.go and mirror.go both mutate)")
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("unguarded mirror mutation(s):\n  %s\n\n"+
			"A shared mirror mutated without holding its lock is the crossed/lost-write class "+
			"the live matrix found in July 2026. Take a *MirrorLock parameter and call mutateTree, "+
			"or add the function to mutationGuardExemptFuncs WITH a reason.",
			strings.Join(offenders, "\n  "))
	}
}

// TestFindMutationSitesReportsBothDirections is the scanner's own teeth: a
// gate that cannot fail is worth exactly as much as no gate, and this one
// is the only thing standing behind the compiler.
func TestFindMutationSitesReportsBothDirections(t *testing.T) {
	t.Parallel()

	const src = `package p

func unguarded(ctx C, dir string) error {
	return runGit(ctx, dir, "reset", "--hard", "origin/main")
}

func guarded(ctx C, lock *MirrorLock, dir string) error {
	return runGit(ctx, dir, "checkout", "-B", "b", "origin/main")
}

func readOnly(ctx C, dir string) error {
	return runGit(ctx, dir, "log", "--oneline")
}
`
	sites, err := findMutationSites("x.go", src)
	if err != nil {
		t.Fatalf("findMutationSites: %v", err)
	}

	got := map[string]bool{}
	for _, s := range sites {
		got[s.Func+"/"+s.Subcommand] = s.TakesLock
	}

	takesLock, found := got["unguarded/reset"]
	if !found {
		t.Error("missed the unguarded `reset` — the scanner would not have caught the lost write")
	} else if takesLock {
		t.Error("reported the unguarded function as lock-taking")
	}

	takesLock, found = got["guarded/checkout"]
	if !found {
		t.Error("missed the guarded `checkout` site")
	} else if !takesLock {
		t.Error("a function taking *MirrorLock was reported as unguarded — this would red a correct tree")
	}

	if _, found := got["readOnly/log"]; found {
		t.Error("flagged `git log` as a mutation — a read-only subcommand must not need the lock, " +
			"or every read serialises behind every write")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
