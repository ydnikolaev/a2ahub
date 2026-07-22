package cache

import (
	"bytes"
	"context"
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
)

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
	SpaceID       string
	RelPath       string
	Raw           []byte
	Digest        string
	Env           envelopeProbe
	Result        fold.Result
	Events        []fold.Event
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
}

func (f foldedArtifact) kind() fold.Kind { return fold.Kind(f.Env.Type) }

// buildIndex composes spaceID's full read-model: every artifact under
// dir's working tree, folded against its correctly-gathered event set
// (plan 07 Placement decision: parent events PLUS the events attached via
// that parent's own respond events — never a naive subject==id-only
// query, which silently misses verify/dispute).
func buildIndex(ctx context.Context, spaceID, dir string, manifest space.Manifest) ([]foldedArtifact, error) {
	artifacts, err := walkArtifacts(dir)
	if err != nil {
		return nil, fmt.Errorf("cache: buildIndex(%s): walk artifacts: %w", spaceID, err)
	}
	events, err := walkEvents(dir)
	if err != nil {
		return nil, fmt.Errorf("cache: buildIndex(%s): walk events: %w", spaceID, err)
	}
	seq, err := commitOrder(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("cache: buildIndex(%s): commit order: %w", spaceID, err)
	}
	for i := range events {
		events[i].CommitSeq = seq[events[i].RelPath]
	}

	membership := membershipView(manifest)

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
	// — see this phase's Deviations report.
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
		for _, re := range events {
			if re.Ev.Subject != a.Env.ID {
				continue
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
			EventAt: eventAt, LatestPublishVersion: latestPublishVersion,
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

	return out, nil
}

// gatherEvents assembles the FULL event set fold.Fold needs to compute
// id's correct Result: every event whose subject IS id (primary-scoped,
// including the respond event that seeds Result.Responses), PLUS every
// event whose subject is a response id known (via parentOf) to be
// attached to id — the verify/dispute events D-024's closure model
// requires fold to apply against the SAME running Result as the parent's
// own primary-scoped events (plan 07 Placement decision: "a naive
// subject==X-only query silently misses them").
func gatherEvents(id string, parentOf map[string]string, eventsBySubject map[string][]fold.Event) []fold.Event {
	out := append([]fold.Event(nil), eventsBySubject[id]...)
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
// simplification vs. "as of the event's own commit"; see this phase's
// Deviations report).
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
// file that fails to parse (not frontmatter-shaped, or its YAML block
// carries no `id`) is silently skipped, never fails the whole walk
// (mirrors internal/cli's MirrorResolver.ensureIndex convention).
func walkArtifacts(dir string) ([]rawArtifact, error) {
	var out []rawArtifact
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
		raw, rerr := readBounded(path, maxCacheReadBytes)
		if rerr != nil {
			return nil //nolint:nilerr // reason: best-effort walk — an unreadable file is silently skipped, not fatal (see func doc)
		}
		fm, ferr := artifact.ParseFrontmatter(raw)
		if ferr != nil {
			return nil //nolint:nilerr // reason: best-effort walk — a non-envelope file is silently skipped, not fatal (see func doc)
		}
		env, everr := decodeEnvelope(fm.YAML)
		if everr != nil || env.ID == "" {
			return nil //nolint:nilerr // reason: best-effort walk — an undecodable envelope is silently skipped, not fatal (see func doc)
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil //nolint:nilerr // reason: best-effort walk — an unrelativizable path is silently skipped, not fatal (see func doc)
		}
		out = append(out, rawArtifact{RelPath: filepath.ToSlash(rel), Raw: raw, Env: env, Digest: artifact.Digest(raw)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// walkEvents walks dir for every committed event/v1 YAML file under any
// system's events/ directory (best-effort skip on decode failure, same
// convention as walkArtifacts).
func walkEvents(dir string) ([]rawEvent, error) {
	var out []rawEvent
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
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
			return nil //nolint:nilerr // reason: best-effort walk — an unrelativizable path is silently skipped, not fatal (see func doc)
		}
		relSlash := filepath.ToSlash(rel)
		if !strings.Contains(relSlash, "/events/") {
			return nil
		}
		raw, rerr := readBounded(path, maxCacheReadBytes)
		if rerr != nil {
			return nil //nolint:nilerr // reason: best-effort walk — an unreadable file is silently skipped, not fatal (see func doc)
		}
		ev, everr := decodeEvent(raw)
		if everr != nil || ev.Event == "" {
			return nil //nolint:nilerr // reason: best-effort walk — an undecodable event is silently skipped, not fatal (see func doc)
		}
		out = append(out, rawEvent{RelPath: relSlash, Ev: ev})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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
