package lane

import "fmt"

// Coverage checks that every path in universe is claimed by at least one
// KindScoped declaration, or is explicitly listed in ungated (D-3).
// KindAlways and KindNever declarations claim nothing — a universal gate
// running on every path does not mean every path has been JUDGED by
// something that reads it, which is the property Coverage checks.
func Coverage(decls []Declaration, universe, ungated []string) []Refusal {
	ungatedSet := map[string]bool{}
	for _, p := range ungated {
		ungatedSet[p] = true
	}

	var scoped []Declaration
	for _, d := range decls {
		if d.Kind == KindScoped {
			scoped = append(scoped, d)
		}
	}

	var refusals []Refusal
	for _, path := range universe {
		if ungatedSet[path] {
			continue
		}
		claimed := false
		for _, d := range scoped {
			if MatchInputs(d.Inputs, path) {
				claimed = true
				break
			}
		}
		if !claimed {
			refusals = append(refusals, Refusal{
				Subject: path,
				Problem: fmt.Sprintf("no gate claims %s", path),
				Fix:     "add it to a gate's inputs or declare it explicitly ungated",
			})
		}
	}
	return refusals
}
