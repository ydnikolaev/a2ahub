package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// VersionCommand implements `a2a version` (and the `--version` / `-v`
// aliases).
//
// It used to print one line — the binary stamp — and that was the whole
// surface a consumer had for "which versions am I actually running?". Three
// axes decide that question and only one of them was visible:
//
//	binary    what this executable is
//	release   what the newest published release is
//	space     what each connected space DECLARES it needs, and which a2ahub
//	          template tag it PINS
//
// On 2026-08-21 an agent wrote to a space from a 0.23 binary while 0.24 was
// published. Nothing was broken: the space's floor was 0.23.0, so the write
// was legitimate. The floor had simply not moved, because the space had not
// been updated — and no surface said either thing. This command is the answer
// to "how would anyone have known".
//
// It deliberately makes NO network call. The release axis renders whatever the
// background check already cached; with no cache it says so in those words
// rather than implying "you are up to date".
type VersionCommand struct {
	// Stamp is the one-line "a2a <version> (<commit>)" the binary already
	// composes; passed in rather than recomputed so there is one stamp.
	Stamp string
	// BinaryVersion is the bare semver the floor comparisons use.
	BinaryVersion string
	// Store is optional BY DESIGN: `a2a version` must answer outside a
	// project, with no config on disk, which is why the dispatch table has
	// always built it without dependencies. A nil store means the space and
	// release axes are UNAVAILABLE, and the output says that — it does not
	// print an empty table that reads like "no spaces".
	Store *cache.Store
	// Now is injected (rails: no buried clock) so the "checked N ago" phrase
	// is testable.
	Now func() time.Time
}

// Run renders the report. Exit codes: 0 always — this is a report, not a
// gate. `a2a doctor` is where these facts become findings that can red.
func (c *VersionCommand) Run(_ context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "emit the report as JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if c.Store == nil {
		if *jsonOut {
			return c.emitJSON(stdio, cache.VersionReport{Binary: c.BinaryVersion, Spaces: []cache.SpaceVersion{}})
		}
		_, _ = fmt.Fprintln(stdio.Stdout, c.Stamp)
		_, _ = fmt.Fprintln(stdio.Stderr)
		_, _ = fmt.Fprintln(stdio.Stderr, "spaces and releases: UNAVAILABLE here — no a2a project was resolved from this directory.")
		_, _ = fmt.Fprintln(stdio.Stderr, "                     run this inside a project, or `a2a init` to create one.")
		return 0
	}

	report := c.Store.VersionReport(c.BinaryVersion, spaceWorkflowPin)
	if *jsonOut {
		return c.emitJSON(stdio, report)
	}
	c.render(stdio, report)
	return 0
}

func (c *VersionCommand) emitJSON(stdio IO, report cache.VersionReport) int {
	enc := json.NewEncoder(stdio.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a version: cannot encode JSON output: %v\n", err)
		return 1
	}
	return 0
}

// render writes the STAMP LINE, and only the stamp line, to stdout; the three
// axes go to stderr.
//
// THIS IS A COMPATIBILITY CONTRACT, not a formatting preference. Every
// already-installed a2a self-checks a downloaded upgrade by running its
// `version` verb and matching stdout — the WHOLE of stdout — against
// `^a2a <version> (<sha>)$`. That parser is frozen inside binaries already on
// people's machines and cannot be fixed retroactively. v0.25.0 put the report
// on stdout and thereby broke `a2a update` for every one of them: the download
// succeeded, the self-check said "unparseable version stamp", and the upgrade
// rolled back. Discovered by running `a2a update` on 2026-08-22, from 0.24.0,
// which is the first time anyone had.
//
// Stdout is therefore the machine contract and stderr is the human report —
// the same split `a2a html --timing` and the update advisory already use, and
// `--json` still puts the full structured report on stdout for callers that
// want the axes. TestVersionStdoutIsTheFrozenStampContract holds it.
func (c *VersionCommand) render(stdio IO, report cache.VersionReport) {
	_, _ = fmt.Fprintln(stdio.Stdout, c.Stamp)
	out := stdio.Stderr
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprintf(out, "binary    %s\n", report.Binary)

	// THE UNMEASURED CASE IS ITS OWN SENTENCE, TWICE OVER. "latest 0.24.0" when
	// 0.24.0 is simply the last thing anyone happened to cache is a different
	// claim from "0.24.0 is the newest release"; and a binary stamped `dev`
	// cannot be compared to any release at all, so calling it "up to date" —
	// which the first draft of this command did — announces a verdict nobody
	// measured. An unparseable version is "cannot verify", never "fine"; that
	// is internal/version.ErrInvalidVersion's own stated contract.
	switch {
	case !comparableVersion(report.Binary):
		_, _ = fmt.Fprintf(out, "latest    %s known, but this binary is stamped %q — a development build carries no release\n", orUnknown(report.Update.Latest), report.Binary)
		_, _ = fmt.Fprintln(out, "          version, so the release axis CANNOT be judged here. Install a released build to compare.")
	case report.Update.Latest == "":
		_, _ = fmt.Fprintln(out, "latest    UNKNOWN — no release check has run on this machine yet.")
		_, _ = fmt.Fprintln(out, "          run `a2a update --check` to ask now; this command never reaches the network itself.")
	case report.Update.UpdateAvailable:
		_, _ = fmt.Fprintf(out, "latest    %s   UPDATE AVAILABLE%s\n", report.Update.Latest, c.checkedPhrase(report.Update))
	default:
		_, _ = fmt.Fprintf(out, "latest    %s   up to date%s\n", report.Update.Latest, c.checkedPhrase(report.Update))
	}

	_, _ = fmt.Fprintln(out)
	if len(report.Spaces) == 0 {
		_, _ = fmt.Fprintln(out, "spaces    none connected — `a2a connect <repo>` to add one.")
	} else {
		_, _ = fmt.Fprintln(out, "spaces")
		width := 0
		for _, sp := range report.Spaces {
			if len(sp.ID) > width {
				width = len(sp.ID)
			}
		}
		for _, sp := range report.Spaces {
			_, _ = fmt.Fprintf(out, "  %-*s  %s   %s   %s\n", width, sp.ID, floorPhrase(sp), templatePhrase(sp), mirrorPhrase(sp))
		}
	}

	var actions []string
	if report.Update.Required {
		actions = append(actions, fmt.Sprintf("a2a update    — REQUIRED: space %q declares min_binary_version %s and this binary is older; writes to it are refused", report.Update.FloorSpace, report.Update.Floor))
	} else if report.Update.UpdateAvailable {
		actions = append(actions, fmt.Sprintf("a2a update    — install %s", report.Update.Latest))
	}
	for _, sp := range report.Spaces {
		if !sp.TemplateBehind {
			continue
		}
		// A STALE MIRROR CANNOT TELL "BEHIND" FROM "ALREADY UPDATED", so it
		// must not name the remedy for the first one. On 2026-08-23 this line
		// told an operator to run `a2a space update` immediately after they
		// had run it and merged its PR: the update had landed, the mirror had
		// not been fetched since, and the row was a fact about a snapshot.
		// Syncing is what turns the snapshot into an answer; only then is
		// "behind" a claim worth acting on.
		if sp.Stale {
			actions = append(actions, fmt.Sprintf("a2a sync    — %q looks behind, but this mirror %s, so that is a fact about a snapshot; sync, then read this row again", sp.ID, mirrorAgePhrase(sp)))
			continue
		}
		actions = append(actions, fmt.Sprintf("a2a space update    — %q pins the a2ahub template at v%s, and v%s is published", sp.ID, sp.TemplatePin, report.Update.Latest))
	}
	if len(actions) > 0 {
		_, _ = fmt.Fprintln(out)
		for _, a := range actions {
			_, _ = fmt.Fprintf(out, "  %s\n", a)
		}
	}
}

// mirrorPhrase is the fourth column, and it exists because the other three are
// read from a local mirror that `a2a version` deliberately never refreshes.
// Without it the row says "BEHIND" with the same confidence whether the mirror
// was fetched a second ago or a week ago.
func mirrorPhrase(sp cache.SpaceVersion) string {
	if !sp.Synced {
		return "mirror NEVER SYNCED"
	}
	if sp.Stale {
		return "mirror " + humanizeMirrorAge(sp.SyncAge) + " old — STALE"
	}
	return "mirror " + humanizeMirrorAge(sp.SyncAge) + " old"
}

// mirrorAgePhrase is the same fact in a sentence, for the action lines. The
// age is computed once, in the store, next to the mirror it describes — the
// renderer needs no clock of its own.
func mirrorAgePhrase(sp cache.SpaceVersion) string {
	if !sp.Synced {
		return "has never been synced"
	}
	return "was last synced " + humanizeMirrorAge(sp.SyncAge) + " ago"
}

func humanizeMirrorAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// comparableVersion reports whether v can be compared to a release at all.
// `dev` (the ldflags-free default) and anything else unparseable cannot, and
// the caller must SAY so rather than fold it into a verdict.
func comparableVersion(v string) bool {
	_, err := version.OlderThan(v, "0.0.0")
	return err == nil
}

func orUnknown(s string) string {
	if s == "" {
		return "no release is"
	}
	return s + " is"
}

// checkedPhrase says WHEN the release fact was learned, because a cached
// "latest" with no timestamp is indistinguishable from a fresh one.
func (c *VersionCommand) checkedPhrase(n cache.UpdateNotice) string {
	if n.CheckedAt.IsZero() {
		return ""
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	age := now().Sub(n.CheckedAt)
	src := n.Source
	if src == "" {
		src = "unknown source"
	}
	return fmt.Sprintf("   (checked %s ago, %s)", roundAge(age), src)
}

func roundAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func floorPhrase(sp cache.SpaceVersion) string {
	if sp.Floor == "" {
		return "floor undeclared"
	}
	if !sp.FloorMet {
		return fmt.Sprintf("floor %s  UNMET — writes refused", sp.Floor)
	}
	return fmt.Sprintf("floor %s  ok", sp.Floor)
}

func templatePhrase(sp cache.SpaceVersion) string {
	switch {
	case sp.TemplatePin == "":
		return "template unknown"
	case sp.TemplatePin == "mixed":
		return "template MIXED — this space's jobs pin different tags"
	case sp.TemplateBehind:
		return fmt.Sprintf("template v%s  BEHIND", sp.TemplatePin)
	default:
		return fmt.Sprintf("template v%s  ok", sp.TemplatePin)
	}
}

// spaceWorkflowPin is the injection point Store.VersionReport takes. The
// indirection is not ceremony: internal/cache must not grow a dependency on how
// a workflow file is spelled, and a test can hand VersionReport a fixed pin
// with no checkout on disk.
var spaceWorkflowPin = space.WorkflowVersion
