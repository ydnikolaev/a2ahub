package notes

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
	"gopkg.in/yaml.v3"
)

// TestLoad_CorpusIntegrity is the P31 corpus-integrity gate
// (make-check-enforced): the embedded release-notes corpus must load, be
// version-ascending, and every file must both schema-validate and satisfy
// this package's own invariants (version matches filename, change ids
// unique within a file).
func TestLoad_CorpusIntegrity(t *testing.T) {
	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Deliberately hardcoded: this is the tripwire that catches a corpus
	// file accidentally dropped or mis-ordered. Bump it when you cut a
	// release — that edit IS the check.
	if len(all) != 64 {
		t.Fatalf("Load returned %d entries, want 64: %+v", len(all), all)
	}

	// 0.11.0 is present BEFORE its tag, deliberately: its entries were
	// written while the changes were fresh, which is when release notes are
	// actually accurate. Its `released:` date is provisional and is corrected
	// when the tag is cut — and this tripwire is what forces whoever cuts it
	// to open the file, so the correction cannot be forgotten.
	wantVersions := []string{"0.2.0", "0.3.0", "0.4.0", "0.5.0", "0.6.0", "0.6.1", "0.6.2", "0.6.3", "0.6.4", "0.7.0", "0.8.0", "0.9.0", "0.9.1", "0.10.0", "0.11.0", "0.12.0", "0.13.0", "0.15.0", "0.15.1", "0.15.2", "0.16.0", "0.16.1", "0.16.2", "0.16.3", "0.17.0", "0.17.1", "0.18.0", "0.18.1", "0.18.2", "0.19.0", "0.19.1", "0.19.2", "0.19.3", "0.19.4", "0.19.5", "0.19.6", "0.19.7", "0.19.8", "0.19.9", "0.19.10", "0.19.11", "0.20.0", "0.21.0", "0.22.0", "0.23.0", "0.24.0", "0.25.0", "0.25.1", "0.25.2", "0.25.3", "0.25.4", "0.25.5", "0.25.6", "0.25.7", "0.25.8", "0.26.0", "0.26.1", "0.26.2", "0.26.3", "0.27.0", "0.27.1", "0.27.2", "0.27.3", "0.27.4"}
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
				if change.Kind == adoption.KindKnownIssue {
					t.Errorf("%s.yaml: current known issue %q must live only in %s",
						rn.Version, change.ID, StandingPath)
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

	issues, err := adoption.LoadStanding(releasenotes.FS, StandingPath)
	if err != nil {
		t.Fatalf("LoadStanding: %v", err)
	}
	raw, err := releasenotes.FS.ReadFile(StandingPath)
	if err != nil {
		t.Fatalf("read %s: %v", StandingPath, err)
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: yaml.Unmarshal: %v", StandingPath, err)
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
		t.Fatalf("%s: schema violations: %+v", StandingPath, violations)
	}

	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue.Kind != adoption.KindKnownIssue {
			t.Errorf("%s: %s has kind %q", StandingPath, issue.ID, issue.Kind)
		}
		if _, duplicate := seen[issue.ID]; duplicate {
			t.Errorf("%s: duplicate issue id %q", StandingPath, issue.ID)
		}
		seen[issue.ID] = struct{}{}
	}
}

// reusableWorkflowDir holds a2ahub's own reusable workflows — the units
// every space's ~5-line caller references by tag. Read from disk rather
// than embedded: it is repo infrastructure, not product data, and no
// package owns it (space-template embeds the CALLERS, not these).
const reusableWorkflowDir = "../../.github/workflows"

// reusableWorkflowPaths is the DERIVED cover set for the pin-freshness gate
// below: every `*-reusable.yml` file under reusableWorkflowDir, sorted for
// deterministic test output. A second reusable workflow (space-notify-2026-08
// P5's a2a-notify-reusable.yml) is covered by CONSTRUCTION the moment it
// exists on disk — no second hardcoded const, no second test function.
//
// This is deliberate: a hardcoded single-path const is exactly the shape
// that shipped this coupling WRONG TWICE (see
// TestReusableRefDefaultMatchesTheNewestAuthoredVersion's own doc comment)
// — its own guard was opt-in because nothing forced a NEW reusable workflow
// to be added to it. A glob has no such opt-in step: any file matching the
// `*-reusable.yml` naming convention this repo already uses is discovered,
// not enumerated.
func reusableWorkflowPaths(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(reusableWorkflowDir, "*-reusable.yml"))
	if err != nil {
		t.Fatalf("glob %s/*-reusable.yml: %v", reusableWorkflowDir, err)
	}
	if len(matches) == 0 {
		t.Fatalf("no *-reusable.yml file found under %s — the discovery glob is broken, not the workflows", reusableWorkflowDir)
	}
	sort.Strings(matches)
	return matches
}

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

	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
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

	// Every *-reusable.yml under .github/workflows/ is covered, DERIVED —
	// see reusableWorkflowPaths' own doc comment for why a second hardcoded
	// path would repeat the exact defect this test exists to close.
	for _, path := range reusableWorkflowPaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			got, err := reusableRefDefault(string(raw))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}

			if strings.TrimPrefix(got, "v") != cutting {
				t.Fatalf("validator skew: %s pins a2a-ref default %q, but the newest authored "+
					"release-notes version — the one being cut — is %q.\n\n"+
					"A space's caller passes no a2a-ref of its own, so this default IS the binary that "+
					"runs the pinned verb at that space's merge gate / CI. Tags are immutable, so shipping a "+
					"stale value here cannot be corrected after the fact: every space pinned to that tag "+
					"runs the old binary until somebody runs `a2a space update`, and nothing "+
					"reports it. v0.7.0 and v0.10.0 both shipped this way for the validator.\n\n"+
					"Fix: set the a2a-ref default to \"v%s\" BEFORE tagging.\n"+
					"NB the space-template's pin and min_binary_version do NOT move with it — those name "+
					"an already-PUBLISHED tag and bump after the release (docs/runbooks/release.md).\n"+
					"If you are cutting a HOTFIX off an older line while newer notes already exist, this "+
					"gate is asking for the wrong version: fix the ordering, do not weaken the gate.",
					path, got, cutting, cutting)
			}
		})
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

// TestParseReleaseNotes_UnreleasedSentinelPassesThrough is the other half of
// AC2 (one-answer-2026-08 P6): CLI (`a2a whatsnew`), the MCP
// StructuredContent, and internal/html's ReleaseNote projection all print
// Release.Released verbatim (they hold no special-case for it), so
// proving adoption.Parse preserves the sentinel string unmodified is
// sufficient evidence those three surfaces render it as "unreleased" too.
// RenderMarkdown (this package's own GitHub-Release-body renderer) is the one
// surface that is NOT a pass-through and gets its own test — see
// TestRenderMarkdownRendersUnreleasedSentinelAsProse in markdown_test.go.
func TestParseReleaseNotes_UnreleasedSentinelPassesThrough(t *testing.T) {
	raw := []byte(`
schema: release-notes/v1
version: "0.99.0"
released: unreleased
headline: "authored ahead of its tag"
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
	rn, err := adoption.Parse(raw)
	if err != nil {
		t.Fatalf("adoption.Parse: %v", err)
	}
	if rn.Released != adoption.Unreleased {
		t.Errorf("Released = %q, want the unreleased sentinel %q", rn.Released, adoption.Unreleased)
	}
}

// TestParseReleaseNotesRefusesAMalformedReleasedValue is the POLICY half of
// what one-answer-2026-08 P6 first tried to put in the frozen schema. The
// schema-only fixture corpus cannot express a policy-class refusal, so this
// is where the constraint is proven — the shape the operational-confidence
// guard's own teaching block prescribes for a byte-frozen v1 file.
func TestParseReleaseNotesRefusesAMalformedReleasedValue(t *testing.T) {
	t.Parallel()
	base := "schema: release-notes/v1\nversion: \"0.1.0\"\nheadline: h\nchanges: []\n"
	for _, tc := range []struct {
		name    string
		value   string
		refused bool
	}{
		{"an ISO date is accepted", "2026-08-13", false},
		{"the sentinel is accepted", adoption.Unreleased, false},
		{"an absent value is accepted — not-yet-chosen is not malformed", "", false},
		{"free prose is refused", "soon", true},
		{"a non-ISO date shape is refused", "13-08-2026", true},
		{"a near-miss sentinel is refused", "un-released", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := base
			if tc.value != "" {
				raw += "released: \"" + tc.value + "\"\n"
			}
			_, err := adoption.Parse([]byte(raw))
			if tc.refused && err == nil {
				t.Fatalf("released: %q should be refused as malformed, got no error", tc.value)
			}
			if !tc.refused && err != nil {
				t.Fatalf("released: %q should be accepted, got %v", tc.value, err)
			}
		})
	}
}

// A detect passes when its command exits 0. `a2a space update --dry-run`
// exits 0 with a diff pending and refuses a project connected to two spaces,
// so as a detect it never caught a space that was behind and could never
// clear in a multi-space repository (fucktory, 2026-11-08). `--check` is the
// spelling whose exit code answers the question.
func TestNoDetectRunsASpaceUpdateDryRun(t *testing.T) {
	t.Parallel()
	all, err := adoption.Load(releasenotes.FS, CorpusPattern)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, rn := range all {
		for _, ch := range rn.Changes {
			for _, cmd := range ch.Action.Detect {
				if strings.Contains(cmd, "space update") && strings.Contains(cmd, "--dry-run") {
					t.Errorf("%s %s: detect %q exits 0 while the space is behind; use `a2a space update --check`", rn.Version, ch.ID, cmd)
				}
			}
		}
	}
}
