// `a2a space init` — self-service space scaffolding (spec 33 §12) — and
// `a2a space update` — template-drift migration (spec 35). This file's
// only package-level symbols are SpaceCommand + its NewSpaceCommand
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
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// SpaceCommand implements `a2a space <init|update>` (mirrors the `contract`
// group's dispatch shape, cmd_contract.go).
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

	// --- `space update`-only DI (spec 35) -----------------------------
	//
	// All six are nil-able ("nil means not wired", this package's DI
	// convention): `a2a space init` never reads any of them, so it keeps
	// working when every one is left zero (TestSpaceCommandStructLiteralWorks
	// proves this for TemplateFiles/Version already; the same holds for
	// these). `a2a space update` refuses cleanly (spaceUpdateWired) rather
	// than nil-panicking when any is unwired. The LEAD wires all six in
	// cmd/a2a — this package never resolves a "connected space" itself.

	// Funnel is the write-funnel seam (submitFunnel, cmd_submit.go) — reused
	// as-is, no second funnel entry point.
	Funnel submitFunnel
	// MirrorDir is the connected space's local mirror clone working
	// directory (same role as SubmitCommand.mirrorDir).
	MirrorDir string
	// SpaceID is the connected space's id — both the `--space` flag's
	// expected value and the substitution target for space.yaml's sentinel
	// (space init reuses spaceApplySubstitutions, which needs a space id;
	// update's mirror already carries a real one, so this is used only to
	// validate `--space`, never to rewrite the id).
	SpaceID string
	// OwnSystem is this project's configured own system id (§7.4) — the
	// authoring system for the branch name; the section guard for the
	// infrastructure paths this command writes is satisfied via
	// AllowSpaceInfrastructure, not via section membership.
	OwnSystem string
	// HostCfg carries the push/PR target and commit authorship (same shape
	// SubmitCommand uses).
	HostCfg SubmitHostConfig
	// Scopes probes what the write credential is actually allowed to do.
	// Nil disables the check (an `init`-only construction, or a test that
	// does not care) — the command then behaves exactly as before.
	Scopes spaceScopeChecker

	// AwaitPR polls the pull request this command opened until it merges and
	// then refreshes the local mirror — `--wait`'s entire implementation, and
	// deliberately NOT a second poller: the lead wires it to the same
	// space.Awaiter `a2a await` uses, whose state machine already refuses a
	// PR that closes without merging and one whose required check fails.
	//
	// Nil-able, like the six above: `--wait` then REFUSES by name rather than
	// silently behaving as though the flag were absent. A flag that is
	// accepted and ignored is worse than one that does not exist.
	AwaitPR func(ctx context.Context, branch string) (space.AwaitResult, error)

	// readFile is a DI seam (mirrors writeFile/mkdirAll's own convention)
	// so a test can simulate the mirror's on-disk content without a real
	// filesystem. NewSpaceCommand defaults it to os.ReadFile.
	readFile func(path string) ([]byte, error)
}

// NewSpaceCommand constructs the space command. templateFiles is the
// embedded space-template/ tree; version is this build's own version stamp.
func NewSpaceCommand(templateFiles fs.FS, version string) *SpaceCommand {
	return &SpaceCommand{
		TemplateFiles: templateFiles,
		Version:       version,
		writeFile:     os.WriteFile,
		mkdirAll:      os.MkdirAll,
		readFile:      os.ReadFile,
	}
}

// Name implements cli.Command.
func (c *SpaceCommand) Name() string { return "space" }

// Synopsis implements cli.Command.
func (c *SpaceCommand) Synopsis() string {
	return "space scaffolding: init <space-id> [--dir <path>] | update [--space <id>] [--dry-run] [--wait] — write/migrate a space tree from the embedded template"
}

// Run implements cli.Command.
func (c *SpaceCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a space <init|update> ...")
		return 2
	}
	sub, rest := args[0], args[1:]
	if IsHelpArg(sub) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a space <init|update> ...")
		for _, s := range SpaceSubcommands() {
			_, _ = fmt.Fprintf(stdio.Stdout, "  %-14s %s\n", s.Name, s.Synopsis)
		}
		return 0
	}
	switch sub {
	case "init":
		return c.runInit(ctx, rest, stdio)
	case "update":
		return c.runUpdate(ctx, rest, stdio)
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
		{Name: "update", Synopsis: "diff the connected space's scaffolding against the embedded template and open a PR (spec 35)"},
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
	// D8 (no-silent-yes-2026-08/P3 stage 2, spec 03 §8 AC 6/14): the
	// renamed sentinel now matches the id grammar's own ordinary
	// [a-z0-9]+(-[a-z0-9]+)* branch, so bytes.ReplaceAll in
	// spaceApplySubstitutions can no longer distinguish "the caller typed
	// the template's own placeholder" from "the caller genuinely wants a
	// space named that" — a scaffold built from spaceID ==
	// spaceIDSentinel would write a schema-VALID space.yaml that still
	// carries the scaffolding sentinel as its literal `space:` value.
	// Refuse it here, before any file is written, rather than emit a
	// template that only LOOKS like a real space.
	if spaceID == string(spaceIDSentinel) {
		_, _ = fmt.Fprintf(stdio.Stderr, "space init: refusing to scaffold — %q is the template's own scaffolding sentinel, not a real space id\n", spaceID)
		return 1
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
	// The residual steps are printed IN ORDER, and the order is the part
	// that used to be missing. Two of these steps brick a space when taken
	// early, and neither ordering was stated in any published document:
	// pushing after protection is armed is refused (nothing can merge into
	// an empty protected main), and arming the required check before the
	// pinned reusable-workflow release exists means no PR can ever satisfy
	// it. Both were internal-runbook-only knowledge until now.
	_, _ = fmt.Fprintln(stdio.Stdout, "space init: remaining steps, IN THIS ORDER:")
	_, _ = fmt.Fprintln(stdio.Stdout, "  1. edit CODEOWNERS: replace BOTH halves of every placeholder — the org AND the name after the slash.")
	_, _ = fmt.Fprintln(stdio.Stdout, "     An owner that does not exist is IGNORED by GitHub, not rejected: swapping only the org leaves a")
	_, _ = fmt.Fprintln(stdio.Stdout, "     team nobody created, and the file then gates nothing while looking correct. Individual logins are")
	_, _ = fmt.Fprintln(stdio.Stdout, "     the safe default; check GitHub's own CODEOWNERS view afterwards, it is the only feedback you get.")
	_, _ = fmt.Fprintln(stdio.Stdout, "  2. delete README.md from this tree — it documents the TEMPLATE, not your space")
	_, _ = fmt.Fprintln(stdio.Stdout, "  3. create the space repo on GitHub, EMPTY (no README, no license)")
	_, _ = fmt.Fprintln(stdio.Stdout, "  4. git init -b main && git add -A && git commit && git remote add origin <url> && git push -u origin main")
	_, _ = fmt.Fprintln(stdio.Stdout, "     (push BEFORE arming protection: a protected empty main cannot accept its own bootstrap)")
	_, _ = fmt.Fprintln(stdio.Stdout, `  5. enable Settings -> General -> "Allow auto-merge" (off by default on every new repo;`)
	_, _ = fmt.Fprintln(stdio.Stdout, "     a2a submit arms auto-merge, so without it every write stalls behind a PR nothing will merge)")
	_, _ = fmt.Fprintln(stdio.Stdout, `  6. arm branch protection: required check "a2a-validate / validate", 0 required approvals,`)
	_, _ = fmt.Fprintln(stdio.Stdout, "     code-owner review ON, enforce_admins OFF — the full checklist is in BRANCH-PROTECTION.md")
	_, _ = fmt.Fprintln(stdio.Stdout, "  7. for each participant: add them to space.yaml, scaffold their section, add their CODEOWNERS entry")
	_, _ = fmt.Fprintln(stdio.Stdout, "")
	_, _ = fmt.Fprintln(stdio.Stdout, "space init: then verify from a participant's checkout — `a2a connect <url>` and `a2a doctor`.")
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
//
// no-silent-yes-2026-08/P3 stage 2 (D8, spec 03 §8 AC 14): RENAMED to the
// conforming "replace-with-space-id" from an OLD, screaming-snake-case
// spelling (this file's own git history carries the prior byte value; it
// is deliberately not quoted here — this stage's own acceptance check
// greps for it, under this directory, expecting NOTHING).
// schemas/manifest/v1/space.schema.json:14's own description explains why
// the old spelling needed a dedicated alternation in the frozen id
// pattern: that file's authoring scope did not include this one, so
// renaming the sentinel to a conforming value was not an option from
// there. D8 overrides that from the side that DOES own this file: the
// renamed value matches the pattern's ordinary SECOND branch
// ([a-z0-9]+(-[a-z0-9]+)*) like any real space id, so the frozen schema
// needs no value-level exemption at all — the alternation's FIRST branch
// stays in the frozen bytes (git diff schemas/manifest/v1/space.schema.json
// is EMPTY, AC 14) but is no longer load-bearing: nothing in the product
// depends on it any more.
//
// Renaming the SPELLING changes NO environment variable name — verified,
// not assumed: internal/space.CredentialEnvVar (credential.go:31) maps
// every `-` to `_` before uppercasing, so the old and new spellings both
// render to the identical "A2A_TOKEN_" + the uppercased, underscored
// sentinel.
var spaceIDSentinel = []byte("replace-with-space-id")

// spaceMinVersionPattern targets space.yaml's `min_binary_version: <ver>`
// value (whatever the template currently bakes in), leaving the rest of the
// line (including its trailing comment) intact.
var spaceMinVersionPattern = regexp.MustCompile(`(min_binary_version:\s*)\S+`)

// spaceWorkflowRefPattern targets the reusable-workflow `uses:` ref's
// version suffix (`a2a-validate-reusable.yml@vX.Y.Z`) in the caller
// workflow — a targeted replace of the token, not the whole line, so
// unrelated workflow content (job names, inputs) is untouched.
var spaceWorkflowRefPattern = regexp.MustCompile(`(a2a-validate-reusable\.yml@)v?[0-9]+\.[0-9]+\.[0-9]+`)

// spaceNotifyWorkflowRefPattern is spaceWorkflowRefPattern's own counterpart
// for the notification caller (space-notify-2026-08 P5, spec 05 "Template
// and adoption") — a SEPARATE pattern rather than widening the validator's
// own, because the two callers pin two DIFFERENT reusable-workflow files and
// a shared regex would rewrite whichever ref happened to match first, the
// exact "wrong neighbour" bug class notes_test.go's own reusableRefDefault
// scopes itself against.
var spaceNotifyWorkflowRefPattern = regexp.MustCompile(`(a2a-notify-reusable\.yml@)v?[0-9]+\.[0-9]+\.[0-9]+`)

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
	case ".github/workflows/a2a-notify.yml":
		// Same rule, same trap, the notification caller's own ref (spec 05
		// "Template and adoption").
		data = spaceNotifyWorkflowRefPattern.ReplaceAll(data, []byte("${1}v"+version))
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

// --- `a2a space update` (spec 35) ---------------------------------------

// spaceScopeChecker is this file's narrow consumer-side seam over the host's
// token-capability probe (rails ISP/DI) — *host.GitHubHost satisfies it
// structurally.
type spaceScopeChecker interface {
	TokenScopes(ctx context.Context, cred host.Credential) (scopes []string, reported bool, err error)
}

// spaceWorkflowScope is the GitHub scope required to create or update a file
// under .github/workflows/. `space update` rewrites the space's CI caller, so
// it needs this; NO other a2a write does — every artifact write is confined to
// the caller's own section by the funnel's guard, and .github/ is reachable
// only through this one command.
const spaceWorkflowScope = "workflow"

// spaceCheckWorkflowScope reports two strings:
//   - refuse: the reason this write MUST be refused (exit 1), or "" if it may proceed.
//   - note: an advisory note for the user (printed to stdout before attempting the write),
//     or "" if there is nothing to warn about.
//
// The distinction matters because fine-grained PATs and App installation tokens do not
// advertise scopes at all (reported=false). Treating that silence as refusal would block
// exactly the most narrowly-scoped credentials a2a wants people to use. Instead, when
// scopes cannot be verified, we proceed but warn the user that GitHub may still refuse
// the push if the permission is missing.
//
// When scopes ARE reported and the credential provably lacks the workflow scope, we
// refuse hard: GitHub will reject it anyway, and the pre-flight prevents the user
// from computing a plan they cannot execute.
func (c *SpaceCommand) spaceCheckWorkflowScope(ctx context.Context) (refuse, note string) {
	if c.Scopes == nil {
		return "", ""
	}
	scopes, reported, err := c.Scopes.TokenScopes(ctx, c.HostCfg.Credential)
	if err != nil {
		return "", ""
	}
	if !reported {
		// Cannot verify scope for fine-grained credential type. Proceed but warn.
		return "", "scope could not be verified for this credential type (fine-grained PAT or GitHub App token does not advertise scopes) — the write may still be refused by GitHub if the token lacks Workflows permission"
	}
	for _, s := range scopes {
		if s == spaceWorkflowScope {
			return "", ""
		}
	}
	// Scopes were reported and do not include workflow. This is a hard refusal.
	//
	// Names the SPECIFIC file(s) this command may write under .github/workflows/
	// (spec 05 "The scope constraint that comes with it"): with a SECOND managed
	// workflow (a2a-notify.yml, space-notify-2026-08 P5) a bare ".github/workflows/"
	// no longer identifies which file the operator is being blocked on, and they
	// may not have asked for either one specifically.
	return "this write updates " + strings.Join(spaceManagedWorkflowPaths(), ", ") + ", which GitHub refuses from a token without the `" + spaceWorkflowScope + "` scope.\n" +
		"  your token reports: " + strings.Join(scopes, ", ") + "\n" +
		"  fix (either one):\n" +
		"    gh auth refresh -h github.com -s " + spaceWorkflowScope + "\n" +
		"    or use a fine-grained PAT scoped to this space with Contents/Pull requests/Workflows: write", ""
}

// spaceManagedWorkflowPaths returns every ".github/workflows/" path this
// command may write, sorted, from spaceUpdateDispositionTable's own managed
// set — a2a-validate.yml today, a2a-notify.yml joining it (spec 05). A single
// SSOT so the scope refusal can never name a file the table itself no longer
// carries.
func spaceManagedWorkflowPaths() []string {
	var paths []string
	for p := range spaceUpdateDispositionTable {
		if hasPathPrefixSpaceWorkflows(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	return paths
}

// hasPathPrefixSpaceWorkflows reports whether p is under .github/workflows/.
func hasPathPrefixSpaceWorkflows(p string) bool {
	return strings.HasPrefix(p, ".github/workflows/")
}

// SpaceInfraNoValidation is the space.SubmitValidator `a2a space update`
// wires — and it deliberately validates nothing.
//
// The V2 submit validator (SubmitValidatorAdapter) parses every non-event,
// non-registry file as an ARTIFACT: frontmatter, envelope, referential
// integrity. `space update` writes no artifacts at all — a CI workflow, a
// dependabot config, CODEOWNERS and a manifest field have no envelope, so
// running that adapter here would reject every legitimate write.
//
// What bounds this write instead is the funnel's own guard: the request
// carries AllowSpaceInfrastructure, so `spaceInfraOK` admits ONLY
// space.yaml / CODEOWNERS / BRANCH-PROTECTION.md / .github/**, and anything
// else is still refused with ErrWrongSection before any git action. The
// space's V3 gate then validates the resulting PR for real. This type is
// named rather than an inline nil so the reason is greppable and a future
// reader cannot mistake it for an oversight.
type SpaceInfraNoValidation struct{}

// ValidateSubmit implements space.SubmitValidator.
func (SpaceInfraNoValidation) ValidateSubmit(context.Context, []space.FileWrite) error { return nil }

// spaceUpdateDisposition classifies how `a2a space update` treats one
// template-relative path when diffing a connected space's scaffolding
// against the embedded template (spec 35 §2 T1; plan 35 "Intake findings"
// §2, the file-disposition model). A naive whole-file diff is destructive:
// a live space's CODEOWNERS carries real teams where the template carries
// `@REPLACE_WITH_ORG/...`, and its space.yaml carries real participants
// where the template carries a skeleton — a whole-file diff would propose
// reverting both, and would never converge (breaking AC-950.4's idempotent
// no-op).
type spaceUpdateDisposition int

const (
	// spaceDispositionManaged: the template owns the WHOLE file — always
	// overwritten with the substituted template content, whether or not it
	// already exists in the space.
	spaceDispositionManaged spaceUpdateDisposition = iota
	// spaceDispositionSeeded: the template owns only the file's EXISTENCE —
	// created if absent, but NEVER overwritten once present (its content is
	// the space's own — e.g. real CODEOWNERS teams). Drift from the
	// template is reported as an advisory note only.
	spaceDispositionSeeded
	// spaceDispositionFieldManaged: the template owns ONE named field
	// inside an otherwise space-owned file — only that field is rewritten
	// (via the exact regex `space init` already uses), every other byte of
	// the space's own file is left untouched.
	spaceDispositionFieldManaged
)

// spaceUpdateDispositionTable is the single package-level SSOT for every
// template path this command special-cases (plan 35 "Intake findings" §2).
// A template path NOT listed here defaults to spaceDispositionManaged when
// it is ABSENT from the space (a new template file — e.g. a new file under
// .github/ — propagates automatically, no code change required, AC-951.1)
// and is left untouched when it already exists in the space (an existing,
// unlisted file is presumed space-owned/customized — this command only
// knows how to safely reconcile the three dispositions below; see
// spaceComputeUpdatePlan's own "default" branch for exactly how that
// distinction is applied).
var spaceUpdateDispositionTable = map[string]spaceUpdateDisposition{
	".github/workflows/a2a-validate.yml": spaceDispositionManaged,
	// a2a-notify.yml IS listed, deliberately, even though the "unlisted
	// path" default branch already ADDS it automatically on the first
	// `space update` a space that lacks it runs (spec 05's own AC-951.1
	// path, spaceComputeUpdatePlanFor's "default" branch: unlisted+absent
	// -> propagate). That default branch's OTHER half is what forces this
	// row: "unlisted+PRESENT -> never touched again" (see this table's own
	// doc comment above). Dependabot bumps an ADOPTED caller's own `uses:`
	// TAG per space, but it does not rewrite the file's SHAPE — a future
	// structural edit to this caller (a new `with:` input, a renamed
	// trigger) would silently stop reaching every space that already
	// adopted it, the exact drift `spaceDispositionManaged` exists to
	// close for a2a-validate.yml. Verified empirically before writing this
	// row (see this phase's own disposition_decision), not assumed from
	// the spec.
	".github/workflows/a2a-notify.yml": spaceDispositionManaged,
	".github/dependabot.yml":           spaceDispositionManaged,
	"BRANCH-PROTECTION.md":             spaceDispositionManaged,
	"CODEOWNERS":                       spaceDispositionSeeded,
	"space.yaml":                       spaceDispositionFieldManaged,
}

// spaceSeededAlternates lists the OTHER locations a seeded file may legitimately
// occupy in a space. The list for CODEOWNERS is GitHub's own resolution order
// (.github/, repo root, docs/) — a space with any one of them has a CODEOWNERS,
// whatever path the template ships.
var spaceSeededAlternates = map[string][]string{
	"CODEOWNERS": {".github/CODEOWNERS", "docs/CODEOWNERS"},
}

// spaceSeededElsewhereIn reports whether a seeded template path is already
// satisfied at one of its alternate locations in mirrorDir. Package-level
// (not a *SpaceCommand method) so spaceComputeUpdatePlanFor — the shared
// drift computation `space update` and `doctor`'s scaffolding-current row
// both call — needs no *SpaceCommand receiver to use it.
func spaceSeededElsewhereIn(mirrorDir, templatePath string, readFile func(string) ([]byte, error)) (string, bool) {
	for _, alt := range spaceSeededAlternates[templatePath] {
		if _, err := readFile(filepath.Join(mirrorDir, filepath.FromSlash(alt))); err == nil {
			return alt, true
		}
	}
	return "", false
}

// spaceUpdatePlan is the diff spaceComputeUpdatePlan produces: writes is the
// exact SubmitRequest.Files payload a non-dry-run submits; summary is the
// human-readable line per pending write; drift is advisory-only lines
// (seeded files that exist but no longer match the template — never
// written).
type spaceUpdatePlan struct {
	writes  []space.FileWrite
	summary []string
	drift   []string
	// currentFloor is the space's OWN min_binary_version as the mirror
	// carries it right now (before this update). It is what the funnel's
	// CC-085 guard must be given — the floor this write has to clear is the
	// one in force, not the one the update may be about to raise.
	currentFloor string
	// skipped lists template paths this command structurally cannot carry
	// (not space infrastructure — e.g. the template's own README.md). They
	// are REPORTED rather than silently dropped: a maintainer adding such a
	// file to the template needs to learn that `space update` will not
	// propagate it, instead of assuming it did.
	skipped []string
}

// spaceMinVersionValuePattern captures space.yaml's min_binary_version
// VALUE (spaceMinVersionPattern next to it captures only the key, for
// replacement). Tolerates the quoted form the schema also permits.
var spaceMinVersionValuePattern = regexp.MustCompile(`min_binary_version:\s*"?([0-9]+\.[0-9]+\.[0-9]+)"?`)

// spaceMinVersionValue extracts space.yaml's declared min_binary_version.
func spaceMinVersionValue(spaceYAML []byte) (string, bool) {
	m := spaceMinVersionValuePattern.FindSubmatch(spaceYAML)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}

// spaceUpdateFloor decides what a space's min_binary_version becomes on an
// update, and it is deliberately NOT "this binary's version".
//
// Two properties make that the wrong source. (1) The floor is a FLEET
// policy, not scaffolding: raising it refuses every other participant whose
// binary is older (CC-085), so one participant running `space update` with a
// fresh build would lock out the rest as a side effect of a convenience
// command. (2) Convergence: with the running binary as the source, two
// participants on different builds produce different space.yaml content and
// fight forever — AC-950.4's "re-running is a no-op" would hold only per
// binary version, which is not idempotence.
//
// The TEMPLATE's own declared floor is the SSOT (that is what "template
// drift" means, and it is the axis P33 deliberately kept separate from the
// caller's `@vX.Y.Z` ref). And the move is MONOTONE: a space already at or
// above the template's floor keeps its own — converging downward would
// weaken a guard, which no drift-sync should ever do silently.
func spaceUpdateFloor(current, templateFloor string) (next string, changed bool, err error) {
	older, err := version.OlderThan(current, templateFloor)
	if err != nil {
		return "", false, fmt.Errorf("cannot compare min_binary_version %q with the template's %q: %w", current, templateFloor, err)
	}
	if !older {
		return current, false, nil
	}
	return templateFloor, true, nil
}

// spaceComputeUpdatePlan walks c.TemplateFiles, applies spaceApplySubstitutions
// per file (spaceID/version — same substitution engine `space init` uses),
// and reconciles each path's disposition against the mirror's current
// content (read via c.readFile, DI-seamed so tests never touch a real
// filesystem). It performs NO write — pure diff, safe to call for
// --dry-run and for a real run alike.
//
// A thin wrapper over spaceComputeUpdatePlanFor — the package-level function
// doctor's "space scaffolding current" row (cmd_doctor.go) ALSO calls, so
// the two commands can never disagree about "is this space behind" (spec 35
// §9's precedent: space.IsInfrastructurePath is the one owner for the
// funnel/planner agreement; this is the same move for the drift diff
// itself). This wrapper's own behaviour is unchanged — same inputs, same
// readFile default, same output — the lift is byte-identical.
func (c *SpaceCommand) spaceComputeUpdatePlan(spaceID, version string) (spaceUpdatePlan, error) {
	return spaceComputeUpdatePlanFor(c.TemplateFiles, c.MirrorDir, spaceID, version, c.readFile)
}

// spaceComputeUpdatePlanFor is the package-level drift computation shared by
// `space update` (spaceComputeUpdatePlan, above) and `doctor`'s "space
// scaffolding current" row (doctorCheckScaffoldingCurrent, cmd_doctor.go) —
// ONE owner for "is this space behind the embedded template", never a
// second implementation. templateFiles is the embedded space-template/
// tree; mirrorDir is the space's local mirror working directory; readFile
// is DI-seamed (nil defaults to os.ReadFile) so a caller — a test, or
// doctor's own read-only check — never needs a real filesystem. It performs
// NO write — pure diff, safe for --dry-run, a real run, and doctor's
// read-only row alike.
func spaceComputeUpdatePlanFor(templateFiles fs.FS, mirrorDir, spaceID, version string, readFile func(string) ([]byte, error)) (spaceUpdatePlan, error) {
	if readFile == nil {
		readFile = os.ReadFile
	}

	var plan spaceUpdatePlan
	err := fs.WalkDir(templateFiles, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		templateData, rerr := fs.ReadFile(templateFiles, p)
		if rerr != nil {
			return fmt.Errorf("cannot read embedded %s: %w", p, rerr)
		}
		// The plan may only ever propose paths the FUNNEL can carry. The
		// funnel admits exactly space.IsInfrastructurePath (the shared
		// predicate — see its doc for why this is not a second copy);
		// anything else is refused with ErrWrongSection, which would fail
		// the entire write over a file nobody asked to update. The real
		// template carries such files (README.md), so this is not
		// hypothetical — the e2e tier caught it.
		if !space.IsInfrastructurePath(p) {
			if _, listed := spaceUpdateDispositionTable[p]; listed {
				// A disposition row that the funnel cannot carry is a
				// contradiction in this command's own configuration.
				return fmt.Errorf("disposition table lists %s, which is not a space-infrastructure path the funnel can carry", p)
			}
			plan.skipped = append(plan.skipped, p)
			return nil
		}

		substituted := spaceApplySubstitutions(p, templateData, spaceID, version)

		mirrorPath := filepath.Join(mirrorDir, filepath.FromSlash(p))
		current, rerr := readFile(mirrorPath)
		exists := true
		if rerr != nil {
			if !os.IsNotExist(rerr) {
				return fmt.Errorf("cannot inspect %s: %w", mirrorPath, rerr)
			}
			exists = false
		}

		disposition, listed := spaceUpdateDispositionTable[p]

		switch disposition {
		case spaceDispositionFieldManaged:
			// space.yaml: the template's own skeleton is irrelevant to the
			// file's BODY — only the mirror's own current bytes are
			// rewritten, and only the min_binary_version field within them
			// (reuses spaceMinVersionPattern, the exact regex `space init`
			// uses). The template IS the authority for that one value —
			// see spaceUpdateFloor for why the running binary's version is
			// deliberately not the source.
			if !exists {
				return fmt.Errorf("%s: expected in every connected space but is missing from the mirror", p)
			}
			templateFloor, ok := spaceMinVersionValue(templateData)
			if !ok {
				return fmt.Errorf("the embedded template's %s declares no min_binary_version — cannot decide the space's write floor", p)
			}
			currentFloor, ok := spaceMinVersionValue(current)
			if !ok {
				// Silently doing nothing here would leave the space
				// permanently un-floored while reporting success.
				return fmt.Errorf("%s in the mirror declares no min_binary_version — fix the space manifest before updating", p)
			}
			plan.currentFloor = currentFloor

			nextFloor, changed, ferr := spaceUpdateFloor(currentFloor, templateFloor)
			if ferr != nil {
				return ferr
			}
			if !changed {
				if currentFloor != templateFloor {
					plan.drift = append(plan.drift, fmt.Sprintf(
						"advisory: %s pins min_binary_version %s, above the template's %s — left as is (the floor never converges downward)",
						p, currentFloor, templateFloor))
				}
				return nil
			}
			updated := spaceMinVersionPattern.ReplaceAll(current, []byte("${1}"+nextFloor))
			if !bytes.Equal(updated, current) {
				plan.writes = append(plan.writes, space.FileWrite{Path: p, Content: updated})
				plan.summary = append(plan.summary, fmt.Sprintf("field-update %s (min_binary_version %s -> %s, the template's floor)", p, currentFloor, nextFloor))
			}
		case spaceDispositionSeeded:
			// A seeded file's identity is the ROLE it plays, not the path the
			// template happens to use. GitHub resolves CODEOWNERS from
			// .github/, the repo root, or docs/ — first match wins — so a
			// space that keeps its real owners in .github/CODEOWNERS already
			// HAS one. Comparing paths instead of roles would drop a root
			// CODEOWNERS full of @REPLACE_WITH_ORG placeholders next to it:
			// inert today (the .github/ copy wins), but noise in the repo,
			// an error in GitHub's CODEOWNERS UI, and a silent
			// gates-nothing takeover the day the real file moves.
			// Found on the live getvisa space, invisible to the fixtures.
			if !exists {
				if alt, found := spaceSeededElsewhereIn(mirrorDir, p, readFile); found {
					plan.drift = append(plan.drift, fmt.Sprintf(
						"advisory: template ships %s, this space keeps it at %s — left untouched (seeded, one per space)", p, alt))
					return nil
				}
			}
			switch {
			case !exists:
				plan.writes = append(plan.writes, space.FileWrite{Path: p, Content: substituted})
				plan.summary = append(plan.summary, fmt.Sprintf("add %s", p))
			case !bytes.Equal(current, substituted):
				plan.drift = append(plan.drift, fmt.Sprintf("advisory: %s exists and differs from the current template — left untouched (seeded, customize-once file)", p))
			}
		default: // spaceDispositionManaged — the fixed rows above, or an
			// unlisted template path treated the same way ONLY while it is
			// absent from the space (AC-951.1); once present, an unlisted
			// path is presumed space-owned and this command never touches
			// it again.
			if !listed && exists {
				return nil
			}
			switch {
			case !exists:
				plan.writes = append(plan.writes, space.FileWrite{Path: p, Content: substituted})
				plan.summary = append(plan.summary, fmt.Sprintf("add %s", p))
			case !bytes.Equal(current, substituted):
				plan.writes = append(plan.writes, space.FileWrite{Path: p, Content: substituted})
				plan.summary = append(plan.summary, fmt.Sprintf("update %s", p))
			}
		}
		return nil
	})
	if err != nil {
		return spaceUpdatePlan{}, err
	}
	return plan, nil
}

// spaceUpdateWired reports a clear "not wired" error when the lead has not
// yet supplied `space update`'s own DI fields — `a2a space init` never
// touches any of them and keeps working with all six left zero (this
// package's "nil means not wired" convention); `a2a space update` needs a
// connected space and refuses cleanly here rather than nil-panicking deeper
// in (e.g. on a nil Funnel).
func (c *SpaceCommand) spaceUpdateWired() error {
	if c.Funnel == nil || c.MirrorDir == "" || c.SpaceID == "" || c.OwnSystem == "" ||
		c.HostCfg == (SubmitHostConfig{}) {
		return fmt.Errorf("not wired for a connected space (Funnel/MirrorDir/SpaceID/OwnSystem/HostCfg) — connect a space first")
	}
	return nil
}

// spaceUpdateDirectiveLines renders the two admin-only migration steps as
// `scope: space` directives (spec 31's directive model, applied here per
// spec 35 §T2/AC-952.1: a2a PRINTS the exact command, a human/agent runs
// it — a2a itself calls no admin API; internal/host's Host interface
// exposes no branch-protection primitive at all, so that is structural,
// not just a convention this command happens to follow). owner/name
// identify the space's GitHub repo; baseBranch is the PR's target branch
// (branch protection is a per-branch GitHub setting).
func spaceUpdateDirectiveLines(owner, name, baseBranch string) []string {
	repo := owner + "/" + name
	return []string{
		// Stated as a CONDITION, not as a fact about this repo. a2a calls no
		// admin API — internal/host exposes no branch-protection primitive at
		// all — so this command cannot know which context protection requires
		// and prints the directive unconditionally. Asserting "this PR cannot
		// merge" is therefore a claim beyond what the code observed, and it is
		// false for every already-migrated space: it told an operator whose
		// protection was already on the compound context to go fix it, and
		// would say so again on every future `space update`, forever. A
		// directive that is wrong for the migrated majority is one people learn
		// to skip — including the time it is right.
		"scope: space directive — prerequisite (spec 35 §T3: IF protection still requires the old flat context, this PR cannot merge until that is changed)",
		"  why: the reusable-workflow caller (spec 33 §11) surfaces the compound context \"a2a-validate / validate\", never the old flat \"a2a-validate\"",
		"  skip if: your required check is already \"a2a-validate / validate\" — a2a calls no admin API, so it cannot check this for you",
		// Byte-identical to docs/runbooks/space-bootstrap.md's "P33
		// migration" step — that runbook is the SSOT an operator already
		// follows, and two different commands for one job is how a
		// half-applied protection change happens.
		fmt.Sprintf(`  run: gh api -X PUT repos/%s/branches/%s/protection/required_status_checks -f 'checks[][context]=a2a-validate / validate'`, repo, baseBranch),
		"scope: space directive — cleanup (optional: P33 removed the need for this secret)",
		fmt.Sprintf("  run: gh secret delete A2A_BINARY_FETCH_TOKEN --repo %s", repo),
	}
}

// spaceUpdatePRBody renders the PR body for a `space update` write: it must
// state that the required-check rename is a prerequisite (spec 35 §T3) —
// the PR cannot merge while protection still requires the old flat context
// — plus the same directive commands, for a reviewer who never runs
// `a2a space update` themselves.
func spaceUpdatePRBody(owner, name, baseBranch string) string {
	var b strings.Builder
	b.WriteString("a2a space update: sync this space's scaffolding with the current embedded template.\n\n")
	b.WriteString("Prerequisite (spec 35 §T3): IF branch protection's required status check is still the flat `a2a-validate`, this PR cannot merge until it is renamed to the compound `a2a-validate / validate` — the reusable-workflow caller shape (spec 33 §11) never reports the old flat context, so the PR would otherwise hang \"Expected — waiting for status to be reported\". Already on the compound context? Nothing to do; a2a calls no admin API and cannot check this for you.\n\n")
	for _, line := range spaceUpdateDirectiveLines(owner, name, baseBranch) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// runUpdate implements `a2a space update [--space <id>] [--dry-run] [--wait]` (spec
// 35): diff the connected space's scaffolding against the embedded
// template, print it, and (without --dry-run) submit it through the
// existing write funnel with AllowSpaceInfrastructure set. Exit codes:
// 2 = usage error; 1 = not wired / version guard / diff error / funnel
// error; 0 = success (including the idempotent already-current no-op).
func (c *SpaceCommand) runUpdate(ctx context.Context, args []string, stdio IO) int {
	fset := flag.NewFlagSet("space update", flag.ContinueOnError)
	fset.SetOutput(stdio.Stderr)
	spaceFlag := fset.String("space", "", "space id (defaults to the connected space)")
	dryRun := fset.Bool("dry-run", false, "print the scaffolding diff without writing or pushing")
	wait := fset.Bool("wait", false, "after opening the PR, poll until it merges and then refresh the local mirror")
	if err := fset.Parse(args); err != nil {
		return 2
	}
	if fset.NArg() > 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a space update [--space <id>] [--dry-run] [--wait]")
		return 2
	}
	if *wait && *dryRun {
		_, _ = fmt.Fprintln(stdio.Stderr, "space update: --wait and --dry-run are contradictory: a dry run opens no PR to wait for")
		return 2
	}
	if *wait && c.AwaitPR == nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "space update: --wait is not wired in this build; open the PR without it and run `a2a sync` after it merges")
		return 1
	}

	if err := c.spaceUpdateWired(); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: %v\n", err)
		return 1
	}
	if *spaceFlag != "" && *spaceFlag != c.SpaceID {
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: unknown space %q (connected space is %q)\n", *spaceFlag, c.SpaceID)
		return 1
	}

	// Capability pre-flight. A real run REFUSES here rather than computing a
	// plan, printing it in full with both directives, and only then dying on
	// a raw git push rejection — which reads as "it worked, then broke".
	// --dry-run still shows the plan (that is what it is for) but says the
	// run would be refused, so the scope is learned BEFORE it matters.
	scopeRefusal, scopeNote := c.spaceCheckWorkflowScope(ctx)
	if scopeNote != "" {
		_, _ = fmt.Fprintf(stdio.Stdout, "space update: %s\n", scopeNote)
	}
	if scopeRefusal != "" && !*dryRun {
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: %s\n", scopeRefusal)
		return 1
	}

	// Version guard (fail closed), same as `space init` — a dev build must
	// not pin a caller ref / min_binary_version to a release that does not
	// exist.
	cleanVersion, err := spaceCleanVersion(c.Version)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: refusing to update — %v\n", err)
		return 1
	}

	plan, err := c.spaceComputeUpdatePlan(c.SpaceID, cleanVersion)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: %v\n", err)
		return 1
	}

	for _, note := range plan.drift {
		_, _ = fmt.Fprintf(stdio.Stdout, "space update: %s\n", note)
	}
	if len(plan.skipped) > 0 {
		_, _ = fmt.Fprintf(stdio.Stdout,
			"space update: not propagated (outside the space-infrastructure paths this command may write): %s\n",
			strings.Join(plan.skipped, ", "))
	}

	if len(plan.writes) == 0 {
		_, _ = fmt.Fprintf(stdio.Stdout, "space update: %s is already current with the embedded template — nothing to do\n", c.SpaceID)
		return 0
	}

	_, _ = fmt.Fprintln(stdio.Stdout, "space update: scaffolding diff against the embedded template:")
	for _, line := range plan.summary {
		_, _ = fmt.Fprintf(stdio.Stdout, "  %s\n", line)
	}

	baseBranch := c.HostCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	for _, line := range spaceUpdateDirectiveLines(c.HostCfg.Repo.Owner, c.HostCfg.Repo.Name, baseBranch) {
		_, _ = fmt.Fprintf(stdio.Stdout, "space update: %s\n", line)
	}

	if *dryRun {
		if scopeRefusal != "" {
			_, _ = fmt.Fprintf(stdio.Stdout, "space update: WOULD BE REFUSED — %s\n", scopeRefusal)
		}
		_, _ = fmt.Fprintln(stdio.Stdout, "space update: --dry-run — no files written, nothing pushed")
		return 0
	}

	commitMsg := "a2a(space-update): sync scaffolding with the embedded template"
	req := space.SubmitRequest{
		RepoDir:                  c.MirrorDir,
		System:                   c.OwnSystem,
		Verb:                     "space-update",
		ArtifactID:               "scaffolding",
		Files:                    plan.writes,
		CommitMessage:            commitMsg,
		CommitAuthorName:         c.HostCfg.CommitAuthorName,
		CommitAuthorEmail:        c.HostCfg.CommitAuthorEmail,
		RemoteURL:                c.HostCfg.RemoteURL,
		Repo:                     c.HostCfg.Repo,
		BaseBranch:               baseBranch,
		PRTitle:                  commitMsg,
		PRBody:                   spaceUpdatePRBody(c.HostCfg.Repo.Owner, c.HostCfg.Repo.Name, baseBranch),
		Credential:               c.HostCfg.Credential,
		MinBinaryVersion:         plan.currentFloor,
		AllowSpaceInfrastructure: true,
	}

	result, err := c.Funnel.Submit(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: %v\n", err)
		return 1
	}

	switch result.State {
	case space.WriteStateAlreadyOpen, space.WriteStateAlreadyMerged:
		_, _ = fmt.Fprintf(stdio.Stdout, "space update: already submitted (PR %s, %s)\n", result.PRURL, result.State)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout, "space update: opened PR %s (%s)\n", result.PRURL, result.State)
		warnAutoMerge(stdio, "space update", result.AutoMergeNote)
	}
	if !*wait {
		spaceUpdateNextStep(stdio)
		return 0
	}
	return c.spaceUpdateWaitForMerge(ctx, stdio, result)
}

// spaceUpdateWaitForMerge is `--wait`: it observes the pull request until the
// host reports it merged, then refreshes the mirror, which is the step whose
// absence made `a2a version` contradict a completed update.
//
// It OBSERVES and never merges. The distinction is the whole reason this is
// opt-in rather than the default: merging is the space's decision, gated by
// its own CI and branch protection and sometimes by a person, and a CLI that
// blocks by default on a human gate is a hung command rather than a complete
// one. Today's own PR could not merge until a required check context was
// changed, and the command said so — it must not have been waiting silently
// while that was true.
func (c *SpaceCommand) spaceUpdateWaitForMerge(ctx context.Context, stdio IO, result space.WriteResult) int {
	if result.Branch == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "space update: --wait has no branch to observe (the write reported none); run `a2a sync` after the PR merges")
		return 1
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "space update: waiting for %s to merge — this observes, it never merges; Ctrl-C is safe and changes nothing\n", result.PRURL)
	awaited, err := c.AwaitPR(ctx, result.Branch)
	if err != nil {
		// A refusal here is about the PR, not about the update: the branch is
		// pushed and the PR is open either way, so the remedy is always the
		// same two words rather than a re-run of this command.
		_, _ = fmt.Fprintf(stdio.Stderr, "space update: --wait stopped: %v\n", err)
		_, _ = fmt.Fprintln(stdio.Stderr, "space update:   the PR is still open and unchanged; resolve it, then run `a2a sync`")
		return 1
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "space update: merged (%s), and the local mirror is refreshed — `a2a version` now reads the updated space.\n", awaited.PRURL)
	return 0
}

// spaceUpdateNextStep names what actually closes this loop, because the
// command's last line otherwise reads as completion.
//
// It does not: this command OPENS A PULL REQUEST and writes nothing to the
// space. Even after that PR merges, the local mirror is unchanged until
// something fetches it — and `a2a version`, `a2a doctor` and the dashboard all
// read the mirror, never the network. On 2026-08-23 an operator ran this
// command, merged its PR, and was then told by `a2a version` that the space
// was still BEHIND and that the remedy was to run this command again. Nothing
// was broken; one step was missing and nothing named it.
func spaceUpdateNextStep(stdio IO) {
	_, _ = fmt.Fprintln(stdio.Stdout, "space update: next: this opened a PR and changed nothing in the space yet.")
	_, _ = fmt.Fprintln(stdio.Stdout, "space update:   after it merges, run `a2a sync` — the local mirror is what")
	_, _ = fmt.Fprintln(stdio.Stdout, "space update:   `a2a version`, `a2a doctor` and the dashboard read, and it does")
	_, _ = fmt.Fprintln(stdio.Stdout, "space update:   not move on its own. `a2a space update --wait` does both for you.")
}
