// writefloor_test.go is the structural gate for CC-085's reach: every
// production site that builds a space.SubmitRequest must set
// MinBinaryVersion.
//
// It exists because the guard is opt-in by shape. The funnel enforces the
// floor only `if req.MinBinaryVersion != ""` (internal/space/funnel.go) —
// a perfectly reasonable design, since a request with no floor to check is
// not an error. The consequence is that FORGETTING the field is silent:
// nothing fails, nothing warns, and the write simply is not gated. Until
// 2026-07-26 exactly three of six construction sites set it, so the floor
// an operator raised in space.yaml bound `a2a submit` and nothing else —
// not one of the 15 lifecycle verbs, and not `contract publish`, which is
// the verb that actually produced a duplicate PR in a live space.
//
// This gate lives in internal/e2e rather than beside either builder for
// the reason the drift itself demonstrates: the CLI and MCP builders are
// deliberate mirrors of each other, and a rule that lives inside one of
// them is a rule the other can drift away from. It scans both.
package e2e

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// submitRequestSite is one composite literal of type space.SubmitRequest
// that actually carries fields (a bare `space.SubmitRequest{}` returned
// alongside an error is not a write and is skipped).
type submitRequestSite struct {
	File     string
	Line     int
	Func     string
	SetsML   bool
	NumField int
}

// findSubmitRequestSites reports every populated space.SubmitRequest
// literal in one Go source, attributed to its enclosing function. Pure —
// the teeth below drive it on string literals.
func findSubmitRequestSites(path, source string) ([]submitRequestSite, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, source, 0)
	if err != nil {
		return nil, err
	}

	var sites []submitRequestSite
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "SubmitRequest" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "space" {
				return true
			}
			// A zero-value literal is an error return, not a write.
			if len(lit.Elts) == 0 {
				return true
			}

			setsML := false
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "MinBinaryVersion" {
					setsML = true
				}
			}
			sites = append(sites, submitRequestSite{
				File: path, Line: fset.Position(lit.Pos()).Line,
				Func: fn.Name.Name, SetsML: setsML, NumField: len(lit.Elts),
			})
			return true
		})
	}
	return sites, nil
}

// writeFloorExempt maps "<file>:<func>" to the reason that site legitimately
// builds a write with no floor. Each entry is a hole in CC-085's reach and
// has to earn its place.
var writeFloorExempt = map[string]string{
	// Feedback writes to the PRODUCT's feedback repo (s.cfg.RemoteURL,
	// system "feedback", into feedback/inbox/) — not to the user's space.
	// The user's space.yaml floor is simply not the applicable pin: there
	// is no manifest for that repo in these deps, and inventing one would
	// gate a write on a floor that belongs to a different repository.
	"internal/feedback/submit.go:Submit": "writes to the product feedback repo, not the user's space — no space manifest applies",
}

func TestEverySubmitRequestCarriesTheWriteFloor(t *testing.T) {
	t.Parallel()

	root := repoRootForFloorScan(t)

	var offenders, checked []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		sites, perr := findSubmitRequestSites(rel, string(raw))
		if perr != nil {
			return perr
		}
		for _, site := range sites {
			key := site.File + ":" + site.Func
			checked = append(checked, key)
			if site.SetsML {
				continue
			}
			if _, ok := writeFloorExempt[key]; ok {
				continue
			}
			offenders = append(offenders, key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(checked) == 0 {
		t.Fatal("found NO populated space.SubmitRequest literal in the whole module — " +
			"the scanner is broken, not the module")
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("space.SubmitRequest built without MinBinaryVersion:\n  %s\n\n"+
			"The funnel checks the floor only when the field is set, so omitting it silently "+
			"un-gates the write — that is how `contract publish` stayed writable by a binary "+
			"older than the space's own pin. Set it from the parsed manifest, or add the site "+
			"to writeFloorExempt WITH a reason.",
			strings.Join(offenders, "\n  "))
	}
}

// TestFindSubmitRequestSitesReportsBothDirections is the scanner's teeth.
func TestFindSubmitRequestSitesReportsBothDirections(t *testing.T) {
	t.Parallel()

	const src = `package p

func withFloor() space.SubmitRequest {
	return space.SubmitRequest{RepoDir: d, MinBinaryVersion: m}
}

func withoutFloor() space.SubmitRequest {
	return space.SubmitRequest{RepoDir: d, Verb: "publish"}
}

func errorReturn() (space.SubmitRequest, error) {
	return space.SubmitRequest{}, err
}

func notOurType() other.SubmitRequest {
	return other.SubmitRequest{RepoDir: d}
}
`
	sites, err := findSubmitRequestSites("x.go", src)
	if err != nil {
		t.Fatalf("findSubmitRequestSites: %v", err)
	}

	byFunc := map[string]bool{}
	for _, s := range sites {
		byFunc[s.Func] = s.SetsML
	}

	if setsML, found := byFunc["withoutFloor"]; !found {
		t.Error("missed the un-floored write — the gate would not have caught the real drift")
	} else if setsML {
		t.Error("reported an un-floored write as floored")
	}
	if setsML, found := byFunc["withFloor"]; !found {
		t.Error("missed the floored site")
	} else if !setsML {
		t.Error("a site that sets MinBinaryVersion was reported as missing it — this would red a correct tree")
	}
	if _, found := byFunc["errorReturn"]; found {
		t.Error("flagged a zero-value error return as a write; every builder has several and they are not writes")
	}
	if _, found := byFunc["notOurType"]; found {
		t.Error("matched a SubmitRequest from a different package")
	}
}

// repoRootForFloorScan walks up from the test's working directory to the
// module root (the directory holding go.mod).
func repoRootForFloorScan(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found walking up from the test's working directory")
		}
		dir = parent
	}
}
