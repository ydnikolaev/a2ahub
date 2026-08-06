package lane

import "testing"

func TestReconcileMissingDeclaration(t *testing.T) {
	corpus := []Phase{{Name: "readme-lint", Source: "Makefile:88"}}
	refusals := Reconcile(nil, corpus)
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if refusals[0].Subject != "readme-lint" {
		t.Errorf("Subject = %q, want readme-lint", refusals[0].Subject)
	}
}

func TestReconcileOrphanDeclaration(t *testing.T) {
	decls := []Declaration{{Phase: "ghost-gate", Kind: KindScoped, Inputs: []string{"x"}, Source: "scripts/ghost.sh:1"}}
	refusals := Reconcile(decls, nil)
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if refusals[0].Subject != "scripts/ghost.sh:1" {
		t.Errorf("Subject = %q, want the declaration's Source", refusals[0].Subject)
	}
}

func TestReconcileDerivedGoTestScopedIsExempt(t *testing.T) {
	decls := []Declaration{{
		Phase:  "go-test-scoped:./internal/notes/...",
		Kind:   KindScoped,
		Inputs: []string{"releasenotes/**"},
		Source: "internal/notes/doc.go:5",
	}}
	// No corpus entry for it at all — it must not be flagged an orphan.
	refusals := Reconcile(decls, nil)
	if len(refusals) != 0 {
		t.Fatalf("derived go-test-scoped declaration should be exempt from corpus membership, got %+v", refusals)
	}
}

func TestReconcileHappyPath(t *testing.T) {
	corpus := []Phase{{Name: "readme-lint", Source: "Makefile:88"}}
	decls := []Declaration{{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}, Source: "scripts/check-readme.sh:6"}}
	refusals := Reconcile(decls, corpus)
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals, got %+v", refusals)
	}
}
