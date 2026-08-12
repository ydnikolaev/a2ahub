package artifact

import "testing"

func TestCleanRelativePath(t *testing.T) {
	t.Parallel()

	valid := []string{
		"a",
		"a.json",
		"a/b/c.json",
		"data/foo-bar_baz.09.json",
		"a-b_c.d",
	}
	for _, path := range valid {
		if !CleanRelativePath(path) {
			t.Errorf("CleanRelativePath(%q) = false, want true", path)
		}
	}

	invalid := map[string]string{
		"":                "empty",
		".":               "dot",
		"..":              "dot-dot alone",
		"/etc/passwd":     "absolute path",
		"a/../../b":       "traversal climbing above root",
		"../a":            "leading traversal",
		"a/..":            "trailing traversal",
		"a/../b":          "internal traversal",
		"a//b":            "empty segment (double slash)",
		"a/./b":           "dot segment not clean-normalized",
		`a\b`:             "backslash",
		"C:\\foo":         "windows drive letter + backslash",
		"c:foo":           "windows drive letter without slash",
		"a\x00b":          "embedded NUL byte",
		"a\x01b":          "embedded control character",
		"a\tb":            "embedded tab control character",
		"café.json":       "NFC-composed non-ASCII byte",
		"cafe\u0301.json": "NFD-decomposed non-ASCII byte (combining acute)",
		"a b.json":        "embedded space",
		"a:b.json":        "embedded colon (not the drive-letter case)",
		"a$(rm).json":     "shell metacharacter",
	}
	for path, reason := range invalid {
		if CleanRelativePath(path) {
			t.Errorf("CleanRelativePath(%q) = true, want false (%s)", path, reason)
		}
	}
}

func TestCleanRelativePathNFCAndNFDBothRejectedIdentically(t *testing.T) {
	t.Parallel()

	// "café" composed (NFC, single U+00E9) vs decomposed (NFD, e + U+0301).
	// Byte-for-byte these are different strings; both must be refused by the
	// same ASCII-only rule, proving there is no normalization step whose
	// absence would let one form through and not the other.
	nfc := "cafe\u0301.json" // decomposed form used as the baseline value
	nfd := "café.json"       // composed form (already normalized by the Go source)
	if nfc == nfd {
		t.Fatal("test fixture error: NFC and NFD forms must differ byte-for-byte")
	}
	if CleanRelativePath(nfc) {
		t.Errorf("CleanRelativePath(NFD form) = true, want false")
	}
	if CleanRelativePath(nfd) {
		t.Errorf("CleanRelativePath(NFC form) = true, want false")
	}
}

func TestIsDataPackageReadmePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "the packed README — the exact incident shape",
			path: "seomatrix/data/DP-seomatrix-20260808-7xaa/README.md",
			want: true,
		},
		{
			name: "case-insensitive basename — classifyPackEntries's own EqualFold match",
			path: "seomatrix/data/DP-seomatrix-20260808-7xaa/readme.md",
			want: true,
		},
		{
			name: "a DP- id whose OWN embedded system disagrees with the directory it sits under — the real verify-report shape",
			path: "peer/data/DP-axon-20260804-ab12/README.md",
			want: true,
		},
		{
			name: "the manifest itself — not the readme",
			path: "seomatrix/data/DP-seomatrix-20260808-7xaa/manifest.json",
			want: false,
		},
		{
			name: "a nested payload file, not the root-level readme",
			path: "seomatrix/data/DP-seomatrix-20260808-7xaa/rows/README.md",
			want: false,
		},
		{
			name: "a genuine artifact elsewhere under the same system — must NOT match",
			path: "seomatrix/exchanges/XW-seomatrix-20260808-ab12.md",
			want: false,
		},
		{
			name: "no data segment",
			path: "seomatrix/README.md",
			want: false,
		},
		{
			name: "a DP- id shaped path under a directory that is not literally \"data\"",
			path: "seomatrix/notdata/DP-seomatrix-20260808-7xaa/README.md",
			want: false,
		},
		{
			name: "not a DP- id at all",
			path: "seomatrix/data/XH-seomatrix-20260808-7xaa/README.md",
			want: false,
		},
		{
			name: "path traversal",
			path: "seomatrix/data/../DP-seomatrix-20260808-7xaa/README.md",
			want: false,
		},
		{
			name: "the package directory has no trailing file",
			path: "seomatrix/data/DP-seomatrix-20260808-7xaa",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsDataPackageReadmePath(tt.path); got != tt.want {
				t.Fatalf("IsDataPackageReadmePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCaseCollisions(t *testing.T) {
	t.Parallel()

	t.Run("no collision among distinct-lowercase paths", func(t *testing.T) {
		t.Parallel()
		got := CaseCollisions([]string{"a.json", "b.json", "dir/c.json"})
		if len(got) != 0 {
			t.Fatalf("CaseCollisions = %v, want empty", got)
		}
	})

	t.Run("case-only collision reported", func(t *testing.T) {
		t.Parallel()
		got := CaseCollisions([]string{"Foo.json", "foo.json"})
		if len(got) != 1 || got[0] != "foo.json" {
			t.Fatalf("CaseCollisions = %v, want [foo.json] (the lexicographically later path)", got)
		}
	})

	t.Run("exact duplicate is not a collision", func(t *testing.T) {
		t.Parallel()
		got := CaseCollisions([]string{"foo.json", "foo.json"})
		if len(got) != 0 {
			t.Fatalf("CaseCollisions = %v, want empty for an exact duplicate", got)
		}
	})

	t.Run("three-way collision reports every later path", func(t *testing.T) {
		t.Parallel()
		got := CaseCollisions([]string{"FOO.json", "Foo.json", "foo.json"})
		if len(got) != 2 {
			t.Fatalf("CaseCollisions = %v, want 2 later-colliding paths", got)
		}
	})

	t.Run("nested-directory case collision", func(t *testing.T) {
		t.Parallel()
		got := CaseCollisions([]string{"Data/File.json", "data/file.json"})
		if len(got) != 1 {
			t.Fatalf("CaseCollisions = %v, want 1 collision across a case-differing directory component too", got)
		}
	})
}
