package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"github.com/ydnikolaev/a2ahub/internal/version"
)

// canonicalEventVersion reformats a committed event's own `version` field
// through internal/version.Canonical, so two spellings of the same version
// ("1.0.0" and "01.0.0") produce the IDENTICAL string once threaded into
// fold.Event.Version — fold.Result.Versions is a map[string]State keyed on
// the raw string with no canonicalization of its own (P4,
// 04-per-version-lifecycle.plan.md; internal/fold is off-limits to this
// phase, so this is the caller's own half of keeping that map's keys
// consistent, at the one place every committed event's version enters
// fold's own input). Fails open (returns v unchanged) on an empty or
// unparseable v — canonicalization is never itself a refusal path; fold's
// own legality checks decide whether an unparseable version is legal.
func canonicalEventVersion(v string) string {
	if v == "" {
		return v
	}
	if c, err := version.Canonical(v); err == nil {
		return c
	}
	return v
}

// maxCacheReadBytes bounds every mirror file read this package performs
// (rails: "bounded reads everywhere").
const maxCacheReadBytes = 1 << 20 // 1 MiB

// rawArtifact is one *.md file found anywhere under a mirror's working
// tree (excluding vendored/ — a read-only mirror of a NON-participant's
// spec, out of this space's own lifecycle-exchange scope, and .git).
type rawArtifact struct {
	RelPath string
	Raw     []byte
	Env     envelopeProbe
	Digest  string
}

// rawEvent is one committed event/v1 YAML file found under any system's
// <system>/events/<year>/ directory.
type rawEvent struct {
	RelPath   string
	Ev        eventProbe
	CommitSeq int64
}

// foldedArtifact is one artifact's fully composed read-model: its
// envelope facts, the correctly-gathered event set (see gatherEvents),
// and the resulting fold.Result — the ONE folded-state computation this
// package ever performs (composed over internal/fold, never
// reimplemented, per spec §5).
type foldedArtifact struct {
	SpaceID string
	RelPath string
	Raw     []byte
	Digest  string
	Env     envelopeProbe
	Result  fold.Result
	Events  []fold.Event
	// EventRefs preserves each committed event's refs[] beside the pure fold
	// input. fold.Event deliberately does not need relationship metadata, but
	// read models do: contract deprecate records its successor on the
	// deprecate event, and the dashboard must not lose that canonical link.
	EventRefs     map[string][]refEntry
	LatestEventAt time.Time
	// EventAt maps a committed event's ULID to its `at` timestamp —
	// fold.Event itself carries none (fold is a pure, timestamp-free
	// package, §T1); this side table is this package's own way of
	// recovering it for show/thread rendering without extending fold's
	// input shape.
	EventAt map[string]time.Time
	// LatestPublishVersion is the most recent `publish` event's `version`
	// field for this artifact (D-023: contract versions resolve through
	// publish events) — empty when none recorded (never published, or a
	// non-contract kind).
	LatestPublishVersion string
	// Seq is this artifact's own file's per-space first-parent commit
	// sequence (commitOrder's own return value, keyed by RelPath) —
	// spec 46 §T3's primary thread-transcript ordering key, alongside
	// fold.Event.CommitSeq (already carried per event). Zero both for "no
	// commit found" (declared-order fallback, see OrderKnown) and for a
	// genuinely first-ever commit — OrderKnown is what disambiguates them.
	Seq int64
	// OrderKnown is true when this space's commit history was readable
	// (commitOrder returned a non-empty map) — every artifact in one
	// space's index shares the same value (space-level fact, carried
	// per-item the same way EventAt already is). False means Seq/CommitSeq
	// are meaningless zero values and a reader must fall back to
	// created/at ordering, reporting that degradation rather than
	// pretending the commit guarantee still holds (§T3 "Degradation is
	// designed, not silent").
	OrderKnown bool

	// DeprecatesMyDependency is P4's Edge 3, evaluated ONCE here rather
	// than at each read site: true when this artifact is a deprecation
	// announcement whose `deprecates:` names a contract listed in THIS
	// system's own consumes.yaml. addressedToMe reads it, so every caller
	// of that predicate — inbox, --actionable's condition 1, statusline,
	// overdue — inherits the rule from one evaluation instead of four
	// copies. This package has paid for one rule read in two places
	// enough times (see broadcastAckPermitted and contractVersionVerdict).
	DeprecatesMyDependency bool
}

func (f foldedArtifact) kind() fold.Kind { return fold.Kind(f.Env.Type) }

// buildIndex composes spaceID's full read-model: every artifact under
// dir's working tree, folded against its correctly-gathered event set
// (plan 07 Placement decision: parent events PLUS the events attached via
// that parent's own respond events — never a naive subject==id-only
// query, which silently misses verify/dispute).
func buildIndex(ctx context.Context, spaceID, dir, ownSystem string, manifest space.Manifest) ([]foldedArtifact, []SkippedFile, error) {
	artifacts, artifactSkips, err := walkArtifacts(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: buildIndex(%s): walk artifacts: %w", spaceID, err)
	}
	events, eventSkips, err := walkEvents(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: buildIndex(%s): walk events: %w", spaceID, err)
	}
	// skips is this space's SPACE-LEVEL fact (parallel to how OrderKnown
	// below is a space-level fact carried per-item): a skip can never be
	// "per-item" the way OrderKnown's per-artifact copy is, because the
	// whole point of a skip is that no folded item exists for it. Merging
	// two independently-sorted slices does not itself produce a sorted
	// slice, so this re-sorts the union rather than assuming order.
	skips := append(append([]SkippedFile{}, artifactSkips...), eventSkips...)
	sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	seq, err := commitOrder(ctx, dir)
	if err != nil {
		return nil, nil, fmt.Errorf("cache: buildIndex(%s): commit order: %w", spaceID, err)
	}
	for i := range events {
		events[i].CommitSeq = seq[events[i].RelPath]
	}
	orderKnown := len(seq) > 0

	membership := membershipView(manifest)

	// myDependencies is Edge 3's own input: the contract ids THIS system
	// declares in its own consumes.yaml. An unreadable or absent registry
	// yields an empty set plus a reported skip, never an error — see
	// myDependencyContracts for why this direction of failure is the
	// opposite of the retire gate's.
	myDependencies, depSkip := myDependencyContracts(dir, ownSystem)
	if depSkip != nil {
		skips = append(skips, *depSkip)
		sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	}

	// parentOf: response artifact ID -> parent artifact ID
	// (response.schema.json's own `parent` field — the schema-grounded
	// fact this package composes over, rather than an invented refs[]
	// convention).
	parentOf := map[string]string{}
	// responsesBySeqAndParent: commit seq -> parent ID -> sorted response
	// IDs committed at that same seq. D-026 ("one commit, one event per
	// artifact") means a respond event on the parent and its paired
	// response artifact land in the SAME commit — that shared commit seq
	// is this package's correlation key (a schema-grounded fact, not an
	// invented convention). A batch submit committing >1 response to the
	// SAME parent in the SAME commit is a genuine ambiguity this
	// resolves best-effort (first response ID, deterministically sorted)
	// — see v1-min spec 07 §11.
	responsesBySeqAndParent := map[int64]map[string][]string{}
	for _, a := range artifacts {
		if fold.Kind(a.Env.Type) == fold.KindResponse && a.Env.Parent != "" {
			parentOf[a.Env.ID] = a.Env.Parent
			s := seq[a.RelPath]
			if responsesBySeqAndParent[s] == nil {
				responsesBySeqAndParent[s] = map[string][]string{}
			}
			responsesBySeqAndParent[s][a.Env.Parent] = append(responsesBySeqAndParent[s][a.Env.Parent], a.Env.ID)
		}
	}
	for _, byParent := range responsesBySeqAndParent {
		for k := range byParent {
			sort.Strings(byParent[k])
		}
	}

	eventsBySubject := map[string][]fold.Event{}
	for _, re := range events {
		fe := fold.Event{
			ULID:         re.Ev.Event,
			CommitSeq:    re.CommitSeq,
			Subject:      re.Ev.Subject,
			Transition:   re.Ev.Transition,
			ClaimedState: fold.State(re.Ev.State),
			Actor:        fold.Actor{Kind: re.Ev.Actor.Kind, Name: re.Ev.Actor.Name, System: re.Ev.Actor.System},
			Version:      canonicalEventVersion(re.Ev.Version),
		}
		if re.Ev.Transition == fold.TRespond {
			if cands, ok := responsesBySeqAndParent[re.CommitSeq][re.Ev.Subject]; ok && len(cands) > 0 {
				fe.ResponseID = cands[0]
				responsesBySeqAndParent[re.CommitSeq][re.Ev.Subject] = cands[1:]
			}
		}
		eventsBySubject[fe.Subject] = append(eventsBySubject[fe.Subject], fe)
	}

	eventAt := make(map[string]time.Time, len(events))
	for _, re := range events {
		if t, terr := time.Parse(time.RFC3339, re.Ev.At); terr == nil {
			eventAt[re.Ev.Event] = t
		}
	}

	out := make([]foldedArtifact, 0, len(artifacts))
	for _, a := range artifacts {
		if a.Env.ID == "" || a.Env.Type == "" {
			continue
		}
		env := fold.Envelope{
			ID:                a.Env.ID,
			Kind:              fold.Kind(a.Env.Type),
			From:              a.Env.From,
			To:                normalizeTo(a.Env.To),
			RequiredApprovers: a.Env.RequiredApprovers,
		}
		evs := gatherEvents(a.Env.ID, parentOf, eventsBySubject)
		result := fold.Fold(env.Kind, env, evs, membership)

		var latest time.Time
		var latestPublishSeq int64 = -1
		var latestPublishVersion string
		eventRefs := map[string][]refEntry{}
		for _, re := range events {
			if re.Ev.Subject != a.Env.ID {
				continue
			}
			if len(re.Ev.Refs) > 0 {
				eventRefs[re.Ev.Event] = append([]refEntry(nil), re.Ev.Refs...)
			}
			if t, terr := time.Parse(time.RFC3339, re.Ev.At); terr == nil && t.After(latest) {
				latest = t
			}
			if re.Ev.Transition == fold.TPublish && re.Ev.Version != "" && re.CommitSeq > latestPublishSeq {
				latestPublishSeq = re.CommitSeq
				latestPublishVersion = re.Ev.Version
			}
		}

		out = append(out, foldedArtifact{
			SpaceID: spaceID, RelPath: a.RelPath, Raw: a.Raw, Digest: a.Digest,
			Env: a.Env, Result: result, Events: evs, LatestEventAt: latest,
			EventAt: eventAt, EventRefs: eventRefs, LatestPublishVersion: latestPublishVersion,
			Seq: seq[a.RelPath], OrderKnown: orderKnown,
			// Edge 3, evaluated once — see foldedArtifact's own comment.
			// The lookup is on the contract id alone; myDependencies is
			// empty for every system that consumes nothing, which makes
			// this false for every artifact, which is exactly today's
			// behaviour for such a system.
			DeprecatesMyDependency: myDependencies[a.Env.deprecatedContractID()],
		})
	}

	// Response closure-state overlay: fold's own model (see
	// applyResponseScoped's doc comment in internal/fold/fold.go) is that
	// a response artifact carries NO separate envelope of its own for
	// verify/dispute authorization purposes — its authoritative
	// submitted/verified/disputed sub-state lives ONLY in its parent's
	// Result.Responses map (keyed by the response's own id), populated by
	// the SAME gather this function already performs for the parent.
	// A response artifact's own independent Fold call above therefore
	// only ever reaches create/submitted (RoleAny rows); this pass
	// overlays the parent's authoritative view onto the response's own
	// displayed State so `a2a show <response-id>` renders "verified"/
	// "disputed" rather than a stale "submitted" — cache's own
	// composition, not a second fold implementation (spec §5).
	byID := make(map[string]int, len(out))
	for i, fa := range out {
		byID[fa.Env.ID] = i
	}
	for respID, parentID := range parentOf {
		pIdx, ok := byID[parentID]
		if !ok {
			continue
		}
		rIdx, ok := byID[respID]
		if !ok {
			continue
		}
		if state, ok := out[pIdx].Result.Responses[respID]; ok {
			out[rIdx].Result.State = state
		}
	}

	return out, skips, nil
}

// gatherEvents assembles the FULL event set fold.Fold needs to compute
// id's correct Result: every event whose subject IS id (primary-scoped,
// including the respond event that seeds Result.Responses), PLUS every
// event whose subject is a response id known (via parentOf) to be
// attached to id — the verify/dispute events D-024's closure model
// requires fold to apply against the SAME running Result as the parent's
// own primary-scoped events (plan 07 Placement decision: "a naive
// subject==X-only query silently misses them").
// A RESPONSE artifact is the one case where "every event whose subject IS
// id" over-collects. Its own verify/dispute events are, by fold's explicit
// model (applyResponseScoped's doc comment), scoped to the PARENT: they
// authorize against the parent's owner and they write the parent's
// Result.Responses map. Handing them to the response's OWN independent fold
// makes that fold resolve RoleOwner against the response's `from` — the
// responder — who is never the party authorized to verify their own answer.
// The result was a spurious `unauthorized-actor` flag on EVERY verified
// response in the product, invisible because the closure-state overlay below
// only overwrites Result.State and never Result.Flags.
//
// That mattered more than a cosmetic wrong flag: spec 46's thread reader
// promises that a conversation carrying an illegal-transition or
// unauthorized-actor flag never renders clean, so a healthy, correctly
// verified exchange would have reported itself as suspect — the
// authoritative-looking wrong answer this phase exists to prevent, produced
// by the phase's own reader.
//
// Found while writing P46's e2e chain fixture, empirically: a two-line
// fold.Fold(KindResponse, respEnv, []Event{verify}) probe reproduces it with
// no cache involved at all.
func gatherEvents(id string, parentOf map[string]string, eventsBySubject map[string][]fold.Event) []fold.Event {
	own := eventsBySubject[id]
	if _, isResponse := parentOf[id]; isResponse {
		filtered := make([]fold.Event, 0, len(own))
		for _, ev := range own {
			if ev.Transition == fold.TVerify || ev.Transition == fold.TDispute {
				continue
			}
			filtered = append(filtered, ev)
		}
		own = filtered
	}
	out := append([]fold.Event(nil), own...)
	for respID, parentID := range parentOf {
		if parentID == id {
			out = append(out, eventsBySubject[respID]...)
		}
	}
	return out
}

// membershipView adapts a space.Manifest's participant list into a
// fold.MembershipView (D-017: membership resolved against the manifest,
// cache reads it once per space rather than per-commit — a known
// simplification vs. "as of the event's own commit"; see v1-min spec 07
// §11).
func membershipView(manifest space.Manifest) fold.MembershipView {
	return func(system string) fold.MembershipStatus {
		for _, p := range manifest.Participants {
			if p.System == system {
				if p.Status == "left" {
					return fold.MembershipLeft
				}
				return fold.MembershipMember
			}
		}
		return fold.MembershipUnknown
	}
}

// walkArtifacts walks dir for every *.md file (excluding .git and
// vendored/), best-effort decoding each as an envelope/v1 document — a
// file that fails to parse is silently skipped from the returned
// []rawArtifact, never fails the whole walk (mirrors internal/cli's
// MirrorResolver.ensureIndex convention), but is reported (never dropped
// without a trace) via the returned []SkippedFile — see skipped.go's own
// doc comment for why that report exists at all.
func walkArtifacts(dir string) ([]rawArtifact, []SkippedFile, error) {
	var out []rawArtifact
	var skips []SkippedFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skips = append(skips, SkippedFile{Path: reportPath(dir, path), Reason: SkipReasonUnreadable})
			return nil //nolint:nilerr // reason: best-effort walk — skip an inaccessible entry, don't abort the whole walk (see func doc)
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendored" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		a, skip := decodeArtifactFile(dir, path)
		if skip != nil {
			skips = append(skips, *skip)
			return nil
		}
		out = append(out, a)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	return out, skips, nil
}

// decodeArtifactFile attempts every stage of one candidate *.md file's
// best-effort decode (path relativization, bounded read, frontmatter
// split, envelope decode, `id` presence) and returns EITHER the
// successfully decoded rawArtifact OR a SkippedFile naming the first stage
// that rejected it — never both. Extracted from walkArtifacts as its own
// function so each stage's failure path is independently testable without
// going through a real filepath.WalkDir traversal.
func decodeArtifactFile(dir, path string) (rawArtifact, *SkippedFile) {
	rel, relErr := filepath.Rel(dir, path)
	if relErr != nil {
		return rawArtifact{}, &SkippedFile{Path: path, Reason: SkipReasonUnrelativizable}
	}
	relSlash := filepath.ToSlash(rel)

	raw, rerr := readBounded(path, maxCacheReadBytes)
	if rerr != nil {
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUnreadable}
	}

	fm, ferr := artifact.ParseFrontmatter(raw)
	if ferr != nil {
		reason := SkipReasonNotFrontmatterShaped
		if errors.Is(ferr, artifact.ErrMalformedFrontmatter) {
			// The delimiter pair IS present and well-formed; it is the YAML
			// inside it that fails to decode (e.g. a duplicate mapping key)
			// — that is an undecodable-YAML fact, not a "not shaped like
			// frontmatter at all" one (see SkipReasonUndecodableYAML's doc).
			reason = SkipReasonUndecodableYAML
		}
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: reason}
	}

	env, everr := decodeEnvelope(fm.YAML)
	if everr != nil {
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUndecodableYAML}
	}
	if env.ID == "" {
		return rawArtifact{}, &SkippedFile{Path: relSlash, Reason: SkipReasonNoID}
	}
	return rawArtifact{RelPath: relSlash, Raw: raw, Env: env, Digest: artifact.Digest(raw)}, nil
}

// walkEvents walks dir for every committed event/v1 YAML file under any
// system's events/ directory (best-effort skip on decode failure, same
// convention — and same skip-reporting — as walkArtifacts).
func walkEvents(dir string) ([]rawEvent, []SkippedFile, error) {
	var out []rawEvent
	var skips []SkippedFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			skips = append(skips, SkippedFile{Path: reportPath(dir, path), Reason: SkipReasonUnreadable})
			return nil //nolint:nilerr // reason: best-effort walk — skip an inaccessible entry, don't abort the whole walk (see func doc)
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			skips = append(skips, SkippedFile{Path: path, Reason: SkipReasonUnrelativizable})
			return nil //nolint:nilerr // reason: best-effort walk — an unrelativizable path is silently skipped (but reported, see skipped.go), not fatal (see func doc)
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.Contains(relSlash, "/events/") {
			return nil
		}
		ev, skip := decodeEventFile(path, relSlash)
		if skip != nil {
			skips = append(skips, *skip)
			return nil
		}
		out = append(out, ev)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(skips, func(i, j int) bool { return skips[i].Path < skips[j].Path })
	return out, skips, nil
}

// decodeEventFile is walkEvents' own per-file stage runner (bounded read,
// event decode, `event` id presence) — same "one function, independently
// testable, no real WalkDir needed" shape as decodeArtifactFile.
func decodeEventFile(path, relSlash string) (rawEvent, *SkippedFile) {
	raw, rerr := readBounded(path, maxCacheReadBytes)
	if rerr != nil {
		return rawEvent{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUnreadable}
	}
	ev, everr := decodeEvent(raw)
	if everr != nil {
		return rawEvent{}, &SkippedFile{Path: relSlash, Reason: SkipReasonUndecodableYAML}
	}
	if ev.Event == "" {
		return rawEvent{}, &SkippedFile{Path: relSlash, Reason: SkipReasonNoID}
	}
	return rawEvent{RelPath: relSlash, Ev: ev}, nil
}

// reportPath best-effort relativizes path against dir for a SkippedFile
// report where the walk has already failed before this package's own
// filepath.Rel-based decode stage would run (the filepath.WalkDir
// traversal-error branch) — falls back to the raw path so the report is
// never simply dropped, even though it may then be absolute rather than
// space-relative like every other SkippedFile.Path.
func reportPath(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// commitOrder recovers D-017's first-parent commit order on `main` for
// every path in dir's history, in exactly ONE git subprocess call (never
// a per-file call — the statusline <100ms budget and every other verb's
// responsiveness depends on this). A path's sequence number is the index
// of the FIRST commit that introduced it (event/artifact files are
// committed exactly once and never modified thereafter, so "first" and
// "only" coincide). An empty/absent history (fresh clone with nothing on
// main yet, or a non-git dir in a test double) returns an empty map
// rather than an error — every event then falls back to ULID-only
// ordering, a documented degradation, not a hard failure.
func commitOrder(ctx context.Context, dir string) (map[string]int64, error) {
	out, err := runGitOutput(ctx, dir, "log", "--first-parent", "--reverse", "--name-only", "--format=%x02%H")
	if err != nil {
		return map[string]int64{}, nil //nolint:nilerr // reason: absent/failed git history degrades to ULID-only ordering by design (see func doc)
	}
	seq := map[string]int64{}
	var idx int64
	for _, chunk := range strings.Split(out, "\x02") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		lines := strings.Split(chunk, "\n")
		for _, p := range lines[1:] {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, exists := seq[p]; !exists {
				seq[p] = idx
			}
		}
		idx++
	}
	return seq, nil
}

// runGitOutput runs `git <args...>` with cwd=dir via explicit argv (never
// sh -c), returning stdout on success — this package's own copy of the
// same minimal git-plumbing helper internal/space/mirror.go keeps
// unexported to that package.
func runGitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cache: git %v: %w: %s", args, err, stderr.String())
	}
	return out.String(), nil
}

// readBounded reads path with a size cap (rails: bounded reads
// everywhere).
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // reason: read-only fd, close error is not actionable here

	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("cache: %s exceeds %d byte read bound", path, max)
	}
	return raw, nil
}
