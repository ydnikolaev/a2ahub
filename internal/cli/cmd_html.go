// OP-214 `a2a html` (+ the `a2a dashboard` alias): render a self-contained,
// static local dashboard from the composed read surface — the graph (who
// depends on / exchanges with whom), inbox, contract drift, tooling, per-space
// health — plus a human-readable Guide. Pure read layer (§7.6): no network in
// the render path, no writes to any space. This file's only package-level
// symbols are HtmlCommand + NewHtmlCommand + NewDashboardCommand.
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/html"
)

const htmlDefaultOut = ".a2a/dashboard.html"

// HtmlCommand implements `a2a html` / `a2a dashboard`. name carries the invoked
// verb so the usage line and catalog show the right one (they are the same
// command; `dashboard` is the friendly alias).
type HtmlCommand struct {
	store *cache.Store
	name  string
}

// NewHtmlCommand constructs the `a2a html` command over a composed Store.
func NewHtmlCommand(store *cache.Store) *HtmlCommand {
	return &HtmlCommand{store: store, name: "html"}
}

// NewDashboardCommand is the `a2a dashboard` alias (same behavior).
func NewDashboardCommand(store *cache.Store) *HtmlCommand {
	return &HtmlCommand{store: store, name: "dashboard"}
}

// Name implements Command.
func (c *HtmlCommand) Name() string { return c.name }

// Synopsis implements Command.
func (c *HtmlCommand) Synopsis() string {
	return "render a self-contained local dashboard (graph + inbox + contracts) from the cache"
}

// Run implements Command. Exit codes: 2 = usage; 1 = assemble/render/write
// error; 0 = ok.
func (c *HtmlCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	system := fs.String("system", "", "view this system's perspective (default: your configured system)")
	out := fs.String("out", htmlDefaultOut, "output HTML file path")
	jsonOut := fs.Bool("json", false, "emit the DATA model as JSON to stdout (no HTML file)")
	demo := fs.Bool("demo", false, "render the embedded demo fixture (all states/types) — no connected space needed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s [--system <id>] [--out <path>] [--json] [--demo]\n", c.name)
		return 2
	}

	var data html.Data
	var err error
	if *demo {
		data, err = html.DemoData()
	} else {
		data, err = html.Assemble(ctx, c.store, *system, time.Now())
	}
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: %v\n", c.name, err)
		return 1
	}

	if *jsonOut {
		b, mErr := html.MarshalData(data)
		if mErr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: %v\n", c.name, mErr)
			return 1
		}
		_, _ = stdio.Stdout.Write(b)
		_, _ = fmt.Fprintln(stdio.Stdout)
		return 0
	}

	page, rErr := html.Render(html.DefaultTemplate(), data)
	if rErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: %v\n", c.name, rErr)
		return 1
	}
	if mkErr := os.MkdirAll(filepath.Dir(*out), 0o755); mkErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: cannot create output dir: %v\n", c.name, mkErr)
		return 1
	}
	if wErr := os.WriteFile(*out, page, 0o644); wErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: cannot write %s: %v\n", c.name, *out, wErr)
		return 1
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "a2a %s: wrote %s (open it in a browser)\n", c.name, *out)
	return 0
}

var _ Command = (*HtmlCommand)(nil)
