package spacenotify

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/pendency"
)

func TestClassify_HumanGateWinsOverP1(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{
		Priority: "p1", Addressees: []string{"seomatrix"},
		Verdict: pendency.Verdict{HumanGate: "G3"},
	}
	if got := classify(fa); got != ClassHumanGate {
		t.Fatalf("classify = %q, want %q (an artifact that is both p1 and gate-pending gets ONE class, deterministically)", got, ClassHumanGate)
	}
}

func TestClassify_BlockingRequiresAnAddressee(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Priority: "p1"} // no Addressees, no Broadcast
	if got := classify(fa); got != ClassPublished {
		t.Fatalf("classify = %q, want %q (p1 with no receiving participant is not inbound)", got, ClassPublished)
	}
}

func TestClassify_BlockingFlag(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Blocking: true, Addressees: []string{"seomatrix"}}
	if got := classify(fa); got != ClassBlocking {
		t.Fatalf("classify = %q, want %q", got, ClassBlocking)
	}
}

func TestClassify_BroadcastCountsAsInbound(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Priority: "p1", Broadcast: true}
	if got := classify(fa); got != ClassBlocking {
		t.Fatalf("classify = %q, want %q", got, ClassBlocking)
	}
}

func TestClassify_Published(t *testing.T) {
	t.Parallel()
	fa := cache.NotifyArtifact{Priority: "p3", Addressees: []string{"seomatrix"}}
	if got := classify(fa); got != ClassPublished {
		t.Fatalf("classify = %q, want %q", got, ClassPublished)
	}
}

// TestPresets_DifferentialAgainstClassify is AC-6: "each legacy class
// keeps EXACTLY its current membership when expressed as a preset" — for
// every artifact in this corpus, classify's own output for a class must
// agree with that class's own preset predicate, in both directions. This
// runs BEFORE classify.go's own refactor could have drifted from it (spec
// 11 §"What to do" step 3: "write that test before touching the
// classifier") and stays the permanent regression guard afterward.
//
// The corpus deliberately includes a row satisfying BOTH presetHumanGate
// and presetBlocking's own raw condition (gate-pending AND inbound p1) —
// advisor's own point: an independent, unguarded presetBlocking would
// wrongly also match it. presetBlocking's `!presetHumanGate(fa)` guard is
// what this row actually exercises.
func TestPresets_DifferentialAgainstClassify(t *testing.T) {
	t.Parallel()
	corpus := []cache.NotifyArtifact{
		// human-gate wins over p1+inbound (the classic cascade row).
		{Priority: "p1", Addressees: []string{"seomatrix"}, Verdict: pendency.Verdict{HumanGate: "G3"}},
		// gate-pending, but with NO addressee at all — human-gate still
		// wins (it never depended on inbound-ness).
		{Verdict: pendency.Verdict{HumanGate: "G1"}},
		// gate-pending AND blocking-flagged AND broadcast: still
		// human-gate — the row presetBlocking's own guard exists for.
		{Blocking: true, Broadcast: true, Verdict: pendency.Verdict{HumanGate: "G5"}},
		// p1 with no receiving participant: not inbound, so published.
		{Priority: "p1"},
		// blocking flag, addressed: blocking.
		{Blocking: true, Addressees: []string{"seomatrix"}},
		// p1, broadcast (broadcast counts as inbound): blocking.
		{Priority: "p1", Broadcast: true},
		// ordinary p3, addressed: published.
		{Priority: "p3", Addressees: []string{"seomatrix"}},
		// no priority, no flags, no addressees at all: published.
		{},
	}

	for i, fa := range corpus {
		want := classify(fa)
		gotHumanGate := presetHumanGate(fa)
		gotBlocking := presetBlocking(fa)
		gotPublished := presetPublished(fa)

		if gotHumanGate != (want == ClassHumanGate) {
			t.Errorf("corpus[%d]: presetHumanGate = %v, want %v (classify says %q)", i, gotHumanGate, want == ClassHumanGate, want)
		}
		if gotBlocking != (want == ClassBlocking) {
			t.Errorf("corpus[%d]: presetBlocking = %v, want %v (classify says %q)", i, gotBlocking, want == ClassBlocking, want)
		}
		if gotPublished != (want == ClassPublished) {
			t.Errorf("corpus[%d]: presetPublished = %v, want %v (classify says %q)", i, gotPublished, want == ClassPublished, want)
		}

		// Exactly one preset must fire — mirrors AC-14's "every artifact
		// still gets exactly one class".
		fired := 0
		for _, v := range []bool{gotHumanGate, gotBlocking, gotPublished} {
			if v {
				fired++
			}
		}
		if fired != 1 {
			t.Errorf("corpus[%d]: %d presets fired, want exactly 1", i, fired)
		}
	}
}
