package lane

import "testing"

func TestCorpusUnionsMakefileAndVerify(t *testing.T) {
	phases, err := Corpus("testdata/fixture1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// REQUIRED phases only. Corpus also admits every other real Makefile
	// target as an Optional phase (web-quality's home) — those may carry a
	// declaration but are never refused for lacking one, so counting them
	// here would measure the fixture's Makefile, not the corpus contract.
	names := map[string]bool{}
	var required int
	for _, p := range phases {
		if p.Optional {
			continue
		}
		required++
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
	if required != len(want) {
		t.Errorf("got %d required phases, want %d: %+v", required, len(want), phases)
	}
}
