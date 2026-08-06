package lane

import (
	"os"
	"strings"
	"testing"
)

func TestScanVerifyPhasesTopLevelOnly(t *testing.T) {
	raw, err := os.ReadFile("testdata/fixture1/scripts/verify.sh")
	if err != nil {
		t.Fatal(err)
	}
	callSites, functions, err := scanVerifyPhases(strings.Split(string(raw), "\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := map[string]bool{}
	for _, cs := range callSites {
		names[cs.Name] = true
	}
	// Variable call site (run_repo_gates' "$gate") and the teeth self-test's
	// deliberate-red must both be excluded — the same rule, top-level only.
	for _, unwanted := range []string{"$gate", "deliberate-red"} {
		if names[unwanted] {
			t.Errorf("call site %q should have been excluded (function-nested)", unwanted)
		}
	}
	for _, want := range []string{"vet", "build-cli", "gofmt"} {
		if !names[want] {
			t.Errorf("expected top-level call site %q, got %v", want, names)
		}
	}
	if len(callSites) != 3 {
		t.Errorf("got %d call sites, want 3: %+v", len(callSites), callSites)
	}
	for _, fn := range []string{"run_phase", "run_repo_gates", "build_cli", "check_gofmt", "run_teeth"} {
		if !functions[fn] {
			t.Errorf("expected function %q to be discovered", fn)
		}
	}
}

func TestCorpusFromVerifyDedupsAndMapsFunctions(t *testing.T) {
	phases, funcToPhase, err := corpusFromVerify("testdata/fixture1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(phases) != 3 {
		t.Fatalf("got %d phases, want 3: %+v", len(phases), phases)
	}
	if got := funcToPhase["build_cli"]; got != "build-cli" {
		t.Errorf("funcToPhase[build_cli] = %q, want build-cli", got)
	}
	if got := funcToPhase["check_gofmt"]; got != "gofmt" {
		t.Errorf("funcToPhase[check_gofmt] = %q, want gofmt", got)
	}
	if _, ok := funcToPhase["go"]; ok {
		t.Errorf("bare command %q should not be treated as a function", "go")
	}
}

// TestCorpusFromVerifyVacuityGuard proves the extractor refuses loudly
// rather than returning an empty phase list when it can no longer find a
// TOP-LEVEL run_phase call site — the same shape as a stuck insideFunc
// tracker (a function that never closes at column 0, or every run_phase
// call site being function-nested). A silently empty Corpus would make
// Reconcile report every real verify.sh declaration as an orphan, pointing
// an operator at exactly the wrong file.
func TestCorpusFromVerifyVacuityGuard(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/scripts", 0o755); err != nil {
		t.Fatal(err)
	}
	// Every run_phase call site here is function-nested — none is
	// top-level, so the extractor must refuse rather than return an empty
	// corpus.
	src := `#!/usr/bin/env bash
only_nested() {
  run_phase go-test run_go_tests
}
`
	if err := os.WriteFile(dir+"/scripts/verify.sh", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := corpusFromVerify(dir)
	if err == nil {
		t.Fatal("expected an error: no top-level run_phase call site exists in this fixture")
	}
}

func TestCorpusFromVerifyDedupsByName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/scripts", 0o755); err != nil {
		t.Fatal(err)
	}
	src := `#!/usr/bin/env bash
run_logic_tests() {
  echo dedup
}

run_phase logic-e2e run_logic_tests
run_phase logic-e2e run_logic_tests
`
	if err := os.WriteFile(dir+"/scripts/verify.sh", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	phases, _, err := corpusFromVerify(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	count := 0
	for _, p := range phases {
		if p.Name == "logic-e2e" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("logic-e2e appeared %d times in the corpus, want exactly 1 (two call sites, one declaration)", count)
	}
}
