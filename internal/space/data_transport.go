package space

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/datatransport"
)

// spaceGitDriver is the space-git datatransport.Driver spec 05a AC-8 asks
// for (docs/features/archive/agent-ops-2026-07/specs/05a-contract-data-exchange-loop.md
// §4 AC-8, the 2026-08-13 amendments), closing the criterion's Go half end
// to end: DeliverDataPackage (this file's own sibling) calls Put for the
// payload+manifest half of one delivery commit, and ResolveDataPackage
// (data_resolve.go) is reused, never reimplemented, for Get.
//
// The import of internal/datatransport is GRANTED, by ADR-015 in
// docs/decisions.md — written in the same commit as this file after the
// phase audit asked for it by name. ADR-001's own Consequences say crossing
// these boundaries takes a new ADR rather than a widened cell, so that is
// what it got. The grant is narrow: internal/space may import the SEAM, and
// still may not import internal/datapackage.
type spaceGitDriver struct {
	// mirrorDir is the local git checkout Get reads from — the exact
	// argument ResolveDataPackage already takes. Put needs none: it is
	// pure and performs no I/O of its own (see its own doc comment), so a
	// caller that only ever calls Put (DeliverDataPackage does) may
	// construct this driver with mirrorDir empty.
	mirrorDir string
}

// newSpaceGitDriver returns the space-git Driver reading from mirrorDir.
// registerSpaceGit is the registry entry AC-8's own words promise. §9.3 says
// a later driver is "a registry entry plus conformance tests, with no schema,
// lifecycle or command change" — and until this existed the registry had NO
// production caller at all: DeliverDataPackage constructed the space-git
// driver directly, so adding a second one still meant editing the delivery
// path. Found by this phase's own audit, which was right to call the promise
// overstated.
//
// It is a sync.OnceValue and not an `init`, deliberately: `.golangci.yml`
// bans init functions (gochecknoinits), and the ban is right — implicit
// ordering with invisible side effects is exactly what a registry populated
// at import time would be. Lazy registration keeps the same one-line
// "a driver registers itself" property while the error stays returnable.
var registerSpaceGit = sync.OnceValue(func() error {
	return datatransport.Default.Register(newSpaceGitDriver(""))
})

func newSpaceGitDriver(mirrorDir string) *spaceGitDriver {
	return &spaceGitDriver{mirrorDir: mirrorDir}
}

var _ datatransport.Driver = (*spaceGitDriver)(nil)

// spaceGitDriverName mirrors datapackage.TransportDriverSpaceGit
// ("space-git") without importing internal/datapackage — the same
// duplicate-over-import idiom dataPackagePrefix (data_resolve.go) already
// uses for the identical ADR-001 reason. Unlike ErrUnsafeLocator (checked by
// errors.Is, so it must be the SAME value — hence the re-export in
// internal/datatransport instead), Name()'s return is compared by ordinary
// string equality, so duplicating the literal is sufficient here.
const spaceGitDriverName = "space-git"

// Name is part of the datatransport.Driver interface.
func (d *spaceGitDriver) Name() string { return spaceGitDriverName }

// Locate is part of the datatransport.Driver interface. It is exactly
// dataPackageDir(system, packageID) — spec 05a §T2.1's own words for the
// manifest's `locator` field, "the package root relative to the space."
func (d *spaceGitDriver) Locate(system, packageID string) (string, error) {
	if system == "" || packageID == "" {
		return "", fmt.Errorf("space-git: Locate: system and packageID are required")
	}
	return dataPackageDir(system, packageID), nil
}

// AcceptsLocator is part of the datatransport.Driver interface.
//
// DEVIATION from the brief's literal text ("the same clean-relative-path
// rule the file already uses (isCleanRelativePath)"), verified empirically
// before shipping: isCleanRelativePath (funnel.go) accepts
// "user@host:22/axon" — no ".." segment, not absolute, already in Clean
// form — while spec 05a §T2.4 requires a credential-shaped locator refused.
// artifact.CleanRelativePath (internal/artifact/paths.go) is the predicate
// actually built for this exact rule — its own doc comment names "a Windows
// drive letter (e.g. C:foo)" and is why nulldriver_test.go's own nullDriver
// uses it for the identical purpose — and internal/space may import
// internal/artifact (ADR-001, already imported by this package's own
// data_resolve.go). Using it here costs nothing DeliverDataPackage depends
// on: every locator DeliverDataPackage ever passes is internally derived
// from Locate(req.System, req.PackageID), never user-supplied at this
// layer, so both predicates already agree on every locator this driver is
// used against in production; they diverge only on the adversarial vectors
// RunConformance is written to catch.
func (d *spaceGitDriver) AcceptsLocator(locator string) bool {
	return artifact.CleanRelativePath(locator)
}

// Put is part of the datatransport.Driver interface. It is pure — no git,
// no network, no filesystem — because AC-3 requires payload, manifest,
// handoff and event to land in exactly ONE commit, and space-git's own
// write mechanism (DeliverDataPackage, this package's sibling file) is what
// performs that commit; a Driver that wrote here would be a second write
// path (§0's own "not a second write mechanism"). It validates locator and
// every entry path, then returns the exact space-relative path→bytes map
// DeliverDataPackage's own FileWrite assembly needs: every key of files
// joined onto locator, unchanged otherwise.
func (d *spaceGitDriver) Put(ctx context.Context, locator string, files map[string][]byte) (map[string][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !d.AcceptsLocator(locator) {
		return nil, fmt.Errorf("space-git: Put: %w: %q", datatransport.ErrUnsafeLocator, locator)
	}
	inSpace := make(map[string][]byte, len(files))
	for relative, raw := range files {
		if !isCleanRelativePath(relative) {
			return nil, fmt.Errorf("space-git: Put: %w: entry %q is not a clean relative path", datatransport.ErrUnsafeLocator, relative)
		}
		inSpace[path.Join(locator, relative)] = raw
	}
	return inSpace, nil
}

// Get is part of the datatransport.Driver interface. It reads back through
// ResolveDataPackage (data_resolve.go) — the exact bounded git tree walk
// DeliverDataPackage's own reader already owns — reused, never
// reimplemented, exactly as this file's own doc comment promises. It is
// honest about the visibility asymmetry §0's loop already sequences (the
// 2026-08-13 RETRACTION amendment): ResolveDataPackage reads
// origin/main, which is only what a MERGED delivery pull request produces,
// never a pending branch a caller may have just pushed.
func (d *spaceGitDriver) Get(ctx context.Context, locator string) (map[string][]byte, error) {
	if !d.AcceptsLocator(locator) {
		return nil, fmt.Errorf("space-git: Get: %w: %q", datatransport.ErrUnsafeLocator, locator)
	}
	system, packageID, err := parseDataPackageLocator(locator)
	if err != nil {
		return nil, err
	}
	pkg, err := ResolveDataPackage(ctx, d.mirrorDir, packageID)
	if err != nil {
		return nil, err
	}
	if pkg.System != system {
		return nil, fmt.Errorf("space-git: Get: locator %q names system %q but the package resolves under %q", locator, system, pkg.System)
	}
	out := make(map[string][]byte, len(pkg.Payload)+1)
	out[dataPackageManifestName] = pkg.ManifestRaw
	for relative, raw := range pkg.Payload {
		out[relative] = raw
	}
	return out, nil
}

// parseDataPackageLocator reverses dataPackageDir(system, packageID) —
// "<system>/data/<DP-id>" — the exact grammar Locate (above) produces, so
// Get can recover the (system, packageID) pair ResolveDataPackage needs
// from the locator string alone, mirroring DataPackageForPath's own
// three-segment parse (data_delivery.go) for the identical grammar.
func parseDataPackageLocator(locator string) (system, packageID string, err error) {
	parts := strings.Split(locator, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "data" {
		return "", "", fmt.Errorf("space-git: locator %q is not a data package directory", locator)
	}
	if _, ok := dataPackagePrefixParsed(parts[2]); !ok {
		return "", "", fmt.Errorf("space-git: locator %q does not name a data package", locator)
	}
	return parts[0], parts[2], nil
}
