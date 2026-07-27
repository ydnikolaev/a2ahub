package cache

// resolver_index.go exports the resolver-facing counterpart of
// buildIndex's fold path: an index builder over walkArtifacts (this file's
// own best-effort walk, complete with the []SkippedFile report) for
// external validate.Resolver/ThreadResolver implementations —
// internal/cli's and internal/mcp's own MirrorResolver types — that need
// exactly the facts walkArtifacts already decodes and nothing more (spec
// agent-ops-2026-07/specs/01-resolver-one-home.md).
//
// Before this, both internal/cli/adapters.go and internal/mcp/adapters.go
// carried their OWN copy of this walk: no []SkippedFile report (a decode
// failure vanished with no trace, the exact defect skipped.go's own doc
// comment describes for the read model), and neither excluded vendored/ or
// .git the way walkArtifacts always has — so a ref into a vendored mirror
// resolved for `validate` while `a2a show` (which reads through
// internal/cache) could never display it. Exporting THIS walk closes both
// gaps in one place instead of two, worse, unreported copies.

// ArtifactIndexEntry is one artifact's resolver-relevant facts, as decoded
// by walkArtifacts' own best-effort stage chain: the space-relative path
// (KnownArtifact/Digest's data source) and the `thread` frontmatter field
// (ThreadOf/ThreadExists' data source) — the same two facts
// internal/cli's and internal/mcp's own former mirrorArtifact types each
// carried, now sourced from ONE walk rather than two independent ones.
// Digest is walkArtifacts' own decodeArtifactFile-computed digest as of
// THIS walk — see BuildArtifactIndex's own doc comment for what that means
// for a caller across a long-lived resolver's lifetime.
type ArtifactIndexEntry struct {
	Path   string
	Thread string
	Digest string
}

// BuildArtifactIndex walks dir (a mirror clone's working tree, or any
// directory laid out the same way) via walkArtifacts and returns id ->
// ArtifactIndexEntry plus the same []SkippedFile report SkippedFiles/
// AllSkippedFiles already expose for the read model — one bad file is
// dropped, never silently, and never blinds the walk to every other
// artifact in the tree (walkArtifacts' own doc comment).
//
// Digest is captured AT WALK TIME (from the same bounded read
// decodeArtifactFile already performed to decode the envelope), not
// re-read per lookup — a deliberate simplification over the two former
// adapter-local walks, which re-read the file from disk on every Digest()
// call after building their own id/path/thread index once. A resolver's
// observed lifetime is one CLI/MCP invocation with no concurrent mutation
// of the mirror clone expected mid-process, so the two never actually
// differed in practice; this trades a second full-file read per Digest()
// call for a single walk-time read, and is reported as this phase's own
// deviation rather than assumed harmless.
//
// An id colliding across two files (should never happen in a well-formed
// mirror — §3.3's id scheme is meant to be unique) keeps whichever
// walkArtifacts visited last, the same last-write-wins behavior the two
// former adapter-local walks already had (neither guarded against it
// either).
func BuildArtifactIndex(dir string) (map[string]ArtifactIndexEntry, []SkippedFile, error) {
	artifacts, skipped, err := walkArtifacts(dir)
	if err != nil {
		return nil, nil, err
	}
	idx := make(map[string]ArtifactIndexEntry, len(artifacts))
	for _, a := range artifacts {
		idx[a.Env.ID] = ArtifactIndexEntry{Path: a.RelPath, Thread: a.Env.Thread, Digest: a.Digest}
	}
	return idx, skipped, nil
}
