package surface

import (
	"os"
	"path/filepath"
)

// Detect returns every Registry() row whose marker directory
// (<root>/<Surface.MarkerDir()>, e.g. ".claude") exists under root, in
// Registry()'s deterministic order. Detection is best-effort: a missing
// root, a missing marker dir, or an unreadable entry all yield exclusion —
// Detect never returns an error, so a caller (e.g. `a2a init`) can always
// treat its result as "the surfaces this repo already shows".
func Detect(root string) []Surface {
	var found []Surface
	for _, s := range Registry() {
		info, err := os.Stat(filepath.Join(root, s.MarkerDir()))
		if err != nil || !info.IsDir() {
			continue
		}
		found = append(found, s)
	}
	return found
}
