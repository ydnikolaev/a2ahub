package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/feedback"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/mcp"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
	"github.com/ydnikolaev/a2ahub/skill"
)

// wire.go is cmd/a2a's single dependency-injection point (ADR-001: "wiring
// only"). Each OP-2xx verb is registered as a dispatch closure that, at
// invocation time, loads the config it needs, resolves the target space,
// constructs the internal/cli command with real core services, and runs it.
// Config-independent verbs (version, template, init) build cheaply; the
// config-dependent verbs (new/validate/submit/sync/doctor) resolve lazily so
// a bare `a2a version` never requires a config file on disk.
//
// The submit closure enforces the AC-201.3 ordering the unit layer cannot
// see: the foreign-section refusal is a config-only check that MUST run
// before any mirror clone/fetch, so a foreign-section artifact never causes
// a network call. SubmitCommand.Run repeats the refusal as defense-in-depth
// for the direct-construction (test) path.

const (
	githubAPIBaseURL  = "https://api.github.com"
	defaultBaseBranch = "main"
)

// paths bundles the resolved config/staging locations a verb closure needs.
type paths struct {
	projectConfig string // .a2a/config.yaml
	machineConfig string // ~/.config/a2a/config.yaml
	projectRoot   string // cwd (the project the .a2a/ lives in)
	staging       string // .a2a/staging/
}

func resolvePaths() (paths, error) {
	root, err := os.Getwd()
	if err != nil {
		return paths{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	return paths{
		projectConfig: filepath.Join(root, ".a2a", "config.yaml"),
		machineConfig: filepath.Join(home, ".config", "a2a", "config.yaml"),
		projectRoot:   root,
		staging:       filepath.Join(root, ".a2a", "staging"),
	}, nil
}

// cacheDirOf is the `.a2a/cache/` path — the home the read-surface Store
// reads and the write verbs' pending-merge markers write.
func cacheDirOf(p paths) string {
	return filepath.Join(p.projectRoot, ".a2a", "cache")
}

// stdio builds the injected stream set from the dispatch writers.
func stdio(stdout, stderr io.Writer) cli.IO {
	return cli.IO{Stdin: os.Stdin, Stdout: stdout, Stderr: stderr}
}

// buildCommands returns the dispatch map. Each entry is a closure matching
// the existing `command` signature; it constructs the real verb and runs it.
func buildCommands() map[string]command {
	m := map[string]command{
		"version": runVersion,
	}

	// Static / cheap verbs.
	m["init"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		cmd := cli.NewInitCommand(p.projectConfig)
		// FIX B (spec 18 §T1/§8): wire the machine-config skeleton DI
		// seam, mirroring how the validate closure sets CIGitHubActor.
		cmd.MachineConfigPath = p.machineConfig
		// P20/P21 default-on onboarding: init installs the skill tree and the
		// AGENTS.md pointer by default (opt out via --no-skill / --no-agents-pointer).
		cmd.AgentsPath = filepath.Join(p.projectRoot, "AGENTS.md")
		cmd.SkillFiles = skill.Files
		cmd.SkillTarget = filepath.Join(p.projectRoot, ".a2ahub", "skill")
		cmd.Version = version
		return cmd.Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["template"] = func(args []string, stdout, stderr io.Writer) int {
		return cli.NewTemplateCommand().Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["skill"] = func(args []string, stdout, stderr io.Writer) int {
		return cli.NewSkillCommand(skill.Files, version).Run(context.Background(), args, stdio(stdout, stderr))
	}
	// P23 (OP-222): shell completion. A pure host-side render — no store, no
	// config — fed the dispatch surface it belongs to (completionCmds/
	// completionContractSubs read the SAME buildCommands()/ContractSubcommands()
	// the binary wires, so a new verb is completable the moment it registers).
	m["completion"] = func(args []string, stdout, stderr io.Writer) int {
		return cli.NewCompletionCommand(completionCmds(), completionSubFamilies()).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["connect"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		return cli.NewConnectCommand(p.projectConfig, p.machineConfig, p.projectRoot).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["disconnect"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		return cli.NewDisconnectCommand(p.projectConfig, p.machineConfig, p.projectRoot, cli.NewCacheBackedCacheRemover(cacheDirOf(p))).Run(context.Background(), args, stdio(stdout, stderr))
	}

	// Config-dependent verbs.
	m["new"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		cfg, err := space.LoadProjectConfig(p.projectConfig)
		if err != nil {
			return failf(stderr, "a2a new: no project config (run `a2a init` first): %v", err)
		}
		resolve := func(f cli.ActorFlags) template.Actor {
			// ResolveActor reads A2A_ACTOR_* env internally at §7.4 priority
			// 2; harness/config sources have no live provider yet (zero).
			return cli.ResolveActor(f, cli.HarnessDefaults{}, cli.ConfigActor{})
		}
		return cli.NewNewCommand(p.staging, cfg.System, resolve).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["validate"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		engine, err := newEngine()
		if err != nil {
			return fail(stderr, err)
		}
		cmd := cli.NewValidateCommand(engine, p.staging)
		// Config layer resolves the CI diff-authz author from the
		// environment (config & secrets rail: internal/cli never reads env
		// itself); the `--author` flag, if given, overrides this inside Run.
		cmd.CIGitHubActor = os.Getenv("GITHUB_ACTOR")
		return cmd.Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["sync"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		return cli.NewSyncCommand(p.projectConfig, p.machineConfig, p.projectRoot, cli.NewCacheBackedPendingMarker(cacheDirOf(p))).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["doctor"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		h := host.NewGitHubHost(http.DefaultClient, githubAPIBaseURL)
		// Pass the BARE dotted version, not versionStamp() ("a2a x.y.z
		// (sha)") — doctorVersionOlder parses a bare major.minor.patch, so
		// the full stamp made `a2a doctor` report `versions: FAIL` against
		// every space that pins min_binary_version (P10 e2e surfaced it).
		return cli.NewDoctorCommand(h, version, p.projectConfig, p.machineConfig, p.projectRoot).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["update"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		// Bare version (like doctor): release.Info/version.OlderThan parse a
		// bare major.minor.patch, not the "a2a x.y.z (sha)" stamp.
		return cli.NewUpdateCommand(version, p.projectConfig, p.machineConfig, p.projectRoot).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["submit"] = runSubmit
	m["feedback"] = runFeedback

	// Read verbs (P7): federated over ALL connected spaces via one
	// cache.Store; read-only, no network in the render path.
	for name, construct := range readVerbs() {
		m[name] = func(args []string, stdout, stderr io.Writer) int {
			p, err := resolvePaths()
			if err != nil {
				return fail(stderr, err)
			}
			store, err := buildStore(p)
			if err != nil {
				return failf(stderr, "a2a: %v", err)
			}
			return construct(store).Run(context.Background(), args, stdio(stdout, stderr))
		}
	}

	// Lifecycle verbs (P8): per-space, funnel-backed like submit. The
	// target space is resolved from the first artifact id on the command
	// line (the artifact already lives in a connected space's mirror).
	for name, construct := range lifecycleVerbs() {
		m[name] = func(args []string, stdout, stderr io.Writer) int {
			return runLifecycle(args, stdout, stderr, construct)
		}
	}

	// Contract verb (P8): dispatches its own sub-verbs; per-space like the
	// lifecycle verbs, plus the P6 new-command for the `contract new` alias.
	m["contract"] = runContract

	// MCP façade (P14, OP-216): serve the §7.7 tool set over stdio JSON-RPC
	// for the life of the session. internal/mcp re-wires the same core (never
	// imports internal/cli); the bare `version` (not the full stamp) feeds
	// its write funnel's min_binary_version guard, matching the doctor fix.
	// __catalog (P13, spec 13 §11 wave-7 amendment): hidden, machine-
	// consumed verb printing the deterministic command/MCP catalog
	// (catalog.go) — never listed in printUsage (main.go).
	m["__catalog"] = runCatalog

	m["mcp"] = func(_ []string, stdout, stderr io.Writer) int {
		ctx := context.Background()
		p, err := mcp.ResolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		srv, err := mcp.NewServerFromConfig(ctx, p, version)
		if err != nil {
			return failf(stderr, "a2a mcp: %v", err)
		}
		if err := srv.Serve(ctx, os.Stdin, stdout); err != nil {
			return failf(stderr, "a2a mcp: %v", err)
		}
		return 0
	}

	return m
}

// completionCmds returns the top-level verb names `a2a completion` offers:
// buildCommands() keys minus the hidden __catalog meta verb (never listed in
// usage, so never completed). Read from the SAME dispatch map the binary
// wires — not a second hand-kept list — so a newly registered verb is
// completable automatically (the completion parity test guards the invariant).
// RenderCompletion sorts, so the map's non-deterministic key order is fine.
func completionCmds() []string {
	var out []string
	for name := range buildCommands() {
		if name == "__catalog" {
			continue
		}
		out = append(out, name)
	}
	return out
}

// completionContractSubs returns the `a2a contract <sub>` sub-verb names from
// the same cli.ContractSubcommands() SSOT the catalog and MCP parity use.
func completionContractSubs() []string {
	subs := cli.ContractSubcommands()
	out := make([]string, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.Name)
	}
	return out
}

// completionSubFamilies maps each `a2a <verb> <sub>` family to its sub-verb
// names from the same SSOTs the catalog/MCP parity use — contract
// (ContractSubcommands) + feedback (FeedbackSubcommands). Adding a family here
// is the ONLY completion edit a new sub-verb family needs (renderer is N-family).
func completionSubFamilies() map[string][]string {
	return map[string][]string{
		"contract": completionContractSubs(),
		"feedback": cli.FeedbackSubcommands(),
	}
}

// readVerbs maps each P7 read verb to its cache.Store-backed constructor.
func readVerbs() map[string]func(*cache.Store) cli.Command {
	return map[string]func(*cache.Store) cli.Command{
		"inbox":     func(s *cache.Store) cli.Command { return cli.NewInboxCommand(s) },
		"outbox":    func(s *cache.Store) cli.Command { return cli.NewOutboxCommand(s) },
		"show":      func(s *cache.Store) cli.Command { return cli.NewShowCommand(s) },
		"thread":    func(s *cache.Store) cli.Command { return cli.NewThreadCommand(s) },
		"search":    func(s *cache.Store) cli.Command { return cli.NewSearchCommand(s) },
		"contracts": func(s *cache.Store) cli.Command { return cli.NewContractsCommand(s) },
		"statusline": func(s *cache.Store) cli.Command {
			return cli.NewStatuslineCommand(s)
		},
		"html":      func(s *cache.Store) cli.Command { return cli.NewHtmlCommand(s) },
		"dashboard": func(s *cache.Store) cli.Command { return cli.NewDashboardCommand(s) },
	}
}

// buildStore constructs the federated cache.Store over every connected
// space's mirror (resolving each mirror dir + loading its space.yaml
// manifest). Read verbs never touch the network to build this.
//
// It is TOLERANT of an ABSENT config: a project with no `.a2a/config.yaml`
// (or no connected spaces, or no machine config) yields a store over zero
// mirrors — the read verbs then report empty, and `a2a statusline` stays
// silent + exit 0 (CC-092). A missing config is a normal pre-onboarding
// state, not an error the read path should crash on.
//
// A MALFORMED config (bad YAML, invalid credential reference) is NOT
// tolerated — it surfaces loudly. Silently degrading a broken config to
// "zero connected spaces" would make `a2a inbox`/`statusline` go quietly
// empty while the user has real spaces and a typo, an undiagnosable failure
// the no-swallowed-errors rail exists to prevent. Only os.ErrNotExist (the
// file genuinely absent) is swallowed.
func buildStore(p paths) (*cache.Store, error) {
	cfg, err := space.LoadProjectConfig(p.projectConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load project config: %w", err)
	}
	machine, err := space.LoadMachineConfig(p.machineConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("load machine config: %w", err)
	}
	mirrors := make([]cache.SpaceMirror, 0, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		dir := space.ResolveMirrorLocation(p.projectRoot, ref, machine)
		var manifest space.Manifest
		if m, err := loadManifest(dir); err == nil {
			manifest = m
		} // a not-yet-cloned mirror yields a zero manifest; the store copes
		mirrors = append(mirrors, cache.SpaceMirror{
			SpaceID: ref.ID, Dir: dir, RepoURL: ref.RepoURL, Manifest: manifest,
		})
	}
	store := cache.NewStore(cfg.System, cacheDirOf(p), mirrors, time.Now, 0)
	// P19: enable the proactive update notice on the read store (statusline /
	// inbox / outbox render it; statusline's stale-trigger fires the checker).
	cache.ConfigureUpdateNotice(store, version, machine.Defaults)
	return store, nil
}

// lifecycleDeps is the per-space dependency set every P8 lifecycle/contract
// verb constructor takes (same shape as submit's).
type lifecycleDeps struct {
	funnel       *space.WriteFunnel
	mirrorDir    string
	spaceID      string
	ownSystem    string
	manifest     space.Manifest
	hostCfg      cli.SubmitHostConfig
	resolveActor func(cli.ActorFlags) template.Actor
}

// lifecycleConstructor builds a cli.Command from the resolved deps.
type lifecycleConstructor func(d lifecycleDeps) cli.Command

// lifecycleVerbs maps every OP-211 verb name to its constructor.
func lifecycleVerbs() map[string]lifecycleConstructor {
	simple := func(f func(*space.WriteFunnel, string, string, string, space.Manifest, cli.SubmitHostConfig, func(cli.ActorFlags) template.Actor) cli.Command) lifecycleConstructor {
		return func(d lifecycleDeps) cli.Command {
			return f(d.funnel, d.mirrorDir, d.spaceID, d.ownSystem, d.manifest, d.hostCfg, d.resolveActor)
		}
	}
	return map[string]lifecycleConstructor{
		"ack": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewAckCommand(f, md, sid, own, m, hc, ra)
		}),
		"accept": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewAcceptCommand(f, md, sid, own, m, hc, ra)
		}),
		"decline": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewDeclineCommand(f, md, sid, own, m, hc, ra)
		}),
		"start": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewStartCommand(f, md, sid, own, m, hc, ra)
		}),
		"block": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewBlockCommand(f, md, sid, own, m, hc, ra)
		}),
		"unblock": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewUnblockCommand(f, md, sid, own, m, hc, ra)
		}),
		"cancel": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewCancelCommand(f, md, sid, own, m, hc, ra)
		}),
		"close": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewCloseCommand(f, md, sid, own, m, hc, ra)
		}),
		"withdraw": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewWithdrawCommand(f, md, sid, own, m, hc, ra)
		}),
		"supersede": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewSupersedeCommand(f, md, sid, own, m, hc, ra)
		}),
		"satisfy": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewSatisfyCommand(f, md, sid, own, m, hc, ra)
		}),
		"approve": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewApproveCommand(f, md, sid, own, m, hc, ra)
		}),
		"reject": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewRejectCommand(f, md, sid, own, m, hc, ra)
		}),
		"verify-pass": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewVerifyPassCommand(f, md, sid, own, m, hc, ra)
		}),
		"verify-fail": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewVerifyFailCommand(f, md, sid, own, m, hc, ra)
		}),
		"respond": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewRespondCommand(f, md, sid, own, m, hc, ra)
		}),
		"verify": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewVerifyCommand(f, md, sid, own, m, hc, ra)
		}),
		"dispute": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewDisputeCommand(f, md, sid, own, m, hc, ra)
		}),
		"note": simple(func(f *space.WriteFunnel, md, sid, own string, m space.Manifest, hc cli.SubmitHostConfig, ra func(cli.ActorFlags) template.Actor) cli.Command {
			return cli.NewNoteCommand(f, md, sid, own, m, hc, ra)
		}),
	}
}

// runLifecycle resolves the target space, builds the per-space deps, and
// runs the constructed verb.
func runLifecycle(args []string, stdout, stderr io.Writer, construct lifecycleConstructor) int {
	ctx := context.Background()
	p, err := resolvePaths()
	if err != nil {
		return fail(stderr, err)
	}
	deps, code := resolveLifecycleDeps(ctx, p, args, stderr)
	if code >= 0 {
		return code
	}
	return construct(deps).Run(ctx, args, stdio(stdout, stderr))
}

func runContract(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	p, err := resolvePaths()
	if err != nil {
		return fail(stderr, err)
	}
	cfg, err := space.LoadProjectConfig(p.projectConfig)
	if err != nil {
		return failf(stderr, "a2a contract: no project config (run `a2a init` first): %v", err)
	}
	newCmd := cli.NewNewCommand(p.staging, cfg.System, actorResolver())
	deps, code := resolveLifecycleDeps(ctx, p, args, stderr)
	if code >= 0 {
		return code
	}
	cmd := cli.NewContractCommand(newCmd, deps.funnel, deps.mirrorDir, deps.spaceID, deps.ownSystem, deps.manifest, deps.hostCfg, deps.resolveActor)
	return cmd.Run(ctx, args, stdio(stdout, stderr))
}

// funnelBinaryVersion is the single seam feeding space.NewWriteFunnel across
// every write path (submit + lifecycle/contract). It returns the BARE dotted
// version, never versionStamp() ("a2a x.y.z (sha)"): the funnel's CC-085
// min_binary_version guard parses a bare major.minor.patch, so the full stamp
// makes every write against a version-pinned space fail with "invalid version
// string" (the P11 smoke test surfaced it — same class as the P10 doctor fix +
// the MCP wiring). Centralized here so the two call sites can't drift, and
// guarded by TestFunnelBinaryVersionIsBare.
func funnelBinaryVersion() string { return version }

// resolveLifecycleDeps loads config, resolves the target space (the one
// whose mirror holds the first artifact id in args, else the first
// connected space — so `contract new`/no-arg verbs still get a valid
// funnel context they won't necessarily use), and builds the per-space
// funnel + deps. A non-negative return is a terminal exit code.
func resolveLifecycleDeps(ctx context.Context, p paths, args []string, stderr io.Writer) (lifecycleDeps, int) {
	cfg, err := space.LoadProjectConfig(p.projectConfig)
	if err != nil {
		return lifecycleDeps{}, failf(stderr, "a2a: no project config (run `a2a init` first): %v", err)
	}
	if len(cfg.Spaces) == 0 {
		return lifecycleDeps{}, failf(stderr, "a2a: no connected space (run `a2a connect` first)")
	}
	machine, err := space.LoadMachineConfig(p.machineConfig)
	if err != nil {
		return lifecycleDeps{}, failf(stderr, "a2a: no machine config (%s): %v", p.machineConfig, err)
	}

	ref := resolveTargetSpaceRef(cfg, machine, p.projectRoot, firstArtifactID(args))
	mirrorDir := space.ResolveMirrorLocation(p.projectRoot, ref, machine)
	if err := space.CloneOrFetch(ctx, mirrorDir, ref.RepoURL); err != nil {
		return lifecycleDeps{}, failf(stderr, "a2a: mirror sync failed: %v", err)
	}
	manifest, err := loadManifest(mirrorDir)
	if err != nil {
		return lifecycleDeps{}, failf(stderr, "a2a: %v", err)
	}
	engine, err := newEngine()
	if err != nil {
		return lifecycleDeps{}, fail(stderr, err)
	}
	cred, err := resolveCredential(ctx, ref.ID, machine)
	if err != nil {
		return lifecycleDeps{}, failf(stderr, "a2a: %v", err)
	}
	owner, name, err := parseGitHubRepo(ref.RepoURL)
	if err != nil {
		return lifecycleDeps{}, failf(stderr, "a2a: %v", err)
	}
	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	legality := cli.NewLegalityAdapter(mirrorDir, cfg.System, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, cfg.System, resolver, legality)
	h := host.NewGitHubHost(http.DefaultClient, githubAPIBaseURL)
	funnel := space.NewWriteFunnel(h, validator, funnelBinaryVersion())
	hostCfg := cli.SubmitHostConfig{
		RemoteURL: ref.RepoURL, Repo: host.Repo{Owner: owner, Name: name},
		BaseBranch: defaultBaseBranch, Credential: cred,
		CommitAuthorName: cfg.System, CommitAuthorEmail: cfg.System + "@a2a.local",
	}
	return lifecycleDeps{
		funnel: funnel, mirrorDir: mirrorDir, spaceID: ref.ID, ownSystem: cfg.System,
		manifest: manifest, hostCfg: hostCfg, resolveActor: actorResolver(),
	}, -1
}

// resolveTargetSpaceRef finds the connected space whose mirror already
// holds an <id>.md file, else falls back to the first connected space.
func resolveTargetSpaceRef(cfg space.ProjectConfig, machine space.MachineConfig, projectRoot, id string) space.Ref {
	if id != "" {
		for _, ref := range cfg.Spaces {
			dir := space.ResolveMirrorLocation(projectRoot, ref, machine)
			if mirrorHoldsArtifact(dir, id) {
				return ref
			}
		}
	}
	return cfg.Spaces[0]
}

func mirrorHoldsArtifact(mirrorDir, id string) bool {
	var found bool
	_ = filepath.WalkDir(mirrorDir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil //nolint:nilerr // reason: best-effort walk — mirrorHoldsArtifact already ignores WalkDir's overall error, an inaccessible entry just isn't a match
		}
		// Skip the bare `.git` object store — it never holds artifact files
		// and walking it wastes work that grows with history (matches
		// internal/cache's own walkers).
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == id+".md" {
			found = true
		}
		return nil
	})
	return found
}

// firstArtifactID returns the first non-flag argument (the artifact id most
// lifecycle verbs take first), or "" if there is none.
func firstArtifactID(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

func actorResolver() func(cli.ActorFlags) template.Actor {
	return func(f cli.ActorFlags) template.Actor {
		return cli.ResolveActor(f, cli.HarnessDefaults{}, cli.ConfigActor{})
	}
}

// runSubmit is the config-dependent submit closure. It resolves the target
// space from the staged artifact's `space` field, enforces the AC-201.3
// config-only foreign-section refusal BEFORE any mirror clone, then builds
// the write funnel + validation adapters and runs SubmitCommand.
func runSubmit(args []string, stdout, stderr io.Writer) int {
	ctx := context.Background()
	io := stdio(stdout, stderr)

	p, err := resolvePaths()
	if err != nil {
		return fail(stderr, err)
	}
	cfg, err := space.LoadProjectConfig(p.projectConfig)
	if err != nil {
		return failf(stderr, "a2a submit: no project config (run `a2a init` first): %v", err)
	}

	// Resolve the artifact(s) named on the command line via the SINGLE
	// shared submit-arg resolver (no drifted second copy) — this is what
	// makes `--drafts`, `--batch`, and the bare-id form reach the same
	// targets the SubmitCommand will resolve. Then read every target's
	// envelope facts LOCALLY (no network) so the config-only guards below
	// run before any mirror clone (AC-201.3).
	targets, err := cli.ResolveSubmitTargets(p.staging, args)
	if err != nil {
		return failf(stderr, "a2a submit: %v", err)
	}
	if len(targets) == 0 {
		_, _ = fmt.Fprintln(stdout, "submit: nothing to submit")
		return 0
	}
	facts := make([]envelopeFacts, 0, len(targets))
	for _, t := range targets {
		f, err := readEnvelopeFacts(t)
		if err != nil {
			return failf(stderr, "a2a submit: %v", err)
		}
		facts = append(facts, f)
	}

	// AC-201.3 (config-only, BEFORE any clone/network): refuse any
	// foreign-section artifact whose `from` is not this system, and refuse
	// a batch spanning multiple spaces (one submit = one space = one PR).
	for _, f := range facts {
		if f.from != cfg.System {
			return failf(stderr, "a2a submit: refused — artifact `from: %s` is not this system (%s) [CC-002]", f.from, cfg.System)
		}
		if f.space != facts[0].space {
			return failf(stderr, "a2a submit: refused — batch spans multiple spaces (%q vs %q)", facts[0].space, f.space)
		}
	}

	// Resolve the target space from the artifact's `space` field.
	ref, ok := findSpace(cfg, facts[0].space)
	if !ok {
		return failf(stderr, "a2a submit: artifact space %q is not a connected space (run `a2a connect`)", facts[0].space)
	}

	// Machine config (credential refs + mirror root) is needed only from
	// here on — after the config-only guards, before any network work.
	machine, err := space.LoadMachineConfig(p.machineConfig)
	if err != nil {
		return failf(stderr, "a2a submit: no machine config (%s): %v", p.machineConfig, err)
	}

	mirrorDir := space.ResolveMirrorLocation(p.projectRoot, ref, machine)
	if err := space.CloneOrFetch(ctx, mirrorDir, ref.RepoURL); err != nil {
		return failf(stderr, "a2a submit: mirror sync failed: %v", err)
	}
	manifest, err := loadManifest(mirrorDir)
	if err != nil {
		return failf(stderr, "a2a submit: %v", err)
	}

	engine, err := newEngine()
	if err != nil {
		return fail(stderr, err)
	}
	cred, err := resolveCredential(ctx, ref.ID, machine)
	if err != nil {
		return failf(stderr, "a2a submit: %v", err)
	}
	owner, name, err := parseGitHubRepo(ref.RepoURL)
	if err != nil {
		return failf(stderr, "a2a submit: %v", err)
	}

	resolver := cli.NewMirrorResolver(mirrorDir, manifest)
	legality := cli.NewLegalityAdapter(mirrorDir, cfg.System, manifest)
	validator := cli.NewSubmitValidatorAdapter(engine, cfg.System, resolver, legality)
	h := host.NewGitHubHost(http.DefaultClient, githubAPIBaseURL)
	funnel := space.NewWriteFunnel(h, validator, funnelBinaryVersion())

	hostCfg := cli.SubmitHostConfig{
		RemoteURL:         ref.RepoURL,
		Repo:              host.Repo{Owner: owner, Name: name},
		BaseBranch:        defaultBaseBranch,
		Credential:        cred,
		CommitAuthorName:  cfg.System,
		CommitAuthorEmail: cfg.System + "@a2a.local",
	}
	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewCacheBackedPendingMarker(cacheDirOf(p)), mirrorDir, ref.ID, cfg.System, p.staging, hostCfg)
	return cmd.Run(ctx, args, io)
}

// canonicalFeedbackRepo is the default `a2a feedback submit` target (§T1) when
// neither --repo nor A2A_FEEDBACK_REPO is set: the product repo itself.
const canonicalFeedbackRepo = "https://github.com/ydnikolaev/a2ahub"

// feedbackToken resolves the push credential for `a2a feedback submit`. Feedback
// targets a fixed repo, not a connected space, so it does not use the machine
// config's per-space credential refs; it reads an ambient token (A2A_FEEDBACK_
// TOKEN, else GITHUB_TOKEN/GH_TOKEN). Empty is tolerated — only a real push
// needs it (the e2e submit path uses FakeHost, §11 A5).
func feedbackToken() string {
	for _, k := range []string{"A2A_FEEDBACK_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// runFeedback wires `a2a feedback <new|validate|submit|status|triage>`. Unlike
// submit it targets a FIXED product repo (canonicalFeedbackRepo, or the
// --repo/A2A_FEEDBACK_REPO override — §11 A8), never a connected space, and runs
// its OWN validation (feedback is not an envelope, I1) — so the shared
// space.WriteFunnel is wired with a nil envelope validator (feedback pre-
// validates before Submit). All state lives under the project-local
// .a2a/feedback/ (gitignored), so no `a2a init`/connect is required.
func runFeedback(args []string, stdout, stderr io.Writer) int {
	p, err := resolvePaths()
	if err != nil {
		return fail(stderr, err)
	}
	feedbackDir := filepath.Join(p.projectRoot, ".a2a", "feedback")
	ledgerPath := filepath.Join(feedbackDir, "ledger.yaml")

	drafter := feedback.NewDrafter(feedbackDir, version)

	repoURL := os.Getenv("A2A_FEEDBACK_REPO")
	if repoURL == "" {
		repoURL = canonicalFeedbackRepo
	}
	owner, name, err := parseGitHubRepo(repoURL)
	if err != nil {
		return failf(stderr, "a2a feedback: bad feedback repo %q: %v", repoURL, err)
	}

	h := host.NewGitHubHost(http.DefaultClient, githubAPIBaseURL)
	funnel := space.NewWriteFunnel(h, nil, funnelBinaryVersion())
	submitCfg := feedback.SubmitConfig{
		RemoteURL:         repoURL,
		Repo:              host.Repo{Owner: owner, Name: name},
		BaseBranch:        defaultBaseBranch,
		Credential:        host.Credential{Token: feedbackToken()},
		CommitAuthorName:  "a2a-feedback",
		CommitAuthorEmail: "a2a-feedback@a2a.local",
	}
	submitter := feedback.NewSubmitter(funnel, ledgerPath, p.projectRoot, owner+"-"+name, submitCfg)

	hubReader := feedback.DefaultHubReader(http.DefaultClient,
		"https://raw.githubusercontent.com/"+owner+"/"+name+"/"+defaultBaseBranch)

	hubRoot, err := os.Getwd()
	if err != nil {
		return fail(stderr, err)
	}

	cmd := cli.NewFeedbackCommand(drafter, submitter, ledgerPath, hubRoot, hubReader)
	return cmd.Run(context.Background(), args, stdio(stdout, stderr))
}

// envelopeFacts is the minimal frontmatter the submit closure reads locally
// before any network work.
type envelopeFacts struct {
	from  string
	space string
}

func readEnvelopeFacts(path string) (envelopeFacts, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // reason: path is a staged draft under the project's own .a2a/staging, resolved from user args
	if err != nil {
		return envelopeFacts{}, fmt.Errorf("read draft %s: %w", path, err)
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return envelopeFacts{}, fmt.Errorf("parse draft %s: %w", path, err)
	}
	inst, err := schema.DecodeYAMLInstance(fm.YAML)
	if err != nil {
		return envelopeFacts{}, fmt.Errorf("decode draft %s: %w", path, err)
	}
	m, ok := inst.(map[string]any)
	if !ok {
		return envelopeFacts{}, fmt.Errorf("draft %s: frontmatter is not a mapping", path)
	}
	from, _ := m["from"].(string)
	sp, _ := m["space"].(string)
	if from == "" || sp == "" {
		return envelopeFacts{}, fmt.Errorf("draft %s: missing `from` or `space`", path)
	}
	return envelopeFacts{from: from, space: sp}, nil
}

func findSpace(cfg space.ProjectConfig, spaceID string) (space.Ref, bool) {
	for _, r := range cfg.Spaces {
		if r.ID == spaceID {
			return r, true
		}
	}
	return space.Ref{}, false
}

func loadManifest(mirrorDir string) (space.Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		return space.Manifest{}, fmt.Errorf("read space.yaml: %w", err)
	}
	return space.ParseManifest(raw)
}

func resolveCredential(ctx context.Context, spaceID string, machine space.MachineConfig) (host.Credential, error) {
	refStr, ok := machine.Credentials[spaceID]
	if !ok {
		return host.Credential{}, fmt.Errorf("no credential reference for space %q in machine config", spaceID)
	}
	ref, err := space.ParseCredentialReference(refStr)
	if err != nil {
		return host.Credential{}, err
	}
	return space.ResolveCredential(ctx, "A2A_TOKEN_"+strings.ToUpper(spaceID), ref)
}

// parseGitHubRepo extracts owner/name from a GitHub remote URL
// (https://github.com/<owner>/<name>[.git] or git@github.com:<owner>/<name>).
func parseGitHubRepo(url string) (owner, name string, err error) {
	s := strings.TrimSuffix(url, ".git")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[len(parts)-2] == "" || parts[len(parts)-1] == "" {
		return "", "", fmt.Errorf("cannot parse owner/name from repo URL %q", url)
	}
	return parts[len(parts)-2], parts[len(parts)-1], nil
}

func newEngine() (*validate.Engine, error) {
	corpus, err := schema.Load()
	if err != nil {
		return nil, err
	}
	return validate.New(corpus), nil
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, err)
	return 1
}

func failf(stderr io.Writer, format string, a ...any) int {
	_, _ = fmt.Fprintf(stderr, format+"\n", a...)
	return 1
}
