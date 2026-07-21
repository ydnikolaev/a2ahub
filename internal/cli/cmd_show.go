// OP-209 `a2a show` (spec 07 T1). This file's only package-level
// symbols are ShowCommand + NewShowCommand plus its own uniquely-named,
// file-private helpers (show* prefix) — no shared helper, no package
// var, per this phase's plan Placement decision.
//
// This is the ONE P7 verb file that imports internal/validate (the plan
// Placement decision, binding): internal/cache stays validate-free per
// ADR-001 and only supplies digest/staleness FACTS (cache.RefFact,
// ShowResult.SyncStale) — the V5 registry-code lookup itself happens
// here, mapping those facts onto the SAME codes internal/validate's V2
// path already emits (REF-004/REF-008, schemas/errors/v1/registry.yaml),
// never a second, divergent code.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// showOutput is `a2a show`'s JSON shape: cache's own ShowResult plus the
// V5 warnings this file derives from it.
type showOutput struct {
	cache.ShowResult
	Warnings []validate.Violation `json:"warnings,omitempty"`
}

// ShowCommand implements `a2a show <ref>` (OP-209): artifact body +
// folded state + event list + any V5 digest/staleness warning, never a
// hard error for a resolvable ref — only ref-not-found is an error.
type ShowCommand struct {
	store *cache.Store
}

// NewShowCommand constructs the show command. store must not be nil
// (rails anti-pattern #10).
func NewShowCommand(store *cache.Store) *ShowCommand {
	return &ShowCommand{store: store}
}

// Name implements cli.Command.
func (c *ShowCommand) Name() string { return "show" }

// Synopsis implements cli.Command.
func (c *ShowCommand) Synopsis() string {
	return "show an artifact's body, folded state, events, and any V5 digest/staleness warning"
}

// Run implements cli.Command. Exit codes: 2 = usage; 1 = ref not found
// or a connected space's mirror could not be read; 0 = success — a V5
// warning is present in the output but NEVER flips this to non-zero
// (OP-209: "never blocks").
func (c *ShowCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	jsonOut := fs.Bool("json", false, "JSON output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a show <ref>")
		return 2
	}
	ref := fs.Arg(0)

	result, err := c.store.Show(ctx, ref)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "show: %v\n", err)
		return 1
	}

	out := showOutput{ShowResult: result, Warnings: showV5Warnings(result)}
	if *jsonOut {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "show: cannot encode JSON output: %v\n", err)
			return 1
		}
		return 0
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "%s %s (%s) — %s\n", result.ID, result.Type, result.State, result.Title)
	_, _ = fmt.Fprintln(stdio.Stdout, result.Body)
	for _, w := range out.Warnings {
		code := w.Code
		if code == "" {
			code = "-"
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "warning: [%s] %s\n", code, w.Message)
	}
	return 0
}

// showV5Warnings maps cache's own digest/staleness FACTS (cache.RefFact,
// ShowResult.SyncStale — cache never mints a registry code, per the
// plan's binding Placement decision) to the V5 registry code:
//
//   - REF-004 (schemas/errors/v1/registry.yaml: "a pinned ref's digest
//     does not match its resolved target") when a pinned ref resolved
//     but its digest mismatches — the SAME code V2 emits for this exact
//     condition (internal/validate/referential.go), reused verbatim, at
//     SeverityWarning (V5 never blocks, unlike V2's reject).
//   - REF-008 ("a digest-pinned ref's target could not be resolved to
//     verify") when a pinned ref could not be resolved at all.
//
// General mirror staleness (sync-age > the statusline TTL) has NO
// registry code: schemas/errors/v1/registry.yaml (off this phase's
// allowlist) carries none for it, and minting one is a lead-level schema
// change this file cannot make. It is surfaced as a warning with an
// empty Code rather than a fabricated one — see this phase's Deviations
// report.
func showV5Warnings(result cache.ShowResult) []validate.Violation {
	var out []validate.Violation
	for _, rf := range result.Refs {
		switch {
		case rf.DigestMismatch:
			out = append(out, validate.Violation{
				Code: "REF-004", Class: validate.ClassReferential, Path: "refs",
				Message:  fmt.Sprintf("V5: pinned ref %s digest does not match the resolved target", rf.Ref),
				Severity: validate.SeverityWarning,
			})
		case rf.PinnedDigest != "" && !rf.Resolved:
			out = append(out, validate.Violation{
				Code: "REF-008", Class: validate.ClassReferential, Path: "refs",
				Message:  fmt.Sprintf("V5: pinned ref %s could not be resolved to verify", rf.Ref),
				Severity: validate.SeverityWarning,
			})
		}
	}
	if result.SyncStale {
		out = append(out, validate.Violation{
			Class:    validate.ClassReferential,
			Message:  fmt.Sprintf("V5: this space's mirror sync-age (%s) exceeds the refresh TTL; data may be stale", result.SyncAge),
			Severity: validate.SeverityWarning,
		})
	}
	return out
}

var _ Command = (*ShowCommand)(nil)
