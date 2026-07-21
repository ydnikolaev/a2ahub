// OP-215 `a2a statusline` (spec 07 §7.5). This file's only package-level
// symbols are StatuslineCommand + NewStatuslineCommand — no shared
// helper, no package var, per this phase's plan Placement decision.
//
// NO hub client symbol is imported or referenced anywhere in this file
// (spec 07 §8 AC row 9): the background refresh internal/cache.Store
// spawns when stale is git-fetch only (v1-min scope cut, D-030) — this
// file never constructs, imports, or wires anything hub-shaped.
package cli

import (
	"context"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// StatuslineCommand implements `a2a statusline` (§7.5): at most one
// line, cache-read only, <100ms; exit 0 quiet / 10 items pending / 11
// p1-or-gate-pending, so harnesses can style without parsing. All
// severity/zero-noise/TTL-refresh logic lives in internal/cache.Store —
// this file writes exactly what Store.Statusline returns and nothing
// else (never real os.Stdout/Stderr, only the injected IO).
type StatuslineCommand struct {
	store *cache.Store
}

// NewStatuslineCommand constructs the statusline command. store must
// not be nil (rails anti-pattern #10).
func NewStatuslineCommand(store *cache.Store) *StatuslineCommand {
	return &StatuslineCommand{store: store}
}

// Name implements cli.Command.
func (c *StatuslineCommand) Name() string { return "statusline" }

// Synopsis implements cli.Command.
func (c *StatuslineCommand) Synopsis() string {
	return "print at most one status line for embedding in your own statusline (§7.5)"
}

// Run implements cli.Command. Takes no flags/args (§7.5: "reads config
// only"). Exit codes are Store.Statusline's own severity contract
// (0/10/11); a computation error is exit 1 with nothing written to
// stdout (never a partial/malformed line).
func (c *StatuslineCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a statusline")
		return 2
	}

	result, err := c.store.Statusline(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "statusline: %v\n", err)
		return 1
	}
	if result.Line != "" {
		_, _ = fmt.Fprintln(stdio.Stdout, result.Line)
	}
	return result.Exit
}

var _ Command = (*StatuslineCommand)(nil)
