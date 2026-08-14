package html

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/workreport"
)

type vocabularyPair struct {
	family VocabularyFamily
	value  string
}

func TestDashboardVocabularyCoverage(t *testing.T) {
	t.Parallel()
	want := dashboardVocabularyPairs()
	table := DashboardVocabulary()
	got := make(map[vocabularyPair]bool, len(table.Entries))
	gotOrder := make([]vocabularyPair, 0, len(table.Entries))
	for _, entry := range table.Entries {
		pair := vocabularyPair{family: entry.Family, value: entry.Value}
		if entry.Family == "" || entry.Value == "" {
			t.Errorf("vocabulary contains fallback-like row family=%q value=%q", entry.Family, entry.Value)
			continue
		}
		if got[pair] {
			t.Errorf("vocabulary duplicates %s/%s", pair.family, pair.value)
		}
		got[pair] = true
		gotOrder = append(gotOrder, pair)
	}
	for _, pair := range want {
		if !got[pair] {
			t.Errorf("vocabulary omits %s/%s", pair.family, pair.value)
		}
	}
	wantSet := make(map[vocabularyPair]bool, len(want))
	for _, pair := range want {
		wantSet[pair] = true
	}
	for pair := range got {
		if !wantSet[pair] {
			t.Errorf("vocabulary has surplus %s/%s", pair.family, pair.value)
		}
	}
	if !reflect.DeepEqual(gotOrder, want) {
		t.Errorf("vocabulary order = %v, want stable family/value order %v", gotOrder, want)
	}
}

func TestDashboardVocabularyEntriesAreComplete(t *testing.T) {
	t.Parallel()
	table := DashboardVocabulary()
	labelsRU := map[VocabularyFamily]map[string]string{}
	labelsEN := map[VocabularyFamily]map[string]string{}
	for _, entry := range table.Entries {
		if entry.LabelRU == "" || entry.LabelEN == "" || entry.ExplanationRU == "" || entry.ExplanationEN == "" {
			t.Errorf("%s/%s has an empty localized field", entry.Family, entry.Value)
		}
		if entry.LabelRU == entry.Value || entry.LabelEN == entry.Value || entry.ExplanationRU == entry.Value || entry.ExplanationEN == entry.Value {
			t.Errorf("%s/%s exposes the raw value as human wording", entry.Family, entry.Value)
		}
		cue, ok := toneCues[entry.Tone]
		if !ok {
			t.Errorf("%s/%s has unknown tone %q", entry.Family, entry.Value, entry.Tone)
		} else if entry.Cue != cue {
			t.Errorf("%s/%s cue = %q, want tone-owned %q", entry.Family, entry.Value, entry.Cue, cue)
		}
		assertUniqueVocabularyLabel(t, labelsRU, entry.Family, entry.Value, entry.LabelRU, "RU")
		assertUniqueVocabularyLabel(t, labelsEN, entry.Family, entry.Value, entry.LabelEN, "EN")
	}
	if len(toneCues) != 6 {
		t.Errorf("tone cue count = %d, want closed six", len(toneCues))
	}
	seenCues := map[string]VocabularyTone{}
	for tone, cue := range toneCues {
		if cue == "" {
			t.Errorf("tone %q has an empty cue", tone)
		}
		if other, exists := seenCues[cue]; exists {
			t.Errorf("tones %q and %q share cue %q", other, tone, cue)
		}
		seenCues[cue] = tone
	}
}

func TestDashboardVocabularyFallbackIsSeparate(t *testing.T) {
	t.Parallel()
	unknown := DashboardVocabulary().Unknown
	if unknown.LabelRU == "" || unknown.LabelEN == "" || unknown.ExplanationRU == "" || unknown.ExplanationEN == "" {
		t.Fatalf("fallback has an empty localized field: %+v", unknown)
	}
	if unknown.Tone != VocabularyToneUnknown || unknown.Cue != toneCues[VocabularyToneUnknown] {
		t.Errorf("fallback tone/cue = %q/%q, want %q/%q", unknown.Tone, unknown.Cue, VocabularyToneUnknown, toneCues[VocabularyToneUnknown])
	}
	raw, err := json.Marshal(unknown)
	if err != nil {
		t.Fatalf("marshal fallback: %v", err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("decode fallback: %v", err)
	}
	if _, ok := shape["family"]; ok {
		t.Error("fallback serialized a family")
	}
	if _, ok := shape["value"]; ok {
		t.Error("fallback serialized a value")
	}
}

func TestDashboardVocabularyReturnsFreshCopies(t *testing.T) {
	t.Parallel()
	want := DashboardVocabulary()
	mutated := DashboardVocabulary()
	if len(mutated.Entries) == 0 {
		t.Fatal("DashboardVocabulary returned no entries")
	}
	mutated.Entries[0].LabelEN = "mutated"
	mutated.Unknown.LabelEN = "mutated"
	if got := DashboardVocabulary(); !reflect.DeepEqual(got, want) {
		t.Errorf("DashboardVocabulary after caller mutation = %+v, want fresh %+v", got, want)
	}
}

func TestDashboardVocabularyEnumeratorsReturnFreshCopies(t *testing.T) {
	t.Parallel()
	wantFamilies := []VocabularyFamily{
		VocabularyFamilyFreshness,
		VocabularyFamilySourceFreshness,
		VocabularyFamilyOutcome,
		VocabularyFamilyLifecycleState,
		VocabularyFamilyReason,
		VocabularyFamilyGate,
		VocabularyFamilyWorkMode,
		VocabularyFamilyDependencyDrift,
		VocabularyFamilyConsistencySeverity,
		VocabularyFamilyOperationalState,
		VocabularyFamilyLiveTransport,
	}
	gotFamilies := VocabularyFamilies()
	if !reflect.DeepEqual(gotFamilies, wantFamilies) {
		t.Errorf("VocabularyFamilies = %v, want exact closed set %v", gotFamilies, wantFamilies)
	}
	gotFamilies[0] = "mutated"
	if fresh := VocabularyFamilies(); !reflect.DeepEqual(fresh, wantFamilies) {
		t.Errorf("VocabularyFamilies after caller mutation = %v, want %v", fresh, wantFamilies)
	}
	wantTransport := []string{
		LiveTransportUpdated,
		LiveTransportNewerAvailable,
		LiveTransportRefreshing,
		LiveTransportStale,
		LiveTransportUnavailable,
	}
	gotTransport := LiveTransportStates()
	if !reflect.DeepEqual(gotTransport, wantTransport) {
		t.Errorf("LiveTransportStates = %v, want exact closed set %v", gotTransport, wantTransport)
	}
	gotTransport[0] = "mutated"
	if fresh := LiveTransportStates(); !reflect.DeepEqual(fresh, wantTransport) {
		t.Errorf("LiveTransportStates after caller mutation = %v, want %v", fresh, wantTransport)
	}
}

func TestDashboardVocabularyUsesEveryClosedTone(t *testing.T) {
	t.Parallel()
	closed := []VocabularyTone{
		VocabularyToneNeedsYou,
		VocabularyToneWaitingThem,
		VocabularyToneProgressing,
		VocabularyToneSettled,
		VocabularyToneBroken,
		VocabularyToneUnknown,
	}
	if len(toneCues) != len(closed) {
		t.Fatalf("tone cue count = %d, want %d", len(toneCues), len(closed))
	}
	used := map[VocabularyTone]bool{DashboardVocabulary().Unknown.Tone: true}
	for _, entry := range DashboardVocabulary().Entries {
		used[entry.Tone] = true
	}
	for _, tone := range closed {
		if _, ok := toneCues[tone]; !ok {
			t.Errorf("closed tone %q has no cue", tone)
		}
		if !used[tone] {
			t.Errorf("closed tone %q has no representative vocabulary entry", tone)
		}
	}
}

func dashboardVocabularyPairs() []vocabularyPair {
	base := fold.BuildVocabulary()
	values := map[VocabularyFamily][]string{
		VocabularyFamilyFreshness:           stringsOf(operational.FreshnessValues()),
		VocabularyFamilySourceFreshness:     stringsOf(operational.SourceFreshnessValues()),
		VocabularyFamilyOutcome:             append([]string(nil), base.Outcomes...),
		VocabularyFamilyLifecycleState:      unionValues(base.States),
		VocabularyFamilyReason:              stringsOf(cache.ReasonCodes()),
		VocabularyFamilyGate:                unionStrings(base.HumanGates),
		VocabularyFamilyWorkMode:            stringsOf(workreport.Modes()),
		VocabularyFamilyDependencyDrift:     cache.DependencyDrifts(),
		VocabularyFamilyConsistencySeverity: stringsOf(operational.ConsistencySeverities()),
		VocabularyFamilyOperationalState:    cache.OperationalStates(),
		VocabularyFamilyLiveTransport:       LiveTransportStates(),
	}
	var pairs []vocabularyPair
	for _, family := range VocabularyFamilies() {
		familyValues, ok := values[family]
		if !ok || len(familyValues) == 0 {
			pairs = append(pairs, vocabularyPair{family: family})
			continue
		}
		for _, value := range familyValues {
			pairs = append(pairs, vocabularyPair{family: family, value: value})
		}
	}
	return pairs
}

func stringsOf[T ~string](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func unionValues(values map[string][]string) []string {
	seen := map[string]bool{}
	for _, group := range values {
		for _, value := range group {
			if value != "" {
				seen[value] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func unionStrings(values map[string]string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func assertUniqueVocabularyLabel(t *testing.T, seen map[VocabularyFamily]map[string]string, family VocabularyFamily, value, label, locale string) {
	t.Helper()
	if seen[family] == nil {
		seen[family] = map[string]string{}
	}
	if other, exists := seen[family][label]; exists {
		t.Errorf("%s labels for %s/%s and %s/%s both equal %q", locale, family, other, family, value, label)
	}
	seen[family][label] = value
}
