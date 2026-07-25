package livee2e

import (
	"strings"
	"testing"
)

// TestFoldKindSetDerivesTheEightKnownKinds is a sanity check on the PARSER
// itself, not the coverage gate: it confirms foldKindSet's AST walk actually
// finds internal/fold/types.go's eight `Kind`-typed consts, so a break in the
// parser (a moved file, a renamed type) fails loudly HERE instead of
// silently returning an empty set — which would make
// TestEveryFoldKindHasALiveRow below pass VACUOUSLY (an empty expected set
// has no missing members, and "the gate always passes" is worse than no
// gate at all, since it looks like coverage).
func TestFoldKindSetDerivesTheEightKnownKinds(t *testing.T) {
	t.Parallel()
	kinds, err := foldKindSet()
	if err != nil {
		t.Fatalf("foldKindSet: %v", err)
	}
	want := []string{"announcement", "contract", "decision", "handoff", "question", "requirement", "response", "work_request"}
	if len(kinds) != len(want) {
		t.Fatalf("foldKindSet = %v (%d kinds), want %d: %v", kinds, len(kinds), len(want), want)
	}
	for i, k := range want {
		if kinds[i] != k {
			t.Errorf("kinds[%d] = %q, want %q (full: %v)", i, kinds[i], k, kinds)
		}
	}
}

// TestMissingCoveredKindsFindsAGap is missingCoveredKinds' own unit test — a
// SYNTHETIC gap, not a real catalogue mutation, so this test stays green
// permanently while still proving the comparison logic itself reds on an
// uncovered kind.
//
// TEETH: this is the mechanical half of AC-983.1's "delete a kind's rows
// from the catalogue and the gate must red naming that kind" requirement.
// The other half — proving it against the REAL Catalogue() — was done by
// hand this wave (every Kinds: []string{"decision"} entry removed from
// catalogue.go; TestEveryFoldKindHasALiveRow reds naming "decision"; the
// edit was then reverted and the suite reran green) rather than committed
// as a test that mutates the real catalogue, which would leave the
// production matrix broken for every OTHER test in this package.
func TestMissingCoveredKindsFindsAGap(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "a", Kinds: []string{"contract"}},
		{Name: "b", Kinds: []string{"question", "handoff"}},
	}
	got := missingCoveredKinds([]string{"contract", "question", "handoff", "decision"}, scenarios)
	if len(got) != 1 || got[0] != "decision" {
		t.Fatalf("missingCoveredKinds = %v, want [decision]", got)
	}
}

func TestMissingCoveredKindsEmptyWhenFullyCovered(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{{Name: "a", Kinds: []string{"contract", "question"}}}
	got := missingCoveredKinds([]string{"contract", "question"}, scenarios)
	if len(got) != 0 {
		t.Fatalf("missingCoveredKinds = %v, want empty", got)
	}
}

// TestUnknownDeclaredKindsFindsATypo is unknownDeclaredKinds' own unit
// test — AC-983.1's REVERSE direction: a Scenario.Kinds entry that names
// something outside the real corpus (a typo, a renamed/removed kind) must
// be caught too, or the Kinds field itself rots exactly the way a hand-typed
// second list would.
func TestUnknownDeclaredKindsFindsATypo(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{
		{Name: "good-row", Kinds: []string{"contract"}},
		{Name: "typo-row", Kinds: []string{"Contract"}}, // capitalized — not the real value
		{Name: "stale-row", Kinds: []string{"widget"}},  // never a real fold.Kind
	}
	got := unknownDeclaredKinds([]string{"contract", "question"}, scenarios)
	want := []string{`stale-row: "widget"`, `typo-row: "Contract"`}
	if len(got) != len(want) {
		t.Fatalf("unknownDeclaredKinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("unknownDeclaredKinds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestUnknownDeclaredKindsEmptyWhenAllValid(t *testing.T) {
	t.Parallel()
	scenarios := []Scenario{{Name: "a", Kinds: []string{"contract", "question"}}}
	got := unknownDeclaredKinds([]string{"contract", "question", "decision"}, scenarios)
	if len(got) != 0 {
		t.Fatalf("unknownDeclaredKinds = %v, want empty", got)
	}
}

// TestEveryFoldKindHasALiveRow is AC-983.1 itself: the matrix is "complete"
// only if every kind internal/fold defines has at least one Catalogue() row
// naming it in Kinds, AND every Kinds entry any row declares actually names
// a real fold.Kind. A new or renamed envelope kind merging with no Layer-1
// live coverage — or a row's Kinds field silently drifting from the real
// corpus — must red HERE, in the suite `make check` runs, not surface only
// once every 40 minutes during a live run nobody re-triggers on a whim.
func TestEveryFoldKindHasALiveRow(t *testing.T) {
	t.Parallel()
	kinds, err := foldKindSet()
	if err != nil {
		t.Fatalf("foldKindSet: %v", err)
	}
	catalogue := Catalogue()

	if missing := missingCoveredKinds(kinds, catalogue); len(missing) > 0 {
		t.Errorf("these internal/fold kinds have no live Catalogue() row covering them: %s — a new or renamed envelope kind merged with no Layer-1 live coverage (spec 38 AC-983.1)",
			strings.Join(missing, ", "))
	}
	if unknown := unknownDeclaredKinds(kinds, catalogue); len(unknown) > 0 {
		t.Errorf("these Scenario.Kinds entries name something internal/fold does not define: %s — a typo, or a kind renamed/removed in fold.Kind that a catalogue row's Kinds field never followed (spec 38 AC-983.1)",
			strings.Join(unknown, ", "))
	}
}
