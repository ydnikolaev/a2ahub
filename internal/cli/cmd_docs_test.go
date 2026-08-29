package cli

import (
	"bytes"
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/skill"
)

// docsFixtureManifest is a two-group, four-section manifest: two "Start"
// sections, and two "Concepts" sections — one of which
// (fixture-missing-page) names a file this fixture tree deliberately does
// NOT carry, for the §6 edge case (a manifest section whose page is
// missing). IDs are deliberately synthetic (fixture-*), not real corpus
// topic ids: AC-4's own check greps for a literal real section id under
// internal/ and cmd/, and a test fixture that happened to spell one would
// read as exactly the section roster that check exists to forbid.
const docsFixtureManifest = `{
  "schema": "a2a-docs-manifest/v1",
  "groups": ["Start", "Concepts"],
  "loop_corpus": ["a2ahub/loops.md"],
  "sections": [
    {"id": "fixture-start-a", "group": "Start", "title": "Fixture Start A", "file": "a2ahub/onboarding.md"},
    {"id": "fixture-start-b", "group": "Start", "title": "Fixture Start B", "file": "a2ahub/overview.md"},
    {"id": "fixture-concept-a", "group": "Concepts", "title": "Fixture Concept A", "file": "a2ahub/loops.md"},
    {"id": "fixture-missing-page", "group": "Concepts", "title": "Fixture Missing Page", "file": "a2ahub/missing.md"}
  ]
}`

// docsFixtureTree builds a minimal skill tree over docsFixtureManifest —
// the fixture-FS shape skill/manifest_test.go establishes for
// skill.LoadDocsManifest, copied here rather than reinvented so AC-5 (a
// manifest addition becomes a topic with no code change) is provable
// against this command the same way it already is against the decode.
func docsFixtureTree(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		skill.DocsManifestPath: {Data: []byte(docsFixtureManifest)},
		"a2ahub/onboarding.md": {Data: []byte("Fixture start A body.\n")},
		"a2ahub/overview.md":   {Data: []byte("Fixture start B body.\n")},
		"a2ahub/loops.md":      {Data: []byte("Fixture concept A body.\n")},
		// a2ahub/missing.md is deliberately absent.
	}
}

func TestDocsListsVocabularyGroupedInManifestGroupOrder(t *testing.T) {
	t.Parallel()
	c := &DocsCommand{tree: docsFixtureTree(t)}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("Run() = %d, stderr=%q, want 0", code, errOut.String())
	}

	got := out.String()
	// The manifest declares groups [Start, Concepts]; within each group,
	// sections must appear in the manifest's own section order — not
	// alphabetical, not encounter order in some other structure.
	wantInOrder := []string{
		"Start:", "fixture-start-a", "Fixture Start A",
		"fixture-start-b", "Fixture Start B",
		"Concepts:", "fixture-concept-a", "Fixture Concept A",
		"fixture-missing-page", "Fixture Missing Page",
	}
	lastIdx := -1
	for _, want := range wantInOrder {
		idx := strings.Index(got, want)
		if idx < 0 {
			t.Fatalf("listing %q does not contain %q", got, want)
		}
		if idx < lastIdx {
			t.Fatalf("listing %q: %q appears out of manifest group order", got, want)
		}
		lastIdx = idx
	}
}

func TestDocsPrintsKnownTopicPageVerbatim(t *testing.T) {
	t.Parallel()
	c := &DocsCommand{tree: docsFixtureTree(t)}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"fixture-start-b"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("Run(fixture-start-b) = %d, stderr=%q, want 0", code, errOut.String())
	}
	if out.String() != "Fixture start B body.\n" {
		t.Fatalf("stdout = %q, want the fixture page verbatim", out.String())
	}
}

// TestDocsUnknownTopicRefusesNamingValidIDs is the table test AC-3 asks
// for: an unknown topic must refuse (never "see the docs") and NAME the
// vocabulary it actually holds.
func TestDocsUnknownTopicRefusesNamingValidIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		topic string
	}{
		{"nonexistent", "nope"},
		{"case-sensitive-miss", "FIXTURE-START-A"},
		{"empty-string-argument", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := &DocsCommand{tree: docsFixtureTree(t)}
			var out, errOut bytes.Buffer
			code := c.Run(context.Background(), []string{tc.topic}, IO{Stdout: &out, Stderr: &errOut})
			if code != 1 {
				t.Fatalf("Run(%q) = %d, stderr=%q, want 1", tc.topic, code, errOut.String())
			}
			if out.Len() != 0 {
				t.Fatalf("Run(%q): stdout = %q, want empty on refusal", tc.topic, out.String())
			}
			msg := errOut.String()
			for _, id := range []string{"fixture-start-a", "fixture-start-b", "fixture-concept-a", "fixture-missing-page"} {
				if !strings.Contains(msg, id) {
					t.Errorf("Run(%q): refusal %q does not name valid id %q", tc.topic, msg, id)
				}
			}
		})
	}
}

// TestDocsResolveTopic drives the pure resolver directly — no stdio, no
// exit code — the shape AC-3 names ("a table test over the topic
// resolver").
func TestDocsResolveTopic(t *testing.T) {
	t.Parallel()
	manifest, err := skill.LoadDocsManifest(docsFixtureTree(t))
	if err != nil {
		t.Fatalf("LoadDocsManifest: %v", err)
	}

	for _, tc := range []struct {
		name     string
		topic    string
		wantOK   bool
		wantFile string
	}{
		{"known", "fixture-start-b", true, "a2ahub/overview.md"},
		{"known-missing-page-entry-still-resolves", "fixture-missing-page", true, "a2ahub/missing.md"},
		{"unknown", "nope", false, ""},
		{"empty", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry, ok := docsResolveTopic(manifest, tc.topic)
			if ok != tc.wantOK {
				t.Fatalf("docsResolveTopic(%q) ok = %v, want %v", tc.topic, ok, tc.wantOK)
			}
			if ok && entry.File != tc.wantFile {
				t.Errorf("docsResolveTopic(%q).File = %q, want %q", tc.topic, entry.File, tc.wantFile)
			}
		})
	}
}

// TestDocsMissingPageRefusesNamingSectionAndAbsentPage is the §6 edge case:
// the manifest and the tree disagreeing (class C4) must refuse — naming
// both the section and the absent path — never crash.
func TestDocsMissingPageRefusesNamingSectionAndAbsentPage(t *testing.T) {
	t.Parallel()
	c := &DocsCommand{tree: docsFixtureTree(t)}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"fixture-missing-page"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("Run(fixture-missing-page) = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on refusal", out.String())
	}
	msg := errOut.String()
	if !strings.Contains(msg, "fixture-missing-page") {
		t.Errorf("refusal %q does not name the section id", msg)
	}
	if !strings.Contains(msg, "a2ahub/missing.md") {
		t.Errorf("refusal %q does not name the absent page path", msg)
	}
}

// TestDocsAddingManifestSectionAddsTopicWithNoCodeChange is AC-5: a
// section added to the manifest becomes a topic with no code change to
// this file — the property the "topic vocabulary IS the manifest" rule
// (AC-4) exists to guarantee.
func TestDocsAddingManifestSectionAddsTopicWithNoCodeChange(t *testing.T) {
	t.Parallel()
	base := docsFixtureTree(t)
	manifestBytes, err := fs.ReadFile(base, skill.DocsManifestPath)
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}
	extendedManifest := strings.Replace(
		string(manifestBytes),
		`{"id": "fixture-missing-page", "group": "Concepts", "title": "Fixture Missing Page", "file": "a2ahub/missing.md"}`,
		`{"id": "fixture-missing-page", "group": "Concepts", "title": "Fixture Missing Page", "file": "a2ahub/missing.md"},
                    {"id": "invented-topic", "group": "Concepts", "title": "Invented", "file": "a2ahub/invented.md"}`,
		1,
	)
	if extendedManifest == string(manifestBytes) {
		t.Fatal("could not seed the extra section: the anchor string did not match")
	}

	extended := fstest.MapFS{}
	for k, v := range base {
		extended[k] = v
	}
	extended[skill.DocsManifestPath] = &fstest.MapFile{Data: []byte(extendedManifest)}
	extended["a2ahub/invented.md"] = &fstest.MapFile{Data: []byte("Invented body.\n")}

	c := &DocsCommand{tree: extended}

	// The new topic lists...
	var listOut, listErr bytes.Buffer
	if code := c.Run(context.Background(), nil, IO{Stdout: &listOut, Stderr: &listErr}); code != 0 {
		t.Fatalf("Run() = %d, stderr=%q, want 0", code, listErr.String())
	}
	if !strings.Contains(listOut.String(), "invented-topic") {
		t.Fatalf("listing %q does not carry the manifest-added topic", listOut.String())
	}

	// ...and reads, with no code change beyond the manifest/tree fixture.
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"invented-topic"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("Run(invented-topic) = %d, stderr=%q, want 0", code, errOut.String())
	}
	if out.String() != "Invented body.\n" {
		t.Fatalf("stdout = %q, want the invented page verbatim", out.String())
	}
}

func TestDocsTooManyArgumentsIsUsageError(t *testing.T) {
	t.Parallel()
	c := &DocsCommand{tree: docsFixtureTree(t)}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), []string{"a", "b"}, IO{Stdout: &out, Stderr: &errOut})
	if code != 2 {
		t.Fatalf("Run(a, b) = %d, want 2 (usage)", code)
	}
}

func TestDocsBrokenCorpusRefusesRatherThanCrashing(t *testing.T) {
	t.Parallel()
	c := &DocsCommand{tree: fstest.MapFS{}}
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 1 {
		t.Fatalf("Run() over a manifest-less tree = %d, want 1", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("expected a refusal on stderr, got none")
	}
}

// TestDocsWorksInAnEmptyDirectoryWithNoProjectConfig is AC-6: the verb
// reads only the embedded skill tree, so it must work with no project
// config and no connected space — proven here by literally running it from
// an empty temp directory against the REAL embedded corpus
// (NewDocsCommand, not the fixture tree).
func TestDocsWorksInAnEmptyDirectoryWithNoProjectConfig(t *testing.T) {
	t.Chdir(t.TempDir())

	c := NewDocsCommand()
	var out, errOut bytes.Buffer
	code := c.Run(context.Background(), nil, IO{Stdout: &out, Stderr: &errOut})
	if code != 0 {
		t.Fatalf("Run() in an empty directory = %d, stderr=%q, want 0", code, errOut.String())
	}
	if out.Len() == 0 {
		t.Fatal("expected the real embedded topic vocabulary to be listed")
	}
}
