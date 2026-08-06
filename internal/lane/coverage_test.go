package lane

import "testing"

func TestCoverageUnclaimedPath(t *testing.T) {
	decls := []Declaration{
		{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}},
	}
	refusals := Coverage(decls, []string{"skill/a2ahub/loops.md"}, nil)
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	if refusals[0].Subject != "skill/a2ahub/loops.md" {
		t.Errorf("Subject = %q", refusals[0].Subject)
	}
	want := "lane: no gate claims skill/a2ahub/loops.md — add it to a gate's inputs or declare it explicitly ungated"
	if got := refusals[0].String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestCoverageUngatedListSuppressesRefusal(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	refusals := Coverage(decls, []string{"integrations/macos-notifier/Package.swift"}, []string{"integrations/macos-notifier/Package.swift"})
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals for an explicitly ungated path, got %+v", refusals)
	}
}

func TestCoverageKindAlwaysClaimsNothing(t *testing.T) {
	decls := []Declaration{
		{Phase: "classify-guard", Kind: KindAlways, Reason: "reads the whole tracked set"},
	}
	refusals := Coverage(decls, []string{"skill/a2ahub/loops.md"}, nil)
	if len(refusals) != 1 {
		t.Fatalf("KindAlways must not count as claiming — got %+v", refusals)
	}
}

func TestCoverageClaimedPath(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	refusals := Coverage(decls, []string{"internal/lane/derive.go"}, nil)
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals, got %+v", refusals)
	}
}
