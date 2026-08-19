package viewvocab

// lifecycle_state_entry_test.go covers F2 (defects-fix-2026-08 P5,
// docs/inbox/defects/02-narrow-answers-survey.md): the two live
// collisions between handoff's own meaning of `accepted`/`rejected` and
// every other kind's for the same state names.
//
// This file imports internal/fold — a TEST import, not a production one.
// TestImportsNothing (import_test.go) inspects go/build's pkg.Imports,
// which excludes test files, so this does not weaken that gate.

import (
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/fold"
)

// TestHandoffAcceptedAndRejectedDifferFromQuestionDecision is AC3
// (spec 05 §8): the two named collisions must resolve to labels that
// differ from the SAME state names on question/decision.
func TestHandoffAcceptedAndRejectedDifferFromQuestionDecision(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"accepted", "rejected"} {
		handoff := LifecycleStateEntry("handoff", state)
		question := LifecycleStateEntry("question", state)
		decision := LifecycleStateEntry("decision", state)

		if handoff.LabelEN == "" || handoff.LabelRU == "" {
			t.Fatalf("LifecycleStateEntry(handoff, %s) is empty — every RestingStates() member needs a label", state)
		}
		if handoff.LabelEN == question.LabelEN || handoff.LabelRU == question.LabelRU {
			t.Errorf("LifecycleStateEntry(handoff, %s) = %+v, want it to differ from question's %+v for the same state name", state, handoff, question)
		}
		if handoff.LabelEN == decision.LabelEN || handoff.LabelRU == decision.LabelRU {
			t.Errorf("LifecycleStateEntry(handoff, %s) = %+v, want it to differ from decision's %+v for the same state name", state, handoff, decision)
		}
		if handoff.ExplanationEN == question.ExplanationEN {
			t.Errorf("LifecycleStateEntry(handoff, %s) explanation matches question's verbatim — F2's whole point is that the meaning differs", state)
		}
	}
}

// TestHandoffAcceptedIsTrueForAHandoff pins the domain content, not just
// "differs": handoff `accepted` is the END of the exchange (fold.Outcome
// == OutcomeSettled), and the label must not carry question's "still in
// progress" wording.
func TestHandoffAcceptedIsTrueForAHandoff(t *testing.T) {
	t.Parallel()

	if got := fold.OutcomeOf(fold.KindHandoff, fold.StateAccepted); got != fold.OutcomeSettled {
		t.Fatalf("test's own premise is wrong: OutcomeOf(handoff, accepted) = %q, want %q", got, fold.OutcomeSettled)
	}
	entry := LifecycleStateEntry("handoff", "accepted")
	if entry.Tone != VocabularyToneSettled {
		t.Errorf("handoff/accepted tone = %q, want %q — verify-pass is where a handoff ends, no row leaves it", entry.Tone, VocabularyToneSettled)
	}
}

// TestHandoffRejectedIsTrueForAHandoff pins the sharpest defects/02
// finding: `rejected` reads as a failed human approval on a decision, but
// on a handoff it is the one state pendency calls a genuinely OWED
// supersede — the producer must resubmit.
func TestHandoffRejectedIsTrueForAHandoff(t *testing.T) {
	t.Parallel()

	if got := fold.Terminal(fold.KindHandoff, fold.StateRejected); got {
		t.Fatal("test's own premise is wrong: Terminal(handoff, rejected) = true, want false — the producer may still supersede")
	}
	entry := LifecycleStateEntry("handoff", "rejected")
	decision := LifecycleStateEntry("decision", "rejected")
	if entry.Tone == decision.Tone && entry.LabelEN == decision.LabelEN {
		t.Errorf("handoff/rejected reads byte-identical to decision/rejected: %+v", entry)
	}
}

// TestLifecycleStateEntryDefersForEveryOtherPair sweeps fold.RestingStates()
// and requires that every pair OTHER than the two named handoff overrides
// resolves to exactly the state-keyed entry DashboardVocabulary's own
// table already carries — this seam narrows nothing else.
func TestLifecycleStateEntryDefersForEveryOtherPair(t *testing.T) {
	t.Parallel()

	for _, krs := range fold.RestingStates() {
		kind := string(krs.Kind)
		state := string(krs.State)
		if kind == "handoff" && (state == "accepted" || state == "rejected") {
			continue // the two deliberate overrides, covered above
		}
		got := LifecycleStateEntry(kind, state)
		var want VocabularyEntry
		for _, e := range dashboardVocabularyEntries {
			if e.Family == VocabularyFamilyLifecycleState && e.Value == state {
				want = e
				break
			}
		}
		if got != want {
			t.Errorf("LifecycleStateEntry(%s, %s) = %+v, want the state-keyed table entry %+v unchanged", kind, state, got, want)
		}
	}
}

// TestLifecycleStateEntryEveryRestingStateHasALabel is the spec's own
// testing requirement (§6): "every (kind, state) in RestingStates() gets a
// label check".
func TestLifecycleStateEntryEveryRestingStateHasALabel(t *testing.T) {
	t.Parallel()

	for _, krs := range fold.RestingStates() {
		entry := LifecycleStateEntry(string(krs.Kind), string(krs.State))
		if entry.LabelRU == "" || entry.LabelEN == "" || entry.ExplanationRU == "" || entry.ExplanationEN == "" {
			t.Errorf("LifecycleStateEntry(%s, %s) has an empty localized field: %+v", krs.Kind, krs.State, entry)
		}
	}
}
