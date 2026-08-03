package host

import (
	"bytes"
	"strings"
	"testing"
)

func TestReadRemoteRecoveryTreeFilesReturnsExactBoundedRegularBytes(t *testing.T) {
	t.Parallel()
	fixture := newRemoteRecoveryFixture(t)
	writeRecoveryFile(t, fixture.source, "contracts/demo/contract.md", []byte("descriptor\n"))
	writeRecoveryFile(t, fixture.source, "contracts/demo/schema/main.json", []byte(`{"type":"object"}`))
	fixture.commitAll("snapshot")
	commit := gitOutput(t, fixture.source, "rev-parse", "HEAD")

	files, err := NewGitHubHost(nil, "").ReadRemoteRecoveryTreeFiles(t.Context(), fixture.source, commit, []string{
		"contracts/demo/schema/main.json", "contracts/demo/contract.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["contracts/demo/contract.md"].Mode != "100644" ||
		!bytes.Equal(files["contracts/demo/contract.md"].Raw, []byte("descriptor\n")) {
		t.Fatalf("files=%+v", files)
	}
}

func TestListRemoteRecoveryTreeFilesReadsOnlyRequestedPrefixes(t *testing.T) {
	t.Parallel()
	fixture := newRemoteRecoveryFixture(t)
	writeRecoveryFile(t, fixture.source, "contracts/demo/schema/main.json", []byte(`{"type":"object"}`))
	writeRecoveryFile(t, fixture.source, "contracts/demo/fixtures/valid.json", []byte(`{}`))
	writeRecoveryFile(t, fixture.source, "contracts/other/secret", []byte("outside"))
	fixture.commitAll("snapshot")
	commit := gitOutput(t, fixture.source, "rev-parse", "HEAD")

	files, err := NewGitHubHost(nil, "").ListRemoteRecoveryTreeFiles(t.Context(), fixture.source, commit, []string{
		"contracts/demo/schema", "contracts/demo/fixtures",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files["contracts/demo/schema/main.json"].Mode != "100644" {
		t.Fatalf("files=%+v", files)
	}
}

func TestReadRemoteRecoveryTreeFilesRejectsUnsafeDuplicateMissingAndOversizedSets(t *testing.T) {
	t.Parallel()
	fixture := newRemoteRecoveryFixture(t)
	writeRecoveryFile(t, fixture.source, "safe", []byte("ok"))
	fixture.commitAll("snapshot")
	commit := gitOutput(t, fixture.source, "rev-parse", "HEAD")
	host := NewGitHubHost(nil, "")
	for _, paths := range [][]string{{"../escape"}, {"safe", "safe"}, {"missing"}, make([]string, maxRecoveryChangedPaths+3)} {
		if _, err := host.ReadRemoteRecoveryTreeFiles(t.Context(), fixture.source, commit, paths); err == nil {
			t.Fatalf("paths %q accepted", paths)
		}
	}
	tooMany := make([]string, maxRecoveryChangedPaths+3)
	for index := range tooMany {
		tooMany[index] = "safe-" + strings.Repeat("0", index%3)
	}
	if _, err := host.ReadRemoteRecoveryTreeFiles(t.Context(), fixture.source, commit, tooMany); err == nil {
		t.Fatal("over-bound path set accepted")
	}
}
