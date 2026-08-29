// P31 `a2a whatsnew` (spec 31 T1): render the committed, embedded
// release-notes corpus (internal/notes over releasenotes.FS) as an
// agent-consumable digest — informational only, a2a never runs a
// `scope: space` directive itself (schemas/release-notes/v1's own
// description). This file's only package-level symbols are
// WhatsnewCommand + NewWhatsnewCommand plus its own uniquely-named,
// file-private helpers (whatsnew* prefix) — no shared helper, no package
// var, per this package's established Placement convention.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/version"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// whatsnewImpactRank orders a Change's `impact` (schema enum: high, normal,
// low) high-to-normal-to-low for the text digest; an unrecognized impact
// (never emitted by a schema-valid corpus, but not this command's job to
// re-validate) sorts last rather than erroring.
// whatsnewImpactOrder delegates to notes.ImpactOrder, which owns the rule.
// It moved down on 2026-08-29 (ADR-019) because internal/mcp needs the same
// tie-break and may not import this package; this wrapper stays so the
// call sites here read unchanged.
func whatsnewImpactOrder(impact string) int { return notes.ImpactOrder(impact) }

// whatsnewDetailWidth is the wrapped-detail line's target column count
// (indent included).
const whatsnewDetailWidth = 76

// WhatsnewCommand implements `a2a whatsnew [--since <v>] [--json]` (P31):
// a thin flags-in/JSON-or-text-out wrapper over internal/notes — zero
// business rules live here (ADR-001 "thin frontend").
type WhatsnewCommand struct {
	binaryVersion     string
	load              func() ([]notes.ReleaseNotes, error)
	loadCurrentIssues func() ([]notes.Change, error)
}

// NewWhatsnewCommand constructs the whatsnew command. binaryVersion is this
// build's own version stamp (injected, same convention as
// NewUpdateCommand/NewDoctorCommand — tests control it directly). load
// defaults to the real embedded corpus (notes.Load(releasenotes.FS));
// tests override it to drive a fixed corpus.
func NewWhatsnewCommand(binaryVersion string) *WhatsnewCommand {
	return &WhatsnewCommand{
		binaryVersion: binaryVersion,
		load:          func() ([]notes.ReleaseNotes, error) { return notes.Load(releasenotes.FS) },
		loadCurrentIssues: func() ([]notes.Change, error) {
			return notes.LoadCurrentKnownIssues(releasenotes.FS)
		},
	}
}

// Name implements cli.Command.
func (c *WhatsnewCommand) Name() string { return "whatsnew" }

// Synopsis implements cli.Command.
func (c *WhatsnewCommand) Synopsis() string {
	return "show what changed in this release and what to do about it (P31)"
}

// Run implements cli.Command. Exit codes: 2 = usage error; 1 = corpus load
// or JSON-encode failure; 0 = success (including the zero-matches case —
// no release notes for this selection is not a failure).
func (c *WhatsnewCommand) Run(_ context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("whatsnew", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	sinceFlag := fs.String("since", "", "show notes strictly newer than this version")
	jsonFlag := fs.Bool("json", false, "machine-readable output ([]notes.ReleaseNotes)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a whatsnew [--since <v>] [--json]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
		return 2
	}

	all, err := c.load()
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "whatsnew: %v\n", err)
		return 1
	}
	currentIssues, err := c.loadCurrentIssues()
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "whatsnew: %v\n", err)
		return 1
	}

	// upto bounds the query at this binary's own version — a real release
	// never embeds notes for a version newer than itself. A binary stamp
	// that does not parse as a dotted version (a dev build, or empty) fails
	// OPEN on the bound only (never on the query itself): --since must still
	// work on a dev build, so upto is left unbounded rather than excluding
	// everything.
	upto := c.binaryVersion
	if _, verr := version.OlderThan(c.binaryVersion, c.binaryVersion); verr != nil {
		upto = ""
	}

	var slice []notes.ReleaseNotes
	if *sinceFlag != "" {
		slice = notes.Since(all, *sinceFlag, upto)
	} else if rn, ok := notes.Exactly(all, c.binaryVersion); ok {
		slice = []notes.ReleaseNotes{rn}
	} else {
		slice = []notes.ReleaseNotes{}
	}
	slice = notes.AttachCurrentKnownIssues(slice, all, currentIssues)

	if *jsonFlag {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(slice); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "whatsnew: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}

	whatsnewRenderText(stdio, slice, *sinceFlag, c.binaryVersion)
	return 0
}

// whatsnewRenderText renders the digest: newest release first (slice is
// version-ascending, notes.Load's own postcondition), each release's
// changes ordered high->normal->low impact, and — for any change whose
// Action.Scope is not "none" — the directive's why/detect/run.
func whatsnewRenderText(stdio IO, slice []notes.ReleaseNotes, since, binaryVersion string) {
	if len(slice) == 0 {
		if since != "" {
			_, _ = fmt.Fprintf(stdio.Stdout, "no release notes since %s\n", since)
		} else {
			_, _ = fmt.Fprintf(stdio.Stdout, "no release notes for version %s\n", binaryVersion)
		}
		return
	}

	for i := len(slice) - 1; i >= 0; i-- {
		rn := slice[i]
		_, _ = fmt.Fprintf(stdio.Stdout, "v%s (%s) — %s\n", rn.Version, rn.Released, rn.Headline)

		changes := append([]notes.Change(nil), rn.Changes...)
		// A known-issue is not a change and must not be read as one: it says
		// "this is broken right now, use the other thing". Sorted to the TOP
		// regardless of impact, because a reader who stops after the first line
		// must have seen it — the whole reason the kind exists is that an agent
		// acting on a surface it should be avoiding gets a wrong answer with no
		// error. Within each group the existing impact order still applies.
		sort.SliceStable(changes, func(a, b int) bool {
			ka, kb := changes[a].Kind == notes.KindKnownIssue, changes[b].Kind == notes.KindKnownIssue
			if ka != kb {
				return ka
			}
			return whatsnewImpactOrder(changes[a].Impact) < whatsnewImpactOrder(changes[b].Impact)
		})

		for _, ch := range changes {
			// The label carries the kind for a known-issue and the impact for a
			// change. Rendering both identically — which is what this did before
			// the kind existed — makes a standing limitation indistinguishable
			// from a feature, and this text is the update notice's own body.
			label := ch.Impact
			if ch.Kind == notes.KindKnownIssue {
				label = "KNOWN ISSUE"
			}
			_, _ = fmt.Fprintf(stdio.Stdout, "  [%s] %s\n", label, ch.Subject)
			for _, line := range whatsnewWrapDetail(strings.TrimSpace(ch.Detail), "    ", whatsnewDetailWidth) {
				_, _ = fmt.Fprintln(stdio.Stdout, line)
			}
			if ch.Action.Scope != "none" {
				_, _ = fmt.Fprintf(stdio.Stdout, "    → why: %s\n", ch.Action.Why)
				if len(ch.Action.Detect) > 0 {
					_, _ = fmt.Fprintf(stdio.Stdout, "      detect: %s\n", strings.Join(ch.Action.Detect, ", "))
				}
				if len(ch.Action.Run) > 0 {
					_, _ = fmt.Fprintf(stdio.Stdout, "      run: %s\n", strings.Join(ch.Action.Run, ", "))
				}
			} else if ch.Kind == notes.KindKnownIssue {
				// scope:none normally means "nothing to do", so the why is
				// suppressed. For a known issue the why IS the payload — it
				// names the working alternative.
				_, _ = fmt.Fprintf(stdio.Stdout, "    → %s\n", ch.Action.Why)
			}
		}
	}
}

// whatsnewWrapDetail greedily word-wraps detail to width columns (indent
// included), each returned line already carrying indent. An empty detail
// returns nil (no line printed).
func whatsnewWrapDetail(detail, indent string, width int) []string {
	words := strings.Fields(detail)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, 1)
	cur := indent + words[0]
	for _, w := range words[1:] {
		candidate := cur + " " + w
		if len(candidate) > width {
			lines = append(lines, cur)
			cur = indent + w
			continue
		}
		cur = candidate
	}
	lines = append(lines, cur)
	return lines
}

var _ Command = (*WhatsnewCommand)(nil)
