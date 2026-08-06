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

// A lane-claims path must satisfy Derive's claim test WITHOUT the claiming
// gate being selected by it — the gate is KindAlways and is in the lane
// regardless. Discriminating: an ALWAYS gate with NO Claims must still refuse
// the same path, so the test fails if claimedByAlways is deleted OR if it is
// widened to "any ALWAYS declaration claims everything".
func TestDeriveAlwaysClaimsSatisfyTheClaimWithoutSelecting(t *testing.T) {
	withClaims := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "judges commit subjects", Claims: []string{"docs/status.md"}},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	sel, refusals := Derive(withClaims, []string{"docs/status.md"})
	if len(refusals) != 0 {
		t.Fatalf("a lane-claims path must not be refused, got: %+v", refusals)
	}
	if len(sel.Phases) != 1 || sel.Phases[0].Declaration.Phase != "epic-drift" {
		t.Fatalf("expected only the ALWAYS gate selected, got %+v", sel.Phases)
	}
	if len(sel.Phases[0].Matched) != 0 {
		t.Fatalf("a claim must not register as a MATCH — that would read as though the path selected the gate: %+v", sel.Phases[0].Matched)
	}

	noClaims := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "judges commit subjects"},
		{Phase: "readme-lint", Kind: KindScoped, Inputs: []string{"README.md"}},
	}
	_, refusals = Derive(noClaims, []string{"docs/status.md"})
	if len(refusals) != 1 {
		t.Fatalf("an ALWAYS gate with no Claims must still leave the path unclaimed, got %d refusals: %+v", len(refusals), refusals)
	}
}
