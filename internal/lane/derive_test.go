package lane

import "testing"

func TestDeriveUnclaimedPathRefuses(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	sel, refusals := Derive(decls, []string{"skill/a2ahub/loops.md"})
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if len(sel.Phases) != 0 {
		t.Fatalf("expected no phases selected, got %+v", sel.Phases)
	}
}

func TestDeriveUnionOfMultipleGates(t *testing.T) {
	decls := []Declaration{
		{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}},
		{Phase: "vet", Kind: KindScoped, Inputs: []string{"**/*.go"}},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	sel, refusals := Derive(decls, []string{"internal/lane/derive.go"})
	if len(refusals) != 0 {
		t.Fatalf("unexpected refusals: %+v", refusals)
	}
	names := map[string]bool{}
	for _, p := range sel.Phases {
		names[p.Declaration.Phase] = true
	}
	if !names["go-test"] || !names["vet"] {
		t.Fatalf("expected the diff to select BOTH go-test and vet (union), got %v", names)
	}
	if names["readme-lint"] {
		t.Fatalf("readme-lint's input did not intersect the diff, should not be selected")
	}
}

func TestDeriveAlwaysAlwaysSelected(t *testing.T) {
	decls := []Declaration{
		{Phase: "classify-guard", Kind: KindAlways, Reason: "reads the whole tracked set"},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	sel, refusals := Derive(decls, []string{"README.md"})
	if len(refusals) != 0 {
		t.Fatalf("unexpected refusals: %+v", refusals)
	}
	names := map[string]bool{}
	for _, p := range sel.Phases {
		names[p.Declaration.Phase] = true
	}
	if !names["classify-guard"] {
		t.Fatalf("KindAlways must always run, got %v", names)
	}
}

func TestDeriveNeverIsNeverSelected(t *testing.T) {
	decls := []Declaration{
		{Phase: "live-e2e", Kind: KindNever, Reason: "needs two credentials, network and a real GitHub space"},
	}
	sel, refusals := Derive(decls, []string{"schemas/envelope/v2/contract.schema.json"})
	if len(refusals) != 1 {
		// The path is genuinely unclaimed since KindNever has no Inputs.
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	for _, p := range sel.Phases {
		if p.Declaration.Phase == "live-e2e" {
			t.Fatalf("live-e2e (KindNever) must never be auto-selected")
		}
	}
}

func TestDeriveMatchedPathsRecorded(t *testing.T) {
	decls := []Declaration{{Phase: "go-test-scoped:./internal/notes/...", Kind: KindScoped, Inputs: []string{"releasenotes/**"}}}
	sel, refusals := Derive(decls, []string{"releasenotes/v0.7.0.yaml", "README.md"})
	if len(refusals) != 1 {
		t.Fatalf("README.md should be unclaimed, got %d refusals: %+v", len(refusals), refusals)
	}
	if len(sel.Phases) != 1 {
		t.Fatalf("got %d phases, want 1: %+v", len(sel.Phases), sel.Phases)
	}
	got := sel.Phases[0].Matched
	if len(got) != 1 || got[0].Path != "releasenotes/v0.7.0.yaml" || got[0].Pattern != "releasenotes/**" {
		t.Fatalf("Matched = %+v", got)
	}
}
