package version

import (
	"errors"
	"testing"
)

// TestOlderThan carries the table cases moved from
// internal/space/version_test.go verbatim (spec 19 §7 anti-dup): the leaf
// is the new SSOT, internal/space now wraps it (see internal/space/version.go).
func TestOlderThan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		a, b         string
		wantOlder    bool
		wantParseErr bool
	}{
		{name: "equal", a: "0.1.0", b: "0.1.0", wantOlder: false},
		{name: "binary newer patch", a: "0.1.1", b: "0.1.0", wantOlder: false},
		{name: "binary older patch", a: "0.1.0", b: "0.1.1", wantOlder: true},
		{name: "binary older minor", a: "0.0.9", b: "0.1.0", wantOlder: true},
		{name: "binary older major", a: "0.9.9", b: "1.0.0", wantOlder: true},
		{name: "v prefix tolerated", a: "v1.2.0", b: "1.1.0", wantOlder: false},
		{name: "missing components default to 0", a: "1", b: "1.0.0", wantOlder: false},
		{name: "unparseable binary version fails closed", a: "not-a-version", b: "1.0.0", wantParseErr: true},
		{name: "unparseable min version fails closed", a: "1.0.0", b: "not-a-version", wantParseErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			older, err := OlderThan(tc.a, tc.b)
			if tc.wantParseErr {
				if !errors.Is(err, ErrInvalidVersion) {
					t.Fatalf("OlderThan(%q, %q) error = %v, want ErrInvalidVersion", tc.a, tc.b, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("OlderThan(%q, %q): %v", tc.a, tc.b, err)
			}
			if older != tc.wantOlder {
				t.Fatalf("OlderThan(%q, %q) = %v, want %v", tc.a, tc.b, older, tc.wantOlder)
			}
		})
	}
}

// TestCanonical pins the normalisation POL-011 depends on, and the reason it
// exists: `contract publish` writes its own parsed value while `deprecate`
// and `retire` write the operator's `--version` verbatim, so one version can
// reach a policy under two spellings.
func TestCanonical(t *testing.T) {
	t.Parallel()

	same := []struct{ in, want string }{
		{"1.0.0", "1.0.0"},
		{"01.0.0", "1.0.0"},
		{"1.00.0", "1.0.0"},
		{"1.0.00", "1.0.0"},
		{"v1.0.0", "1.0.0"},
		{"10.20.30", "10.20.30"},
		// The parser pads a short form, and for POL-011 that is right: "1.0"
		// and "1.0.0" name one version, so they must key alike.
		{"1.0", "1.0.0"},
		{"1", "1.0.0"},
	}
	for _, tc := range same {
		got, err := Canonical(tc.in)
		if err != nil {
			t.Errorf("Canonical(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// An unparseable spelling must ERROR rather than pass itself through:
	// a caller that silently accepts garbage as a key reintroduces exactly
	// the two-keys-for-one-version bug this function was written to close.
	for _, bad := range []string{"", "1.0.0.0", "one.0.0", "1.0.x"} {
		if got, err := Canonical(bad); err == nil {
			t.Errorf("Canonical(%q) = %q, want an error", bad, got)
		}
	}
}
