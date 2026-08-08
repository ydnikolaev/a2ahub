package cache

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ydnikolaev/a2ahub/internal/artifact"
	"github.com/ydnikolaev/a2ahub/internal/avatar"
	"github.com/ydnikolaev/a2ahub/internal/fold"
	"github.com/ydnikolaev/a2ahub/internal/host"
	"github.com/ydnikolaev/a2ahub/internal/release"
	"github.com/ydnikolaev/a2ahub/internal/space"
	"gopkg.in/yaml.v3"
)

// SpaceMirror is one connected space's local read surface: its mirror
// clone's working directory (already cloned/fetched via
// space.CloneOrFetch) and its structurally-parsed manifest. cmd/a2a
// (lead, post-wave) builds one of these per space.ProjectConfig.Spaces
// entry.
type SpaceMirror struct {
	SpaceID string
	Dir     string
	RepoURL string
	// Credential authenticates THIS space's mirror refresh. It is per-space
	// because credentials are per-space (A2A_TOKEN_<SPACE_ID>, or this
	// machine's config reference for the id), and the read path had no way to
	// carry one until a private space made that fatal. Empty means "refresh
	// tokenless", which every public space needs and gets.
	//
	// It is a resolved secret, so it inherits host.Credential's rules: never
	// logged, never persisted, never placed in anything that serializes —
	// which is why SpaceMirror itself carries no struct tags.
	Credential host.Credential
	Manifest   space.Manifest
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
	spacesMu  sync.RWMutex
	spaces    []SpaceMirror
	now       Clock
	ttl       time.Duration

	// cloneOrFetch is refresh.go's injected mirror-refresh primitive
	// (SyncIfStale's own read of it — the M1 stale-read fix, AC-1050).
	// NewStore defaults it to space.CloneOrFetch; SetCloneOrFetchForTest
	// overrides it (same test-only DI convention as
	// feedback.Submitter.SetCloneOrFetchForTest /
	// cli.SyncCommand's own cloneOrFetch field).
	cloneOrFetch func(ctx context.Context, dir, repoURL string, credential host.Credential) error

	// syncTotalBudget and syncPerMirrorTimeout are refresh.go's two wall-clock
	// bounds. NewStore leaves them zero and refresh.go reads them through
	// accessors that fall back to the package constants, so production
	// behaviour is byte-identical and every existing caller is untouched.
	//
	// They exist because the tests that prove the BUDGET RULE had to spend the
	// budget to prove it. TestSyncIfStale_BudgetExhaustion_NamesUnattemptedMirrors
	// asserts a scheduling fact — the third mirror is never attempted and is
	// named with ErrSyncBudgetExhausted — and burned 13s of real wall clock
	// reaching it, because 10s+5s were compiled in. The assertion is about the
	// rule, not about the number; injecting the number keeps the rule and
	// stops paying for it.
	syncTotalBudget      time.Duration
	syncPerMirrorTimeout time.Duration

	// update.go's T3/T4 fields — all zero-value until EnableUpdateNotice
	// is called (never by NewStore itself, so every existing caller's
	// Statusline output stays byte-unchanged until the lead wires the
	// setter at a call site).
	updateEnabled       bool
	updateBinaryVersion string
	updateCachePath     string
	updateDetailPath    string
	updateRepo          string
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
	return &Store{
		ownSystem: ownSystem, cacheDir: cacheDir, spaces: spaces, now: now, ttl: ttl,
		cloneOrFetch: space.CloneOrFetch,
	}
}

// SetCloneOrFetchForTest overrides the injected mirror-refresh seam
// (test-only DI, rails anti-pattern #10 convention — same shape as
// feedback.Submitter.SetCloneOrFetchForTest). Production always uses
// NewStore's own space.CloneOrFetch default; refresh_test.go uses this to
// inject a call-counting / failing fake without a real network fetch.
func (s *Store) SetCloneOrFetchForTest(f func(ctx context.Context, dir, repoURL string, credential host.Credential) error) {
	s.cloneOrFetch = f
}

func (s *Store) cursorPath() string { return filepath.Join(s.cacheDir, "cursor.json") }

// OwnSystem returns this project's configured own system id (the dashboard's
// ego node / --system default).
func (s *Store) OwnSystem() string { return s.ownSystem }

// SpaceMirrors returns a read-only view of the connected space mirrors (id,
// dir, repo URL, manifest) — the dashboard reads nodes (participants), the
// connected-repo list, and each mirror's dir (to walk consumes.yaml) from here
// rather than replicating wire.go's buildStore wiring. The returned slice is a
// stable snapshot; callers must not mutate manifests' nested values.
func (s *Store) SpaceMirrors() []SpaceMirror { return s.spaceMirrorsSnapshot() }

// spaceMirrorsSnapshot returns a stable copy of the connected-space slice.
// SyncIfStale replaces a mirror's Manifest after fetching; readers must not
// retain or range over s.spaces while that replacement is in progress.
// Manifest's nested slices are safe to share because Store never mutates a
// published manifest in place: refresh installs a newly parsed value.
func (s *Store) spaceMirrorsSnapshot() []SpaceMirror {
	s.spacesMu.RLock()
	defer s.spacesMu.RUnlock()
	return append([]SpaceMirror(nil), s.spaces...)
}

// ClassificationSummaries walks the same decoded artifact corpus as the read
// index and returns policy-free facts for each connected space. It deliberately
// does not call doctor or infer repository visibility.
func (s *Store) ClassificationSummaries(ctx context.Context) ([]ClassificationSummary, error) {
	spaces := s.spaceMirrorsSnapshot()
	out := make([]ClassificationSummary, 0, len(spaces))
	for _, sm := range spaces {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		artifacts, skipped, err := walkArtifacts(sm.Dir)
		if err != nil {
			return nil, fmt.Errorf("cache: classification summary %s: %w", sm.SpaceID, err)
		}
		summary := ClassificationSummary{
			Space: sm.SpaceID, DecodedCount: len(artifacts), SkippedCount: len(skipped),
			Complete: len(skipped) == 0, Skipped: append([]SkippedFile(nil), skipped...),
		}
		highestRank := 0
		for _, artifact := range artifacts {
			classification := artifact.Env.Classification
			if classification == "" {
				classification = "internal"
			}
			rank, known := classificationRank(classification)
			if !known {
				summary.Complete = false
				summary.IncompletePaths = append(summary.IncompletePaths, artifact.RelPath)
				continue
			}
			if summary.Highest == "" || rank > highestRank {
				summary.Highest, highestRank = classification, rank
			}
		}
		summary.IncompleteCount = len(summary.IncompletePaths)
		sort.Strings(summary.IncompletePaths)
		out = append(out, summary)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Space < out[j].Space })
	return out, nil
}

func classificationRank(classification string) (int, bool) {
	switch classification {
	case "public":
		return 0, true
	case "internal":
		return 1, true
	case "restricted":
		return 2, true
	default:
		return 0, false
	}
}

// AvatarDataURL reads one validated, project-local avatar cache record. It is
// intentionally a Store capability so HTML does not learn the cache layout;
// the false result is a normal monogram fallback, never a render failure.
func (s *Store) AvatarDataURL(login string) (string, bool) {
	return avatar.DataURL(s.cacheDir, login)
}

// SpaceSyncFacts returns one cache-owned mirror snapshot fact per connected
// space, in the same order as SpaceMirrors. It keeps exact HEAD revision,
// mirrorSyncAge and the TTL in their owner instead of making the HTML layer
// inspect .git metadata or duplicate stale policy.
func (s *Store) SpaceSyncFacts(ctx context.Context) []SpaceSyncInfo {
	spaces := s.spaceMirrorsSnapshot()
	out := make([]SpaceSyncInfo, 0, len(spaces))
	now := s.now()
	for _, sm := range spaces {
		age, synced := mirrorSyncAge(now, sm.Dir)
		revision, _ := runGitOutput(ctx, sm.Dir, "rev-parse", "--verify", "HEAD")
		out = append(out, SpaceSyncInfo{
			Space: sm.SpaceID, Revision: strings.TrimSpace(revision),
			Age: age, Synced: synced, Stale: !synced || age > s.ttl,
		})
	}
	return out
}

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
// cache already holds. StatuslineRefreshNeeded reads checker presence and
// cache freshness, while the CLI-owned detached `a2a sync` invokes the
// canonical checker out of process.
func (s *Store) EnableUpdateNotice(binaryVersion, cachePath string, ttl time.Duration, checker func(context.Context)) {
	if ttl <= 0 {
		ttl = DefaultUpdateCheckTTL
	}
	s.updateEnabled = true
	s.updateBinaryVersion = binaryVersion
	s.updateCachePath = cachePath
	s.updateDetailPath = filepath.Join(filepath.Dir(cachePath), "release-detail.json")
	s.updateRepo = release.DefaultUpdateRepo
	s.updateTTL = ttl
	s.updateChecker = checker
}

// index composes every connected space's read-model (buildIndex) — the
// one place every verb below funnels through. The second return value is
// each space's SkippedFile report (see skipped.go) — a space-level fact,
// never merged into foldedArtifact, because a skip's whole point is that
// no per-item foldedArtifact exists for it.
func (s *Store) index(ctx context.Context) (map[string][]foldedArtifact, map[string][]SkippedFile, error) {
	spaces := s.spaceMirrorsSnapshot()
	out := make(map[string][]foldedArtifact, len(spaces))
	skips := make(map[string][]SkippedFile, len(spaces))
	for _, sm := range spaces {
		fa, sk, err := buildIndex(ctx, sm.SpaceID, sm.Dir, s.ownSystem, sm.Manifest)
		if err != nil {
			return nil, nil, fmt.Errorf("cache: Store.index: space %s: %w", sm.SpaceID, err)
		}
		out[sm.SpaceID] = fa
		skips[sm.SpaceID] = sk
	}
	return out, skips, nil
}

// spaceSyncStale reports whether spaceID's mirror sync-age exceeds the
// Store's TTL (never-synced counts as stale).
func (s *Store) spaceSyncStale(spaceID string) bool {
	for _, sm := range s.spaceMirrorsSnapshot() {
		if sm.SpaceID == spaceID {
			age, synced := mirrorSyncAge(s.now(), sm.Dir)
			return !synced || age > s.ttl
		}
	}
	return true
}

func (s *Store) slaFor(spaceID string) time.Duration {
	for _, sm := range s.spaceMirrorsSnapshot() {
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
// this package's own naming choice, see v1-min spec 07 §11).
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
		CreatedAt: parseTimeField(fa.Env.Created), CreatedSeq: fa.Seq, CreatedOrderKnown: fa.OrderKnown,
		LatestEventAt: fa.LatestEventAt, LatestEventSeq: fa.LatestEventSeq, LatestEventID: fa.LatestEventID,
		Description: bodySummary(fa.Raw, 240),
		Outcome:     fold.OutcomeOf(fa.kind(), fa.Result.State),
		Terminal:    fold.Terminal(fa.kind(), fa.Result.State),
		StateSince:  fa.StateSince, StateBy: fa.StateBy, StateEvent: fa.StateEventID,
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
	return s.inbox(ctx, actionableOnly, true, false, false)
}

// InboxSnapshot computes the same read model as Inbox without advancing the
// human read cursor. Passive presentation surfaces such as local HTML use
// this method; OP-207's explicit inbox read remains the only cursor writer.
func (s *Store) InboxSnapshot(ctx context.Context, actionableOnly bool) ([]Item, error) {
	return s.inbox(ctx, actionableOnly, false, true, false)
}

// OpenInboxSnapshot returns the passive inbox projection used by surfaces
// that promise to show exchanges still in progress. The CLI's ordinary inbox
// deliberately includes every artifact addressed to this system, including
// terminal history; presentation code must not silently relabel that broader
// query as "in flight".
func (s *Store) OpenInboxSnapshot(ctx context.Context) ([]Item, error) {
	return s.inbox(ctx, false, false, true, true)
}

// ExchangeArchiveSnapshot returns every terminal artifact that belongs to the
// current system's exchange: documents it authored or documents addressed to
// it. Active Inbox/Outbox keep their operational meaning, while this explicit
// archive makes the durable Git record discoverable instead of letting a
// completed conversation appear to vanish from the dashboard.
func (s *Store) ExchangeArchiveSnapshot(ctx context.Context) ([]Item, error) {
	idx, _, err := s.index(ctx)
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
		manifest := s.manifestFor(spaceID)
		for _, fa := range artifacts {
			if exchangeActive(fa, s.ownSystem, manifest) {
				continue
			}
			if !ownedByMe(fa, s.ownSystem) && !addressedToMe(fa, s.ownSystem) {
				continue
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			_, seen := prior.Items[fa.Env.ID]
			item.New = !seen
			out = append(out, item)
		}
	}
	sortItems(out)
	return out, nil
}

func (s *Store) inbox(ctx context.Context, actionableOnly, advance, annotate, exchangeActiveOnly bool) ([]Item, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return nil, err
	}

	var out []Item
	for spaceID, artifacts := range idx {
		manifest := s.manifestFor(spaceID)
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		var yourMove map[string]bool
		if annotate {
			yourMove = yourMoveByArtifact(artifacts, s.manifestFor(spaceID), s.ownSystem)
		}
		for _, fa := range artifacts {
			if exchangeActiveOnly && !exchangeActive(fa, s.ownSystem, manifest) {
				continue
			}
			// The verdict is computed for EVERY item now, not only when a
			// caller asked to filter or annotate. It is a pure table lookup
			// over facts already in hand, and it is the one answer all four
			// surfaces have to agree on — computing it conditionally is how
			// `a2a inbox --json` came to omit fields the dashboard showed
			// for the same artifact.
			reasons, verdict := actionableReasons(fa, s.ownSystem, manifest)
			switch {
			case actionableOnly:
				if len(reasons) == 0 {
					continue
				}
			case !addressedToMe(fa, s.ownSystem):
				continue
			case !annotate:
				// Reasons are an --actionable/annotate concern; the verdict
				// itself stays, because every surface needs it.
				reasons = nil
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			item.WaitingOn = verdict.Owners
			item.ExpectedTransition = verdict.Expected
			item.Why = verdict.Why
			if annotate {
				item.YourMove = yourMove[fa.Env.ID]
			}
			item.Reasons = reasons
			_, seen := prior.Items[fa.Env.ID]
			item.New = !seen
			out = append(out, item)
		}
	}
	sortItems(out)

	if advance {
		if err := s.advanceCursor(idx); err != nil {
			return nil, err
		}
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
	return s.outbox(ctx, attentionOnly, false, false)
}

// OutboxSnapshot returns every own open item without consuming cursor state,
// annotated with all current attention reasons and canonical move ownership
// for passive presentation surfaces such as the local dashboard.
func (s *Store) OutboxSnapshot(ctx context.Context) ([]Item, error) {
	return s.outbox(ctx, false, true, true)
}

func (s *Store) outbox(ctx context.Context, attentionOnly, annotate, exchangeActiveOnly bool) ([]Item, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	prior, err := loadCursor(s.cursorPath())
	if err != nil {
		return nil, err
	}

	var out []Item
	for spaceID, artifacts := range idx {
		manifest := s.manifestFor(spaceID)
		stale := s.spaceSyncStale(spaceID)
		markers, _ := ReadMarkers(s.cacheDir, spaceID)
		pending := markerSet(markers)
		sla := s.slaFor(spaceID)
		var yourMove map[string]bool
		if annotate {
			yourMove = yourMoveByArtifact(artifacts, s.manifestFor(spaceID), s.ownSystem)
		}
		for _, fa := range artifacts {
			if !ownedByMe(fa, s.ownSystem) {
				continue
			}
			if exchangeActiveOnly && !exchangeActive(fa, s.ownSystem, manifest) {
				continue
			}
			var reasons []string
			if attentionOnly || annotate {
				reasons = attentionReasons(fa, prior, s.now(), sla)
				if attentionOnly && len(reasons) == 0 {
					continue
				}
			}
			if !attentionOnly && !isOpen(fa.kind(), fa.Result.State) {
				continue
			}
			item := toItem(fa, stale, pending[fa.Env.ID])
			if annotate {
				item.YourMove = yourMove[fa.Env.ID]
			}
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
	idx, _, err := s.index(ctx)
	if err != nil {
		return ShowResult{}, err
	}
	return s.showFromIndex(idx, ref)
}

// ShowMany resolves several artifact detail records from one composed cache
// index. Dashboard assembly uses this instead of calling Show in a loop, which
// would re-walk and re-fold every connected mirror once per visible row.
// Results preserve refs order; callers should pass space-qualified refs when
// the source record already carries a space.
func (s *Store) ShowMany(ctx context.Context, refs []string) ([]ShowResult, error) {
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ShowResult, 0, len(refs))
	for _, ref := range refs {
		result, showErr := s.showFromIndex(idx, ref)
		if showErr != nil {
			return nil, showErr
		}
		out = append(out, result)
	}
	return out, nil
}

func (s *Store) showFromIndex(idx map[string][]foldedArtifact, ref string) (ShowResult, error) {
	id, _, _ := splitRef(ref)
	spaceID := ""
	if i := strings.IndexByte(id, ':'); i >= 0 {
		spaceID, id = id[:i], id[i+1:]
	}

	spaceIDs := make([]string, 0, len(idx))
	for candidate := range idx {
		if spaceID == "" || candidate == spaceID {
			spaceIDs = append(spaceIDs, candidate)
		}
	}
	sort.Strings(spaceIDs)
	for _, candidate := range spaceIDs {
		artifacts := idx[candidate]
		for _, fa := range artifacts {
			if fa.Env.ID == id {
				return s.buildShowResult(fa, candidate, artifacts), nil
			}
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
	envelope := map[string]any{}
	_ = yaml.Unmarshal(fm.YAML, &envelope) // ParseFrontmatter already proved valid YAML.

	events := make([]EventSummary, 0, len(fa.Events))
	for _, e := range fa.Events {
		evidence := fa.EventEvidence[e.ULID]
		events = append(events, EventSummary{
			ULID: e.ULID, Subject: e.Subject, Transition: e.Transition,
			ClaimedState: string(e.ClaimedState), ActorKind: evidence.Actor.Kind,
			Actor: evidence.Actor.Name, ActorSystem: evidence.Actor.System,
			ActorModel: evidence.Actor.Model, ActorSession: evidence.Actor.Session,
			ProducedBy: evidence.Producer, Consistency: receiptMismatchFor(fa, e.ULID), At: fa.EventAt[e.ULID],
			Note: fa.EventNotes[e.ULID], ReasonCode: fa.EventReasonCodes[e.ULID],
			TransitionFree: fold.TransitionFree(fa.kind(), e.Transition),
		})
	}
	sort.Slice(events, func(i, j int) bool {
		if !events[i].At.Equal(events[j].At) {
			return events[i].At.Before(events[j].At)
		}
		return events[i].ULID < events[j].ULID
	})

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
		Space: spaceID, Path: fa.RelPath, ID: fa.Env.ID, Type: fa.Env.Type, Title: fa.Env.Title,
		From: fa.Env.From, To: normalizeTo(fa.Env.To), State: string(fa.Result.State),
		Body: string(fm.Body), Thread: fa.Env.Thread, Digest: fa.Digest, Events: events, Flags: flags, Refs: refs,
		SyncStale: syncStale, SyncAge: age.String(), Envelope: envelope,
	}
}

func yourMoveByArtifact(artifacts []foldedArtifact, manifest space.Manifest, ownSystem string) map[string]bool {
	byID := make(map[string]foldedArtifact, len(artifacts))
	for _, fa := range artifacts {
		byID[fa.Env.ID] = fa
	}
	out := make(map[string]bool, len(artifacts))
	for _, item := range buildOpenItems(artifacts, byID, manifest, ownSystem) {
		out[item.ID] = item.YourMove
	}
	return out
}

func (s *Store) mirrorDirFor(spaceID string) string {
	for _, sm := range s.spaceMirrorsSnapshot() {
		if sm.SpaceID == spaceID {
			return sm.Dir
		}
	}
	return ""
}

// manifestFor returns spaceID's structurally-parsed manifest — threadview.go's
// own open-items computation needs the participant list as its candidate-
// system set (spec 46 §T3's documented "legalRole also takes a membership
// status, and open_items passes a constant" V1 imprecision).
func (s *Store) manifestFor(spaceID string) space.Manifest {
	for _, sm := range s.spaceMirrorsSnapshot() {
		if sm.SpaceID == spaceID {
			return sm.Manifest
		}
	}
	return space.Manifest{}
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
// ordered by creation (earliest first). Within one readable space, Git commit
// sequence is the primary truth; the declared creation timestamp is the
// explicit degradation for legacy cross-space or unreadable-history calls.
func (s *Store) Thread(ctx context.Context, threadID string) ([]Item, error) {
	idx, _, err := s.index(ctx)
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
	useCommittedOrder := len(matched) > 0
	committedSpace := ""
	for _, fa := range matched {
		if !fa.OrderKnown || (committedSpace != "" && fa.SpaceID != committedSpace) {
			useCommittedOrder = false
			break
		}
		committedSpace = fa.SpaceID
	}
	sort.Slice(matched, func(i, j int) bool {
		if useCommittedOrder && matched[i].Seq != matched[j].Seq {
			return matched[i].Seq < matched[j].Seq
		}
		at, bt := parseTimeField(matched[i].Env.Created), parseTimeField(matched[j].Env.Created)
		if !at.Equal(bt) {
			return at.Before(bt)
		}
		if !useCommittedOrder {
			if matched[i].SpaceID != matched[j].SpaceID {
				return matched[i].SpaceID < matched[j].SpaceID
			}
		}
		return matched[i].Env.ID < matched[j].Env.ID
	})

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
	idx, _, err := s.index(ctx)
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
			if filters.Thread != "" && filters.Thread != fa.Env.Thread {
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
	idx, _, err := s.index(ctx)
	if err != nil {
		return nil, err
	}
	out := []ContractInfo{}
	for spaceID, artifacts := range idx {
		deprecations := contractDeprecationIndex(artifacts)
		for _, fa := range artifacts {
			if fa.kind() != fold.KindContract {
				continue
			}
			if provider != "" && fa.Env.From != provider {
				continue
			}
			versions := contractVersionWindow(fa.Result)
			attachContractDeprecations(fa.Env.ID, versions, deprecations)
			out = append(out, ContractInfo{
				Space: spaceID, ID: fa.Env.ID, Provider: fa.Env.From,
				Version: fa.LatestPublishVersion, State: string(fa.Result.State),
				Description: bodySummary(fa.Raw, 240),
				Versions:    versions,
				Category:    fa.Env.Category, SchemaFormat: fa.Env.SchemaFormat,
				CompatPolicy:  fa.Env.CompatPolicy,
				GeneratedTool: fa.Env.GeneratedFrom.Tool,
				SourceDigest:  fa.Env.GeneratedFrom.SourceDigest,
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
