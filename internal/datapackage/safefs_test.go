package datapackage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeLocalFile(t *testing.T, root, relPath string, contents []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func generousBounds() Bounds {
	return Bounds{MaxEntryBytes: 1 << 20, MaxPackageBytes: 1 << 30, MaxEntries: 1000, MaxRecords: 1000}
}

// --- WalkLocalDirectory: happy path ---

func TestWalkLocalDirectoryReturnsSortedFlatSet(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "b.json", []byte("b"))
	writeLocalFile(t, source, "a/nested.json", []byte("nested"))
	writeLocalFile(t, source, "a.json", []byte("a"))

	files, err := WalkLocalDirectory(t.Context(), source, generousBounds())
	if err != nil {
		t.Fatalf("WalkLocalDirectory = %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("len(files) = %d, want 3", len(files))
	}
	want := []string{"a.json", "a/nested.json", "b.json"}
	for i, w := range want {
		if files[i].RelPath != w {
			t.Errorf("files[%d].RelPath = %q, want %q", i, files[i].RelPath, w)
		}
	}
	for _, f := range files {
		if f.RelPath == "a.json" && string(f.Bytes) != "a" {
			t.Errorf("a.json bytes = %q, want %q", f.Bytes, "a")
		}
	}
}

// --- adversarial: symlink entry (walker) ---

func TestWalkLocalDirectoryRefusesSymlinkEntry(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	// The symlink target is INSIDE source deliberately: os.Root permits
	// following a symlink whose target stays within the root (only an
	// escaping target is refused by os.Root itself), so an internal target
	// is what actually exercises this package's own explicit symlink
	// refusal rather than os.Root's separate, unrelated containment check.
	writeLocalFile(t, source, "decoy.json", []byte("secret"))
	// A RELATIVE target ("decoy.json", not an absolute path) is essential:
	// os.Root refuses every absolute symlink target unconditionally,
	// regardless of where it points, so an absolute target would be caught
	// by os.Root's own unrelated refusal instead of this package's check.
	if err := os.Symlink("decoy.json", filepath.Join(source, "linked.json")); err != nil {
		t.Fatal(err)
	}

	_, err := WalkLocalDirectory(t.Context(), source, generousBounds())
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("WalkLocalDirectory = %v, want ErrSymlink", err)
	}
}

func TestWalkLocalDirectoryRefusesSymlinkDirectory(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalFile(t, outside, "sub/file.json", []byte("x"))
	if err := os.Symlink(filepath.Join(outside, "sub"), filepath.Join(source, "sub")); err != nil {
		t.Fatal(err)
	}

	_, err := WalkLocalDirectory(t.Context(), source, generousBounds())
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("WalkLocalDirectory = %v, want ErrSymlink", err)
	}
}

// --- adversarial: symlink swapped between Lstat and read (real TOCTOU) ---

func TestReadDataPackageFileRefusesSymlinkSwappedBetweenLstatAndRead(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	writeLocalFile(t, source, "target.json", []byte("original"))
	// The swap-in target is deliberately INSIDE source: os.Root permits
	// following a symlink whose target stays within the root, so if this
	// swap were caught only by os.Root's own (unrelated) escape protection
	// rather than by this package's post-open identity check, an
	// internal-target swap would slip through where an external one would
	// not. Using an internal target proves the identity check itself, not
	// os.Root's separate containment guarantee.
	writeLocalFile(t, source, "decoy.json", []byte("attacker-controlled"))

	swapped := false
	hooks := dataPackageHooks{
		afterLeafLstat: func(relative string) {
			if swapped || relative != "target.json" {
				return
			}
			swapped = true
			leaf := filepath.Join(source, "target.json")
			if err := os.Remove(leaf); err != nil {
				t.Fatal(err)
			}
			// Relative target, for the same reason as the symlink-entry
			// test above: os.Root refuses every absolute target
			// unconditionally, so a relative target is required to prove
			// this package's own post-open identity check rather than
			// os.Root's separate absolute-target refusal.
			if err := os.Symlink("decoy.json", leaf); err != nil {
				t.Fatal(err)
			}
		},
	}

	_, err := walkLocalDirectory(t.Context(), source, generousBounds(), hooks)
	if !swapped {
		t.Fatal("TOCTOU hook never fired; test did not exercise the race")
	}
	if !errors.Is(err, ErrSymlink) {
		t.Fatalf("walkLocalDirectory after swap = %v, want ErrSymlink", err)
	}
}

func TestWalkLocalDirectoryRefusesDirectorySwappedBetweenLstatAndOpen(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalFile(t, source, "dir/file.json", []byte("inside"))
	// The swap-in target is a second directory INSIDE source, for the same
	// reason as the leaf TOCTOU test above: os.Root follows an
	// internal-target symlink, so only this package's own post-open
	// identity re-check — not os.Root's separate escape protection — can
	// catch an internal-target swap.
	if err := os.MkdirAll(filepath.Join(source, "decoy-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeLocalFile(t, source, "decoy-dir/file.json", []byte("decoy"))

	swapped := false
	hooks := dataPackageHooks{
		afterDirectoryLstat: func(relative string) {
			if swapped || relative != "dir" {
				return
			}
			swapped = true
			if err := os.Rename(filepath.Join(source, "dir"), filepath.Join(source, "dir-old")); err != nil {
				t.Fatal(err)
			}
			// Relative target, for the same os.Root absolute-target refusal
			// reason as above.
			if err := os.Symlink("decoy-dir", filepath.Join(source, "dir")); err != nil {
				t.Fatal(err)
			}
		},
	}

	_, err := walkLocalDirectory(t.Context(), source, generousBounds(), hooks)
	if !swapped {
		t.Fatal("TOCTOU hook never fired; test did not exercise the race")
	}
	if !errors.Is(err, ErrSymlink) && !errors.Is(err, ErrPathEscapes) {
		t.Fatalf("walkLocalDirectory after directory swap = %v, want ErrSymlink or ErrPathEscapes", err)
	}
}

// --- bounds enforced at the walker (pack side) ---

func TestWalkLocalDirectoryEnforcesPerEntryByteBound(t *testing.T) {
	t.Parallel()

	t.Run("at limit passes", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		writeLocalFile(t, source, "f.json", make([]byte, 10))
		_, err := WalkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 10, MaxPackageBytes: 1000, MaxEntries: 10, MaxRecords: 10})
		if err != nil {
			t.Fatalf("WalkLocalDirectory at limit = %v, want nil", err)
		}
	})

	t.Run("one over refuses", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		writeLocalFile(t, source, "f.json", make([]byte, 11))
		_, err := WalkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 10, MaxPackageBytes: 1000, MaxEntries: 10, MaxRecords: 10})
		if !errors.Is(err, ErrBoundExceeded) {
			t.Fatalf("WalkLocalDirectory one over = %v, want ErrBoundExceeded", err)
		}
	})
}

// TestReadDataPackageFileEnforcesReadCapIndependentlyOfTheSizePreCheck proves
// the read-cap in readDataPackageFileContext is a distinct guard from the
// before.Size() pre-check in readDataPackageFile, not merely decoration
// beside it: the file passes the pre-check (its Lstat'd size is exactly at
// the bound) and only grows past the bound after that check, between the
// hook firing and the read. Only the read-cap can catch this.
func TestReadDataPackageFileEnforcesReadCapIndependentlyOfTheSizePreCheck(t *testing.T) {
	t.Parallel()
	source := t.TempDir()
	leaf := filepath.Join(source, "f.json")
	writeLocalFile(t, source, "f.json", make([]byte, 10)) // exactly at the bound

	grown := false
	hooks := dataPackageHooks{
		afterLeafLstat: func(relative string) {
			if grown || relative != "f.json" {
				return
			}
			grown = true
			if err := os.WriteFile(leaf, make([]byte, 11), 0o644); err != nil { // now one over
				t.Fatal(err)
			}
		},
	}

	_, err := walkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 10, MaxPackageBytes: 1000, MaxEntries: 10, MaxRecords: 10}, hooks)
	if !grown {
		t.Fatal("growth hook never fired; test did not exercise the race")
	}
	if !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("walkLocalDirectory after growth past the pre-check = %v, want ErrBoundExceeded", err)
	}
}

func TestWalkLocalDirectoryEnforcesPackageByteBound(t *testing.T) {
	t.Parallel()

	t.Run("at limit passes", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		writeLocalFile(t, source, "a.json", make([]byte, 5))
		writeLocalFile(t, source, "b.json", make([]byte, 5))
		_, err := WalkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 10, MaxEntries: 10, MaxRecords: 10})
		if err != nil {
			t.Fatalf("WalkLocalDirectory at limit = %v, want nil", err)
		}
	})

	t.Run("one over refuses", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		writeLocalFile(t, source, "a.json", make([]byte, 5))
		writeLocalFile(t, source, "b.json", make([]byte, 6))
		_, err := WalkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 10, MaxEntries: 10, MaxRecords: 10})
		if !errors.Is(err, ErrBoundExceeded) {
			t.Fatalf("WalkLocalDirectory one over = %v, want ErrBoundExceeded", err)
		}
	})
}

func TestWalkLocalDirectoryEnforcesEntryCountBound(t *testing.T) {
	t.Parallel()

	t.Run("at limit passes", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		for i := range 3 {
			writeLocalFile(t, source, filepath.Join("f", indexName(i)), []byte("x"))
		}
		_, err := WalkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 1000, MaxEntries: 3, MaxRecords: 10})
		if err != nil {
			t.Fatalf("WalkLocalDirectory at limit = %v, want nil", err)
		}
	})

	t.Run("one over refuses", func(t *testing.T) {
		t.Parallel()
		source := t.TempDir()
		for i := range 4 {
			writeLocalFile(t, source, filepath.Join("f", indexName(i)), []byte("x"))
		}
		_, err := WalkLocalDirectory(t.Context(), source, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 1000, MaxEntries: 3, MaxRecords: 10})
		if !errors.Is(err, ErrBoundExceeded) {
			t.Fatalf("WalkLocalDirectory one over = %v, want ErrBoundExceeded", err)
		}
	})
}

func indexName(i int) string {
	return "file-" + string(rune('a'+i)) + ".json"
}

// --- InstallLocalFiles: adversarial path shapes ---

func TestInstallLocalFilesRefusesTraversalPath(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{{RelPath: "../../etc/passwd", Bytes: []byte("x")}}

	_, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if !errors.Is(err, ErrPathEscapes) {
		t.Fatalf("InstallLocalFiles(traversal) = %v, want ErrPathEscapes", err)
	}
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after refusal: %v", statErr)
	}
}

func TestInstallLocalFilesRefusesAbsolutePath(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{{RelPath: "/etc/passwd", Bytes: []byte("x")}}

	_, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if !errors.Is(err, ErrPathEscapes) {
		t.Fatalf("InstallLocalFiles(absolute) = %v, want ErrPathEscapes", err)
	}
	if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("destination exists after refusal: %v", statErr)
	}
}

// --- InstallLocalFiles: fresh install, divergence and acceptance ---
//
// A fresh (non-existent) destination goes through renameNoReplace, which is
// linux/darwin only (safefs_noreplace_{linux,darwin,unsupported}.go). The
// fresh-install cases are therefore guarded on atomicNoReplaceSupported;
// the compare/accept/refuse half needs no rename at all and runs everywhere.

func TestInstallLocalFilesInstallsAFreshDestinationAtomically(t *testing.T) {
	t.Parallel()
	if !atomicNoReplaceSupported {
		t.Skip("atomic no-replace install is linux/darwin only, by design (plan D-4)")
	}
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte("alpha")},
		{RelPath: "dir/nested/b.json", Bytes: []byte("beta")},
	}

	outcome, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if err != nil {
		t.Fatalf("InstallLocalFiles(fresh) = %v, want nil", err)
	}
	if outcome != Installed {
		t.Fatalf("InstallLocalFiles(fresh) outcome = %q, want %q", outcome, Installed)
	}
	for _, f := range files {
		raw, readErr := os.ReadFile(filepath.Join(destination, filepath.FromSlash(f.RelPath)))
		if readErr != nil {
			t.Fatalf("read %s: %v", f.RelPath, readErr)
		}
		if string(raw) != string(f.Bytes) {
			t.Fatalf("%s = %q, want %q", f.RelPath, raw, f.Bytes)
		}
	}
	// No temporary sibling may survive a successful install: the whole point
	// of the rename is that the destination appears complete or not at all.
	siblings, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(siblings) != 1 || siblings[0].Name() != "dest" {
		names := make([]string, 0, len(siblings))
		for _, s := range siblings {
			names = append(names, s.Name())
		}
		t.Fatalf("parent holds %v after install, want only [dest]", names)
	}
}

// A second install of the same bytes must be accepted rather than refused —
// this is the re-fetch the retry loop depends on, and it is the case a
// "refuse any non-empty destination" reading would have broken (plan D-5).
func TestInstallLocalFilesIsIdempotentAcrossTwoRuns(t *testing.T) {
	t.Parallel()
	if !atomicNoReplaceSupported {
		t.Skip("atomic no-replace install is linux/darwin only, by design (plan D-4)")
	}
	destination := filepath.Join(t.TempDir(), "dest")
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte("alpha")}}

	first, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if err != nil || first != Installed {
		t.Fatalf("first install = (%q, %v), want (installed, nil)", first, err)
	}
	second, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if err != nil {
		t.Fatalf("second install = %v, want nil", err)
	}
	if second != AlreadyPresent {
		t.Fatalf("second install outcome = %q, want %q", second, AlreadyPresent)
	}
}

func TestInstallLocalFilesLeavesNothingBehindWhenBoundsRefuse(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	bounds := generousBounds()
	bounds.MaxEntryBytes = 4
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte("far too long")}}

	if _, err := InstallLocalFiles(t.Context(), destination, files, bounds); !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("InstallLocalFiles(over bound) = %v, want ErrBoundExceeded", err)
	}
	siblings, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(siblings) != 0 {
		t.Fatalf("parent holds %d entries after a refused install, want none", len(siblings))
	}
}

func TestInstallLocalFilesAcceptsByteIdenticalDestination(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte("alpha")},
		{RelPath: "dir/b.json", Bytes: []byte("beta")},
	}
	writeLocalFile(t, destination, "a.json", []byte("alpha"))
	writeLocalFile(t, destination, "dir/b.json", []byte("beta"))

	outcome, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if err != nil {
		t.Fatalf("InstallLocalFiles(identical) = %v, want nil", err)
	}
	if outcome != AlreadyPresent {
		t.Fatalf("InstallLocalFiles(identical) outcome = %q, want %q", outcome, AlreadyPresent)
	}
}

func TestInstallLocalFilesRefusesDivergentDestinationLeavingItUnchanged(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte("alpha")}}
	writeLocalFile(t, destination, "a.json", []byte("DIFFERENT"))

	_, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if !errors.Is(err, ErrDestinationDiverged) {
		t.Fatalf("InstallLocalFiles(divergent) = %v, want ErrDestinationDiverged", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(destination, "a.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(raw) != "DIFFERENT" {
		t.Fatalf("destination content = %q after refusal, want unchanged %q", raw, "DIFFERENT")
	}
}

func TestInstallLocalFilesRefusesDestinationWithMissingFile(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte("alpha")},
		{RelPath: "b.json", Bytes: []byte("beta")},
	}
	writeLocalFile(t, destination, "a.json", []byte("alpha")) // b.json missing

	_, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if !errors.Is(err, ErrDestinationDiverged) {
		t.Fatalf("InstallLocalFiles(missing file) = %v, want ErrDestinationDiverged", err)
	}
}

func TestInstallLocalFilesRefusesDestinationWithExtraFile(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte("alpha")}}
	writeLocalFile(t, destination, "a.json", []byte("alpha"))
	writeLocalFile(t, destination, "extra.json", []byte("unexpected"))

	_, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if !errors.Is(err, ErrDestinationDiverged) {
		t.Fatalf("InstallLocalFiles(extra file) = %v, want ErrDestinationDiverged", err)
	}
}

func TestInstallLocalFilesRefusesSymlinkAtDestinationRoot(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	outside := t.TempDir()
	destination := filepath.Join(parent, "dest")
	if err := os.Symlink(outside, destination); err != nil {
		t.Fatal(err)
	}
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte("alpha")}}

	_, err := InstallLocalFiles(t.Context(), destination, files, generousBounds())
	if !errors.Is(err, ErrDestinationDiverged) {
		t.Fatalf("InstallLocalFiles(symlink destination) = %v, want ErrDestinationDiverged", err)
	}
}

// --- adversarial: symlink swapped during destination compare (TOCTOU) ---

func TestCompareInstalledDirectoryRefusesSymlinkSwappedDuringCompare(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	writeLocalFile(t, destination, "a.json", []byte("alpha"))
	// The swap-in target is inside destination itself, for the same reason
	// as the walker's own TOCTOU tests: an internal-target symlink is one
	// os.Root will follow, so only this package's own post-open identity
	// re-check can catch it.
	writeLocalFile(t, destination, "decoy.json", []byte("attacker"))
	files := []LocalFile{{RelPath: "a.json", Bytes: []byte("alpha")}}

	swapped := false
	hooks := dataPackageHooks{
		afterCompareLeafLstat: func(relative string) {
			if swapped || relative != "a.json" {
				return
			}
			swapped = true
			leaf := filepath.Join(destination, "a.json")
			if err := os.Remove(leaf); err != nil {
				t.Fatal(err)
			}
			// Relative target, for the same os.Root absolute-target refusal
			// reason as the walker's own TOCTOU tests above.
			if err := os.Symlink("decoy.json", leaf); err != nil {
				t.Fatal(err)
			}
		},
	}

	_, err := installLocalFiles(t.Context(), destination, files, generousBounds(), hooks)
	if !swapped {
		t.Fatal("TOCTOU hook never fired; test did not exercise the race")
	}
	if err == nil {
		t.Fatal("installLocalFiles after compare-time swap = nil, want a refusal")
	}
}

// --- bounds enforced at InstallLocalFiles (fetch side) ---

func TestInstallLocalFilesEnforcesPerEntryByteBound(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	t.Run("at limit passes (already-present)", func(t *testing.T) {
		t.Parallel()
		destination := filepath.Join(parent, "at-limit")
		files := []LocalFile{{RelPath: "a.json", Bytes: make([]byte, 10)}}
		writeLocalFile(t, destination, "a.json", make([]byte, 10))
		if _, err := InstallLocalFiles(t.Context(), destination, files, Bounds{MaxEntryBytes: 10, MaxPackageBytes: 1000, MaxEntries: 10, MaxRecords: 10}); err != nil {
			t.Fatalf("InstallLocalFiles at limit = %v, want nil", err)
		}
	})

	t.Run("one over refuses", func(t *testing.T) {
		t.Parallel()
		destination := filepath.Join(parent, "over-limit")
		files := []LocalFile{{RelPath: "a.json", Bytes: make([]byte, 11)}}
		_, err := InstallLocalFiles(t.Context(), destination, files, Bounds{MaxEntryBytes: 10, MaxPackageBytes: 1000, MaxEntries: 10, MaxRecords: 10})
		if !errors.Is(err, ErrBoundExceeded) {
			t.Fatalf("InstallLocalFiles one over = %v, want ErrBoundExceeded", err)
		}
		if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
			t.Fatalf("destination exists after refusal: %v", statErr)
		}
	})
}

func TestInstallLocalFilesEnforcesPackageByteBound(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()

	t.Run("at limit passes (already-present)", func(t *testing.T) {
		t.Parallel()
		destination := filepath.Join(parent, "at-limit")
		files := []LocalFile{
			{RelPath: "a.json", Bytes: make([]byte, 5)},
			{RelPath: "b.json", Bytes: make([]byte, 5)},
		}
		writeLocalFile(t, destination, "a.json", make([]byte, 5))
		writeLocalFile(t, destination, "b.json", make([]byte, 5))
		if _, err := InstallLocalFiles(t.Context(), destination, files, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 10, MaxEntries: 10, MaxRecords: 10}); err != nil {
			t.Fatalf("InstallLocalFiles at limit = %v, want nil", err)
		}
	})

	t.Run("one over refuses", func(t *testing.T) {
		t.Parallel()
		destination := filepath.Join(parent, "over-limit")
		files := []LocalFile{
			{RelPath: "a.json", Bytes: make([]byte, 5)},
			{RelPath: "b.json", Bytes: make([]byte, 6)},
		}
		_, err := InstallLocalFiles(t.Context(), destination, files, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 10, MaxEntries: 10, MaxRecords: 10})
		if !errors.Is(err, ErrBoundExceeded) {
			t.Fatalf("InstallLocalFiles one over = %v, want ErrBoundExceeded", err)
		}
		if _, statErr := os.Lstat(destination); !os.IsNotExist(statErr) {
			t.Fatalf("destination exists after refusal: %v", statErr)
		}
	})
}

func TestInstallLocalFilesEnforcesEntryCountBound(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "dest")
	files := []LocalFile{
		{RelPath: "a.json", Bytes: []byte("a")},
		{RelPath: "b.json", Bytes: []byte("b")},
	}

	_, err := InstallLocalFiles(t.Context(), destination, files, Bounds{MaxEntryBytes: 100, MaxPackageBytes: 1000, MaxEntries: 1, MaxRecords: 10})
	if !errors.Is(err, ErrBoundExceeded) {
		t.Fatalf("InstallLocalFiles over entry count = %v, want ErrBoundExceeded", err)
	}
}
