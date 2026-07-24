package notes

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"gopkg.in/yaml.v3"
)

// TestLoad_CorpusIntegrity is the P31 corpus-integrity gate
// (make-check-enforced): the embedded release-notes corpus must load, be
// version-ascending, and every file must both schema-validate and satisfy
// this package's own invariants (version matches filename, change ids
// unique within a file).
func TestLoad_CorpusIntegrity(t *testing.T) {
	all, err := Load(releasenotes.FS)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("Load returned %d entries, want 4: %+v", len(all), all)
	}

	wantVersions := []string{"0.2.0", "0.3.0", "0.4.0", "0.5.0"}
	for i, rn := range all {
		if rn.Version != wantVersions[i] {
			t.Errorf("entry %d: version = %q, want %q (ascending order)", i, rn.Version, wantVersions[i])
		}
	}

	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}

	for _, rn := range all {
		t.Run(rn.Version, func(t *testing.T) {
			// version matches its filename (0.4.0.yaml -> "0.4.0").
			wantFile := rn.Version + ".yaml"
			raw, err := releasenotes.FS.ReadFile(wantFile)
			if err != nil {
				t.Fatalf("release-notes entry version %q has no matching file %s: %v", rn.Version, wantFile, err)
			}
			if string(raw) != string(rn.Raw) {
				t.Errorf("entry version %q: Raw does not match the bytes of %s", rn.Version, wantFile)
			}

			// change ids are unique within the file.
			seen := map[string]bool{}
			for _, ch := range rn.Changes {
				if seen[ch.ID] {
					t.Errorf("%s: duplicate change id %q", wantFile, ch.ID)
				}
				seen[ch.ID] = true
			}

			// every embedded corpus file validates against the schema.
			var doc any
			if err := yaml.Unmarshal(rn.Raw, &doc); err != nil {
				t.Fatalf("%s: yaml.Unmarshal for schema validation: %v", wantFile, err)
			}
			violations, err := corpus.ValidateReleaseNotes(doc)
			if err != nil {
				t.Fatalf("%s: ValidateReleaseNotes: %v", wantFile, err)
			}
			if len(violations) != 0 {
				t.Errorf("%s: schema violations: %+v", wantFile, violations)
			}
		})
	}
}

func TestParseReleaseNotes_InvalidYAML(t *testing.T) {
	_, err := ParseReleaseNotes([]byte("not: [valid: yaml"))
	if err == nil {
		t.Fatal("expected an error for malformed yaml")
	}
	if !errors.Is(err, ErrReleaseNotesInvalid) {
		t.Errorf("error = %v, want wrapping ErrReleaseNotesInvalid", err)
	}
}

func TestParseReleaseNotes_Valid(t *testing.T) {
	raw := []byte(`
schema: release-notes/v1
version: "1.2.3"
released: "2026-01-01"
headline: "test"
changes:
  - id: RN-TEST-1
    kind: feat
    impact: low
    subject: "s"
    detail: "d"
    action:
      scope: none
      why: "y"
`)
	rn, err := ParseReleaseNotes(raw)
	if err != nil {
		t.Fatalf("ParseReleaseNotes: %v", err)
	}
	if rn.Version != "1.2.3" {
		t.Errorf("Version = %q, want 1.2.3", rn.Version)
	}
	if len(rn.Changes) != 1 || rn.Changes[0].ID != "RN-TEST-1" {
		t.Errorf("Changes = %+v", rn.Changes)
	}
	if string(rn.Raw) != string(raw) {
		t.Error("Raw does not match input bytes")
	}
}

func TestLoad_GlobError(t *testing.T) {
	// fstest.MapFS never returns an error from fs.Glob for a valid
	// pattern, so exercise the parse-failure path instead: a malformed
	// *.yaml file in the fs.FS must surface as an error from Load, not a
	// silently-dropped entry.
	fsys := fstest.MapFS{
		"bad.yaml": &fstest.MapFile{Data: []byte("not: [valid: yaml")},
	}
	if _, err := Load(fsys); err == nil {
		t.Fatal("expected an error for a malformed corpus file")
	}
}

func TestLoad_EmptyFS(t *testing.T) {
	fsys := fstest.MapFS{}
	all, err := Load(fsys)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("Load on empty fs = %d entries, want 0", len(all))
	}
}

func TestLoad_ReadFileError(t *testing.T) {
	// A fs.FS whose ReadFile always errors must surface that as Load's
	// error, never a silently-dropped entry.
	if _, err := Load(erroringFS{}); err == nil {
		t.Fatal("expected an error when ReadFile fails")
	}
}

// erroringFS is a minimal fs.FS whose Open (and therefore ReadFile) always
// fails, used to exercise Load's read-error path.
type erroringFS struct{}

func (erroringFS) Open(name string) (fs.File, error) {
	return nil, errors.New("erroringFS: open always fails")
}

func (erroringFS) Glob(pattern string) ([]string, error) {
	return []string{"0.1.0.yaml"}, nil
}

func TestSince(t *testing.T) {
	all := mustLoadFixtureCorpus(t)

	got := versionsOf(Since(all, "0.2.0", "0.4.0"))
	want := []string{"0.3.0", "0.4.0"}
	assertVersionsEqual(t, "Since(0.2.0,0.4.0)", got, want)

	got = versionsOf(Since(all, "", "0.3.0"))
	want = []string{"0.2.0", "0.3.0"}
	assertVersionsEqual(t, `Since("",0.3.0)`, got, want)

	got = versionsOf(Since(all, "0.3.0", ""))
	want = []string{"0.4.0", "0.5.0"}
	assertVersionsEqual(t, `Since(0.3.0,"")`, got, want)

	got = versionsOf(Since(all, "", ""))
	want = []string{"0.2.0", "0.3.0", "0.4.0", "0.5.0"}
	assertVersionsEqual(t, `Since("","")`, got, want)
}

func TestExactly(t *testing.T) {
	all := mustLoadFixtureCorpus(t)

	rn, ok := Exactly(all, "0.4.0")
	if !ok {
		t.Fatal("Exactly(0.4.0): not found")
	}
	if rn.Version != "0.4.0" {
		t.Errorf("Exactly(0.4.0).Version = %q", rn.Version)
	}

	if _, ok := Exactly(all, "9.9.9"); ok {
		t.Error("Exactly(9.9.9): expected not found")
	}
}

func mustLoadFixtureCorpus(t *testing.T) []ReleaseNotes {
	t.Helper()
	all, err := Load(releasenotes.FS)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return all
}

func versionsOf(all []ReleaseNotes) []string {
	out := make([]string, len(all))
	for i, rn := range all {
		out[i] = rn.Version
	}
	return out
}

func assertVersionsEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", label, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", label, got, want)
			return
		}
	}
}
