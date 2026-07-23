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
	"io"
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
