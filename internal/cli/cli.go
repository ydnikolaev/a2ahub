// Package cli is the OP-2xx verb surface (ADR-001 "thin frontend"): flags/
// JSON in, exit codes/JSON out, zero business rules — every rule lives in a
// core package it calls. Each verb is a Command constructed with exactly the
// core dependencies it needs (rails DI); cmd/a2a is the single point that
// builds them with real implementations and registers them for dispatch.
//
// This file is the shared seam every verb file in this package builds
// against. It is deliberately minimal: the Command contract and the injected
// IO streams, nothing else. Verb files (cmd_init.go, cmd_new.go,
// cmd_submit.go, cmd_sync.go, cmd_doctor.go, and later P7/P8 verbs) each
// define their own command type + constructor; they never add package-level
// mutable state here.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
)

// Command is one a2a subcommand. Run receives the args AFTER the verb name
// and returns the process exit code (0 success; 2 usage/unknown by CLI
// convention; other non-zero for a runtime failure with an actionable
// message already written to IO.Stderr). Run must never call os.Exit and
// never write to the real os.Std* directly — only through the injected IO,
// so the whole surface stays testable.
type Command interface {
	// Name is the verb as typed on the command line (e.g. "submit").
	Name() string
	// Synopsis is a one-line description for the usage listing.
	Synopsis() string
	// Run executes the verb. ctx carries cancellation for any network/git
	// call the verb makes through a core package.
	Run(ctx context.Context, args []string, stdio IO) int
}

// IsHelpArg reports whether a command-line token is a request for help.
// Verbs whose FIRST argument is a sub-verb or a type name (`new <type>`,
// `contract <sub>`, `template <sub>`, `completion <shell>`, `feedback
// <sub>`) never reach flag.Parse for that token, so without this they
// answer `--help` with "unknown type/subcommand" — the least useful reply
// available to a program being asked how to use it. The three spellings
// match Go's own flag package.
func IsHelpArg(s string) bool {
	return s == "-h" || s == "--help" || s == "-help"
}

// IO is the injected stream set a Command reads and writes — never the
// global os.Std* (that is cmd/a2a's to supply), so tests drive a verb with
// buffers and assert on output + exit code.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// parseArgsAnyOrder parses fs while accepting positional arguments BEFORE
// the flags, and returns the positionals in the order they were written.
//
// It exists because Go's flag package stops parsing at the first non-flag
// token, so e.g. `contract deprecate <id> --successor X --sunset Y` leaves
// both flags unset — and the verb's own usage line tells the caller to
// write exactly that. The command documented an order it then refused,
// which is worse than either order alone: following the help text is what
// breaks.
//
// `contract adopt` and `feedback new` each already carried a private copy
// of this lift when `contract deprecate` made it the THIRD occurrence — the
// logic got one home then (cmd_contract.go). Wave K's live run found
// thirteen MORE commands with the identical bare-`fs.Parse(args)` gap
// (`a2a show <id> --json` among them), which makes a single per-file copy
// the wrong shape entirely; the lift now lives here, on cli.go, this
// package's own shared seam every verb file already builds against (see
// this file's own doc comment) — the same file IsHelpArg already calls
// home for the same reason: a small arg-parsing utility every verb file
// needs, owned by none of them.
//
// Both orders stay legal — flags-first callers (including every test
// written before this was found) are unaffected, because the lifted
// positionals are concatenated with whatever fs.Args() reports.
// workflowLine composes the one extra line a verb whose docs-manifest topic
// shares its own dispatch name (answers-that-hold-2026-08 spec 08 T1: "every
// verb with a workflow page") prints on a bare or malformed invocation — a
// pointer at the same topic's `a2a docs <topic>` page.
//
// It exists as a NAMED CALL, not an inline literal, because
// scripts/check-usage-workflow.sh's own extractor is an AST walk that finds
// every call to this one function and reads its argument (a literal, or an
// identifier resolved to a package-level `const` — spec 08 AC-6) — never a
// prefix grep over the file's bytes. A verb file composing this text inline
// would be invisible to that walk and is exactly the "gate whose regex was
// inert for weeks" class spec 08 T1 names. Every verb whose usage output
// should carry this line calls workflowLine with its own topic id; nothing
// else in this package may spell "workflow: " itself.
func workflowLine(topic string) string {
	return fmt.Sprintf("workflow: run `a2a docs %s` for the walkthrough", topic)
}

func parseArgsAnyOrder(fs *flag.FlagSet, args []string) ([]string, error) {
	var lifted []string
	rest := args
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		lifted = append(lifted, rest[0])
		rest = rest[1:]
	}
	if err := fs.Parse(rest); err != nil {
		return nil, err
	}
	return append(lifted, fs.Args()...), nil
}
