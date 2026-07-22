// OP-skill `a2a skill install` (spec 20): materialize the a2ahub expert-skill
// tree (embedded in the binary) into a consumer repo so the repo's agent can
// read the operating manual locally and defer to `a2a` for command/validation
// truth. This file's only package-level symbols are SkillCommand +
// NewSkillCommand plus its own skill*-prefixed file-private helpers, per this
// package's Placement convention.
//
// Safety contract (operator requirement 2026-07-23): install writes ONLY under
// its own namespace (default .a2ahub/skill/), never into .claude/, AGENTS.md,
// or any consumer file — so it cannot clobber an existing harness. It is
// idempotent on its OWN target (refresh) but REFUSES a target that holds
// non-a2ahub content unless --force, and every install drops a PROVENANCE.md
// marker (what this is, where it came from, how to refresh).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skillProvenanceFile is the marker file every install writes at the target
// root. Its presence identifies a target as a2ahub-owned (so a refresh may
// overwrite it) and tells a human/agent what the tree is and how to refresh.
const skillProvenanceFile = "PROVENANCE.md"

// skillProvenanceTag is the machine-recognizable first line of PROVENANCE.md —
// a re-install checks for it to distinguish "our target, refresh freely" from
// "someone else's directory, refuse".
const skillProvenanceTag = "<!-- a2ahub-skill-install -->"

// skillDefaultDir is the default install target, relative to cwd. A dedicated,
// provider-neutral namespace: it cannot collide with .claude/ (Claude Code) or
// a hand-written AGENTS.md, so a re-install never touches the harness.
const skillDefaultDir = ".a2ahub/skill"

// SkillCommand implements `a2a skill <subcommand>`. Today the only subcommand
// is `install`; the dispatch shape leaves room for `a2a skill path`/`list`
// later without a second top-level verb.
type SkillCommand struct {
	// files is the embedded skill tree, rooted at "a2ahub/" (skill.Files).
	files fs.FS
	// version is this build's version stamp, recorded in PROVENANCE.md.
	version string
}

// NewSkillCommand constructs the command. files is the embedded a2ahub skill
// tree (cmd/a2a passes skill.Files); version is the binary's version stamp.
func NewSkillCommand(files fs.FS, version string) *SkillCommand {
	return &SkillCommand{files: files, version: version}
}

// Name implements Command.
func (c *SkillCommand) Name() string { return "skill" }

// Synopsis implements Command.
func (c *SkillCommand) Synopsis() string {
	return "install the a2ahub expert-skill tree into this repo (for its agent)"
}

// Run implements Command. Exit codes: 2 = usage; 1 = install error; 0 = ok.
func (c *SkillCommand) Run(_ context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a skill install [--dir <path>] [--force]")
		return 2
	}
	switch args[0] {
	case "install":
		return c.runInstall(args[1:], stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill: unknown subcommand %q (want: install)\n", args[0])
		return 2
	}
}

func (c *SkillCommand) runInstall(args []string, stdio IO) int {
	fset := flag.NewFlagSet("skill install", flag.ContinueOnError)
	fset.SetOutput(stdio.Stderr)
	dir := fset.String("dir", skillDefaultDir, "install target directory")
	force := fset.Bool("force", false, "overwrite a target that is not an a2ahub-managed skill install")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a skill install [--dir <path>] [--force]")
		return 2
	}
	target := *dir

	// No-clobber gate: a target that exists, is non-empty, and carries NO
	// a2ahub provenance marker is treated as someone else's content — refuse
	// unless --force. Our own prior install (marker present) refreshes freely.
	if owned, err := skillTargetIsOwnedOrEmpty(target); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill install: %v\n", err)
		return 1
	} else if !owned && !*force {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"a2a skill install: %s exists and is not an a2ahub skill install — refusing to overwrite (pass --force or --dir <path>)\n", target)
		return 1
	}

	written, err := c.writeTree(target)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill install: %v\n", err)
		return 1
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(c.provenance()), 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill install: write provenance: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "a2a skill: installed %d files to %s\n", written, target)
	_, _ = fmt.Fprintf(stdio.Stdout, "  entry point: %s\n", filepath.Join(target, "SKILL.md"))
	_, _ = fmt.Fprintln(stdio.Stdout, "  point your repo's AGENTS.md/CLAUDE.md at it (or run `a2a init --agents-pointer`)")
	return 0
}

// writeTree materializes the embedded a2ahub tree under target, stripping the
// embed's "a2ahub/" root prefix so files land at <target>/SKILL.md etc. Returns
// the number of files written. It writes ONLY under target.
func (c *SkillCommand) writeTree(target string) (int, error) {
	var count int
	err := fs.WalkDir(c.files, "a2ahub", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "a2ahub")
		rel = strings.TrimPrefix(rel, "/")
		dest := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, readErr := fs.ReadFile(c.files, p)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return mkErr
		}
		if wErr := os.WriteFile(dest, data, 0o644); wErr != nil {
			return wErr
		}
		count++
		return nil
	})
	return count, err
}

// provenance renders the PROVENANCE.md marker: the machine tag first, then the
// human "what/where/how-to-refresh" note.
func (c *SkillCommand) provenance() string {
	return skillProvenanceTag + "\n" +
		"# a2ahub skill — installed artifact\n\n" +
		"Written by `a2a skill install` (a2a " + c.version + ").\n" +
		"Source: github.com/ydnikolaev/a2ahub — skill/a2ahub/.\n\n" +
		"**Do not hand-edit** — refresh with `a2a skill install`.\n\n" +
		"a2ahub is the protocol for typed cross-system artifact exchange. Start at " +
		"[SKILL.md](SKILL.md); the `a2a` binary is the source of truth for command " +
		"syntax and validation (this tree defers to it, never restates it).\n"
}

// skillTargetIsOwnedOrEmpty reports whether target is safe to (re)install into:
// it does not exist, or is empty, or already carries the a2ahub provenance
// marker. A non-empty target WITHOUT the marker returns false (someone else's
// content — the caller refuses unless --force).
func skillTargetIsOwnedOrEmpty(target string) (bool, error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	if len(entries) == 0 {
		return true, nil
	}
	data, err := os.ReadFile(filepath.Join(target, skillProvenanceFile))
	if err != nil {
		return false, nil //nolint:nilerr // absent/unreadable marker => treat as unowned, not a hard error
	}
	return strings.HasPrefix(string(data), skillProvenanceTag), nil
}

var _ Command = (*SkillCommand)(nil)
