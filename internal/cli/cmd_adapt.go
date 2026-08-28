// P13 `a2a adapt` (spec docs/features/active/answers-that-hold-2026-08/
// specs/13-adapt-from-a-baseline.md): a filtered, ordered, imperative
// projection of the release-note corpus down to what OBLIGES the reader —
// `action.scope != "none"`, already labelled by the corpus itself
// (internal/notes.Change.Action; this file classifies nothing) — walked
// from the REPOSITORY's own `adapted_through` baseline (internal/space.
// ProjectConfig), never from the binary's own version history (the "two
// clocks" the spec's own title names). This file's only package-level
// symbols are AdaptCommand + NewAdaptCommand plus its own uniquely-named,
// file-private helpers (adapt* prefix) — no shared helper, no package var,
// per this package's established Placement convention (cmd_whatsnew.go,
// cmd_update.go).
//
// NOT WIRED into cmd/a2a/wire.go/catalog.go/help.go — that three-line
// registration is lead-owned (P13 plan allowlist); this file is
// constructible and independently unit-testable without it.
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
	"gopkg.in/yaml.v3"
)

// AdaptCommand implements `a2a adapt [--done] [--json]` (P13 T1): a
// thin flags-in/JSON-or-text-out wrapper over internal/notes.Pending —
// zero business rules live here (ADR-001 "thin frontend"); the group
// assignment lives in internal/notes, and the one thing this file DOES
// decide is the within-group tie-break, because it reuses
// cmd_whatsnew.go's whatsnewImpactOrder (internal/notes cannot import this
// package — dependency direction — so the final sort happens here).
type AdaptCommand struct {
	binaryVersion     string
	projectConfigPath string

	load              func() ([]notes.ReleaseNotes, error)
	loadCurrentIssues func() ([]notes.Change, error)
	loadProjectConfig func(path string) (space.ProjectConfig, error)

	// readFile/writeFile back adaptSaveAdaptedThrough's single-key YAML
	// update (below) — the same os.ReadFile/os.WriteFile DI seam
	// cmd_update.go and cmd_init.go already use, so tests drive --done's
	// write path without touching a real file.
	readFile  func(path string) ([]byte, error)
	writeFile func(path string, data []byte, perm os.FileMode) error

	// detectRun executes one detect: command for `--done` (AC-7). nil
	// defaults to notes.DefaultDetectRunner (a real shell); tests inject a
	// fake so the corpus's own prose/placeholder detect strings (e.g.
	// "a2a submit <id>") are never actually exec'd by a test run.
	detectRun notes.DetectRunner
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
		load:              func() ([]notes.ReleaseNotes, error) { return notes.Load(releasenotes.FS) },
		loadCurrentIssues: func() ([]notes.Change, error) {
			return notes.LoadCurrentKnownIssues(releasenotes.FS)
		},
		loadProjectConfig: space.LoadProjectConfig,
		readFile:          os.ReadFile,
		writeFile:         os.WriteFile,
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
	Version string       `json:"version"`
	Group   int          `json:"group"`
	Change  notes.Change `json:"change"`
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
	Recorded string `json:"recorded,omitempty"`
	Total    int    `json:"total"`
	Verified int    `json:"verified"`
	Refused  bool   `json:"refused"`
	Reason   string `json:"reason,omitempty"`
	ChangeID string `json:"change_id,omitempty"`
}

// Run implements cli.Command. Exit codes: 2 = usage error; 1 = corpus/
// config load failure, a refused baseline (AC-10), or --done's refusal
// (AC-7); 0 = success INCLUDING "nothing pending" (AC-9: zero only when no
// obligation remains; non-zero otherwise, so a caller that always exits 0
// is exactly the reader an agent may ignore).
func (c *AdaptCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("adapt", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	doneFlag := fs.Bool("done", false, "record adapted_through at this binary's version; refuses if a pending detect: still fires")
	jsonFlag := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a adapt [--done] [--json]")
		return 2
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

	proj, err := notes.Pending(all, currentIssues, cfg.AdaptedThrough, c.binaryVersion)
	if err != nil {
		if errors.Is(err, notes.ErrBaselineAheadOfBinary) {
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
	adaptRenderText(stdio, proj)
	if len(proj.Items) == 0 {
		return 0
	}
	return 1
}

// adaptSortItems orders items by Group (internal/notes' own assignment)
// then, within a group, by whatsnewImpactOrder (cmd_whatsnew.go) — REUSED,
// not re-derived (P13 spec §5).
func adaptSortItems(items []notes.PendingItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Group != items[j].Group {
			return items[i].Group < items[j].Group
		}
		return whatsnewImpactOrder(items[i].Change.Impact) < whatsnewImpactOrder(items[j].Change.Impact)
	})
}

// adaptEncodeJSON writes proj as adaptJSON and returns the matching exit
// code (AC-9's "--json carries the same verdict as the exit code").
func adaptEncodeJSON(proj notes.Projection, stdio IO) int {
	out := adaptJSON{
		Baseline:          proj.Baseline,
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
func adaptBaselineLabel(proj notes.Projection) string {
	if proj.Baseline != "" {
		return proj.Baseline
	}
	return proj.Oldest
}

// adaptRenderText renders the human digest: the directive (AC-3 opens
// with it), each obligation in Pending/adaptSortItems' own order, and a
// closing line naming the outstanding count (AC-3 closes with it).
func adaptRenderText(stdio IO, proj notes.Projection) {
	if len(proj.Items) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "a2a adapt: nothing to adapt — every change in range is scope: none.")
		return
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "a2a adapt — %d obligations since v%s (%d releases).\n\n",
		len(proj.Items), adaptBaselineLabel(proj), proj.Releases)
	if proj.StartedFromOldest {
		_, _ = fmt.Fprintf(stdio.Stdout,
			"this repository has never recorded adapted_through — walking from the oldest embedded release, v%s.\n\n",
			proj.Oldest)
	}
	_, _ = fmt.Fprintln(stdio.Stdout, adaptDirectiveHeader)
	_, _ = fmt.Fprintln(stdio.Stdout, adaptDirectiveBody)
	_, _ = fmt.Fprintln(stdio.Stdout)

	for i, item := range proj.Items {
		ch := item.Change
		label := ch.Impact
		if ch.Kind == notes.KindKnownIssue {
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
// notes.DetectRunner's own doc); otherwise record adapted_through and say,
// honestly, how much of that record is verified.
func (c *AdaptCommand) runDone(ctx context.Context, proj notes.Projection, stdio IO, jsonMode bool) int {
	if result := notes.CheckDetects(ctx, proj.Items, c.detectRun); result != nil {
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

	verified := 0
	for _, item := range proj.Items {
		if len(item.Change.Action.Detect) > 0 {
			verified++
		}
	}
	total := len(proj.Items)

	if err := adaptSaveAdaptedThrough(c.readFile, c.writeFile, c.projectConfigPath, c.binaryVersion); err != nil {
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
		return adaptEncodeDoneJSON(stdio, adaptDoneJSON{Recorded: c.binaryVersion, Total: total, Verified: verified})
	}

	switch {
	case total == 0:
		_, _ = fmt.Fprintf(stdio.Stdout, "a2a adapt --done: nothing pending — recorded adapted_through=v%s\n", c.binaryVersion)
	case verified == 0:
		_, _ = fmt.Fprintf(stdio.Stdout,
			"a2a adapt --done: recorded adapted_through=v%s — UNVERIFIED: none of the %d obligations just recorded carry a detect:; this is recorded on the agent's word, not machine-checked\n",
			c.binaryVersion, total)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout,
			"a2a adapt --done: recorded adapted_through=v%s — %d/%d verified via detect:, %d recorded on the agent's word (no detect:)\n",
			c.binaryVersion, verified, total, total-verified)
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

// adaptSaveAdaptedThrough writes ONLY the adapted_through key into path's
// existing YAML, preserving every other key, comment and ordering —
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
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: version}
	replaced := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "adapted_through" {
			root.Content[i+1] = value
			replaced = true
			break
		}
	}
	if !replaced {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "adapted_through"},
			value,
		)
	}

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

var _ Command = (*AdaptCommand)(nil)
