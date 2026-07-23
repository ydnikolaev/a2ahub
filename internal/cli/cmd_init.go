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
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

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
// binary-is-truth rule. Provider-agnostic prose (AGENTS.md is read by both
// Claude Code and Codex, §8.8) — it points at the binary and the installed
// skill, never restating command or validation rules.
func initAgentsPointerBlock() string {
	return initAgentsPointerStart + "\n" +
		"## a2ahub\n\n" +
		"This repo participates in **a2ahub** — typed cross-system artifact exchange " +
		"(questions, work requests, contracts, decisions) with other systems' agents.\n\n" +
		"- **Operating skill:** `.a2ahub/skill/SKILL.md` (install / refresh with `a2a skill install`).\n" +
		"- **Session start:** run `a2a doctor`, then `a2a inbox`; act on blocking items.\n" +
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
	return "non-interactive project setup: config + skill install + AGENTS.md pointer (--no-skill / --no-agents-pointer to opt out)"
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
	noAgentsPointer := fs.Bool("no-agents-pointer", false, "do NOT add the a2ahub pointer to AGENTS.md (added by default)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *system == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "init: --system is required (non-interactive mode never prompts)")
		return 2
	}
	if len(spaces) == 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "init: at least one --space is required (non-interactive mode never prompts)")
		return 2
	}

	refs := make([]space.Ref, 0, len(spaces))
	for _, s := range spaces {
		refs = append(refs, space.Ref{ID: initSpaceIDFromURL(s), RepoURL: s})
	}
	cfg := space.ProjectConfig{System: *system, Spaces: refs}

	// FIX B runs before the idempotent short-circuit so a repeat `a2a
	// init` still ensures/hints the machine config (e.g. an operator who
	// deleted it after a first run).
	if code := c.ensureMachineConfig(ctx, refs, stdio); code != 0 {
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
	if !*noAgentsPointer {
		_ = c.ensureAgentsPointer(stdio) // prints its own error; never fatal to init
	}

	if existing, ok := c.loadExisting(); ok && initConfigsEquivalent(existing, cfg) {
		_, _ = fmt.Fprintln(stdio.Stdout, "init: already configured")
		_, _ = fmt.Fprintln(stdio.Stdout, "init: run `a2a doctor` to verify credentials and space access")
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
	_, _ = fmt.Fprintln(stdio.Stdout, "init: run `a2a doctor` to verify credentials and space access")
	return 0
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
func (c *InitCommand) ensureMachineConfig(ctx context.Context, refs []space.Ref, stdio IO) int {
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
		raw = append(raw, "credentials:\n"...)
		for _, ref := range refs {
			raw = append(raw, "  "+ref.ID+": "+c.credentialRef(ctx, ref.ID)+"\n"...)
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

	for _, ref := range refs {
		_, _ = fmt.Fprintf(stdio.Stdout, "init: credential for space %q: %s resolves it, or override with  export %s=<token>\n",
			ref.ID, c.credentialRef(ctx, ref.ID), space.CredentialEnvVar(ref.ID))
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
// connected-space repo URLs (order-independent).
func initConfigsEquivalent(a, b space.ProjectConfig) bool {
	if a.System != b.System {
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
	cloneOrFetch      func(ctx context.Context, dir, repoURL string) error
	writeFile         func(path string, data []byte, perm os.FileMode) error
	readFile          func(path string) ([]byte, error)

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
	if len(args) != 1 || args[0] == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a connect <space-repo>")
		return 2
	}
	repoURL := args[0]
	urlID := initSpaceIDFromURL(repoURL)

	cfg, _ := c.loadProjectConfig(c.projectConfigPath) // absent config is fine — connect can be the first write
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

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
	if err := c.cloneOrFetch(ctx, dir, repoURL); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot establish mirror for %s: %v\n", urlID, err)
		return 1
	}

	id := connectResolveSpaceID(dir, urlID)

	// The authoritative id is known only HERE (it comes out of the freshly
	// cloned mirror's space.yaml), and the machine config's credentials map
	// is keyed by exactly that id — so this is the only place that can seed
	// a credential reference under the right key. `a2a init` seeds one under
	// its URL-derived guess; when the manifest disagrees, that entry is
	// keyed to a space that does not exist and every write reds until an
	// operator notices and hand-edits the YAML.
	c.ensureCredentialEntry(ctx, machine, id, stdio)

	_, existed := connectFind(cfg, id, repoURL)
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
		return 0
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "connect: space %q already connected; mirror refreshed\n", id)
	return 0
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

	ref := space.DefaultCredentialReference(ctx, id)
	if c.defaultCredentialRef != nil {
		ref = c.defaultCredentialRef(ctx, id)
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

func connectFind(cfg space.ProjectConfig, id, repoURL string) (space.Ref, bool) {
	for _, r := range cfg.Spaces {
		if r.ID == id || r.RepoURL == repoURL {
			return r, true
		}
	}
	return space.Ref{}, false
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
	if len(args) != 1 || args[0] == "" {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a disconnect <space>")
		return 2
	}
	target := args[0]

	cfg, _ := c.loadProjectConfig(c.projectConfigPath)
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

	ref, idx := disconnectFind(cfg, target)
	if idx < 0 {
		_, _ = fmt.Fprintf(stdio.Stdout, "disconnect: %q is not connected (nothing to do)\n", target)
		return 0
	}

	dir := c.resolveMirror(c.projectRoot, ref, machine)
	if err := c.removeAll(dir); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cannot remove mirror for %s: %v\n", ref.ID, err)
		return 1
	}
	if err := c.cache.RemoveSpace(ctx, ref.ID); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cache removal seam failed for %s: %v\n", ref.ID, err)
		return 1
	}

	cfg.Spaces = append(cfg.Spaces[:idx], cfg.Spaces[idx+1:]...)
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cannot encode config: %v\n", err)
		return 1
	}
	if err := c.writeFile(c.projectConfigPath, raw, 0o644); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "disconnect: cannot write %s: %v\n", c.projectConfigPath, err)
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "disconnect: removed space %q (config entry + mirror)\n", ref.ID)
	return 0
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
