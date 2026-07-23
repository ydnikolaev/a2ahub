// P23 (OP-222) `a2a completion <bash|zsh|fish>`: print a static shell
// completion script to stdout. Pure host-side act (no space, no network, no
// config) — the script is rendered from an injected verb inventory (see
// completion.go). This file's only package-level symbols are CompletionCommand
// + NewCompletionCommand.
package cli

import (
	"context"
	"fmt"
	"strings"
)

// CompletionCommand implements `a2a completion`. cmds is the top-level verb
// list and subFamilies maps each `a2a <verb> <sub>` family verb (contract,
// feedback, …) to its sub-verb names, both injected by cmd/a2a (the single
// owner of the dispatch surface). Both may be nil when the command is
// constructed only to read Name()/Synopsis() (the catalog seam) — Run is never
// invoked in that case.
type CompletionCommand struct {
	cmds        []string
	subFamilies map[string][]string
}

// NewCompletionCommand constructs the completion verb over an injected verb
// inventory. Passing nil/nil is valid for a metadata-only construction.
func NewCompletionCommand(cmds []string, subFamilies map[string][]string) *CompletionCommand {
	return &CompletionCommand{cmds: cmds, subFamilies: subFamilies}
}

// Name implements Command.
func (c *CompletionCommand) Name() string { return "completion" }

// Synopsis implements Command.
func (c *CompletionCommand) Synopsis() string {
	return "print a shell completion script (bash|zsh|fish)"
}

// Run implements Command. Exit codes: 2 = usage (missing/extra arg); 1 =
// unknown shell / render error; 0 = ok.
func (c *CompletionCommand) Run(_ context.Context, args []string, stdio IO) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a completion <%s>\n", strings.Join(CompletionShells, "|"))
		return 2
	}
	script, err := RenderCompletion(args[0], c.cmds, c.subFamilies)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a completion: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprint(stdio.Stdout, script)
	return 0
}

var _ Command = (*CompletionCommand)(nil)
