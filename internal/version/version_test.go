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
