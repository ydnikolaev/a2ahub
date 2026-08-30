// OP-207 `a2a inbox` (spec 07 T1). This file's only package-level
// symbols are InboxCommand + NewInboxCommand plus its own uniquely-
// named, file-private helpers (inbox* prefix) — no shared helper, no
// package var, per this phase's plan Placement decision (avoids
// collision with P6/P8's parallel verb files in this same package).
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// InboxCommand implements `a2a inbox [--actionable] [--json]` (OP-207):
// the computed inbox across every connected space, JSON output
// guaranteed. Business logic (the 5-condition `--actionable` union,
// federation, cursor-advance) lives entirely in internal/cache — this
// file is a thin flags-in/JSON-out wrapper (ADR-001 "thin frontend").
type InboxCommand struct {
	store *cache.Store
}

// NewInboxCommand constructs the inbox command. store must not be nil
// (rails anti-pattern #10).
func NewInboxCommand(store *cache.Store) *InboxCommand {
	return &InboxCommand{store: store}
}

// Name implements cli.Command.
func (c *InboxCommand) Name() string { return "inbox" }

// Synopsis implements cli.Command.
func (c *InboxCommand) Synopsis() string {
	return "list items across every connected space; --actionable applies the normative OP-207 union"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = a connected
// space's mirror could not be read; 0 = success (including the
// zero-items case — an empty inbox is not a failure).
//
// With --exit-code, success instead carries §7.5's severity: 0 nothing,
// 10 items pending, 11 p1/blocking/gate. 1 and 2 keep their meanings, so a
// caller can always tell "there is work" from "the command failed".
//
// --overdue answers the question `--actionable` structurally cannot: what
// is late and MINE. See internal/cache/overdue.go for why it is a separate
// query rather than a sixth condition on a normative union.
func (c *InboxCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	actionable := fs.Bool("actionable", false, "apply the normative --actionable union (OP-207)")
	jsonOut := fs.Bool("json", false, "JSON array output (guaranteed shape)")
	overdue := fs.Bool("overdue", false, "only open items addressed to you whose needed_by has passed — the work that is late and yours")
	exitCode := fs.Bool("exit-code", false, "exit with §7.5 severity instead of 0: 0 nothing, 10 items pending, 11 p1/blocking/gate (for schedulers and hooks)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a inbox [--actionable|--overdue] [--json] [--exit-code]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-watch"))
		return 2
	}
	// Two different questions, and combining them would have to mean either
	// union or intersection. Rather than pick one silently, refuse: an
	// overdue item that is ALSO actionable is common, so a caller who wrote
	// both almost certainly wants one of them and would not notice getting
	// the other.
	if *actionable && *overdue {
		_, _ = fmt.Fprintln(stdio.Stderr,
			"inbox: --actionable and --overdue are different questions — pass one.\n"+
				"  --actionable  what OP-207 says needs your move now\n"+
				"  --overdue     what you owe whose needed_by has passed (acked or not)")
		return 2
	}

	var items []cache.Item
	var err error
	if *overdue {
		items, err = c.store.Overdue(ctx)
	} else {
		items, err = c.store.Inbox(ctx, *actionable)
	}
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "inbox: %v\n", err)
		return 1
	}
	code := inboxRender(stdio, items, *jsonOut)
	// Defect fix (filed 2026-07-26): a malformed mirror file used to drop
	// out of the index without a word — see skipadvisory.go's own doc
	// comment. inbox is cross-space, so this reports the union across every
	// connected mirror.
	if skipped, skErr := c.store.AllSkippedFiles(ctx); skErr == nil {
		skipAdvisory(stdio, cache.FlattenSkippedFiles(skipped), *jsonOut)
	} else {
		// computed-not-listed-2026-08 P6 AC-8/§8 row 8, through the ONE
		// shared copy in skipadvisory.go — see skipAdvisoryUnavailable.
		skipAdvisoryUnavailable(stdio, "inbox", skErr, *jsonOut)
	}
	// Severity replaces the success code only — never an error. A render
	// failure (code != 0) must stay distinguishable from "you have items",
	// or a scheduler treats a broken run as work waiting and the other way
	// round.
	if *exitCode && code == 0 {
		return int(cache.SeverityOf(items))
	}
	return code
}

// inboxRender writes items to stdio.Stdout as a guaranteed JSON array
// (--json) or a one-line-per-item text projection of the SAME data
// (T1: "text rendering is a projection of the same JSON").
func inboxRender(stdio IO, items []cache.Item, jsonOut bool) int {
	if items == nil {
		items = []cache.Item{}
	}
	if jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "inbox: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}
	for _, it := range items {
		newMark := ""
		if it.New {
			newMark = " [new]"
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s%s\n", it.Space, it.ID, it.State, it.Title, newMark)
	}
	return 0
}

var _ Command = (*InboxCommand)(nil)
