package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// --- construction call sites --------------------------------------------

// TestWireGoWriteConstructionSitesUseTheCriteriaWrapper is a structural
// gate (mirrors internal/cache/pendency_callsite_test.go's own AST shape),
// scoped to wire.go — the one file this wave's brief allows editing. The
// unit tests above prove mirrorResolverWithCriteria itself works, but
// nothing behavioural in this package's normal test tier reaches
// resolveLifecycleDepsWithPolicy/runSubmit end-to-end (both need a real
// git remote + GitHub host). Without this, wire.go's two `resolver :=`
// lines could silently regress back to bare cli.NewMirrorResolver — same
// validate.Resolver interface, so it would still compile and every OTHER
// test would still pass — and REF-018 would go dormant again in
// production with nothing here to say so.
//
// DEVIATION (reported, not fixed — off this wave's allowlist):
// contract_p6_wiring.go and work_wiring.go each build their OWN
// cli.NewSubmitValidatorAdapter over a bare cli.NewMirrorResolver — the
// `a2a contract <sub>` and `a2a work` write paths, neither of which this
// gate can touch or claims to cover. REF-018 stays dormant on those two
// paths after this wave; this test is deliberately scoped to wire.go
// alone rather than the whole package so it does not fail for a gap this
// wave's allowlist does not license fixing.
func TestWireGoWriteConstructionSitesUseTheCriteriaWrapper(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(".", "wire.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse wire.go: %v", err)
	}

	var bareMirrorResolverCalls []string
	wrapperCalls := 0

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			pkg, ok := fn.X.(*ast.Ident)
			if ok && pkg.Name == "cli" && fn.Sel.Name == "NewMirrorResolver" {
				bareMirrorResolverCalls = append(bareMirrorResolverCalls, fset.Position(call.Pos()).String())
			}
		case *ast.Ident:
			if fn.Name == "newMirrorResolverWithCriteria" {
				wrapperCalls++
			}
		}
		return true
	})

	if len(bareMirrorResolverCalls) > 0 {
		t.Fatalf("cli.NewMirrorResolver called directly in wire.go — this bypasses the ParentCriteriaCounter wrapper, so REF-018 goes dormant again for that write path: %s",
			strings.Join(bareMirrorResolverCalls, ", "))
	}
	if wrapperCalls < 2 {
		t.Fatalf("expected newMirrorResolverWithCriteria called at both known write-construction sites in wire.go (resolveLifecycleDepsWithPolicy, runSubmit), found %d call site(s)", wrapperCalls)
	}
}

// --- findMirrorArtifactPath -------------------------------------------

func TestFindMirrorArtifactPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "axon", "exchanges"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "axon", "exchanges", "XW-axon-20260808-p9d3.md")
	if err := os.WriteFile(target, []byte("parent artifact"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, found := findMirrorArtifactPath(dir, "XW-axon-20260808-p9d3")
	if !found {
		t.Fatalf("findMirrorArtifactPath: expected found=true")
	}
	if got != target {
		t.Errorf("findMirrorArtifactPath = %q, want %q", got, target)
	}

	if _, found := findMirrorArtifactPath(dir, "XW-axon-does-not-exist"); found {
		t.Errorf("findMirrorArtifactPath: expected found=false for an unknown id")
	}
}

// --- AcceptanceCriteriaCount --------------------------------------------

func writeParentArtifact(t *testing.T, dir, id, criteriaYAML string) {
	t.Helper()
	raw := "---\nschema: envelope/v1\nid: " + id + "\ntype: work_request\ntitle: t\nspace: getvisa\nfrom: axon\nto: [seomatrix]\nthread: thread:axon-1\nactor: {kind: agent, name: codex}\ncreated: \"2026-08-08T08:40:00Z\"\npriority: p3\nblocking: true\nclassification: internal\ncategory: feature\nproposed_change: x\n" +
		criteriaYAML + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptanceCriteriaCount(t *testing.T) {
	t.Parallel()

	t.Run("declared criteria count", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeParentArtifact(t, dir, "XW-axon-20260808-p9d3", "acceptance_criteria: [\"a\", \"b\", \"c\"]")
		r := newMirrorResolverWithCriteria(dir, space.Manifest{})

		count, ok := r.AcceptanceCriteriaCount("XW-axon-20260808-p9d3")
		if !ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=true")
		}
		if count != 3 {
			t.Errorf("AcceptanceCriteriaCount = %d, want 3", count)
		}
	})

	t.Run("unknown parent degrades to not-ok", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		r := newMirrorResolverWithCriteria(dir, space.Manifest{})

		count, ok := r.AcceptanceCriteriaCount("XW-axon-does-not-exist")
		if ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=false for an unresolvable parent, got count=%d", count)
		}
	})

	t.Run("absent acceptance_criteria degrades to not-ok, never a bare zero", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// A handoff never declares acceptance_criteria at all — the field is
		// absent from its frontmatter, not present-and-empty.
		raw := "---\nschema: envelope/v1\nid: XH-axon-20260808-f3s5\ntype: handoff\ntitle: t\nspace: getvisa\nfrom: axon\nto: [seomatrix]\nthread: thread:axon-1\nactor: {kind: agent, name: codex}\ncreated: \"2026-08-08T08:40:00Z\"\npriority: p3\nblocking: true\nclassification: internal\ndeliverables: []\n---\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, "XH-axon-20260808-f3s5.md"), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		r := newMirrorResolverWithCriteria(dir, space.Manifest{})

		count, ok := r.AcceptanceCriteriaCount("XH-axon-20260808-f3s5")
		if ok {
			t.Fatalf("AcceptanceCriteriaCount: expected ok=false for an absent field, got count=%d", count)
		}
	})
}

// TestMirrorResolverWithCriteria_FiresREF018InProductionShape is the
// production-shape proof this wave's brief asks for: not the package's
// own criteriaResolver fake, but the REAL resolver wire.go now
// constructs (newMirrorResolverWithCriteria, wrapping cli.MirrorResolver
// by embedding), reading a REAL on-disk parent artifact through the REAL
// validate.Engine, over the SAME real corpus newEngine() loads.
func TestMirrorResolverWithCriteria_FiresREF018InProductionShape(t *testing.T) {
	t.Parallel()
	engine, err := newEngine()
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}

	mirrorDir := t.TempDir()
	writeParentArtifact(t, mirrorDir, "XW-axon-20260808-p9d3", "acceptance_criteria: [\"a\"]")
	manifest := space.Manifest{Participants: []space.Participant{{System: "seomatrix", Status: "active"}}}
	resolver := newMirrorResolverWithCriteria(mirrorDir, manifest)

	raw := []byte("---\n" + responseV2Header("XS-axon-20260808-p9d3", "XW-axon-20260808-p9d3", 5) + "---\nBody.\n")

	result, err := engine.ValidateForSubmit(
		validate.Draft{Path: "axon/exchanges/XS-axon-20260808-p9d3.md", Raw: raw},
		nil,
		validate.LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if result.Valid {
		t.Fatalf("expected refusal for an out-of-range unmet index against the real mirror resolver, got Valid=true")
	}
	if !hasViolationCode(result.Violations, "REF-018") {
		t.Fatalf("expected REF-018 among violations, got %+v", result.Violations)
	}

	// The in-range half of the same production path: index 0 fits the
	// parent's single declared criterion, so nothing fires.
	inRange := []byte("---\n" + responseV2Header("XS-axon-20260808-p9d4", "XW-axon-20260808-p9d3", 0) + "---\nBody.\n")
	result, err = engine.ValidateForSubmit(
		validate.Draft{Path: "axon/exchanges/XS-axon-20260808-p9d4.md", Raw: inRange},
		nil,
		validate.LocalContext{OwnSystem: "axon", Resolver: resolver},
	)
	if err != nil {
		t.Fatalf("ValidateForSubmit: %v", err)
	}
	if hasViolationCode(result.Violations, "REF-018") {
		t.Fatalf("expected no REF-018 for an in-range unmet index, got %+v", result.Violations)
	}
}

// responseV2Header builds a schema-valid envelope/v2 response frontmatter
// body (mirrors internal/validate/incompleteness_test.go's own
// responseV2Base, an unexported test-only const this package cannot
// import) naming id/parent/the single unmet index this test varies.
func responseV2Header(id, parent string, unmetIndex int) string {
	return "schema: envelope/v2\n" +
		"id: " + id + "\n" +
		"type: response\n" +
		"title: A valid v2 response\n" +
		"space: getvisa\n" +
		"from: axon\n" +
		"to: [seomatrix]\n" +
		"thread: thread:axon-20260808-k3f9\n" +
		"actor: {kind: agent, name: codex}\n" +
		"created: \"2026-08-08T08:40:00Z\"\n" +
		"priority: p3\n" +
		"blocking: true\n" +
		"classification: internal\n" +
		"parent: " + parent + "\n" +
		"result: partial\n" +
		"unmet: [" + strconv.Itoa(unmetIndex) + "]\n" +
		"blocked_by:\n" +
		"  reason_code: out-of-scope\n" +
		"  owner: seomatrix\n" +
		"  needs: bytes\n"
}

func hasViolationCode(vs []validate.Violation, code string) bool {
	for _, v := range vs {
		if v.Code == code {
			return true
		}
	}
	return false
}
