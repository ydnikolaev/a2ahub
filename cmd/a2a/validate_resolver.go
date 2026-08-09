package main

import (
	"os"
	"path/filepath"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/cli"
	"github.com/ydnikolaev/a2ahub/internal/schema"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/validate"
)

// validate_resolver.go closes P6's REF-018 dormancy gap
// (internal/validate/incompleteness.go's own package-doc Deviation: "the
// lead adds ... a cmd/a2a implementation backed by the local mirror").
// internal/validate defines validate.ParentCriteriaCounter as a
// consumer-side OPTIONAL upgrade to its Resolver seam (seam.go's own doc
// comment licenses exactly this pattern: "a concrete implementation is
// expected to close over its own local cache"); this file is that
// upgrade's ONE shipped implementation.

// mirrorResolverWithCriteria wraps cli.MirrorResolver (P6's shipped
// validate.Resolver) with validate.ParentCriteriaCounter. It EMBEDS the
// pointer rather than delegating each Resolver method individually so
// every optional capability MirrorResolver already satisfies today
// (validate.ThreadResolver's ThreadOf/ThreadExists, the unexported
// skipReporter's Skipped()) is promoted for free — a hand-written
// delegating wrapper would silently drop whichever of those a future
// MirrorResolver method addition grows, the same class of defect
// US-2 (adapters.go's own ViolationError doc comment) exists to prevent
// for a different capability.
type mirrorResolverWithCriteria struct {
	*cli.MirrorResolver
	mirrorDir string
}

// newMirrorResolverWithCriteria constructs the production
// validate.Resolver both write paths (runSubmit, resolveLifecycleDepsWithPolicy)
// wire: the same mirrorDir/manifest cli.NewMirrorResolver already took,
// plus mirrorDir kept here for AcceptanceCriteriaCount's own on-disk read.
func newMirrorResolverWithCriteria(mirrorDir string, manifest space.Manifest) *mirrorResolverWithCriteria {
	return &mirrorResolverWithCriteria{
		MirrorResolver: cli.NewMirrorResolver(mirrorDir, manifest),
		mirrorDir:      mirrorDir,
	}
}

var _ validate.Resolver = (*mirrorResolverWithCriteria)(nil)
var _ validate.ParentCriteriaCounter = (*mirrorResolverWithCriteria)(nil)

// AcceptanceCriteriaCount implements validate.ParentCriteriaCounter: it
// locates parentID's own <id>.md file in the connected space's mirror
// clone (findMirrorArtifactPath's bounded walk, mirroring
// mirrorHoldsArtifact's own shape in wire.go) and counts its declared
// `acceptance_criteria[]` entries via the SAME parse/decode pair
// readEnvelopeFacts already uses (artifact.ParseFrontmatter ->
// schema.DecodeYAMLInstance), never a second frontmatter parser.
//
// ok=false covers every "cannot count" case alike — parentID not found on
// disk, the file failing to parse/decode, or `acceptance_criteria` absent
// from its frontmatter — deliberately never degrading to (0, true) for an
// absent field: that would make REF-018's caller (checkUnmetIndexRange)
// treat "this parent kind carries no acceptance_criteria[] at all" the
// same as "this parent declares zero criteria", firing REF-018 against
// any unmet[] entry on a parent kind that never had the field to begin
// with — a verdict change ParentCriteriaCounter's own seam.go-licensed
// contract (see incompleteness.go's AcceptanceCriteriaCount doc comment:
// "ok=false ... is deliberately NOT a violation here") does not permit
// this wiring pass to introduce.
func (r *mirrorResolverWithCriteria) AcceptanceCriteriaCount(parentID string) (count int, ok bool) {
	path, found := findMirrorArtifactPath(r.mirrorDir, parentID)
	if !found {
		return 0, false
	}
	raw, err := os.ReadFile(path) //nolint:gosec // reason: path was located by walking r.mirrorDir itself (findMirrorArtifactPath), not taken directly from caller input — same class as readEnvelopeFacts' own G304 suppression in this package
	if err != nil {
		return 0, false
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return 0, false
	}
	inst, err := schema.DecodeYAMLInstance(fm.YAML)
	if err != nil {
		return 0, false
	}
	m, ok := inst.(map[string]any)
	if !ok {
		return 0, false
	}
	criteria, ok := m["acceptance_criteria"].([]any)
	if !ok {
		return 0, false
	}
	return len(criteria), true
}

// findMirrorArtifactPath returns the on-disk path to id's own <id>.md file
// within mirrorDir, and whether it was found — mirrorHoldsArtifact's own
// walk (wire.go), returning the matched path instead of only its boolean,
// since AcceptanceCriteriaCount needs to read the file, not merely know
// it exists.
func findMirrorArtifactPath(mirrorDir, id string) (path string, found bool) {
	_ = filepath.WalkDir(mirrorDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if found {
			return filepath.SkipAll
		}
		// Skip the bare `.git` object store — it never holds artifact files
		// and walking it wastes work that grows with history (matches
		// mirrorHoldsArtifact's own guard).
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == id+".md" {
			path, found = p, true
		}
		return nil
	})
	return path, found
}
