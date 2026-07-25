package livee2e

import (
	"fmt"
	"sort"
	"strings"
)

// selectedFamilies resolves EnvFamilies' raw value against the families the
// runner actually declares, returning the set to run.
//
// It lives in an UNTAGGED file, beside branchmatch.go's own pure matching
// rule and for the same reason: the RULE is testable by `go test
// ./internal/livee2e/` — which is what `make check` runs — while the
// network-touching runner that calls it stays behind the livee2e build tag.
// A filter whose only test is a live run is a filter nobody tests.
//
// An empty or whitespace-only value selects EVERYTHING, so an unset variable
// is the full matrix and no caller has to know the filter exists.
//
// An unknown name is an ERROR, never an empty selection. Silently matching
// nothing would run zero families, and since every row then keeps its
// declared not-run verdict, the report would be indistinguishable from a
// catastrophic matrix failure — sending the operator to debug the product
// instead of their own typo. The refusal names both the unknown value and
// the full set of legal ones, because "invalid family" without the list is
// a second round-trip for something the program already knows.
func selectedFamilies(raw string, declared []string) (map[string]bool, error) {
	known := make(map[string]bool, len(declared))
	for _, d := range declared {
		known[d] = true
	}

	if strings.TrimSpace(raw) == "" {
		return known, nil
	}

	out := map[string]bool{}
	var unknown []string
	for _, part := range strings.Split(raw, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue // a trailing or doubled comma is a typo, not a request
		}
		if !known[name] {
			unknown = append(unknown, name)
			continue
		}
		out[name] = true
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown scenario famil%s %s; declared families are: %s",
			plural(len(unknown), "y", "ies"), strings.Join(unknown, ", "), strings.Join(sortedCopy(declared), ", "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("names no family (value was %q); declared families are: %s",
			raw, strings.Join(sortedCopy(declared), ", "))
	}
	return out, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// sortedKeys renders a selection deterministically for the run's own log
// line — two runs of the same narrowed matrix must read identically.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
