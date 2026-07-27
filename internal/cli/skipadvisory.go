// Defect closed here (filed 2026-07-26): internal/cache's read model was
// already best-effort BY DESIGN — one malformed artifact/event file must
// never blind the whole space to every other document in it — but the file
// it dropped was silently indistinguishable from one that simply did not
// exist. `a2a search`, `a2a inbox`, `a2a outbox` and `a2a thread` all showed
// one FEWER row than the space actually held, with no word anywhere. This
// file is the stage-2 fix: it surfaces internal/cache's own SkippedFile
// report (skipped.go) to the agents calling these read verbs, OUT-OF-BAND —
// never on stdout.
package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// skipAdvisory writes the OUT-OF-BAND advisory for a read verb's own
// skipped-file list, to stderr ONLY — the same channel/shape rule
// inboxWriteUpdateAdvisory (cmd_inbox.go) already established for the T4
// update notice: "the stdout item array's bytes must stay byte-identical for
// existing consumers", so this never touches stdout, one JSON line in
// --json mode, one "note: " prose line otherwise. Nothing is written when
// skipped is empty (a gate that fires on a clean space is a gate people
// silence).
//
// ONE shared copy across search/inbox/outbox/thread — deliberately UNLIKE
// this package's usual one-copy-per-verb-file Placement convention (see
// cmd_inbox.go's own doc comment on inboxWriteUpdateAdvisory): this
// advisory's wording must stay identical across every verb that can surface
// it, and four independent copies would drift the first time one of them
// was edited without the others.
func skipAdvisory(stdio IO, skipped []cache.SkippedFile, jsonOut bool) {
	if len(skipped) == 0 {
		return
	}
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(struct {
			Skipped []cache.SkippedFile `json:"skipped"`
		}{Skipped: skipped})
		return
	}
	parts := make([]string, 0, len(skipped))
	for _, sk := range skipped {
		parts = append(parts, fmt.Sprintf("%s (%s)", sk.Path, sk.Reason))
	}
	_, _ = fmt.Fprintf(stdio.Stderr,
		"note: %d file(s) are in this space but could not be decoded, so they are missing from this output: %s\n",
		len(skipped), strings.Join(parts, ", "))
}

// flattenSkipped merges a Store.AllSkippedFiles map into one deterministic
// slice (space id order, then each space's own already-sorted path order —
// skipped.go's own doc comment) — inbox/outbox/search are not space-scoped
// the way `a2a thread` is (cache.AllSkippedFiles' own doc comment: "a2a
// inbox is not space-scoped ... it needs the union across every connected
// mirror"), so their advisory needs one stable order across every connected
// space rather than cache.SkippedFiles' single-space slice.
func flattenSkipped(bySpace map[string][]cache.SkippedFile) []cache.SkippedFile {
	spaceIDs := make([]string, 0, len(bySpace))
	for id := range bySpace {
		spaceIDs = append(spaceIDs, id)
	}
	sort.Strings(spaceIDs)
	var out []cache.SkippedFile
	for _, id := range spaceIDs {
		out = append(out, bySpace[id]...)
	}
	return out
}
