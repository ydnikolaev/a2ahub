package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/template"
	"github.com/ydnikolaev/a2ahub/internal/validate"
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
		return cli.NewInitCommand(p.projectConfig).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["template"] = func(args []string, stdout, stderr io.Writer) int {
		return cli.NewTemplateCommand().Run(context.Background(), args, stdio(stdout, stderr))
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
		return cli.NewDisconnectCommand(p.projectConfig, p.machineConfig, p.projectRoot, cli.NewNoopCacheRemover()).Run(context.Background(), args, stdio(stdout, stderr))
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
		return cli.NewValidateCommand(engine, p.staging).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["sync"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		return cli.NewSyncCommand(p.projectConfig, p.machineConfig, p.projectRoot, cli.NewNoopPendingMarker()).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["doctor"] = func(args []string, stdout, stderr io.Writer) int {
		p, err := resolvePaths()
		if err != nil {
			return fail(stderr, err)
		}
		h := host.NewGitHubHost(http.DefaultClient, githubAPIBaseURL)
		return cli.NewDoctorCommand(h, versionStamp(), p.projectConfig, p.machineConfig, p.projectRoot).Run(context.Background(), args, stdio(stdout, stderr))
	}
	m["submit"] = runSubmit

	return m
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
	machine, err := space.LoadMachineConfig(p.machineConfig)
	if err != nil {
		return failf(stderr, "a2a submit: no machine config (%s): %v", p.machineConfig, err)
	}

	// Resolve the artifact(s) named on the command line to their staged
	// files, read the first's envelope facts (local, no network).
	artifactPaths, err := submitArtifactPaths(args, p.staging)
	if err != nil {
		return failf(stderr, "a2a submit: %v", err)
	}
	env, err := readEnvelopeFacts(artifactPaths[0])
	if err != nil {
		return failf(stderr, "a2a submit: %v", err)
	}

	// AC-201.3 (config-only, BEFORE any clone/network): refuse a
	// foreign-section artifact whose `from` is not this system.
	if env.from != cfg.System {
		return failf(stderr, "a2a submit: refused — artifact `from: %s` is not this system (%s) [CC-002]", env.from, cfg.System)
	}

	// Resolve the target space from the artifact's `space` field.
	ref, ok := findSpace(cfg, env.space)
	if !ok {
		return failf(stderr, "a2a submit: artifact space %q is not a connected space (run `a2a connect`)", env.space)
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
	funnel := space.NewWriteFunnel(h, validator, versionStamp())

	hostCfg := cli.SubmitHostConfig{
		RemoteURL:         ref.RepoURL,
		Repo:              host.Repo{Owner: owner, Name: name},
		BaseBranch:        defaultBaseBranch,
		Credential:        cred,
		CommitAuthorName:  cfg.System,
		CommitAuthorEmail: cfg.System + "@a2a.local",
	}
	cmd := cli.NewSubmitCommand(funnel, legality, cli.NewNoopPendingMarker(), mirrorDir, ref.ID, cfg.System, p.staging, hostCfg)
	return cmd.Run(ctx, args, io)
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

// submitArtifactPaths turns the submit args into staged file paths. It
// accepts `submit <artifact>`; batch/drafts flag parsing is SubmitCommand's
// own concern, but the closure needs at least one path to read the target
// space, so it extracts the first non-flag argument here.
func submitArtifactPaths(args []string, staging string) ([]string, error) {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if filepath.IsAbs(a) || strings.Contains(a, string(filepath.Separator)) {
			out = append(out, a)
		} else {
			out = append(out, filepath.Join(staging, a))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no artifact named (usage: a2a submit <artifact>)")
	}
	return out, nil
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
	raw, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml")) //nolint:gosec // reason: mirrorDir is the resolved local mirror clone
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
