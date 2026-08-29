package lane

import (
	"strings"
	"testing"
)

func TestCoverageUnclaimedPath(t *testing.T) {
	decls := []Declaration{
		{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}},
	}
	refusals := Coverage(decls, []string{"skill/a2ahub/loops.md"}, nil, nil)
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
	refusals := Coverage(decls, []string{"integrations/macos-notifier/Package.swift"}, []string{"integrations/macos-notifier/Package.swift"}, nil)
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
		[]string{"integrations/macos-notifier/**"}, nil)
	if len(refusals) != 0 {
		t.Fatalf("a glob ungated entry should cover every path under its root, got %+v", refusals)
	}
}

func TestCoverageKindAlwaysClaimsNothing(t *testing.T) {
	decls := []Declaration{
		{Phase: "classify-guard", Kind: KindAlways, Reason: "reads the whole tracked set"},
	}
	refusals := Coverage(decls, []string{"skill/a2ahub/loops.md"}, nil, nil)
	if len(refusals) != 1 {
		t.Fatalf("KindAlways must not count as claiming — got %+v", refusals)
	}
}

func TestCoverageAlwaysWithClaimsCoversThePath(t *testing.T) {
	decls := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "check A runs git log with no pathspec", Claims: []string{"docs/status.md", "docs/features/**/tracker.yaml"}},
	}
	backing := map[string]PhaseBacking{
		"epic-drift": {BackedPatterns: map[string]bool{"docs/status.md": true}},
	}
	refusals := Coverage(decls, []string{"docs/status.md"}, nil, backing)
	if len(refusals) != 0 {
		t.Fatalf("lane-claims: path should be covered, got %+v", refusals)
	}
}

func TestCoverageAlwaysWithClaimsDoesNotCoverOtherPaths(t *testing.T) {
	decls := []Declaration{
		{Phase: "epic-drift", Kind: KindAlways, Reason: "check A runs git log with no pathspec", Claims: []string{"docs/status.md"}},
	}
	refusals := Coverage(decls, []string{"skill/a2ahub/loops.md"}, nil, nil)
	if len(refusals) != 1 {
		t.Fatalf("lane-claims: is narrow — it must not cover an unrelated path, got %+v", refusals)
	}
}

func TestCoverageClaimedPath(t *testing.T) {
	decls := []Declaration{{Phase: "go-test", Kind: KindScoped, Inputs: []string{"**/*.go"}}}
	backing := map[string]PhaseBacking{
		"go-test": {BackedPatterns: map[string]bool{"**/*.go": true}},
	}
	refusals := Coverage(decls, []string{"internal/lane/derive.go"}, nil, backing)
	if len(refusals) != 0 {
		t.Fatalf("expected no refusals, got %+v", refusals)
	}
}

// TestCoverageClaimedButUnbackedPathReds pins the defect this phase exists
// to close: release-notes-freshness declares internal/** and reads none of
// it — internal/release/trusted_root.json is CLAIMED but the claim is not
// BACKED by anything the extractor found, and no lane-reads-opaque line
// covers it. Before P1 this counted as coverage; now it must reach the
// CLAIMED-BUT-UNBACKED refusal, naming the phase and the glob.
func TestCoverageClaimedButUnbackedPathReds(t *testing.T) {
	decls := []Declaration{
		{Phase: "release-notes-freshness", Kind: KindScoped, Inputs: []string{"internal/**"}},
	}
	refusals := Coverage(decls, []string{"internal/release/trusted_root.json"}, nil, nil)
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want 1: %+v", len(refusals), refusals)
	}
	r := refusals[0]
	if r.Subject != "internal/release/trusted_root.json" {
		t.Errorf("Subject = %q", r.Subject)
	}
	if !containsAll(r.Problem, `phase "release-notes-freshness"`, "internal/release/trusted_root.json", "internal/**") {
		t.Errorf("Problem does not name the phase and the glob: %+v", r)
	}
	if !containsAll(r.Fix, "narrow", "lane-reads-opaque") {
		t.Errorf("Fix does not name both legal fixes (narrow the glob, or declare it opaque): %+v", r)
	}
}

// TestCoverageOpaqueDirectiveDoesNotLaunderAnUnbackedGlob is the
// discriminating case for claimVerdict's own design choice: a phase's
// lane-reads-opaque directive suppresses D-9's per-line refusal for the
// unresolved construct it accompanies, but it must NOT retroactively back
// every OTHER glob the same phase declares. `projection`'s own `**`
// directive is exactly this shape — true, and legitimately unresolvable for
// the ONE construct it covers, but it must not launder an unrelated,
// never-read path into "covered". Opaque alone, with no BackedPatterns and
// no NoSubject, must still red.
func TestCoverageOpaqueDirectiveBacksTheGlobsItsPhaseDeclares(t *testing.T) {
	decls := []Declaration{
		{Phase: "projection", Kind: KindScoped, Inputs: []string{"**"}},
	}
	backing := map[string]PhaseBacking{
		"projection": {Opaque: true},
	}
	refusals := Coverage(decls, []string{"internal/release/trusted_root.json"}, nil, backing)
	if len(refusals) != 0 {
		t.Fatalf("an honest lane-reads-opaque directive is evidence of a read and must back its phase's globs, got %+v", refusals)
	}
}

// TestCoverageNoDirectiveAndNoReadStillReds is the half of the
// anti-laundering concern that survives the restore, and it is the one that
// matters: a directive is evidence, but its ABSENCE is still decisive. A
// phase that neither resolves a read nor says why it cannot reaches the
// CLAIMED-BUT-UNBACKED refusal. Without this, "opaque backs the phase"
// would have no floor under it.
func TestCoverageNoDirectiveAndNoReadStillReds(t *testing.T) {
	decls := []Declaration{
		{Phase: "silent-gate", Kind: KindScoped, Inputs: []string{"internal/**"}},
	}
	backing := map[string]PhaseBacking{
		"silent-gate": {BackedPatterns: map[string]bool{"docs/**": true}},
	}
	refusals := Coverage(decls, []string{"internal/release/trusted_root.json"}, nil, backing)
	if len(refusals) != 1 {
		t.Fatalf("a phase with a resolved read elsewhere, no directive, and nothing backing this glob must red, got %+v", refusals)
	}
	if !containsAll(refusals[0].Problem, "silent-gate", "internal/**") {
		t.Fatalf("the refusal must name the phase and the deciding glob, got %q", refusals[0].Problem)
	}
}

// TestCoverageOneBackedAndOneUnbackedClaimantStaysCovered is the "does not
// double-punish" edge case: two declarations claim the SAME path, one
// backed and one not. The path must stay covered — a single backed
// claimant is enough, the same way a single ungated entry or a single
// matching Inputs pattern already is.
func TestCoverageOneBackedAndOneUnbackedClaimantStaysCovered(t *testing.T) {
	decls := []Declaration{
		{Phase: "backed-gate", Kind: KindScoped, Inputs: []string{"docs/**"}},
		{Phase: "unbacked-gate", Kind: KindScoped, Inputs: []string{"docs/**"}},
	}
	backing := map[string]PhaseBacking{
		"backed-gate": {BackedPatterns: map[string]bool{"docs/**": true}},
	}
	refusals := Coverage(decls, []string{"docs/guide.md"}, nil, backing)
	if len(refusals) != 0 {
		t.Fatalf("one backed claimant must be enough, got %+v", refusals)
	}
}

// containsAll reports whether s contains every substring in want — a small
// local helper so the refusal-text assertions above read as a checklist
// rather than a chain of strings.Contains calls.
func containsAll(s string, want ...string) bool {
	for _, w := range want {
		if !strings.Contains(s, w) {
			return false
		}
	}
	return true
}
