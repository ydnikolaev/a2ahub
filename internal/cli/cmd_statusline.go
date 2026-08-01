// OP-215 `a2a statusline` (spec 07 §7.5). This file owns the command and the
// detached process adapter; it has no package-level mutable state.
//
// NO hub client symbol is imported or referenced anywhere in this file
// (spec 07 §8 AC row 9): cache only reports local freshness, and the CLI
// adapter reuses canonical `a2a sync` (v1-min scope cut, D-030). This file
// never constructs, imports, or wires anything hub-shaped.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// StatuslineCommand implements `a2a statusline` (§7.5): at most one
// line, cache-read only, <100ms; exit 0 quiet / 10 items pending / 11
// p1-or-gate-pending, so harnesses can style without parsing. All
// severity/zero-noise/TTL-refresh logic lives in internal/cache.Store —
// this file writes exactly what Store.Statusline returns and nothing
// else (never real os.Stdout/Stderr, only the injected IO).
type StatuslineCommand struct {
	store           *cache.Store
	refreshLauncher func() error
}

// NewStatuslineCommand constructs the statusline command. Production wiring
// injects StartDetachedSync; tests that do not exercise refresh pass nil.
func NewStatuslineCommand(store *cache.Store, refreshLauncher func() error) *StatuslineCommand {
	return &StatuslineCommand{store: store, refreshLauncher: refreshLauncher}
}

// Name implements cli.Command.
func (c *StatuslineCommand) Name() string { return "statusline" }

// Synopsis implements cli.Command.
func (c *StatuslineCommand) Synopsis() string {
	return "print status facts for prompt integration [--json] [--sample] [--no-prefix] (§7.5)"
}

// Run implements cli.Command. The default remains cache/config-only;
// --sample bypasses both for integration checks. Exit codes are
// Store.Statusline's own severity contract
// (0/10/11); a computation error is exit 1 with nothing written to
// stdout (never a partial/malformed line).
func (c *StatuslineCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("statusline", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "emit structured statusline facts")
	sample := fs.Bool("sample", false, "emit deterministic representative output without reading config/cache")
	noPrefix := fs.Bool("no-prefix", false, "omit the leading a2a: presentation prefix")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || (*jsonOut && *noPrefix) {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a statusline [--json] [--sample] [--no-prefix]")
		return 2
	}

	var result cache.StatuslineResult
	if *sample {
		result = cache.SampleStatusline()
	} else {
		var err error
		result, err = c.store.Statusline(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "statusline: %v\n", err)
			return 1
		}
	}
	// Deliberately NO skip-file advisory here (see skipadvisory.go, wired
	// into search/inbox/outbox/thread): this verb's entire output is at
	// most one line destined for a shell prompt, and stderr noise here is
	// worse than the omission — `a2a doctor`'s own new row is where a
	// skipped mirror file surfaces for an operator who never runs the other
	// read verbs.
	if *jsonOut {
		if err := json.NewEncoder(stdio.Stdout).Encode(result); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "statusline: %v\n", err)
			return 1
		}
		c.startRefreshIfNeeded(*sample)
		return result.Exit
	}
	line := result.Line
	if *noPrefix {
		line = cache.RenderStatusline(result, false)
	}
	if line != "" {
		_, _ = fmt.Fprintln(stdio.Stdout, line)
	}
	c.startRefreshIfNeeded(*sample)
	return result.Exit
}

func (c *StatuslineCommand) startRefreshIfNeeded(sample bool) {
	if sample || c.store == nil || c.refreshLauncher == nil || !c.store.ClaimStatuslineRefreshLease() {
		return
	}
	started := func() (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		return c.refreshLauncher() == nil
	}()
	if !started {
		c.store.ReleaseStatuslineRefreshLease()
	}
}

// StartDetachedSync launches the current a2a executable's canonical sync
// command without inheriting prompt-facing stdio or waiting for completion.
func StartDetachedSync() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open null device: %w", err)
	}
	defer func() {
		_ = null.Close() // reason: child owns its duplicated descriptors after Start; parent close is best-effort
	}()

	// The prompt-facing refresh stays on sync's canonical mirror/update path,
	// but skips optional avatar downloads. A first avatar fetch has its own
	// network budget and must never keep the statusline lease alive behind a
	// shell prompt; an explicit `a2a sync` still refreshes that cache.
	cmd := exec.Command(executable, "sync", "--statusline-refresh")
	configureDetachedProcess(cmd)
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start detached sync: %w", err)
	}
	// The one-shot parent exits immediately; releasing the handle avoids
	// retaining process resources while the child completes independently.
	_ = cmd.Process.Release() // reason: child already started; parent exits and cannot repair a release-handle failure
	return nil
}

var _ Command = (*StatuslineCommand)(nil)
