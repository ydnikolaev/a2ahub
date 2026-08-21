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
//
// With --exit-code, success instead carries §7.5's severity (see
// cache.SeverityOf and InboxCommand.Run's own note). The session-start
// floor names inbox AND outbox — verification of answers you requested is
// yours to close — so a scheduler that can only branch on one of them
// covers half the loop.
func (c *OutboxCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("outbox", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	attention := fs.Bool("attention", false, "apply the normative --attention union (OP-208)")
	jsonOut := fs.Bool("json", false, "JSON array output (guaranteed shape)")
	exitCode := fs.Bool("exit-code", false, "exit with §7.5 severity instead of 0: 0 nothing, 10 items pending, 11 p1/blocking/gate (for schedulers and hooks)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a outbox [--attention] [--json] [--exit-code]")
		return 2
	}

	items, err := c.store.Outbox(ctx, *attention)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "outbox: %v\n", err)
		return 1
	}
	code := outboxRender(stdio, items, *jsonOut)
	// Defect fix (filed 2026-07-26): a malformed mirror file used to drop
	// out of the index without a word — see skipadvisory.go's own doc
	// comment. outbox is cross-space, so this reports the union across
	// every connected mirror.
	if skipped, skErr := c.store.AllSkippedFiles(ctx); skErr == nil {
		skipAdvisory(stdio, flattenSkipped(skipped), *jsonOut)
	}
	// Severity replaces the success code only — see InboxCommand.Run.
	if *exitCode && code == 0 {
		return int(cache.SeverityOf(items))
	}
	return code
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
