// OP-210 `a2a thread` (spec 07 T1). This file's only package-level
// symbols are ThreadCommand + NewThreadCommand plus its own uniquely-
// named, file-private helpers (thread* prefix) — no shared helper, no
// package var, per this phase's plan Placement decision.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// ThreadCommand implements `a2a thread <thread-id>` (OP-210): every
// artifact across every connected space carrying that thread id,
// ordered chronologically.
type ThreadCommand struct {
	store *cache.Store
}

// NewThreadCommand constructs the thread command. store must not be nil
// (rails anti-pattern #10).
func NewThreadCommand(store *cache.Store) *ThreadCommand {
	return &ThreadCommand{store: store}
}

// Name implements cli.Command.
func (c *ThreadCommand) Name() string { return "thread" }

// Synopsis implements cli.Command.
func (c *ThreadCommand) Synopsis() string {
	return "show every artifact on a thread, ordered conversation view (OP-210)"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = a connected
// space's mirror could not be read; 0 = success (including the
// zero-match case).
func (c *ThreadCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("thread", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON array output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a thread <thread-id>")
		return 2
	}

	items, err := c.store.Thread(ctx, fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "thread: %v\n", err)
		return 1
	}
	if items == nil {
		items = []cache.Item{}
	}
	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "thread: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}
	for _, it := range items {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\n", it.Space, it.ID, it.State, it.Title)
	}
	return 0
}

var _ Command = (*ThreadCommand)(nil)
