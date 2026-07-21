package space

import (
	"errors"
	"testing"
)

func TestVersionOlderThan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		binary, min  string
		wantOlder    bool
		wantParseErr bool
	}{
		{name: "equal", binary: "0.1.0", min: "0.1.0", wantOlder: false},
		{name: "binary newer patch", binary: "0.1.1", min: "0.1.0", wantOlder: false},
		{name: "binary older patch", binary: "0.1.0", min: "0.1.1", wantOlder: true},
		{name: "binary older minor", binary: "0.0.9", min: "0.1.0", wantOlder: true},
		{name: "binary older major", binary: "0.9.9", min: "1.0.0", wantOlder: true},
		{name: "v prefix tolerated", binary: "v1.2.0", min: "1.1.0", wantOlder: false},
		{name: "missing components default to 0", binary: "1", min: "1.0.0", wantOlder: false},
		{name: "unparseable binary version fails closed", binary: "not-a-version", min: "1.0.0", wantParseErr: true},
		{name: "unparseable min version fails closed", binary: "1.0.0", min: "not-a-version", wantParseErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			older, err := versionOlderThan(tc.binary, tc.min)
			if tc.wantParseErr {
				if !errors.Is(err, ErrInvalidVersion) {
					t.Fatalf("versionOlderThan(%q, %q) error = %v, want ErrInvalidVersion", tc.binary, tc.min, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("versionOlderThan(%q, %q): %v", tc.binary, tc.min, err)
			}
			if older != tc.wantOlder {
				t.Fatalf("versionOlderThan(%q, %q) = %v, want %v", tc.binary, tc.min, older, tc.wantOlder)
			}
		})
	}
}
