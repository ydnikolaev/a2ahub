// P25 `a2a feedback <new|validate|submit|status|triage>` (spec 25 §T1).
// This file's only package-level symbols are FeedbackCommand + its
// NewFeedbackCommand constructor + FeedbackSubcommands() — no shared
// helper, no package var beyond that SSOT list, mirroring cmd_contract.go's
// own Placement convention. Every sub-verb is dispatched by an internal
// switch (never registered as individual cli.Command values), same shape
// as ContractCommand.Run.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/feedback"
)

// FeedbackCommand implements `a2a feedback <new|validate|submit|status|
// triage>` (spec 25 §T1). `triage` is a hub-operator verb (mirrors how
// `skill install`, P20, is host-only) — this file adds no MCP glue.
type FeedbackCommand struct {
	drafter    *feedback.Drafter
	submitter  *feedback.Submitter
	ledgerPath string
	hubRoot    string
	hubReader  feedback.HubReader

	now      func() time.Time
	readFile func(path string) ([]byte, error)
}

// NewFeedbackCommand constructs the feedback command. drafter is required
// for `new`; submitter is required for `submit` (rails anti-pattern #10 —
// callers wire the real space.WriteFunnel-backed feedback.Submitter at
// cmd/a2a, tests inject one built over host.NewFakeHost()). ledgerPath is
// `.a2a/feedback/ledger.yaml`'s path; hubRoot is the cwd `triage` runs
// from (§T1: "run from the hub repo root"); hubReader resolves `status`'s
// hub-side reads (production: feedback.DefaultHubReader; tests: a
// local-fixture func).
func NewFeedbackCommand(drafter *feedback.Drafter, submitter *feedback.Submitter, ledgerPath, hubRoot string, hubReader feedback.HubReader) *FeedbackCommand {
	return &FeedbackCommand{
		drafter: drafter, submitter: submitter, ledgerPath: ledgerPath, hubRoot: hubRoot, hubReader: hubReader,
		now: time.Now, readFile: os.ReadFile,
	}
}

// SetReadFileForTest overrides the injected file reader (test-only DI
// seam, rails anti-pattern #10 convention).
func (c *FeedbackCommand) SetReadFileForTest(f func(path string) ([]byte, error)) {
	c.readFile = f
}

// SetClockForTest overrides the injected clock (triage --apply's digest
// date / backlog date).
func (c *FeedbackCommand) SetClockForTest(now func() time.Time) { c.now = now }

// Name implements cli.Command.
func (c *FeedbackCommand) Name() string { return "feedback" }

// Synopsis implements cli.Command.
func (c *FeedbackCommand) Synopsis() string {
	return "agent feedback loop: new <kind> [--title] | validate <file> [--ci] | submit <file> | status [--json] | triage [--json] [--apply <file>]"
}

// Run implements cli.Command.
func (c *FeedbackCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback <new|validate|submit|status|triage> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "new":
		return c.runNew(rest, stdio)
	case "validate":
		return c.runValidate(rest, stdio)
	case "submit":
		return c.runSubmit(ctx, rest, stdio)
	case "status":
		return c.runStatus(rest, stdio)
	case "triage":
		return c.runTriage(rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "feedback: unknown subcommand %q\n", sub)
		return 2
	}
}

var _ Command = (*FeedbackCommand)(nil)

// FeedbackSubcommands is the SSOT list of the `a2a feedback` family's
// sub-verbs for surface enumeration (mirrors ContractSubcommands' role) —
// the lead's completion/catalog derivation reads this list. KEEP IN SYNC
// with Run's switch above.
func FeedbackSubcommands() []string {
	return []string{"new", "validate", "submit", "status", "triage"}
}

// runNew implements `a2a feedback new <kind> [--title <text>]` (§T1: the
// positional <kind> comes FIRST, matching the canonical invocation the
// spec/skill docs/e2e txtar all write — args[0] is taken as kind BEFORE
// flag.Parse runs, since Go's flag package stops parsing at the first
// non-flag token and would otherwise treat `new bug --title x` as 3
// positional args (see ContractCommand.runNew's identical shape).
func (c *FeedbackCommand) runNew(args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback new <bug|feature|docs|friction|protocol> [--title <text>]")
		return 2
	}
	kind := args[0]

	fs := flag.NewFlagSet("feedback new", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	title := fs.String("title", "", "the report's title (8-80 chars)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback new <bug|feature|docs|friction|protocol> [--title <text>]")
		return 2
	}
	path, err := c.drafter.Draft(kind, *title)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "feedback new: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdio.Stdout, path)
	return 0
}

// runValidate implements `a2a feedback validate <file> [--ci]` — the same
// exit-code contract shape as `a2a validate --ci` (0 valid / 1 invalid /
// 2 usage, §11 A1).
func (c *FeedbackCommand) runValidate(args []string, stdio IO) int {
	fs := flag.NewFlagSet("feedback validate", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	ci := fs.Bool("ci", false, "CI intake mode: additionally enforce the filename/path guards")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback validate <file> [--ci]")
		return 2
	}
	path := fs.Arg(0)
	raw, err := c.readFile(path)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "feedback validate: %v\n", err)
		return 1
	}
	report := feedback.Validate(raw, feedback.Options{CI: *ci, Path: path})
	for _, v := range report.Violations {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s %s: %s\n", v.Code, v.Field, v.Message)
	}
	if !report.Valid {
		return 1
	}
	_, _ = fmt.Fprintln(stdio.Stdout, "valid")
	return 0
}

// runSubmit implements `a2a feedback submit <file>`.
func (c *FeedbackCommand) runSubmit(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("feedback submit", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback submit <file>")
		return 2
	}
	result, err := c.submitter.Submit(ctx, fs.Arg(0))
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "feedback submit: %v\n", err)
		return 1
	}
	if result.AlreadyOpen {
		_, _ = fmt.Fprintf(stdio.Stdout, "feedback submit: already submitted (PR %s)\n", result.PRURL)
		return 0
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "feedback submit: opened PR %s for %s\n", result.PRURL, result.ID)
	return 0
}

// runStatus implements `a2a feedback status [--json]`.
func (c *FeedbackCommand) runStatus(args []string, stdio IO) int {
	fs := flag.NewFlagSet("feedback status", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback status [--json]")
		return 2
	}
	rows, err := feedback.Status(c.ledgerPath, c.hubReader)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "feedback status: %v\n", err)
		return 1
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "feedback status: no feedback filed")
		return 0
	}
	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "feedback status: %v\n", err)
			return 1
		}
		return 0
	}
	// §T1 table: id · kind · title · PR · hub status · resolution. (PR is the
	// PR URL, not a derived open/merged/closed state — feedback status reads the
	// committed hub file, not live PR state; a host round-trip for PR state is a
	// documented v1 simplification. Resolution is hub-mutated and empty until
	// triage lands a verdict.)
	for _, r := range rows {
		res := r.Resolution
		if res == "" {
			res = "-"
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Kind, r.Title, r.PRURL, r.HubStatus, res)
	}
	return 0
}

// runTriage implements `a2a feedback triage [--json] [--apply <file>]`.
// The mechanical verb both lists dedupe candidates (default) and, given
// `--apply <verdicts.yaml>`, lands the operator-agent's already-written
// judgment (mutate/backlog/digest) — spec §T1's flag column only names
// `--json`; `--apply` is this wave's own addition to carry verdict input
// into the mechanical verb (see this phase's Deviations report).
func (c *FeedbackCommand) runTriage(args []string, stdio IO) int {
	fs := flag.NewFlagSet("feedback triage", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	apply := fs.String("apply", "", "apply verdicts from a YAML file (operator-agent judgment written after reviewing the listing)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a feedback triage [--json] [--apply <verdicts.yaml>]")
		return 2
	}

	if *apply != "" {
		raw, err := c.readFile(*apply)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "feedback triage: %v\n", err)
			return 1
		}
		verdicts, err := feedback.ParseVerdicts(raw)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "feedback triage: %v\n", err)
			return 1
		}
		result, err := feedback.ApplyVerdicts(c.hubRoot, verdicts, c.now())
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "feedback triage: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "feedback triage: applied %d, skipped %d, wip-limit-hit %d\n", len(result.Applied), len(result.Skipped), len(result.WipLimitHit))
		return 0
	}

	report, err := feedback.Triage(c.hubRoot)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "feedback triage: %v\n", err)
		return 1
	}
	if report.Clean() {
		_, _ = fmt.Fprintln(stdio.Stdout, "feedback triage: inbox clean")
		return 0
	}
	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report.Entries); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "feedback triage: %v\n", err)
			return 1
		}
		return 0
	}
	for _, e := range report.Entries {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s\t%s\t%s\n", e.Item.ID, e.Item.Kind, e.Item.Title)
		for _, cand := range e.Candidates {
			_, _ = fmt.Fprintf(stdio.Stdout, "  dup? %s (%s): %s\n", cand.ID, cand.Source, cand.Title)
		}
	}
	return 0
}
