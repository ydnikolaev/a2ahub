package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
)

// catalog.go implements the hidden `a2a __catalog` verb (spec 13 §11
// wave-7 amendment: "catalog seam RESOLVED to cmd/a2a-only"). It prints a
// deterministic markdown projection of every CLI verb (§7.2 dispatch
// surface) and every MCP tool (§7.7) to stdout, built entirely by
// constructing the SAME cli.Command / mcp.Registry types the binary
// already wires — never a second, hand-maintained catalog.
//
// The MCP half reuses cmd/a2a/mcp_parity_test.go's emptyRegistry() nil/
// stub-dep pattern verbatim. The CLI half applies the identical idea to
// internal/cli's own Command constructors: each is built with nil/zero
// dependencies purely to read Name()+Synopsis() — no handler or
// dependency is ever invoked, so a nil *cache.Store / lifecycleDeps{} /
// nil interface is always safe here.
//
// skill/a2ahub/reference/commands.md is this verb's committed, byte-for-
// byte output (regenerated via `go run ./cmd/a2a __catalog`); the
// skill-drift CI job (spec 13 T4/T5) regenerates and diffs it.

// catalogHandTypedSynopsis carries the ONLY three hand-typed synopses in
// this file: dispatch verbs with no cli.Command to read Synopsis() from.
// version prints a build stamp; mcp serves the JSON-RPC session for the
// life of the process; __catalog is this verb itself (self-referencing —
// it cannot construct-and-read its own Synopsis()).
var catalogHandTypedSynopsis = map[string]string{
	"version":   "print the binary version stamp",
	"mcp":       "serve the §7.7 MCP tool surface over stdio JSON-RPC",
	"__catalog": "print this generated command/MCP catalog (hidden, machine-consumed)",
}

// catalogCommandRow is one "## Commands" section row.
type catalogCommandRow struct {
	Name     string
	Synopsis string
}

// catalogCommandRows builds every "## Commands" row, sorted by name:
// buildCommands() keys, each read from a nil/stub-dep cli.Command
// construction (never re-typed); `contract` EXPANDED to `contract <sub>`
// rows from cli.ContractSubcommands() (its own doc comment: the ONLY
// machine-enumerable home of the 6 contract sub-verbs — mirrors
// mcp_parity_test.go's designatedCLIVerbs() two-level shape); and the 3
// catalogHandTypedSynopsis entries for verbs with no cli.Command.
//
// The separator is a SPACE, and it used to be a hyphen. That rendered seven
// names — `contract-publish`, `contract-adopt`, … — that the dispatcher
// answers with `unknown command`: `contract` is a single dispatch key whose
// sub-verb is its first ARGUMENT, never part of the verb's own name. This
// catalog is the machine-consumed one (it IS
// skill/a2ahub/reference/commands.md, shipped into every consumer repo), so
// an agent reading it typed a command that does not exist, and
// skill/a2ahub/loops.md had propagated one of them into prose.
//
// The `skill-drift` CI gate could not see it: it regenerates this file from
// this same function and byte-diffs, so it proves the committed copy is
// current, never that what it advertises is real. TestEveryCatalogNameIsDispatchable
// (catalog_test.go) is the guard that checks the thing that actually matters.
func catalogCommandRows() []catalogCommandRow {
	var rows []catalogCommandRow
	for name := range buildCommands() {
		if name == "contract" {
			for _, sub := range cli.ContractSubcommands() {
				rows = append(rows, catalogCommandRow{Name: "contract " + sub.Name, Synopsis: sub.Synopsis})
			}
			continue
		}
		if synopsis, ok := catalogHandTypedSynopsis[name]; ok {
			rows = append(rows, catalogCommandRow{Name: name, Synopsis: synopsis})
			continue
		}
		cmd, ok := catalogCLICommand(name)
		if !ok {
			// Every buildCommands() key not handled above MUST resolve to a
			// real cli.Command — a gap here is exactly the drift the
			// name-parity guard (catalog_test.go) exists to catch.
			// Panicking at generation time (this closure only runs inside
			// `a2a __catalog` / its tests, never a real user's other verb
			// invocation) surfaces a missing case immediately instead of
			// silently omitting a row.
			panic(fmt.Sprintf("a2a __catalog: no cli.Command construction for dispatch verb %q", name))
		}
		rows = append(rows, catalogCommandRow{Name: cmd.Name(), Synopsis: cmd.Synopsis()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}

// catalogCLICommand constructs verb name's cli.Command with nil/stub deps
// (the P14 emptyRegistry() precedent) purely to read Name()+Synopsis() —
// no handler/dependency is ever invoked. contract/version/mcp/__catalog
// are handled by catalogCommandRows' own caller-side special cases and
// never reach here. The read verbs (readVerbs()) and lifecycle verbs
// (lifecycleVerbs()) are constructed via wire.go's own maps — reused, not
// re-listed — so this switch only covers the verbs wire.go builds inline.
func catalogCLICommand(name string) (cli.Command, bool) {
	switch name {
	case "init":
		return cli.NewInitCommand(""), true
	case "template":
		return cli.NewTemplateCommand(), true
	case "space":
		// Nil template FS / empty version: the catalog only reads Name() +
		// Synopsis(), never scaffolds.
		return cli.NewSpaceCommand(nil, ""), true
	case "skill":
		return cli.NewSkillCommand(nil, ""), true
	case "completion":
		return cli.NewCompletionCommand(nil, nil), true
	case "notifications":
		return cli.NewNotificationsCommand(nil), true
	case "connect":
		return cli.NewConnectCommand("", "", ""), true
	case "disconnect":
		return cli.NewDisconnectCommand("", "", "", nil), true
	case "new":
		return cli.NewNewCommand("", "", nil, nil), true
	case "validate":
		return cli.NewValidateCommand(nil, ""), true
	case "sync":
		return cli.NewSyncCommand("", "", "", nil), true
	case "attach":
		// Nil bounds/staging: like "data" below, the catalog path reads only
		// Name()+Synopsis(). `attach` is a designated TOP-LEVEL verb rather
		// than a `data` sub-action deliberately — P4's plan argues
		// attachment is a first-class act, and the parity gate is a reason
		// to ship both halves together, not to hide it inside a subsystem.
		return cli.NewAttachCommand("", datapackage.Bounds{}), true
	case "data":
		// Nil operations: the catalog/help path only reads Name()+Synopsis()
		// (DataCommand.Run tolerates nil ops for every sub-verb it never
		// reaches here). One flat row, mirroring "work"'s own precedent
		// (WorkCommand{} below) rather than "contract"'s per-sub expansion:
		// catalog_test.go's expectedCatalogCommandNames() — an independent
		// second derivation outside this wave's allowlist — special-cases
		// only "contract", so expanding "data" here would drift against it.
		return cli.NewDataCommand(nil), true
	case "doctor":
		return cli.NewDoctorCommand(nil, "", "", "", ""), true
	case "update":
		return cli.NewUpdateCommand("", "", "", ""), true
	case "submit":
		return cli.NewSubmitCommand(nil, nil, nil, "", "", "", "", cli.SubmitHostConfig{}), true
	case "feedback":
		return cli.NewFeedbackCommand(nil, nil, "", "", nil), true
	case "await":
		return cli.NewAwaitCommand(nil), true
	case "whatsnew":
		return cli.NewWhatsnewCommand(""), true
	case "work":
		// Metadata-only zero value: Name/Synopsis do not touch dependencies.
		return &cli.WorkCommand{}, true
	case "serve":
		return cli.NewServeCommand(nil), true
	}
	if construct, ok := readVerbs()[name]; ok {
		return construct(nil), true
	}
	if construct, ok := lifecycleVerbs()[name]; ok {
		return construct(lifecycleDeps{}), true
	}
	return nil, false
}

// catalogMCPRow is one "## MCP tools" section row.
type catalogMCPRow struct {
	Name        string
	Description string
}

// catalogMCPRows builds every "## MCP tools" row from the SAME
// mcp.BuildRegistry nil/stub-dep pattern cmd/a2a/mcp_parity_test.go's
// emptyRegistry() already uses (no handler in the returned registry is
// ever invoked here). Registry.List() already returns tools sorted by
// name.
func catalogMCPRows() []catalogMCPRow {
	reg := mcp.BuildRegistry(nil, mcp.WriteDeps{}, "", nil, mcp.NewDeps{})
	specs := reg.List()
	rows := make([]catalogMCPRow, 0, len(specs))
	for _, s := range specs {
		rows = append(rows, catalogMCPRow{Name: s.Name, Description: s.Description})
	}
	return rows
}

// renderCatalog renders the full deterministic markdown document: a
// title/preamble, "## Commands" (sorted by name), "## MCP tools" (sorted
// by name). No timestamp, no absolute path, no version/sha — every line is
// either a literal string or read from a name-sorted, in-process
// construction, so two calls in the same build always produce identical
// bytes (spec 13 §8 AC #3 / T5's byte-diff drift gate).
func renderCatalog() string {
	var b strings.Builder
	b.WriteString("# a2a command / MCP tool catalog\n\n")
	b.WriteString("Generated by `a2a __catalog` (spec 13 §11 wave-7 amendment). Do not hand-edit — this file is regenerated from the built binary and byte-diffed by the `skill-drift` CI job (spec 13 T4/T5).\n\n")

	b.WriteString("## Commands\n\n")
	for _, r := range catalogCommandRows() {
		fmt.Fprintf(&b, "- `%s` — %s\n", r.Name, r.Synopsis)
	}
	b.WriteString("\n")

	b.WriteString("## MCP tools\n\n")
	for _, r := range catalogMCPRows() {
		fmt.Fprintf(&b, "- `%s` — %s\n", r.Name, r.Description)
	}

	return b.String()
}

// runCatalog implements the hidden `a2a __catalog` verb: prints the
// deterministic markdown catalog to stdout and exits 0. It is registered
// in wire.go's buildCommands() but deliberately absent from main.go's
// printUsage — it is a machine-consumed, not a user-facing, verb.
//
// `--vocabulary --json` switches it to the machine-readable vocabulary a
// GATE reads: every outcome, every state per kind, every transition name,
// all derived from the transition table rather than listed. Without it, a
// gate policing what a component may spell has to carry its own copy of
// the list — correct the day it is written and silently wrong the day a
// state is added, which is the defect this epic exists to remove.
//
// The bare `a2a __catalog` output is UNCHANGED, byte for byte:
// skill/a2ahub/reference/commands.md is its committed projection and the
// skill-drift job regenerates and diffs it.
func runCatalog(args []string, stdout, stderr io.Writer) int {
	vocabulary, asJSON := false, false
	for _, arg := range args {
		switch arg {
		case "--vocabulary":
			vocabulary = true
		case "--json":
			asJSON = true
		default:
			_, _ = fmt.Fprintf(stderr, "a2a __catalog: unknown flag %q (accepts --vocabulary --json)\n", arg)
			return 2
		}
	}

	switch {
	case vocabulary && !asJSON:
		// Refused rather than defaulted to markdown. The vocabulary exists
		// to be PARSED; rendering it would invite a gate to read prose,
		// which is how the forbidden list becomes hand-maintained again by
		// a different route.
		_, _ = fmt.Fprintln(stderr, "a2a __catalog: --vocabulary requires --json (the vocabulary exists to be parsed, never rendered)")
		return 2
	case asJSON && !vocabulary:
		_, _ = fmt.Fprintln(stderr, "a2a __catalog: --json is only defined with --vocabulary (the command catalog itself is markdown by contract)")
		return 2
	case vocabulary:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(fold.BuildVocabulary()); err != nil {
			_, _ = fmt.Fprintf(stderr, "a2a __catalog: encode vocabulary: %v\n", err)
			return 1
		}
		return 0
	}

	_, _ = io.WriteString(stdout, renderCatalog())
	return 0
}
