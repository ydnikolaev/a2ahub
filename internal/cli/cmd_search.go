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
	"strings"

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
	return "search the local cache: search <query> [--type --space --state --thread]"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = a connected
// space's mirror could not be read; 0 = success (including zero hits).
func (c *SearchCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	typeFlag := fs.String("type", "", "filter by envelope type")
	spaceFlag := fs.String("space", "", "filter by connected space id")
	stateFlag := fs.String("state", "", "filter by folded state")
	threadFlag := fs.String("thread", "", "filter by thread id")
	jsonOut := fs.Bool("json", false, "JSON array output")
	// Wave K fix (live run 6, "thirteen verbs refuse a flag written after
	// their positional argument"): parseArgsAnyOrder (cli.go), not a bare
	// fs.Parse(args) — `search foo --type question` used to refuse.
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a search <query> [--type --space --state --thread] [--json]")
		return 2
	}

	items, err := c.store.Search(ctx, positional[0], cache.SearchFilters{
		Type: *typeFlag, Space: *spaceFlag, State: *stateFlag, Thread: *threadFlag,
	})
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "search: %v\n", err)
		return 1
	}
	// Defect fix (filed 2026-07-26): a malformed mirror file used to drop
	// out of the index without a word — see skipadvisory.go's own doc
	// comment. search is cross-space (not scoped by --space, which is only
	// a filter on the fold, not the walk), so this reports the union across
	// every connected mirror, same as inbox/outbox below.
	if skipped, skErr := c.store.AllSkippedFiles(ctx); skErr == nil {
		skipAdvisory(stdio, flattenSkipped(skipped), *jsonOut)
	} else {
		// computed-not-listed-2026-08 P6 AC-8/§8 row 8 — see cmd_inbox.go's
		// own copy of this comment for the defect this closes.
		_, _ = fmt.Fprintf(stdio.Stderr, "search: could not determine which files, if any, were skipped: %v\n", skErr)
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
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
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
		// The five existing columns are UNCHANGED — this output is parsed by
		// scripts and by agents, and the version/state summary they read
		// still means exactly what it meant.
		//
		// The rolling window (P4) is appended as a SIXTH column, and only
		// when there is more than one version: a contract with one line has
		// nothing to add beyond the summary, and printing "1.4.2=published"
		// beside "1.4.2 published" on every row is noise that trains a
		// reader to stop reading the column where it matters. A version-less
		// history — every history before P4 — prints byte-identically to
		// before.
		if window := contractsVersionWindow(ci); window != "" {
			_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", ci.Space, ci.ID, ci.Provider, ci.Version, ci.State, window)
			continue
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\t%s\n", ci.Space, ci.ID, ci.Provider, ci.Version, ci.State)
	}
	return 0
}

// contractsVersionWindow renders ci's per-version states as
// "1.0.0=retired 1.4.1=published 2.0.0=published", semver-ascending (the
// order internal/cache already put them in), or "" when the contract has
// fewer than two recorded versions.
//
// Space-separated inside one tab-delimited column, so the row stays five
// fields for every caller that splits on tab and six only where there is a
// window to read.
func contractsVersionWindow(ci cache.ContractInfo) string {
	if len(ci.Versions) < 2 {
		return ""
	}
	parts := make([]string, 0, len(ci.Versions))
	for _, v := range ci.Versions {
		parts = append(parts, v.Version+"="+v.State)
	}
	return strings.Join(parts, " ")
}

var _ Command = (*ContractsCommand)(nil)
