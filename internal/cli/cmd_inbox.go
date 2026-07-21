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
func (c *InboxCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	actionable := fs.Bool("actionable", false, "apply the normative --actionable union (OP-207)")
	jsonOut := fs.Bool("json", false, "JSON array output (guaranteed shape)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a inbox [--actionable] [--json]")
		return 2
	}

	items, err := c.store.Inbox(ctx, *actionable)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "inbox: %v\n", err)
		return 1
	}
	return inboxRender(stdio, items, *jsonOut)
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
