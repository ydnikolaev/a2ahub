package cache

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

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

// FormatSkippedList renders a skip report as `path (reason), path (reason)`.
//
// It lives here rather than once per surface for the reason the wave that
// added it was already applying elsewhere: it operates purely on this
// package's own type, both surfaces already import this package, and a
// four-line formatter duplicated across `internal/cli` and `internal/mcp` is
// the same shape of drift — smaller, but the same — as the duplicated walk
// that wave existed to delete. The FRAMING sentence around this list stays
// per-surface and deliberately differs: a read verb reporting its own item
// list and a refusal explaining why a good ref failed must not send the
// reader to the same place.
func FormatSkippedList(skipped []SkippedFile) string {
	parts := make([]string, 0, len(skipped))
	for _, sk := range skipped {
		parts = append(parts, sk.Path+" ("+sk.Reason+")")
	}
	return strings.Join(parts, ", ")
}

// FlattenSkippedFiles merges an AllSkippedFiles map into one deterministic
// slice: space id order, then each space's own already-sorted path order
// (SkippedFiles' own doc comment). `a2a inbox`, `a2a outbox` and `a2a
// search` are not space-scoped the way `a2a thread` is (AllSkippedFiles'
// own doc comment: "a2a inbox is not space-scoped ... it needs the union
// across every connected mirror"), so their skip report needs one stable
// order across every connected space rather than SkippedFiles' single-space
// slice.
//
// It lives here, beside FormatSkippedList, because BOTH stage-2 surfaces
// need it and neither may import the other (ADR-001 as re-scoped by
// ADR-004). ADR-019 half 1 makes that placement the default rather than the
// exception: internal/cli and internal/mcp each shipped a byte-identical
// private copy, each one's doc comment citing the other's by name to
// explain the duplication — the exact shape ADR-019's detection half
// refuses. FormatSkippedList had already taken this route for the same pair
// of callers; this function simply follows the precedent already in this
// file instead of inventing a second shape.
func FlattenSkippedFiles(bySpace map[string][]SkippedFile) []SkippedFile {
	spaceIDs := make([]string, 0, len(bySpace))
	for id := range bySpace {
		spaceIDs = append(spaceIDs, id)
	}
	sort.Strings(spaceIDs)
	var out []SkippedFile
	for _, id := range spaceIDs {
		out = append(out, bySpace[id]...)
	}
	return out
}

// SkippedFilesUnavailableNextStep is the action a reader can take when the
// skip report ITSELF could not be produced. It is deliberately about what
// the READER should do with the output already in front of them, not about
// repairing the store: the verb has already returned its rows, and the only
// thing the reader can act on immediately is whether to trust that list.
//
// One home, for the ADR-019 reason FlattenSkippedFiles states above — this
// string shipped verbatim in both stage-2 surfaces, and a next step that
// names a command (`a2a doctor`) is exactly the vocabulary that must not
// drift between the two surfaces a caller might reach it through.
const SkippedFilesUnavailableNextStep = "treat this listing as possibly incomplete; run `a2a doctor` to see whether the mirror is readable, then re-run this command"

// SkippedFilesUnavailableAttempted names the action that failed, in the
// form a three-part refusal's first part takes: what was being attempted
// when the skip report could not be computed.
func SkippedFilesUnavailableAttempted(verb string) string {
	return verb + ": could not determine which files, if any, were skipped"
}

// SkippedFilesUnavailableMessage composes the whole advisory: what was
// attempted, what was found, and what to do next. A symptom with no action
// is the defect the four per-verb copies this replaced actually shipped
// ("could not determine which files, if any, were skipped: %v" and nothing
// more).
//
// Two surfaces render it through different machinery — internal/cli builds
// it with its own NewRefusal (which exists to make an empty next step
// impossible to construct, and stays in that package by the decision
// recorded in refusal_state.go's header), internal/mcp carries it as a
// validate.Violation because its transport has no stderr channel. The
// SENTENCE is the same on both, and cli's TestSkipAdvisoryUnavailableMatchesTheSharedMessage
// pins it to this function so the two renderings cannot drift apart in
// silence.
func SkippedFilesUnavailableMessage(verb string, err error) string {
	return fmt.Sprintf("%s: %v — %s", SkippedFilesUnavailableAttempted(verb), err, SkippedFilesUnavailableNextStep)
}
