// `a2a notify render` (space-notify-2026-08 P3, spec 03 §T1): turns a
// space-repo checkout (cwd) into an ordered JSON array of P3's message
// model. It reads `./space.yaml` and reuses cmd_validate_ci.go's own
// gitChangedFilesFunc seam for the push range — never a second `git diff`
// shell-out, never a second space.yaml reader.
//
// `notify` and `notifications` are two different verbs on purpose:
// `a2a notifications` owns the LOCAL native channels (macOS/VS Code) and
// their offer machine; `a2a notify` owns the SPACE-SIDE CI projection and
// delivery. This file's own Synopsis says so, per spec 03 T1's own
// requirement that each verb's help line name which plane it belongs to.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/spacenotify"
)

// NotifyCommand implements `a2a notify <render>`. Only `render` exists in
// this phase (P3); `send` (P4) and `setup`/`discover`/`verify` (P6) are
// later phases' additions to this SAME command, not a new one — see spec
// 03 T1's own "what adding a verb costs" note.
type NotifyCommand struct {
	// gitChanged is the DI seam over `git diff --name-only`
	// (cmd_validate_ci.go's own gitChangedFilesFunc type + gitDiffNameOnly
	// implementation) — reused verbatim, never a second shell-out.
	gitChanged gitChangedFilesFunc
	// now is the injected clock (rails: no buried time.Now()).
	now func() time.Time
}

// NewNotifyCommand constructs the real, wired NotifyCommand.
func NewNotifyCommand() *NotifyCommand {
	return &NotifyCommand{gitChanged: gitDiffNameOnly, now: time.Now}
}

// Name implements cli.Command.
func (c *NotifyCommand) Name() string { return "notify" }

// Synopsis implements cli.Command.
func (c *NotifyCommand) Synopsis() string {
	return "space-side CI notification projection and delivery (SPACE plane — see `a2a notifications` for the local/native plane): render --base <sha>|--all|--only <id,id> [--limit <n>] [--json] — turn a push range into P3's ordered JSON message array"
}

const notifyUsage = "usage: a2a notify render (--base <sha> | --all | --only <id,id>) [--limit <n>] [--json]"

// Run implements cli.Command.
func (c *NotifyCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 || IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, notifyUsage)
		return 0
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "render":
		return runNotifyRender(ctx, ".", c.gitChanged, c.now, rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify: unknown sub-verb %q\n%s\n", sub, notifyUsage)
		return 2
	}
}

// runNotifyRender is `render`'s own body, factored out (root/git/now as
// explicit parameters, mirroring runValidateCI's own shape) so a test can
// point it at a fixture checkout without going through NotifyCommand.Run.
// Exit codes: 2 = usage (no/conflicting range selector, bad --limit);
// 1 = a Render refusal (unknown --only id, undeclared secret) or a
// checkout/manifest read error; 0 = success (an empty array is still 0).
func runNotifyRender(ctx context.Context, root string, git gitChangedFilesFunc, now func() time.Time, args []string, stdio IO) int {
	fs := flag.NewFlagSet("notify render", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	base := fs.String("base", "", "the push range's base sha (mutually exclusive with --all/--only)")
	all := fs.Bool("all", false, "render every qualifying artifact, ignoring any push range")
	only := fs.String("only", "", "comma-separated artifact ids — bypasses the push range, the event filter and digest coalescing")
	limit := fs.Int("limit", 5, "the digest-coalescing threshold for the automatic (--base/--all) path")
	_ = fs.Bool("json", true, "JSON is the only output format in v1; this flag exists for CLI symmetry")
	if err := fs.Parse(args); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, notifyUsage)
		return 2
	}

	selectors := 0
	if *base != "" {
		selectors++
	}
	if *all {
		selectors++
	}
	if *only != "" {
		selectors++
	}
	if selectors != 1 {
		_, _ = fmt.Fprintf(stdio.Stderr, "%s: exactly one of --base, --all, --only is required\n%s\n", "a2a notify render", notifyUsage)
		return 2
	}
	if *limit <= 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a notify render: --limit must be positive")
		return 2
	}

	raw, err := os.ReadFile(filepath.Join(root, "space.yaml"))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: cannot read space.yaml: %v\n", err)
		return 1
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: %v\n", err)
		return 1
	}

	opts := spacenotify.Options{Limit: *limit, Now: now()}
	switch {
	case *only != "":
		opts.Mode = spacenotify.ModeOnly
		opts.OnlyIDs = splitNonEmpty(*only, ",")
	case *all:
		opts.Mode = spacenotify.ModeAll
	default:
		opts.Mode = spacenotify.ModePush
		changed, err := git(ctx, root, *base)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: %v\n", err)
			return 1
		}
		opts.Changed = changed
	}

	messages, err := spacenotify.Render(ctx, root, manifest, opts)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: %v\n", err)
		return 1
	}
	if messages == nil {
		messages = []spacenotify.Message{}
	}

	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(messages); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a notify render: cannot encode JSON output: %v\n", err)
		return 1
	}
	return 0
}

// splitNonEmpty splits s on sep, trims each part and drops empties — the
// `--only a,b,,c` typo-tolerance every comma-list flag in this package
// applies.
func splitNonEmpty(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
