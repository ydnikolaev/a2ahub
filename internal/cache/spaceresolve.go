package cache

// spaceresolve.go is the ONE cross-space id-resolution rule (ADR-019's
// sixth named instance; no-silent-yes-2026-08 P2a): given a project's
// connected spaces and an artifact id, which connected space's mirror
// actually holds it.
//
// Before this file, cmd/a2a/wire.go's resolveTargetSpaceRef and
// internal/mcp/wire.go's SpaceOfArtifacts closure each carried their OWN
// copy of the exact same walk — a filepath.WalkDir loop over each
// connected space's mirror, looking for a file literally named "<id>.md".
// ADR-001 forbids internal/mcp importing internal/cli, and cmd/a2a is a
// main package neither side can import, so the two copies could not share
// code directly with each other. ADR-001's own matrix grants internal/cache
// this question instead: it already answers "which artifact is where" one
// level down (resolver_index.go's BuildArtifactIndex), internal/cache
// already imports internal/space (mirror.go), and internal/space imports
// nothing from internal/cache — so this direction has no import cycle.
//
// ResolveArtifactSpace REPLACES both walks and additionally REFUSES rather
// than silently defaulting: an id held by NO connected space's mirror used
// to make each caller's own copy of this rule fall back to the first
// connected space (cmd/a2a/wire.go's former `return cfg.Spaces[0]`) —
// running a write verb against the wrong space, silently, whenever the
// id actually lived in a DIFFERENT connected space than the first. This
// returns ArtifactSpaceNotFoundError instead (REF-025, schemas/errors/v1/
// registry.yaml), naming the id AND every space actually searched, so a
// caller's refusal says what was actually looked at rather than the bare
// "artifact not found" that used to accompany a wrong-space resolution.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ydnikolaev/a2ahub/internal/space"
)

// ArtifactSpaceNotFoundError is ResolveArtifactSpace's refusal when id is
// not held by any connected space's mirror — REF-025. It carries the id
// AND the ids of every space searched (in refs' own order), so a caller
// can print a refusal that names what was actually looked at.
type ArtifactSpaceNotFoundError struct {
	ID       string
	Searched []string
}

// Error names the id and every space whose mirror was WALKED — deliberately
// not "searched". The distinction is this epic's own subject applied to its
// own refusal: a connected space whose mirror has never been cloned is a
// directory that does not exist, the walk finds nothing there, and the space
// is still listed. Saying "searched" would assert that its contents were
// examined, which is more than the walk established. A caller that reads
// "walked" and finds its id genuinely absent knows to check whether that
// mirror is synced — which "searched" would have told it not to bother with.
func (e *ArtifactSpaceNotFoundError) Error() string {
	return fmt.Sprintf(
		"cache: REF-025: artifact %q is not held by any connected space's mirror; spaces walked (a never-cloned mirror is walked and empty, not searched): %s",
		e.ID, strings.Join(e.Searched, ", "))
}

// ResolveArtifactSpace returns the first ref in refs whose mirror (as
// located by mirrorDir) holds a file literally named "<id>.md" anywhere
// below its root, or a *ArtifactSpaceNotFoundError naming id and every
// searched space's id if none does.
//
// mirrorDir is a closure rather than a (projectRoot, space.MachineConfig)
// pair so this function need not depend on how a caller resolves a mirror
// location: cmd/a2a/wire.go and internal/mcp/wire.go each already own that
// pair and close over space.ResolveMirrorLocation themselves.
//
// TWO id families never match this walk, by construction, and a caller
// should not feed them to it expecting resolution:
//   - a contract id (XC-<slug>): its committed path is
//     <slug>/provides/<name>/contract.md (space.Layout.ProvidesContract),
//     never "<id>.md" — the a2a_contract family (both the CLI and MCP
//     surfaces) passes its own explicit space input instead of deriving
//     one from ids.
//   - a data-package id (DP-<system>-...): its committed path is
//     <system>/data/<DP-id>/manifest.json (internal/space's own
//     dataPackageManifestPath), never "<id>.md" either.
//
// Every OTHER envelope type this rule is meant for (XQ/XW/XH/XA/XS via
// space.Layout.Exchange, XR via Layout.Requires, XD via space.Decision) IS
// committed at exactly "<id>.md", which is what makes the walk correct for
// them and structurally incapable of ever resolving the other two.
func ResolveArtifactSpace(refs []space.Ref, mirrorDir func(space.Ref) string, id string) (space.Ref, error) {
	searched := make([]string, 0, len(refs))
	for _, ref := range refs {
		searched = append(searched, ref.ID)
		if mirrorHoldsArtifact(mirrorDir(ref), id) {
			return ref, nil
		}
	}
	return space.Ref{}, &ArtifactSpaceNotFoundError{ID: id, Searched: searched}
}

// mirrorHoldsArtifact reports whether mirrorDir contains a file named
// "<id>.md" anywhere below its root — the walk both former copies of this
// rule performed, now performed exactly once.
func mirrorHoldsArtifact(mirrorDir, id string) bool {
	var found bool
	_ = filepath.WalkDir(mirrorDir, func(_ string, d os.DirEntry, err error) error {
		if found {
			return filepath.SkipAll
		}
		if err != nil {
			return nil //nolint:nilerr // reason: best-effort walk — an inaccessible entry just is not a match, and this walk discards WalkDir's overall error too (the same grant both former copies carried)
		}
		// Skip the bare `.git` object store — it never holds artifact files,
		// and walking it wastes work that grows with history (matches
		// internal/cache's own walkers, e.g. walkArtifacts).
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
