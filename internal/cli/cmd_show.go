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
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/datapackage"
	"github.com/ydnikolaev/a2ahub/internal/html"
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
	// Wave K fix (live run 6): `a2a show <id> --json` exited 2 with
	// "usage: a2a show <ref>" — Go's flag package stops parsing at the
	// first non-flag token, so --json was counted as a SECOND positional
	// and fs.NArg() != 1. parseArgsAnyOrder (cli.go) accepts both orders.
	positional, err := parseArgsAnyOrder(fs, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a show <ref>")
		return 2
	}
	ref := positional[0]

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
	if result.Thread != "" {
		// Printed as a runnable next step, not as a bare field: an agent
		// holding one artifact id otherwise has no way to reach the
		// conversation it belongs to (spec 46 D6), and naming the command
		// is what turns "here is a value" into "here is your next move".
		_, _ = fmt.Fprintf(stdio.Stdout, "thread: %s (a2a thread %s)\n", result.Thread, result.Thread)
	}
	_, _ = fmt.Fprintln(stdio.Stdout, result.Body)
	for _, w := range out.Warnings {
		code := w.Code
		if code == "" {
			code = "-"
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "warning: [%s] %s\n", code, w.Message)
	}
	// Guard (top-level brief's D4): an artifact carrying no attachments[]
	// prints exactly what it printed before this wave — nothing is added
	// when the envelope has none, so an ordinary delivery's rendering is
	// unchanged.
	for _, line := range ShowAttachmentLines(decodeShowAttachments(result.Envelope), time.Now()) {
		_, _ = fmt.Fprintln(stdio.Stdout, line)
	}
	return 0
}

// decodeShowAttachments reads envelope's `attachments` array — the same
// key schemas/envelope/v2/work_request.schema.json:78-114 declares — off
// result.Envelope (cache.ShowResult's own untyped frontmatter projection,
// populated on both the CLI and dashboard `Show`/`ShowMany` paths so this
// reads the SAME data the dashboard will project once a later,
// template-allowlisted wave wires it in).
//
// entry.ExpiresAt is always "" here: a committed artifact's frontmatter
// never carries attachments[].expires_at (cmd_attach.go's own doc
// comment — the schema declares no such property, additionalProperties:
// false), so this decode alone cannot answer AC5's "has this lapsed"
// question for a real, on-disk attachment. See this wave's own deviations
// report: html.ProjectAttachmentClaim's Lapsed branch is proven by
// cmd_show_attachment_test.go directly, against a resolved
// AttachmentManifestEntry, not through this decode path — closing that
// gap end-to-end needs a schema property or a side-car manifest, both
// outside this file's allowlist.
func decodeShowAttachments(envelope map[string]any) []datapackage.AttachmentManifestEntry {
	raw, ok := envelope["attachments"]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]datapackage.AttachmentManifestEntry, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, datapackage.AttachmentManifestEntry{
			Attachment: datapackage.Attachment{
				Ref:          showAttachmentStringField(m, "ref"),
				Digest:       showAttachmentStringField(m, "digest"),
				Role:         showAttachmentStringField(m, "role"),
				ConformsTo:   showAttachmentStringField(m, "conforms_to"),
				Verification: showAttachmentStringField(m, "verification"),
				Retention:    showAttachmentStringField(m, "retention"),
			},
		})
	}
	return out
}

func showAttachmentStringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

// ShowAttachmentLines renders each attachment's claim via
// html.ProjectAttachmentClaim — the SAME projection the dashboard surface
// uses (internal/html/delivery.go) — so `a2a show` and the dashboard say
// the SAME thing about the same attachment (top-level brief: "one
// artifact, one answer"). This file invents no second vocabulary for
// verification:none or a lapsed retention.
//
// Exported (rather than kept file-private like this file's other helpers)
// specifically so cmd_show_attachment_test.go can assert AC5's lapse
// rendering directly against a resolved AttachmentManifestEntry: Run's own
// only caller (decodeShowAttachments) can never construct one with a
// non-empty ExpiresAt from a committed artifact today (see that function's
// own doc comment), so a black-box, Run()-only test could never reach the
// Lapsed branch this function also has to render correctly.
func ShowAttachmentLines(entries []datapackage.AttachmentManifestEntry, now time.Time) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		claim := html.ProjectAttachmentClaim(entry, now)
		line := fmt.Sprintf("attachment %s: %s", entry.Ref, claim.VerificationClaim)
		if claim.Lapsed {
			line += "; " + claim.LapseClaim
		}
		lines = append(lines, line)
	}
	return lines
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
