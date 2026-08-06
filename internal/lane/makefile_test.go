package lane

import "testing"

func TestCorpusFromMakefileSkipsPresenceGatedAbsentScript(t *testing.T) {
	phases, doc, err := corpusFromMakefile("testdata/fixture1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc == nil {
		t.Fatal("doc is nil")
	}
	names := map[string]bool{}
	var required int
	for _, p := range phases {
		if p.Optional {
			continue
		}
		required++
		names[p.Name] = true
	}
	if names["feature-lint"] {
		t.Errorf("feature-lint should be skipped: its script is absent from testdata/fixture1")
	}
	if !names["classify-guard"] || !names["readme-lint"] {
		t.Errorf("classify-guard and readme-lint should be present, got %v", names)
	}
	if required != 2 {
		t.Errorf("got %d required phases, want 2 (classify-guard, readme-lint): %v", required, phases)
	}
}

func TestCorpusFromMakefileMissingRepoGatesTargetErrors(t *testing.T) {
	_, _, err := corpusFromMakefile("testdata/fixture-bad-target")
	if err == nil {
		t.Fatal("expected an error when a REPO_GATES entry names a nonexistent target")
	}
}

func TestScriptToPhaseRestrictedToCorpus(t *testing.T) {
	phases, doc, err := corpusFromMakefile("testdata/fixture1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := scriptToPhase(doc, phases)
	if got := m["scripts/check-readme.sh"]; got != "readme-lint" {
		t.Errorf("scripts/check-readme.sh -> %q, want readme-lint", got)
	}
	if got := m["scripts/classify-guard.sh"]; got != "classify-guard" {
		t.Errorf("scripts/classify-guard.sh -> %q, want classify-guard", got)
	}
	// feature-lint was excluded from the corpus (presence-gated absent),
	// so its script must not appear in the restricted reverse map either.
	if _, ok := m["scripts/check-feature-lint.sh"]; ok {
		t.Errorf("scripts/check-feature-lint.sh should not be mapped: feature-lint left the corpus")
	}
}

func TestPresenceGateScript(t *testing.T) {
	recipe := []string{
		"\t@if [ -f scripts/check-feature-lint.sh ]; then \\",
		"\t  bash scripts/check-feature-lint.sh; \\",
		"\telse \\",
		"\t  echo skip; \\",
		"\tfi",
	}
	script, ok := presenceGateScript(recipe)
	if !ok || script != "scripts/check-feature-lint.sh" {
		t.Fatalf("got (%q, %v)", script, ok)
	}
	if _, ok := presenceGateScript([]string{"\t@bash scripts/check-readme.sh"}); ok {
		t.Fatalf("an ungated recipe should not report a presence-gate script")
	}
}
