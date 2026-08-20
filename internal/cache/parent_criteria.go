package cache

// parent_criteria.go — rules-that-reach-2026-08 P5: the parent-criteria
// resolution both internal/cli's and internal/mcp's own MirrorResolver
// types need to answer validate.ParentCriteriaCounter,
// validate.ParentCriteriaIDs and validate.ResponseParentResolver, MOVED
// here per ADR-004 (docs/decisions.md) — the mirror walk already moved
// DOWN into internal/cache rather than being shared ACROSS the two
// surfaces, both surfaces already import this package, and porting this
// ~198-line read/parse/decode chain a second time into
// internal/mcp/adapters.go would have been the SIXTH instance of the
// cli/mcp duplication class the ADR-001 DESIGN row tracks (B16, B22, B46,
// P1's floor split, this).
//
// What moved is the SUBSTANCE of internal/cli/adapters.go's former
// AcceptanceCriteriaCount/AcceptanceCriteriaIDs/ParentOf methods (P6 wave
// C, defects-fix-2026-08 P4) — each function below carries that method's
// own doc-comment contract forward, unabridged, because those contracts
// are exactly what a move risks losing: which id a violation must name,
// and what "cannot check" (ok=false) means as distinct from a synthesized
// zero-count or empty-id-list standing in for "check passed".
//
// What did NOT move: each surface's own MirrorResolver TYPE, its index
// build (ensureIndex, memoized via sync.Once) and BuildArtifactIndex
// itself (resolver_index.go — already here, unchanged). Every function
// below takes dir and an already-built index as plain parameters rather
// than owning either — it resolves ONE artifact's own path from an index
// its caller already has, then re-reads and decodes that single file one
// field deeper than ArtifactIndexEntry carries (Path/Thread/Digest only;
// no full envelope, no acceptance_criteria, no parent).
//
// internal/cli and internal/mcp both already import internal/cache
// (ADR-001's own table); neither imports the other, before or after this
// file exists — ADR-004's re-scoped rule ("no cli<->mcp sharing; a shared
// concern moves DOWN into a core package instead") is preserved, not
// bent. Each surface's MirrorResolver calls these three functions as thin
// delegations, passing its own already-built r.mirrorDir/r.index — see
// internal/cli/adapters.go's and internal/mcp/adapters.go's own
// AcceptanceCriteriaCount/AcceptanceCriteriaIDs/ParentOf methods.

import (
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/schema"
)

// AcceptanceCriteriaCount reports how many `acceptance_criteria[]` entries
// parentID's own frontmatter declares — validate.ParentCriteriaCounter's
// own capability (P6's REF-018 rule, internal/validate/incompleteness.go),
// now answerable by both surfaces via this ONE implementation.
//
// It resolves parentID's on-disk path from index (a caller's own
// already-built BuildArtifactIndex result, resolver_index.go) rather than
// walking dir a second time — TestMirrorResolverAdapterCarriesNoWalk (both
// surfaces' own adapters_test.go) guards each surface's adapter file
// against ever regaining its own directory walk (AC-1.3, spec
// 01-resolver-one-home.md). The file is then re-read and parsed for
// `acceptance_criteria[]` specifically, via the SAME
// artifact.ParseFrontmatter -> schema.DecodeYAMLInstance pair
// internal/cli's own package already used for this — never a second
// frontmatter parser — walkArtifacts' own decode discards this field
// (ArtifactIndexEntry carries only Path/Thread/Digest), so it cannot be
// read off the index alone.
//
// ok=false covers every "cannot count" case alike — parentID absent from
// index, its file failing to re-read/parse/decode, or `acceptance_criteria`
// absent from its frontmatter — deliberately never degrading to (0, true)
// for an absent field: that would make REF-018's caller
// (checkUnmetIndexRange) treat "this parent kind carries no
// acceptance_criteria[] at all" the same as "this parent declares zero
// criteria", firing REF-018 against any unmet[] entry on a parent kind
// that never had the field to begin with — a verdict change
// ParentCriteriaCounter's own doc comment (incompleteness.go) does not
// permit.
//
// Deviation from the CLI method this replaced: the file re-read below uses
// this package's own bounded readBounded (mirror.go, maxCacheReadBytes)
// rather than a bare os.ReadFile — the former adapter-local methods read
// unbounded. walkArtifacts already bounded-reads every file it indexes, so
// a file too large to satisfy that bound was never in index to begin with;
// this only makes the re-read consistent with the bound its own entry
// already passed once.
func AcceptanceCriteriaCount(dir string, index map[string]ArtifactIndexEntry, parentID string) (count int, ok bool) {
	entry, found := index[parentID]
	if !found {
		return 0, false
	}
	raw, err := readBounded(filepath.Join(dir, filepath.FromSlash(entry.Path)), maxCacheReadBytes)
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

// AcceptanceCriteriaIDs implements validate.ParentCriteriaIDs
// (defects-fix-2026-08 P4). It is the SAME read as AcceptanceCriteriaCount
// above, one field deeper: the ids an id-addressed parent declares, so
// REF-019's range check and REF-023's completeness rule can resolve a
// `criterion:`-keyed verdict entry to a position.
//
// ok=false means the parent declares no ids — a plain-string
// acceptance_criteria array, an unresolvable parent, or no criteria at all.
// The consumer degrades to the ordinal path rather than refusing, which is
// resolveParentCriteriaIDs' own documented contract (internal/validate/
// verdicts.go).
func AcceptanceCriteriaIDs(dir string, index map[string]ArtifactIndexEntry, parentID string) (ids []string, ok bool) {
	entry, found := index[parentID]
	if !found {
		return nil, false
	}
	raw, err := readBounded(filepath.Join(dir, filepath.FromSlash(entry.Path)), maxCacheReadBytes)
	if err != nil {
		return nil, false
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return nil, false
	}
	inst, err := schema.DecodeYAMLInstance(fm.YAML)
	if err != nil {
		return nil, false
	}
	m, ok := inst.(map[string]any)
	if !ok {
		return nil, false
	}
	criteria, ok := m["acceptance_criteria"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(criteria))
	for _, c := range criteria {
		obj, isObj := c.(map[string]any)
		if !isObj {
			// The array is HOMOGENEOUS by schema (P3): one plain string means
			// this is an ordinal-addressed parent, so it declares no ids at
			// all rather than a partial set.
			return nil, false
		}
		id, isStr := obj["id"].(string)
		if !isStr || id == "" {
			return nil, false
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// ParentOf implements validate.ResponseParentResolver (P6 wave C's REF-019,
// internal/validate/verdicts.go): it reports a RESPONSE artifact's own
// `parent` field, so checkVerdictIndexRange can hop from a `verify` event's
// subject (the response id) to the criteria-bearing artifact
// AcceptanceCriteriaCount actually knows how to count.
//
// Same shape as AcceptanceCriteriaCount just above: resolve responseID's
// on-disk path from index (no second walk), then re-read and parse for
// `parent` specifically — walkArtifacts' own decode discards it
// (ArtifactIndexEntry carries only Path/Thread/Digest). Deliberately reads
// via a direct yaml.Unmarshal into a one-field probe rather than
// schema.DecodeYAMLInstance — the same decode path internal/cli's own
// former method used for this single string field, kept as-is here rather
// than unified with AcceptanceCriteriaCount/AcceptanceCriteriaIDs' own
// decode-to-instance path: that would risk a behaviour change (a
// duplicate-key or otherwise malformed `parent` block tolerated by one
// decoder and rejected by the other) dressed as a refactor, which this
// move's own "byte-identical after the move" constraint forbids.
//
// ok=false covers every "cannot resolve" case alike: responseID absent from
// the index, the file failing to re-read/parse/decode, or `parent` empty or
// absent (a non-response artifact, or a malformed response) — never a
// synthesized parent id.
func ParentOf(dir string, index map[string]ArtifactIndexEntry, responseID string) (parentID string, ok bool) {
	entry, found := index[responseID]
	if !found {
		return "", false
	}
	raw, err := readBounded(filepath.Join(dir, filepath.FromSlash(entry.Path)), maxCacheReadBytes)
	if err != nil {
		return "", false
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return "", false
	}
	var probe struct {
		Parent string `yaml:"parent"`
	}
	if err := yaml.Unmarshal(fm.YAML, &probe); err != nil {
		return "", false
	}
	if probe.Parent == "" {
		return "", false
	}
	return probe.Parent, true
}
