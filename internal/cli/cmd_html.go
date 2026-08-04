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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/html"
	"github.com/ydnikolaev/a2ahub/internal/operational"
	"github.com/ydnikolaev/a2ahub/internal/space"
)

const htmlDefaultOut = ".a2a/dashboard.html"

// HtmlCommand implements `a2a html` / `a2a dashboard`. name carries the invoked
// verb so the usage line and catalog show the right one (they are the same
// command; `dashboard` is the friendly alias).
type HtmlCommand struct {
	store            *cache.Store
	operational      OperationalSnapshotReader
	historyValidator space.ContractHistoryDocumentValidator
	name             string
}

// OperationalSnapshotReader supplies the shared operational projection. The
// HTML transport consumes it verbatim; it does not infer work or protocol
// state from its separate attention/read models.
type OperationalSnapshotReader interface {
	Snapshot(context.Context) (operational.Snapshot, error)
}

// NewHtmlCommand constructs the `a2a html` command over a composed Store.
func NewHtmlCommand(store *cache.Store) *HtmlCommand {
	return &HtmlCommand{store: store, name: "html"}
}

// NewHtmlCommandWithOperational constructs the production HTML command over
// the canonical operational source.
func NewHtmlCommandWithOperational(store *cache.Store, source OperationalSnapshotReader) *HtmlCommand {
	return &HtmlCommand{store: store, operational: source, name: "html"}
}

// NewHtmlCommandWithOperationalAndContractHistory constructs the production
// HTML command with both canonical read dependencies supplied by cmd/a2a.
func NewHtmlCommandWithOperationalAndContractHistory(store *cache.Store, source OperationalSnapshotReader, validator space.ContractHistoryDocumentValidator) *HtmlCommand {
	return &HtmlCommand{store: store, operational: source, historyValidator: validator, name: "html"}
}

// NewDashboardCommand is the `a2a dashboard` alias (same behavior).
func NewDashboardCommand(store *cache.Store) *HtmlCommand {
	return &HtmlCommand{store: store, name: "dashboard"}
}

// NewDashboardCommandWithOperational is the production dashboard alias.
func NewDashboardCommandWithOperational(store *cache.Store, source OperationalSnapshotReader) *HtmlCommand {
	return &HtmlCommand{store: store, operational: source, name: "dashboard"}
}

// NewDashboardCommandWithOperationalAndContractHistory is the production
// dashboard alias with the canonical historical-document validator injected.
func NewDashboardCommandWithOperationalAndContractHistory(store *cache.Store, source OperationalSnapshotReader, validator space.ContractHistoryDocumentValidator) *HtmlCommand {
	return &HtmlCommand{store: store, operational: source, historyValidator: validator, name: "dashboard"}
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
	noOpen := fs.Bool("no-open", false, "don't open the rendered file in your browser (for scripts/CI)")
	focus := fs.String("focus", "", "focus one trusted space:artifact route")
	updateFocus := fs.Bool("update", false, "focus the trusted local update card")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stdio.Stderr, "usage: a2a %s [--system <id>] [--out <path>] [--json] [--demo] [--no-open] [--focus <space:id>] [--update]\n", c.name)
		return 2
	}

	var data html.Data
	var err error
	if *demo {
		data, err = html.DemoData()
	} else {
		now := time.Now()
		if c.operational == nil {
			data, err = html.AssembleWithContractHistory(ctx, c.store, *system, now, c.historyValidator)
		} else {
			var snapshot operational.Snapshot
			snapshot, err = c.operational.Snapshot(ctx)
			if err == nil {
				data, err = html.AssembleWithOperationalAndContractHistory(ctx, c.store, *system, now, snapshot, c.historyValidator)
			}
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: %v\n", c.name, err)
		return 1
	}
	if *focus != "" {
		spaceID, artifactID, ok := parseHTMLFocus(*focus)
		if !ok {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: --focus requires a qualified space:artifact id\n", c.name)
			return 2
		}
		found := false
		for _, item := range append(append([]html.Item(nil), data.Inbox...), data.Outbox...) {
			if item.Space == spaceID && item.ID == artifactID {
				found = true
				break
			}
		}
		data.Focus = &html.Focus{Space: spaceID, ArtifactID: artifactID, Found: found}
	} else if *updateFocus {
		data.Focus = &html.Focus{Update: true, Found: data.Tooling.UpdateAvailable || data.Tooling.Required}
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

	docs, dErr := html.Docs()
	if dErr != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: %v\n", c.name, dErr)
		return 1
	}
	page, rErr := html.Render(html.DefaultTemplate(), data, docs)
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
	_, _ = fmt.Fprintf(stdio.Stdout, "a2a %s: wrote %s\n", c.name, *out)

	// Open it in the default browser (default-on convenience; --no-open for
	// scripts/CI). Best-effort: a launch failure never fails the render.
	if !*noOpen {
		browserFocus := *focus
		if *updateFocus {
			browserFocus = "update"
		}
		if oErr := openInBrowser(*out, browserFocus); oErr != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a %s: couldn't open a browser (%v) — open %s yourself\n", c.name, oErr, *out)
		} else {
			_, _ = fmt.Fprintf(stdio.Stdout, "a2a %s: opening it in your browser…\n", c.name)
		}
	}
	return 0
}

// openInBrowser launches the OS default handler for path (the rendered HTML),
// fire-and-forget — it does NOT wait for the browser. The path is a2a's own
// computed output file, never external input. Absolute-izes the path so the
// launcher resolves it regardless of the browser's working directory.
func openInBrowser(path, focus string) error {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	target := path
	if focus != "" {
		fragment := "a2a-focus=" + focus
		if focus == "update" {
			fragment = "a2a-update"
		}
		target = (&url.URL{Scheme: "file", Path: path, Fragment: fragment}).String()
	}
	return browserCommand(runtime.GOOS, target).Start()
}

func parseHTMLFocus(value string) (string, string, bool) {
	spaceID, artifactID, ok := strings.Cut(value, ":")
	if !ok || spaceID == "" || artifactID == "" || strings.ContainsAny(value, "\r\n\t/#?") {
		return "", "", false
	}
	return spaceID, artifactID, true
}

// browserCommand builds (but does not start) the OS default-handler command for
// path. Split out from openInBrowser so the per-GOOS argv is unit-testable
// without launching a real browser. argv-based (no shell) — path is never
// shell-interpreted.
func browserCommand(goos, path string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", path)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default: // linux, *bsd
		return exec.Command("xdg-open", path)
	}
}

var _ Command = (*HtmlCommand)(nil)
