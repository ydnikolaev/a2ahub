package contract

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
)

func TestFullShippedLegacyCorpusMatchesDigestTreeFS(t *testing.T) {
	t.Parallel()

	const wantLegacyCorpusFiles = 69 // Every shipped envelope/event/manifest/consumes v1 schema + fixture file.
	schemasRoot := filepath.Join("..", "..", "schemas")
	digestRoot := t.TempDir()
	var candidates []CandidateFile

	for _, family := range []string{"envelope", "event", "manifest", "consumes"} {
		familyRoot := filepath.Join(schemasRoot, family, "v1")
		schemaPaths, err := filepath.Glob(filepath.Join(familyRoot, "*.schema.json"))
		if err != nil {
			t.Fatalf("glob shipped %s/v1 schemas: %v", family, err)
		}
		for _, source := range schemaPaths {
			addLegacyCorpusFile(t, digestRoot, source, filepath.Join("schema", family, filepath.Base(source)), &candidates)
		}

		fixtureRoot := filepath.Join(familyRoot, "fixtures")
		err = filepath.WalkDir(fixtureRoot, func(source string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(fixtureRoot, source)
			if relErr != nil {
				return relErr
			}
			addLegacyCorpusFile(t, digestRoot, source, filepath.Join("fixtures", family, rel), &candidates)
			return nil
		})
		if err != nil {
			t.Fatalf("walk shipped %s/v1 fixtures: %v", family, err)
		}
	}
	if len(candidates) != wantLegacyCorpusFiles {
		t.Fatalf("shipped legacy corpus contains %d files, want pinned full corpus of %d", len(candidates), wantLegacyCorpusFiles)
	}

	wantDigest, wantPerFile, err := artifact.DigestTreeFS(digestRoot, []string{"schema", "fixtures"})
	if err != nil {
		t.Fatalf("DigestTreeFS over full shipped legacy corpus: %v", err)
	}
	set, issues := BuildCarriedSet(ProfileContractTreeV1, nil, Descriptor{}, candidates)
	assertNoIssues(t, issues)
	if set.AggregateDigest != wantDigest {
		t.Fatalf("contract-tree-v1 digest = %s, DigestTreeFS = %s", set.AggregateDigest, wantDigest)
	}
	if !maps.Equal(set.PerFileDigest, wantPerFile) {
		t.Fatalf("contract-tree-v1 per-file projection diverged from DigestTreeFS")
	}
}

func addLegacyCorpusFile(t *testing.T, digestRoot, source, relative string, candidates *[]CandidateFile) {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read shipped legacy corpus file %s: %v", source, err)
	}
	destination := filepath.Join(digestRoot, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create legacy digest corpus directory: %v", err)
	}
	if err := os.WriteFile(destination, raw, 0o644); err != nil {
		t.Fatalf("write legacy digest corpus file: %v", err)
	}
	*candidates = append(*candidates, CandidateFile{
		Path: strings.TrimPrefix(filepath.ToSlash(relative), "./"),
		Kind: CandidateRegular,
		Raw:  raw,
	})
}
