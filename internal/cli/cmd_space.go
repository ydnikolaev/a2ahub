// `a2a space init` — self-service space scaffolding (spec 33 §12). This
// file's only package-level symbols are SpaceCommand + its NewSpaceCommand
// constructor plus file-private, uniquely-named helpers (space* prefix) —
// no shared helper, no package var, matching cmd_contract.go's own
// Placement convention for this package.
package cli

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// SpaceCommand implements `a2a space <init>` — today a single sub-verb
// (mirrors the `contract` group's dispatch shape, cmd_contract.go).
type SpaceCommand struct {
	// TemplateFiles is the embedded space-template/ tree (spacetemplate.Files,
	// DI'd from wire.go — this package never imports space-template
	// directly, keeping the layering rule "internal/cli must not reach
	// outside its layer" intact; the lead's wiring supplies the concrete
	// fs.FS).
	TemplateFiles fs.FS
	// Version is this binary's own version stamp (ldflags main.version,
	// DI'd from wire.go — mirrors DoctorCommand.binaryVersion/InitCommand.Version).
	// A `go build` with no ldflags yields "dev", which spaceCleanVersion
	// refuses (fail closed — see runInit's version guard).
	Version string

	// writeFile/mkdirAll are DI seams (rails, mirrors InitCommand's own
	// writeFile convention) so a future test can simulate a write failure
	// without a real unwritable path. NewSpaceCommand defaults both to the
	// real os operations.
	writeFile func(path string, data []byte, perm os.FileMode) error
	mkdirAll  func(path string, perm os.FileMode) error
}

// NewSpaceCommand constructs the space command. templateFiles is the
// embedded space-template/ tree; version is this build's own version stamp.
func NewSpaceCommand(templateFiles fs.FS, version string) *SpaceCommand {
	return &SpaceCommand{
		TemplateFiles: templateFiles,
		Version:       version,
		writeFile:     os.WriteFile,
		mkdirAll:      os.MkdirAll,
	}
}

// Name implements cli.Command.
func (c *SpaceCommand) Name() string { return "space" }

// Synopsis implements cli.Command.
func (c *SpaceCommand) Synopsis() string {
	return "space scaffolding: init <space-id> [--dir <path>] — write a ready-to-push space tree from the embedded template"
}

// Run implements cli.Command.
func (c *SpaceCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a space <init> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	if IsHelpArg(sub) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a space <init> ...")
		for _, s := range SpaceSubcommands() {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %-14s %s\n", s.Name, s.Synopsis)
		}
		return 0
	}
	switch sub {
	case "init":
		return c.runInit(ctx, rest, stdio)
	default:
		_, _ = fmt.Fprintf(stdio.Stderr, "space: unknown subcommand %q\n", sub)
		return 2
	}
}

var _ Command = (*SpaceCommand)(nil)

// SpaceSubcommand describes one `a2a space <sub>` sub-verb for external
// surface enumeration.
type SpaceSubcommand struct {
	Name     string // e.g. "init"
	Synopsis string
}

// SpaceSubcommands is the SSOT list of the `a2a space` family's sub-verbs
// for surface enumeration — mirrors ContractSubcommands' own role (the P14
// CLI/MCP parity check and the P13 command-catalog projection read that
// one; a future wiring of `space` into either surface should read this one
// the same way). The space sub-verbs are dispatched by the bare switch in
// SpaceCommand.Run (not registered as individual cli.Command values), so
// this list is their only machine-enumerable home. KEEP IN SYNC with that
// switch.
func SpaceSubcommands() []SpaceSubcommand {
	return []SpaceSubcommand{
		{Name: "init", Synopsis: "scaffold a new space repo tree from the embedded template"},
	}
}

// runInit implements `a2a space init <space-id> [--dir <path>]` (spec 33
// §12). Exit codes: 2 = usage error; 1 = version guard / non-empty target /
// write failure; 0 = success.
func (c *SpaceCommand) runInit(_ context.Context, args []string, stdio IO) int {
	// The space-id is lifted out BEFORE flag.Parse, so both orders work —
	// Go's flag package stops at the first non-flag token, which would
	// otherwise make `init <space-id> --dir <path>` silently leave --dir
	// unparsed (the same guard runAdopt documents for its own id arg,
	// cmd_contract.go).
	fset := flag.NewFlagSet("space init", flag.ContinueOnError)
	fset.SetOutput(stdio.Stderr)
	dirFlag := fset.String("dir", "", "target directory (default ./<space-id>)")

	var spaceID string
	rest := args
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		spaceID, rest = args[0], args[1:]
	}
	if err := fset.Parse(rest); err != nil {
		return 2
	}
	if spaceID == "" && fset.NArg() == 1 {
		spaceID = fset.Arg(0)
	}
	if spaceID == "" || fset.NArg() > 1 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a space init <space-id> [--dir <path>]")
		return 2
	}
	target := *dirFlag
	if target == "" {
		target = "./" + spaceID
	}

	// Version guard (fail closed): a dev build (or any non-clean semver)
	// must not scaffold a pin to a nonexistent release tag.
	cleanVersion, err := spaceCleanVersion(c.Version)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space init: refusing to scaffold — %v\n", err)
		return 1
	}

	if nonEmpty, err := spaceDirNonEmpty(target); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space init: cannot inspect %s: %v\n", target, err)
		return 1
	} else if nonEmpty {
		_, _ = fmt.Fprintf(stdio.Stderr, "space init: %s already exists and is not empty — remove it or pass a different --dir\n", target)
		return 1
	}

	written, err := c.spaceScaffold(target, spaceID, cleanVersion)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space init: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "space init: wrote %d files to %s\n", written, target)
	_, _ = fmt.Fprintln(stdio.Stdout, "space init: residual manual steps:")
	_, _ = fmt.Fprintln(stdio.Stdout, "  - CODEOWNERS: replace the @REPLACE_WITH_ORG/... placeholders with real GitHub teams/logins")
	_, _ = fmt.Fprintln(stdio.Stdout, "  - create the space repo on GitHub and push this tree")
	_, _ = fmt.Fprintln(stdio.Stdout, `  - arm branch protection with required check "a2a-validate / validate" (see BRANCH-PROTECTION.md)`)
	return 0
}

// spaceDirNonEmpty reports whether target exists and contains at least one
// entry. A target that does not exist at all is reported as empty (nil
// error) — scaffolding creates it.
func spaceDirNonEmpty(target string) (bool, error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return len(entries) > 0, nil
}

// spaceVersionPattern matches a clean "vX.Y.Z" or "X.Y.Z" version string —
// the shape a release tag actually resolves to. Anything else (a "dev"
// build, a pre-release suffix, a bare major) fails closed.
var spaceVersionPattern = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)$`)

// spaceCleanVersion validates raw against spaceVersionPattern and returns
// it WITHOUT a leading "v" (the form space.yaml's min_binary_version and
// the caller `uses:` ref's suffix both need). A non-matching input (e.g.
// "dev", the no-ldflags default) is refused — a dev build must not
// scaffold a pin to a release tag that does not exist.
func spaceCleanVersion(raw string) (string, error) {
	m := spaceVersionPattern.FindStringSubmatch(raw)
	if m == nil {
		return "", fmt.Errorf("binary version %q is not a release version (vX.Y.Z) — build a tagged release, not a dev build, before scaffolding a space", raw)
	}
	return m[1], nil
}

// spaceIDSentinel is the space.yaml placeholder `a2a space init` replaces
// with the given space id.
var spaceIDSentinel = []byte("REPLACE_WITH_SPACE_ID")

// spaceMinVersionPattern targets space.yaml's `min_binary_version: <ver>`
// value (whatever the template currently bakes in), leaving the rest of the
// line (including its trailing comment) intact.
var spaceMinVersionPattern = regexp.MustCompile(`(min_binary_version:\s*)\S+`)

// spaceWorkflowRefPattern targets the reusable-workflow `uses:` ref's
// version suffix (`a2a-validate-reusable.yml@vX.Y.Z`) in the caller
// workflow — a targeted replace of the token, not the whole line, so
// unrelated workflow content (job names, inputs) is untouched.
var spaceWorkflowRefPattern = regexp.MustCompile(`(a2a-validate-reusable\.yml@)v?[0-9]+\.[0-9]+\.[0-9]+`)

// spaceApplySubstitutions rewrites one embedded template file's content per
// its repo-relative path (io/fs slash-separated, matching fs.WalkDir's own
// convention regardless of host OS). CODEOWNERS' `@REPLACE_WITH_ORG/...`
// placeholders are deliberately left untouched — a documented residual
// manual step (spec 33 §12 "Residual manual step"), not a bug.
func spaceApplySubstitutions(path string, data []byte, spaceID, version string) []byte {
	switch path {
	case "space.yaml":
		data = bytes.ReplaceAll(data, spaceIDSentinel, []byte(spaceID))
		data = spaceMinVersionPattern.ReplaceAll(data, []byte("${1}"+version))
	case ".github/workflows/a2a-validate.yml":
		// The RUNNING BINARY's own version, never the version baked into
		// the embedded template — a v0.6.0 binary must not scaffold
		// `@v0.5.0` (spec 33 §12 trap #2).
		data = spaceWorkflowRefPattern.ReplaceAll(data, []byte("${1}v"+version))
	}
	return data
}

// spaceScaffold walks c.TemplateFiles and writes every regular file under
// target, applying spaceApplySubstitutions per file. Returns the count of
// files written.
func (c *SpaceCommand) spaceScaffold(target, spaceID, version string) (int, error) {
	// Nil-safe: a caller that builds SpaceCommand as a struct literal
	// (rather than through NewSpaceCommand) still gets working defaults —
	// mirrors the rest of this package's DI convention of "nil means real
	// implementation", never a nil-seam panic.
	writeFile := c.writeFile
	if writeFile == nil {
		writeFile = os.WriteFile
	}
	mkdirAll := c.mkdirAll
	if mkdirAll == nil {
		mkdirAll = os.MkdirAll
	}

	written := 0
	err := fs.WalkDir(c.TemplateFiles, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, rerr := fs.ReadFile(c.TemplateFiles, p)
		if rerr != nil {
			return fmt.Errorf("cannot read embedded %s: %w", p, rerr)
		}
		data = spaceApplySubstitutions(p, data, spaceID, version)

		destPath := filepath.Join(target, filepath.FromSlash(p))
		if merr := mkdirAll(filepath.Dir(destPath), 0o755); merr != nil {
			return fmt.Errorf("cannot create directory for %s: %w", destPath, merr)
		}
		if werr := writeFile(destPath, data, 0o644); werr != nil {
			return fmt.Errorf("cannot write %s: %w", destPath, werr)
		}
		written++
		return nil
	})
	if err != nil {
		return written, err
	}
	return written, nil
}
