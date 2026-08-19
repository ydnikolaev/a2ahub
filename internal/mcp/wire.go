package mcp

// wire.go is internal/mcp's own DI-construction point — the mcp-side
// mirror of cmd/a2a/wire.go's config -> mirror -> funnel/store/engine
// construction (plan 14 Placement decisions, binding: duplicated, not
// shared, since an mcp server is a long-lived stdio session whose wiring
// legitimately differs from the CLI's per-invocation wiring). It does NOT
// import cmd/a2a or internal/cli.
//
// The legacy lifecycle write graph below retains its historical single-space
// construction. P6 contract operations are injected by cmd/a2a as a
// per-request router: every contract request can name its connected space,
// and ambiguous multi-space requests fail closed instead of inheriting the
// first configured connection.

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

// githubAPIEnv / githubAPIBase mirror cmd/a2a's own resolver — the MCP
// surface wires the same host against the same knob (GitHub Enterprise, and
// an e2e harness that controls the host). The duplication across the two
// wiring points is ADR-001's deliberate one: internal/mcp never imports
// internal/cli, and cmd/a2a's copy is not importable from here.
const githubAPIEnv = "A2A_GITHUB_API"

func githubAPIBase() string {
	if v := os.Getenv(githubAPIEnv); v != "" {
		return v
	}
	return githubAPIBaseURL
}

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
// builds the federated cache.Store, and accepts the work dependency graph
// composed once at cmd/a2a. That graph routes an explicit space for every
// multi-space call; MCP never composes or silently selects a space itself.
// The result is a ready-to-Serve Server with the full §7.7 tool set.
func NewServerFromConfig(ctx context.Context, p Paths, binaryVersion string, injectedWork ...WorkToolDeps) (*Server, error) {
	return newServerFromConfig(ctx, p, binaryVersion, nil, nil, injectedWork...)
}

// ContractOperationsFactory is cmd/a2a's production composition seam. It is
// invoked only after the existing bounded mirror/config wiring succeeds, so
// an unreachable space retains this constructor's read-only degradation.
type ContractOperationsFactory func(ActorResolver) (ContractToolOperations, error)

// DataOperationsFactory is cmd/a2a's production composition seam for
// a2a_data — the exact same shape as ContractOperationsFactory (an injected
// ActorResolver so cmd/a2a's factory can resolve §7.4 identity without this
// package exporting its own unexported resolveActor), invoked the same way
// (newServerFromConfig calls it with this package's own resolveActor,
// mirroring buildContractOperations(resolveActor) exactly). It differs only
// in what a nil/failing factory costs: contract operations are a hard
// dependency of the P6 grouped tool, so a failing factory there IS a
// startup failure, while a2a_data already has a shipped, named degraded
// path (DataToolDeps{}'s zero value, NewDataTool's own doc comment) — a nil
// or failing data factory therefore never fails the session, it only keeps
// a2a_data degraded. See newServerFromConfig's own handling.
type DataOperationsFactory func(ActorResolver) (DataToolDeps, error)

// NewServerFromConfigWithContractOperations is the production constructor
// used by cmd/a2a for callers not yet supplying data operations. P6
// filesystem/Git/domain adapters are composed at cmd/a2a and injected here;
// internal/mcp owns only transport registration. It delegates to
// NewServerFromConfigWithOperations with a nil data factory, so a2a_data
// stays degraded exactly as it did before that constructor existed.
func NewServerFromConfigWithContractOperations(ctx context.Context, p Paths, binaryVersion string, work WorkToolDeps, buildContractOperations ContractOperationsFactory) (*Server, error) {
	return NewServerFromConfigWithOperations(ctx, p, binaryVersion, work, buildContractOperations, nil)
}

// NewServerFromConfigWithOperations is the production constructor used by
// cmd/a2a once a data operations factory is available, threading BOTH
// contract and data dependencies. P6/data filesystem/Git/domain adapters are
// composed at cmd/a2a and injected here; internal/mcp owns only transport
// registration.
func NewServerFromConfigWithOperations(ctx context.Context, p Paths, binaryVersion string, work WorkToolDeps, buildContractOperations ContractOperationsFactory, buildDataOperations DataOperationsFactory) (*Server, error) {
	if buildContractOperations == nil {
		return nil, fmt.Errorf("mcp: production contract dependency factory must be injected by cmd/a2a")
	}
	return newServerFromConfig(ctx, p, binaryVersion, buildContractOperations, buildDataOperations, work)
}

func newServerFromConfig(ctx context.Context, p Paths, binaryVersion string, buildContractOperations ContractOperationsFactory, buildDataOperations DataOperationsFactory, injectedWork ...WorkToolDeps) (*Server, error) {
	cfg, err := space.LoadProjectConfig(p.ProjectConfig)
	if err != nil {
		return nil, fmt.Errorf("mcp: load project config: %w", err)
	}
	machine, err := space.LoadMachineConfig(p.MachineConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("mcp: load machine config: %w", err)
	}

	store, err := buildStore(ctx, cfg, machine, p, binaryVersion)
	if err != nil {
		return nil, fmt.Errorf("mcp: %w", err)
	}

	registry := NewRegistry()
	if len(cfg.Spaces) == 0 {
		// No connected space yet: the space-free tools still work (over zero
		// mirrors, cache.Store's own tolerant-of-absent-config contract);
		// write tools have nothing to target, so this phase registers
		// none — mirrors the CLI's own "no connected space" refusal, just
		// surfaced as an absent tool rather than a runtime error.
		registerSpaceFree(registry, store)
		return NewServer(registry, "a2a-mcp", binaryVersion, nil), nil
	}
	if len(injectedWork) != 1 {
		return nil, fmt.Errorf("mcp: production work dependencies must be injected by cmd/a2a")
	}
	workDeps := injectedWork[0]
	workTool, err := NewWorkTool(workDeps)
	if err != nil {
		return nil, err
	}
	var contractOperations ContractToolOperations
	if buildContractOperations != nil {
		contractOperations, err = buildContractOperations(resolveActor)
		if err != nil {
			return nil, fmt.Errorf("mcp: build contract dependencies: %w", err)
		}
		if !contractOperations.complete() {
			return nil, fmt.Errorf("mcp: production contract dependencies are incomplete")
		}
	}
	var dataOperations DataToolDeps
	if buildDataOperations != nil {
		dataOperations, err = buildDataOperations(resolveActor)
		if err != nil {
			// Unlike contract operations above, a2a_data has a shipped,
			// named degraded path (DataOperationsFactory's own doc
			// comment): the error is reported to stderr and swallowed
			// rather than failing the whole session.
			fmt.Fprintf(os.Stderr,
				"a2a mcp: a2a_data unavailable — could not build data dependencies (%v); serving it degraded.\n", err)
			dataOperations = DataToolDeps{}
		}
	}

	write, submitDeps, newDeps, err := buildWriteDeps(ctx, cfg, machine, p, binaryVersion)
	if err != nil {
		// An unreachable space must not cost the agent its READ tools.
		//
		// buildWriteDeps clones or fetches the mirror, so a network blip, an
		// expired credential, or a laptop on a plane made the whole server
		// fail to START — while every read tool needs nothing but the local
		// mirror that is already on disk. The CLI's own read path has made
		// the opposite choice since CC-092 (buildStore is explicitly tolerant
		// of an absent config, absent spaces, absent machine config), and
		// this is the same judgement: a degraded session that can still tell
		// you what your counterparty sent beats a session that will not open.
		//
		// The degradation is NAMED, not silent — an agent whose write tools
		// simply were not registered would otherwise report "unknown tool"
		// and have no idea why. stderr is safe: stdout is the JSON-RPC
		// channel and this package never writes there.
		fmt.Fprintf(os.Stderr,
			"a2a mcp: write tools unavailable — could not reach the space (%v).\n"+
				"a2a mcp: serving local read/contract tools over the mirror; re-start once the space is reachable.\n", err)
		registerSpaceFree(registry, store)
		registry.Register(workTool)
		if contractOperations.complete() {
			ref := cfg.Spaces[0]
			registerContractTool(registry, NewDeps{
				StagingDir: p.Staging, OwnSystem: cfg.System, Now: time.Now, Entropy: rand.Reader,
				ResolveActor: resolveActor, WriteFile: os.WriteFile,
			}, ContractDeps{
				WriteDeps: WriteDeps{
					MirrorDir: space.ResolveMirrorLocation(p.ProjectRoot, ref, machine), RepoURL: ref.RepoURL,
					SpaceID: ref.ID, OwnSystem: cfg.System, ResolveActor: resolveActor,
				},
				StagingDir: p.Staging, Publication: contractOperations.Publication,
				Materialize: contractOperations.Materialize, Check: contractOperations.Check,
				Inspection: contractOperations.Inspection,
			}, true)
		}
		return NewServer(registry, "a2a-mcp", binaryVersion, nil), nil
	}
	// P7 (one agent, one space): EVERY write family now resolves its own
	// target per call, so this install is no longer how a multi-space
	// session behaves — it is the backstop underneath them.
	//
	//   a2a_submit          reads the draft envelope's own `space:`
	//   a2a_lifecycle       derives from the ids the call carries
	//   a2a_exchange        derives from parent_ids / targets / ids
	//   a2a_contract legacy takes the explicit "space" input its own P6
	//                       siblings already take (a contract commits to
	//                       <slug>/provides/<name>/contract.md, not
	//                       "<id>.md", so deriving is unavailable there)
	//
	// Each of those resolves BEFORE its first mirror read and receives a
	// per-space WriteDeps carrying that space's REAL funnel — buildWriteDeps
	// stores its per-space builds by VALUE, so the mutation below cannot
	// reach them. What is left refusing here is a write that resolved
	// nothing: a handler added later without a resolve call, or a path none
	// of the four shapes above covers. That is the property worth keeping —
	// a new write tool's default is REFUSAL, not a silent landing in
	// Spaces[0], which is how this defect was born. A single connected
	// space is untouched (US-2, no default, ever).
	if len(cfg.Spaces) > 1 {
		write.Funnel = ambiguousSpaceFunnel{connected: spaceIDs(cfg.Spaces)}
	}
	registry = BuildRegistryWithOperations(store, write, submitDeps, newDeps, contractOperations, dataOperations, workDeps)
	srv := NewServer(registry, "a2a-mcp", binaryVersion, nil)

	// The session refreshes its mirror before every non-contract write/read
	// tool call. P6 contract reads are explicitly offline, while contract
	// publish freezes its candidate before its shared service refreshes main;
	// refreshing the grouped contract tool here would violate both contracts.
	// Without this hook for the remaining tools,
	// the CloneOrFetch above is the ONLY one the process ever runs: every
	// later call — including the legality fold that decides whether a
	// transition is legal — reads a working tree frozen at server start.
	//
	// The refresh takes the mirror's own lock and releases it before the
	// funnel acquires its own, so the two holds are sequential and cannot
	// nest (internal/space/mirrorlock.go's budget doc spells out why that
	// matters).
	//
	// P7: SetPreCall only ever receives the tool NAME, never the call's
	// arguments, so it cannot single out the one space a multi-space call
	// actually named even where a tool has a "space" field. Refreshing
	// EVERY connected space's mirror before every non-work/non-contract
	// call is therefore both the safe and the only available answer here —
	// the single-space case loops once and behaves exactly as before,
	// while a two-space session no longer leaves the SECOND space's mirror
	// frozen at server start for the life of the process (the same
	// Spaces[0]-only root cause this phase closes for writes, just on the
	// read-refresh seam instead of the write-funnel one).
	refreshSpaces := append([]space.Ref(nil), cfg.Spaces...)
	srv.SetPreCall(func(ctx context.Context, tool string) error {
		if tool == WorkToolName || tool == "a2a_contract" {
			return nil
		}
		for _, ref := range refreshSpaces {
			dir := space.ResolveMirrorLocation(p.ProjectRoot, ref, machine)
			if err := space.CloneOrFetch(ctx, dir, ref.RepoURL, readMirrorCredential(ctx, ref.ID, machine)); err != nil {
				return err
			}
		}
		return nil
	})
	return srv, nil
}

func buildStore(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig, p Paths, binaryVersion string) (*cache.Store, error) {
	mirrors := make([]cache.SpaceMirror, 0, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		dir := space.ResolveMirrorLocation(p.ProjectRoot, ref, machine)
		var manifest space.Manifest
		if raw, err := os.ReadFile(filepath.Join(dir, "space.yaml")); err == nil {
			if m, merr := space.ParseManifest(raw); merr == nil {
				manifest = m
			}
		}
		mirrors = append(mirrors, cache.SpaceMirror{
			SpaceID: ref.ID, Dir: dir, RepoURL: ref.RepoURL,
			Credential: readMirrorCredential(ctx, ref.ID, machine), Manifest: manifest,
		})
	}
	store := cache.NewStore(cfg.System, cacheDirOf(p), mirrors, time.Now, 0)
	// P19: a2a_read surfaces the update advisory on its text body from this
	// store's UpdateNotice (checker inert here — MCP reads are cache-read-only
	// for the notice per T3; sync/statusline/update --check refresh the cache).
	cache.ConfigureUpdateNotice(store, binaryVersion, machine.Defaults)
	return store, nil
}

// buildWriteDeps builds a per-space dependency set for EVERY connected
// space, then returns Spaces[0]'s own set — mirrors cmd/a2a/wire.go's own
// resolveLifecycleDeps for a single, statically-chosen space (this file's
// own deviation, see the doc comment above), which is why every eager
// caller (wire.go:191's degraded path, the len(cfg.Spaces) == 1 install
// path below) still sees exactly what it did before this wave.
//
// When more than one space is connected, the returned WriteDeps also
// carries ResolveSpace/SpaceOfArtifacts closures over every OTHER space's
// built set — the per-call seam the NEXT wave's handlers consume. Nothing
// calls them yet: ambiguousSpaceFunnel below still refuses every legacy
// write exactly as before, and ONLY Spaces[0]'s build failing changes what
// the caller sees (a DIFFERENT space's build failure is stored and surfaced
// only if THAT space is later resolved — never at server start, never for
// the space actually in use).
func buildWriteDeps(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig, p Paths, binaryVersion string) (WriteDeps, SubmitDeps, NewDeps, error) {
	corpus, err := schema.Load()
	if err != nil {
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, err
	}
	engine := validate.New(corpus)

	// spaceBuild is this function's own per-space result — a VALUE (not a
	// pointer) so bySpace's entries are independent copies. That matters:
	// wire.go's len(cfg.Spaces) > 1 install path below mutates the RETURNED
	// write's Funnel field (write.Funnel = ambiguousSpaceFunnel{...}) after
	// this function returns. A pointer map would alias that mutation into
	// every space's stored build, so a later ResolveSpace(otherSpaceID) call
	// would hand back the funnel that refuses everything instead of the real
	// one — silently breaking the very seam this wave ships. Value semantics
	// make that impossible: mutating the copy returned to the caller cannot
	// reach bySpace's own stored copies.
	type spaceBuild struct {
		write   WriteDeps
		submit  SubmitDeps
		newDeps NewDeps
		err     error
	}
	bySpace := make(map[string]spaceBuild, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		write, submitDeps, newDeps, buildErr := buildWriteDepsForSpace(ctx, cfg, machine, p, binaryVersion, ref, engine)
		bySpace[ref.ID] = spaceBuild{write: write, submit: submitDeps, newDeps: newDeps, err: buildErr}
	}

	primary := bySpace[cfg.Spaces[0].ID]
	if primary.err != nil {
		// Spaces[0] is the only build this function's caller reads
		// synchronously (wire.go:191's degraded path) — a DIFFERENT
		// connected space failing here must cost that caller nothing, so
		// only Spaces[0]'s own error is returned.
		return WriteDeps{}, SubmitDeps{}, NewDeps{}, primary.err
	}

	if len(cfg.Spaces) > 1 {
		connected := spaceIDs(cfg.Spaces)
		primary.write.ResolveSpace = func(spaceID string) (WriteDeps, error) {
			if spaceID == "" {
				return WriteDeps{}, fmt.Errorf(
					"mcp: space is required when multiple spaces are connected; connected spaces are %s",
					strings.Join(connected, ", "))
			}
			b, ok := bySpace[spaceID]
			if !ok {
				return WriteDeps{}, fmt.Errorf(
					"mcp: space %q is not connected; connected spaces are %s", spaceID, strings.Join(connected, ", "))
			}
			if b.err != nil {
				// Surfaced only because THIS space is the one targeted — see
				// this function's own doc comment.
				return WriteDeps{}, b.err
			}
			return b.write, nil
		}
		// SubmitDeps' own resolver — mirrors primary.write.ResolveSpace above
		// exactly (same refusals, same wording), but binds bySpace[id].submit
		// so a resolved call's Legality is that space's own mirror-bound
		// build, not Spaces[0]'s (tools_submit.go's SubmitDeps.ResolveSpace
		// doc comment spells out the trap this closes).
		primary.submit.ResolveSpace = func(spaceID string) (SubmitDeps, error) {
			if spaceID == "" {
				return SubmitDeps{}, fmt.Errorf(
					"mcp: space is required when multiple spaces are connected; connected spaces are %s",
					strings.Join(connected, ", "))
			}
			b, ok := bySpace[spaceID]
			if !ok {
				return SubmitDeps{}, fmt.Errorf(
					"mcp: space %q is not connected; connected spaces are %s", spaceID, strings.Join(connected, ", "))
			}
			if b.err != nil {
				// Surfaced only because THIS space is the one targeted — see
				// this function's own doc comment.
				return SubmitDeps{}, b.err
			}
			return b.submit, nil
		}
		primary.write.SpaceOfArtifacts = func(ids []string) (string, error) {
			resolvedSpace, resolvedID := "", ""
			for _, id := range ids {
				found := ""
				for _, ref := range cfg.Spaces {
					dir := space.ResolveMirrorLocation(p.ProjectRoot, ref, machine)
					if mirrorHoldsArtifact(dir, id) {
						found = ref.ID
						break
					}
				}
				if found == "" {
					return "", fmt.Errorf(
						"mcp: no connected space's mirror holds %s; connected spaces are %s",
						id, strings.Join(connected, ", "))
				}
				if resolvedSpace == "" {
					resolvedSpace, resolvedID = found, id
					continue
				}
				if found != resolvedSpace {
					return "", fmt.Errorf(
						"mcp: batch spans multiple spaces: %s resolves to %q, %s resolves to %q — one call targets one space",
						resolvedID, resolvedSpace, id, found)
				}
			}
			return resolvedSpace, nil
		}
	}

	return primary.write, primary.submit, primary.newDeps, nil
}

// buildWriteDepsForSpace resolves ONE connected space's mirror (cloning/
// fetching it if needed), credential, and repo facts, then builds
// WriteDeps/SubmitDeps/NewDeps over it. engine is built once by
// buildWriteDeps' caller and shared across every space, rather than
// reloading the embedded schema corpus once per connected space.
func buildWriteDepsForSpace(ctx context.Context, cfg space.ProjectConfig, machine space.MachineConfig, p Paths, binaryVersion string, ref space.Ref, engine *validate.Engine) (WriteDeps, SubmitDeps, NewDeps, error) {
	mirrorDir := space.ResolveMirrorLocation(p.ProjectRoot, ref, machine)
	if err := space.CloneOrFetch(ctx, mirrorDir, ref.RepoURL, readMirrorCredential(ctx, ref.ID, machine)); err != nil {
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
	cred, err := space.ResolveWriteCredential(ctx, space.CredentialEnvVar(ref.ID), credRef)
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
	h := host.NewGitHubHost(http.DefaultClient, githubAPIBase())
	funnel := space.NewWriteFunnel(h, validator, binaryVersion)

	hostCfg := SubmitHostConfig{
		RemoteURL: ref.RepoURL, Repo: host.Repo{Owner: owner, Name: name},
		BaseBranch: defaultBaseBranch, Credential: cred,
		CommitAuthorName: cfg.System, CommitAuthorEmail: cfg.System + "@a2a.local",
	}
	write := WriteDeps{
		Funnel: funnel, MirrorDir: mirrorDir, RepoURL: ref.RepoURL, SpaceID: ref.ID, OwnSystem: cfg.System,
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

// ambiguousSpaceFunnel refuses a write instead of silently landing it in
// cfg.Spaces[0] (P7). It is WriteDeps.Funnel's sole consumer-facing seam
// (internal/mcp/eventdoc.go's WriteDeps.submit calls Funnel.Submit exactly
// once), which is what let P7's first pass close the defect for every legacy
// write tool at once without touching a single handler file.
//
// It is now the BACKSTOP, not the behaviour: every write family resolves its
// own target per call and gets that space's real funnel, so this one is
// reached only by a write that resolved nothing. Keeping it means a write
// tool added later without a resolve call fails closed — see the install
// site's own comment for the four shapes that do resolve.
type ambiguousSpaceFunnel struct {
	connected []string
}

func (f ambiguousSpaceFunnel) Submit(context.Context, space.SubmitRequest) (space.WriteResult, error) {
	return space.WriteResult{}, fmt.Errorf(
		"mcp: space is required when multiple spaces are connected; connected spaces are %s",
		strings.Join(f.connected, ", "))
}

// spaceIDs extracts each connected space's id, in cfg.Spaces' own order.
func spaceIDs(refs []space.Ref) []string {
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.ID
	}
	return ids
}

// mirrorHoldsArtifact reports whether mirrorDir contains a file named
// "<id>.md" anywhere below its root — this package's own copy of
// cmd/a2a/wire.go's mirrorHoldsArtifact/resolveTargetSpaceRef rule. ADR-001
// forbids internal/mcp importing internal/cli, and cmd/a2a is a main
// package neither side can import, so this walk is deliberately duplicated
// rather than shared.
//
// A contract id (XC-<slug>) never matches here: a contract's committed path
// is <slug>/provides/<name>/contract.md (space.Layout.ProvidesContract), not
// "<id>.md" — SpaceOfArtifacts refusing to resolve one is therefore by
// design; the a2a_contract family passes its own explicit "space" input
// instead of deriving one from ids.
func mirrorHoldsArtifact(mirrorDir, id string) bool {
	var found bool
	_ = filepath.WalkDir(mirrorDir, func(_ string, d os.DirEntry, err error) error {
		if found {
			return filepath.SkipAll
		}
		if err != nil {
			return nil //nolint:nilerr // reason: best-effort walk — an inaccessible entry just is not a match, and mirrorHoldsArtifact discards WalkDir's overall error too (same grant as cmd/a2a/wire.go's own copy)
		}
		// Skip the bare `.git` object store — it never holds artifact files,
		// and walking it grows with history (matches cmd/a2a's own walker
		// and internal/cache's).
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

// readMirrorCredential resolves the credential a MIRROR REFRESH should carry
// for spaceID, degrading to the empty credential when none resolves.
//
// It is deliberately the lenient sibling of buildWriteDeps' own
// space.ResolveCredential call, which returns its error: a write with no
// credential cannot work, while a read with no credential works against every
// public space and must keep doing so. The private case is what the
// credential buys, and its absence surfaces as git's own error rather than as
// a refusal to start the session.
func readMirrorCredential(ctx context.Context, spaceID string, machine space.MachineConfig) host.Credential {
	var ref space.CredentialReference
	if raw, ok := machine.Credentials[spaceID]; ok {
		parsed, err := space.ParseCredentialReference(raw)
		if err != nil {
			return host.Credential{}
		}
		ref = parsed
	}
	credential, err := space.ResolveCredential(ctx, space.CredentialEnvVar(spaceID), ref)
	if err != nil {
		return host.Credential{}
	}
	return credential
}
