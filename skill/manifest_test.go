package skill_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ydnikolaev/a2ahub/skill"
)

// fixtureTree builds a minimal skill tree carrying just a manifest, so a test
// can state a manifest and read back what the decode made of it. This is the
// reason LoadDocsManifest takes an fs.FS: the property "a section added to the
// manifest is reachable with no code change" is a property OF the decode, and
// a loader hardcoded to the embed could not be asked about it.
func fixtureTree(t *testing.T, manifest string) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{skill.DocsManifestPath: {Data: []byte(manifest)}}
}

const oneSection = `{
  "schema": "a2a-docs-manifest/v1",
  "groups": ["Start"],
  "loop_corpus": ["a2ahub/loops.md"],
  "sections": [{"id": "getting-started", "group": "Start", "title": "Getting started", "file": "a2ahub/onboarding.md"}]
}`

func TestLoadDocsManifestDecodesEveryKeyTheFileCarries(t *testing.T) {
	t.Parallel()

	got, err := skill.LoadDocsManifest(fixtureTree(t, oneSection))
	if err != nil {
		t.Fatalf("LoadDocsManifest: %v", err)
	}
	if got.Schema != skill.DocsManifestSchema {
		t.Errorf("Schema = %q, want %q", got.Schema, skill.DocsManifestSchema)
	}
	if len(got.Groups) != 1 || got.Groups[0] != "Start" {
		t.Errorf("Groups = %v, want [Start]", got.Groups)
	}
	// LoopCorpus has no Go reader today (scripts/check-loop-coverage.sh reads
	// the key). It is decoded anyway, and asserted here, so the field cannot be
	// quietly dropped by a later edit — see the type's own doc.
	if len(got.LoopCorpus) != 1 || got.LoopCorpus[0] != "a2ahub/loops.md" {
		t.Errorf("LoopCorpus = %v, want [a2ahub/loops.md]", got.LoopCorpus)
	}
	if len(got.Sections) != 1 || got.Sections[0].ID != "getting-started" || got.Sections[0].File != "a2ahub/onboarding.md" {
		t.Errorf("Sections = %+v, want one getting-started entry", got.Sections)
	}
}

// TestLoadDocsManifestSectionListFollowsTheManifest is the property the CLI's
// topic vocabulary rests on: the set of sections is whatever the manifest says,
// with no Go source consulted. Adding a row here adds a section, and nothing
// else in the codebase had to change for it to.
func TestLoadDocsManifestSectionListFollowsTheManifest(t *testing.T) {
	t.Parallel()

	extended := strings.Replace(
		oneSection,
		`{"id": "getting-started", "group": "Start", "title": "Getting started", "file": "a2ahub/onboarding.md"}`,
		`{"id": "getting-started", "group": "Start", "title": "Getting started", "file": "a2ahub/onboarding.md"},
                    {"id": "invented-topic", "group": "Start", "title": "Invented", "file": "a2ahub/invented.md"}`,
		1,
	)
	if extended == oneSection {
		t.Fatal("could not seed the extra section: the anchor string did not match")
	}

	got, err := skill.LoadDocsManifest(fixtureTree(t, extended))
	if err != nil {
		t.Fatalf("LoadDocsManifest: %v", err)
	}
	if len(got.Sections) != 2 || got.Sections[1].ID != "invented-topic" {
		t.Fatalf("Sections = %+v, want the manifest's two entries in order", got.Sections)
	}
}

func TestLoadDocsManifestRefuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		tree     fstest.MapFS
		wantWord string
	}{
		{"absent", fstest.MapFS{}, "read"},
		{"undecodable", fixtureTree(t, "{not json"), "decode"},
		{
			"unknown schema",
			fixtureTree(t, strings.Replace(oneSection, "a2a-docs-manifest/v1", "a2a-docs-manifest/v99", 1)),
			"schema",
		},
		{
			"no sections",
			fixtureTree(t, `{"schema":"a2a-docs-manifest/v1","groups":["Start"],"sections":[]}`),
			"0 sections",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := skill.LoadDocsManifest(tc.tree)
			if err == nil {
				t.Fatalf("LoadDocsManifest(%s) = nil error, want a refusal", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantWord) {
				t.Errorf("refusal %q does not name %q — a caller cannot act on it", err, tc.wantWord)
			}
		})
	}
}

// TestEmbeddedDocsManifestIsTheShippedTree proves the convenience wrapper reads
// the real embed, so internal/html's package-level initialization keeps working
// through this move.
func TestEmbeddedDocsManifestIsTheShippedTree(t *testing.T) {
	t.Parallel()

	got := skill.EmbeddedDocsManifest()
	if len(got.Sections) == 0 || len(got.Groups) == 0 {
		t.Fatalf("embedded manifest is empty: %+v", got)
	}
	for _, s := range got.Sections {
		if _, err := skill.Files.Open(s.File); err != nil {
			t.Errorf("section %q names %s, which is not in the embedded tree: %v", s.ID, s.File, err)
		}
	}
}
