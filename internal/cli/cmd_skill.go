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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/surface"
)

// errSkillForeignTarget is returned by installSkillTree when the target is
// non-empty, carries no a2ahub provenance marker (someone else's content), and
// force is not set — nothing is written. Callers decide: `a2a skill install`
// errors; `a2a init` warns and skips (so onboarding never fails on it).
var errSkillForeignTarget = errors.New("target is not an a2ahub-managed skill install")

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

// SkillCommand implements `a2a skill <subcommand>`: `install` (materialize
// the SSOT tree) and `link` (P32, OP-916/917: install a per-surface
// discovery entry pointing AT the installed SSOT tree). The dispatch shape
// leaves room for `a2a skill path`/`list` later without a second top-level
// verb.
type SkillCommand struct {
	// files is the embedded skill tree, rooted at "a2ahub/" (skill.Files).
	files fs.FS
	// version is this build's version stamp, recorded in PROVENANCE.md.
	version string

	// ProjectRoot is the consumer repo's root, DI'd from wire.go, used by
	// `skill link` to detect agent surfaces and resolve link targets. Left
	// empty (catalog.go's NewSkillCommand(nil, "") construction, or a test
	// that never sets it), `runLink` reports an error rather than silently
	// no-opping — link has no other sensible default target.
	ProjectRoot string
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
	return "install the a2ahub expert-skill tree into this repo, and link it so an agent surface can discover it"
}

// Run implements Command. Exit codes: 2 = usage; 1 = install/link error; 0 =
// ok. `install` materializes the SSOT tree under a local namespace (never a
// consumer harness path); `link` (P32) installs a per-surface discovery
// entry pointing AT that tree, so the agent surface that must read it
// actually finds it.
func (c *SkillCommand) Run(_ context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a skill install [--dir <path>] [--force] | a2a skill link [--surface <id>] [--force]")
		return 2
	}
	switch args[0] {
	case "install":
		return c.runInstall(args[1:], stdio)
	case "link":
		return c.runLink(args[1:], stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill: unknown subcommand %q (want: install, link)\n", args[0])
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
	written, err := installSkillTree(c.files, *dir, c.version, *force)
	if errors.Is(err, errSkillForeignTarget) {
		_, _ = fmt.Fprintf(stdio.Stderr,
			"a2a skill install: %s exists and is not an a2ahub skill install — refusing to overwrite (pass --force or --dir <path>)\n", *dir)
		return 1
	}
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill install: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "a2a skill: installed %d files to %s\n", written, *dir)
	_, _ = fmt.Fprintf(stdio.Stdout, "  entry point: %s\n", filepath.Join(*dir, "SKILL.md"))
	_, _ = fmt.Fprintln(stdio.Stdout, "  point your repo's AGENTS.md/CLAUDE.md at it (or run `a2a init` / `a2a init --agents-pointer`)")
	return 0
}

// runLink implements `a2a skill link` (P32, spec 32 §2/AC-916.*/AC-917.*):
// installs a discovery entry (symlink, or a stub fallback) for one or every
// detected agent surface, pointing at the installed SSOT tree
// (skillDefaultDir). It never invents a harness directory a consumer has not
// opted into (surface.Detect only sees a marker dir that already exists),
// and it refuses to link a target that is not a2ahub-owned unless --force.
func (c *SkillCommand) runLink(args []string, stdio IO) int {
	fset := flag.NewFlagSet("skill link", flag.ContinueOnError)
	fset.SetOutput(stdio.Stderr)
	surfaceID := fset.String("surface", "", "link only this surface id (default: every detected surface)")
	force := fset.Bool("force", false, "overwrite a link target that is not an a2ahub-managed link")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a skill link [--surface <id>] [--force]")
		return 2
	}
	if c.ProjectRoot == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a skill link: no project root configured")
		return 1
	}

	ssotRel := skillDefaultDir
	if _, err := os.Stat(filepath.Join(c.ProjectRoot, ssotRel, "SKILL.md")); err != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "a2a skill link: no a2ahub skill installed — run 'a2a skill install' first")
		return 1
	}

	explicit := *surfaceID != ""
	var targets []surface.Surface
	if explicit {
		s, ok := surface.ByID(*surfaceID)
		if !ok {
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill link: unknown surface %q (known: claude, codex)\n", *surfaceID)
			return 2
		}
		targets = []surface.Surface{s}
	} else {
		targets = surface.Detect(c.ProjectRoot)
		if len(targets) == 0 {
			_, _ = fmt.Fprintln(stdio.Stdout, "a2a skill link: no known agent surface detected (.claude/ or .codex/) — nothing to link")
			return 0
		}
	}

	hardFail := false
	for _, s := range targets {
		result, err := surface.Link(c.ProjectRoot, s, ssotRel, *force)
		switch {
		case errors.Is(err, surface.ErrForeignLinkTarget):
			_, _ = fmt.Fprintf(stdio.Stderr,
				"a2a skill link: %s already exists and is not an a2ahub link — refusing (pass --force)\n",
				filepath.Join(s.SkillsHome, "a2ahub"))
			if explicit {
				hardFail = true
			}
		case err != nil:
			_, _ = fmt.Fprintf(stdio.Stderr, "a2a skill link: %v\n", err)
			hardFail = true
		default:
			_, _ = fmt.Fprintf(stdio.Stdout, "linked %s: %s (%s)\n", s.ID, result.Path, result.Mode)
		}
	}

	if hardFail {
		return 1
	}
	return 0
}

// installSkillTree is the reusable install core shared by `a2a skill install`
// and `a2a init` (default onboarding). It materializes files' "a2ahub" subtree
// under target with a PROVENANCE.md marker, and returns the file count.
//
// No-clobber + mirror semantics: a non-empty target WITHOUT our marker and
// without force yields errSkillForeignTarget (nothing written). An authorized
// non-empty target (our own marker, or force over foreign content) is wiped
// FIRST so the result mirrors the embedded tree (no orphaned stale files). An
// absent/empty target installs directly. It writes ONLY under target.
func installSkillTree(files fs.FS, target, version string, force bool) (int, error) {
	nonEmpty, owned, err := skillTargetState(target)
	if err != nil {
		return 0, err
	}
	if nonEmpty && !owned && !force {
		return 0, errSkillForeignTarget
	}
	if nonEmpty {
		if err := os.RemoveAll(target); err != nil {
			return 0, fmt.Errorf("cannot refresh %s: %w", target, err)
		}
	}

	written, err := writeSkillTree(files, target)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(target, skillProvenanceFile), []byte(skillProvenance(version)), 0o644); err != nil {
		return written, fmt.Errorf("write provenance: %w", err)
	}
	return written, nil
}

// writeSkillTree materializes files' embedded a2ahub tree under target,
// stripping the embed's "a2ahub/" root prefix so files land at <target>/SKILL.md
// etc. Returns the number of files written. It writes ONLY under target.
func writeSkillTree(files fs.FS, target string) (int, error) {
	var count int
	err := fs.WalkDir(files, "a2ahub", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "a2ahub"), "/")
		dest := filepath.Join(target, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, readErr := fs.ReadFile(files, p)
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

// skillProvenance renders the PROVENANCE.md marker: the machine tag first, then
// the human "what/where/how-to-refresh" note.
func skillProvenance(version string) string {
	return skillProvenanceTag + "\n" +
		"# a2ahub skill — installed artifact\n\n" +
		"Written by `a2a skill install` / `a2a init` (a2a " + version + ").\n" +
		"Source: github.com/ydnikolaev/a2ahub — skill/a2ahub/.\n\n" +
		"**Do not hand-edit** — refresh with `a2a skill install`.\n\n" +
		"a2ahub is the protocol for typed cross-system artifact exchange. Start at " +
		"[SKILL.md](SKILL.md); the `a2a` binary is the source of truth for command " +
		"syntax and validation (this tree defers to it, never restates it).\n"
}

// skillTargetState inspects target for the install gate: nonEmpty is true when
// it exists and holds at least one entry; owned is true when it carries the
// a2ahub provenance marker (a prior install of ours). An absent or empty target
// is (false, false) — safe to install without --force and needs no wipe. A
// non-empty target without the marker is (true, false) — someone else's content
// (refuse unless --force).
func skillTargetState(target string) (nonEmpty, owned bool, err error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	if len(entries) == 0 {
		return false, false, nil
	}
	data, readErr := os.ReadFile(filepath.Join(target, skillProvenanceFile))
	if readErr != nil {
		return true, false, nil //nolint:nilerr // absent/unreadable marker => non-empty + unowned, not a hard error
	}
	return true, strings.HasPrefix(string(data), skillProvenanceTag), nil
}

var _ Command = (*SkillCommand)(nil)
