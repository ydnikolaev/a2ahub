package main

import (
	"context"
	"fmt"
	"io"

	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

// help.go makes `a2a <verb> --help` answerable WITHOUT a project config, a
// mirror, or a credential.
//
// Every config-dependent verb is dispatched through a wire.go closure that
// resolves its dependencies BEFORE the command runs — so asking a verb how
// to call it used to fail on the very setup the operator was trying to get
// right ("no project config", or a credential prompt for `contract publish
// --help`). Help is documentation, not an operation: it must never need
// state.
//
// The construction reuses catalog.go's own nil/stub-dep pattern (each
// cli.Command is built purely so its Run can parse flags and print
// usage/defaults — no dependency is ever invoked, because flag.Parse
// returns flag.ErrHelp before any handler body runs). helpCoverage_test.go
// drives EVERY dispatch verb through this path with no config present, so a
// new verb cannot quietly reintroduce the trap.

// helpFlags are the tokens that mean "explain yourself", matching Go's own
// flag package (which treats all three as help).
var helpFlags = map[string]bool{"-h": true, "--help": true, "-help": true}

// helpRequested reports whether args contain a help flag before the `--`
// end-of-flags terminator. Anything after `--` is data, not a flag.
func helpRequested(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if helpFlags[a] {
			return true
		}
	}
	return false
}

// runVerbHelp prints verb's own usage/flags and returns 0. Both streams go
// to stdout: help is the requested output, so `a2a submit --help | less`
// works (Go's flag package writes defaults to the FlagSet's output, which
// each command points at stderr for real error paths).
func runVerbHelp(verb string, args []string, stdout io.Writer) int {
	cmd, ok := helpCommand(verb)
	if !ok {
		// version / mcp / __catalog: no cli.Command to introspect. Their
		// one-line synopsis IS their help.
		if synopsis, known := catalogHandTypedSynopsis[verb]; known {
			_, _ = fmt.Fprintf(stdout, "a2a %s — %s\n", verb, synopsis)
			return 0
		}
		_, _ = fmt.Fprintf(stdout, "a2a %s\n", verb)
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "a2a %s — %s\n\n", cmd.Name(), cmd.Synopsis())
	cmd.Run(helpContext(), args, cli.IO{Stdout: stdout, Stderr: stdout})
	return 0
}

// helpCommand builds verb's cli.Command with nil/stub dependencies —
// catalogCLICommand for everything it already covers, plus the two verbs
// its caller special-cases (contract, which catalog expands into
// `contract-<sub>` rows, and html/dashboard's alias pair).
func helpCommand(verb string) (cli.Command, bool) {
	if cmd, ok := catalogCLICommand(verb); ok {
		return cmd, true
	}
	if verb == "contract" {
		return cli.NewContractCommand(nil, nil, "", "", "", space.Manifest{}, cli.SubmitHostConfig{}, nil), true
	}
	return nil, false
}

// helpContext is the context a help-mode Run gets. It is never used for
// I/O: flag parsing short-circuits before any command body touches it.
func helpContext() context.Context { return context.Background() }
