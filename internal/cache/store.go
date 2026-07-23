package cache

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// SpaceMirror is one connected space's local read surface: its mirror
// clone's working directory (already cloned/fetched via
// space.CloneOrFetch) and its structurally-parsed manifest. cmd/a2a
// (lead, post-wave) builds one of these per space.ProjectConfig.Spaces
// entry.
type SpaceMirror struct {
	SpaceID  string
	Dir      string
	RepoURL  string
	Manifest space.Manifest
}

// Store is this package's facade: every P7 read verb (inbox/outbox/
// show/thread/search/contracts/statusline) is a thin call into one of
// its methods. It composes internal/fold over every connected space's
// mirror on each call — no long-lived in-memory index survives between
// calls (D-001: nothing this package cannot rebuild from git + the
// on-disk cursor/marker files is kept anywhere).
type Store struct {
	ownSystem string
	cacheDir  string
	spaces    []SpaceMirror
	now       Clock
	ttl       time.Duration

	// update.go's T3/T4 fields — all zero-value until EnableUpdateNotice
	// is called (never by NewStore itself, so every existing caller's
	// Statusline output stays byte-unchanged until the lead wires the
	// setter at a call site).
	updateEnabled       bool
	updateBinaryVersion string
	updateCachePath     string
	updateTTL           time.Duration
	updateChecker       func(context.Context)
}

// NewStore constructs a Store. ownSystem is this project's configured
// own system id (§7.4); cacheDir is `.a2a/cache/`'s path (cursor +
// pending-marker files live under it — never spaces' mirror working
// trees, which cacheDir's own "mirrors/" sibling subdir already owns per
// space.ResolveMirrorLocation's default). now must not be nil (rails
// anti-pattern #10). ttl is the statusline refresh TTL (§7.5); zero
// defaults to DefaultStatuslineTTL.
func NewStore(ownSystem, cacheDir string, spaces []SpaceMirror, now Clock, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultStatuslineTTL
	}
	return &Store{ownSystem: ownSystem, cacheDir: cacheDir, spaces: spaces, now: now, ttl: ttl}
}

func (s *Store) cursorPath() string { return filepath.Join(s.cacheDir, "cursor.json") }

// OwnSystem returns this project's configured own system id (the dashboard's
// ego node / --system default).
func (s *Store) OwnSystem() string { return s.ownSystem }

// SpaceMirrors returns a read-only view of the connected space mirrors (id,
// dir, repo URL, manifest) — the dashboard reads nodes (participants), the
// connected-repo list, and each mirror's dir (to walk consumes.yaml) from here
// rather than replicating wire.go's buildStore wiring. The slice is the Store's
// own (callers must not mutate it).
func (s *Store) SpaceMirrors() []SpaceMirror { return s.spaces }

// EnableUpdateNotice turns on the T3/T4 update-notice mechanism on an
// already-constructed Store: a post-construction setter, deliberately NOT a
// NewStore parameter, so every existing call site's behavior (and
// Statusline's byte output) is unaffected until a caller opts in.
// binaryVersion is this build's bare current version (release.Resolve's
// "current"); cachePath is the T3 machine-level update-check cache file
// (release.CachePath()); ttl is the cache's freshness window (<=0 defaults
// to DefaultUpdateCheckTTL); checker is the background refresh function
// (typically release.NewChecker's return value) triggerUpdateRefreshIfStale
// spawns detached when the cache is stale — nil disables the refresh
// trigger while still allowing UpdateNotice to render from whatever the
// cache already holds.
func (s *Store) EnableUpdateNotice(binaryVersion, cachePath string, ttl time.Duration, checker func(context.Context)) {
	if ttl <= 0 {
		ttl = DefaultUpdateCheckTTL
	}
	s.updateEnabled = true
	s.updateBinaryVersion = binaryVersion
	s.updateCachePath = cachePath
	s.updateTTL = ttl
	s.updateChecker = checker
}

// index composes every connected space's read-model (buildIndex) — the
// one place every verb below funnels through.
func (s *Store) index(ctx context.Context) (map[string][]foldedArtifact, error) {
	out := make(map[string][]foldedArtifact, len(s.spaces))
	for _, sm := range s.spaces {
		fa, err := buildIndex(ctx, sm.SpaceID, sm.Dir, sm.Manifest)
		if err != nil {
			return nil, fmt.Errorf("cache: Store.index: space %s: %w", sm.SpaceID, err)
		}
		out[sm.SpaceID] = fa
	}
	return out, nil
}

// spaceSyncStale reports whether spaceID's mirror sync-age exceeds the
// Store's TTL (never-synced counts as stale).
func (s *Store) spaceSyncStale(spaceID string) bool {
	for _, sm := range s.spaces {
		if sm.SpaceID == spaceID {
			age, synced := mirrorSyncAge(s.now(), sm.Dir)
			return !synced || age > s.ttl
		}
	}
	return true
}

func (s *Store) slaFor(spaceID string) time.Duration {
	for _, sm := range s.spaces {
		if sm.SpaceID == spaceID {
			return slaFromManifest(sm.Manifest)
		}
	}
	return DefaultStalenessSLA
}

// slaFromManifest reads space.yaml's OPTIONAL `staleness_sla_days`
// extension field (OP-208: "space.yaml, default 7 days") straight from
// Manifest.Raw — the schema's own additionalProperties:true permissive
// typing (Open Q1, no normative field name exists yet for this override;
// this package's own naming choice, see Deviations report).
func slaFromManifest(m space.Manifest) time.Duration {
	if len(m.Raw) == 0 {
		return DefaultStalenessSLA
	}
	var probe struct {
		StalenessSLADays int `yaml:"staleness_sla_days"`
	}
	if err := yaml.Unmarshal(m.Raw, &probe); err != nil || probe.StalenessSLADays <= 0 {
		return DefaultStalenessSLA
	}
	return time.Duration(probe.StalenessSLADays) * 24 * time.Hour
}

func toItem(fa foldedArtifact, syncStale, pendingMerge bool) Item {
	return Item{
		Space: fa.SpaceID, ID: fa.Env.ID, Type: fa.Env.Type, Title: fa.Env.Title,
		From: fa.Env.From, To: normalizeTo(fa.Env.To), State: string(fa.Result.State),
		Priority: fa.Env.Priority, Blocking: fa.Env.Blocking, NeededBy: fa.Env.NeededBy,
		Thread: fa.Env.Thread, PendingMerge: pendingMerge, SyncStale: syncStale,
		LatestEventAt: fa.LatestEventAt, Description: bodySummary(fa.Raw, 240),
	}
}

// bodySummary extracts a short human-readable description from an artifact's
// body (the markdown after its frontmatter): the first non-empty paragraph,
// whitespace-collapsed, a leading markdown heading/quote marker trimmed, capped
// at max runes. Returns "" for a missing/empty body or an unparseable file
// (degrade, never fail the caller). Used for the dashboard's item + contract
// descriptions (D-001/D-002).
func bodySummary(raw []byte, max int) string {
	if max < 1 {
		max = 1
	}
	fm, err := artifact.ParseFrontmatter(raw)
	if err != nil {
		return ""
	}
	body := strings.ReplaceAll(string(fm.Body), "\r\n", "\n") // CRLF → LF so the split matches
	s := strings.TrimSpace(body)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[:i] // first paragraph only
	}
	s = trimLeadingMarker(s)
	s = strings.Join(strings.Fields(s), " ") // collapse internal whitespace + newlines
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max-1])) + "…"
	}
	return s
}

// trimLeadingMarker drops ONE leading markdown block marker — an ATX heading
// (`#`+), a blockquote (`>`), or a list bullet (`-`/`*`/`+`) — but only when it
// is a real marker, i.e. followed by a space. So prose like "-50°C is cold" or
// "**bold**" keeps its leading character (the old blanket TrimLeft mangled it).
func trimLeadingMarker(s string) string {
	t := strings.TrimLeft(s, " \t")
	if h := strings.TrimLeft(t, "#"); h != t && strings.HasPrefix(h, " ") {
		return strings.TrimLeft(h, " ") // heading: '#'+ then space
	}
	for _, m := range []string{"> ", "- ", "* ", "+ "} {
		if strings.HasPrefix(t, m) {
			return strings.TrimLeft(t[len(m):], " ")
		}
	}
	return s
}

func sortItems(items []Item) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Space != items[j].Space {
			return items[i].Space < items[j].Space
		}
		return items[i].ID < items[j].ID
	})
}

// Inbox computes `a2a inbox` (OP-207): actionableOnly=false is the base
// §4.2 "to includes me" query; actionableOnly=true applies the
// normative 5-condition `--actionable` union instead (a different,
// broader query — see actionableReasons' doc comment). Every call
// advances the per-system read cursor (spec 07 "what to do" #2: "inbox
// ... advances the read cursor on run" — unqualified by the flag).
func (s *Store) Inbox(ctx context.Context, actionableOnly bool) ([]Item, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return nil, err
	}

	var out []Item
	for spaceID, artifacts := range idx {
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		for _, fa := range artifacts {
			var reasons []string
			if actionableOnly {
				reasons = actionableReasons(fa, s.ownSystem)
				if len(reasons) == 0 {
					continue
				}
			} else if !addressedToMe(fa, s.ownSystem) {
				continue
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			item.Reasons = reasons
			_, seen := prior.Items[fa.Env.ID]
			item.New = !seen
			out = append(out, item)
		}
	}
	sortItems(out)

	if err := s.advanceCursor(idx); err != nil {
		return nil, err
	}
	return out, nil
}

// advanceCursor snapshots every known item's CURRENT folded state across
// every connected space — the comprehensive baseline both `a2a inbox`'s
// own "new" computation and `a2a outbox`'s "state changed since read
// cursor" condition compare against.
func (s *Store) advanceCursor(idx map[string][]foldedArtifact) error {
	snap := cursorSnapshot{AdvancedAt: s.now(), Items: map[string]string{}}
	for _, artifacts := range idx {
		for _, fa := range artifacts {
			snap.Items[fa.Env.ID] = string(fa.Result.State)
		}
	}
	return saveCursor(s.cursorPath(), snap)
}

// Outbox computes `a2a outbox` (OP-208): attentionOnly=false lists every
// own OPEN item; attentionOnly=true applies the normative 4-condition
// `--attention` union. Outbox never advances the read cursor itself
// (only `a2a inbox` does, per OP-207's own wording) — it reads whatever
// cursor snapshot the last inbox run left behind.
func (s *Store) Outbox(ctx context.Context, attentionOnly bool) ([]Item, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return nil, err
	}

	var out []Item
	for spaceID, artifacts := range idx {
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		sla := s.slaFor(spaceID)
		for _, fa := range artifacts {
			if !ownedByMe(fa, s.ownSystem) {
				continue
			}
			var reasons []string
			if attentionOnly {
				reasons = attentionReasons(fa, prior, s.now(), sla)
				if len(reasons) == 0 {
					continue
				}
			} else if !isOpen(fa.kind(), fa.Result.State) {
				continue
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			item.Reasons = reasons
			out = append(out, item)
		}
	}
	sortItems(out)
	return out, nil
}

// ErrNotFound is returned by Show when ref does not resolve to any
// artifact in any connected space.
var ErrNotFound = fmt.Errorf("cache: artifact not found")

// Show computes `a2a show <ref>` (OP-209): ref is a §5.7 ref grammar
// string (`id`, `id@version`, `id#digest`, or a `space:id...` cross-
// space form — only the bare `id` segment resolves the target here;
// version/digest are compared against, not used to select, since D-023
// resolves versions through publish events, and this package's own
// per-artifact history is already available via Result/Events).
func (s *Store) Show(ctx context.Context, ref string) (ShowResult, error) {
	id, _, _ := splitRef(ref)
	if i := strings.IndexByte(id, ':'); i >= 0 {
		id = id[i+1:] // cross-space "space:id" form — id segment only
	}

	idx, err := s.index(ctx)
	if err != nil {
		return ShowResult{}, err
	}
	for spaceID, artifacts := range idx {
		for _, fa := range artifacts {
			if fa.Env.ID != id {
				continue
			}
			return s.buildShowResult(fa, spaceID, artifacts), nil
		}
	}
	return ShowResult{}, fmt.Errorf("%w: %q", ErrNotFound, ref)
}

func splitRef(ref string) (id, version, digest string) {
	rest := ref
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		digest = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		version = rest[i+1:]
		rest = rest[:i]
	}
	return rest, version, digest
}

func (s *Store) buildShowResult(fa foldedArtifact, spaceID string, all []foldedArtifact) ShowResult {
	fm, _ := artifact.ParseFrontmatter(fa.Raw)

	events := make([]EventSummary, 0, len(fa.Events))
	for _, e := range fa.Events {
		events = append(events, EventSummary{
			ULID: e.ULID, Subject: e.Subject, Transition: e.Transition,
			ClaimedState: string(e.ClaimedState), Actor: e.Actor.Name, ActorSystem: e.Actor.System, At: fa.EventAt[e.ULID],
		})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })

	var flags []string
	for _, f := range fa.Result.Flags {
		flags = append(flags, string(f.Kind))
	}

	byID := make(map[string]foldedArtifact, len(all))
	for _, a := range all {
		byID[a.Env.ID] = a
	}
	var refs []RefFact
	for _, r := range fa.Env.Refs {
		refs = append(refs, resolveRefFact(r, byID))
	}

	age, synced := mirrorSyncAge(s.now(), s.mirrorDirFor(spaceID))
	syncStale := !synced || age > s.ttl

	return ShowResult{
		Space: spaceID, ID: fa.Env.ID, Type: fa.Env.Type, Title: fa.Env.Title,
		From: fa.Env.From, To: normalizeTo(fa.Env.To), State: string(fa.Result.State),
		Body: string(fm.Body), Digest: fa.Digest, Events: events, Flags: flags, Refs: refs,
		SyncStale: syncStale, SyncAge: age.String(),
	}
}

func (s *Store) mirrorDirFor(spaceID string) string {
	for _, sm := range s.spaces {
		if sm.SpaceID == spaceID {
			return sm.Dir
		}
	}
	return ""
}

func resolveRefFact(r refEntry, byID map[string]foldedArtifact) RefFact {
	id, version, digest := splitRef(r.Ref)
	out := RefFact{Ref: r.Ref, ID: id, Version: version, PinnedDigest: digest}
	target, ok := byID[id]
	if !ok {
		return out
	}
	out.Resolved = true
	out.ResolvedDigest = target.Digest
	if digest != "" && digest != target.Digest {
		out.DigestMismatch = true
	}
	return out
}

// Thread computes `a2a thread <thread-id>` (OP-210): every artifact
// across every connected space whose envelope `thread` field matches,
// ordered by creation (earliest first) via the same commit-order fold
// already resolved (LatestEventAt is used as the ordering proxy — the
// entry event's own timestamp).
func (s *Store) Thread(ctx context.Context, threadID string) ([]Item, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	var matched []foldedArtifact
	for _, artifacts := range idx {
		for _, fa := range artifacts {
			if fa.Env.Thread == threadID {
				matched = append(matched, fa)
			}
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].LatestEventAt.Before(matched[j].LatestEventAt) })

	out := make([]Item, 0, len(matched))
	for _, fa := range matched {
		out = append(out, toItem(fa, s.spaceSyncStale(fa.SpaceID), false))
	}
	return out, nil
}

// Search computes `a2a search <query> [--type --space --state]` (OP-221
// first clause): a case-insensitive substring match over id/title/body,
// hub-less, local-cache only. Zero hits returns an empty (non-nil)
// slice, never an error (spec §6: "empty result, not error").
func (s *Store) Search(ctx context.Context, query string, filters SearchFilters) ([]Item, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	out := []Item{}
	for spaceID, artifacts := range idx {
		if filters.Space != "" && filters.Space != spaceID {
			continue
		}
		for _, fa := range artifacts {
			if filters.Type != "" && filters.Type != fa.Env.Type {
				continue
			}
			if filters.State != "" && filters.State != string(fa.Result.State) {
				continue
			}
			fm, _ := artifact.ParseFrontmatter(fa.Raw)
			hay := strings.ToLower(fa.Env.ID + " " + fa.Env.Title + " " + string(fm.Body))
			if q != "" && !strings.Contains(hay, q) {
				continue
			}
			out = append(out, toItem(fa, s.spaceSyncStale(spaceID), false))
		}
	}
	sortItems(out)
	return out, nil
}

// Contracts computes `a2a contracts [--provider <sys>]` (OP-221 second
// clause): every KindContract artifact known to the local cache, its
// provider (envelope `from`), latest recorded `publish` event's version
// (D-023), and its folded state.
func (s *Store) Contracts(ctx context.Context, provider string) ([]ContractInfo, error) {
	idx, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	out := []ContractInfo{}
	for spaceID, artifacts := range idx {
		for _, fa := range artifacts {
			if fa.kind() != fold.KindContract {
				continue
			}
			if provider != "" && fa.Env.From != provider {
				continue
			}
			out = append(out, ContractInfo{
				Space: spaceID, ID: fa.Env.ID, Provider: fa.Env.From,
				Version: fa.LatestPublishVersion, State: string(fa.Result.State),
				Description: bodySummary(fa.Raw, 240),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Space != out[j].Space {
			return out[i].Space < out[j].Space
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
