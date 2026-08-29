// cmd_contract_verify_published.go — answers-that-hold-2026-08 P7 (spec
// 07-where-do-my-contracts-stand.md): `a2a contract verify-published`, one
// config-driven call reporting, per contract THIS system provides, whether
// the published bytes still match the local source — the aggregate form a
// consumer wrote 137 lines of bash to substitute for (fb-20260827-455fca).
//
// This file AGGREGATES P2's comparison; it never re-implements it. Every
// row's verdict comes from ContractInspectionOperations.VerifyContractExport
// — the SAME digest logic `contract verify-export` already runs — called
// once per contract this system provides, with the version resolved from
// the published descriptor (never asked of the caller, US-2/AC-2) and the
// local subject resolved from a caller-nameable per-contract override
// (US-4/AC-6), never from `.a2a/staging` (untracked, publisher-rewritten).
//
// TWO ENTRY POINTS, ONE AGGREGATOR. contractVerifyPublishedRun is the only
// place rows are built and rendered:
//
//   - (*ContractCommand).runVerifyPublished is what `a2a contract
//     verify-published` reaches through the existing single-space
//     ContractCommand switch (cmd_contract.go) — it reuses c.deps/c.inspection
//     verbatim (no new field on ContractCommand; this file's allowlist grant
//     there is "the switch and the roster ONLY"), so it covers exactly the
//     space that instance is already bound to.
//   - ContractVerifyPublishedCommand is a standalone, separately-constructed
//     command (own Go constructor, DI'd project/machine config paths —
//     internal/cli never reads os.Getwd/os.UserHomeDir itself, see
//     cmd_init.go's ensureMachineConfig doc comment) that resolves EVERY
//     connected space from `.a2a/config.yaml` (AC-7) and builds one
//     ContractInspectionOperations per space through an injected resolver.
//     This is the shape spec 07 §11's 2026-08-28 amendment names directly:
//     "the command's own Go constructor called directly" — AC-7's own tier.
//
// See this phase's Deviations report for the CLI wiring gap: today only the
// standalone constructor can see more than one space; wiring
// `a2a contract verify-published` itself to reach every connected space
// needs a small addition in cmd/a2a/wire.go (LEAD-OWNED, off-limits here).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// ContractVerifyPublishedRow is one provided contract's verify-published
// outcome. Status is never a fourth Verdict (spec 07 §11's own correction,
// tracking `no-silent-yes-2026-08` D9): matched/drifted/not-published-yet
// are row-local vocabulary, and "unmeasured" is validate.SeverityUnmeasured
// carried BY VALUE — the same string, never a second word for it.
type ContractVerifyPublishedRow struct {
	ID      string `json:"id"`
	SpaceID string `json:"space_id"`
	// Version is the version RESOLVED from the published descriptor
	// (US-2/AC-2) — empty exactly when Status is "not-published-yet".
	Version string `json:"version,omitempty"`
	Status  string `json:"status"`
	// Local is the project-relative subject this row was checked against —
	// empty when no override was given for this id (Status is then
	// "unmeasured", never a silent skip).
	Local string `json:"local,omitempty"`
	// Detail carries the reason behind an "unmeasured" row (no local
	// subject given, or the comparison itself could not run) — never set
	// for matched/drifted/not-published-yet.
	Detail string `json:"detail,omitempty"`
}

// ContractVerifyPublishedResult is the full aggregate report — the --json
// shape (AC-8). Total is the run's own DENOMINATOR (US-3/AC-3): printed
// under --json exactly as under the human path, so a run that examined
// zero contracts is visibly distinguishable from a clean pass in EITHER
// render mode. Field set for the render ledger P3 introduces (spec 07 §7 —
// P3 has not shipped; see this phase's Deviations report): system, total,
// and every ContractVerifyPublishedRow field above.
type ContractVerifyPublishedResult struct {
	System string                       `json:"system"`
	Total  int                          `json:"total"`
	Rows   []ContractVerifyPublishedRow `json:"rows"`
}

// contractVerifyPublishedSpaceContext is one connected space's already-
// resolved read surface: its own mirror directory and its own
// ContractInspectionOperations (P2's comparison, scoped to that space).
type contractVerifyPublishedSpaceContext struct {
	ref        space.Ref
	mirrorDir  string
	inspection ContractInspectionOperations
}

// ContractVerifyPublishedSpaceInspector builds the read-only inspection
// capability for exactly one connected space, given its already-resolved
// mirror directory. Production wiring (LEAD-OWNED, cmd/a2a) keys this the
// same way mcpContractP6Router.bySpace already does; test wiring returns a
// fake.
type ContractVerifyPublishedSpaceInspector func(ref space.Ref, mirrorDir string) (ContractInspectionOperations, error)

// contractVerifyPublishedResolveLocal is the per-contract local-subject
// resolver (US-4/AC-6): a caller-nameable project-relative path, never a
// default onto `.a2a/staging`. An override present but empty is treated as
// absent — defensive, since contractVerifyPublishedParseArgs already
// refuses an empty path at parse time.
func contractVerifyPublishedResolveLocal(overrides map[string]string, id string) (path string, ok bool) {
	path, ok = overrides[id]
	if path == "" {
		return "", false
	}
	return path, true
}

// contractVerifyPublishedParseArgs parses `[--json] [--local
// <XC-id>=<project-relative-path>]...` — no positional arguments (the
// invocation carries no version, US-2/AC-2, and no per-contract id: this
// verb aggregates every contract the system provides).
func contractVerifyPublishedParseArgs(args []string, stdio IO) (asJSON bool, overrides map[string]string, ok bool) {
	const usage = "usage: a2a contract verify-published [--json] [--local <XC-id>=<project-relative-path>]...\n"
	fs := flag.NewFlagSet("contract verify-published", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	asJSONFlag := fs.Bool("json", false, "emit JSON")
	var localFlags newStringList
	fs.Var(&localFlags, "local", "per-contract local subject override: <XC-id>=<project-relative-path> (repeatable)")
	positionals, perr := parseArgsAnyOrder(fs, args)
	if perr != nil {
		return false, nil, false
	}
	if len(positionals) != 0 {
		_, _ = fmt.Fprint(stdio.Stderr, usage)
		return false, nil, false
	}
	overrides = make(map[string]string, len(localFlags))
	for _, kv := range localFlags {
		id, path, found := strings.Cut(kv, "=")
		if !found || id == "" || path == "" {
			_, _ = fmt.Fprint(stdio.Stderr, usage)
			return false, nil, false
		}
		overrides[id] = path
	}
	return *asJSONFlag, overrides, true
}

// contractVerifyPublishedRefuse builds and writes a Refusal (rails: every
// refusal here goes through NewRefusal, never a raw err-to-stderr
// passthrough — this file's own refusal-ratchet budget is 0).
func contractVerifyPublishedRefuse(stdio IO, attempted, found, nextStep string) int {
	refusal, buildErr := NewRefusal(attempted, found, nextStep)
	if buildErr != nil {
		_, _ = fmt.Fprintln(stdio.Stderr, "contract verify-published: internal error building a refusal (empty next step) — this is a bug in a2a itself, file one")
		return 1
	}
	_, _ = fmt.Fprintln(stdio.Stderr, refusal)
	return 1
}

// contractVerifyPublishedRowsFor enumerates ownSystem's `provides/*` tree in
// sc's mirror and builds one row per contract descriptor found there. A
// contract descriptor with no recorded `version:` is "not-published-yet"
// (contractPublishedMajor's own established `probe.Version == ""` reading,
// reused here) — never passed to VerifyContractExport, which has no
// version to compare against. Every other row is a real, per-space
// aggregation: reuses contractReadDescriptor and contractCanonicalVersion
// (both file-private, same package, already the descriptor-reading and
// version-canonicalizing SSOT the rest of cmd_contract.go uses) and calls
// sc.inspection.VerifyContractExport — P2's comparison, never re-derived.
func contractVerifyPublishedRowsFor(ctx context.Context, ownSystem string, sc contractVerifyPublishedSpaceContext, overrides map[string]string) ([]ContractVerifyPublishedRow, error) {
	layout, err := space.NewLayout(ownSystem)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(sc.mirrorDir, ownSystem, "provides"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Nothing provided yet — a legitimate zero-row state (US-3), not
			// an error: the caller prints the denominator either way.
			return nil, nil
		}
		return nil, err
	}
	slugs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		descriptorPath := filepath.Join(sc.mirrorDir, layout.ProvidesContract(entry.Name()))
		if _, statErr := os.Stat(descriptorPath); statErr != nil {
			continue // no contract.md under this slug — not a provided contract
		}
		slugs = append(slugs, entry.Name())
	}
	sort.Strings(slugs)

	rows := make([]ContractVerifyPublishedRow, 0, len(slugs))
	for _, slug := range slugs {
		id, _, ok := space.ContractForPath(layout.ProvidesContract(slug))
		if !ok {
			continue
		}
		_, probe, _, _, derr := contractReadDescriptor(sc.mirrorDir, id)
		if derr != nil {
			return nil, fmt.Errorf("%s: %w", id, derr)
		}

		row := ContractVerifyPublishedRow{ID: id, SpaceID: sc.ref.ID}
		if probe.Version == "" {
			row.Status = "not-published-yet"
			rows = append(rows, row)
			continue
		}

		version := contractCanonicalVersion(probe.Version)
		row.Version = version

		local, hasLocal := contractVerifyPublishedResolveLocal(overrides, id)
		if !hasLocal {
			row.Status = string(validate.SeverityUnmeasured)
			row.Detail = "no local subject given for " + id + " — pass --local " + id + "=<project-relative-path>"
			rows = append(rows, row)
			continue
		}
		row.Local = local

		verify, verifyErr := sc.inspection.VerifyContractExport(ctx, ContractVerifyExportRequest{Local: local, Ref: id + "@" + version})
		if verifyErr != nil {
			row.Status = string(validate.SeverityUnmeasured)
			row.Detail = verifyErr.Error()
			rows = append(rows, row)
			continue
		}
		// verify.Outcome already carries the D9-mapped three-outcome
		// vocabulary (contract.ExportVerification, at cmd/a2a's own render
		// boundary) — passed through verbatim, never reclassified here.
		row.Status = verify.Outcome
		rows = append(rows, row)
	}
	return rows, nil
}

// contractVerifyPublishedRun is the shared aggregator + renderer both entry
// points call. A space whose mirror is absent, or whose HEAD cannot be
// resolved (never synced, or corrupt), REFUSES the WHOLE run (ACs 4-5) —
// naming `a2a sync` — because enumeration over an untrustworthy mirror
// cannot be trusted either. Otherwise the denominator is printed on EVERY
// run (US-3/AC-3), and the run exits 1 only when a row actually DRIFTED —
// "unmeasured" and "not-published-yet" are legitimate, non-failing states
// (D9: SeverityUnmeasured alone must never flip a result to failing), and a
// zero-row run WARNS rather than refusing (spec 07 T1's own asymmetry).
func contractVerifyPublishedRun(ctx context.Context, ownSystem string, spaces []contractVerifyPublishedSpaceContext, overrides map[string]string, stdio IO, asJSON bool) int {
	result := ContractVerifyPublishedResult{System: ownSystem}
	drifted := false

	for _, sc := range spaces {
		if _, statErr := os.Stat(sc.mirrorDir); statErr != nil {
			return contractVerifyPublishedRefuse(stdio,
				fmt.Sprintf("contract verify-published: read %s's synced mirror at %s", sc.ref.ID, sc.mirrorDir),
				statErr.Error(),
				fmt.Sprintf("run `a2a sync` to clone %s's mirror, then retry", sc.ref.ID),
			)
		}
		if _, headErr := space.ResolveContractPublicationCandidateCommit(ctx, sc.mirrorDir); headErr != nil {
			return contractVerifyPublishedRefuse(stdio,
				fmt.Sprintf("contract verify-published: resolve %s's mirror HEAD", sc.ref.ID),
				headErr.Error(),
				fmt.Sprintf("run `a2a sync` to refresh %s's mirror (it looks stale or was never synced), then retry", sc.ref.ID),
			)
		}
		rows, rowsErr := contractVerifyPublishedRowsFor(ctx, ownSystem, sc, overrides)
		if rowsErr != nil {
			return contractVerifyPublishedRefuse(stdio,
				fmt.Sprintf("contract verify-published: enumerate %s's provided contracts in %s", ownSystem, sc.ref.ID),
				rowsErr.Error(),
				fmt.Sprintf("run `a2a sync` to refresh %s's mirror, then retry", sc.ref.ID),
			)
		}
		for _, row := range rows {
			if row.Status == "drifted" {
				drifted = true
			}
			result.Rows = append(result.Rows, row)
		}
	}
	result.Total = len(result.Rows)

	if asJSON {
		if encodeErr := json.NewEncoder(stdio.Stdout).Encode(result); encodeErr != nil {
			return contractVerifyPublishedRefuse(stdio,
				"contract verify-published: encode the result as JSON",
				encodeErr.Error(),
				"this is likely a bug in a2a itself — file one",
			)
		}
		if drifted {
			return 1
		}
		return 0
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "%d contracts published for %s\n", result.Total, ownSystem)
	for _, row := range result.Rows {
		ref := row.ID
		if row.Version != "" {
			ref = row.ID + "@" + row.Version
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "%s [%s]: %s", ref, row.SpaceID, row.Status)
		if row.Detail != "" {
			_, _ = fmt.Fprintf(stdio.Stdout, " (%s)", row.Detail)
		}
		_, _ = fmt.Fprintln(stdio.Stdout)
	}
	if drifted {
		return 1
	}
	return 0
}

// runVerifyPublished is `a2a contract verify-published`'s entry point
// through the existing single-space ContractCommand switch. It covers
// exactly the ONE space c.deps/c.inspection are already bound to — see this
// file's own doc comment for why AC-7 (two connected spaces) is proven
// through ContractVerifyPublishedCommand's own constructor instead.
func (c *ContractCommand) runVerifyPublished(ctx context.Context, args []string, stdio IO) int {
	if c.inspection == nil {
		return contractServiceUnavailable(stdio, "verify-published")
	}
	asJSON, overrides, ok := contractVerifyPublishedParseArgs(args, stdio)
	if !ok {
		return 2
	}
	spaces := []contractVerifyPublishedSpaceContext{{
		ref:        space.Ref{ID: c.deps.spaceID},
		mirrorDir:  c.deps.mirrorDir,
		inspection: c.inspection,
	}}
	return contractVerifyPublishedRun(ctx, c.deps.ownSystem, spaces, overrides, stdio, asJSON)
}

// ContractVerifyPublishedCommand is the standalone, multi-space-capable
// form of `a2a contract verify-published` (AC-7): it loads
// `.a2a/config.yaml` itself, resolves every connected space's mirror, and
// builds one ContractInspectionOperations per space through the injected
// inspectorFor — never constructing a P6 core itself (that construction is
// cmd/a2a's own, LEAD-OWNED). Paths are DI'd, never read from
// os.Getwd/os.UserHomeDir here (rails: internal/cli never reads them
// itself — see cmd_init.go's ensureMachineConfig doc comment), matching
// ConnectCommand/DisconnectCommand's own constructor shape.
type ContractVerifyPublishedCommand struct {
	projectConfigPath string
	machineConfigPath string
	projectRoot       string

	loadProjectConfig func(string) (space.ProjectConfig, error)
	loadMachineConfig func(string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	inspectorFor      ContractVerifyPublishedSpaceInspector
}

// NewContractVerifyPublishedCommand constructs the standalone,
// multi-space-capable command. inspectorFor must not be nil (rails
// anti-pattern #10): production wiring (cmd/a2a, LEAD-OWNED) supplies the
// real per-space P6 core; tests supply a fake.
func NewContractVerifyPublishedCommand(projectConfigPath, machineConfigPath, projectRoot string, inspectorFor ContractVerifyPublishedSpaceInspector) *ContractVerifyPublishedCommand {
	return &ContractVerifyPublishedCommand{
		projectConfigPath: projectConfigPath,
		machineConfigPath: machineConfigPath,
		projectRoot:       projectRoot,
		loadProjectConfig: space.LoadProjectConfig,
		loadMachineConfig: space.LoadMachineConfig,
		resolveMirror:     space.ResolveMirrorLocation,
		inspectorFor:      inspectorFor,
	}
}

// Name implements cli.Command.
func (c *ContractVerifyPublishedCommand) Name() string { return "contract-verify-published" }

// Synopsis implements cli.Command.
func (c *ContractVerifyPublishedCommand) Synopsis() string {
	return "report whether every published contract this system provides still matches its code, across every connected space"
}

// Run implements cli.Command.
func (c *ContractVerifyPublishedCommand) Run(ctx context.Context, args []string, stdio IO) int {
	asJSON, overrides, ok := contractVerifyPublishedParseArgs(args, stdio)
	if !ok {
		return 2
	}

	cfg, cfgErr := c.loadProjectConfig(c.projectConfigPath)
	if cfgErr != nil {
		return contractVerifyPublishedRefuse(stdio,
			"contract verify-published: load this project's `.a2a/config.yaml`",
			cfgErr.Error(),
			"run `a2a init` to create a project config, then `a2a connect <repo-url>` to connect a space",
		)
	}
	if len(cfg.Spaces) == 0 {
		return contractVerifyPublishedRefuse(stdio,
			"contract verify-published: find at least one connected space in `.a2a/config.yaml`",
			"spaces: is empty",
			"run `a2a connect <repo-url>` to connect a space",
		)
	}

	// A missing machine config degrades to its zero value (ConnectCommand/
	// DisconnectCommand's own established convention) — ResolveMirrorLocation
	// falls back to the project-relative default mirror root either way.
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

	spaces := make([]contractVerifyPublishedSpaceContext, 0, len(cfg.Spaces))
	for _, ref := range cfg.Spaces {
		mirrorDir := c.resolveMirror(c.projectRoot, ref, machine)
		inspection, inspectErr := c.inspectorFor(ref, mirrorDir)
		if inspectErr != nil {
			return contractVerifyPublishedRefuse(stdio,
				fmt.Sprintf("contract verify-published: build %s's own inspection service", ref.ID),
				inspectErr.Error(),
				"confirm `.a2a/config.yaml` names a valid, reachable space, then retry",
			)
		}
		spaces = append(spaces, contractVerifyPublishedSpaceContext{ref: ref, mirrorDir: mirrorDir, inspection: inspection})
	}

	return contractVerifyPublishedRun(ctx, cfg.System, spaces, overrides, stdio, asJSON)
}

var _ Command = (*ContractVerifyPublishedCommand)(nil)
