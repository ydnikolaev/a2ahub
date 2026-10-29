// OP-201 `a2a init`, OP-202 `a2a connect`/`a2a disconnect` (spec 06 T1).
// This file's only package-level symbols are InitCommand/ConnectCommand/
// DisconnectCommand + their NewXCommand constructors plus file-private,
// uniquely-named helpers (init*/connect*/disconnect* prefix) — no shared
// helper, no package var, per this phase's plan Placement decision
// (avoids collision with P7/P8/P9's parallel verb files in this package).
package cli

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/surface"
	"gopkg.in/yaml.v3"
)

// initCanonicalPath returns p as an absolute path with symlinks resolved. The
// deepest EXISTING ancestor is resolved with EvalSymlinks and the missing tail
// is appended unchanged, so a path that does not exist yet (a local space about
// to be created) canonicalizes to exactly where it will exist. For an existing
// path this is filepath.EvalSymlinks(filepath.Abs(p)).
//
// It is the one spelling `space init --local`, `connect` and `init --space`
// persist for a local space: the write journal keys a scheme-less remote on its
// absolute path (internal/space/prepared.go), so `/var/...` against
// `/private/var/...` on macOS, a relative path from another cwd, or a trailing
// slash would otherwise name the same repository three ways.
func initCanonicalPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	head, tail := abs, ""
	for {
		resolved, rerr := filepath.EvalSymlinks(head)
		if rerr == nil {
			return filepath.Join(resolved, tail), nil
		}
		if !errors.Is(rerr, fs.ErrNotExist) {
			return "", rerr
		}
		parent := filepath.Dir(head)
		if parent == head {
			return "", rerr
		}
		tail = filepath.Join(filepath.Base(head), tail)
		head = parent
	}
}

// initLocalLocator returns the spelling `connect` and `init --space` persist for
// a space locator. A locator that addresses a directory on this machine (an
// absolute path, or a relative one that names an existing directory) is
// canonicalized with initCanonicalPath; a URL (anything with a scheme), an
// scp-style address, a path that does not exist, or a path that cannot be
// resolved is returned exactly as given. A relative path is made absolute BEFORE
// the host is opened because the host classifies only an absolute path as a
// path.
func initLocalLocator(raw string) string {
	if raw == "" || strings.Contains(raw, "://") {
		return raw
	}
	if colon := strings.IndexByte(raw, ':'); colon >= 0 && (strings.IndexByte(raw, '/') < 0 || colon < strings.IndexByte(raw, '/')) {
		return raw // scp-style git address: user@host:owner/name
	}
	if !filepath.IsAbs(raw) {
		info, err := os.Stat(raw)
		if err != nil || !info.IsDir() {
			return raw
		}
	}
	canonical, err := initCanonicalPath(raw)
	if err != nil {
		return raw
	}
	return canonical
}

// initOpenLocator opens the host for a space locator and returns the spelling
// to persist along with it. A locator that is a space on this machine (the
// opened host declares OnThisMachine) is persisted canonicalized; every other
// locator is persisted exactly as given, because rewriting the spelling of a
// GitHub-kind space would make `connect` stop recognising an entry an earlier
// version wrote. The host is nil when no opener is wired or the locator cannot
// be opened, and the decisions that read it then keep today's behaviour.
func initOpenLocator(open host.Opener, raw string) (string, host.Host) {
	if open == nil {
		return raw, nil
	}
	if canonical := initLocalLocator(raw); canonical != raw {
		if h, _, err := open(canonical); err == nil && h != nil && h.Capabilities().OnThisMachine {
			return canonical, h
		}
	}
	h, _, err := open(raw)
	if err != nil {
		return raw, nil
	}
	return raw, h
}

// initHostNeedsCredential is true unless h declares that it needs none; a nil
// host (nothing wired, or an address that would not open) keeps seeding one.
func initHostNeedsCredential(h host.Host) bool {
	return h == nil || h.Capabilities().NeedsCredential
}

// initAgentsPointerStart/End delimit the a2ahub pointer block in AGENTS.md.
// The start marker is the idempotency key: a re-run that finds it makes no
// change. It also tells a human/agent the block is tool-managed — edit around
// it, not inside — and how to refresh (the same flag).
const (
	initAgentsPointerStart = "<!-- a2ahub:pointer:start (managed by `a2a init --agents-pointer`) -->"
	initAgentsPointerEnd   = "<!-- a2ahub:pointer:end -->"
)

// initAgentsPointerBlock renders the marker-wrapped pointer: what a2ahub is,
// where the local operating skill lives, the session-start floor, and the
// binary-is-truth rule. AGENTS.md reaches Codex (and any other
// AGENTS.md-reading surface, internal/surface.Registry's ReadsAgentsMD
// rows) directly; Claude Code does NOT read AGENTS.md (verified against
// code.claude.com/docs/en/memory, 2026-07-23) — it is reached instead via
// the skill link (`a2a skill link`, P32) plus a CLAUDE.md pointer
// (ensureClaudeMdPointer). The block's own prose stays provider-agnostic —
// it points at the binary and the installed skill, never restating command
// or validation rules.
func initAgentsPointerBlock() string {
	return initAgentsPointerStart + "\n" +
		"## a2ahub\n\n" +
		"This repo participates in **a2ahub** — typed cross-system artifact exchange " +
		"(questions, work requests, contracts, decisions) with other systems' agents.\n\n" +
		"- **Operating skill:** `.a2ahub/skill/SKILL.md` (install / refresh with `a2a skill install`).\n" +
		// The normative session-start floor is plan §8.1 (D-021), which the
		// installed skill quotes verbatim in loops.md. This pointer used to
		// paraphrase it as "run a2a doctor, then a2a inbox" — dropping both
		// `--actionable` (the normative filtered union) and the outbox check
		// entirely. This block is the highest-read-rate artifact in the whole
		// delivery chain: it is what an agent sees before it has opened the
		// skill. A paraphrase that disagrees with the floor is worse here than
		// anywhere else, so it now names the floor's own commands and defers
		// for the rest.
		"- **Session start (the floor, per the skill's loops.md §8.1):** `a2a inbox --actionable`, " +
		"then `a2a outbox --attention`; act on blocking items. Run `a2a doctor` when something looks wrong.\n" +
		"- **Source of truth:** the `a2a` binary — `a2a <verb>` and `a2a validate`; never " +
		"hand-edit space files. The skill documents, the binary validates.\n" +
		initAgentsPointerEnd + "\n"
}

// --- init (OP-201) -------------------------------------------------------

// InitCommand implements `a2a init`: fully flag-driven non-interactive
// mode (`--system --space ...`) is normative (§7.2 OP-201, quoted); it
// writes .a2a/config.yaml, is idempotent on identical re-run ("already
// configured"), and NEVER blocks on stdin — a missing required flag is a
// usage error (exit 2), not a prompt. TTY interactive prompting is
// documented sugar this phase does not implement (see this phase's
// Deviations report): implementing a real prompt loop is exactly where a
// hang bug lives, and AC row 6 only requires the flag-driven path to
// never block.
type InitCommand struct {
	projectConfigPath string

	// MachineConfigPath is FIX B's DI seam (spec 18 §T1/§8): when the
	// wiring layer sets this (cmd/a2a/wire.go's init closure, mirroring
	// how the validate closure sets CIGitHubActor), Run seeds a
	// `~/.config/a2a/config.yaml` skeleton on first run so `a2a submit`
	// never dies "no machine config" before an operator has ever run
	// `a2a doctor`. Left empty (e.g. the catalog/test construction path),
	// this is a no-op — no behavior change.
	MachineConfigPath string

	// AgentsPath is the consumer repo's AGENTS.md path, DI'd from wire.go
	// (<projectRoot>/AGENTS.md). The pointer is written by DEFAULT (opt out with
	// --no-agents-pointer); left empty (catalog/test path), the step is a no-op.
	AgentsPath string

	// SkillFiles + SkillTarget + Version drive the default-on skill install
	// (opt out with --no-skill). SkillFiles is the embedded tree (skill.Files,
	// DI'd from wire.go); SkillTarget is where it lands (<projectRoot>/.a2ahub/
	// skill). Left nil/empty (catalog/test path), the skill step is a no-op.
	SkillFiles  fs.FS
	SkillTarget string
	Version     string

	// ProjectRoot is the consumer repo's root, DI'd from wire.go, used by
	// the default-on surface-link step (linkDetectedSurfaces, opt out with
	// --no-skill-link). Left empty (catalog/test path), the step is a no-op.
	ProjectRoot string

	// ClaudeMdPath is the consumer repo's CLAUDE.md path, DI'd from wire.go
	// (<projectRoot>/CLAUDE.md). Governed by the SAME --no-agents-pointer
	// flag as AgentsPath (spec 32 §2.3's CLAUDE.md three-way). Left empty
	// (catalog/test path), the step is a no-op.
	ClaudeMdPath string

	// Open resolves a space locator to its host, so a space whose host needs no
	// credential (a space on this machine) gets no credential reference seeded.
	// Nil (catalog and tests) keeps seeding one for every space.
	Open host.Opener

	// writeFile/loadProjectConfig are DI seams (rails, mirrors
	// DoctorCommand's own convention) so tests never touch a real
	// .a2a/config.yaml path. defaultCredentialRef is the same kind of seam
	// over the credential-reference probe (nil = internal/space's real one,
	// which shells out to `gh`).
	writeFile            func(path string, data []byte, perm os.FileMode) error
	loadProjectConfig    func(path string) (space.ProjectConfig, error)
	defaultCredentialRef func(ctx context.Context, spaceID string) string
}

// SetDefaultCredentialRefForTest overrides the credential-reference probe
// (test-only DI, rails anti-pattern #10 convention) so tests never shell
// out to the ambient `gh`.
func (c *InitCommand) SetDefaultCredentialRefForTest(f func(ctx context.Context, spaceID string) string) {
	c.defaultCredentialRef = f
}

// NewInitCommand constructs the init command. projectConfigPath is
// `.a2a/config.yaml`'s path (§7.4).
func NewInitCommand(projectConfigPath string) *InitCommand {
	return &InitCommand{
		projectConfigPath: projectConfigPath,
		writeFile:         os.WriteFile,
		loadProjectConfig: space.LoadProjectConfig,
	}
}

// Name implements cli.Command.
func (c *InitCommand) Name() string { return "init" }

// Synopsis implements cli.Command.
func (c *InitCommand) Synopsis() string {
	return "non-interactive project setup: config + skill install + surface link + AGENTS.md/CLAUDE.md pointer (--no-skill / --no-skill-link / --no-agents-pointer to opt out)"
}

// Run implements cli.Command. Exit codes: 2 = usage error (missing
// --system, or zero --space values); 1 = config write failure; 0 =
// success (including the idempotent "already configured" no-op).
func (c *InitCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	system := fs.String("system", "", "this project's own system id (required)")
	var spaces initStringList
	fs.Var(&spaces, "space", "connected space repo URL (repeatable; at least one required)")
	noSkill := fs.Bool("no-skill", false, "do NOT install the a2ahub skill tree (installed by default)")
	noAgentsPointer := fs.Bool("no-agents-pointer", false, "do NOT add the a2ahub pointers to AGENTS.md and CLAUDE.md (added by default)")
	noSkillLink := fs.Bool("no-skill-link", false, "do NOT link the installed skill into detected agent surfaces (linked by default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *system == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "init: --system is required (non-interactive mode never prompts)")
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a init --system <id> --space <repo-url>... [--no-skill] [--no-skill-link] [--no-agents-pointer]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-send"))
		return 2
	}
	if len(spaces) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "init: at least one --space is required (non-interactive mode never prompts)")
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a init --system <id> --space <repo-url>... [--no-skill] [--no-skill-link] [--no-agents-pointer]")
		_, _ = fmt.Fprintln(stdio.Stderr, workflowLine("loop-send"))
		return 2
	}

	existing, hasExisting := c.loadExisting()
	refs := make([]space.Ref, 0, len(spaces))
	noCredential := make(map[string]struct{}) // locators whose host needs none
	for _, s := range spaces {
		locator, h := initOpenLocator(c.Open, s)
		// The mirror location is the same key `connect` computes and pins
		// (initSpaceIDFromURL of the locator), so the resolver answers the
		// path `connect` clones into from the very first read: init never
		// clones, but a read verb that runs before `connect` does, and with no
		// location it would clone into a directory `connect` then abandons.
		if err := space.CheckMirrorKey("id", initSpaceIDFromURL(locator)); err != nil {
			return spaceRefuse(stdio, "init --space "+s+": name the space", err.Error(),
				"pass the space repository's own URL or path, whose last element is the space's name")
		}
		ref := space.Ref{ID: initSpaceIDFromURL(locator), RepoURL: locator, MirrorLocation: initSpaceIDFromURL(locator)}
		if hasExisting {
			// A re-init that rewrites the config must not drop what `connect`
			// already pinned for the same repository.
			if prev, ok := initFindRef(existing, locator); ok && prev.MirrorLocation != "" {
				ref.MirrorLocation = prev.MirrorLocation
			}
		}
		refs = append(refs, ref)
		if !initHostNeedsCredential(h) {
			noCredential[locator] = struct{}{}
		}
	}
	cfg := space.ProjectConfig{System: *system, Spaces: refs}
	if hasExisting {
		// init owns system/space onboarding, not the skill install location.
		// Preserve the additive project setting when init refreshes the rest of
		// the config so a custom install cannot silently snap back to default.
		cfg.SkillDir = existing.SkillDir
	}

	// FIX B runs before the idempotent short-circuit so a repeat `a2a
	// init` still ensures/hints the machine config (e.g. an operator who
	// deleted it after a first run).
	if code := c.ensureMachineConfig(ctx, refs, noCredential, stdio); code != 0 {
		return code
	}

	// Default-on onboarding (opt-out), before the idempotent short-circuit so a
	// repeat `a2a init` still refreshes both. Both are best-effort extras — a
	// failure warns but never fails init's primary job (the config write). The
	// consent for writing into the consumer's AGENTS.md is the act of running
	// `a2a init` in that repo; --no-agents-pointer opts out (D-021).
	if !*noSkill {
		c.installSkill(stdio)
	}
	if !*noSkillLink {
		c.linkDetectedSurfaces(stdio)
	}
	if !*noAgentsPointer {
		_ = c.ensureAgentsPointer(stdio) // prints its own error; never fatal to init
		c.ensureClaudeMdPointer(stdio)   // best-effort; never fatal to init
	}

	if hasExisting && initConfigsEquivalent(existing, cfg) {
		_, _ = fmt.Fprintln(stdio.Stdout, "init: already configured")
		initPrintNextSteps(stdio, refs, noCredential)
		c.printCommitTable(stdio)
		return 0
	}

	raw, err := yaml.Marshal(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot encode config: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(c.projectConfigPath), 0o755); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot create config directory: %v\n", err)
		return 1
	}
	if err := c.writeFile(c.projectConfigPath, raw, 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot write %s: %v\n", c.projectConfigPath, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "init: wrote %s\n", c.projectConfigPath)
	initPrintNextSteps(stdio, refs, noCredential)
	// Named here because this is the step nobody discovers on their own: every
	// trigger a2a ships is human-initiated, so a repo without a session-start
	// hook and a scheduled poll goes quiet while both sides believe they are
	// waiting on the other. It is a pointer, never a write — the harness config
	// belongs to whoever owns the repo (D-021).
	_, _ = fmt.Fprintln(stdio.Stdout, "init: set up the automatic checks so a counterparty's message cannot sit unread —")
	_, _ = fmt.Fprintln(stdio.Stdout, "init:   a session-start hook running `a2a sync && a2a inbox --actionable`, and a")
	_, _ = fmt.Fprintln(stdio.Stdout, "init:   scheduled workflow in THIS repo polling it (`--exit-code` gives severity).")
	_, _ = fmt.Fprintln(stdio.Stdout, "init:   see the skill's onboarding page, \"Making the loop run without a human\"")
	// Named here for the same reason as the automation pointer above: feed
	// liveness, retraction, and bindings are conventions on TOP of the
	// shipped schema, not new fields the templates surface on their own —
	// an agent that never opens reference/ would not discover them exist.
	_, _ = fmt.Fprintln(stdio.Stdout, "init:   also see reference/status-announcements.md, retraction.md, and bindings.md")
	c.printCommitTable(stdio)
	return 0
}

// initFindRef returns the ref of cfg that names repoURL.
func initFindRef(cfg space.ProjectConfig, repoURL string) (space.Ref, bool) {
	for _, r := range cfg.Spaces {
		if r.RepoURL == repoURL {
			return r, true
		}
	}
	return space.Ref{}, false
}

// initPrintNextSteps prints the connect hint per KIND of space, never one
// sentence for all of them: a space whose host needs a credential gets the
// credential wording, a space on this machine does not. Each distinct line is
// printed once, so a config of three GitHub spaces reads like a config of one.
// The doctor line follows the same split.
func initPrintNextSteps(stdio IO, refs []space.Ref, noCredential map[string]struct{}) {
	const (
		hostedHint = "init: next: a2a connect <url> (registers it, clones its mirror and checks the credential)"
		localHint  = "init: next: a2a connect <path> (registers it and clones its mirror)"
	)
	var needsCred, noCred bool
	for _, r := range refs {
		if _, none := noCredential[r.RepoURL]; none {
			noCred = true
		} else {
			needsCred = true
		}
	}
	if needsCred {
		_, _ = fmt.Fprintln(stdio.Stdout, hostedHint)
	}
	if noCred {
		_, _ = fmt.Fprintln(stdio.Stdout, localHint)
	}
	if needsCred {
		_, _ = fmt.Fprintln(stdio.Stdout, "init: run `a2a doctor` to verify credentials and space access")
	} else {
		_, _ = fmt.Fprintln(stdio.Stdout, "init: run `a2a doctor` to verify space access")
	}
}

// Markers the onboarding page wraps its commit table in. They are HTML
// comments, invisible when the page renders; InitCommitTable reads the block
// between them, so the table is typed in exactly one place.
const (
	initCommitTableBegin = "<!-- a2a:commit-table:begin -->"
	initCommitTableEnd   = "<!-- a2a:commit-table:end -->"
	// initOnboardingPage is the page's path inside the embedded skill tree.
	initOnboardingPage = "a2ahub/onboarding.md"
)

// InitCommitTable extracts the commit table from the onboarding page of the
// embedded skill tree: the lines between the begin and end markers with the
// blockquote prefix removed. ok is false when files is nil, the page is
// absent, or either marker is missing.
func InitCommitTable(files fs.FS) (table string, ok bool) {
	if files == nil {
		return "", false
	}
	raw, err := fs.ReadFile(files, initOnboardingPage)
	if err != nil {
		return "", false
	}
	var block []string
	inside, closed := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case !inside && strings.TrimSpace(line) == initCommitTableBegin:
			inside = true
		case inside && strings.TrimSpace(line) == initCommitTableEnd:
			closed = true
		case inside && !closed:
			switch {
			case strings.HasPrefix(line, "> "):
				line = line[2:]
			case line == ">":
				line = ""
			}
			block = append(block, line)
		}
		if closed {
			break
		}
	}
	if !closed {
		return "", false
	}
	return strings.Join(block, "\n") + "\n", true
}

// printCommitTable prints what to commit after init, from the one text the
// onboarding page carries. It never fails init: with no embedded tree (the
// catalog and test path) or a page missing a marker it says so on stderr.
func (c *InitCommand) printCommitTable(stdio IO) {
	table, ok := InitCommitTable(c.SkillFiles)
	if !ok {
		_, _ = fmt.Fprintln(stdio.Stderr, "init: commit table unavailable in this build")
		return
	}
	_, _ = fmt.Fprintln(stdio.Stdout, "init: what to commit")
	_, _ = fmt.Fprint(stdio.Stdout, table)
}

func (c *InitCommand) loadExisting() (space.ProjectConfig, bool) {
	cfg, err := c.loadProjectConfig(c.projectConfigPath)
	if err != nil {
		return space.ProjectConfig{}, false
	}
	return cfg, true
}

// ensureMachineConfig implements FIX B (spec 18 §T1/§8): a no-op unless
// MachineConfigPath is wired (config/secrets rail — internal/cli never
// reads os.UserHomeDir/os.Getenv itself; the path is always DI'd in from
// wire.go). When wired and no machine config exists yet, it seeds a
// skeleton — mirror_root plus a WORKING credential REFERENCE per connected
// space (never a literal secret): `cmd:gh auth token` when the GitHub CLI
// is installed and authenticated, else `env:A2A_TOKEN_<SPACE_ID>`. The
// commented-out placeholder this used to write meant a fresh install's
// first `a2a submit`/`a2a doctor` was red until the operator hand-edited
// YAML, which is precisely the friction the install is supposed to remove.
// An existing machine config is never touched (it may carry the operator's
// own comments and entries) — it only gets the hint printed. Every
// connected space's exact credential env var is printed either way, so a
// repeat `a2a init` still re-surfaces it.
func (c *InitCommand) ensureMachineConfig(ctx context.Context, refs []space.Ref, noCredential map[string]struct{}, stdio IO) int {
	if c.MachineConfigPath == "" {
		return 0
	}

	if _, err := os.Stat(c.MachineConfigPath); err != nil {
		if !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot stat %s: %v\n", c.MachineConfigPath, err)
			return 1
		}

		// mirror_root defaults to a "mirrors" sibling of the machine
		// config file itself (e.g. ~/.config/a2a/mirrors) — derived
		// purely from the already-DI'd path's own base, never from
		// os.UserHomeDir/os.Getenv read inside this package.
		mirrorRoot := filepath.Join(filepath.Dir(c.MachineConfigPath), "mirrors")
		skeleton, err := yaml.Marshal(space.MachineConfig{MirrorRoot: mirrorRoot})
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot encode machine config skeleton: %v\n", err)
			return 1
		}
		// space.MachineConfig.Credentials carries `yaml:"credentials,omitempty"`
		// (internal/space is read-only here), so the map never
		// round-trips through yaml.Marshal — the `credentials:` block is
		// appended verbatim instead. This is still a valid, parseable
		// MachineConfig: only the presentation, not the schema, is
		// hand-assembled.
		var raw []byte
		raw = append(raw, "# a2a machine config — personal, per-machine credential REFERENCES only (never commit this file; §7.4).\n"...)
		raw = append(raw, skeleton...)
		// A space whose host needs no credential gets no reference, and the
		// probe behind credentialRef is not called for it.
		var entries []byte
		for _, ref := range refs {
			if _, none := noCredential[ref.RepoURL]; none {
				continue
			}
			entries = append(entries, "  "+ref.ID+": "+c.credentialRef(ctx, ref.ID)+"\n"...)
		}
		if len(entries) > 0 {
			raw = append(raw, "credentials:\n"...)
			raw = append(raw, entries...)
		}

		if err := os.MkdirAll(filepath.Dir(c.MachineConfigPath), 0o755); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot create machine config directory: %v\n", err)
			return 1
		}
		if err := c.writeFile(c.MachineConfigPath, raw, 0o600); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot write %s: %v\n", c.MachineConfigPath, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "init: wrote machine config skeleton %s\n", c.MachineConfigPath)
	}

	return 0
}

// credentialRef picks the machine-config credential reference to seed//hint
// for spaceID, through the DI'd seam (tests pin it; wire.go leaves it nil,
// which falls back to internal/space's own probe — the only layer allowed
// to look at the machine's environment).
func (c *InitCommand) credentialRef(ctx context.Context, spaceID string) string {
	if c.defaultCredentialRef != nil {
		return c.defaultCredentialRef(ctx, spaceID)
	}
	return space.DefaultCredentialReference(ctx, spaceID)
}

// installSkill materializes the embedded a2ahub skill tree under SkillTarget
// (default onboarding, opt out with --no-skill). Best-effort: a foreign target
// or a write error prints a note and returns — it never fails init's primary
// config write. A no-op when SkillFiles/SkillTarget are unwired (catalog/test).
func (c *InitCommand) installSkill(stdio IO) {
	if c.SkillFiles == nil || c.SkillTarget == "" {
		return
	}
	written, err := installSkillTree(c.SkillFiles, c.SkillTarget, c.Version, false)
	switch {
	case errors.Is(err, errSkillForeignTarget):
		_, _ = fmt.Fprintf(stdio.Stdout,
			"init: skipped skill install — %s has non-a2ahub content (run `a2a skill install --force` to overwrite)\n", c.SkillTarget)
	case err != nil:
		_, _ = fmt.Fprintf(stdio.Stderr, "init: skill install skipped: %v\n", err)
	default:
		_, _ = fmt.Fprintf(stdio.Stdout, "init: installed a2ahub skill (%d files) to %s\n", written, c.SkillTarget)
	}
}

// linkDetectedSurfaces installs a discovery entry (P32, spec 32 §2.2) for
// every agent surface this repo already shows — best-effort, exactly like
// installSkill: a foreign link target or a write error prints a note and
// continues, it never fails init's primary job. A no-op when ProjectRoot is
// unwired (catalog/test), or when the SSOT skill tree is not installed
// (installSkill's own target — a link to a missing tree is a dangling
// pointer, so this waits for a successful install; --no-skill combined with
// --no-skill-link both unset degrades to this silent no-op rather than a
// spurious error, matching installSkill's own best-effort framing).
func (c *InitCommand) linkDetectedSurfaces(stdio IO) {
	if c.ProjectRoot == "" {
		return
	}
	ssotRel := skillDefaultDir
	if _, err := os.Stat(filepath.Join(c.ProjectRoot, ssotRel, "SKILL.md")); err != nil {
		return
	}
	targets := surface.Detect(c.ProjectRoot)
	for _, s := range targets {
		result, err := surface.Link(c.ProjectRoot, s, ssotRel, false)
		switch {
		case errors.Is(err, surface.ErrForeignLinkTarget):
			_, _ = fmt.Fprintf(stdio.Stdout,
				"init: skipped skill link for %s — %s already exists and is not an a2ahub link (run `a2a skill link --surface %s --force` to overwrite)\n",
				s.ID, filepath.Join(s.SkillsHome, "a2ahub"), s.ID)
		case err != nil:
			_, _ = fmt.Fprintf(stdio.Stderr, "init: skill link for %s skipped: %v\n", s.ID, err)
		default:
			_, _ = fmt.Fprintf(stdio.Stdout, "init: linked %s: %s (%s)\n", s.ID, result.Path, result.Mode)
		}
	}
}

// ensureClaudeMdPointer implements spec 32 §2.3's CLAUDE.md three-way, the
// sibling of ensureAgentsPointer for the one verified surface that does NOT
// read AGENTS.md (Claude Code). Governed by the same --no-agents-pointer
// flag as ensureAgentsPointer (the flag means "write context-file
// pointers", not "AGENTS.md only"). A no-op when ClaudeMdPath is unwired
// (catalog/test path).
//
//  1. CLAUDE.md exists and already imports "@AGENTS.md" — the bridge is
//     already in place, nothing to do.
//  2. CLAUDE.md exists (no @AGENTS.md import) — append the SAME
//     marker-fenced pointer block ensureAgentsPointer writes, idempotent on
//     the same start marker.
//  3. Neither exists — do NOT create CLAUDE.md a consumer never asked for;
//     the skill link (linkDetectedSurfaces) is discovery-sufficient on its
//     own.
func (c *InitCommand) ensureClaudeMdPointer(stdio IO) {
	if c.ClaudeMdPath == "" {
		return
	}
	existing, err := os.ReadFile(c.ClaudeMdPath)
	if err != nil {
		if !os.IsNotExist(err) {
			_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot read %s: %v\n", c.ClaudeMdPath, err)
		}
		return // absent — skill link alone is discovery-sufficient (spec 32 §2.3.3)
	}
	if bytes.Contains(existing, []byte("@AGENTS.md")) {
		_, _ = fmt.Fprintf(stdio.Stdout, "init: %s already bridged to AGENTS.md (@AGENTS.md import present)\n", c.ClaudeMdPath)
		return
	}
	if bytes.Contains(existing, []byte(initAgentsPointerStart)) {
		_, _ = fmt.Fprintf(stdio.Stdout, "init: a2ahub pointer already present in %s\n", c.ClaudeMdPath)
		return
	}

	out := append([]byte(nil), existing...)
	if len(existing) > 0 {
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		out = append(out, '\n')
	}
	out = append(out, initAgentsPointerBlock()...)

	if err := c.writeFile(c.ClaudeMdPath, out, 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot write %s: %v\n", c.ClaudeMdPath, err)
		return
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "init: appended a2ahub pointer to %s\n", c.ClaudeMdPath)
}

// ensureAgentsPointer appends the a2ahub pointer block to AGENTS.md (default
// onboarding, opt out with --no-agents-pointer). Safety (operator requirement
// 2026-07-23): it
// NEVER overwrites — it APPENDS to whatever the consumer already has (creating
// the file if absent), and it is idempotent (a file already carrying the start
// marker is left untouched). A no-op when AgentsPath is unwired (catalog/test).
func (c *InitCommand) ensureAgentsPointer(stdio IO) int {
	if c.AgentsPath == "" {
		return 0
	}
	existing, err := os.ReadFile(c.AgentsPath)
	if err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot read %s: %v\n", c.AgentsPath, err)
		return 1
	}
	if bytes.Contains(existing, []byte(initAgentsPointerStart)) {
		_, _ = fmt.Fprintf(stdio.Stdout, "init: a2ahub pointer already present in %s\n", c.AgentsPath)
		return 0
	}

	// Append, preserving existing bytes and separating with a blank line.
	out := append([]byte(nil), existing...)
	if len(existing) > 0 {
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		out = append(out, '\n')
	}
	out = append(out, initAgentsPointerBlock()...)

	if err := c.writeFile(c.AgentsPath, out, 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "init: cannot write %s: %v\n", c.AgentsPath, err)
		return 1
	}
	action := "appended a2ahub pointer to"
	if len(existing) == 0 {
		action = "wrote a2ahub pointer to"
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "init: %s %s\n", action, c.AgentsPath)
	return 0
}

// initConfigsEquivalent compares two ProjectConfigs for the "identical
// flags -> no-op" idempotency check: same system id, same set of
// connected-space repo URLs (order-independent), and the configured skill
// install location.
func initConfigsEquivalent(a, b space.ProjectConfig) bool {
	if a.System != b.System || a.SkillDir != b.SkillDir {
		return false
	}
	if len(a.Spaces) != len(b.Spaces) {
		return false
	}
	setA := map[string]bool{}
	for _, r := range a.Spaces {
		setA[r.RepoURL] = true
	}
	for _, r := range b.Spaces {
		if !setA[r.RepoURL] {
			return false
		}
	}
	return true
}

// initSpaceIDFromURL derives a space id from a repo URL: the final path
// segment, with a trailing ".git" stripped (e.g.
// "https://github.com/org/spacename.git" -> "spacename"). The plan's
// OP-202 catalog names no explicit --id flag for connect/init, so this is
// this phase's own, documented convention — see this phase's Deviations
// report.
func initSpaceIDFromURL(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(url, "/"), ".git")
	if i := strings.LastIndexAny(trimmed, "/:"); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// initStringList is a repeatable string flag (flag.Value), stdlib-only —
// the established idiom for `--space <repo>` accepting multiple values.
type initStringList []string

func (l *initStringList) String() string { return strings.Join(*l, ",") }
func (l *initStringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

var _ Command = (*InitCommand)(nil)

// --- connect / disconnect (OP-202) ---------------------------------------

// ConnectCommand implements `a2a connect <space-repo>`: registers the
// space in .a2a/config.yaml and establishes its mirror clone (§7.4).
// Idempotent: connecting an already-connected space id/URL re-fetches the
// existing mirror rather than erroring or duplicating the config entry.
type ConnectCommand struct {
	projectConfigPath string
	machineConfigPath string
	projectRoot       string

	loadProjectConfig func(path string) (space.ProjectConfig, error)
	loadMachineConfig func(path string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	cloneOrFetch      func(ctx context.Context, dir, repoURL string, credential host.Credential) error
	resolveCredential credentialResolver
	writeFile         func(path string, data []byte, perm os.FileMode) error
	readFile          func(path string) ([]byte, error)

	// Open resolves a space locator to its host: a host that needs no
	// credential gets no credential entry and no credential on the mirror
	// refresh, and a host whose owner identity is self-asserted is pointed at
	// `a2a space join`. Nil (catalog and tests) is today's behaviour.
	Open host.Opener

	// defaultCredentialRef is the same probe seam InitCommand carries (nil
	// = internal/space's real one).
	defaultCredentialRef func(ctx context.Context, spaceID string) string
}

// SetDefaultCredentialRefForTest overrides the credential-reference probe
// (test-only DI) so tests never shell out to the ambient `gh`.
func (c *ConnectCommand) SetDefaultCredentialRefForTest(f func(ctx context.Context, spaceID string) string) {
	c.defaultCredentialRef = f
}

// NewConnectCommand constructs the connect command.
func NewConnectCommand(projectConfigPath, machineConfigPath, projectRoot string) *ConnectCommand {
	return &ConnectCommand{
		projectConfigPath: projectConfigPath,
		machineConfigPath: machineConfigPath,
		projectRoot:       projectRoot,
		loadProjectConfig: space.LoadProjectConfig,
		loadMachineConfig: space.LoadMachineConfig,
		resolveMirror:     space.ResolveMirrorLocation,
		cloneOrFetch:      space.CloneOrFetch,
		resolveCredential: space.ResolveCredential,
		writeFile:         os.WriteFile,
		readFile:          os.ReadFile,
	}
}

// Name implements cli.Command.
func (c *ConnectCommand) Name() string { return "connect" }

// Synopsis implements cli.Command.
func (c *ConnectCommand) Synopsis() string {
	return "register a space locally (mirror clone + config entry)"
}

// Run implements cli.Command. Exit codes: 2 = usage (missing space-repo
// arg); 1 = clone/config-write failure; 0 = success (including the
// idempotent already-connected path).
func (c *ConnectCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 1 && IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a connect <space-repo-url>")
		_, _ = fmt.Fprintln(stdio.Stdout, "clones the space's mirror, registers it under the id its own space.yaml declares,")
		_, _ = fmt.Fprintln(stdio.Stdout, "and records a credential reference for it. Safe to re-run.")
		return 0
	}
	if len(args) != 1 || args[0] == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a connect <space-repo>")
		return 2
	}
	// The host is opened once, up front: its declared capabilities decide
	// whether a credential is involved at all and whether the join hint
	// applies, and a space on this machine is persisted canonicalized. A
	// locator the opener cannot open leaves spaceHost nil and every decision at
	// today's behaviour.
	repoURL, spaceHost := initOpenLocator(c.Open, args[0])
	urlID := initSpaceIDFromURL(repoURL)
	if err := space.CheckMirrorKey("id", urlID); err != nil {
		return spaceRefuse(stdio, "connect "+repoURL+": name its mirror", err.Error(),
			"pass the space repository's own URL or path, whose last element is the space's name")
	}
	needsCredential := initHostNeedsCredential(spaceHost)

	cfg, err := loadProjectConfigIfPresent(c.loadProjectConfig, c.projectConfigPath)
	if err != nil {
		return spaceRefuse(stdio, "connect "+repoURL+": read "+c.projectConfigPath+" (it was not rewritten)", err.Error(),
			"fix the entry the error names in "+c.projectConfigPath+", then re-run `a2a connect "+args[0]+"`")
	}
	machine, _ := c.loadMachineConfig(c.machineConfigPath)
	// The refs as loaded: the superseded-clone rule below needs the ref that
	// every earlier read resolved through, before any repair edits it.
	loadedSpaces := slices.Clone(cfg.Spaces)

	// The authoritative space id lives in the mirror's own space.yaml
	// (spec 18 §T1) — it can only be read AFTER a clone, so the clone target
	// is keyed by the URL-derived id first (chicken/egg). The clone happens
	// ONCE, before the real id (or whether it equals urlID) is known, so the
	// clone ref AND the persisted ref BOTH pin MirrorLocation = urlID
	// unconditionally: that makes ResolveMirrorLocation return the SAME path
	// for the clone and for every later resolveMirror(ref) call (submit,
	// disconnect, doctor) in ALL cases — including a configured machine
	// `mirror_root` (which fix B now seeds by default) combined with a
	// manifest id ≠ the repo basename. Keying resolution off the id instead
	// would resolve to a directory that was never cloned into (the mirror
	// lands at the urlID-keyed path, since that's all that's known at clone
	// time). Pinning MirrorLocation is the existing space.Ref seam for
	// exactly "location key ≠ id".
	dir := c.resolveMirror(c.projectRoot, space.Ref{ID: urlID, MirrorLocation: urlID}, machine)
	// The credential is keyed by urlID for the same chicken/egg reason the
	// mirror path is: the authoritative id lives in the space.yaml this clone
	// is about to fetch. urlID is the right key anyway — `a2a init` seeds its
	// credential entry under exactly that URL-derived guess, and
	// A2A_TOKEN_<urlID> is what an operator connecting to a private space has
	// to export before this line can succeed. If the manifest later disagrees
	// about the id, ensureCredentialEntry below re-keys the entry; the clone
	// has already happened by then and only ever needed read access.
	var credential host.Credential
	if needsCredential {
		credential = mirrorCredential(ctx, c.resolveCredential, urlID, machine)
	}
	// Whether this connect makes the clone or finds it, decided BEFORE the
	// clone: it is what the report says.
	_, gitErr := os.Stat(filepath.Join(dir, ".git"))
	mirrorPresent := gitErr == nil
	if err := c.cloneOrFetch(ctx, dir, repoURL, credential); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot establish mirror for %s: %v\n", urlID, err)
		return 1
	}

	id := connectResolveSpaceID(dir, urlID)
	if err := space.CheckMirrorKey("id", id); err != nil {
		return spaceRefuse(stdio, "connect "+repoURL+": register the id its space.yaml declares (nothing was registered)", err.Error(),
			"the space's owner corrects the `space:` field of its space.yaml; then re-run `a2a connect "+args[0]+"`")
	}

	// The authoritative id is known only HERE (it comes out of the freshly
	// cloned mirror's space.yaml), and the machine config's credentials map
	// is keyed by exactly that id — so this is the only place that can seed
	// a credential reference under the right key. `a2a init` seeds one under
	// its URL-derived guess; when the manifest disagrees, that entry is
	// keyed to a space that does not exist and every write reds until an
	// operator notices and hand-edits the YAML.
	if needsCredential {
		c.ensureCredentialEntry(ctx, machine, id, stdio)
	}
	// Only the opened host's own declaration makes the join hint apply, and
	// only for a system that is not already an active participant.
	joinHint := func() {
		if spaceHost == nil || !spaceHost.Capabilities().OwnerIdentitySelfAsserted || cfg.System == "" {
			return
		}
		if connectIsActiveParticipant(dir, cfg.System) {
			return
		}
		_, _ = fmt.Fprintln(stdio.Stdout, "connect: next: a2a space join")
	}

	// An entry registered under a DIFFERENT id for this same repo URL is a
	// stale id, not a second space: `a2a init -space <url>` has to guess the
	// id from the URL (it never clones, so it cannot read the manifest), and
	// when the manifest disagrees — the documented `a2a` vs `getvisa` case —
	// every later verb looks up an id that no space has. connect is the
	// first moment the truth is knowable, so it repairs the entry instead of
	// leaving the operator with a green `a2a doctor` and a submit that tells
	// them to run the command they already ran.
	_, wasID, idFixed := connectRepairStaleID(&cfg, id, repoURL)

	// The SAME lookup connectFindIndex uses below for "is this a fresh
	// registration" also locates the entry any repair targets — a
	// narrower match here (e.g. RepoURL only) would mean an entry
	// "already connected" reports on is not the one a repair reaches,
	// which is this defect's exact shape displaced rather than fixed
	// (spec: "the mirror the tool clones is not the one it later reads").
	idx, existed := connectFindIndex(cfg, id, repoURL)
	var wasMirror string
	var mirrorFixed bool
	if existed {
		wasMirror, mirrorFixed = connectRepairMirrorLocation(&cfg, idx, urlID)
	}

	if idFixed || mirrorFixed {
		raw, err := yaml.Marshal(cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot encode config: %v\n", err)
			return 1
		}
		if err := c.writeFile(c.projectConfigPath, raw, 0o644); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot write %s: %v\n", c.projectConfigPath, err)
			return 1
		}
		if idFixed {
			_, _ = fmt.Fprintf(stdio.Stdout, "connect: corrected space id %q -> %q (the space's own space.yaml is authoritative)\n", wasID, id)
		}
		if mirrorFixed {
			if wasMirror == "" {
				newPath := c.resolveMirror(c.projectRoot, cfg.Spaces[idx], machine)
				_, _ = fmt.Fprintf(stdio.Stdout, "connect: space %q now uses the machine mirror at %s\n", id, newPath)
				c.retireSupersededClone(ctx, loadedSpaces[idx], repoURL, newPath, machine, stdio)
			} else {
				_, _ = fmt.Fprintf(stdio.Stdout, "connect: corrected mirror location for space %q: %q -> %q\n", id, wasMirror, urlID)
			}
		}
		_, _ = fmt.Fprintln(stdio.Stdout, connectExistingEntryLine(id, mirrorPresent))
		joinHint()
		return 0
	}

	if !existed {
		ref := space.Ref{ID: id, RepoURL: repoURL, MirrorLocation: urlID}
		cfg.Spaces = append(cfg.Spaces, ref)

		raw, err := yaml.Marshal(cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot encode config: %v\n", err)
			return 1
		}
		if err := os.MkdirAll(filepath.Dir(c.projectConfigPath), 0o755); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot create config directory: %v\n", err)
			return 1
		}
		if err := c.writeFile(c.projectConfigPath, raw, 0o644); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot write %s: %v\n", c.projectConfigPath, err)
			return 1
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "connect: registered space %q and cloned its mirror\n", id)
		joinHint()
		return 0
	}

	_, _ = fmt.Fprintln(stdio.Stdout, connectExistingEntryLine(id, mirrorPresent))
	joinHint()
	return 0
}

// connectExistingEntryLine is the report for a space whose config entry was
// already there: "connected; cloned its mirror" when this connect made the
// clone (the entry came from `init`, which never clones), "already connected;
// mirror refreshed" when the clone was already on disk.
func connectExistingEntryLine(id string, mirrorPresent bool) string {
	if mirrorPresent {
		return fmt.Sprintf("connect: space %q already connected; mirror refreshed", id)
	}
	return fmt.Sprintf("connect: space %q connected; cloned its mirror", id)
}

// retireSupersededClone removes the clone a read verb made before this connect
// filled the entry's mirror location, when it is provably that clone and
// provably superseded. before is the ref as it was loaded (the one every earlier
// read resolved through); newPath is where the filled ref resolves. All three
// must hold:
//  1. the path is exactly what the resolver answers for the pre-fill ref;
//  2. it differs from newPath (equal means it IS the live clone);
//  3. it is a git work tree whose remote.origin.url equals the entry's RepoURL.
//
// Anything else is left alone, and when the path exists connect says why.
// The directory is under .a2a/cache/, which is disposable (D-001).
func (c *ConnectCommand) retireSupersededClone(ctx context.Context, before space.Ref, repoURL, newPath string, machine space.MachineConfig, stdio IO) {
	oldPath := c.resolveMirror(c.projectRoot, before, machine)
	if oldPath == newPath {
		return
	}
	if _, err := os.Stat(oldPath); err != nil {
		return // nothing was cloned there
	}
	origin, err := space.CloneOrigin(ctx, oldPath)
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(stdio.Stdout, "connect: kept the earlier clone at %s: %v\n", oldPath, err)
		return
	case origin != repoURL:
		_, _ = fmt.Fprintf(stdio.Stdout, "connect: kept the earlier clone at %s: its origin is %s, not %s\n", oldPath, origin, repoURL)
		return
	}
	if err := os.RemoveAll(oldPath); err != nil {
		_, _ = fmt.Fprintf(stdio.Stdout, "connect: kept the earlier clone at %s: cannot remove it: %v\n", oldPath, err)
		return
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "connect: removed the superseded clone at %s\n", oldPath)
}

// connectIsActiveParticipant reports whether system is an active participant of
// the space whose mirror is at dir. A mirror that cannot be read or parsed
// answers false: the hint is a pointer, and a pointer to the next step is the
// safe thing to print when the state cannot be read.
func connectIsActiveParticipant(dir, system string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "space.yaml"))
	if err != nil {
		return false
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil {
		return false
	}
	for _, p := range manifest.Participants {
		if p.System == system && p.Status == fold.MembershipMember {
			return true
		}
	}
	return false
}

// ensureCredentialEntry adds `<id>: <reference>` to an EXISTING machine
// config that has no entry for id yet, by a targeted text insert rather
// than a marshal round-trip — the file is the operator's, and re-encoding
// it would silently drop their comments and key order. A missing machine
// config is left to `a2a init` (which seeds the whole skeleton); an
// unreadable/unwritable one degrades to the printed hint. Best-effort by
// design: connect's own job is the mirror + config entry, and this never
// fails it.
func (c *ConnectCommand) ensureCredentialEntry(ctx context.Context, machine space.MachineConfig, id string, stdio IO) {
	if c.machineConfigPath == "" {
		return
	}
	if _, present := machine.Credentials[id]; present {
		return
	}
	raw, err := c.readFile(c.machineConfigPath)
	if err != nil {
		return // no machine config yet — `a2a init` seeds it, hint below still applies
	}

	var ref string
	if c.defaultCredentialRef != nil {
		ref = c.defaultCredentialRef(ctx, id)
	} else {
		ref = space.DefaultCredentialReference(ctx, id)
	}
	updated, ok := insertCredentialEntry(raw, id, ref)
	if !ok {
		return
	}
	if err := c.writeFile(c.machineConfigPath, updated, 0o600); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "connect: could not record the credential reference for %q: %v\n", id, err)
		return
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "connect: recorded credential reference for space %q (%s); override any time with  export %s=<token>\n",
		id, ref, space.CredentialEnvVar(id))
}

// insertCredentialEntry inserts `  <id>: <ref>` into a machine config's
// raw YAML text: under an existing top-level `credentials:` key, else as a
// new block appended at the end. Returns ok=false when an entry for id is
// already textually present (so a rewrite is not needed). Text surgery,
// deliberately: it preserves every comment the operator wrote.
func insertCredentialEntry(raw []byte, id, ref string) ([]byte, bool) {
	lines := strings.Split(string(raw), "\n")
	entry := "  " + id + ": " + ref

	credsAt := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "credentials:") {
			credsAt = i
			continue
		}
		if credsAt >= 0 && strings.HasPrefix(strings.TrimSpace(line), id+":") {
			return nil, false
		}
	}

	if credsAt < 0 {
		text := strings.TrimRight(string(raw), "\n")
		return []byte(text + "\ncredentials:\n" + entry + "\n"), true
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:credsAt+1]...)
	out = append(out, entry)
	out = append(out, lines[credsAt+1:]...)
	return []byte(strings.Join(out, "\n")), true
}

// connectRepairStaleID rewrites the id of an entry registered for repoURL
// under a different id than the manifest's authoritative one. It reports
// (newID, oldID, true) when it changed something; the caller persists.
// An entry already carrying the right id, or no entry for this URL, is
// left alone (ok=false) — this only ever fixes a wrong id in place, it
// never adds, removes, or reorders entries.
func connectRepairStaleID(cfg *space.ProjectConfig, id, repoURL string) (repaired, was string, ok bool) {
	for i, r := range cfg.Spaces {
		if r.RepoURL != repoURL || r.ID == id {
			continue
		}
		// Another entry may legitimately already hold the correct id (an
		// operator who ran connect before init) — then the stale row is a
		// duplicate this function must not turn into a second copy.
		for _, other := range cfg.Spaces {
			if other.ID == id && other.RepoURL == repoURL {
				return "", "", false
			}
		}
		was = r.ID
		cfg.Spaces[i].ID = id
		return id, was, true
	}
	return "", "", false
}

// connectFindIndex locates the config entry connect's "already registered"
// path operates on, matched by resolved id OR repo URL — the same rule
// connectRepairMirrorLocation's caller uses to decide WHICH entry to repair,
// so "already connected" and "the entry a repair targets" can never
// diverge (see Run's comment at the call site).
func connectFindIndex(cfg space.ProjectConfig, id, repoURL string) (int, bool) {
	for i, r := range cfg.Spaces {
		if r.ID == id || r.RepoURL == repoURL {
			return i, true
		}
	}
	return -1, false
}

// connectRepairMirrorLocation sets/repairs the MirrorLocation of the
// ALREADY-registered entry at cfg.Spaces[idx] to urlID — the same key
// value the clone itself resolved against (Run, above: `dir :=
// c.resolveMirror(..., space.Ref{ID: urlID, MirrorLocation: urlID}, ...)`)
// — when it does not already carry that value.
//
// Without this, an entry `a2a init --space <url>` registered (which never
// clones, so it never sets MirrorLocation at all) never converges with the
// directory the clone actually landed in: ResolveMirrorLocation
// (internal/space/config.go) treats an empty MirrorLocation as
// project-relative-by-ID and ignores a configured mirror_root entirely, so
// a configured mirror_root silently diverges the read path (inbox/outbox/
// submit/doctor) from the write path (this clone) — the defect this fix
// closes. Idempotent: an entry that already carries urlID is left alone
// (ok=false) — this never adds, removes, or reorders entries.
func connectRepairMirrorLocation(cfg *space.ProjectConfig, idx int, urlID string) (was string, ok bool) {
	r := &cfg.Spaces[idx]
	if r.MirrorLocation == urlID {
		return "", false
	}
	was = r.MirrorLocation
	r.MirrorLocation = urlID
	return was, true
}

// connectResolveSpaceID resolves the authoritative space id from the
// freshly-cloned mirror's own space.yaml `space:` field (space.Manifest.Space,
// spec 18 §T1) — this is the fix for the id mismatch that made `a2a submit`
// space-resolution fail: registering the URL-derived id (e.g. "a2a" for a
// repo path ending in a2a.git) instead of the manifest's own id (e.g.
// "getvisa") meant no persisted ref ever had the id `submit --space
// getvisa` actually looks up. Falls back to fallback (the URL-derived id)
// when space.yaml is unreadable, unparseable, or structurally present but
// carries no `space:` value — never crashes on a malformed mirror.
func connectResolveSpaceID(mirrorDir, fallback string) string {
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		return fallback
	}
	manifest, err := space.ParseManifest(raw)
	if err != nil || manifest.Space == "" {
		return fallback
	}
	return manifest.Space
}

// loadProjectConfigIfPresent is the project config for a verb that may write
// it: an absent file is an empty config, and any other failure is returned, so
// the verb never rewrites, or deletes by, a config it could not read.
func loadProjectConfigIfPresent(load func(string) (space.ProjectConfig, error), path string) (space.ProjectConfig, error) {
	cfg, err := load(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return space.ProjectConfig{}, err
	}
	return cfg, nil
}

var _ Command = (*ConnectCommand)(nil)

// DisconnectCommand implements `a2a disconnect <space>`: removes the
// config entry + mirror clone for that space (and calls the future
// internal/cache removal seam, currently a no-op — spec 06 Open Q-A).
// Disconnecting a space that was never connected is itself an idempotent
// no-op (exit 0), consistent with §7.2's "every mutating command is safe
// to re-run" rule.
type DisconnectCommand struct {
	projectConfigPath string
	machineConfigPath string
	projectRoot       string
	cache             CacheRemover

	loadProjectConfig func(path string) (space.ProjectConfig, error)
	loadMachineConfig func(path string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	removeAll         func(path string) error
	writeFile         func(path string, data []byte, perm os.FileMode) error
}

// NewDisconnectCommand constructs the disconnect command. cache is the
// PendingMarker's sibling no-op seam (P7 supplies the real
// internal/cache-backed CacheRemover later); cache must not be nil (rails
// anti-pattern #10 — construct with NewNoopCacheRemover() until P7 lands).
func NewDisconnectCommand(projectConfigPath, machineConfigPath, projectRoot string, cache CacheRemover) *DisconnectCommand {
	return &DisconnectCommand{
		projectConfigPath: projectConfigPath,
		machineConfigPath: machineConfigPath,
		projectRoot:       projectRoot,
		cache:             cache,
		loadProjectConfig: space.LoadProjectConfig,
		loadMachineConfig: space.LoadMachineConfig,
		resolveMirror:     space.ResolveMirrorLocation,
		removeAll:         os.RemoveAll,
		writeFile:         os.WriteFile,
	}
}

// Name implements cli.Command.
func (c *DisconnectCommand) Name() string { return "disconnect" }

// Synopsis implements cli.Command.
func (c *DisconnectCommand) Synopsis() string {
	return "remove a connected space's config entry + mirror + cache"
}

// Run implements cli.Command. Exit codes: 2 = usage (missing space arg);
// 1 = mirror-removal/config-write failure; 0 = success (including the
// idempotent never-connected no-op).
func (c *DisconnectCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) == 1 && IsHelpArg(args[0]) {
		_, _ = fmt.Fprintln(stdio.Stdout, "usage: a2a disconnect <space-id>")
		_, _ = fmt.Fprintln(stdio.Stdout, "removes the space's config entry, mirror clone and cache. Safe to re-run.")
		return 0
	}
	if len(args) != 1 || args[0] == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a disconnect <space>")
		return 2
	}
	ref, found, code := c.RemoveConnected(ctx, args[0], stdio)
	if code != 0 {
		return code
	}
	if !found {
		_, _ = fmt.Fprintf(stdio.Stdout, "disconnect: %q is not connected (nothing to do)\n", args[0])
		return 0
	}
	_, _ = fmt.Fprintf(stdio.Stdout, "disconnect: removed space %q (config entry + mirror)\n", ref.ID)
	return 0
}

// ConnectedRef returns this project's configured ref for target (a space id or
// a repo URL) without changing anything. `space delete` reads it through its own
// consumer-side seam.
func (c *DisconnectCommand) ConnectedRef(target string) (space.Ref, bool) {
	cfg, _ := c.loadProjectConfig(c.projectConfigPath)
	ref, idx := disconnectFind(cfg, target)
	return ref, idx >= 0
}

// RemoveConnected is disconnect's removal itself, the one body `a2a disconnect`
// and `a2a space delete --approve` both run: it removes the mirror clone, calls
// the cache seam, and drops the config entry for target. found is false when
// target is not connected (nothing was touched); a non-zero code is a failure
// already reported on stderr. It prints nothing on success, so each caller
// words its own report.
func (c *DisconnectCommand) RemoveConnected(ctx context.Context, target string, stdio IO) (ref space.Ref, found bool, code int) {
	cfg, err := loadProjectConfigIfPresent(c.loadProjectConfig, c.projectConfigPath)
	if err != nil {
		return space.Ref{}, false, spaceRefuse(stdio, "disconnect "+target+": read "+c.projectConfigPath+" (nothing was removed)", err.Error(),
			"fix the entry the error names in "+c.projectConfigPath+", then re-run the command")
	}
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

	ref, idx := disconnectFind(cfg, target)
	if idx < 0 {
		return space.Ref{}, false, 0
	}

	dir := c.resolveMirror(c.projectRoot, ref, machine)
	if err := c.removeAll(dir); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cannot remove mirror for %s: %v\n", ref.ID, err)
		return ref, true, 1
	}
	if err := c.cache.RemoveSpace(ctx, ref.ID); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cache removal seam failed for %s: %v\n", ref.ID, err)
		return ref, true, 1
	}

	cfg.Spaces = append(cfg.Spaces[:idx], cfg.Spaces[idx+1:]...)
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cannot encode config: %v\n", err)
		return ref, true, 1
	}
	if err := c.writeFile(c.projectConfigPath, raw, 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cannot write %s: %v\n", c.projectConfigPath, err)
		return ref, true, 1
	}
	return ref, true, 0
}

func disconnectFind(cfg space.ProjectConfig, target string) (space.Ref, int) {
	for i, r := range cfg.Spaces {
		if r.ID == target || r.RepoURL == target {
			return r, i
		}
	}
	return space.Ref{}, -1
}

var _ Command = (*DisconnectCommand)(nil)
