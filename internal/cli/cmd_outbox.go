// OP-208 `a2a outbox` (spec 07 T1). This file's only package-level
// symbols are OutboxCommand + NewOutboxCommand plus its own uniquely-
// named, file-private helpers (outbox* prefix) — no shared helper, no
// package var, per this phase's plan Placement decision.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/release"
)

// OutboxCommand implements `a2a outbox [--attention] [--json]` (OP-208):
// own open items and their states; --attention applies the normative
// 4-condition union.
type OutboxCommand struct {
	store *cache.Store
}

// NewOutboxCommand constructs the outbox command. store must not be nil
// (rails anti-pattern #10).
func NewOutboxCommand(store *cache.Store) *OutboxCommand {
	return &OutboxCommand{store: store}
}

// Name implements cli.Command.
func (c *OutboxCommand) Name() string { return "outbox" }

// Synopsis implements cli.Command.
func (c *OutboxCommand) Synopsis() string {
	return "list own open items across every connected space; --attention applies the normative OP-208 union"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = a connected
// space's mirror could not be read; 0 = success.
func (c *OutboxCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("outbox", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	attention := fs.Bool("attention", false, "apply the normative --attention union (OP-208)")
	jsonOut := fs.Bool("json", false, "JSON array output (guaranteed shape)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a outbox [--attention] [--json]")
		return 2
	}

	items, err := c.store.Outbox(ctx, *attention)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "outbox: %v\n", err)
		return 1
	}
	code := outboxRender(stdio, items, *jsonOut)
	outboxWriteUpdateAdvisory(stdio, c.store.UpdateNotice(), *jsonOut)
	return code
}

// outboxWriteUpdateAdvisory emits the spec 19 T4 update-notice advisory
// OUT-OF-BAND, to stderr ONLY (wave 12c amendment: the stdout item array's
// bytes must stay byte-identical for existing consumers). See
// inboxWriteUpdateAdvisory's doc comment (cmd_inbox.go) — same rule, kept a
// file-private, uniquely-named copy per this package's own Placement
// convention (no shared helper across verb files).
func outboxWriteUpdateAdvisory(stdio IO, n cache.UpdateNotice, jsonOut bool) {
	if n.Grade == release.GradeNone {
		return
	}
	if jsonOut {
		_ = json.NewEncoder(stdio.Stderr).Encode(n)
		return
	}
	_, _ = fmt.Fprintf(stdio.Stderr, "note: %s\n", n.Sentence)
}

func outboxRender(stdio IO, items []cache.Item, jsonOut bool) int {
	if items == nil {
		items = []cache.Item{}
	}
	if jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "outbox: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}
	for _, it := range items {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\n", it.Space, it.ID, it.State, it.Title)
	}
	return 0
}

var _ Command = (*OutboxCommand)(nil)
