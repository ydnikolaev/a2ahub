package mcp

// wire.go is internal/mcp's own DI-construction point — the mcp-side
// mirror of cmd/a2a/wire.go's config -> mirror -> funnel/store/engine
// construction (plan 14 Placement decisions, binding: duplicated, not
// shared, since an mcp server is a long-lived stdio session whose wiring
// legitimately differs from the CLI's per-invocation wiring). It does NOT
// import cmd/a2a or internal/cli.
//
// Deviation (see this phase's report): a long-lived MCP session may have
// MULTIPLE connected spaces, but every write tool ultimately targets ONE
// space per call (the CLI resolves it per-invocation from the first
// artifact id in args — see cmd/a2a/wire.go's resolveTargetSpaceRef).
// This constructor builds write-tool deps for the FIRST connected space
// only (a real multi-space server would need to re-resolve per call,
// mirroring resolveTargetSpaceRef, which this phase's own tests do not
// exercise and which the equivalence suite constructs directly instead —
// out of scope for this tail phase's ~1 day budget).

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/cache"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

const (
	githubAPIBaseURL  = "https://api.github.com"
	defaultBaseBranch = "main"
)

// Paths bundles the resolved config/staging locations the mcp server
// needs — mirrors cmd/a2a/wire.go's own paths struct.
type Paths struct {
	ProjectConfig string
	MachineConfig string
	ProjectRoot   string
	Staging       string
}

// ResolvePaths resolves Paths from the current working directory and the
// user's home directory — mirrors cmd/a2a/wire.go's resolvePaths.
func ResolvePaths() (Paths, error) {
	root, err := os.Getwd()
	if err != nil {
		return Paths{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ProjectConfig: filepath.Join(root, ".a2a", "config.yaml"),
		MachineConfig: filepath.Join(home, ".config", "a2a", "config.yaml"),
		ProjectRoot:   root,
		Staging:       filepath.Join(root, ".a2a", "staging"),
	}, nil
}

func cacheDirOf(p Paths) string { return filepath.Join(p.ProjectRoot, ".a2a", "cache") }

// NewServerFromConfig loads the project+machine config once (a long-lived
// session's own "load config once" contract, plan 14 Brief item 5),
// builds the federated cache.Store, resolves the FIRST connected space's
// write-tool dependencies (see this file's own deviation note), and
// returns a ready-to-Serve Server registered with the full §7.7 tool set.
func NewServerFromConfig(ctx context.Context, p Paths, binaryVersion string) (*Server, error) {
	cfg, err := space.LoadProjectConfig(p.ProjectConfig)
	if err != nil {
		return nil, fmt.Errorf("mcp: load project config: %w", err)
	}
	machine, err := space.LoadMachineConfig(p.MachineConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mcp: load machine config: %w", err)
	}

	store, err := buildStore(cfg, machine, p, binaryVersion)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}

	registry := NewRegistry()
	if len(cfg.Spaces) == 0 {
		// No connected space yet: read tools still work (over zero
		// mirrors, cache.Store's own tolerant-of-absent-config contract);
		// write tools have nothing to target, so this phase registers
		// none — mirrors the CLI's own "no connected space" refusal, just
		// surfaced as an absent tool rather than a runtime error.
		registerReadOnly(registry, store)
		return NewServer(registry, "a2a-mcp", binaryVersion, nil), nil
	}

	write, submitDeps, newDeps, err := buildWriteDeps(ctx, cfg, machine, p, binaryVersion)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}
	registry = BuildRegistry(store, write, submitDeps.StagingDir, submitDeps.Legality, newDeps)
	return NewServer(registry, "a2a-mcp", binaryVersion, nil), nil
}

// registerReadOnly registers only the six read tools — used when no
// space is connected yet (write tools have no legitimate target).
func registerReadOnly(r *Registry, store *cache.Store) {
	r.Register(ToolSpec{Name: "a2a_inbox", Handler: newInboxHandler(store)})
	r.Register(ToolSpec{Name: "a2a_outbox", Handler: newOutboxHandler(store)})
	r.Register(ToolSpec{Name: "a2a_show", Handler: newShowHandler(store)})
	r.Register(ToolSpec{Name: "a2a_thread", Handler: newThreadHandler(store)})
	r.Register(ToolSpec{Name: "a2a_search", Handler: newSearchHandler(store)})
	r.Register(ToolSpec{Name: "a2a_contracts", Handler: newContractsHandler(store)})
}

func buildStore(cfg space.ProjectConfig, machine space.MachineConfig, p Paths, binaryVersion string) (*cache.Store, error) {
	mirrors := make([]cache.SpaceMirror, 0, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		dir := space.ResolveMirrorLocation(p.ProjectRoot, ref, machine)
		var manifest space.Manifest
		if raw, err := os.ReadFile(filepath.Join(dir, "space.yaml")); err == nil {
			if m, merr := space.ParseManifest(raw); merr == nil {
				manifest = m
			}
		}
		mirrors = append(mirrors, cache.SpaceMirror{SpaceID: ref.ID, Dir: dir, RepoURL: ref.RepoURL, Manifest: manifest})
	}
	store := cache.NewStore(cfg.System, cacheDirOf(p), mirrors, time.Now, 0)
	// P19: a2a_read surfaces the update advisory on its text body from this
	// store's UpdateNotice (checker inert here — MCP reads are cache-read-only
	// for the notice per T3; sync/statusline/update --check refresh the cache).
	cache.ConfigureUpdateNotice(store, binaryVersion, machine.Defaults)
	return store, nil
}

// buildWriteDeps resolves the FIRST connected space's mirror (cloning/
// fetching it if needed), engine, credential, and repo facts, then builds
// WriteDeps/SubmitDeps/NewDeps over it — mirrors cmd/a2a/wire.go's own
// resolveLifecycleDeps for a single, statically-chosen space (this file's
// own deviation, see the doc comment above).
func buildWriteDeps(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig, p Paths, binaryVersion string) (WriteDeps, SubmitDeps, NewDeps, error) {
	ref := cfg.Spaces[0]
	mirrorDir := space.ResolveMirrorLocation(p.ProjectRoot, ref, machine)
	if err := space.CloneOrFetch(ctx, mirrorDir, ref.RepoURL); err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, fmt.Errorf("mirror sync failed: %w", err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(mirrorDir, "space.yaml"))
	if err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, fmt.Errorf("read space.yaml: %w", err)
	}
	manifest, err := space.ParseManifest(manifestRaw)
	if err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, fmt.Errorf("parse manifest: %w", err)
	}

	corpus, err := schema.Load()
	if err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, err
	}
	engine := validate.New(corpus)

	// The machine-config reference is OPTIONAL: the explicit
	// A2A_TOKEN_<SPACE_ID> override is precedence step (a) and must be
	// sufficient on its own (same contract as cmd/a2a's own
	// resolveCredential — an unresolvable credential then reports what it
	// checked, rather than pre-empting the check).
	var credRef space.CredentialReference
	if credRefStr, ok := machine.Credentials[ref.ID]; ok {
		parsed, perr := space.ParseCredentialReference(credRefStr)
		if perr != nil {
			return WriteDeps{}, SubmitDeps{}, NewDeps{}, perr
		}
		credRef = parsed
	}
	cred, err := space.ResolveCredential(ctx, space.CredentialEnvVar(ref.ID), credRef)
	if err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, err
	}
	owner, name, err := parseGitHubRepo(ref.RepoURL)
	if err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, err
	}

	resolver := NewMirrorResolver(mirrorDir, manifest)
	legality := NewLegalityAdapter(mirrorDir, cfg.System, manifest)
	validator := NewSubmitValidatorAdapter(engine, cfg.System, resolver, legality)
	h := host.NewGitHubHost(http.DefaultClient, githubAPIBaseURL)
	funnel := space.NewWriteFunnel(h, validator, binaryVersion)

	hostCfg := SubmitHostConfig{
		RemoteURL: ref.RepoURL, Repo: host.Repo{Owner: owner, Name: name},
		BaseBranch: defaultBaseBranch, Credential: cred,
		CommitAuthorName: cfg.System, CommitAuthorEmail: cfg.System + "@a2a.local",
	}
	write := WriteDeps{
		Funnel: funnel, MirrorDir: mirrorDir, SpaceID: ref.ID, OwnSystem: cfg.System,
		Manifest: manifest, HostCfg: hostCfg, ResolveActor: resolveActor,
		Now: time.Now, Entropy: rand.Reader, ReadFile: os.ReadFile,
	}
	submitDeps := SubmitDeps{WriteDeps: write, StagingDir: p.Staging, Legality: legality}
	newDeps := NewDeps{
		StagingDir: p.Staging, OwnSystem: cfg.System, Now: time.Now, Entropy: rand.Reader,
		ResolveActor: resolveActor, WriteFile: os.WriteFile,
	}
	return write, submitDeps, newDeps, nil
}

// parseGitHubRepo extracts owner/name from a GitHub remote URL — mirrors
// cmd/a2a/wire.go's parseGitHubRepo.
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
