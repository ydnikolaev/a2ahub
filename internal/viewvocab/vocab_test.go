package viewvocab

import "testing"

// TestDashboardVocabularyIsPopulated is the moved-table behaviour check for
// this package (spec space-notify-2026-08 P2 §6): the closed family and
// transport sets, and the vocabulary table itself, are non-empty and stable
// in the package the data now lives in. internal/html/vocab_test.go still
// covers the exhaustive coverage/shape/localization invariants against this
// same table through html.DashboardVocabulary()'s delegation.
func TestDashboardVocabularyIsPopulated(t *testing.T) {
	t.Parallel()
	if got, want := len(VocabularyFamilies()), 12; got != want {
		t.Errorf("VocabularyFamilies count = %d, want %d", got, want)
	}
	if got, want := len(LiveTransportStates()), 5; got != want {
		t.Errorf("LiveTransportStates count = %d, want %d", got, want)
	}
	table := DashboardVocabulary()
	if len(table.Entries) == 0 {
		t.Fatal("DashboardVocabulary returned no entries")
	}
	if table.Unknown.Tone != VocabularyToneUnknown {
		t.Errorf("fallback tone = %q, want %q", table.Unknown.Tone, VocabularyToneUnknown)
	}
}

// TestToneCuesReturnsFreshCopy guards the mutation-safety this package's data
// discipline promises: a caller mutating the map it received must not affect
// the next caller.
func TestToneCuesReturnsFreshCopy(t *testing.T) {
	t.Parallel()
	first := ToneCues()
	if len(first) == 0 {
		t.Fatal("ToneCues returned no entries")
	}
	for tone := range first {
		first[tone] = "mutated"
	}
	second := ToneCues()
	for tone, cue := range second {
		if cue == "mutated" {
			t.Errorf("ToneCues shares storage across calls: tone %q leaked a caller mutation", tone)
		}
	}
}

// A transition without an actor form falls back to its label, and the label is
// a statement about the record ("ответ записан"), not about the person the row
// names. That fallback is honest but reads as broken grammar in a sentence
// which already carries a name, so the set is closed here rather than left to
// whoever adds the next transition.
func TestEveryTransitionCarriesItsActorForm(t *testing.T) {
	t.Parallel()
	table := DashboardVocabulary()
	seen := 0
	for _, entry := range table.Entries {
		if entry.Family != VocabularyFamilyTransition {
			if entry.PastRU != "" || entry.PastEN != "" {
				t.Errorf("%s/%s carries an actor form; only transitions have one", entry.Family, entry.Value)
			}
			continue
		}
		seen++
		if entry.PastRU == "" || entry.PastEN == "" {
			t.Errorf("transition %q has no actor form — a timeline row would read %q after a person's name", entry.Value, entry.LabelRU)
		}
		if entry.PastRU == entry.LabelRU {
			t.Errorf("transition %q reuses its label as the actor form; the two answer different questions", entry.Value)
		}
	}
	if seen == 0 {
		t.Fatal("no transition entries found — this test would pass vacuously")
	}
	for value := range transitionPastForms {
		found := false
		for _, entry := range table.Entries {
			if entry.Family == VocabularyFamilyTransition && entry.Value == value {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("actor form %q names a transition the vocabulary does not carry", value)
		}
	}
}
