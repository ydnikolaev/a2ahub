package mcp

// a2a_adapt (P13, answers-that-hold-2026-08 spec 13's MCP twin of
// internal/cli's cmd_adapt.go): the READ half of `a2a adapt`, exposed as an
// action-free, standalone tool (the a2a_whatsnew precedent, tools_whatsnew.go
// + its registration in tools.go's registerSpaceFree) — surfacing
// internal/notes.Pending's filtered, group-tagged obligation projection as
// StructuredContent.
//
// `a2a whatsnew` (this tool's own sibling) already answers "what changed?"
// on this surface; hiding what the reader still OWES while exposing what
// changed is exactly the "silent yes" defect answers-that-hold-2026-08 exists
// to remove, so this is not an `mcpExcludedVerbs` row (cmd/a2a/
// mcp_parity_test.go's own doc comment names the host-machine acts that ARE
// excluded — adapt is not one).
//
// The two facts notes.Pending needs — the running binary's own version and
// the repository's `.a2a/config.yaml` path — are INJECTED at wire time
// (this package's own wire.go, mirroring how newWhatsnewHandler's two
// loaders are injected), never accepted as call arguments:
// tools_work_test.go's own TestWorkToolSchemaIsClosedAndHasAllEnumsWithoutRoot
// forbids a root/cwd/path selector on ANY tool's input schema, and that rule
// holds here too — a caller that could name its own config path could read
// an arbitrary file on the host.
//
// `--done` is DELIBERATELY NOT exposed: it writes .a2a/config.yaml and runs
// this repository's own `detect:` shell commands (internal/notes/adapt.go's
// DetectRunner) — a repository-state write plus arbitrary shell execution,
// neither of which belongs on a read tool. An agent that has done the work
// still records it through `a2a adapt --done` on the host, same as `update`/
// `skill`/`html` remain CLI-only host-machine acts.
//
// Registered UNCONDITIONALLY by registerSpaceFree (tools.go), exactly beside
// a2a_read/a2a_whatsnew, via the AdaptDeps this file defines below — so it is
// reachable on every wiring path (no space, degraded write, healthy
// connected) AND from a bare mcp.BuildRegistry(...) call, which is what
// cmd/a2a/catalog.go's catalogMCPRows() uses to generate the skill-drift-
// gated "## MCP tools" catalogue. A caller with no real binaryVersion/
// projectConfigPath to inject (BuildRegistry's own many test callers) gets
// AdaptDeps{}'s zero value: notes.Pending then refuses
// ErrBinaryVersionUnusable for the empty binary version, exactly the
// refusal a real `dev` build gets — never a silently empty pending list —
// so the degraded registration is honest rather than a pretend success.
//
// Ordering deviation from the CLI, and why: cmd_adapt.go's own adaptSortItems
// sorts (Group, then within-group by cmd_whatsnew.go's whatsnewImpactOrder) —
// but internal/mcp/wire.go's own doc comment states this package "does NOT
// import cmd/a2a or internal/cli" (ADR-001), and whatsnewImpactOrder lives in
// internal/cli. This tool therefore sorts by Group ONLY (notes.Pending's own
// field, already in-package) and leaves each group's own encounter order
// (ascending release order, then each release's own Changes order —
// notes.Projection.Items' own documented order) as the within-group
// tie-break, rather than duplicating cmd_whatsnew.go's impact-rank table
// across the ADR-001 boundary. This mirrors an EXISTING precedent, not a new
// one: newWhatsnewHandler (tools_whatsnew.go) already returns its selected
// releases without applying cmd_whatsnew.go's own per-change impact sort
// either.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/ydnikolaev/a2ahub/internal/notes"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/releasenotes"
)

// AdaptInput is a2a_adapt's structured input — empty. Every value Pending
// needs (binary version, project config path) is injected at wire time (see
// this file's own doc comment), never accepted as a call argument; decoding
// through decodeStrict still refuses an unexpected field rather than
// silently ignoring it (rawSchema's own additionalProperties:false doc
// comment explains why a client-side-only refusal is not enough).
type AdaptInput struct{}

// AdaptPendingItem is one obligation in AdaptOutput.Pending — mirrors
// cmd_adapt.go's own adaptJSONItem (Version, Group, the full Change, which
// already carries Action.Scope).
type AdaptPendingItem struct {
	Version string       `json:"version"`
	Group   int          `json:"group"`
	Change  notes.Change `json:"change"`
}

// AdaptOutput is a2a_adapt's StructuredContent shape — mirrors cmd_adapt.go's
// own adaptJSON, including ObligationsRemain (spec 13 §6: "a caller need not
// infer it from Count") even though the exit-code half of that CLI rationale
// does not transfer to MCP — the caller-convenience half does.
type AdaptOutput struct {
	Baseline          string             `json:"baseline,omitempty"`
	BinaryVersion     string             `json:"binary_version"`
	Releases          int                `json:"releases"`
	StartedFromOldest bool               `json:"started_from_oldest,omitempty"`
	Oldest            string             `json:"oldest,omitempty"`
	Count             int                `json:"count"`
	ObligationsRemain bool               `json:"obligations_remain"`
	Pending           []AdaptPendingItem `json:"pending"`
}

// newAdaptHandler builds a2a_adapt's handler. load/loadCurrentIssues mirror
// newWhatsnewHandler's own two injected loaders; loadProjectConfig mirrors
// AdaptCommand's own field (NewAdaptCommand's default is space.
// LoadProjectConfig, tolerant of a missing .a2a/config.yaml — "never
// adapted" is not a fatal error, cmd_adapt.go's own Run comment). binaryVersion
// and projectConfigPath are this call's own injected facts (see this file's
// doc comment for why they are never input-schema fields).
func newAdaptHandler(
	load func() ([]notes.ReleaseNotes, error),
	loadCurrentIssues func() ([]notes.Change, error),
	loadProjectConfig func(path string) (space.ProjectConfig, error),
	binaryVersion string,
	projectConfigPath string,
) HandlerFunc {
	return func(_ context.Context, args json.RawMessage) (any, string, error) {
		var in AdaptInput
		if err := decodeStrict(args, &in, "a2a_adapt", 0); err != nil {
			return nil, "", err
		}

		all, err := load()
		if err != nil {
			return nil, "", fmt.Errorf("a2a_adapt: load the embedded release-note corpus: %w", err)
		}
		currentIssues, err := loadCurrentIssues()
		if err != nil {
			return nil, "", fmt.Errorf("a2a_adapt: load the embedded known-issues corpus: %w", err)
		}
		// Best-effort, same tolerant convention AdaptCommand.Run uses: no
		// `a2a init` yet is "never adapted", not a fatal error.
		cfg, _ := loadProjectConfig(projectConfigPath)

		proj, err := notes.Pending(all, currentIssues, cfg.AdaptedThrough, binaryVersion)
		if err != nil {
			switch {
			case errors.Is(err, notes.ErrBaselineAheadOfBinary):
				return nil, "", fmt.Errorf(
					"a2a_adapt: adapted_through (v%s) is newer than the running binary (v%s) — refusing to walk backwards",
					cfg.AdaptedThrough, binaryVersion)
			case errors.Is(err, notes.ErrBinaryVersionUnusable):
				return nil, "", fmt.Errorf(
					"a2a_adapt: this binary's own version (%q) is not a comparable release version, so the obligation range from adapted_through (%q) cannot be computed — run a real release build, not a dev build",
					binaryVersion, cfg.AdaptedThrough)
			default:
				return nil, "", fmt.Errorf("a2a_adapt: compute the pending-obligation range from adapted_through and this binary's version: %w", err)
			}
		}

		items := append([]notes.PendingItem(nil), proj.Items...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].Group < items[j].Group })

		out := AdaptOutput{
			Baseline:          proj.Baseline,
			BinaryVersion:     proj.BinaryVersion,
			Releases:          proj.Releases,
			StartedFromOldest: proj.StartedFromOldest,
			Oldest:            proj.Oldest,
			Count:             len(items),
			ObligationsRemain: len(items) > 0,
			Pending:           make([]AdaptPendingItem, 0, len(items)),
		}
		for _, item := range items {
			out.Pending = append(out.Pending, AdaptPendingItem{Version: item.Version, Group: int(item.Group), Change: item.Change})
		}
		return out, "", nil
	}
}

// AdaptDeps is a2a_adapt's registration-time dependency set: the two facts
// notes.Pending needs (BinaryVersion, ProjectConfigPath — see this file's own
// doc comment for why neither is a call argument) plus the three loaders
// NewAdaptCommand's own field defaults use, injectable so a test can drive a
// fixed corpus/config without touching the embedded FS or the filesystem.
//
// The zero value is a legitimate, INTENTIONALLY degraded registration, not an
// oversight: registerAdaptTool's own withDefaults fills the three loaders
// unconditionally, but BinaryVersion/ProjectConfigPath are left as-is, so a
// caller with nothing real to inject (every BuildRegistry(...) call site
// that predates this tool) still registers a working tool NAME — visible to
// the catalogue and the parity surface — whose calls honestly refuse
// (notes.ErrBinaryVersionUnusable) rather than silently answering an empty
// pending list.
type AdaptDeps struct {
	BinaryVersion     string
	ProjectConfigPath string
	Load              func() ([]notes.ReleaseNotes, error)
	LoadCurrentIssues func() ([]notes.Change, error)
	LoadProjectConfig func(path string) (space.ProjectConfig, error)
}

// withDefaults fills d's three loaders with their production implementations
// when unset (NewAdaptCommand's own defaults: notes.Load/
// notes.LoadCurrentKnownIssues over releasenotes.FS, space.LoadProjectConfig)
// — mirrors registerSpaceFree's own a2a_whatsnew registration, which
// constructs the identical two loaders inline rather than through a struct;
// this one is a struct because AdaptDeps is also BuildRegistryWithOperations'
// own new parameter, threaded from wire.go with real values, not just a
// registration-site literal.
func (d AdaptDeps) withDefaults() AdaptDeps {
	if d.Load == nil {
		d.Load = func() ([]notes.ReleaseNotes, error) { return notes.Load(releasenotes.FS) }
	}
	if d.LoadCurrentIssues == nil {
		d.LoadCurrentIssues = func() ([]notes.Change, error) { return notes.LoadCurrentKnownIssues(releasenotes.FS) }
	}
	if d.LoadProjectConfig == nil {
		d.LoadProjectConfig = space.LoadProjectConfig
	}
	return d
}

// registerAdaptTool registers a2a_adapt on r. Called unconditionally from
// registerSpaceFree (tools.go) — see this file's own doc comment for why an
// UNCONDITIONAL registration, with deps' zero value an honest degraded case,
// is what makes the tool visible on every wiring path and to
// cmd/a2a/catalog.go's bare mcp.BuildRegistry(...) catalogue generation.
func registerAdaptTool(r *Registry, deps AdaptDeps) {
	deps = deps.withDefaults()
	r.Register(ToolSpec{
		Name:        "a2a_adapt",
		Description: "read-only: this repository's pending-obligation projection since its adapted_through baseline (a2a adapt's READ half — --done, which WRITES .a2a/config.yaml and runs detect: commands, is not exposed here)",
		InputSchema: rawSchema(map[string]propSpec{}),
		Handler: newAdaptHandler(
			deps.Load, deps.LoadCurrentIssues, deps.LoadProjectConfig,
			deps.BinaryVersion, deps.ProjectConfigPath,
		),
	})
}
