// P13 `a2a adapt` (spec docs/features/active/answers-that-hold-2026-08/
// specs/13-adapt-from-a-baseline.md): a filtered, ordered, imperative
// projection of the release-note corpus down to what OBLIGES the reader —
// `action.scope != "none"`, already labelled by the corpus itself
// (adoption.Change.Action; this file classifies nothing) — walked
// from the REPOSITORY's own `adapted_through` baseline (internal/space.
// ProjectConfig), never from the binary's own version history (the "two
// clocks" the spec's own title names). This file's only package-level
// symbols are AdaptCommand + NewAdaptCommand plus its own uniquely-named,
// file-private helpers (adapt* prefix) — no shared helper, no package var,
// per this package's established Placement convention (cmd_whatsnew.go,
// cmd_update.go).
//
// Wired at cmd/a2a/wire.go's `adapt` entry and listed by catalog.go. This
// file stays constructible and independently unit-testable without either,
// which is what let the verb be written before the registration landed.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
	"github.com/ydnikolaev/go-foundation/units/adoption"
	"gopkg.in/yaml.v3"
)

// AdaptCommand implements `a2a adapt [--done] [--json]` and
// `a2a adapt --require <version|change-id>` (P13 T1, D6): a thin
// flags-in/JSON-or-text-out wrapper over adoption.Pending with a2a's own
// notes.Policy — zero business rules live here (ADR-001 "thin frontend");
// the group assignment lives in the adoption unit, and the one thing this
// file DOES decide is the within-group tie-break, because it reuses
// cmd_whatsnew.go's whatsnewImpactOrder (adoption.ImpactOrder), so the final
// sort happens here.
type AdaptCommand struct {
	binaryVersion     string
	projectConfigPath string

	load              func() ([]adoption.Release, error)
	loadCurrentIssues func() ([]adoption.Change, error)
	loadProjectConfig func(path string) (space.ProjectConfig, error)

	// readFile/writeFile back adaptSaveAdaptedThrough's single-key YAML
	// update (below) — the same os.ReadFile/os.WriteFile DI seam
	// cmd_update.go and cmd_init.go already use, so tests drive --done's
	// write path without touching a real file.
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, perm os.FileMode) error

	// detectRun executes one detect: command for `--done` (AC-7). nil
	// defaults to adoption.DefaultDetectRunner (a real shell); tests inject a
	// fake so the corpus's own prose/placeholder detect strings (e.g.
	// "a2a submit <id>") are never actually exec'd by a test run.
	detectRun adoption.DetectRunner

	// execPath names the running binary in --require's refusal, the same
	// os.Executable seam DoctorCommand uses for the clock-ahead sentence.
	execPath func() (string, error)
}

// NewAdaptCommand constructs the adapt command. binaryVersion is this
// build's own bare version stamp (same convention as NewWhatsnewCommand/
// NewUpdateCommand — tests control it directly; `a2a version`'s dotted
// form, not the "a2a x.y.z (sha)" stamp). projectConfigPath is `.a2a/
// config.yaml` (the same path NewUpdateCommand already receives) — the
// REPOSITORY's own `adapted_through` baseline lives there, never in
// machine-local state.
func NewAdaptCommand(binaryVersion, projectConfigPath string) *AdaptCommand {
	return &AdaptCommand{
		binaryVersion:     binaryVersion,
		projectConfigPath: projectConfigPath,
		load:              func() ([]adoption.Release, error) { return adoption.Load(releasenotes.FS, notes.CorpusPattern) },
		loadCurrentIssues: func() ([]adoption.Change, error) {
			return adoption.LoadStanding(releasenotes.FS, notes.StandingPath)
		},
		loadProjectConfig: space.LoadProjectConfig,
		readFile:          os.ReadFile,
		writeFile:         os.WriteFile,
		execPath:          os.Executable,
	}
}

// Name implements cli.Command.
func (c *AdaptCommand) Name() string { return "adapt" }

// Synopsis implements cli.Command.
func (c *AdaptCommand) Synopsis() string {
	return "show only what obliges this repository since it was last adapted, and record when it is done (P13)"
}

// adaptJSONItem is one `--json` pending obligation.
type adaptJSONItem struct {
	Version string          `json:"version"`
	Group   int             `json:"group"`
	Change  adoption.Change `json:"change"`
}

// adaptJSON is `a2a adapt --json`'s machine-readable shape. Verdict
// carries the SAME true/false the exit code encodes (spec 13 §6 "--json
// carries the same verdict as the exit code") so a caller need not infer
// it from Count.
type adaptJSON struct {
	Baseline          string          `json:"baseline,omitempty"`
	BinaryVersion     string          `json:"binary_version"`
	Releases          int             `json:"releases"`
	StartedFromOldest bool            `json:"started_from_oldest,omitempty"`
	Oldest            string          `json:"oldest,omitempty"`
	Count             int             `json:"count"`
	Verdict           bool            `json:"obligations_remain"`
	Pending           []adaptJSONItem `json:"pending"`
}

// adaptDoneJSON is `a2a adapt --done --json`'s machine-readable shape.
type adaptDoneJSON struct {
	Recorded      string   `json:"recorded,omitempty"`
	RecordedAhead []string `json:"recorded_ahead,omitempty"`
	Total         int      `json:"total"`
	Verified      int      `json:"verified"`
	Refused       bool     `json:"refused"`
	Reason        string   `json:"reason,omitempty"`
	ChangeID      string   `json:"change_id,omitempty"`
	// TemplateOnly names the recorded obligations whose every detect: is a
	// template (`a2a submit <id>`), not a probe: never run, so recorded on
	// the agent's word.
	TemplateOnly []string `json:"template_only,omitempty"`
}

// Run implements cli.Command. Exit codes: 2 = usage error; 1 = corpus/
// config load failure, a refused baseline (AC-10), or --done's refusal
// (AC-7); 0 = success INCLUDING "nothing pending" (AC-9: zero only when no
// obligation remains; non-zero otherwise, so a caller that always exits 0
// is exactly the reader an agent may ignore). --require answers a different
// question and has its own codes; see runRequire.
func (c *AdaptCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("adapt", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	doneFlag := fs.Bool("done", false, "record adapted_through at this binary's version; refuses if a pending detect: still fires")
	jsonFlag := fs.Bool("json", false, "machine-readable output")
	requireFlag := fs.String("require", "", "exit 0 when this binary satisfies `version|change-id`: a version X.Y.Z it is not older than, or a change id it carries or this repository adopted ahead; exit 1 when not; writes nothing and ignores pending steps")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		adaptUsage(stdio)
		return 2
	}
	requireSet := false
	fs.Visit(func(f *flag.Flag) { requireSet = requireSet || f.Name == "require" })
	if requireSet {
		if *doneFlag || *jsonFlag {
			_, _ = fmt.Fprintln(stdio.Stderr, "adapt: --require cannot be combined with --done or --json")
			adaptUsage(stdio)
			return 2
		}
		return c.runRequire(*requireFlag, stdio)
	}

	all, err := c.load()
	if err != nil {
		return adaptRefuse(stdio,
			"adapt: load this binary's own embedded release-note corpus",
			err.Error(),
			"this is a defect in the a2a binary itself, not this repository — run `a2a version` for the exact build and file it against a2ahub",
		)
	}
	currentIssues, err := c.loadCurrentIssues()
	if err != nil {
		return adaptRefuse(stdio,
			"adapt: load this binary's own embedded known-issues corpus",
			err.Error(),
			"this is a defect in the a2a binary itself, not this repository — run `a2a version` for the exact build and file it against a2ahub",
		)
	}
	// Best-effort, same tolerant convention UpdateCommand uses: no `a2a
	// init` yet is "never adapted", not a fatal error.
	cfg, _ := c.loadProjectConfig(c.projectConfigPath)

	proj, err := adoption.Pending(all, currentIssues, adaptClock(cfg), c.binaryVersion, notes.Policy())
	if err != nil {
		if errors.Is(err, adoption.ErrClockAheadOfBinary) {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"adapt: adapted_through (v%s) is newer than the running binary (v%s) — refusing to walk backwards\n",
				cfg.AdaptedThrough, c.binaryVersion)
			return 1
		}
		return adaptRefuse(stdio,
			"adapt: compute the pending-obligation range from adapted_through and this binary's version",
			err.Error(),
			"check .a2a/config.yaml's adapted_through field is a plain x.y.z version, and confirm `a2a version` prints a comparable release version (not `dev`) — run `a2a init` if config.yaml is missing entirely",
		)
	}
	adaptSortItems(proj.Items)

	if *doneFlag {
		return c.runDone(ctx, proj, stdio, *jsonFlag)
	}

	if *jsonFlag {
		return adaptEncodeJSON(proj, stdio)
	}
	adaptRenderText(stdio, proj, c.binaryVersion)
	if len(proj.Items) == 0 {
		return 0
	}
	return 1
}

// adaptUsage prints the verb's usage and the docs topic that explains it.
func adaptUsage(stdio IO) {
	_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a adapt [--done] [--json] | a2a adapt --require <version|change-id>")
	_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-contract-change"))
}

// adaptClock is the repository's adoption clock as .a2a/config.yaml records
// it: adapted_through and the change ids adopted ahead of a release.
func adaptClock(cfg space.ProjectConfig) adoption.Clock {
	return adoption.Clock{Through: cfg.AdaptedThrough, Ahead: cfg.AdaptedAhead}
}

// runRequire implements `a2a adapt --require <arg>` (D6): an exit-code probe
// a script runs before it relies on something. arg is a version (X.Y.Z, an
// optional leading v) or a change id (RN-<digits>-<suffix>). Exit 0: this
// binary is not older than the version, or it carries the change in its own
// embedded corpus (dated or authored ahead) — and, either way, it is not
// older than this repository's adoption clock. Exit 1: not satisfied, with
// one stderr line naming this binary's version and path and what is missing.
// Exit 2: arg is neither.
//
// It is a question about the BINARY. A repository's adapted_ahead naming an
// id this binary does not carry is exactly the stale-binary case, so it
// never satisfies the change form. Pending adoption steps do not change the
// answer (plain `adapt` exits 1 while they remain), and it writes nothing.
// With no readable .a2a/config.yaml, or no adapted_through in it, the clock
// check is skipped and the version form needs no config at all. A build
// whose version is not comparable (`dev`) cannot show it is new enough, so
// the version form fails closed with exit 1; the clock check is skipped for
// it, because there is nothing to compare. Versions compare through the
// adoption unit, the one comparison adoption uses.
func (c *AdaptCommand) runRequire(arg string, stdio IO) int {
	exe := "a2a"
	if c.execPath != nil {
		if p, err := c.execPath(); err == nil {
			exe = p
		}
	}
	switch {
	case adaptIsRequireVersion(arg):
		want := strings.TrimPrefix(arg, "v")
		older, err := adoption.OlderThan(c.binaryVersion, want)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"adapt --require: this binary (a2a %s at %s) is not a release build, so it cannot show it is v%s or newer — install a released a2a\n",
				c.binaryVersion, exe, want)
			return 1
		}
		if older {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"adapt --require: this binary (a2a %s at %s) is older than the required v%s — update a2a, or check which a2a comes first in PATH\n",
				c.binaryVersion, exe, want)
			return 1
		}
	case adaptIsRequireChangeID(arg):
		all, err := c.load()
		if err != nil {
			return adaptRefuse(stdio,
				"adapt --require: load this binary's own embedded release-note corpus",
				err.Error(),
				"this is a defect in the a2a binary itself, not this repository — run `a2a version` for the exact build and file it against a2ahub",
			)
		}
		if !adaptCorpusCarries(all, arg) {
			_, _ = fmt.Fprintf(stdio.Stderr,
				"adapt --require: this binary (a2a %s at %s) does not carry change %s — update a2a, or check which a2a comes first in PATH\n",
				c.binaryVersion, exe, arg)
			return 1
		}
	default:
		_, _ = fmt.Fprintf(stdio.Stderr,
			"adapt --require: %q is neither a version (X.Y.Z) nor a change id (RN-<digits>-<suffix>, the `id` field `a2a whatsnew --json` and `a2a adapt --json` print)\n", arg)
		adaptUsage(stdio)
		return 2
	}
	// Best-effort, as plain adapt reads it: no config is no clock.
	cfg, err := c.loadProjectConfig(c.projectConfigPath)
	if err != nil || cfg.AdaptedThrough == "" {
		return 0
	}
	older, err := adoption.OlderThan(c.binaryVersion, cfg.AdaptedThrough)
	if err != nil {
		if _, binErr := adoption.OlderThan(c.binaryVersion, c.binaryVersion); binErr != nil {
			return 0 // a dev build: nothing to compare, the version form already judged it
		}
		// Fail closed: a clock this binary cannot read cannot show the
		// binary is not behind it.
		_, _ = fmt.Fprintf(stdio.Stderr,
			"adapt --require: this repository's adapted_through %q is not a version, so this binary (v%s at %s) cannot show it is not behind it — run `a2a doctor`\n",
			cfg.AdaptedThrough, c.binaryVersion, exe)
		return 1
	}
	if older {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"adapt --require: this repository adapted through v%s, newer than this binary (v%s at %s) — update a2a, or check which a2a comes first in PATH\n",
			cfg.AdaptedThrough, c.binaryVersion, exe)
		return 1
	}
	return 0
}

// adaptCorpusCarries reports whether any release in all carries change id.
func adaptCorpusCarries(all []adoption.Release, id string) bool {
	for _, rn := range all {
		for _, ch := range rn.Changes {
			if ch.ID == id {
				return true
			}
		}
	}
	return false
}

// adaptIsRequireVersion reports whether arg is X.Y.Z with an optional
// leading v: exactly three dot-separated runs of digits.
func adaptIsRequireVersion(arg string) bool {
	parts := strings.Split(strings.TrimPrefix(arg, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if !adaptAllOf(p, "0123456789") {
			return false
		}
	}
	return true
}

// adaptIsRequireChangeID reports whether arg is a release-note change id as
// the corpus spells them: "RN-", a run of digits, "-", and a run of letters
// and digits (RN-02703-4, RN-0270-9b).
func adaptIsRequireChangeID(arg string) bool {
	rest, ok := strings.CutPrefix(arg, "RN-")
	if !ok {
		return false
	}
	release, suffix, ok := strings.Cut(rest, "-")
	return ok && adaptAllOf(release, "0123456789") &&
		adaptAllOf(suffix, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
}

// adaptAllOf reports whether s is non-empty and made only of runes in set.
func adaptAllOf(s, set string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(set, r) {
			return false
		}
	}
	return true
}

// adaptOnboardingProcedure is D-044's first-clock procedure, worded for the
// repository's session: the evidence to read, where to write the value, and
// the one case where the running binary is the answer. It names no value it
// inferred.
func adaptOnboardingProcedure(binaryVersion string) string {
	return fmt.Sprintf("set `adapted_through:` in .a2a/config.yaml to the version that onboarded this repository (D-044): "+
		"the PROVENANCE stamp in the commit that first added the skill, or with no stamp the release before the one current on the day .a2a/config.yaml was first committed; "+
		"a repository onboarded by this binary takes %s. Then run `a2a adapt`, which lists only what came after it", binaryVersion)
}

// adaptSortItems orders items by Group (the adoption unit's own assignment)
// then, within a group, by whatsnewImpactOrder (cmd_whatsnew.go) — REUSED,
// not re-derived (P13 spec §5).
func adaptSortItems(items []adoption.PendingItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		return whatsnewImpactOrder(items[i].Change.Impact) < whatsnewImpactOrder(items[j].Change.Impact)
	})
}

// adaptEncodeJSON writes proj as adaptJSON and returns the matching exit
// code (AC-9's "--json carries the same verdict as the exit code").
func adaptEncodeJSON(proj adoption.Projection, stdio IO) int {
	out := adaptJSON{
		Baseline:          proj.Clock.Through,
		BinaryVersion:     proj.BinaryVersion,
		Releases:          proj.Releases,
		StartedFromOldest: proj.StartedFromOldest,
		Oldest:            proj.Oldest,
		Count:             len(proj.Items),
		Verdict:           len(proj.Items) > 0,
	}
	for _, item := range proj.Items {
		out.Pending = append(out.Pending, adaptJSONItem{Version: item.Version, Group: int(item.Group), Change: item.Change})
	}
	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return adaptRefuse(stdio,
			"adapt --json: encode the pending-obligation projection as JSON",
			err.Error(),
			"if this output was piped to another command, confirm it isn't closing the pipe before adapt finishes writing (e.g. avoid `| head`) — otherwise this is an a2a defect, file it",
		)
	}
	if len(proj.Items) == 0 {
		return 0
	}
	return 1
}

// adaptDirectiveHeader and adaptDirectiveBody are spec 13 §"The directive"
// verbatim (the imperative AC-3 requires) — a description of what changed
// is not an instruction to act on it.
const (
	adaptDirectiveHeader = "ADAPT THIS REPOSITORY TO THE CHANGES BELOW."
	adaptDirectiveBody   = "Ordered so the first item is always one you can do alone.\nWhen the work is done — and only then — run `a2a adapt --done`."
)

// adaptBaselineLabel is the version adaptRenderText's header names as
// "since v<label>" — the recorded baseline when one exists, otherwise the
// oldest embedded note the walk actually started from.
func adaptBaselineLabel(proj adoption.Projection) string {
	if proj.Clock.Through != "" {
		return proj.Clock.Through
	}
	return proj.Oldest
}

// adaptRenderText renders the human digest: the directive (AC-3 opens
// with it), each obligation in Pending/adaptSortItems' own order, and a
// closing line naming the outstanding count (AC-3 closes with it).
func adaptRenderText(stdio IO, proj adoption.Projection, binaryVersion string) {
	if len(proj.Items) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "a2a adapt: nothing to adapt — every change in range is scope: none.")
		return
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "a2a adapt — %d obligations since v%s (%d releases).\n\n",
		len(proj.Items), adaptBaselineLabel(proj), proj.Releases)
	if proj.StartedFromOldest {
		_, _ = fmt.Fprintf(stdio.Stdout,
			"this repository has never recorded adapted_through — walking from the oldest embedded release, v%s.\n%s\n\n",
			proj.Oldest, adaptOnboardingProcedure(binaryVersion))
	}
	_, _ = fmt.Fprintln(stdio.Stdout, adaptDirectiveHeader)
	_, _ = fmt.Fprintln(stdio.Stdout, adaptDirectiveBody)
	_, _ = fmt.Fprintln(stdio.Stdout)

	for i, item := range proj.Items {
		ch := item.Change
		label := ch.Impact
		if ch.Kind == adoption.KindKnownIssue {
			label = "KNOWN ISSUE"
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "[%d] (%s, v%s) %s\n", i+1, label, item.Version, ch.Subject)
		for _, line := range whatsnewWrapDetail(strings.TrimSpace(ch.Detail), "    ", whatsnewDetailWidth) {
			_, _ = fmt.Fprintln(stdio.Stdout, line)
		}
		if ch.Action.Why != "" {
			_, _ = fmt.Fprintf(stdio.Stdout, "    → why: %s\n", ch.Action.Why)
		}
		if len(ch.Action.Detect) > 0 {
			_, _ = fmt.Fprintf(stdio.Stdout, "      detect: %s\n", strings.Join(ch.Action.Detect, ", "))
		}
		if len(ch.Action.Run) > 0 {
			_, _ = fmt.Fprintf(stdio.Stdout, "      run: %s\n", strings.Join(ch.Action.Run, ", "))
		}
		_, _ = fmt.Fprintln(stdio.Stdout)
	}

	_, _ = fmt.Fprintf(stdio.Stdout,
		"%d obligations remain. Run `a2a adapt --done` when finished — it refuses if a detect: still fires.\n",
		len(proj.Items))
}

// runDone implements `a2a adapt --done` (AC-6, AC-7, AC-8): run every
// detect: in the pending set first and refuse NAMING the change if one
// still fires (or could not be run at all — never conflated, see
// adoption.DetectRunner's own doc); otherwise record adapted_through and say,
// honestly, how much of that record is verified.
func (c *AdaptCommand) runDone(ctx context.Context, proj adoption.Projection, stdio IO, jsonMode bool) int {
	// A never-clocked repository is refused before any detect: runs (D-044,
	// amending spec 13's "absent field walks from the oldest note" for
	// --done; the listing still walks). Its projection reaches back to
	// v0.2.0, and obligations from before onboarding describe states the
	// repository never had: measured 2026-11-08 on a clock-less devkit
	// clone, 11 of those detects fire there for that reason alone. Walking
	// them can only refuse with a misleading change id. The session sets
	// the first clock from onboarding evidence; the tool names how, and
	// never picks the value.
	if proj.StartedFromOldest && len(proj.Items) > 0 {
		if jsonMode {
			return adaptEncodeDoneJSON(stdio, adaptDoneJSON{
				Total: len(proj.Items), Refused: true,
				Reason: "this repository has never recorded adapted_through; " + adaptOnboardingProcedure(c.binaryVersion),
			})
		}
		return adaptRefuse(stdio,
			"adapt --done: record adapted_through at .a2a/config.yaml",
			"this repository has never recorded adapted_through, so the pending range reaches back to the oldest embedded release",
			adaptOnboardingProcedure(c.binaryVersion),
		)
	}
	if result := adoption.CheckDetects(ctx, proj.Items, c.detectRun); result != nil {
		if result.Err != nil {
			if jsonMode {
				return adaptEncodeDoneJSON(stdio, adaptDoneJSON{
					Total: len(proj.Items), Refused: true, ChangeID: result.Item.Change.ID,
					Reason: fmt.Sprintf("detect could not be run: %v", result.Err),
				})
			}
			_, _ = fmt.Fprintf(stdio.Stderr,
				"adapt --done: %s's detect (%q) could not be run: %v — refusing to record (a check that cannot be measured is never treated as clean)\n",
				result.Item.Change.ID, result.Command, result.Err)
			return 1
		}
		if jsonMode {
			return adaptEncodeDoneJSON(stdio, adaptDoneJSON{
				Total: len(proj.Items), Refused: true, ChangeID: result.Item.Change.ID,
				Reason: "detect still fires",
			})
		}
		_, _ = fmt.Fprintf(stdio.Stderr,
			"adapt --done: %s's detect (%q) still fires — refusing to record; this repository has not adapted to it yet\n",
			result.Item.Change.ID, result.Command)
		return 1
	}

	templateOnly := adoption.TemplateOnly(proj.Items)
	verified := 0
	for _, item := range proj.Items {
		if len(item.Change.Action.Detect) > 0 {
			verified++
		}
	}
	verified -= len(templateOnly)
	total := len(proj.Items)

	if err := adaptSaveAdaptedThrough(c.readFile, c.writeFile, c.projectConfigPath, c.binaryVersion, proj.AuthoredAhead); err != nil {
		if jsonMode {
			return adaptEncodeDoneJSON(stdio, adaptDoneJSON{Total: total, Verified: verified, Refused: true, Reason: err.Error()})
		}
		return adaptRefuse(stdio,
			"adapt --done: record adapted_through at .a2a/config.yaml",
			err.Error(),
			"confirm .a2a/config.yaml exists, is valid YAML with a mapping at its root, and is writable — run `a2a init` first if it is missing",
		)
	}

	if jsonMode {
		return adaptEncodeDoneJSON(stdio, adaptDoneJSON{Recorded: c.binaryVersion, RecordedAhead: proj.AuthoredAhead, Total: total, Verified: verified, TemplateOnly: templateOnly})
	}

	switch {
	case total == 0:
		_, _ = fmt.Fprintf(stdio.Stdout, "a2a adapt --done: nothing pending — recorded adapted_through=v%s\n", c.binaryVersion)
	case verified == 0:
		_, _ = fmt.Fprintf(stdio.Stdout,
			"a2a adapt --done: recorded adapted_through=v%s — UNVERIFIED: none of the %d obligations just recorded carry a runnable detect:; this is recorded on the agent's word, not machine-checked\n",
			c.binaryVersion, total)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout,
			"a2a adapt --done: recorded adapted_through=v%s — %d/%d verified via detect:, %d recorded on the agent's word (no detect:)\n",
			c.binaryVersion, verified, total, total-verified)
	}
	if len(templateOnly) > 0 {
		_, _ = fmt.Fprintf(stdio.Stdout,
			"a2a adapt --done: %d of those carry only a template detect: (a placeholder such as <id>, not a probe), so they were not run: %s\n",
			len(templateOnly), strings.Join(templateOnly, ", "))
	}
	if n := len(proj.AuthoredAhead); n > 0 {
		_, _ = fmt.Fprintf(stdio.Stdout,
			"a2a adapt --done: %d change(s) from notes authored ahead of v%s recorded in adapted_ahead\n",
			n, c.binaryVersion)
	}
	return 0
}

func adaptEncodeDoneJSON(stdio IO, out adaptDoneJSON) int {
	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return adaptRefuse(stdio,
			"adapt --done --json: encode the done result as JSON",
			err.Error(),
			"if this output was piped to another command, confirm it isn't closing the pipe before adapt --done finishes writing (e.g. avoid `| head`) — otherwise this is an a2a defect, file it",
		)
	}
	if out.Refused {
		return 1
	}
	return 0
}

// adaptRefuse builds a three-part Refusal (attempted/found/nextStep,
// internal/cli/refusal_state.go's NewRefusal — P4, answers-that-hold-2026-08
// spec 04) and writes it to stdio.Stderr, returning 1 — the shared tail
// every err-to-stderr site in this file uses. nextStep is always a fixed,
// non-empty literal at every call site here, so NewRefusal's own
// construction-time refusal is unreachable in practice; the fallback below
// is a structural safety net (the same shape runValidateCI's own migrated
// site already uses in cmd_validate_ci.go), never a trusted-forever
// assumption.
func adaptRefuse(stdio IO, attempted, found, nextStep string) int {
	refusal, rerr := NewRefusal(attempted, found, nextStep)
	if rerr != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "adapt: internal error building a refusal (empty next step) — this is a bug in cmd_adapt.go, file one")
		return 1
	}
	_, _ = fmt.Fprintln(stdio.Stderr, refusal)
	return 1
}

// adaptSaveAdaptedThrough writes ONLY the adapted_through and adapted_ahead
// keys into path's existing YAML, preserving every other key, comment and
// ordering — adapted_ahead is dropped when ahead is empty, since the release
// that dated those notes put them under adapted_through —
// space.marshalMachineConfigPreservingExisting's (internal/space/
// config.go) single-key yaml.Node update technique, copied here rather
// than widened into a general ProjectConfig save path: this phase's
// allowlist grants exactly one field on config.go, not a new save
// function on it. A committed, team-shared file (.a2a/config.yaml) loses
// nothing a teammate wrote by hand just because one agent ran `--done`.
func adaptSaveAdaptedThrough(
	readFile func(string) ([]byte, error),
	writeFile func(string, []byte, os.FileMode) error,
	path string,
	version string,
	ahead []string,
) error {
	existing, err := readFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return fmt.Errorf("%s does not parse as yaml: %w", path, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: root must be a mapping", path)
	}

	root := doc.Content[0]
	adaptSetKey(root, "adapted_through", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: version})
	aheadNode := (*yaml.Node)(nil)
	if len(ahead) > 0 {
		aheadNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, id := range ahead {
			aheadNode.Content = append(aheadNode.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: id})
		}
	}
	adaptSetKey(root, "adapted_ahead", aheadNode)

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeFile(path, out.Bytes(), 0o644)
}

// adaptSetKey replaces key's value in the mapping root, appends it when
// absent, and removes the pair when value is nil.
func adaptSetKey(root *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != key {
			continue
		}
		if value == nil {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return
		}
		root.Content[i+1] = value
		return
	}
	if value != nil {
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
}

var _ Command = (*AdaptCommand)(nil)
