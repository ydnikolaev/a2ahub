package feedback

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// commentHostileRoot is the SHARED adversarial corpus every reader of a
// feedback record is asked (judge-the-thing-2026-08 P10 §T1.3): four shell
// readers through their own `--teeth`, and this package through the tests
// below. It is a third sibling of the valid/ and invalid/ trees
// validate_test.go already reads off disk, and it is deliberately NOT swept
// by the schema oracle — those globs are named.
const commentHostileRoot = schemaFixturesRoot + "/comment-hostile"

// commentHostilePairs returns every <stem>.dirty.yaml in the shared corpus
// beside its byte-clean twin. The pair IS the assertion.
func commentHostilePairs(t *testing.T) map[string]string {
	t.Helper()
	dirty, err := filepath.Glob(filepath.Join(commentHostileRoot, "*.dirty.yaml"))
	if err != nil {
		t.Fatalf("glob the comment-hostile corpus: %v", err)
	}
	if len(dirty) < 6 {
		t.Fatalf("expected at least 6 comment-hostile pairs, found %d", len(dirty))
	}
	pairs := make(map[string]string, len(dirty))
	for _, d := range dirty {
		clean := strings.TrimSuffix(d, ".dirty.yaml") + ".clean.yaml"
		if _, err := os.Stat(clean); err != nil {
			t.Fatalf("%s has no .clean.yaml twin: %v", d, err)
		}
		pairs[d] = clean
	}
	return pairs
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// TestNormalizeRecord_GoldenPairs is AC #2 and AC #4 together, as a golden:
// the clean twin is not merely comment-free, it is the EXACT output of
// normalizing its dirty partner. A full yaml.v3 re-encode passes every value
// assertion in this file and fails this one, which is why it exists.
func TestNormalizeRecord_GoldenPairs(t *testing.T) {
	t.Parallel()
	for dirty, clean := range commentHostilePairs(t) {
		t.Run(filepath.Base(dirty), func(t *testing.T) {
			t.Parallel()
			got, err := normalizeRecord(readFixture(t, dirty))
			if err != nil {
				t.Fatalf("normalizeRecord: %v", err)
			}
			want := readFixture(t, clean)
			if string(got) != string(want) {
				t.Fatalf("normalized bytes are not the clean twin.\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestNormalizeRecord_CommentFree is AC #2, asked of the PARSER rather than
// of a regex: no node in the normalized document carries a head, line or
// foot comment at any depth.
func TestNormalizeRecord_CommentFree(t *testing.T) {
	t.Parallel()
	for dirty := range commentHostilePairs(t) {
		t.Run(filepath.Base(dirty), func(t *testing.T) {
			t.Parallel()
			got, err := normalizeRecord(readFixture(t, dirty))
			if err != nil {
				t.Fatalf("normalizeRecord: %v", err)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(got, &doc); err != nil {
				t.Fatalf("the normalized document does not parse: %v", err)
			}
			if c := firstComment(&doc); c != "" {
				t.Fatalf("a comment survived normalization: %q", c)
			}
		})
	}
}

// TestNormalizeRecord_ValueIdentical is AC #3: the normalized document parses
// to a value deeply equal to the original's, every field included.
func TestNormalizeRecord_ValueIdentical(t *testing.T) {
	t.Parallel()
	for dirty := range commentHostilePairs(t) {
		t.Run(filepath.Base(dirty), func(t *testing.T) {
			t.Parallel()
			raw := readFixture(t, dirty)
			got, err := normalizeRecord(raw)
			if err != nil {
				t.Fatalf("normalizeRecord: %v", err)
			}
			var before, after any
			if err := yaml.Unmarshal(raw, &before); err != nil {
				t.Fatalf("unmarshal input: %v", err)
			}
			if err := yaml.Unmarshal(got, &after); err != nil {
				t.Fatalf("unmarshal output: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("normalization changed a VALUE.\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

// TestNormalizeRecord_NonReflowing is AC #9/#4 stated as the shape of a
// line-level diff rather than as a value comparison: every output line is an
// input line, in order, either byte-identical or truncated at a comment; and
// every input line the output does not carry began with a `#`. Indentation,
// quote style, block-scalar indicator and wrap points therefore cannot move.
func TestNormalizeRecord_NonReflowing(t *testing.T) {
	t.Parallel()
	for dirty := range commentHostilePairs(t) {
		t.Run(filepath.Base(dirty), func(t *testing.T) {
			t.Parallel()
			raw := readFixture(t, dirty)
			got, err := normalizeRecord(raw)
			if err != nil {
				t.Fatalf("normalizeRecord: %v", err)
			}
			in := strings.Split(string(raw), "\n")
			out := strings.Split(string(got), "\n")
			i := 0
			for _, want := range out {
				for {
					if i >= len(in) {
						t.Fatalf("output line %q has no counterpart left in the input", want)
					}
					have := in[i]
					i++
					if have == want {
						break
					}
					if rest, ok := strings.CutPrefix(have, want); ok &&
						(strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t")) &&
						strings.HasPrefix(strings.TrimLeft(rest, " \t"), "#") {
						break // truncated exactly where a comment began
					}
					if !strings.HasPrefix(strings.TrimSpace(have), "#") {
						t.Fatalf("input line %q was dropped or rewritten, and it carried no comment (wanted %q)", have, want)
					}
				}
			}
			for ; i < len(in); i++ {
				if !strings.HasPrefix(strings.TrimSpace(in[i]), "#") {
					t.Fatalf("trailing input line %q was dropped, and it carried no comment", in[i])
				}
			}
		})
	}
}

// TestNormalizeRecord_Idempotent is AC #5: normalizing twice returns identical
// bytes, and an already-clean record comes back byte-identical.
func TestNormalizeRecord_Idempotent(t *testing.T) {
	t.Parallel()
	for dirty, clean := range commentHostilePairs(t) {
		t.Run(filepath.Base(dirty), func(t *testing.T) {
			t.Parallel()
			once, err := normalizeRecord(readFixture(t, dirty))
			if err != nil {
				t.Fatalf("normalizeRecord: %v", err)
			}
			twice, err := normalizeRecord(once)
			if err != nil {
				t.Fatalf("normalizeRecord (second pass): %v", err)
			}
			if string(once) != string(twice) {
				t.Fatalf("normalization is not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
			cleanRaw := readFixture(t, clean)
			cleanOut, err := normalizeRecord(cleanRaw)
			if err != nil {
				t.Fatalf("normalizeRecord (clean twin): %v", err)
			}
			if string(cleanOut) != string(cleanRaw) {
				t.Fatalf("a clean record was not returned byte-identical:\n--- got ---\n%s\n--- want ---\n%s", cleanOut, cleanRaw)
			}
		})
	}
}

// TestNormalizeRecord_HashAsData is AC #6 (US-7): the set is the criterion,
// not one representative. A `#` inside a double-quoted title, inside a
// single-quoted title and inside a folded block scalar is the reporter's own
// text, and the parser is what says so.
func TestNormalizeRecord_HashAsData(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fixture string
		survive string
	}{
		{"hash-in-double-quoted.dirty.yaml", `"a bug in the # channel"`},
		{"hash-in-single-quoted.dirty.yaml", `'the # sign renders as a heading'`},
		{"folded-summary.dirty.yaml", "# This heading is DATA, at any indentation"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeRecord(readFixture(t, filepath.Join(commentHostileRoot, tc.fixture)))
			if err != nil {
				t.Fatalf("normalizeRecord: %v", err)
			}
			if !strings.Contains(string(got), tc.survive) {
				t.Fatalf("normalization ate the reporter's own text %q:\n%s", tc.survive, got)
			}
		})
	}
}

// TestNormalizeRecord_HashWithoutLeadingSpaceIsData pins the boundary of the
// rule §5 says is already written down and is not to be re-derived: a `#`
// opens a comment only at the start of a line or after a space/tab.
func TestNormalizeRecord_HashWithoutLeadingSpaceIsData(t *testing.T) {
	t.Parallel()
	// `bug#42` is a value. A laxer rule would silently make it `bug`, and the
	// fail-closed round trip would then refuse the record rather than ship a
	// quietly different one — but the rule is what keeps it from getting
	// there at all.
	src := []byte("feedback: v1\nid: fb-20260101-aaaaaa\nkind: bug#42 # a real comment\nstatus: new\n")
	got, err := normalizeRecord(src)
	if err != nil {
		t.Fatalf("normalizeRecord: %v", err)
	}
	var before, after any
	if err := yaml.Unmarshal(src, &before); err != nil {
		t.Fatalf("unmarshal input: %v", err)
	}
	if err := yaml.Unmarshal(got, &after); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("a `#` with no leading space is data, not a comment: %#v vs %#v", before, after)
	}
	if !strings.Contains(string(got), "bug#42") {
		t.Fatalf("expected `bug#42` to survive, got:\n%s", got)
	}
}

// TestNormalizeRecord_NeverNewlyRefusing is AC #7 at the normalization layer:
// every fixture the corpus already calls valid normalizes without error and
// stays valid.
func TestNormalizeRecord_NeverNewlyRefusing(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join(schemaFixturesRoot, "valid", "*.yaml"))
	if err != nil {
		t.Fatalf("glob valid fixtures: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least one valid feedback fixture")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			raw := readFixture(t, f)
			got, err := normalizeRecord(raw)
			if err != nil {
				t.Fatalf("normalizeRecord refused a record that submits today: %v", err)
			}
			if report := Validate(got, Options{}); !report.Valid {
				t.Fatalf("the normalized record no longer validates: %+v", report.Violations)
			}
		})
	}
}
