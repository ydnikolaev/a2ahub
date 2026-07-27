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

// TestMajor pins Major's extraction and its fail-closed shape (P4 wave 5,
// Edge 1: the retire gate's major filter).
func TestMajor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		in           string
		want         int
		wantParseErr bool
	}{
		{name: "bare major", in: "1.2.3", want: 1},
		{name: "v prefix tolerated", in: "v2.0.0", want: 2},
		{name: "missing components default to 0", in: "3", want: 3},
		{name: "major zero", in: "0.9.9", want: 0},
		{name: "unparseable fails closed", in: "not-a-version", wantParseErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Major(tc.in)
			if tc.wantParseErr {
				if !errors.Is(err, ErrInvalidVersion) {
					t.Fatalf("Major(%q) error = %v, want ErrInvalidVersion", tc.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Major(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Major(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestBaseline pins P4 wave 5's Edge 2 rule: baseline = max{v ∈ published :
// v < target}, NOT the globally-highest published version. The maintenance-
// line case (AC-8) is the one that matters most: publishing 1.2 while 2.0
// is already published must compare against 1.1, not 2.0.
func TestBaseline(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		published    []string
		target       string
		wantBaseline string
		wantFound    bool
		wantParseErr bool
	}{
		{
			name:      "first-ever publish has no baseline",
			published: nil,
			target:    "1.0.0",
			wantFound: false,
		},
		{
			name:         "ordinary sequential publish picks the immediate prior",
			published:    []string{"1.0.0"},
			target:       "2.0.0",
			wantBaseline: "1.0.0",
			wantFound:    true,
		},
		{
			name:         "maintenance line: 1.2 while 2.0 published compares against 1.1, not 2.0 (AC-8)",
			published:    []string{"1.0.0", "1.1.0", "2.0.0"},
			target:       "1.2.0",
			wantBaseline: "1.1.0",
			wantFound:    true,
		},
		{
			name:      "target equal to a published version is not < it, so it is excluded",
			published: []string{"1.0.0", "1.0.0"},
			target:    "1.0.0",
			wantFound: false,
		},
		{
			name:         "unordered input still finds the correct max",
			published:    []string{"2.0.0", "1.0.0", "1.1.0"},
			target:       "1.2.0",
			wantBaseline: "1.1.0",
			wantFound:    true,
		},
		{
			name:      "downgrade below every published version has no baseline",
			published: []string{"1.0.0", "2.0.0"},
			target:    "0.5.0",
			wantFound: false,
		},
		{
			name:         "unparseable target fails closed",
			published:    []string{"1.0.0"},
			target:       "not-a-version",
			wantParseErr: true,
		},
		{
			name:         "unparseable published entry fails closed",
			published:    []string{"not-a-version"},
			target:       "1.0.0",
			wantParseErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			baseline, found, err := Baseline(tc.published, tc.target)
			if tc.wantParseErr {
				if !errors.Is(err, ErrInvalidVersion) {
					t.Fatalf("Baseline(%v, %q) error = %v, want ErrInvalidVersion", tc.published, tc.target, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Baseline(%v, %q): %v", tc.published, tc.target, err)
			}
			if found != tc.wantFound {
				t.Fatalf("Baseline(%v, %q) found = %v, want %v", tc.published, tc.target, found, tc.wantFound)
			}
			if found && baseline != tc.wantBaseline {
				t.Fatalf("Baseline(%v, %q) = %q, want %q", tc.published, tc.target, baseline, tc.wantBaseline)
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
