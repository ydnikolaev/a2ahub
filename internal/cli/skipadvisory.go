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
	_, _ = fmt.Fprintf(stdio.Stderr,
		"note: %d file(s) are in this space but could not be decoded, so they are missing from this output: %s\n",
		len(skipped), formatSkippedList(skipped))
}

// skipAdvisoryUnavailable is skipAdvisory's OTHER half: what a read verb
// says when it could not even COMPUTE the advisory. The skipped-file report
// is best-effort by design, so a failure to read it used to be dropped
// silently — and the agent then lost two facts at once, that this output
// might be missing rows and that nothing could find out which
// (computed-not-listed-2026-08 P6 AC-8/§8 row 8).
//
// ONE shared copy across search/inbox/outbox/thread, for the reason
// skipAdvisory's own doc comment above already gives at length: the wording
// must stay identical across every verb that can surface it. P6 first
// shipped four independent copies, one per verb file, each carrying a
// pointer to the others' comment — which is the drift that argument
// predicts, written down as a plan.
//
// It goes through NewRefusal (refusal_state.go) rather than writing the
// error straight to stderr, and that is not gate-appeasement: the four
// copies said "could not determine which files, if any, were skipped: %v"
// and stopped there. A symptom with no action is spec 04's own defect — the
// reader is told something is wrong and not what to do about it — and
// check-refusal-ratchet.sh reddening on the four new raw sinks is that rule
// working, not obstructing.
//
// Stderr only, never stdout: an existing consumer's stdout bytes must stay
// byte-identical, the same constraint skipAdvisory carries. In --json mode
// it emits one JSON object rather than prose, so a consumer parsing this
// channel meets the shape it already expects from skipAdvisory.
func skipAdvisoryUnavailable(stdio IO, verb string, err error, jsonOut bool) {
	attempted := cache.SkippedFilesUnavailableAttempted(verb)
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(struct {
			SkippedUnavailable string `json:"skipped_unavailable"`
			Reason             string `json:"reason"`
			NextStep           string `json:"next_step"`
		}{SkippedUnavailable: verb, Reason: err.Error(), NextStep: cache.SkippedFilesUnavailableNextStep})
		return
	}
	refusal, rerr := NewRefusal(attempted, err.Error(), cache.SkippedFilesUnavailableNextStep)
	if rerr != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, verb+": internal problem building the skip-advisory note (empty next step) — this is a bug in "+verb+", not a caller mistake")
		return
	}
	_, _ = fmt.Fprintln(stdio.Stderr, refusal)
}

// formatSkippedList renders skipped as "path (reason), path (reason), ..."
// — the item-list fragment shared between skipAdvisory's own "note: N
// file(s)..." prose above (a read verb's OWN output is missing these rows)
// and ViolationError.Error()'s refusal-time note (adapters.go: the
// resolver's INDEX could not decode these files, which is why a
// resolvable ref may still fail). Same list formatting, deliberately
// different framing sentences per call site — see ViolationError's own
// doc comment for why conflating the two would misdirect a reader.
func formatSkippedList(skipped []cache.SkippedFile) string {
	return cache.FormatSkippedList(skipped)
}
