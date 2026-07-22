// OP-217 `a2a update` (spec 19 T1). This file's only package-level symbols
// are UpdateCommand + NewUpdateCommand plus its own uniquely-named,
// file-private helpers (update* prefix / defaultUpdate* prefix) — no shared
// helper, no package var, per this package's established plan Placement
// convention (avoids collision with the other parallel verb files in this
// same package, e.g. cmd_doctor.go, cmd_sync.go).
//
// This file ORCHESTRATES the shipped internal/release primitives; it never
// hand-sequences Download/Verify/SelfCheckVersion/Swap — release.Apply is
// the package's only safe entry point for that pipeline (an early audit
// closed exactly the exec-before-verify gap that hand-sequencing would
// reopen).
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// defaultUpdateRepo aliases the release-package SSOT (release.DefaultUpdateRepo)
// so the verb and the notice-checker wiring resolve the same product repo.
//
// (superseded doc) the compiled-in "<owner>/<name>" of the product repo
// (spec 19 T3), overridable via machine config `defaults.update_repo`
// (the publish-prep public-repo transition is a one-line default flip).
const defaultUpdateRepo = release.DefaultUpdateRepo

// UpdateCommand implements `a2a update` (OP-217): resolve -> verify -> swap,
// fail-closed at every step, plus `--check`/`--json` report-only modes.
type UpdateCommand struct {
	binaryVersion     string
	projectConfigPath string
	machineConfigPath string
	projectRoot       string
	updateRepo        string

	// The following are real-implementation-backed seams (rails DI, same
	// convention as DoctorCommand/SyncCommand): NewUpdateCommand defaults
	// every one of them to the real internal/release/internal/space/stdlib
	// operation; tests override individual fields to drive the full
	// resolve/verify/swap pipeline without network, exec, or a real swap of
	// the test binary.
	source            func(repo string) release.Source
	cachePath         func() (string, error)
	resolveExec       func() (string, error)
	runner            release.Runner
	httpClient        *http.Client
	isTTY             func() bool
	confirm           func(prompt string, stdio IO) bool
	loadProjectConfig func(path string) (space.ProjectConfig, error)
	loadMachineConfig func(path string) (space.MachineConfig, error)
	resolveMirror     func(projectRoot string, ref space.Ref, machine space.MachineConfig) string
	readFile          func(path string) ([]byte, error)
	verifier          func(repo string) release.Verifier
}

// NewUpdateCommand constructs the update command. binaryVersion is this
// build's own version stamp (injected rather than read from a build var so
// tests control it, same convention as NewDoctorCommand).
// projectConfigPath/machineConfigPath/projectRoot mirror the rest of this
// package's DI convention (`.a2a/config.yaml`, `~/.config/a2a/config.yaml`,
// and the project root used to resolve each connected space's mirror
// directory when a space's config entry has no absolute mirror location).
func NewUpdateCommand(binaryVersion, projectConfigPath, machineConfigPath, projectRoot string) *UpdateCommand {
	return &UpdateCommand{
		binaryVersion:     binaryVersion,
		projectConfigPath: projectConfigPath,
		machineConfigPath: machineConfigPath,
		projectRoot:       projectRoot,
		updateRepo:        defaultUpdateRepo,
		source: func(repo string) release.Source {
			return release.NewGitHubSource(http.DefaultClient, "", repo)
		},
		cachePath:         release.CachePath,
		resolveExec:       defaultUpdateResolveExec,
		runner:            nil, // release.Apply defaults to release.DefaultRunner
		httpClient:        http.DefaultClient,
		isTTY:             defaultUpdateIsTTY,
		confirm:           defaultUpdateConfirm,
		loadProjectConfig: space.LoadProjectConfig,
		loadMachineConfig: space.LoadMachineConfig,
		resolveMirror:     space.ResolveMirrorLocation,
		readFile:          os.ReadFile,
		// Real keyless-cosign verification (checksum-first, then the asset's
		// .cosign.bundle against this repo's release-workflow OIDC identity).
		// repo is the resolved update_repo, so a repo rename flows through.
		verifier: func(repo string) release.Verifier { return release.KeylessVerifier(repo) },
	}
}

// Name implements cli.Command.
func (c *UpdateCommand) Name() string { return "update" }

// Synopsis implements cli.Command.
func (c *UpdateCommand) Synopsis() string {
	return "resolve, verify, and atomically swap to the latest release (OP-217)"
}

// updateJSON is the `--json` machine-readable shape (spec 19 T1 flag
// table): {current, latest, update_available, floor, floor_space,
// required}.
type updateJSON struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Floor           string `json:"floor"`
	FloorSpace      string `json:"floor_space"`
	Required        bool   `json:"required"`
}

// Run implements cli.Command. Exit codes (spec 19 T1): 2 = usage error; 1 =
// resolve/floor/swap error; 0 = up to date / cancelled / successful update;
// 10 = `--check` reports an update is available (severity-code idiom, §7.5).
func (c *UpdateCommand) Run(ctx context.Context, args []string, stdio IO) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stdio.Stderr)
	checkFlag := fs.Bool("check", false, "report only: never downloads or swaps")
	jsonFlag := fs.Bool("json", false, "machine-readable output")
	yesFlag := fs.Bool("yes", false, "skip the interactive confirmation")
	allowUnsignedFlag := fs.Bool("allow-unsigned", false, "proceed when NO signature bundle exists for the asset (never overrides a FAILED signature or a checksum mismatch)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stdio.Stderr, "usage: a2a update [--check] [--json] [--yes] [--allow-unsigned]")
		return 2
	}

	// Project/machine config are best-effort, same tolerant convention
	// SyncCommand uses: no `a2a init`/`a2a connect` yet, or no machine
	// config at all, is not a fatal error — it just means no floor
	// constraint and the compiled-in update repo.
	cfg, _ := c.loadProjectConfig(c.projectConfigPath)
	machine, _ := c.loadMachineConfig(c.machineConfigPath)

	repo := c.updateRepo
	if v, ok := machine.Defaults["update_repo"]; ok && v != "" {
		repo = v
	}

	floor, floorSpace := c.computeFloor(cfg, machine)

	src := c.source(repo)
	latestRel, err := src.Latest(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "update: %v\n", err)
		return 1
	}

	dec, err := release.Resolve(c.binaryVersion, latestRel.Version, latestRel, floor, floorSpace)
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "update: %v\n", err)
		return 1
	}

	// Best-effort cache refresh (T1 step 5 / T3(c)): every live invocation
	// (--check and full) refreshes the cache so the notice clears
	// everywhere. A cache-path or write failure is never fatal to the verb
	// itself.
	if cp, cpErr := c.cachePath(); cpErr == nil {
		_ = release.WriteCheck(cp, release.CheckState{
			CheckedAt: time.Now(),
			Latest:    latestRel.Version,
			Source:    src.Name(),
		})
	}

	// update_available / required come from the shared release.Info SSOT (not
	// re-derived here) so this object agrees value-for-value with the cache
	// UpdateNotice that inbox --json / MCP a2a_read render.
	info := release.Info(dec.Current, dec.Latest, dec.Floor, dec.FloorSpace)

	if *jsonFlag {
		enc := json.NewEncoder(stdio.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(updateJSON{
			Current:         info.Current,
			Latest:          info.Latest,
			UpdateAvailable: info.UpdateAvailable,
			Floor:           info.Floor,
			FloorSpace:      info.FloorSpace,
			Required:        info.Required,
		}); err != nil {
			_, _ = fmt.Fprintf(stdio.Stderr, "update: cannot encode JSON output: %v\n", err)
			return 1
		}
	}

	if *checkFlag {
		if !*jsonFlag {
			if dec.UpToDate {
				_, _ = fmt.Fprintf(stdio.Stdout, "a2a: up to date (v%s)\n", dec.Current)
			} else {
				_, _ = fmt.Fprintf(stdio.Stdout, "a2a: update available v%s -> v%s — run a2a update\n", dec.Current, dec.Latest)
			}
		}
		if dec.UpToDate {
			return 0
		}
		return 10
	}

	// Full update path (spec 19 T1 steps 2-5).
	if dec.UpToDate {
		_, _ = fmt.Fprintf(stdio.Stdout, "a2a: already up to date (v%s)\n", dec.Current)
		return 0
	}
	if dec.BelowFloor {
		_, _ = fmt.Fprintf(stdio.Stderr, "update: latest v%s is below floor v%s pinned by %s\n", dec.Latest, dec.Floor, dec.FloorSpace)
		return 1
	}

	if !*yesFlag {
		if !c.isTTY() {
			_, _ = fmt.Fprintln(stdio.Stderr, "refusing to update without --yes (non-interactive)")
			return 2
		}
		prompt := fmt.Sprintf(
			"a2a update: v%s -> v%s (%s)\nasset: a2a-<os>-<arch> (checksum + keyless-cosign signature verified after download)\nproceed? [y/N] ",
			dec.Current, dec.Latest, latestRel.Commit,
		)
		if !c.confirm(prompt, stdio) {
			_, _ = fmt.Fprintln(stdio.Stdout, "update: cancelled")
			return 0
		}
	}

	execPath, err := c.resolveExec()
	if err != nil {
		_, _ = fmt.Fprintf(stdio.Stderr, "update: %v\n", err)
		return 1
	}

	res, err := release.Apply(ctx, c.binaryVersion, release.ApplyOptions{
		Client:        c.httpClient,
		Token:         release.ResolveToken(),
		Target:        latestRel,
		ExecPath:      execPath,
		AllowUnsigned: *allowUnsignedFlag,
		Verifier:      c.verifier(repo),
		Run:           c.runner,
	})
	if err != nil {
		switch {
		case errors.Is(err, release.ErrSignatureUnverified):
			_, _ = fmt.Fprintf(stdio.Stderr, "update: %v (no cosign bundle for this asset — pass --allow-unsigned to proceed)\n", err)
		case errors.Is(err, release.ErrSignatureInvalid):
			_, _ = fmt.Fprintf(stdio.Stderr, "update: %v — refusing; a present-but-invalid signature is never overridable\n", err)
		default:
			_, _ = fmt.Fprintf(stdio.Stderr, "update: %v\n", err)
		}
		return 1
	}

	_, _ = fmt.Fprintf(stdio.Stdout, "a2a: updated v%s -> v%s (%s)\n", res.FromVersion, res.ToVersion, res.Commit)
	return 0
}

// computeFloor computes the T1 step-1 floor: the MAX min_binary_version
// across every connected space's manifest (mirror read, same source
// doctorCheckVersions uses), and the id of the space that pins it. A space
// with no configured entry, an unreadable mirror, an unparseable manifest,
// or an empty min_binary_version is skipped (best-effort) rather than
// failing the whole computation — the floor is advisory except for
// BelowFloor, per this phase's brief. No spaces, or nothing usable, returns
// ("", "") — no floor constraint.
func (c *UpdateCommand) computeFloor(cfg space.ProjectConfig, machine space.MachineConfig) (floor, floorSpace string) {
	for _, ref := range cfg.Spaces {
		dir := c.resolveMirror(c.projectRoot, ref, machine)
		raw, err := c.readFile(filepath.Join(dir, "space.yaml"))
		if err != nil {
			continue
		}
		manifest, err := space.ParseManifest(raw)
		if err != nil || manifest.MinBinaryVersion == "" {
			continue
		}
		if floor == "" {
			floor, floorSpace = manifest.MinBinaryVersion, ref.ID
			continue
		}
		older, err := version.OlderThan(floor, manifest.MinBinaryVersion)
		if err != nil {
			continue
		}
		if older {
			floor, floorSpace = manifest.MinBinaryVersion, ref.ID
		}
	}
	return floor, floorSpace
}

// defaultUpdateResolveExec resolves the running binary's real path (the
// Swap target, spec 19 T1 step 2): os.Executable then filepath.EvalSymlinks
// so a symlinked install (e.g. a Homebrew shim) resolves to the real file
// Swap must rename over.
func defaultUpdateResolveExec() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// defaultUpdateIsTTY reports whether stdout is a character device (a real
// terminal, not a pipe/redirect) — the consent gate's TTY check (spec 19
// T1 `--yes` row).
func defaultUpdateIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// defaultUpdateConfirm prints prompt to stderr and reads one line from
// stdio.Stdin, returning true for "y"/"yes" (case-insensitive), false for
// anything else (including read errors/EOF — a safe default, D-021: no
// confirmation read as no consent).
func defaultUpdateConfirm(prompt string, stdio IO) bool {
	_, _ = fmt.Fprint(stdio.Stderr, prompt)
	line, _ := bufio.NewReader(stdio.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

var _ Command = (*UpdateCommand)(nil)
