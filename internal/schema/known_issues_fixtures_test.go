package schema

import (
	"path/filepath"
	"testing"
)

func TestKnownIssuesFixtures(t *testing.T) {
	t.Parallel()
	c := mustLoad(t)

	fixtureRoot := filepath.Join(corpusRoot, "known-issues", "v1", "fixtures")
	valid, err := filepath.Glob(filepath.Join(fixtureRoot, "valid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(valid) == 0 {
		t.Fatal("no valid known-issues fixtures found")
	}
	for _, f := range valid {
		violations, vErr := c.ValidateKnownIssues(fixtureInstance(t, f))
		if vErr != nil {
			t.Fatalf("%s: ValidateKnownIssues: %v", f, vErr)
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
		t.Fatal("no invalid known-issues fixtures found")
	}
	for _, f := range invalid {
		violations, vErr := c.ValidateKnownIssues(fixtureInstance(t, f))
		if vErr != nil {
			t.Fatalf("%s: ValidateKnownIssues: %v", f, vErr)
		}
		if len(violations) == 0 {
			t.Errorf("%s: expected at least one violation, got none", filepath.Base(f))
		}
	}
}
