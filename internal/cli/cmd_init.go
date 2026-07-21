// OP-201 `a2a init`, OP-202 `a2a connect`/`a2a disconnect` (spec 06 T1).
// This file's only package-level symbols are InitCommand/ConnectCommand/
// DisconnectCommand + their NewXCommand constructors plus file-private,
// uniquely-named helpers (init*/connect*/disconnect* prefix) — no shared
// helper, no package var, per this phase's plan Placement decision
// (avoids collision with P7/P8/P9's parallel verb files in this package).
package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

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

	// writeFile/loadProjectConfig are DI seams (rails, mirrors
	// DoctorCommand's own convention) so tests never touch a real
	// .a2a/config.yaml path.
	writeFile         func(path string, data []byte, perm os.FileMode) error
	loadProjectConfig func(path string) (space.ProjectConfig, error)
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
	return "non-interactive project setup: --system <id> --space <repo>..."
}

// Run implements cli.Command. Exit codes: 2 = usage error (missing
// --system, or zero --space values); 1 = config write failure; 0 =
// success (including the idempotent "already configured" no-op).
func (c *InitCommand) Run(_ context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	system := fs.String("system", "", "this project's own system id (required)")
	var spaces initStringList
	fs.Var(&spaces, "space", "connected space repo URL (repeatable; at least one required)")
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
	id := initSpaceIDFromURL(repoURL)

	cfg, _ := c.loadProjectConfig(c.projectConfigPath) // absent config is fine — connect can be the first write
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

	ref, existed := connectFind(cfg, id, repoURL)
	if !existed {
		ref = space.Ref{ID: id, RepoURL: repoURL}
		cfg.Spaces = append(cfg.Spaces, ref)
	}

	dir := c.resolveMirror(c.projectRoot, ref, machine)
	if err := c.cloneOrFetch(ctx, dir, repoURL); err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "connect: cannot establish mirror for %s: %v\n", id, err)
		return 1
	}

	if !existed {
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

func connectFind(cfg space.ProjectConfig, id, repoURL string) (space.Ref, bool) {
	for _, r := range cfg.Spaces {
		if r.ID == id || r.RepoURL == repoURL {
			return r, true
		}
	}
	return space.Ref{}, false
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
