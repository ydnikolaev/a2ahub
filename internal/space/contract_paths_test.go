package space

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenContractCandidateReaderRefusesUnsafeLocations(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	if err := os.Mkdir(filepath.Join(project, "candidate"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, "linked")); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"", ".", "../candidate", "/candidate", `candidate\schema`, "linked"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			reader, err := OpenContractCandidateReader(project, ContractCandidateLocation{
				Path: path, Source: ContractCandidateSourceStaging,
			})
			if err == nil {
				_ = reader.Close()
				t.Fatalf("OpenContractCandidateReader(%q) succeeded", path)
			}
			if !errors.Is(err, ErrContractUnsafePath) {
				t.Fatalf("OpenContractCandidateReader(%q) error = %v, want ErrContractUnsafePath", path, err)
			}
		})
	}
}

func TestOpenContractCandidateReaderMirrorAbsentIsNotInSpace(t *testing.T) {
	t.Parallel()

	mirror := t.TempDir()
	if err := os.MkdirAll(filepath.Join(mirror, "devkit", "provides"), 0o755); err != nil {
		t.Fatal(err)
	}
	loc := ContractCandidateLocation{Path: "devkit/provides/secrets-layout", Source: ContractCandidateSourceMirror}
	reader, err := OpenContractCandidateReader(mirror, loc)
	if err == nil {
		_ = reader.Close()
		t.Fatal("OpenContractCandidateReader succeeded for an absent contract")
	}
	if !errors.Is(err, ErrContractNotInSpace) || errors.Is(err, ErrContractUnsafePath) {
		t.Fatalf("error = %v, want ErrContractNotInSpace and not ErrContractUnsafePath", err)
	}

	// A missing staging directory is not a "submit first" case.
	loc.Source = ContractCandidateSourceStaging
	if reader, err = OpenContractCandidateReader(mirror, loc); err == nil {
		_ = reader.Close()
		t.Fatal("staging open succeeded")
	}
	if errors.Is(err, ErrContractNotInSpace) || !errors.Is(err, ErrContractUnsafePath) {
		t.Fatalf("staging error = %v, want ErrContractUnsafePath only", err)
	}

	// A symlinked mirror contract stays unsafe.
	if err := os.Symlink(t.TempDir(), filepath.Join(mirror, "devkit", "provides", "linked")); err != nil {
		t.Fatal(err)
	}
	loc = ContractCandidateLocation{Path: "devkit/provides/linked", Source: ContractCandidateSourceMirror}
	if _, err = OpenContractCandidateReader(mirror, loc); !errors.Is(err, ErrContractUnsafePath) || errors.Is(err, ErrContractNotInSpace) {
		t.Fatalf("symlink error = %v, want ErrContractUnsafePath only", err)
	}
}

func TestContractCandidateReaderRefusesConfiguredPathReplacement(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	candidate := filepath.Join(project, "candidate")
	if err := os.Mkdir(candidate, 0o755); err != nil {
		t.Fatal(err)
	}
	writeContractCandidateFile(t, candidate, "contract.md", "held")

	reader, err := OpenContractCandidateReader(project, ContractCandidateLocation{
		Path: "candidate", Source: ContractCandidateSourceStaging,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	held := filepath.Join(project, "held")
	if err := os.Rename(candidate, held); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeContractCandidateFile(t, outside, "contract.md", "outside")
	if err := os.Symlink(outside, candidate); err != nil {
		t.Fatal(err)
	}

	_, err = reader.Read(t.Context())
	if !errors.Is(err, ErrContractUnsafePath) {
		t.Fatalf("Read after configured path replacement = %v, want ErrContractUnsafePath", err)
	}
}
