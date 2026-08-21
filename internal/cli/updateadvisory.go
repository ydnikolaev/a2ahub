package cli

import (
	"encoding/json"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/release"
)

// WriteUpdateAdvisory emits the update notice OUT-OF-BAND, to stderr only.
//
// ONE shared copy, for the reason skipAdvisory's own doc comment already
// states about its four verbs: the wording must stay identical everywhere it
// can surface, and independent copies drift the first time one is edited
// without the others. This one had THREE copies (inbox, outbox, MCP a2a_read)
// and the drift was worse than wording — the copies decided WHICH VERBS SPEAK.
//
// On 2026-08-21 an agent was asked to send a notification, reached a verb that
// held no copy, and was told nothing about the release it was missing. The
// advisory is a cross-cutting fact; it now renders from the dispatcher for
// every CLI verb and from Registry.Decorate for every MCP tool, so no verb can
// be added without it.
//
// stdout is never touched: an existing consumer's bytes must not move because
// a release happened. GradeNone (notice disabled, or nothing to advise) emits
// nothing at all — a note that fires on a current install is a note people
// learn to ignore.
func WriteUpdateAdvisory(stdio IO, n cache.UpdateNotice, jsonOut bool) {
	if n.Grade == release.GradeNone {
		return
	}
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(n)
		return
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", n.Sentence)
}
