package lane

import "testing"

func TestCorpusUnionsMakefileAndVerify(t *testing.T) {
	phases, err := Corpus("testdata/fixture1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := map[string]bool{}
	for _, p := range phases {
		names[p.Name] = true
	}
	want := []string{"classify-guard", "readme-lint", "vet", "build-cli", "gofmt"}
	for _, w := range want {
		if !names[w] {
			t.Errorf("expected corpus phase %q, got %v", w, names)
		}
	}
	if names["feature-lint"] {
		t.Errorf("feature-lint should have been presence-gated out of the corpus")
	}
	if len(phases) != len(want) {
		t.Errorf("got %d phases, want %d: %+v", len(phases), len(want), phases)
	}
}
