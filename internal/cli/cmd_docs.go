// P9 `a2a docs [topic]` (answers-that-hold-2026-08 spec 09
// docs/features/active/answers-that-hold-2026-08/specs/09-a-doc-page-is-
// reachable.md): a reader over the embedded skill-tree documentation
// manifest. Bare invocation lists every topic (id + title, grouped in
// manifest group order); a topic argument prints that section's page
// verbatim; an unknown topic refuses and names the vocabulary it actually
// holds.
//
// THE ONE RULE THIS FILE EXISTS TO KEEP: the topic vocabulary IS
// skill.LoadDocsManifest's own Sections list, read at RUNTIME. This file
// holds no second copy of it — a `var topics = []string{...}` here would be
// class C3 (two things deciding one predicate), which is the exact defect
// this phase exists to end. Adding a section to docs-manifest.json adds a
// topic with no code change to this file.
//
// This file's only package-level symbols are DocsCommand + NewDocsCommand
// plus its own uniquely-named, file-private helpers (docs* prefix) — no
// shared helper, no package var, per this package's established Placement
// convention (cmd_whatsnew.go, cmd_adapt.go).
//
// Wired at cmd/a2a/wire.go's `docs` entry — deliberately with no
// resolvePaths, no project config and no space — and listed by catalog.go.
// This file stays constructible and independently unit-testable without
// either. It needs no project config and no connected space: Run reads only
// the embedded skill tree, so it works unchanged in an empty directory
// (AC-6).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/skill"
)

// DocsCommand implements `a2a docs [topic]`.
//
// tree defaults to skill.Files (the real embedded corpus, see
// NewDocsCommand) so production reads the shipped documentation; tests
// substitute a fixture fs.FS (skill.LoadDocsManifest's own fixture-FS
// shape, skill/manifest_test.go) so AC-5 — a manifest addition becomes a
// topic with no code change — is provable here without touching the real
// embed.
type DocsCommand struct {
	tree fs.FS
}

// NewDocsCommand constructs the docs command over the real embedded skill
// tree.
func NewDocsCommand() *DocsCommand {
	return &DocsCommand{tree: skill.Files}
}

// Name implements cli.Command.
func (c *DocsCommand) Name() string { return "docs" }

// Synopsis implements cli.Command.
func (c *DocsCommand) Synopsis() string {
	return "print the embedded a2ahub documentation by topic (a2a docs [topic])"
}

// Run implements cli.Command. Exit codes: 2 = usage (too many arguments);
// 1 = the embedded corpus is broken, or the requested topic is unknown or
// missing its page; 0 = success (including the bare-invocation listing).
func (c *DocsCommand) Run(_ context.Context, args []string, stdio IO) int {
	fset := flag.NewFlagSet("docs", flag.ContinueOnError)
	fset.SetOutput(stdio.Stderr)
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() > 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a docs [topic]")
		return 2
	}

	manifest, loadErr := skill.LoadDocsManifest(c.tree)
	if loadErr != nil {
		return docsRefuse(stdio,
			"list a2ahub documentation topics",
			loadErr.Error(),
			"this build's embedded documentation corpus is broken; rebuild the a2a binary")
	}

	if fset.NArg() == 0 {
		docsRenderList(stdio, manifest)
		return 0
	}

	topic := fset.Arg(0)
	entry, ok := docsResolveTopic(manifest, topic)
	if !ok {
		return docsRefuse(stdio,
			fmt.Sprintf("read documentation topic %q", topic),
			"no such topic in the embedded manifest",
			"run `a2a docs` with no topic to list the known topics, or choose one of: "+strings.Join(docsTopicIDs(manifest), ", "))
	}

	body, readErr := fs.ReadFile(c.tree, entry.File)
	if readErr != nil {
		return docsRefuse(stdio,
			fmt.Sprintf("read documentation topic %q", topic),
			fmt.Sprintf("the manifest names %s for this section, which is missing from this build's embedded tree", entry.File),
			"this is a corpus/binary mismatch, not a caller mistake; rebuild the a2a binary or report the missing page")
	}

	_, _ = stdio.Stdout.Write(body)
	return 0
}

// docsResolveTopic resolves topic against manifest.Sections — the pure
// predicate a table test drives directly (AC-3), with no stdio involved.
// ok is false when no section carries that id.
func docsResolveTopic(manifest skill.DocsManifest, topic string) (entry skill.DocSectionEntry, ok bool) {
	for _, s := range manifest.Sections {
		if s.ID == topic {
			return s, true
		}
	}
	return skill.DocSectionEntry{}, false
}

// docsTopicIDs returns every manifest section id, sorted — the vocabulary
// an unknown-topic refusal names (US-1: "refuse and name valid ids", never
// "see the docs").
func docsTopicIDs(manifest skill.DocsManifest) []string {
	ids := make([]string, 0, len(manifest.Sections))
	for _, s := range manifest.Sections {
		ids = append(ids, s.ID)
	}
	sort.Strings(ids)
	return ids
}

// docsRenderList prints id + title for every manifest section, grouped in
// manifest group order (manifest.Groups, not an alphabetical or
// section-encounter order) — a section whose page file is later found
// missing still lists fine, since this never reads page bytes (§6 edge
// case: a listing must not crash on a missing page).
func docsRenderList(stdio IO, manifest skill.DocsManifest) {
	for _, group := range manifest.Groups {
		_, _ = fmt.Fprintf(stdio.Stdout, "%s:\n", group)
		for _, s := range manifest.Sections {
			if s.Group != group {
				continue
			}
			_, _ = fmt.Fprintf(stdio.Stdout, "  %-24s %s\n", s.ID, s.Title)
		}
	}
}

// docsRefuse builds a three-part Refusal (internal/cli's NewRefusal —
// answers-that-hold-2026-08 spec 04) and writes it to stdio.Stderr,
// returning 1 — the same shape cmd_adapt.go's adaptRefuse already uses.
// rerr (NewRefusal's own construction-time refusal on an empty next step)
// is unreachable in practice: every call site above passes a fixed,
// non-empty nextStep literal. The fallback below is a structural safety
// net, never a trusted-forever assumption.
func docsRefuse(stdio IO, attempted, found, nextStep string) int {
	refusal, rerr := NewRefusal(attempted, found, nextStep)
	if rerr != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "docs: internal error building a refusal (empty next step) — this is a bug in the docs command, not a caller mistake")
		return 1
	}
	_, _ = fmt.Fprintln(stdio.Stderr, refusal)
	return 1
}
