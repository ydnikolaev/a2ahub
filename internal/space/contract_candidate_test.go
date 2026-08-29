package space

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ydnikolaev/a2ahub/internal/contract"
)

func TestReadContractCandidateFreezesExactBoundedSnapshot(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	root := filepath.Join(project, "candidate")
	if err := os.MkdirAll(filepath.Join(root, "fixtures", "valid"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeContractCandidateFile(t, root, "contract.md", "descriptor bytes\n")
	writeContractCandidateFile(t, root, "schema/order.schema.json", "schema bytes\n")
	writeContractCandidateFile(t, root, "fixtures/valid/order sample.json", "fixture bytes\n")
	writeContractCandidateFile(t, root, "artifacts/errors.yaml", "errors: []\n")

	got, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
		Path: "candidate", Source: ContractCandidateSourceStaging,
	})
	if err != nil {
		t.Fatalf("ReadContractCandidate: %v", err)
	}
	if got.Source != ContractCandidateSourceStaging {
		t.Fatalf("source = %q", got.Source)
	}
	if got.Fingerprint == "" {
		t.Fatal("empty exact candidate fingerprint")
	}
	wantPaths := []string{
		"artifacts/errors.yaml", "contract.md", "fixtures/valid/order sample.json", "schema/order.schema.json",
	}
	paths := make([]string, 0, len(got.Files))
	for _, file := range got.Files {
		paths = append(paths, file.Path)
		if file.Kind != contract.CandidateRegular || file.Mode != "100644" {
			t.Fatalf("file %q kind/mode = %q/%q", file.Path, file.Kind, file.Mode)
		}
	}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("paths = %q, want %q", paths, wantPaths)
	}
	core := got.CoreSnapshot()
	if string(core.Descriptor.Raw) != "descriptor bytes\n" || len(core.Files) != 3 {
		t.Fatalf("core snapshot = %+v", core)
	}

	firstFingerprint := got.Fingerprint
	writeContractCandidateFile(t, root, "schema/order.schema.json", "changed after collection\n")
	if string(got.file("schema/order.schema.json").Raw) != "schema bytes\n" {
		t.Fatal("snapshot bytes changed after the source file was rewritten")
	}
	if got.Fingerprint != firstFingerprint {
		t.Fatal("snapshot fingerprint changed after the source file was rewritten")
	}
}

func TestReadContractCandidateFingerprintCoversExecutableMode(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	root := filepath.Join(project, "candidate")
	if err := os.MkdirAll(filepath.Join(root, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeContractCandidateFile(t, root, "contract.md", "descriptor")
	writeContractCandidateFile(t, root, "schema/tool", "same bytes")
	regular, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
		Path: "candidate", Source: ContractCandidateSourceStaging,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "schema", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	executable, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
		Path: "candidate", Source: ContractCandidateSourceStaging,
	})
	if err != nil {
		t.Fatal(err)
	}
	if executable.file("schema/tool").Mode != "100755" {
		t.Fatalf("executable mode = %q", executable.file("schema/tool").Mode)
	}
	if executable.Fingerprint == regular.Fingerprint {
		t.Fatal("candidate fingerprint did not change with executable mode")
	}
}

func TestFrozenContractPublicationCandidateDerivesAndVerifiesExactGitTreeOID(t *testing.T) {
	t.Parallel()

	project, commit := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"candidate/contract.md": {content: "descriptor before\n", mode: 0o644},
		"candidate/schema/tool": {content: "#!/bin/sh\n", mode: 0o755},
	})
	root := filepath.Join(project, "candidate")
	reader, err := OpenContractCandidateReader(project, ContractCandidateLocation{Path: "candidate", Source: ContractCandidateSourceMirror})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := NewFrozenContractPublicationCandidate(t.Context(), reader, ContractPublicationMirrorTree{
		RepoDir: project, Commit: commit, Path: "candidate",
	})
	if err != nil {
		t.Fatalf("NewFrozenContractPublicationCandidate: %v", err)
	}
	tree, err := contractGitTreeObjectID(t.Context(), project, commit, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	writeContractCandidateFile(t, root, "contract.md", "descriptor after\n")

	snapshot, err := frozen.ReadContractPublicationCandidate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	source := frozen.ContractPublicationCandidateSource()
	if source.Kind != contract.CandidateSourceMirror || source.Location != tree || source.Fingerprint != snapshot.Fingerprint {
		t.Fatalf("source = %+v snapshot fingerprint=%q", source, snapshot.Fingerprint)
	}
	if got := string(snapshot.file("contract.md").Raw); got != "descriptor before\n" {
		t.Fatalf("frozen descriptor = %q", got)
	}
	if snapshot.file("schema/tool").Mode != "100755" {
		t.Fatalf("frozen executable mode = %q", snapshot.file("schema/tool").Mode)
	}
	writeContractCandidateFile(t, root, "contract.md", "descriptor before\n")
	if err := os.Chmod(filepath.Join(root, "schema", "tool"), 0o644); err != nil {
		t.Fatal(err)
	}

	reader, err = OpenContractCandidateReader(project, ContractCandidateLocation{Path: "candidate", Source: ContractCandidateSourceMirror})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := NewFrozenContractPublicationCandidate(t.Context(), reader, ContractPublicationMirrorTree{
		RepoDir: project, Commit: commit, Path: "candidate",
	}); !errors.Is(err, ErrContractPublicationInvalid) {
		t.Fatalf("tree/snapshot mismatch error = %v", err)
	}
}

func TestResolveContractPublicationCandidateCommitReturnsExactHead(t *testing.T) {
	t.Parallel()

	project, commit := newTreeCommitRepo(t, map[string]treeCommitFixtureFile{
		"candidate/contract.md": {content: "descriptor\n", mode: 0o644},
	})
	got, err := ResolveContractPublicationCandidateCommit(t.Context(), project)
	if err != nil {
		t.Fatalf("ResolveContractPublicationCandidateCommit: %v", err)
	}
	if got != commit {
		t.Fatalf("candidate commit = %q, want exact HEAD %q", got, commit)
	}
}

func TestReadContractCandidateRefusesSymlinkAndIrregularLeaves(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, root string) {
				outside := filepath.Join(t.TempDir(), "outside.json")
				writeContractCandidateFile(t, filepath.Dir(outside), filepath.Base(outside), "secret")
				if err := os.Symlink(outside, filepath.Join(root, "schema", "linked.json")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			root := filepath.Join(project, "candidate")
			if err := os.MkdirAll(filepath.Join(root, "schema"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeContractCandidateFile(t, root, "contract.md", "descriptor")
			tc.setup(t, root)

			_, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
				Path: "candidate", Source: ContractCandidateSourceMirror,
			})
			if !errors.Is(err, ErrContractUnsafeEntry) {
				t.Fatalf("ReadContractCandidate = %v, want ErrContractUnsafeEntry", err)
			}
		})
	}
}

func TestReadContractCandidateRefusesSocketLeaf(t *testing.T) {
	t.Parallel()

	project, err := os.MkdirTemp("/tmp", "a2ac-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(project) }) // reason: invocation-owned short path required by Unix socket limits
	root := filepath.Join(project, "candidate")
	if err := os.MkdirAll(filepath.Join(root, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeContractCandidateFile(t, root, "contract.md", "descriptor")
	socketPath := filepath.Join(root, "schema", "irregular.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		if runtime.GOOS == "windows" {
			return // Windows has no Unix-domain socket leaf to exercise; production still refuses every non-regular mode.
		}
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	_, err = ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
		Path: "candidate", Source: ContractCandidateSourceStaging,
	})
	if !errors.Is(err, ErrContractUnsafeEntry) {
		t.Fatalf("ReadContractCandidate(socket) = %v, want ErrContractUnsafeEntry", err)
	}
}

func TestReadContractCandidateEnforcesPerFileCountAndAggregateBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name: "per-file",
			setup: func(t *testing.T, root string) {
				writeContractCandidateBytes(t, root, "schema/large.json", bytes.Repeat([]byte{'x'}, contract.MaxFileBytes+1))
			},
		},
		{
			name: "count",
			setup: func(t *testing.T, root string) {
				for i := 0; i <= maxContractCandidateFiles; i++ {
					writeContractCandidateFile(t, root, filepath.ToSlash(filepath.Join("schema", contractCandidateIndexName(i))), "")
				}
			},
		},
		{
			name: "aggregate",
			setup: func(t *testing.T, root string) {
				for i := 0; i < maxContractCandidateBytes/contract.MaxFileBytes+1; i++ {
					writeContractCandidateBytes(t, root, filepath.ToSlash(filepath.Join("schema", contractCandidateIndexName(i))), bytes.Repeat([]byte{'x'}, contract.MaxFileBytes))
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			root := filepath.Join(project, "candidate")
			if err := os.MkdirAll(filepath.Join(root, "schema"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeContractCandidateFile(t, root, "contract.md", "descriptor")
			tc.setup(t, root)
			_, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
				Path: "candidate", Source: ContractCandidateSourceStaging,
			})
			if !errors.Is(err, ErrContractBoundedResult) {
				t.Fatalf("ReadContractCandidate = %v, want ErrContractBoundedResult", err)
			}
		})
	}
}

func TestReadContractCandidateAppliesProfileBoundsAfterRawFreeze(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	root := filepath.Join(project, "candidate")
	writeContractCandidateFile(t, root, "contract.md", contractV1Descriptor("1.0.0"))
	for i := 0; i < contract.MaxExplicitFiles; i++ {
		writeContractCandidateFile(t, root, filepath.ToSlash(filepath.Join("schema", contractCandidateIndexName(i))), "")
		writeContractCandidateFile(t, root, filepath.ToSlash(filepath.Join("artifacts", contractCandidateIndexName(i))), "")
	}
	chunk := bytes.Repeat([]byte{'x'}, contract.MaxFileBytes)
	for i := 0; i < contract.MaxAggregateBytes/contract.MaxFileBytes; i++ {
		writeContractCandidateBytes(t, root, filepath.ToSlash(filepath.Join("schema", contractCandidateIndexName(i))), chunk)
		writeContractCandidateBytes(t, root, filepath.ToSlash(filepath.Join("artifacts", contractCandidateIndexName(i))), chunk)
	}

	snapshot, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{Path: "candidate", Source: ContractCandidateSourceStaging})
	if err != nil {
		t.Fatalf("raw candidate freeze: %v", err)
	}
	set, issues := contract.ValidateCarriedSet(contract.ProfileContractTreeV1, contract.Descriptor{}, snapshot.CoreSnapshot())
	if len(issues) != 0 {
		t.Fatalf("legacy profile rejected descriptor/artifacts outside its carried count: %v", issues)
	}
	if len(set.Entries) != contract.MaxExplicitFiles {
		t.Fatalf("legacy carried entries = %d, want %d", len(set.Entries), contract.MaxExplicitFiles)
	}
	aggregate := 0
	for _, raw := range set.Bytes {
		aggregate += len(raw)
	}
	if aggregate != contract.MaxAggregateBytes {
		t.Fatalf("legacy carried aggregate = %d, want %d", aggregate, contract.MaxAggregateBytes)
	}
}

func TestReadContractCandidateEnforcesTopologyBounds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{
			name: "depth",
			setup: func(t *testing.T, root string) {
				relative := "schema"
				for i := 0; i < maxContractCandidateDepth; i++ {
					relative = filepath.Join(relative, "d")
				}
				if err := os.MkdirAll(filepath.Join(root, relative), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directories",
			setup: func(t *testing.T, root string) {
				for i := 0; i < maxContractCandidateDirectories; i++ {
					if err := os.MkdirAll(filepath.Join(root, "schema", contractCandidateIndexName(i)), 0o755); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "nodes",
			setup: func(t *testing.T, root string) {
				for i := 0; i < 600; i++ {
					if err := os.MkdirAll(filepath.Join(root, "schema", "dir-"+contractCandidateIndexName(i)), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				for i := 0; i < 425; i++ {
					writeContractCandidateFile(t, root, filepath.ToSlash(filepath.Join("schema", contractCandidateIndexName(i))), "")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			root := filepath.Join(project, "candidate")
			writeContractCandidateFile(t, root, "contract.md", "descriptor")
			tc.setup(t, root)
			_, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{Path: "candidate", Source: ContractCandidateSourceMirror})
			if !errors.Is(err, ErrContractBoundedResult) {
				t.Fatalf("topology bound error = %v", err)
			}
		})
	}
}

func TestContractCandidateReaderRefusesDeterministicComponentAndLeafSwap(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"directory", "leaf"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			root := filepath.Join(project, "candidate")
			outside := filepath.Join(project, "outside")
			writeContractCandidateFile(t, root, "contract.md", "descriptor")
			writeContractCandidateFile(t, root, "schema/main.json", "inside")
			writeContractCandidateFile(t, outside, "main.json", "outside")
			reader, err := OpenContractCandidateReader(project, ContractCandidateLocation{Path: "candidate", Source: ContractCandidateSourceMirror})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reader.Close() }()
			swapped := false
			if target == "directory" {
				reader.hooks.afterDirectoryLstat = func(path string) {
					if swapped || path != "schema" {
						return
					}
					swapped = true
					if err := os.Rename(filepath.Join(root, "schema"), filepath.Join(root, "schema-old")); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Join(root, "schema")); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				reader.hooks.afterLeafLstat = func(path string) {
					if swapped || path != "schema/main.json" {
						return
					}
					swapped = true
					leaf := filepath.Join(root, "schema", "main.json")
					if err := os.Rename(leaf, leaf+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(outside, "main.json"), leaf); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, err = reader.Read(t.Context())
			if !swapped || (!errors.Is(err, ErrContractUnsafePath) && !errors.Is(err, ErrContractUnsafeEntry)) {
				t.Fatalf("swap result = swapped %v, error %v", swapped, err)
			}
		})
	}
}

// TestReadRootContractFileLabelsDiagnosticBySide covers fb-20260829-e756c0: a
// missing file read from the caller-supplied local/staging subject must be
// reported with a "local:" prefix naming the inspected path, and the exact
// same failure read from the space's own mirror tree must still say
// "space:". readRootContractFile is the one seam every candidate read in
// this file funnels through (Read's descriptor read and walkContractDirectory's
// per-entry read both call it), so this is a direct, non-racy test of the
// label itself rather than of a filesystem race needed to reach it end to
// end.
func TestReadRootContractFileLabelsDiagnosticBySide(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		source     ContractCandidateSource
		wantPrefix string
	}{
		{name: "local subject", source: ContractCandidateSourceStaging, wantPrefix: "local: inspect contract file"},
		{name: "space mirror", source: ContractCandidateSourceMirror, wantPrefix: "space: inspect contract file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()

			_, err = readRootContractFile(t.Context(), root, "missing.json", "schema/missing.json", tc.source, nil)
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("readRootContractFile error = %v, want prefix %q", err, tc.wantPrefix)
			}
			if !strings.Contains(err.Error(), `"schema/missing.json"`) {
				t.Fatalf("readRootContractFile error = %v, want the inspected path named", err)
			}
		})
	}
}

// TestReadContractCandidateRefusesMissingDescriptorAsWrongShape covers the
// smaller half of fb-20260829-e756c0, on the exact file the record reported
// (contract.md, not an arbitrary schema file): a candidate directory that
// EXISTS but holds no contract.md must refuse with ErrContractCandidateShape
// naming the shape it wanted, not the generic wrapped stat error every other
// missing file in this package still reports (that message names
// --local/--staging, which reads as "you forgot a flag" rather than "your
// subject is the wrong shape"). The label must LEAD the message — the
// reporter's own failure mode was reading only the leading token — so this
// asserts HasPrefix, not merely Contains, and covers both sides: a caller's
// --local subject missing the shape (the reported case) and a broken mirror
// missing it too (the same refusal, correctly re-labelled "space:").
func TestReadContractCandidateRefusesMissingDescriptorAsWrongShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		source     ContractCandidateSource
		wantPrefix string
	}{
		{name: "local subject", source: ContractCandidateSourceStaging, wantPrefix: "local: candidate is not a published-shaped tree"},
		{name: "space mirror", source: ContractCandidateSourceMirror, wantPrefix: "space: candidate is not a published-shaped tree"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			project := t.TempDir()
			root := filepath.Join(project, "candidate")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			// The directory exists and is non-empty (an unrelated file), so
			// this is squarely "wrong shape", not "does not exist".
			writeContractCandidateFile(t, root, "README.txt", "not a contract\n")

			_, err := ReadContractCandidate(t.Context(), project, ContractCandidateLocation{
				Path: "candidate", Source: tc.source,
			})
			if !errors.Is(err, ErrContractCandidateShape) {
				t.Fatalf("ReadContractCandidate = %v, want ErrContractCandidateShape", err)
			}
			if err == nil || !strings.HasPrefix(err.Error(), tc.wantPrefix) {
				t.Fatalf("ReadContractCandidate error = %v, want prefix %q", err, tc.wantPrefix)
			}
			if !strings.Contains(err.Error(), "contract.md") {
				t.Fatalf("ReadContractCandidate error = %v, want the missing envelope named", err)
			}
		})
	}
}

func writeContractCandidateFile(t *testing.T, root, path, raw string) {
	t.Helper()
	writeContractCandidateBytes(t, root, path, []byte(raw))
}

func writeContractCandidateBytes(t *testing.T, root, path string, raw []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func contractCandidateIndexName(i int) string {
	return fmt.Sprintf("file-%04d.txt", i)
}
