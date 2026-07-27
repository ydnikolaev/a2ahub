package cache

import "context"

// SkippedFile is one mirror file (artifact or event) that walkArtifacts or
// walkEvents could not fold into a space's read index — the read-model's
// walk stays best-effort (one bad file must never blind the whole space to
// every other artifact in it), but the file it drops is reported here
// rather than vanishing without a trace. Path is space-relative (git-style
// forward slashes, like rawArtifact.RelPath/rawEvent.RelPath); Reason is
// one of the fixed SkipReason* vocabulary below — never a wrapped Go error
// string, because the reason is read by agents and a message whose exact
// text depends on the yaml library's version is not a stable contract.
type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// The SkipReason* vocabulary names which stage of the best-effort decode
// rejected a file — fixed, short, stable strings, because stage 2
// (internal/cli, internal/mcp) matches on these verbatim rather than
// pattern-matching a free-text error message.
const (
	// SkipReasonUnreadable: the file could not be opened/read at all
	// (os.Open/io.ReadAll failure via readBounded), or filepath.WalkDir
	// itself reported a traversal error for that path (e.g. a directory it
	// could not list on the way there).
	SkipReasonUnreadable = "unreadable"

	// SkipReasonNotFrontmatterShaped: the file's bytes do not contain the
	// well-formed `---\n<yaml>\n---\n` delimiter pair at all
	// (artifact.ErrNoFrontmatter) — not an envelope document, structurally.
	// Artifact-only; walkEvents never produces this reason (event/v1 files
	// carry no frontmatter delimiters to be missing).
	SkipReasonNotFrontmatterShaped = "not-frontmatter-shaped"

	// SkipReasonUndecodableYAML: the block that should decode as YAML does
	// not. Two distinct causes collapse into this one reason, deliberately:
	// (1) artifact.ParseFrontmatter's own syntax probe rejected the
	// delimited block (artifact.ErrMalformedFrontmatter) — e.g. a duplicate
	// mapping key, the exact defect filed 2026-07-26 (a doubled `thread:`
	// line made yaml.Unmarshal reject the whole envelope); or (2) the block
	// parsed as generic YAML but does not fit this package's typed
	// envelope/event probe (decodeEnvelope/decodeEvent). Both are "the YAML
	// here doesn't decode" from a reader's point of view — splitting them
	// by which internal call happened to reject it would leak an
	// implementation detail into a reason string agents key off of.
	SkipReasonUndecodableYAML = "undecodable-yaml"

	// SkipReasonNoID: the document decoded but carries no `id` (artifact)
	// or no `event` (event) — the field every downstream computation in
	// this package requires.
	SkipReasonNoID = "no-id"

	// SkipReasonUnrelativizable: filepath.Rel(dir, path) itself failed, so
	// no space-relative path could be computed for the file at all — Path
	// carries the raw path this package saw instead (never empty).
	SkipReasonUnrelativizable = "unrelativizable-path"
)

// SkippedFiles reports, for one connected space, every mirror file this
// package's best-effort walk could not fold into that space's read index —
// sorted by path (deterministic, because this feeds golden CLI/MCP output).
// An unknown spaceID or a space with nothing skipped both return an empty
// slice, never an error.
//
// This accessor — and AllSkippedFiles below — exist because of a filed
// defect: walkArtifacts/walkEvents were already best-effort BY DESIGN (one
// malformed file must never blind the whole space to every other artifact
// in it), but their silence made a malformed artifact indistinguishable
// from one that simply does not exist. A `thread:` key written twice in one
// envelope made yaml.Unmarshal reject it; `search`, `inbox`, `statusline`
// and `thread` all returned nothing for it, with no error anywhere, while
// `a2a show` (which parses frontmatter directly, outside this package)
// still printed it fine. In a shared cross-company record, a counterparty's
// document dropping out of your index without a word is worse than a hard
// error — the report, not the skip, was the missing half. This package
// keeps no long-lived index (D-001), so this recomputes fresh from the
// mirror on every call, same as every other Store method.
func (s *Store) SkippedFiles(ctx context.Context, spaceID string) ([]SkippedFile, error) {
	_, skips, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	return skips[spaceID], nil
}

// AllSkippedFiles is SkippedFiles for every connected space at once (space
// id -> its sorted skipped files) — `a2a inbox` is not space-scoped the way
// `a2a thread` is, so it needs the union across every connected mirror
// rather than one space's slice. See SkippedFiles' doc comment for why this
// pair of accessors exists at all.
func (s *Store) AllSkippedFiles(ctx context.Context) (map[string][]SkippedFile, error) {
	_, skips, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	return skips, nil
}
