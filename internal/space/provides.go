// provides.go answers one question over a mirror's own tree: which contracts
// does this system PROVIDE? It is filesystem-reading, which is why it is not
// in layout.go — that file is pure path construction and stays that way.
package space

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// ProvidedContractIDs lists, in id order, every contract `system` PROVIDES
// according to mirrorDir's own tree: each directory under
// `<system>/provides/` that actually carries a contract descriptor, resolved
// to its XC id.
//
// It lives here rather than in either surface because BOTH surfaces enumerate
// this set and neither may import the other (ADR-001: `internal/mcp` never
// imports `internal/cli`). `a2a contract verify-published` and
// `a2a_contract action=verify-published` shipped with two copies of this walk,
// and the second one's comment said "mirrored here, never imported" — a
// documented duplication, which ADR-019 refuses: what both surfaces need moves
// DOWN into a package neither is forbidden to import. This is that package,
// and the move costs no new import edge: both surfaces already call NewLayout
// and ContractForPath directly.
//
// The convention it owns is "which directory counts": a subdirectory whose
// descriptor file is absent is NOT a provided contract — a `provides/<slug>/`
// left behind by a partial write, or holding only companion files, must not
// appear in a report that claims to name everything this system publishes.
//
// An absent `provides/` tree returns no ids and NO error. Having published
// nothing is a legitimate state, and the caller prints its denominator either
// way; a reader who cannot tell "provides nothing" from "the walk failed" is
// the failure this whole verb exists to remove.
func ProvidedContractIDs(system, mirrorDir string) ([]string, error) {
	layout, err := NewLayout(system)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(mirrorDir, system, "provides"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		descriptor := layout.ProvidesContract(entry.Name())
		if _, statErr := os.Stat(filepath.Join(mirrorDir, descriptor)); statErr != nil {
			continue
		}
		id, _, ok := ContractForPath(descriptor)
		if !ok {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
