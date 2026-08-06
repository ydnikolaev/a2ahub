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

// TestCoverageUngatedListIsGlobNotExactString is the discriminating case
// TestCoverageUngatedListSuppressesRefusal cannot be: that test passes the
// SAME literal string as both the universe entry and the ungated entry, so
// it stays green whether ungated is matched as a glob or as an exact-string
// set. D-6's real adjudication is root-shaped ("integrations/macos-
// notifier/**" covering 40 files in one line, lane-ungated.txt's own
// documented grammar) — an exact-string set would silently match none of
// them.
func TestCoverageUngatedListIsGlobNotExactString(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	refusals := Coverage(decls,
		[]string{"integrations/macos-notifier/Package.swift", "integrations/macos-notifier/Sources/main.swift"},
		[]string{"integrations/macos-notifier/**"})
	if len(refusals) != 0 {
		t.Fatalf("a glob ungated entry should cover every path under its root, got %+v", refusals)
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

func TestCoverageAlwaysWithClaimsCoversThePath(t *testing.T) {
	decls := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "check A runs git log with no pathspec", Claims: []string{"docs/status.md", "docs/features/**/tracker.yaml"}},
	}
	refusals := Coverage(decls, []string{"docs/status.md"}, nil)
	if len(refusals) != 0 {
		t.Fatalf("lane-claims: path should be covered, got %+v", refusals)
	}
}

func TestCoverageAlwaysWithClaimsDoesNotCoverOtherPaths(t *testing.T) {
	decls := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "check A runs git log with no pathspec", Claims: []string{"docs/status.md"}},
	}
	refusals := Coverage(decls, []string{"skill/a2ahub/loops.md"}, nil)
	if len(refusals) != 1 {
		t.Fatalf("lane-claims: is narrow — it must not cover an unrelated path, got %+v", refusals)
	}
}

func TestCoverageClaimedPath(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	refusals := Coverage(decls, []string{"internal/lane/derive.go"}, nil)
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals, got %+v", refusals)
	}
}
