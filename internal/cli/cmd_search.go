// OP-221 `a2a search` / `a2a contracts` (spec 07 T1). This file's only
// package-level symbols are SearchCommand/ContractsCommand + their
// NewXCommand constructors plus file-private, uniquely-named helpers
// (search*/contracts* prefix) — no shared helper, no package var, per
// this phase's plan Placement decision.
//
// The allowlist (plan 07) grants exactly six cmd_ files and does not
// include a dedicated cmd_contracts.go; both `search` and `contracts`
// are OP-221's own two clauses ("discovery over the local cache"), so
// this file holds both commands — the same one-file, two-command
// pattern cmd_submit.go already uses for ValidateCommand+SubmitCommand.
// `a2a contract diff` (OP-221's third clause) is P8's (contract
// lifecycle phase), not implemented here.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
)

// --- search (OP-221 first clause) ---------------------------------------

// SearchCommand implements `a2a search <query> [--type --space --state]`
// (OP-221): ranked local-cache matches, hub-less by design. Zero hits is
// an empty result, never an error.
type SearchCommand struct {
	store *cache.Store
}

// NewSearchCommand constructs the search command. store must not be nil
// (rails anti-pattern #10).
func NewSearchCommand(store *cache.Store) *SearchCommand {
	return &SearchCommand{store: store}
}

// Name implements cli.Command.
func (c *SearchCommand) Name() string { return "search" }

// Synopsis implements cli.Command.
func (c *SearchCommand) Synopsis() string {
	return "search the local cache: search <query> [--type --space --state]"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = a connected
// space's mirror could not be read; 0 = success (including zero hits).
func (c *SearchCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	typeFlag := fs.String("type", "", "filter by envelope type")
	spaceFlag := fs.String("space", "", "filter by connected space id")
	stateFlag := fs.String("state", "", "filter by folded state")
	jsonOut := fs.Bool("json", false, "JSON array output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a search <query> [--type --space --state] [--json]")
		return 2
	}

	items, err := c.store.Search(ctx, fs.Arg(0), cache.SearchFilters{Type: *typeFlag, Space: *spaceFlag, State: *stateFlag})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "search: %v\n", err)
		return 1
	}
	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "search: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}
	for _, it := range items {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\n", it.Space, it.ID, it.State, it.Title)
	}
	return 0
}

var _ Command = (*SearchCommand)(nil)

// --- contracts (OP-221 second clause) -----------------------------------

// ContractsCommand implements `a2a contracts [--provider <sys>]`
// (OP-221): known contracts from the local cache (provider, version,
// state). `a2a contract diff` is P8's, out of this phase's footprint.
type ContractsCommand struct {
	store *cache.Store
}

// NewContractsCommand constructs the contracts command. store must not
// be nil (rails anti-pattern #10).
func NewContractsCommand(store *cache.Store) *ContractsCommand {
	return &ContractsCommand{store: store}
}

// Name implements cli.Command.
func (c *ContractsCommand) Name() string { return "contracts" }

// Synopsis implements cli.Command.
func (c *ContractsCommand) Synopsis() string {
	return "list known contracts from the local cache: contracts [--provider <sys>]"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = a connected
// space's mirror could not be read; 0 = success.
func (c *ContractsCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("contracts", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	provider := fs.String("provider", "", "filter by provider system")
	jsonOut := fs.Bool("json", false, "JSON array output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a contracts [--provider <sys>] [--json]")
		return 2
	}

	contracts, err := c.store.Contracts(ctx, *provider)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "contracts: %v\n", err)
		return 1
	}
	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(contracts); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "contracts: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}
	for _, ci := range contracts {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\t%s\n", ci.Space, ci.ID, ci.Provider, ci.Version, ci.State)
	}
	return 0
}

var _ Command = (*ContractsCommand)(nil)
