package lane

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"internal/localserver/**/*.go", "internal/localserver/foo.go", true},
		{"internal/localserver/**/*.go", "internal/localserver/sub/foo.go", true},
		{"internal/localserver/**/*.go", "internal/localserver/sub/deep/foo.go", true},
		{"internal/localserver/**/*.go", "internal/other/foo.go", false},

		// *.go must not cross a path separator.
		{"*.go", "x/y.go", false},
		{"*.go", "y.go", true},

		// a/**/b matches a/b (zero middle segments) as well as deeper.
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/y", false},
		{"a/**/b", "a", false},

		// a trailing /** matches the directory itself and its whole subtree.
		{"a/**", "a", true},
		{"a/**", "a/b", true},
		{"a/**", "a/b/c", true},
		{"a/**", "b", false},

		// no wildcard: exact segment match only.
		{"README.md", "README.md", true},
		{"README.md", "docs/README.md", false},

		// single-segment wildcard within one component.
		{"schemas/*.json", "schemas/contract.json", true},
		{"schemas/*.json", "schemas/nested/contract.json", false},

		// "**" alone matches everything, including the empty path.
		{"**", "a/b/c", true},
		{"**", "", true},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.name); got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestMatchingInputReportsTheWinningPattern(t *testing.T) {
	inputs := []string{"internal/localserver/**/*.go", "!internal/localserver/**/*_test.go"}
	if ok, pat := MatchingInput(inputs, "internal/localserver/foo.go"); !ok || pat != "internal/localserver/**/*.go" {
		t.Errorf("got (%v, %q), want (true, the include pattern)", ok, pat)
	}
	if ok, pat := MatchingInput(inputs, "internal/localserver/foo_test.go"); ok || pat != "!internal/localserver/**/*_test.go" {
		t.Errorf("got (%v, %q), want (false, the deciding exclusion)", ok, pat)
	}
	if ok, pat := MatchingInput(inputs, "README.md"); ok || pat != "" {
		t.Errorf("got (%v, %q), want (false, \"\") for a path no pattern touches", ok, pat)
	}
}

func TestMatchInputsExclusionOrdering(t *testing.T) {
	cases := []struct {
		name   string
		inputs []string
		path   string
		want   bool
	}{
		{
			name:   "plain include",
			inputs: []string{"internal/localserver/**/*.go"},
			path:   "internal/localserver/foo.go",
			want:   true,
		},
		{
			name:   "exclusion after include narrows",
			inputs: []string{"internal/localserver/**/*.go", "!internal/localserver/**/*_test.go"},
			path:   "internal/localserver/foo_test.go",
			want:   false,
		},
		{
			name:   "excluded path unaffected",
			inputs: []string{"internal/localserver/**/*.go", "!internal/localserver/**/*_test.go"},
			path:   "internal/localserver/foo.go",
			want:   true,
		},
		{
			name:   "later re-include wins over an earlier exclusion",
			inputs: []string{"internal/localserver/**/*.go", "!internal/localserver/**/*_test.go", "internal/localserver/fixture_test.go"},
			path:   "internal/localserver/fixture_test.go",
			want:   true,
		},
		{
			name:   "no matching pattern at all",
			inputs: []string{"schemas/**"},
			path:   "internal/foo.go",
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchInputs(c.inputs, c.path); got != c.want {
				t.Errorf("MatchInputs(%v, %q) = %v, want %v", c.inputs, c.path, got, c.want)
			}
		})
	}
}
