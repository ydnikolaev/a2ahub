package notes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
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
	// Deliberately hardcoded: this is the tripwire that catches a corpus
	// file accidentally dropped or mis-ordered. Bump it when you cut a
	// release — that edit IS the check.
	if len(all) != 38 {
		t.Fatalf("Load returned %d entries, want 38: %+v", len(all), all)
	}

	// 0.11.0 is present BEFORE its tag, deliberately: its entries were
	// written while the changes were fresh, which is when release notes are
	// actually accurate. Its `released:` date is provisional and is corrected
	// when the tag is cut — and this tripwire is what forces whoever cuts it
	// to open the file, so the correction cannot be forgotten.
	wantVersions := []string{"0.2.0", "0.3.0", "0.4.0", "0.5.0", "0.6.0", "0.6.1", "0.6.2", "0.6.3", "0.6.4", "0.7.0", "0.8.0", "0.9.0", "0.9.1", "0.10.0", "0.11.0", "0.12.0", "0.13.0", "0.15.0", "0.15.1", "0.15.2", "0.16.0", "0.16.1", "0.16.2", "0.16.3", "0.17.0", "0.17.1", "0.18.0", "0.18.1", "0.18.2", "0.19.0", "0.19.1", "0.19.2", "0.19.3", "0.19.4", "0.19.5", "0.19.6", "0.19.7", "0.19.8"}
	standingListEra := false
	for i, rn := range all {
		if rn.Version != wantVersions[i] {
			t.Errorf("entry %d: version = %q, want %q (ascending order)", i, rn.Version, wantVersions[i])
		}
		if rn.Version == "0.16.3" {
			standingListEra = true
		}
		if standingListEra {
			for _, change := range rn.Changes {
				if change.Kind == KindKnownIssue {
					t.Errorf("%s.yaml: current known issue %q must live only in %s",
						rn.Version, change.ID, currentKnownIssuesPath)
				}
			}
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

func TestCurrentKnownIssues_CorpusIntegrity(t *testing.T) {
	t.Parallel()

	issues, err := LoadCurrentKnownIssues(releasenotes.FS)
	if err != nil {
		t.Fatalf("LoadCurrentKnownIssues: %v", err)
	}
	raw, err := releasenotes.FS.ReadFile(currentKnownIssuesPath)
	if err != nil {
		t.Fatalf("read %s: %v", currentKnownIssuesPath, err)
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: yaml.Unmarshal: %v", currentKnownIssuesPath, err)
	}
	corpus, err := schema.Load()
	if err != nil {
		t.Fatalf("schema.Load: %v", err)
	}
	violations, err := corpus.ValidateKnownIssues(doc)
	if err != nil {
		t.Fatalf("ValidateKnownIssues: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("%s: schema violations: %+v", currentKnownIssuesPath, violations)
	}

	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue.Kind != KindKnownIssue {
			t.Errorf("%s: %s has kind %q", currentKnownIssuesPath, issue.ID, issue.Kind)
		}
		if _, duplicate := seen[issue.ID]; duplicate {
			t.Errorf("%s: duplicate issue id %q", currentKnownIssuesPath, issue.ID)
		}
		seen[issue.ID] = struct{}{}
	}
}

// reusableWorkflowPath is a2ahub's own reusable validation workflow — the
// unit every space's ~5-line caller references by tag. Read from disk rather
// than embedded: it is repo infrastructure, not product data, and no package
// owns it (space-template embeds the CALLER, not this).
const reusableWorkflowPath = "../../.github/workflows/a2a-validate-reusable.yml"

// TestReusableRefDefaultMatchesTheNewestAuthoredVersion holds the one coupling
// in this repo that has now shipped WRONG TWICE, in the same way, two releases
// apart — and whose existing guard could not catch either time because it is
// opt-in.
//
// The chain, each link read in source:
//
//	a space's .github/workflows/a2a-validate.yml passes `mode` and `base` and
//	NOTHING ELSE (space-template's caller, and `r22d222/a2a` live)
//	  -> so the reusable workflow's own `a2a-ref` DEFAULT decides which a2a
//	     binary runs `validate --ci` at that space's merge gate
//	  -> and a space pins the reusable workflow by TAG, which is immutable
//
// So a tag that ships a stale `a2a-ref` default hands every space that pins it
// a validator from some earlier release, permanently, and says nothing. v0.7.0
// shipped with it at v0.5.0. v0.10.0 shipped with it at v0.9.1 — so `getvisa`
// spent its first day enforcing none of v0.10.0's own CI-side work (space.yaml
// validation, event/v1 validation, the producer stamp, thread-required) while
// its notes called all four shipped.
//
// `release-preflight.sh` (assert_ref_default_matches) already asserts exactly
// this, correctly, and was written after the v0.7.0 miss. It did not prevent
// the v0.10.0 miss, and the reason is the whole point of this test: it takes an
// explicit VERSION argument and a network call, so it lives OUTSIDE `make
// check` and runs only when somebody remembers. A guard nobody is forced to run
// is a guard that documents a defect rather than preventing one.
//
// The invariant here is offline and exact because the version being cut is
// already named in the tree: the NEWEST authored release-notes file. That is
// not incidental — TestLoad_CorpusIntegrity above states the practice outright
// ("0.11.0 is present BEFORE its tag, deliberately"). Notes are written while
// the changes are fresh, so the newest corpus version IS the next tag, from the
// moment the wave starts until it ships.
func TestReusableRefDefaultMatchesTheNewestAuthoredVersion(t *testing.T) {
	t.Parallel()

	all, err := Load(releasenotes.FS)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("the embedded release-notes corpus is empty — the scan is broken, not the workflow")
	}
	// Load sorts version-ASCENDING through internal/version.OlderThan, so the
	// last entry is the newest. Semver, never lexicographic: a string compare
	// would rank 0.9.1 above 0.10.0 and this gate would go quietly green on
	// exactly the skew it exists to catch.
	cutting := all[len(all)-1].Version

	raw, err := os.ReadFile(reusableWorkflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", reusableWorkflowPath, err)
	}

	got, err := reusableRefDefault(string(raw))
	if err != nil {
		t.Fatalf("%s: %v", reusableWorkflowPath, err)
	}

	if strings.TrimPrefix(got, "v") != cutting {
		t.Fatalf("validator skew: %s pins a2a-ref default %q, but the newest authored "+
			"release-notes version — the one being cut — is %q.\n\n"+
			"A space's caller passes no a2a-ref of its own, so this default IS the binary that "+
			"runs `validate --ci` at that space's merge gate. Tags are immutable, so shipping a "+
			"stale value here cannot be corrected after the fact: every space pinned to that tag "+
			"validates with the old binary until somebody runs `a2a space update`, and nothing "+
			"reports it. v0.7.0 and v0.10.0 both shipped this way.\n\n"+
			"Fix: set the a2a-ref default to \"v%s\" BEFORE tagging.\n"+
			"NB the space-template's pin and min_binary_version do NOT move with it — those name "+
			"an already-PUBLISHED tag and bump after the release (docs/runbooks/release.md).\n"+
			"If you are cutting a HOTFIX off an older line while newer notes already exist, this "+
			"gate is asking for the wrong version: fix the ordering, do not weaken the gate.",
			reusableWorkflowPath, got, cutting, cutting)
	}
}

// reusableRefDefault returns the `default:` of the reusable workflow's
// `a2a-ref` input. Scoped to that input's own block rather than matched
// globally, because three inputs declare a default (`base` is "", `space-path`
// is ".") and a global match would read whichever came first — the shape of
// bug this file exists to catch, reintroduced in the check itself.
func reusableRefDefault(yml string) (string, error) {
	const key = "a2a-ref:"
	lines := strings.Split(yml, "\n")

	i := 0
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == key {
			break
		}
	}
	if i == len(lines) {
		return "", fmt.Errorf("declares no %s input — the reusable workflow's shape changed, "+
			"so this scan is what is broken; re-scope it before trusting a green", key)
	}

	indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
	for i++; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A line indented no deeper than `a2a-ref:` itself ends its block —
		// the next input, or the end of `inputs:`.
		if len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			break
		}
		if v, ok := strings.CutPrefix(trimmed, "default:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`), nil
		}
	}
	return "", fmt.Errorf("the %s input declares no default — every space caller would then "+
		"have to name a validator version itself", key)
}

// TestReusableRefDefault_ScopesToItsOwnInput is the teeth for the scan above.
// The gate is only as good as this function, and the way it would fail is by
// returning SOME default — a green that means nothing. Two of the four cases
// below are that shape: `base` declares `default: ""` before `a2a-ref` and
// `space-path` declares `default: "."` after it, so a scan that is not scoped
// reads one of those and reports agreement it never checked.
func TestReusableRefDefault_ScopesToItsOwnInput(t *testing.T) {
	t.Parallel()

	const realShape = `on:
  workflow_call:
    inputs:
      mode:
        required: true
        type: string
      base:
        required: false
        type: string
        default: ""
      a2a-ref:
        # a comment, and a blank line, inside the block
        required: false
        type: string

        default: "v1.2.3"
      space-path:
        required: false
        type: string
        default: "."
`

	tests := []struct {
		name    string
		yml     string
		want    string
		wantErr string
	}{
		{
			name: "reads its own default, not the neighbours'",
			yml:  realShape,
			want: "v1.2.3",
		},
		{
			name: "an input with no default is an error, never a neighbour's value",
			yml: strings.Replace(realShape,
				"\n        default: \"v1.2.3\"\n", "\n", 1),
			wantErr: "declares no default",
		},
		{
			name:    "a renamed or removed input fails the scan rather than passing",
			yml:     strings.Replace(realShape, "a2a-ref:", "a2a-version:", 1),
			wantErr: "declares no a2a-ref: input",
		},
		{
			name: "an unquoted default is read as written",
			yml:  strings.Replace(realShape, `default: "v1.2.3"`, "default: v1.2.3", 1),
			want: "v1.2.3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := reusableRefDefault(tc.yml)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("reusableRefDefault = %q, want an error containing %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("reusableRefDefault: %v", err)
			}
			if got != tc.want {
				t.Errorf("reusableRefDefault = %q, want %q", got, tc.want)
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
	// Synthetic corpus on purpose. Since is a pure range filter — its
	// behavior has nothing to do with which releases happen to be shipped,
	// and pinning it to the real corpus made every release edit this test's
	// open-ended cases. The corpus itself is guarded by
	// TestLoad_CorpusIntegrity, which is where that assertion belongs.
	all := []ReleaseNotes{
		{Version: "0.2.0"}, {Version: "0.3.0"}, {Version: "0.4.0"},
	}

	got := versionsOf(Since(all, "0.2.0", "0.4.0"))
	want := []string{"0.3.0", "0.4.0"}
	assertVersionsEqual(t, "Since(0.2.0,0.4.0)", got, want)

	got = versionsOf(Since(all, "", "0.3.0"))
	want = []string{"0.2.0", "0.3.0"}
	assertVersionsEqual(t, `Since("",0.3.0)`, got, want)

	got = versionsOf(Since(all, "0.3.0", ""))
	want = []string{"0.4.0"}
	assertVersionsEqual(t, `Since(0.3.0,"")`, got, want)

	got = versionsOf(Since(all, "", ""))
	want = []string{"0.2.0", "0.3.0", "0.4.0"}
	assertVersionsEqual(t, `Since("","")`, got, want)
}

func TestLoadCurrentKnownIssuesAndAttachIndependentlyOfSince(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		currentKnownIssuesPath: &fstest.MapFile{Data: []byte(`
schema: known-issues/v1
issues:
  - id: KI-MACOS-ADHOC-SIGNING
    kind: known-issue
    impact: normal
    subject: "macOS may ask once"
    detail: "The companion is ad-hoc signed."
    action:
      scope: none
      why: "Approve only the verified release."
`)},
	}
	issues, err := LoadCurrentKnownIssues(fsys)
	if err != nil {
		t.Fatalf("LoadCurrentKnownIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "KI-MACOS-ADHOC-SIGNING" {
		t.Fatalf("issues = %+v, want the standing macOS issue", issues)
	}
	badFS := fstest.MapFS{
		currentKnownIssuesPath: &fstest.MapFile{Data: []byte("issues: [not: valid")},
	}
	if _, err := LoadCurrentKnownIssues(badFS); !errors.Is(err, ErrKnownIssuesInvalid) {
		t.Fatalf("malformed current issues error = %v, want ErrKnownIssuesInvalid", err)
	}

	all := []ReleaseNotes{
		{Schema: "release-notes/v1", Version: "0.8.0", Changes: []Change{
			{ID: "RN-0800-8", Kind: KindKnownIssue, Subject: "an old issue declaration"},
		}},
		{Schema: "release-notes/v1", Version: "0.9.0", Changes: []Change{
			{ID: "RN-0900-1", Kind: "fix", Subject: "the next release"},
		}},
	}

	// The standing issue is not physically repeated in 0.9.0. It must still
	// reach an updater crossing 0.8.0 -> 0.9.0.
	got := AttachCurrentKnownIssues(Since(all, "0.8.0", "0.9.0"), all, issues)
	if len(got) != 1 || got[0].Version != "0.9.0" {
		t.Fatalf("attached range = %+v, want only the 0.9.0 release carrier", got)
	}
	if len(got[0].Changes) != 2 || got[0].Changes[1].ID != "KI-MACOS-ADHOC-SIGNING" {
		t.Fatalf("0.9.0 changes = %+v, want the standing issue appended", got[0].Changes)
	}

	// Even an empty strict version range still reports current limitations:
	// the newest release is used only as a stable carrier for the unchanged
	// []ReleaseNotes machine contract.
	got = AttachCurrentKnownIssues(Since(all, "0.9.0", "0.9.0"), all, issues)
	if len(got) != 1 || len(got[0].Changes) != 1 || got[0].Changes[0].ID != "KI-MACOS-ADHOC-SIGNING" {
		t.Fatalf("empty range with standing issue = %+v", got)
	}
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
