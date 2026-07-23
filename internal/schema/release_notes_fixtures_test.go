package schema

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReleaseNotesFixtures makes the release-notes/v1 fixture tree
// load-bearing: every fixtures/valid/*.yaml must validate clean and every
// fixtures/invalid/*.yaml must produce at least one violation. Without this,
// the fixtures are dead weight — the envelope SCH- closure (ac401_2_test.go)
// deliberately skips this family (no .expect.yaml sidecars, release-notes is
// not envelope-scoped), so nothing else reads them. This is the family's own
// gate, mirroring how internal/feedback owns its FB- code closure.
func TestReleaseNotesFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	fixtureRoot := filepath.Join(corpusRoot, "release-notes", "v1", "fixtures")

	valid, err := filepath.Glob(filepath.Join(fixtureRoot, "valid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(valid) == 0 {
		t.Fatal("no valid release-notes fixtures found — the family's own gate has nothing to exercise")
	}
	for _, f := range valid {
		instance := fixtureInstance(t, f)
		violations, vErr := c.ValidateReleaseNotes(instance)
		if vErr != nil {
			t.Fatalf("%s: ValidateReleaseNotes: %v", f, vErr)
		}
		if len(violations) != 0 {
			t.Errorf("%s: expected a valid fixture, got %+v", filepath.Base(f), violations)
		}
	}

	invalid, err := filepath.Glob(filepath.Join(fixtureRoot, "invalid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob invalid fixtures: %v", err)
	}
	if len(invalid) == 0 {
		t.Fatal("no invalid release-notes fixtures found — the family's own gate cannot prove the schema discriminates")
	}
	for _, f := range invalid {
		instance := fixtureInstance(t, f)
		violations, vErr := c.ValidateReleaseNotes(instance)
		if vErr != nil {
			t.Fatalf("%s: ValidateReleaseNotes: %v", f, vErr)
		}
		if len(violations) == 0 {
			t.Errorf("%s: expected at least one violation, got none", filepath.Base(f))
		}
	}
}

// fixtureInstance reads a YAML fixture off disk and decodes it through the
// same DecodeYAMLInstance path the production validate methods use.
func fixtureInstance(t *testing.T, path string) any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	instance, err := DecodeYAMLInstance(raw)
	if err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
	return instance
}
