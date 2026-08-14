package html

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

func TestLiveAssemblyPopulatesDashboardVocabulary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := cache.NewStore("atlas", t.TempDir(), nil, func() time.Time { return now }, time.Minute)
	got, err := Assemble(t.Context(), store, "atlas", now)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	assertDashboardVocabularyPayload(t, got)
}

func TestDemoDataPopulatesDashboardVocabulary(t *testing.T) {
	t.Parallel()

	got, err := DemoData()
	if err != nil {
		t.Fatalf("DemoData: %v", err)
	}
	assertDashboardVocabularyPayload(t, got)
}

func assertDashboardVocabularyPayload(t *testing.T, data Data) {
	t.Helper()

	want := DashboardVocabulary()
	if !reflect.DeepEqual(data.Vocabulary, want) {
		t.Fatalf("Vocabulary = %#v, want DashboardVocabulary()", data.Vocabulary)
	}
	encoded, err := CanonicalViewModelJSON(data)
	if err != nil {
		t.Fatalf("CanonicalViewModelJSON: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode canonical payload: %v", err)
	}
	if _, ok := payload["vocabulary"]; !ok {
		t.Fatal("canonical payload omits mandatory vocabulary")
	}
	if hasJSONKey(payload, "policy") {
		t.Fatal("presentation policy leaked into the dashboard payload")
	}
}

func hasJSONKey(value any, key string) bool {
	switch value := value.(type) {
	case map[string]any:
		for candidate, child := range value {
			if candidate == key || hasJSONKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasJSONKey(child, key) {
				return true
			}
		}
	}
	return false
}
