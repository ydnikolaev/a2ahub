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
