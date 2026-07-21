// OP-206 `a2a sync` (spec 06 T1). This file's only package-level symbols
// are SyncCommand + NewSyncCommand — no shared helper, no package var,
// per this phase's plan Placement decision (avoids collision with
// P7/P8/P9's parallel verb files in this package).
package cli

import (
	"context"
	"fmt"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// SyncCommand implements `a2a sync`: fetches every connected space's
// mirror clone and calls the future internal/cache seam ("refresh local
// cache", §7.2 OP-206) — cache population is a documented no-op this
// phase (internal/cache is P7-owned, blocked_by: [P6], and does not exist
// at this phase's build time; spec 06 Open Q-A).
//
// Re-running internal/fold over every artifact in a space (the other
// half of §7.2's "fetch all connected spaces, refresh local cache/fold")
// needs both a place to persist the recomputed per-artifact state
// (internal/cache, P7) and an enumerate-every-artifact-in-a-space
// primitive that neither internal/space nor internal/fold exposes yet —
// this phase's `sync` therefore covers the mirror-refresh half only; see
// this phase's Deviations report.
type SyncCommand struct {
	projectConfigPath string
	machineConfigPath string
	projectRoot       string
	pending           PendingMarker

	loadProjectConfig func(path string) (space.ProjectConfig, error)
	loadMachineConfig func(path string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	cloneOrFetch      func(ctx context.Context, dir, repoURL string) error
}

// NewSyncCommand constructs the sync command. pending must not be nil
// (rails anti-pattern #10 — inject NewNoopPendingMarker() until P7
// lands). projectConfigPath/machineConfigPath/projectRoot mirror
// DoctorCommand's own constructor DI convention.
func NewSyncCommand(projectConfigPath, machineConfigPath, projectRoot string, pending PendingMarker) *SyncCommand {
	return &SyncCommand{
		projectConfigPath: projectConfigPath,
		machineConfigPath: machineConfigPath,
		projectRoot:       projectRoot,
		pending:           pending,
		loadProjectConfig: space.LoadProjectConfig,
		loadMachineConfig: space.LoadMachineConfig,
		resolveMirror:     space.ResolveMirrorLocation,
		cloneOrFetch:      space.CloneOrFetch,
	}
}

// Name implements cli.Command.
func (c *SyncCommand) Name() string { return "sync" }

// Synopsis implements cli.Command.
func (c *SyncCommand) Synopsis() string {
	return "fetch all connected spaces' mirrors and refresh the local cache"
}

// Run implements cli.Command. Exit codes: 2 = usage (unexpected args); 1
// = one or more spaces failed to fetch/refresh; 0 = every connected space
// refreshed (including the trivial zero-connected-spaces case — sync is
// inherently idempotent, §7.2 tail: "refresh has no 'already done' state
// to detect").
func (c *SyncCommand) Run(ctx context.Context, args []string, stdio IO) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a sync")
		return 2
	}

	// An absent project config (never ran `a2a init`/`a2a connect` yet) is
	// not a sync failure — there is simply nothing connected to refresh
	// (§7.2 tail: sync is inherently idempotent, no "already done" state
	// to detect, and the same applies to "nothing configured yet").
	cfg, _ := c.loadProjectConfig(c.projectConfigPath)
	// The machine config is optional (credential references, mirror-root
	// override, personal defaults, §7.4) — its absence is not a failure;
	// callers get the zero value (ResolveMirrorLocation's own project-
	// relative fallback then applies), same tolerant convention
	// ConnectCommand/DisconnectCommand already use for this file.
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

	if len(cfg.Spaces) == 0 {
		_, _ = fmt.Fprintln(stdio.Stdout, "sync: no connected spaces")
		return 0
	}

	allOK := true
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		if err := c.cloneOrFetch(ctx, dir, ref.RepoURL); err != nil {
			allOK = false
			_, _ = fmt.Fprintf(stdio.Stderr, "sync: %s: %v\n", ref.ID, err)
			continue
		}
		// Cache-refresh seam call (this phase's convention: spaceID set,
		// artifactID empty, a zero WriteResult — see PendingMarker's doc
		// comment in adapters.go).
		if err := c.pending.MarkPending(ctx, ref.ID, "", space.WriteResult{}); err != nil {
			allOK = false
			_, _ = fmt.Fprintf(stdio.Stderr, "sync: %s: cache-refresh seam failed: %v\n", ref.ID, err)
			continue
		}
		_, _ = fmt.Fprintf(stdio.Stdout, "sync: %s: refreshed\n", ref.ID)
	}

	if !allOK {
		return 1
	}
	return 0
}

var _ Command = (*SyncCommand)(nil)
