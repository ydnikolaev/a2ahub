package lane

import "testing"

// TestLoadLiveRepoIntegration is the one test this package is allowed to
// point at the real checkout (brief: "one integration test that merely
// asserts Load(\".\") returns without error is acceptable; assertions about
// specific counts or contents of the live tree are not"). Three sibling
// agents are editing the real gate scripts concurrently this wave, so this
// asserts nothing about WHAT Load finds — only that scanning the live tree
// does not crash the parser itself.
func TestLoadLiveRepoIntegration(t *testing.T) {
	if _, err := Load("../.."); err != nil {
		t.Fatalf("Load(\"../..\") returned an error: %v", err)
	}
}

// TestCorpusLiveRepoIntegration is the Corpus half of the same guard: the
// Makefile and scripts/verify.sh parsers must not choke on the real files,
// independent of whether every declaration has landed yet.
func TestCorpusLiveRepoIntegration(t *testing.T) {
	if _, err := Corpus("../.."); err != nil {
		t.Fatalf("Corpus(\"../..\") should never error on the real Makefile/verify.sh shape: %v", err)
	}
}
